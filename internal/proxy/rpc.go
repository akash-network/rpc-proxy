package proxy

import (
	"context"
	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"log/slog"
	"net/http"
	"strings"
)

type RPCProxy struct {
	Proxy
}

func NewRPCProxy(
	ch chan seed.Seed,
	cfg config.Config,
	log *slog.Logger,
) *RPCProxy {
	return &RPCProxy{
		Proxy: Proxy{
			cfg: cfg,
			ch:  ch,
			log: log,
			lb:  NewRoundRobin(log),
		},
	}
}

func (p *RPCProxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if p.shuttingDown.Load() {
		p.log.Error("proxy is shutting down")
		w.WriteHeader(http.StatusInternalServerError)
		return
	}

	r.URL.Path = strings.TrimPrefix(r.URL.Path, "/rpc")
	if srv := p.lb.Next(); srv != nil {
		srv.ServeHTTP(w, r)
		return
	}

	p.log.Error("no servers available")
	w.WriteHeader(http.StatusInternalServerError)
}

func (p *RPCProxy) Start(ctx context.Context) {
	p.Proxy.Start(ctx, p.update)
}

func (p *RPCProxy) update(seed seed.Seed) {
	p.log.Info("updating server list for RPC")
	err := p.doUpdate(seed.APIs.RPC)
	if err != nil {
		p.log.Error("could not update seed", "err", err)
	}
	p.log.Info("updated server list for RPC", "total", len(p.servers))
}
