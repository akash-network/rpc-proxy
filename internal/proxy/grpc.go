package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httputil"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/akash-network/rpc-proxy/internal/halt"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	proxyotel "github.com/akash-network/rpc-proxy/internal/otel"

	"github.com/akash-network/rpc-proxy/internal/seed"
)

// GRPCProxy is a wrapper around Proxy that provides gRPC-specific behavior.
// It embeds the Proxy struct to reuse its core logic and configuration.
type GRPCProxy struct {
	Proxy
}

// NewGRPCProxy creates and returns a new instance of GRPCProxy.
// It initializes the embedded Proxy with the given seed channel,
// configuration, logger, and a custom load balancer.
func NewGRPCProxy(
	ch chan seed.Seed,
	log *slog.Logger,
	lb LoadBalancer,
	hd *halt.Detector,
) *GRPCProxy {
	return &GRPCProxy{
		Proxy: Proxy{
			ch:           ch,
			log:          log,
			lb:           lb,
			haltDetector: hd,
		},
	}
}

// ServeHTTP handles incoming HTTP requests for the GRPCProxy.
// It satisfies the http.Handler interface, allowing GRPCProxy to be used
// directly with an HTTP server (e.g., http.ListenAndServe).
func (p *GRPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if p.haltDetector != nil && p.haltDetector.IsHalted() {
		if err := p.writeHaltResponse(w); err != nil {
			p.log.Error(fmt.Errorf("writing halt response: %w", err).Error())
		}
		return
	}

	ctx, span := proxyotel.Tracer().Start(r.Context(), "lb.select_backend",
		trace.WithAttributes(attribute.String("proxy.type", "grpc")))
	r = r.WithContext(ctx)
	srv := p.lb.NextServer(r)
	if srv != nil {
		span.SetAttributes(attribute.String("proxy.backend", srv.Url.String()))
	}
	span.End()

	if srv != nil {
		fwdCtx, fwdSpan := proxyotel.Tracer().Start(r.Context(), "proxy.forward",
			trace.WithAttributes(
				attribute.String("proxy.type", "grpc"),
				attribute.String("proxy.backend", srv.Url.String()),
			))
		r = r.WithContext(fwdCtx)

		// Create the reverse proxy
		proxy := httputil.ReverseProxy{
			Transport: otelhttp.NewTransport(http.DefaultTransport),
			Director: func(request *http.Request) {
				request.URL.Scheme = srv.Url.Scheme
				request.URL.Opaque = srv.Url.Opaque
				request.URL.Host = srv.Url.Host
				request.URL.Path = r.URL.Path
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
		fwdSpan.End()
		metrics.IncrementRequestCount("grpc", srv.Url.String())
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

// Start begins the lifecycle of the GRPCProxy.
// It delegates to the embedded Proxy's Start method, passing in the context
// and the GRPCProxy's update function.
func (p *GRPCProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *GRPCProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for gRPC")
	err := p.doUpdate(seed.APIs.GRPC)
	if err != nil {
		p.log.Error("could not update seed", "error", err)
	}
	p.log.Info("updated server list for gRPC", "total", len(p.servers))
	metrics.UpdateNodeCount("grpc", float64(len(p.servers)))

	for _, node := range seed.APIs.GRPC { // Update health status for each gRPC node
		metrics.UpdateNodeHealth("grpc", node.Address, node.Healthy())
	}
}
