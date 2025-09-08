package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

// RPCProxy is a wrapper around Proxy that provides RPC-specific functionality.
// It embeds the Proxy struct, inheriting its fields and behavior.
type RPCProxy struct {
	Proxy
}

// NewRPCProxy creates and returns a new instance of RPCProxy.
// It initializes the embedded Proxy with the provided configuration,
// seed channel, logger, and a load balancer.
func NewRPCProxy(
	ch chan seed.Seed,
	cfg config.HealthConfig,
	log *slog.Logger,
	lb LoadBalancer,
) *RPCProxy {
	return &RPCProxy{
		Proxy: Proxy{
			cfg: cfg,
			ch:  ch,
			log: log,
			lb:  lb,
		},
	}
}

// ServeHTTP handles incoming HTTP requests for the RPCProxy.
// It satisfies the http.Handler interface, allowing RPCProxy to be used
// directly with an HTTP server (e.g., http.ListenAndServe).
func (p *RPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rpc")
	if srv := p.lb.Next(r); srv != nil {
		proxy := newRedirectFollowingReverseProxy(srv, p.log, "rpc")
		proxy.ServeHTTP(w, r)
		metrics.IncrementRequestCount("rpc", srv.Url.String())
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

// Start begins the lifecycle of the RPCProxy.
// It delegates to the embedded Proxy's Start method, passing in the context
// and the GRPCProxy's update function.
func (p *RPCProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *RPCProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for RPC")
	err := p.doUpdate(seed.APIs.RPC)
	if err != nil {
		p.log.Error("could not update seed", "error", err)
	}
	p.log.Info("updated server list for RPC", "total", len(p.servers))
	metrics.UpdateNodeCount("rpc", float64(len(p.servers)))

	for _, node := range seed.APIs.RPC { // Update health status for each RPC node
		metrics.UpdateNodeHealth("rpc", node.Address, node.Healthy())
	}
}
