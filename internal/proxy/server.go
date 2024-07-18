package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/akash-network/proxy/internal/avg"
)

func newServer(name, addr string, healthyThreshold, requestTimeout time.Duration) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}
	return &Server{
		name:             name,
		url:              addr,
		pings:            avg.Moving(50),
		proxy:            httputil.NewSingleHostReverseProxy(target),
		healthyThreshold: healthyThreshold,
		requestTimeout:   requestTimeout,
	}, nil
}

type Server struct {
	name  string
	url   string
	pings *avg.MovingAverage
	proxy *httputil.ReverseProxy

	requestCount     atomic.Int64
	healthyThreshold time.Duration
	requestTimeout   time.Duration
}

func (s *Server) Healthy() bool {
	return s.pings.Last() < s.healthyThreshold
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		d := time.Since(start)
		avg := s.pings.Next(d)
		slog.Info("request done", "name", s.name, "avg", avg, "last", d)
	}()

	slog.Info("proxying request", "name", s.name)
	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()
	s.proxy.ServeHTTP(w, r.WithContext(ctx))
	s.requestCount.Add(1)
	if !s.Healthy() && ctx.Err() == nil {
		// if it's not healthy, this is a tryout to improve - if the request
		// wasn't canceled, reset stats
		slog.Info("resetting statistics", "name", s.name)
		s.pings.Reset()
	}
}
