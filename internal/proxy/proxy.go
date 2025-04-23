package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/akash-network/rpc-proxy/internal/proxy/cors"

	"github.com/akash-network/rpc-proxy/internal/metrics"
	"github.com/akash-network/rpc-proxy/internal/proxy/cors"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

type Proxy struct {
	cfg  config.HealthConfig
	log  *slog.Logger
	init sync.Once
	ch   chan seed.Seed

	servers []*Server

	initialized  atomic.Bool
	shuttingDown atomic.Bool
	lb           LoadBalancer
}

type Updater func(s seed.Seed)

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

func (p *Proxy) doUpdate(providers []seed.Node) error {

	// add new servers
	for _, provider := range providers {
		// handle schemeless urls
		if !strings.HasPrefix(provider.Address, "http://") && !strings.HasPrefix(provider.Address, "https://") {
			provider.Address = fmt.Sprintf("https://%s", provider.Address)
		}

		target, err := url.Parse(provider.Address)
		if err != nil {
			return err
		}

		idx := slices.IndexFunc(p.servers, func(srv *Server) bool { return srv.name == provider.Provider })
		if idx == -1 {
			srv, err := newServer(
				provider.Provider,
				target,
				p.log.With("server_address", provider.Address),
				provider,
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
				if !provider.Healthy() { // provider matches but is unhealthy.
					p.log.Info("unhealthy server removed from pool",
						"name", srv.name,
						"catching_up", provider.Status.CatchingUp,
						"reachable", provider.Status.Reachable,
						"latest_block", provider.Status.IsLatestBlock)
					return true
				}
				return false
			}
		}
		p.log.Info("server was removed from pool", "name", srv.name)
		return true
	})

	p.initialized.Store(true)
	p.lb.Update(p.servers)

	return nil
}

// Start initializes and begins the proxy's update loop.
// It ensures the loop is started only once using sync.Once.
// The loop continuously listens for new seed data from the channel and calls the provided update function.
// When the context is cancelled, it marks the proxy as shutting down and exits.
func (p *Proxy) Start(ctx context.Context, update Updater) {
	p.init.Do(func() {
		go func() {
			for {
				select {
				case seed := <-p.ch:
					update(seed)
				case <-ctx.Done():
					p.shuttingDown.Store(true)
					return
				}
			}
		}()
	})
}

// newReverseProxy creates a configured httputil.ReverseProxy with common settings.
func newReverseProxy(srv *Server, log *slog.Logger) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			request.URL.Scheme = srv.Url.Scheme
			request.URL.Host = srv.Url.Host
			request.URL.Path = srv.Url.Path + request.URL.Path
			request.Host = srv.Url.Host

			log.Info("proxying request", "method", request.Method, "target", request.URL, "source", request.URL)
		},
		ModifyResponse: func(response *http.Response) error {
			cors.DeleteCorsHeaders(response)
			metrics.IncrementRequestStatusCount("rpc", srv.Url.String(), response.StatusCode)
			return nil
		},
		ErrorHandler: func(writer http.ResponseWriter, request *http.Request, err error) {
			log.Error("proxy error", "error", err)
			http.Error(writer, "could not proxy request", http.StatusInternalServerError)
		},
	}
}
