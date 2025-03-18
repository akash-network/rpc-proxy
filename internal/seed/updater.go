package seed

import (
	"context"
	"log/slog"
	"sync"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
)

type Updater struct {
	cfg       config.Config
	listeners []chan<- Seed
	init      sync.Once
	log       *slog.Logger
}

func New(cfg config.Config, log *slog.Logger, listeners ...chan<- Seed) *Updater {
	return &Updater{
		cfg:       cfg,
		listeners: listeners,
		log:       log,
	}
}

func (u *Updater) Start(ctx context.Context) {
	u.log.Info("starting updater")
	u.init.Do(func() {
		go func() {
			t := time.NewTicker(u.cfg.SeedRefreshInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					u.fetchAndUpdate()
				case <-ctx.Done():
					return
				}
			}
		}()
		u.fetchAndUpdate()
	})
}

func (u *Updater) fetchAndUpdate() {
	u.log.Info("fetching seed list")
	result, err := fetch(u.cfg.SeedURL)
	if err != nil {
		u.log.Error("could not get initial seed list", "err", err)
		return
	}
	if result.ChainID != u.cfg.ChainID {
		u.log.Error("chain ID is different than expected", "got", result.ChainID, "expected", u.cfg.ChainID)
		return
	}
	for _, ch := range u.listeners {
		ch <- result
	}
}
