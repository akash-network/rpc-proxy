package proxy

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"slices"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

type Proxy struct {
	cfg  config.Config
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
				p.cfg,
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
