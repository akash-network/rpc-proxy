package proxy

import (
	"fmt"
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

type LoadBalancer interface {
	Update([]*Server)
	Next() *Server
}

type RoundRobin struct {
	round   int
	servers []*Server
	mu      sync.Mutex
	log     *slog.Logger
}

func NewRoundRobin(log *slog.Logger) *RoundRobin {
	return &RoundRobin{
		log: log,
	}
}

func (rr *RoundRobin) Next() *Server {
	rr.mu.Lock()
	if len(rr.servers) == 0 {
		return nil
	}
	server := rr.servers[rr.round%len(rr.servers)]

	rr.round++
	rr.mu.Unlock()
	if server.Healthy() {
		return server
	}
	rr.log.Warn("server is unhealthy, trying next", "name", server.name)
	return rr.Next()
}

func (rr *RoundRobin) Update(servers []*Server) {
	rr.mu.Lock()
	rr.servers = servers
	rr.mu.Unlock()
}

type LatencyBased struct {
	servers    []*RatedServer
	mu         sync.Mutex
	log        *slog.Logger
	randomizer *rand.Rand
}

type RatedServer struct {
	*Server
	Rate float64
}

func NewLatencyBased(log *slog.Logger) *LatencyBased {
	return &LatencyBased{
		randomizer: rand.New(rand.NewSource(time.Now().UnixNano())),
		log:        log,
	}
}

func (rr *LatencyBased) Next() *Server {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	r := rr.randomizer.Float64()
	cumulative := 0.0

	for _, s := range rr.servers {
		cumulative += s.Rate
		if r <= cumulative {
			return s.Server
		}
	}

	// Fallback, shouldn't be reached if rates are normalized
	return rr.servers[len(rr.servers)-1].Server
}

func (rr *LatencyBased) Update(servers []*Server) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	var totalInverse float64
	rr.servers = make([]*RatedServer, len(servers))

	// Avoid division by zero: use a small epsilon if latency is 0
	const epsilon = 1e-9

	for i := range servers {
		latency := float64(servers[i].node.Status.Latency.Milliseconds())
		fmt.Println(latency)
		if latency == 0 {
			latency = epsilon
		}
		rr.servers[i] = &RatedServer{
			Server: servers[i],
			Rate:   1 / latency,
		}
		totalInverse += rr.servers[i].Rate
	}

	// Normalize rates to sum to 1.0
	for i := range servers {
		rr.servers[i].Rate /= totalInverse
	}
}
