package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/halt"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	proxyotel "github.com/akash-network/rpc-proxy/internal/otel"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

// RestProxy is a wrapper around Proxy that provides REST-specific behavior.
// It embeds the Proxy struct to reuse shared logic and configuration.
type RestProxy struct {
	Proxy
}

// NewRestProxy creates and returns a new instance of RestProxy.
// It initializes the embedded Proxy with the given seed channel,
// configuration, logger, and load balancer.
func NewRestProxy(
	ch chan seed.Seed,
	cfg config.HealthConfig,
	log *slog.Logger,
	lb LoadBalancer,
	hd *halt.Detector,
) *RestProxy {
	return &RestProxy{
		Proxy: Proxy{
			cfg:          cfg,
			ch:           ch,
			log:          log,
			lb:           lb,
			haltDetector: hd,
		},
	}
}

// ServeHTTP handles incoming HTTP requests for the RestProxy.
// It satisfies the http.Handler interface, allowing RestProxy to be used
// directly with an HTTP server (e.g., http.ListenAndServe).
func (p *RestProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if p.haltDetector != nil && p.haltDetector.IsHalted() {
		p.writeHaltResponse(w)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rest")

	ctx, span := proxyotel.Tracer().Start(r.Context(), "lb.select_backend",
		trace.WithAttributes(attribute.String("proxy.type", "rest")))
	r = r.WithContext(ctx)
	srv := p.lb.NextServer(r)
	if srv != nil {
		span.SetAttributes(attribute.String("proxy.backend", srv.Url.String()))
	}
	span.End()

	if srv != nil {
		fwdCtx, fwdSpan := proxyotel.Tracer().Start(r.Context(), "proxy.forward",
			trace.WithAttributes(
				attribute.String("proxy.type", "rest"),
				attribute.String("proxy.backend", srv.Url.String()),
			))
		proxy := newRedirectFollowingReverseProxy(srv, p.log, "rest")
		proxy.ServeHTTP(w, r.WithContext(fwdCtx))
		fwdSpan.End()
		metrics.IncrementRequestCount("rest", srv.Url.String())
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

// Start begins the lifecycle of the RestProxy.
// It delegates to the embedded Proxy's Start method, passing in the context
// and the RestProxy's update function.
func (p *RestProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *RestProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for REST")
	err := p.doUpdate(seed.APIs.Rest)
	if err != nil {
		p.log.Error("could not update seed", "error", err)
	}
	p.log.Info("updated server list for REST", "total", len(p.servers))
	metrics.UpdateNodeCount("rest", float64(len(p.servers)))

	for _, node := range seed.APIs.Rest { // Update health status for each REST node
		metrics.UpdateNodeHealth("rest", node.Address, node.Healthy())
	}
}
