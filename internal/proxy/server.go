package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"sync/atomic"
	"time"

	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/ttlslice"
)

func newServer(name, addr string, healthyThreshold, requestTimeout time.Duration, healthInterval time.Duration, cfg config.Config) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}

	server := &Server{
		name:             name,
		url:              target,
		pings:            avg.Moving(50),
		cfg:              cfg,
		successes:        ttlslice.New[int](),
		failures:         ttlslice.New[int](),
		healthyThreshold: healthyThreshold,
		requestTimeout:   requestTimeout,
		lastHealthCheck:  time.Now().UTC(),
		healthInterval:   healthInterval,
		healthy:          atomic.Bool{},
	}

	err = checkSingleRPC(addr)
	server.healthy.Store(err == nil)

	return server, nil
}

type Server struct {
	name            string
	url             *url.URL
	pings           *avg.MovingAverage
	lastHealthCheck time.Time
	healthy         atomic.Bool

	requestCount     atomic.Int64
	healthInterval   time.Duration
	healthyThreshold time.Duration
	requestTimeout   time.Duration
	cfg              config.Config
	successes        *ttlslice.Slice[int]
	failures         *ttlslice.Slice[int]
}

func (s *Server) Healthy() bool {
	now := time.Now().UTC()
	if now.Sub(s.lastHealthCheck) >= s.healthInterval {
		slog.Info("checking health", "name", s.name)
		err := checkSingleRPC(s.url.String())
		healthy := err == nil
		s.healthy.Store(healthy)
		s.lastHealthCheck = now

		if healthy {
			slog.Info("server is healthy", "name", s.name)
		} else {
			slog.Error("server is unhealthy", "name", s.name, "err", err)
		}
	}

	return s.pings.Last() < s.healthyThreshold && s.healthy.Load()
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
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var status int = -1
	start := time.Now()
	defer func() {
		d := time.Since(start)
		avg := s.pings.Next(d)
		slog.Info("request done", "name", s.name, "avg", avg, "last", d, "status", status)
	}()

	path := r.URL.Path
	proxiedURL := r.URL
	proxiedURL.Path = s.url.Path + path
	proxiedURL.Host = s.url.Host
	proxiedURL.Scheme = s.url.Scheme

	slog.Info("proxying request", "name", s.name, "url", proxiedURL)

	rr := &http.Request{
		Method:        r.Method,
		URL:           proxiedURL,
		Header:        r.Header,
		Body:          r.Body,
		ContentLength: r.ContentLength,
		Close:         r.Close,
	}

	ctx, cancel := context.WithTimeout(r.Context(), s.cfg.ProxyRequestTimeout)
	defer cancel()

	resp, err := http.DefaultClient.Do(rr.WithContext(ctx))
	if resp != nil {
		status = resp.StatusCode
	}
	if err == nil {
		defer resp.Body.Close()
		for k, v := range resp.Header {
			for _, vv := range v {
				w.Header().Set(k, vv)
			}
		}
		_, _ = io.Copy(w, resp.Body)
	} else {
		slog.Error("could not proxy request", "err", err)
		http.Error(w, "could not proxy request", http.StatusInternalServerError)
	}

	s.requestCount.Add(1)
	if status == 0 || (status >= 200 && status <= 300) {
		s.successes.Append(status, s.cfg.HealthyErrorRateBucketTimeout)
	} else {
		s.failures.Append(status, s.cfg.HealthyErrorRateBucketTimeout)
	}

	if !s.Healthy() && ctx.Err() == nil && err == nil {
		// if it's not healthy, this is a tryout to improve - if the request
		// wasn't canceled, reset stats
		slog.Info("resetting statistics", "name", s.name)
		s.pings.Reset()
	}
}
