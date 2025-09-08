package proxy

import (
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
	// Next returns the next server to be used based on the load balancing algorithm.
	Next(*http.Request) *Server
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
func (rr *RoundRobin) Next(_ *http.Request) *Server {
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
	return rr.Next(nil)
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
func (rr *LatencyBased) Next(_ *http.Request) *Server {
	rr.mu.Lock()
	defer rr.mu.Unlock()

	// Return nil if no servers are available
	if len(rr.servers) == 0 {
		return nil
	}

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

// StickyLatencyBased is a load balancer that combines session affinity with latency-based routing.
// It embeds LatencyBased to reuse latency calculation and server management functionality,
// while adding session stickiness using industry-standard headers and cookies.
// Warning: This load balancer type is not effective if running alongside other replicas as
// the state is not shared between replicas.
type StickyLatencyBased struct {
	// LatencyBased provides the core latency-based selection functionality
	*LatencyBased
	// sessionMap maps session identifiers to server references for sticky sessions.
	sessionMap map[string]*Server
	// sessionMu is a separate mutex for session-specific operations to avoid lock contention
	sessionMu sync.RWMutex
	// sessionTimeout defines how long sessions are kept in memory.
	sessionTimeout time.Duration
	// sessionCleanupTicker periodically cleans up expired sessions.
	sessionCleanupTicker *time.Ticker
	// sessionTimestamps tracks when sessions were last accessed.
	sessionTimestamps map[string]time.Time
}

// NewStickyLatencyBased returns a new StickyLatencyBased load balancer instance.
// It embeds a LatencyBased load balancer and adds session management functionality.
func NewStickyLatencyBased(log *slog.Logger, sessionTimeout time.Duration) *StickyLatencyBased {
	if sessionTimeout == 0 {
		sessionTimeout = 30 * time.Minute // Default session timeout
	}

	slb := &StickyLatencyBased{
		LatencyBased:      NewLatencyBased(log),
		sessionMap:        make(map[string]*Server),
		sessionTimestamps: make(map[string]time.Time),
		sessionTimeout:    sessionTimeout,
	}

	// Start cleanup routine for expired sessions
	slb.sessionCleanupTicker = time.NewTicker(5 * time.Minute)
	go slb.cleanupExpiredSessions()

	return slb
}

// Next returns the next server based on session affinity and latency.
// It first checks for existing session identifiers in headers or cookies,
// then falls back to the embedded LatencyBased selection for new sessions.
func (slb *StickyLatencyBased) Next(req *http.Request) *Server {
	if req == nil {
		slb.log.Warn("provided request is nil")
		return slb.LatencyBased.Next(nil)
	}

	slb.LatencyBased.mu.Lock()
	if len(slb.LatencyBased.servers) == 0 {
		slb.LatencyBased.mu.Unlock()
		return nil
	}
	slb.LatencyBased.mu.Unlock()

	sessionID := slb.extractSessionID(req)

	if sessionID != "" {
		slb.sessionMu.RLock()
		if server, exists := slb.sessionMap[sessionID]; exists {
			// Check if session has timed out (cache miss scenario)
			lastAccessed := slb.sessionTimestamps[sessionID]
			if time.Since(lastAccessed) > slb.sessionTimeout {
				slb.sessionMu.RUnlock()

				// Session timed out, clean it up
				slb.sessionMu.Lock()
				delete(slb.sessionMap, sessionID)
				delete(slb.sessionTimestamps, sessionID)
				slb.sessionMu.Unlock()

				slb.log.Info("session timed out, removed",
					"session_id", sessionID,
					"server", server.name,
					"last_accessed", lastAccessed)
			} else {
				// Session is valid (cache hit scenario)
				slb.sessionMu.RUnlock()

				// Update session timestamp
				slb.sessionMu.Lock()
				slb.sessionTimestamps[sessionID] = time.Now()
				slb.sessionMu.Unlock()
				return server
			}
		} else {
			slb.sessionMu.RUnlock()
		}
	}

	// No existing session or unhealthy server, use embedded LatencyBased selection
	server := slb.LatencyBased.Next(req)

	if server != nil && sessionID != "" {
		// Create new session mapping
		slb.sessionMu.Lock()
		slb.sessionMap[sessionID] = server
		slb.sessionTimestamps[sessionID] = time.Now()
		slb.sessionMu.Unlock()

		slb.log.Debug("created new sticky session",
			"session_id", sessionID,
			"server", server.name)
	}

	return server
}

// extractSessionID extracts session identifier from HTTP request.
// It only checks for the X-PROXY-KEY header. If not provided, returns empty string
// which will cause the load balancer to use normal latency-based selection.
func (slb *StickyLatencyBased) extractSessionID(req *http.Request) string {
	return req.Header.Get(ProxyKeyHeader)
}

// Update updates the list of available servers using the embedded LatencyBased functionality
// and cleans up session mappings for servers that no longer exist.
func (slb *StickyLatencyBased) Update(servers []*Server) {
	slb.LatencyBased.Update(servers)
}

// cleanupExpiredSessions runs in a background goroutine to clean up expired sessions
func (slb *StickyLatencyBased) cleanupExpiredSessions() {
	for range slb.sessionCleanupTicker.C {
		slb.sessionMu.Lock()
		now := time.Now()
		for sessionID, timestamp := range slb.sessionTimestamps {
			if now.Sub(timestamp) > slb.sessionTimeout {
				delete(slb.sessionMap, sessionID)
				delete(slb.sessionTimestamps, sessionID)
				slb.log.Debug("cleaned up expired session", "session_id", sessionID)
			}
		}
		slb.sessionMu.Unlock()
	}
}

// Stop stops the cleanup ticker and releases resources
func (slb *StickyLatencyBased) Stop() {
	if slb.sessionCleanupTicker != nil {
		slb.sessionCleanupTicker.Stop()
	}
}
