package proxy

import (
	"context"
	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"log/slog"
	"net/http"
	"strings"
)

type RestProxy struct {
	Proxy
}

func NewRestProxy(
	ch chan seed.Seed,
	cfg config.Config,
	log *slog.Logger,
) *RestProxy {
	return &RestProxy{
		Proxy: Proxy{
			cfg: cfg,
			ch:  ch,
			log: log,
		},
	}
}

func (p *RestProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rest")
	if srv := p.next(); srv != nil {
		srv.ServeHTTP(w, r)
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func (p *RestProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *RestProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for REST")
	err := p.doUpdate(seed.APIs.Rest)
	if err != nil {
		p.log.Error("could not update seed", "err", err)
	}
	p.log.Info("updated server list for REST", "total", len(p.servers))
}
