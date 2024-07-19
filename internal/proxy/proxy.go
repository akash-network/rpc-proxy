package proxy

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"slices"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akash-network/proxy/internal/config"
	"github.com/akash-network/proxy/internal/seed"
)

func New(cfg config.Config) *Proxy {
	return &Proxy{
		cfg: cfg,
	}
}

type Proxy struct {
	cfg  config.Config
	init sync.Once

	round   int
	mu      sync.Mutex
	servers []*Server

	shuttingDown atomic.Bool
}

func (p *Proxy) Stats() []ServerStat {
	var result []ServerStat
	for _, s := range p.servers {
		reqCount := s.requestCount.Load()
		result = append(result, ServerStat{
			Name:        s.name,
			URL:         s.url,
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
			if rpc.Provider == srv.name {
				return false
			}
		}
		slog.Info("server was removed from pool", "name", srv.name)
		return true
	})

	slog.Info("updated server list", "total", len(p.servers))
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
