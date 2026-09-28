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

// listFetchTimeout bounds the seed-URL fetch. It runs on the same goroutine as
// probing, so an unbounded fetch would stall the whole health loop.
const listFetchTimeout = 15 * time.Second

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
	// IsLatestBlock reports whether the node is fresh enough to serve. It is not set
	// by the probe: the proxy computes it in Server.setStatus by comparing Height to
	// the pool tip within the configured freshness tolerance, and Node.Healthy reads
	// it. Probes only report the raw Height.
	IsLatestBlock bool
	// Height is the latest block height the node reported at probe time. It is zero
	// when the node was unreachable. Routing judges freshness by comparing this to
	// the tip observed across the pool, so a node that drifts behind head can be
	// gated between probe cycles instead of serving stale reads.
	Height int64
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

	// list is the most recently fetched node list, before probing. It is owned by
	// the single updater goroutine, so it needs no lock. The slow ticker refreshes
	// it from the seed URL; the fast ticker re-probes it for freshness.
	list Seed
	// listLoaded is true once a list has been fetched successfully, so probing does
	// not run against an empty list before the first fetch completes.
	listLoaded bool
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

// Start runs a single updater goroutine with two cadences. The slow ticker
// (SeedRefreshInterval) refreshes the node list from the seed URL, which rarely
// changes. The fast ticker (HealthRefreshInterval) re-probes that list and pushes
// the freshly probed Seed to listeners, so a node that falls behind chain head is
// detected within seconds instead of a full seed cycle. It is only executed once
// for every Seeder instance.
func (s *Seeder) Start(ctx context.Context) {
	s.log.Info("starting updater")
	s.init.Do(func() {
		healthInterval := s.cfg.HealthRefreshInterval
		if healthInterval <= 0 {
			healthInterval = s.cfg.SeedRefreshInterval
		}
		go func() {
			listTicker := time.NewTicker(s.cfg.SeedRefreshInterval)
			healthTicker := time.NewTicker(healthInterval)
			defer listTicker.Stop()
			defer healthTicker.Stop()

			s.refreshList()
			s.probeAndPush(ctx)
			for {
				select {
				case <-listTicker.C:
					s.refreshList()
				case <-healthTicker.C:
					s.probeAndPush(ctx)
				case <-ctx.Done():
					return
				}
			}
		}()
	})
}

// refreshList fetches the node list from the seed URL and caches it for probing.
// It leaves the previous list in place on any failure so probing can continue.
func (s *Seeder) refreshList() {
	s.log.Info("fetching seed list")
	list, err := s.fetchList(s.cfg.SeedURL)
	if err != nil {
		s.log.Error("could not get seed list", "error", err)
		return
	}
	if list.ChainID != s.cfg.ChainID {
		s.log.Error("chain ID is different than expected", "got", list.ChainID, "expected", s.cfg.ChainID)
		return
	}
	s.list = list
	s.listLoaded = true
}

// probeAndPush probes the cached node list and pushes the probed Seed to every
// listener. It is a no-op until a list has been fetched successfully.
func (s *Seeder) probeAndPush(ctx context.Context) {
	if !s.listLoaded {
		return
	}
	probed := s.probe(ctx, s.list)
	for _, ch := range s.listeners {
		select {
		case ch <- probed:
		case <-ctx.Done():
			return
		}
	}
}

// fetchList assembles the node list from the seed URL and any manually configured
// nodes. It does not probe; probing happens in probe on the fast cadence.
func (s *Seeder) fetchList(url string) (Seed, error) {
	var seed Seed

	if s.cfg.EnableRemote {
		// Bound the fetch: it shares the updater goroutine with probing, so an
		// unbounded stall on the seed URL would freeze all health probing.
		client := &http.Client{Timeout: listFetchTimeout}
		resp, err := client.Get(url)
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

	for _, addr := range s.cfg.AdditionalNodes.RPC {
		seed.APIs.RPC = append(seed.APIs.RPC, Node{Address: addr, Provider: addr})
	}
	for _, addr := range s.cfg.AdditionalNodes.REST {
		seed.APIs.Rest = append(seed.APIs.Rest, Node{Address: addr, Provider: addr})
	}
	for _, addr := range s.cfg.AdditionalNodes.GRPC {
		seed.APIs.GRPC = append(seed.APIs.GRPC, Node{Address: addr, Provider: addr})
	}

	return seed, nil
}

// probe queries every node in the list and returns a new Seed carrying the probed
// status. It does not mutate list, so the next cadence re-probes fresh nodes.
func (s *Seeder) probe(ctx context.Context, list Seed) Seed {
	log := s.log
	out := Seed{
		Status:  list.Status,
		ChainID: list.ChainID,
		APIs: Apis{
			RPC:  make([]Node, len(list.APIs.RPC)),
			Rest: make([]Node, len(list.APIs.Rest)),
			GRPC: make([]Node, len(list.APIs.GRPC)),
		},
	}

	for i, rpcProxy := range list.APIs.RPC {
		status, err := s.rpcProbe.Probe(ctx, rpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create RPC client: %v", err), "name", rpcProxy.Provider)
			out.APIs.RPC[i] = rpcProxy.WithStatus(status)
			continue
		}
		log.Debug("probed RPC server",
			"name", rpcProxy.Provider,
			"catching_up", status.CatchingUp,
			"height", status.Height,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		out.APIs.RPC[i] = rpcProxy.WithStatus(status)
	}

	for i, restProxy := range list.APIs.Rest {
		status, err := s.restProbe.Probe(ctx, restProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create REST client: %v", err), "name", restProxy.Provider)
			out.APIs.Rest[i] = restProxy.WithStatus(status)
			continue
		}
		log.Debug("probed REST server",
			"name", restProxy.Provider,
			"catching_up", status.CatchingUp,
			"height", status.Height,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		out.APIs.Rest[i] = restProxy.WithStatus(status)
	}

	for i, grpcProxy := range list.APIs.GRPC {
		status, err := s.grpcProbe.Probe(ctx, grpcProxy)
		if err != nil {
			log.Error(fmt.Sprintf("failed to create gRPC client: %v", err), "name", grpcProxy.Provider)
			out.APIs.GRPC[i] = grpcProxy.WithStatus(status)
			continue
		}
		log.Debug("probed gRPC server",
			"name", grpcProxy.Provider,
			"catching_up", status.CatchingUp,
			"height", status.Height,
			"latency", fmt.Sprintf("%dms", status.Latency.Milliseconds()))
		out.APIs.GRPC[i] = grpcProxy.WithStatus(status)
	}

	return out
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
