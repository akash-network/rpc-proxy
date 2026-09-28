package proxy

import (
	"log/slog"
	"net/url"
	"sync"
	"sync/atomic"
	"time"

	"github.com/akash-network/rpc-proxy/internal/seed"

	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/ttlslice"
)

// TODO: Replace these stats with prometheus metrics server.

// statsWindow is the rolling window over which per-server success and failure
// counts are retained for the ErrorRate shown on /status.
const statsWindow = time.Minute

func newServer(name string, target *url.URL, log *slog.Logger, node seed.Node, b *breaker) *Server {
	return &Server{
		name:      name,
		addr:      node.Address,
		Url:       target,
		pings:     avg.Moving(50),
		successes: ttlslice.New[int](),
		failures:  ttlslice.New[int](),
		log:       log,
		node:      node,
		breaker:   b,
	}
}

type Server struct {
	name         string
	addr         string
	Url          *url.URL
	pings        *avg.MovingAverage
	successes    *ttlslice.Slice[int]
	failures     *ttlslice.Slice[int]
	requestCount atomic.Int64
	log          *slog.Logger
	breaker      *breaker

	// mu guards node, which is refreshed in place by the seed updater goroutine and
	// read concurrently by request goroutines through Healthy.
	mu   sync.RWMutex
	node seed.Node
}

func (s *Server) recordSuccess() {
	s.successes.Append(1, statsWindow)
	s.breaker.recordSuccess()
}

func (s *Server) recordFailure() {
	s.failures.Append(1, statsWindow)
	s.breaker.recordFailure(time.Now())
}

func (s *Server) ErrorRate() float64 {
	suss := len(s.successes.List())
	fail := len(s.failures.List())
	total := suss + fail
	if total == 0 {
		return 0
	}
	return (float64(fail) * 100) / float64(total)
}

// setStatus refreshes the server's probed status in place and recomputes freshness
// against the pool tip. A node is fresh when its height is within tolerance of the
// highest height any reachable peer reported. Comparing to the live pool tip rather
// than a monotonic maximum means the check self-heals: a single-node pool is always
// its own tip and never gates itself out.
func (s *Server) setStatus(node seed.Node, tip, tolerance int64) {
	node.Status.IsLatestBlock = node.Status.Height >= tip-tolerance
	s.mu.Lock()
	s.node = node
	s.mu.Unlock()
}

// Healthy returns whether the server should receive traffic. A server is healthy
// when the seed probe reports it reachable, caught up, and fresh, and its transport
// breaker is not currently ejecting it.
func (s *Server) Healthy() bool {
	s.mu.RLock()
	ok := s.node.Healthy()
	s.mu.RUnlock()
	return ok && !s.breaker.open(time.Now())
}

// latency returns the node's last probed latency under the same lock that guards
// node, so the latency-based balancer reads it consistently with setStatus.
func (s *Server) latency() time.Duration {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.node.Status.Latency
}
