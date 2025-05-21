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

type Node struct {
	Address  string `json:"address"`
	Provider string `json:"provider"`
	Status   Status
}

// Status represents the latest status of a node.
type Status struct {
	// Reachable is whether a node was able to be reached or not.
	Reachable bool
	// CatchingUp whether a node is still trying to keep up with the network.
	CatchingUp bool
	// IsLatestBlock is true when the status of the node is caught up to the latest block.
	// This together with the block.BlockManager can be leveraged to confirm that the nodes are up-to-date on the latest block.
	// Because the seeding process takes time, the latest block on the start of the seeding process can be a different one from
	// the end of the seeding process so an absolute value of the latest block is not a good measurement of the node.
	// Instead, the singleton block.BlockManager must be used and set to the latest block height and if there is a
	// block.ErrBlockTooLow when setting the height, it means the node that we queried after was actually not on the latest block yet
	// and is removed from the seed temporarily.
	IsLatestBlock bool
	// Latency ...
	Latency time.Duration
}

type Apis struct {
	RPC  []Node `json:"rpc"`
	Rest []Node `json:"rest"`
	GRPC []Node `json:"grpc"`
}

// Seeder represents a seeder process responsible for updating Seed listeners on new changes to the node Seed.
type Seeder struct {
	cfg       Config
	listeners []chan<- Seed
	init      sync.Once
	log       *slog.Logger
	rpcProbe  Probe
	restProbe Probe
	grpcProbe Probe
}

// New creates a Seeder instance.
// It takes an arbitrary number of listeners that will receive updated Seed structures.
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

// Start executes a single goroutine to fetch and update seed listeners every Config.SeedRefreshInterval.
// It is only executed once for every Seeder instance.
func (s *Seeder) Start(ctx context.Context) {
	s.log.Info("starting updater")
	s.init.Do(func() {
		go func() {
			t := time.NewTicker(s.cfg.SeedRefreshInterval)
			defer t.Stop()
			for {
				select {
				case <-t.C:
					s.fetchAndUpdate(ctx)
				case <-ctx.Done():
					return
				}
			}
		}()
		s.fetchAndUpdate(ctx)
	})
}

func (s *Seeder) fetchAndUpdate(ctx context.Context) {
	s.log.Info("fetching seed list")
	result, err := s.fetch(ctx, s.log, s.cfg.SeedURL)
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

func (s *Seeder) fetch(ctx context.Context, log *slog.Logger, url string) (Seed, error) {
	var seed Seed

	if s.cfg.EnableRemote {
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
	} else {
		seed = Seed{
			ChainID: s.cfg.ChainID,
			APIs:    Apis{},
		}
	}

	// Add manual nodes
	for _, addr := range s.cfg.AdditionalNodes.RPC {
		seed.APIs.RPC = append(seed.APIs.RPC, Node{
			Address:  addr,
			Provider: addr,
		})
	}

	for _, addr := range s.cfg.AdditionalNodes.REST {
		seed.APIs.Rest = append(seed.APIs.Rest, Node{
			Address:  addr,
			Provider: addr,
		})
	}

	for _, addr := range s.cfg.AdditionalNodes.GRPC {
		seed.APIs.GRPC = append(seed.APIs.GRPC, Node{
			Address:  addr,
			Provider: addr,
		})
	}

	// Probe all nodes (both from seed URL and manual)
	for i, rpcProxy := range seed.APIs.RPC {
		status, err := s.rpcProbe.Probe(ctx, rpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create RPC client: %v", err), "name", rpcProxy.Provider)
			seed.APIs.RPC[i] = rpcProxy.WithStatus(status)
			continue
		}

		log.Info("added RPC server",
			"name", rpcProxy.Provider,
			"catching_up", status.CatchingUp,
			"latest_block", status.IsLatestBlock,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		seed.APIs.RPC[i] = rpcProxy.WithStatus(status)
	}

	for i, restProxy := range seed.APIs.Rest {
		status, err := s.restProbe.Probe(ctx, restProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create REST client: %v", err), "name", restProxy.Provider)
			seed.APIs.Rest[i] = restProxy.WithStatus(status)
			continue
		}

		log.Info("added REST server",
			"name", restProxy.Provider,
			"catching_up", status.CatchingUp,
			"latest_block", status.IsLatestBlock,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		seed.APIs.Rest[i] = restProxy.WithStatus(status)
	}

	for i, grpcProxy := range seed.APIs.GRPC {
		status, err := s.grpcProbe.Probe(ctx, grpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create gRPC client: %v", err), "name", grpcProxy.Provider)
			seed.APIs.GRPC[i] = grpcProxy.WithStatus(status)
			continue
		}

		log.Info("added gRPC server",
			"name", grpcProxy.Provider,
			"catching_up", status.CatchingUp,
			"latest_block", status.IsLatestBlock,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		seed.APIs.GRPC[i] = grpcProxy.WithStatus(status)
	}

	return seed, nil
}

func (p Node) WithStatus(status Status) Node {
	return Node{
		Provider: p.Provider,
		Address:  p.Address,
		Status:   status,
	}
}

func (p Node) Healthy() bool {
	return !p.Status.CatchingUp && p.Status.Reachable && p.Status.IsLatestBlock
}
