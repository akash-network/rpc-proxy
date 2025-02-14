package proxy

import (
	"context"
	"log/slog"
	"math/rand"
	"net/http"
	"net/http/httputil"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

type ProxyKind uint8

const (
	RPC  ProxyKind = iota
	Rest ProxyKind = iota
	GRPC ProxyKind = iota
)

func New(
	kind ProxyKind,
	ch chan seed.Seed,
	cfg config.Config,
	log *slog.Logger,
) *Proxy {
	return &Proxy{
		cfg:  cfg,
		ch:   ch,
		kind: kind,
		log:  log,
	}
}

type Proxy struct {
	cfg  config.Config
	log  *slog.Logger
	kind ProxyKind
	init sync.Once
	ch   chan seed.Seed

	round   int
	mu      sync.Mutex
	servers []*Server

	initialized  atomic.Bool
	shuttingDown atomic.Bool
}

func (p *Proxy) Ready() bool { return p.initialized.Load() }
func (p *Proxy) Live() bool  { return !p.shuttingDown.Load() && p.initialized.Load() }

func (p *Proxy) Stats() []ServerStat {
	var result []ServerStat
	for _, s := range p.servers {
		reqCount := s.requestCount.Load()
		result = append(result, ServerStat{
			Name:        s.name,
			URL:         s.Url.String(),
			Avg:         s.pings.Last(),
			Degraded:    !s.Healthy(),
			Initialized: reqCount > 0,
			Requests:    reqCount,
			ErrorRate:   s.ErrorRate(),
		})
	}
	sort.Sort(serverStats(result))
	return result
}

func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	switch p.kind {
	case RPC:
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rpc")
		if srv := p.next(); srv != nil {
			srv.ServeHTTP(w, r)
			return
		}
	case Rest:
		r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rest")
		if srv := p.next(); srv != nil {
			srv.ServeHTTP(w, r)
			return
		}
	case GRPC:
		if srv := p.next(); srv != nil {

			// TODO: Remove as this is used while there is no chain.json with live grpc nodes
			//srv.Url.Scheme = "https"
			//srv.Url.Opaque = ""
			//srv.Url.Host = "grpc17.akashnet.net:10023"
			//srv.Url.Path = r.URL.Path

			// Create the reverse proxy
			proxy := httputil.ReverseProxy{
				Director: func(request *http.Request) {
					request.URL.Scheme = srv.Url.Scheme
					request.URL.Opaque = srv.Url.Opaque
					request.URL.Host = srv.Url.Host
					request.URL.Path = srv.Url.Path
					request.Header.Set("Content-Type", "application/grpc")
					request.Header.Set("te", "trailers")
					request.Body = r.Body
					request.Host = srv.Url.Host // This is very important otherwise a 421 is returned.
				},
				ModifyResponse: func(response *http.Response) error {
					p.log.Info("modifying response", "response", response.StatusCode)
					response.Header.Set("Content-Type", "application/grpc")
					return nil
				},
				ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
					p.log.Error("proxy error", "error", err)
				},
			}

			p.log.Info("serving request", "target", srv.Url, "source", r.URL)

			proxy.ServeHTTP(w, r)
			return
		}

	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func (p *Proxy) next() *Server {
	p.mu.Lock()
	if len(p.servers) == 0 {
		p.mu.Unlock()
		return nil
	}
	server := p.servers[p.round%len(p.servers)]

	p.round++
	p.mu.Unlock()
	if server.Healthy() && server.ErrorRate() <= p.cfg.HealthyErrorRateThreshold {
		return server
	}
	if rand.Intn(99)+1 < p.cfg.UnhealthyServerRecoverChancePct {
		p.log.Warn("giving slow server a chance", "name", server.name, "avg", server.pings.Last())
		return server
	}
	p.log.Warn("server is too slow, trying next", "name", server.name, "avg", server.pings.Last())
	return p.next()
}

func (p *Proxy) update(seed seed.Seed) {
	var err error
	switch p.kind {
	case RPC:
		p.log.Info("updating server list for RPC")
		err = p.doUpdate(seed.APIs.RPC)
	case Rest:
		p.log.Info("updating server list for Rest")
		err = p.doUpdate(seed.APIs.Rest)
	case GRPC:
		p.log.Info("updating server list for gRPC")
		err = p.doUpdate(seed.APIs.GRPC)
	}
	if err != nil {
		p.log.Error("could not update seed", "err", err)
	}
}

func (p *Proxy) doUpdate(providers []seed.Provider) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	// add new servers
	for _, provider := range providers {
		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == provider.Provider })
		if idx == -1 {
			srv, err := newServer(
				provider.Provider,
				provider.Address,
				p.cfg,
				p.log.With("server_address", provider.Address),
			)
			if err != nil {
				return err
			}

			p.servers = append(p.servers, srv)
		}
	}

	// remove deleted servers
	p.servers = slices.DeleteFunc(p.servers, func(srv *Server) bool {
		for _, provider := range providers {
			if provider.Provider == srv.name {
				return false
			}
		}
		p.log.Info("server was removed from pool", "name", srv.name)
		return true
	})

	p.log.Info("updated server list", "total", len(p.servers))
	p.initialized.Store(true)
	return nil
}

func (p *Proxy) Start(ctx context.Context) {
	p.init.Do(func() {
		go func() {
			for {
				select {
				case seed := <-p.ch:
					p.update(seed)
				case <-ctx.Done():
					p.shuttingDown.Store(true)
					return
				}
			}
		}()
	})
}
