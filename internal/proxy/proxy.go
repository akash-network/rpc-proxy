package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"math/rand"
	"net/http"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

type ProxyKind uint8

const (
	RPC  ProxyKind = iota
	Rest ProxyKind = iota
)

func New(
	kind ProxyKind,
	ch chan seed.Seed,
	cfg config.Config,
) *Proxy {
	return &Proxy{
		cfg:  cfg,
		ch:   ch,
		kind: kind,
	}
}

type StatusResponse struct {
	Jsonrpc string `json:"jsonrpc"`
	Result  struct {
		NodeInfo struct {
			ID      string `json:"id"`
			Network string `json:"network"`
			Version string `json:"version"`
		} `json:"node_info"`
		SyncInfo struct {
			LatestBlockTime time.Time `json:"latest_block_time"`
			CatchingUp      bool      `json:"catching_up"`
		} `json:"sync_info"`
	} `json:"result"`
}

type RestStatusResponse struct {
	Block struct {
		Header struct {
			ChainID string    `json:"chain_id"`
			Height  string    `json:"height"`
			Time    time.Time `json:"time"`
		} `json:"header"`
	} `json:"block"`
}
type Proxy struct {
	cfg  config.Config
	kind ProxyKind
	init sync.Once
	ch   chan seed.Seed

	round   int
	mu      sync.Mutex
	servers []*Server

	initialized  atomic.Bool
	shuttingDown atomic.Bool
}

func (p *Proxy) Ready() bool { return p.initialized.Load() }
func (p *Proxy) Live() bool  { return !p.shuttingDown.Load() && p.initialized.Load() }

func (p *Proxy) Stats() []ServerStat {
	var result []ServerStat
	for _, s := range p.servers {
		reqCount := s.requestCount.Load()
		result = append(result, ServerStat{
			Name:        s.name,
			URL:         s.url.String(),
			Avg:         s.pings.Last(),
			Degraded:    !s.Healthy(),
			Initialized: reqCount > 0,
			Requests:    reqCount,
			ErrorRate:   s.ErrorRate(),
		})
	}
	sort.Sort(serverStats(result))
	return result
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		slog.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	switch p.kind {
	case RPC:
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rpc")
	case Rest:
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rest")
	}

	if srv := p.next(); srv != nil {
		srv.ServeHTTP(w, r)
		return
	}
	slog.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func checkEndpoint(url string, kind ProxyKind) error {
	switch kind {
	case RPC:
		return checkRPC(url)
	case Rest:
		return checkREST(url)
	default:
		return fmt.Errorf("unsupported proxy kind: %v", kind)
	}
}

func performGetRequest(url string, timeout time.Duration) ([]byte, error) {
	client := &http.Client{Timeout: timeout}
	resp, err := client.Get(url)
	if err != nil {
		return nil, fmt.Errorf("error making request: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status code: %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("error reading response body: %v", err)
	}

	return body, nil
}

func checkRPC(url string) error {
	body, err := performGetRequest(url+"/status", 2*time.Second)
	if err != nil {
		return err
	}

	var status StatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("error unmarshaling JSON: %v", err)
	}

	if status.Result.SyncInfo.CatchingUp {
		return fmt.Errorf("node is still catching up")
	}

	if !status.Result.SyncInfo.LatestBlockTime.After(time.Now().Add(-time.Minute)) {
		return fmt.Errorf("latest block time is more than 1 minute old")
	}

	return nil
}

func checkREST(url string) error {
	body, err := performGetRequest(url+"/blocks/latest", 2*time.Second)
	if err != nil {
		return err
	}

	var status RestStatusResponse
	if err := json.Unmarshal(body, &status); err != nil {
		return fmt.Errorf("error unmarshaling JSON: %v", err)
	}

	if !status.Block.Header.Time.After(time.Now().Add(-time.Minute)) {
		return fmt.Errorf("latest block time is more than 1 minute old")
	}

	return nil
}

func (p *Proxy) next() *Server {
	p.mu.Lock()
	if len(p.servers) == 0 {
		p.mu.Unlock()
		return nil
	}
	server := p.servers[p.round%len(p.servers)]
	p.round++
	p.mu.Unlock()
	if server.Healthy() && server.ErrorRate() <= p.cfg.HealthyErrorRateThreshold {
		return server
	}
	if rand.Intn(99)+1 < p.cfg.UnhealthyServerRecoverChancePct {
		slog.Warn("giving slow server a chance", "name", server.name, "avg", server.pings.Last())
		return server
	}
	slog.Warn("server is too slow, trying next", "name", server.name, "avg", server.pings.Last())
	return p.next()
}

func (p *Proxy) update(seed seed.Seed) {
	var err error
	switch p.kind {
	case RPC:
		err = p.doUpdate(seed.APIs.RPC)
	case Rest:
		err = p.doUpdate(seed.APIs.Rest)
	}
	if err != nil {
		slog.Error("could not update seed", "err", err)
	}
}

func (p *Proxy) doUpdate(providers []seed.Provider) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// add new servers
	for _, provider := range providers {
		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == provider.Provider })
		if idx == -1 {
			srv, err := newServer(
				provider.Provider,
				provider.Address,
				p.cfg,
				p.kind,
			)
			if err != nil {
				return err
			}
			p.servers = append(p.servers, srv)
		}
	}

	// remove deleted servers
	p.servers = slices.DeleteFunc(p.servers, func(srv *Server) bool {
		for _, provider := range providers {
			if provider.Provider == srv.name && srv.healthy.Load() {
				return false
			}
		}
		slog.Info("server was removed from pool", "name", srv.name)
		return true
	})

	slog.Info("updated server list", "total", len(p.servers))
	p.initialized.Store(true)
	return nil
}

func (p *Proxy) Start(ctx context.Context) {
	p.init.Do(func() {
		go func() {
			for {
				select {
				case seed := <-p.ch:
					p.update(seed)
				case <-ctx.Done():
					p.shuttingDown.Store(true)
					return
				}
			}
		}()
	})
}
