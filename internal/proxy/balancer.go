package proxy

import (
	"log/slog"
	"math/rand"
	"sync"
	"time"
)

// epsilon is a small value used to avoid division by zero when calculating rates and
// used as toleration when comparing floats.
const epsilon = 1e-9

// LoadBalancer is an interface for load balancing algorithms. It provides
// methods to update the list of available servers and to select the next
// server to be used.
type LoadBalancer interface {
	// Update updates the list of available servers.
	Update([]*Server)
	// Next returns the next server to be used based on the load balancing algorithm.
	Next() *Server
}

// RoundRobin is a simple load balancer that distributes incoming requests
// across multiple servers in a round-robin fashion.
type RoundRobin struct {
	// round is the current round number.
	round int
	// servers is the list of available servers.
	servers []*Server
	// mu is a mutex to protect access to the RoundRobin struct.
	mu sync.Mutex
	// log is a logger to log events.
	log *slog.Logger
}

// NewRoundRobin returns a new RoundRobin load balancer instance.
func NewRoundRobin(log *slog.Logger) *RoundRobin {
	return &RoundRobin{
		log: log,
	}
}

// Next returns the next server to be used based on the round-robin algorithm.
// If the selected server is unhealthy, it will recursively try the next server.
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

// Update updates the list of available servers.
func (rr *RoundRobin) Update(servers []*Server) {
	rr.mu.Lock()
	rr.servers = servers
	rr.mu.Unlock()
}

// LatencyBased is a load balancer that selects servers based on their latency.
// The latency is represented as a rate, with higher rates indicating lower latency.
type LatencyBased struct {
	// servers is the list of available servers with their corresponding rates.
	servers []*RatedServer
	// mu is a mutex to protect access to the LatencyBased struct.
	mu sync.Mutex
	// log is a logger to log events.
	log *slog.Logger
	// randomizer is a random number generator used to select servers.
	randomizer *rand.Rand
}

// RatedServer represents a server with a rate, which is used to determine its
// likelihood of being selected by the LatencyBased load balancer.
type RatedServer struct {
	// Server is the underlying server instance.
	*Server
	// Rate is the rate of the server, representing its latency.
	Rate float64
}

// NewLatencyBased returns a new LatencyBased load balancer instance.
func NewLatencyBased(log *slog.Logger) *LatencyBased {
	return &LatencyBased{
		randomizer: rand.New(rand.NewSource(time.Now().UnixNano())),
		log:        log,
	}
}

// Next returns the next server based on the weighted random selection,
// where the weight is determined by the latency Rate of each server. The cumulative
// approach is used to select a server, effectively creating a "range" for each
// server in the interval [0, 1]. For example, if the rates are [0.5, 0.3, 0.2],
// the ranges would be: Server 1: [0, 0.5), Server 2: [0.5, 0.8), Server 3: [0.8, 1).
// The random number will fall into one of these ranges, effectively selecting
// a server based on its latency rate. This approach works regardless of the order of
// the servers, so there's no need to sort them based on latency or rate.
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

// Update updates the list of available servers and their corresponding rates
// based on their latency. The rate of each server is calculated as the inverse
// of its latency, and then normalized to ensure that the rates sum to 1.0.
// This allows the LatencyBased load balancer to select servers based on their
// relative latency.
func (rr *LatencyBased) Update(servers []*Server) {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	var totalInverse float64
	rr.servers = make([]*RatedServer, len(servers))

	for i := range servers {
		// Calculate the latency of the server in milliseconds
		latency := float64(servers[i].node.Status.Latency.Milliseconds())
		// Avoid division by zero by using a small epsilon value if latency is 0
		if latency == 0 {
			latency = epsilon
		}
		// Calculate the rate of the server as the inverse of its latency
		rr.servers[i] = &RatedServer{
			Server: servers[i],
			Rate:   1 / latency,
		}
		totalInverse += rr.servers[i].Rate
	}

	// Normalize the rates to ensure they sum to 1.0
	for i := range servers {
		rr.servers[i].Rate /= totalInverse
	}
}
