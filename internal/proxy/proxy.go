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

func New(cfg config.Config) *Proxy {
	return &Proxy{
		cfg: cfg,
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

type Proxy struct {
	cfg  config.Config
	init sync.Once

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

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rpc")
	if srv := p.next(); srv != nil {
		srv.ServeHTTP(w, r)
		return

	}
	slog.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func checkSingleRPC(url string) error {
    req, err := http.NewRequest("GET", url+"/status", nil)
    if err != nil {
        return fmt.Errorf("error creating request: %v", err)
    }
    client := &http.Client{Timeout: 2 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return fmt.Errorf("error making request: %v", err)
    }
    defer resp.Body.Close()

    if resp.StatusCode != http.StatusOK {
        return fmt.Errorf("unexpected status code: %d", resp.StatusCode)
    }

    body, err := io.ReadAll(resp.Body)
    if err != nil {
        return fmt.Errorf("error reading response body: %v", err)
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

func (p *Proxy) next() *Server {
	p.mu.Lock()
	if len(p.servers) == 0 {
		p.mu.Unlock()
		return nil
	}
	server := p.servers[p.round%len(p.servers)]
	p.round++
	p.mu.Unlock()
	if server.Healthy() {
		return server
	}
	if rand.Intn(99)+1 < p.cfg.UnhealthyServerRecoverChancePct {
		slog.Warn("giving slow server a chance", "name", server.name, "avg", server.pings.Last())
		return server
	}
	slog.Warn("server is too slow, trying next", "name", server.name, "avg", server.pings.Last())
	return p.next()
}

func (p *Proxy) update(rpcs []seed.RPC) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// add new servers
	for _, rpc := range rpcs {
		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == rpc.Provider })
		if idx == -1 {
			srv, err := newServer(
				rpc.Provider,
				rpc.Address,
				p.cfg.HealthyThreshold,
				p.cfg.ProxyRequestTimeout,
				p.cfg.CheckHealthInterval,
			)
			if err != nil {
				return err
			}
			p.servers = append(p.servers, srv)
		}
	}

	// remove deleted servers
	p.servers = slices.DeleteFunc(p.servers, func(srv *Server) bool {
		for _, rpc := range rpcs {
			if rpc.Provider == srv.name && srv.healthy.Load() {
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
			t := time.NewTicker(p.cfg.SeedRefreshInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					p.fetchAndUpdate()
				case <-ctx.Done():
					p.shuttingDown.Store(true)
					return
				}
			}
		}()
		p.fetchAndUpdate()
	})
}

func (p *Proxy) fetchAndUpdate() {
	result, err := seed.Fetch(p.cfg.SeedURL)
	if err != nil {
		slog.Error("could not get initial seed list", "err", err)
		return
	}
	if result.ChainID != p.cfg.ChainID {
		slog.Error("chain ID is different than expected", "got", result.ChainID, "expected", p.cfg.ChainID)
		return
	}
	if err := p.update(result.Apis.RPC); err != nil {
		slog.Error("could not update servers", "err", err)
	}
}
