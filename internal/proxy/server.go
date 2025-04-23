package proxy

import (
	"log/slog"
	"net/url"
	"sync/atomic"

	"github.com/akash-network/rpc-proxy/internal/seed"

	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/ttlslice"
)

// TODO: Replace these stats with prometheus metrics server.

func newServer(name string, target *url.URL, log *slog.Logger, node seed.Node) (*Server, error) {
	return &Server{
		name:      name,
		Url:       target,
		pings:     avg.Moving(50),
		successes: ttlslice.New[int](),
		failures:  ttlslice.New[int](),
		log:       log,
		node:      node,
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

// Healthy returns whether the server is currently healthy by delegating to the underlying node's
// health check.
// This is used by load balancers to determine if requests should be routed to this server.
func (s *Server) Healthy() bool {
	return s.node.Healthy()
}
