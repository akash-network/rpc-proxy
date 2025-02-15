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

// TODO: Replace these stats with prometheus metrics server.

func newServer(name, addr string, cfg config.Config, log *slog.Logger) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}

	return &Server{
		name:      name,
		Url:       target,
		pings:     avg.Moving(50),
		cfg:       cfg,
		successes: ttlslice.New[int](),
		failures:  ttlslice.New[int](),
		log:       log,
	}, nil
}

type Server struct {
	cfg          config.Config
	name         string
	Url          *url.URL
	pings        *avg.MovingAverage
	successes    *ttlslice.Slice[int]
	failures     *ttlslice.Slice[int]
	requestCount atomic.Int64
	log          *slog.Logger
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
	return s.pings.Last() < s.cfg.HealthyThreshold &&
		s.ErrorRate() < s.cfg.HealthyErrorRateThreshold
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	var status int = -1
	start := time.Now()
	defer func() {
		d := time.Since(start)
		avg := s.pings.Next(d)
		s.log.Info("request done", "name", s.name, "avg", avg, "last", d, "status", status)
	}()

	path := r.URL.Path
	proxiedURL := r.URL
	proxiedURL.Path = s.Url.Path + path
	proxiedURL.Host = s.Url.Host
	proxiedURL.Scheme = s.Url.Scheme

	s.log.Info("proxying request", "name", s.name, "url", proxiedURL, "proto", r.Proto)

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
		s.log.Error("could not proxy request", "err", err)
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
		s.log.Info("resetting statistics", "name", s.name)
		s.pings.Reset()
	}
}
