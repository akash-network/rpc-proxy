package proxy

import (
	"context"
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

func New(kind ProxyKind, cfg config.Config) *Proxy {
	return &Proxy{
		cfg:  cfg,
		kind: kind,
	}
}

type Proxy struct {
	cfg  config.Config
	kind ProxyKind
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

// TODO: move this to another thing, share it with multiple proxies
func (p *Proxy) update(providers []seed.Provider) error {
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
			if provider.Provider == srv.name {
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
	switch p.kind {
	case RPC:
		if err := p.update(result.APIs.RPC); err != nil {
			slog.Error("could not update servers", "err", err)
		}
	case Rest:
		if err := p.update(result.APIs.Rest); err != nil {
			slog.Error("could not update servers", "err", err)
		}
	}
}
