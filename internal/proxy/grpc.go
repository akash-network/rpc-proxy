package proxy

import (
	"context"
	"log/slog"
	"net/http"
	"net/http/httputil"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

type GRPCProxy struct {
	Proxy
}

func NewGRPCProxy(
	ch chan seed.Seed,
	cfg config.Config,
	log *slog.Logger,
) *GRPCProxy {
	return &GRPCProxy{
		Proxy: Proxy{
			cfg: cfg,
			ch:  ch,
			log: log,
		},
	}
}

func (p *GRPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	if srv := p.next(); srv != nil {

		// Create the reverse proxy
		proxy := httputil.ReverseProxy{
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
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func (p *GRPCProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *GRPCProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for gRPC")
	err := p.doUpdate(seed.APIs.GRPC)
	if err != nil {
		p.log.Error("could not update seed", "err", err)
	}
}
