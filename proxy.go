package main

import (
	"log/slog"
	"math/rand"
	"net/http"
	"slices"
	"sort"
	"sync"
	"time"
)

func newProxy(seed string, interval time.Duration) *akashProxy {
	return &akashProxy{
		url:      seed,
		interval: interval,
	}
}

type akashProxy struct {
	url      string
	interval time.Duration
	init     sync.Once

	round   int
	mu      sync.Mutex
	servers []*Server
}

type ServerStat struct {
	Name        string
	URL         string
	Avg         time.Duration
	Degraded    bool
	Initialized bool
}

type serverStats []ServerStat

func (st serverStats) Len() int      { return len(st) }
func (st serverStats) Swap(i, j int) { st[i], st[j] = st[j], st[i] }
func (st serverStats) Less(i, j int) bool {
	si := st[i]
	sj := st[j]
	if si.Initialized && !sj.Initialized {
		return true
	}
	if sj.Initialized && !si.Initialized {
		return false
	}
	if si.Degraded && !sj.Degraded {
		return false
	}
	if sj.Degraded && !si.Degraded {
		return true
	}
	return si.Avg < sj.Avg
}

func (p *akashProxy) Stats() []ServerStat {
	var result []ServerStat
	for _, s := range p.servers {
		result = append(result, ServerStat{
			Name:        s.name,
			URL:         s.url,
			Avg:         s.pings.Last(),
			Degraded:    !s.Healthy(),
			Initialized: s.initialized.Load(),
		})
	}
	sort.Sort(serverStats(result))
	return result
}

func (p *akashProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if srv := p.next(); srv != nil {
		srv.ServeHTTP(w, r)
		return

	}
	slog.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func (p *akashProxy) next() *Server {
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
	// give it 1% chance to improve its score
	// TODO: customizable?
	if rand.Intn(99) == 0 {
		server.ResetStatistics()
		slog.Warn("giving slow server a chance", "name", server.name, "avg", server.pings.Last())
		return server
	}
	slog.Warn("server is too slow, trying next", "name", server.name, "avg", server.pings.Last())
	return p.next()
}

func (p *akashProxy) update(rpcs []RPC) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// add new servers
	for _, rpc := range rpcs {
		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == rpc.Provider })
		if idx == -1 {
			srv, err := newServer(rpc.Provider, rpc.Address)
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

func (p *akashProxy) Start() {
	p.init.Do(func() {
		go func() {
			t := time.NewTicker(p.interval)
			for range t.C {
				seed, err := fetchSeed(p.url)
				if err != nil {
					slog.Error("could not fetch seed", "err", err)
					continue
				}
				if err := p.update(seed.Apis.RPC); err != nil {
					slog.Error("could not update servers", "err", err)
					continue
				}
			}
		}()

		seed, err := fetchSeed(p.url)
		if err != nil {
			slog.Error("could not get initial seed list", "err", err)
		}
		if err := p.update(seed.Apis.RPC); err != nil {
			slog.Error("could not update servers", "err", err)
		}
	})
}
