package proxy

import (
	"log/slog"
	"sync"
)

type LoadBalancer interface {
	Next([]*Server) *Server
}

type RoundRobin struct {
	round int
	mu    sync.Mutex
	log   *slog.Logger
}

func New(log *slog.Logger) *RoundRobin {
	return &RoundRobin{
		log: log,
	}
}

func (rr *RoundRobin) Next(servers []*Server) *Server {
	rr.mu.Lock()
	if len(servers) == 0 {
		rr.mu.Unlock()
		return nil
	}
	server := servers[rr.round%len(servers)]

	rr.round++
	rr.mu.Unlock()
	if server.Healthy() {
		return server
	}
	rr.log.Warn("server is too slow, trying next", "name", server.name, "avg", server.pings.Last())
	return rr.Next(servers)
}
