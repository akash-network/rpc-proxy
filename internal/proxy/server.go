package proxy

import (
	"log/slog"
	"net/url"
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

func newServer(name string, target *url.URL, log *slog.Logger, node seed.Node, b *breaker) (*Server, error) {
	return &Server{
		name:      name,
		Url:       target,
		pings:     avg.Moving(50),
		successes: ttlslice.New[int](),
		failures:  ttlslice.New[int](),
		log:       log,
		node:      node,
		breaker:   b,
	}, nil
}

type Server struct {
	name         string
	Url          *url.URL
	pings        *avg.MovingAverage
	successes    *ttlslice.Slice[int]
	failures     *ttlslice.Slice[int]
	requestCount atomic.Int64
	log          *slog.Logger
	node         seed.Node
	breaker      *breaker
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

// Healthy returns whether the server should receive traffic. A server is healthy
// when the seed probe reports it caught up and reachable and its transport
// breaker is not currently ejecting it.
func (s *Server) Healthy() bool {
	return s.node.Healthy() && !s.breaker.open(time.Now())
}
