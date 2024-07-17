package main

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"sync/atomic"
	"time"
)

func newServer(name, addr string) (*Server, error) {
	target, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("could not create new server: %w", err)
	}
	return &Server{
		name:  name,
		url:   addr,
		pings: NewPingAverage(50),
		proxy: httputil.NewSingleHostReverseProxy(target),
	}, nil
}

type Server struct {
	name        string
	url         string
	pings       *PingMovingAverage
	proxy       *httputil.ReverseProxy
	initialized atomic.Bool
}

func (s *Server) ResetStatistics() {
	s.pings.Reset()
}

func (s *Server) Healthy() bool {
	// TODO: configurable?
	return s.pings.Last() < time.Second
}

func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	start := time.Now()
	defer func() {
		d := time.Since(start)
		avg := s.pings.Next(d)
		slog.Info("request done", "name", s.name, "avg", avg, "last", d)
	}()

	slog.Info("proxying request", "name", s.name)
	// TODO: configurable timeout?
	ctx, cancel := context.WithTimeout(r.Context(), time.Second*10)
	defer cancel()
	s.proxy.ServeHTTP(w, r.WithContext(ctx))
	s.initialized.Store(true)
}
