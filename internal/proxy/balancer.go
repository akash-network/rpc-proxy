package proxy

import (
	"crypto/sha256"
	"encoding/binary"
	"log/slog"
	"math/rand"
	"net/http"
	"sync"
	"time"
)

// epsilon is a small value used to avoid division by zero when calculating rates and
// used as toleration when comparing floats.
const epsilon = 1e-9

// ProxyKeyHeader is the HTTP header used for sticky session identification
const ProxyKeyHeader = "X-PROXY-KEY"

// LoadBalancer is an interface for load balancing algorithms. It provides
// methods to update the list of available servers and to select the next
// server to be used.
type LoadBalancer interface {
	// Update updates the list of available servers.
	Update([]*Server)
	// NextServer returns the next server to be used based on the load balancing algorithm.
	NextServer(*http.Request) *Server
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

// NextServer returns the next server to be used based on the round-robin algorithm.
// It skips unhealthy servers and returns nil when none are healthy.
func (rr *RoundRobin) NextServer(r *http.Request) *Server {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	n := len(rr.servers)
	if n == 0 {
		return nil
	}
	for i := 0; i < n; i++ {
		server := rr.servers[rr.round%n]
		rr.round++
		if server.Healthy() {
			return server
		}
		rr.log.Warn("server is unhealthy, trying next", "name", server.name)
	}
	return nil
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

// NextServer returns the next server based on the weighted random selection,
// where the weight is determined by the latency Rate of each server. The cumulative
// approach is used to select a server, effectively creating a "range" for each
// server in the interval [0, 1]. For example, if the rates are [0.5, 0.3, 0.2],
// the ranges would be: Server 1: [0, 0.5), Server 2: [0.5, 0.8), Server 3: [0.8, 1).
// The random number will fall into one of these ranges, effectively selecting
// a server based on its latency rate. This approach works regardless of the order of
// the servers, so there's no need to sort them based on latency or rate.
func (rr *LatencyBased) NextServer(_ *http.Request) *Server {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Select only from healthy servers so an ejected peer stops receiving traffic
	// immediately, without waiting for the next seed refresh.
	var total float64
	healthy := rr.servers[:0:0]
	for _, s := range rr.servers {
		if s.Healthy() {
			healthy = append(healthy, s)
			total += s.Rate
		}
	}

	if len(healthy) == 0 {
		return nil
	}

	r := rr.randomizer.Float64() * total
	cumulative := 0.0
	for _, s := range healthy {
		cumulative += s.Rate
		if r <= cumulative {
			return s.Server
		}
	}

	// Fallback for floating-point drift at the top of the range.
	return healthy[len(healthy)-1].Server
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

func (rr *LatencyBased) pick(key string) *Server {
	rr.mu.Lock()
	defer rr.mu.Unlock()
	var best *Server
	var bestScore uint64
	for _, s := range rr.servers {
		if !s.Healthy() {
			continue
		}
		score := rendezvousScore(key, s.name)
		if best == nil || score > bestScore || (score == bestScore && s.name < best.name) {
			best, bestScore = s.Server, score
		}
	}
	return best
}

// rendezvousScore hashes with SHA-256 (truncated to 64 bits) rather than
// hash/maphash: maphash seeds per process, which would make the same key
// hash to different servers on different replicas. Selection is unweighted
// (latency is ignored) so that all replicas agree on one winner regardless
// of each replica's independently measured latencies.
func rendezvousScore(key, name string) uint64 {
	buf := make([]byte, 0, len(name)+1+len(key))
	buf = append(buf, name...)
	buf = append(buf, 0)
	buf = append(buf, key...)
	sum := sha256.Sum256(buf)
	return binary.BigEndian.Uint64(sum[:8])
}

// StickyLatencyBased routes a keyed request (X-PROXY-KEY) to the same healthy
// server on every call, using unweighted rendezvous hashing (highest random
// weight) over the current healthy set instead of a cached session table. The
// pick for a given key is a pure function of the key and the healthy server
// names, so it holds no shared state and is safe to run across replicas with
// zero coordination: every replica computes the same answer independently.
// Cross-replica determinism depends on s.name being canonical, unique, and
// identical across replicas (doUpdate dedups by name within a replica; the
// seed's Provider field is the shared identity), and on every replica running
// the same AdditionalNodes config. The one caveat: replicas can transiently
// disagree while their healthy sets differ, since the pick only ranges over
// servers each replica currently considers healthy.
type StickyLatencyBased struct {
	*LatencyBased
}

// NewStickyLatencyBased returns a new StickyLatencyBased load balancer instance.
func NewStickyLatencyBased(log *slog.Logger) *StickyLatencyBased {
	return &StickyLatencyBased{LatencyBased: NewLatencyBased(log)}
}

// NextServer returns the server pinned to the request's X-PROXY-KEY, or falls
// back to latency-based selection when the request is nil or carries no key.
func (slb *StickyLatencyBased) NextServer(req *http.Request) *Server {
	if req == nil {
		return slb.LatencyBased.NextServer(req)
	}
	key := req.Header.Get(ProxyKeyHeader)
	if key == "" {
		return slb.LatencyBased.NextServer(req)
	}
	return slb.LatencyBased.pick(key)
}
