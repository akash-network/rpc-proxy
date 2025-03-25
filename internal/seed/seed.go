package seed

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"sync"
	"time"
)

type Seed struct {
	Status  string `json:"status"`
	ChainID string `json:"chain_id"`
	APIs    Apis   `json:"apis"`
}

type Provider struct {
	Address  string `json:"address"`
	Provider string `json:"provider"`
	Status   Status
}

type Status struct {
	Reachable  bool
	CatchingUp bool
}

type Apis struct {
	RPC  []Provider `json:"rpc"`
	Rest []Provider `json:"rest"`
	GRPC []Provider `json:"grpc"`
}

var LatestBlock = 0 // TODO: remove, very bad design.

type Seeder struct {
	cfg       Config
	listeners []chan<- Seed
	init      sync.Once
	log       *slog.Logger
	rpcProbe  Probe
	restProbe Probe
	grpcProbe Probe
}

func New(cfg Config, log *slog.Logger, listeners ...chan<- Seed) *Seeder {
	return &Seeder{
		cfg:       cfg,
		listeners: listeners,
		log:       log,
		rpcProbe:  ProbeFunc(RPCProbe),
		restProbe: ProbeFunc(RESTProbe),
		grpcProbe: ProbeFunc(GRPCProbe),
	}
}

func (s *Seeder) Start(ctx context.Context) {
	s.log.Info("starting updater")
	s.init.Do(func() {
		go func() {
			t := time.NewTicker(s.cfg.SeedRefreshInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					s.fetchAndUpdate()
				case <-ctx.Done():
					return
				}
			}
		}()
		s.fetchAndUpdate()
	})
}

func (s *Seeder) fetchAndUpdate() {
	s.log.Info("fetching seed list")
	result, err := s.fetch(s.log, s.cfg.SeedURL)
	if err != nil {
		s.log.Error("could not get initial seed list", "err", err)
		return
	}
	if result.ChainID != s.cfg.ChainID {
		s.log.Error("chain ID is different than expected", "got", result.ChainID, "expected", s.cfg.ChainID)
		return
	}
	for _, ch := range s.listeners {
		ch <- result
	}
}

func (s *Seeder) fetch(log *slog.Logger, url string) (Seed, error) {
	var seed Seed
	resp, err := http.Get(url)
	if err != nil {
		return seed, fmt.Errorf("get seed: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return seed, fmt.Errorf("request failed: %s", resp.Status)
	}

	bts, err := io.ReadAll(resp.Body)
	if err != nil {
		return seed, fmt.Errorf("read seed: %w", err)
	}
	if err := json.Unmarshal(bts, &seed); err != nil {
		return seed, fmt.Errorf("parse seed: %w", err)
	}

	for i, rpcProxy := range seed.APIs.RPC {
		status, err := s.rpcProbe.Probe(rpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create RPC client: %v", err), "name", rpcProxy.Provider)
			seed.APIs.RPC[i] = rpcProxy.WithStatus(status)
			continue
		}

		log.Info("added rpc server", "name", rpcProxy.Provider, "catching_up", status.CatchingUp)
		seed.APIs.RPC[i] = rpcProxy.WithStatus(status)
	}

	for i, restProxy := range seed.APIs.Rest {
		// TODO: add rest node healtchecks
		seed.APIs.Rest[i] = restProxy.WithStatus(Status{
			CatchingUp: false,
			Reachable:  true,
		})
	}

	for i, grpcProxy := range seed.APIs.GRPC {
		status, err := s.grpcProbe.Probe(grpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create gRPC client: %v", err), "name", grpcProxy.Provider)
			seed.APIs.GRPC[i] = grpcProxy.WithStatus(status)
			continue
		}

		log.Info("added gRPC server", "name", grpcProxy.Provider, "catching_up", status.CatchingUp)
		seed.APIs.GRPC[i] = grpcProxy.WithStatus(status)
	}

	return seed, nil
}

func (p Provider) WithStatus(status Status) Provider {
	return Provider{
		Provider: p.Provider,
		Address:  p.Address,
		Status:   status,
	}
}
