package proxy

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/ttlslice"
)

func newServer(name, addr string, cfg config.Config) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}
	return &Server{
		name:      name,
		url:       target,
		pings:     avg.Moving(50),
		cfg:       cfg,
		successes: ttlslice.New[int](),
		failures:  ttlslice.New[int](),
	}, nil
}

type Server struct {
	cfg          config.Config
	name         string
	url          *url.URL
	pings        *avg.MovingAverage
	successes    *ttlslice.Slice[int]
	failures     *ttlslice.Slice[int]
	requestCount atomic.Int64
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

func (s *Server) Healthy() bool {
	return s.pings.Last() < s.cfg.HealthyThreshold
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

	if !strings.HasSuffix(s.url.Path, "/rpc") {
		proxiedURL.Path = strings.TrimSuffix(proxiedURL.Path, "/rpc")
	}

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
	if resp.StatusCode >= 200 && resp.StatusCode <= 300 {
		s.successes.Append(resp.StatusCode, s.cfg.HealthyErrorRateBucketTimeout)
	} else {
		s.failures.Append(resp.StatusCode, s.cfg.HealthyErrorRateBucketTimeout)
	}

	if !s.Healthy() && ctx.Err() == nil && err == nil {
		// if it's not healthy, this is a tryout to improve - if the request
		// wasn't canceled, reset stats
		slog.Info("resetting statistics", "name", s.name)
		s.pings.Reset()
	}
}
