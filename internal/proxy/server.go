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
)

func newServer(name, addr string, healthyThreshold, requestTimeout time.Duration) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}
	return &Server{
		name:             name,
		url:              target,
		pings:            avg.Moving(50),
		healthyThreshold: healthyThreshold,
		requestTimeout:   requestTimeout,
	}, nil
}

type Server struct {
	name  string
	url   *url.URL
	pings *avg.MovingAverage

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

	proxiedURL := r.URL
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

	ctx, cancel := context.WithTimeout(r.Context(), s.requestTimeout)
	defer cancel()

	resp, err := http.DefaultClient.Do(rr.WithContext(ctx))
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
	if !s.Healthy() && ctx.Err() == nil && err == nil {
		// if it's not healthy, this is a tryout to improve - if the request
		// wasn't canceled, reset stats
		slog.Info("resetting statistics", "name", s.name)
		s.pings.Reset()
	}
}
