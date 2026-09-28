package seed

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestUpdater(t *testing.T) {
	chainID := "test"
	seed := Seed{
		ChainID: chainID,
		APIs: Apis{
			RPC: []Node{
				{
					Address:  "http://rpc.local",
					Provider: "rpc-provider",
					Status:   Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
				},
			},
			Rest: []Node{
				{
					Address:  "http://rest.local",
					Provider: "rest-provider",
					Status:   Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
				},
			},
			GRPC: []Node{
				{
					Address:  "http://grpc.local",
					Provider: "grpc-provider",
					Status:   Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
				},
			},
		},
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bts, _ := json.Marshal(seed)
		_, _ = w.Write(bts)
	}))
	t.Cleanup(srv.Close)

	rpc := make(chan Seed, 1)
	rest := make(chan Seed, 1)
	grpc := make(chan Seed, 1)

	seeder := New(Config{
		EnableRemote:        true,
		SeedRefreshInterval: time.Millisecond,
		SeedURL:             srv.URL,
		ChainID:             chainID,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), rpc, rest, grpc)
	seeder.rpcProbe = ProbeFunc(MockProbe)
	seeder.restProbe = ProbeFunc(MockProbe)
	seeder.grpcProbe = ProbeFunc(MockProbe)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	seeder.Start(ctx)

	go func() {
		time.Sleep(time.Millisecond * 500)
		cancel()
	}()

	var rpcUpdates, restUpdates, grpcUpdates atomic.Uint32

outer:
	for {
		select {
		case got := <-rpc:
			rpcUpdates.Add(1)
			require.Equal(t, seed, got)
		case got := <-rest:
			restUpdates.Add(1)
			require.Equal(t, seed, got)
		case got := <-grpc:
			grpcUpdates.Add(1)
			require.Equal(t, seed, got)
		case <-ctx.Done():
			break outer
		}
	}

	require.NotZero(t, rpcUpdates.Load())
	require.NotZero(t, restUpdates.Load())
	require.NotZero(t, grpcUpdates.Load())
}

func MockProbe(_ context.Context, _ Node) (Status, error) {
	return Status{
		Reachable:     true,
		CatchingUp:    false,
		IsLatestBlock: true,
	}, nil
}

// TestSeederProbesOnHealthInterval proves probing is decoupled from the seed-list
// fetch: the list is fetched once on the slow cadence while probes run repeatedly on
// the fast one, so a node falling behind head is caught within seconds.
func TestSeederProbesOnHealthInterval(t *testing.T) {
	chainID := "test"
	list := Seed{ChainID: chainID, APIs: Apis{RPC: []Node{{Address: "http://rpc.local", Provider: "rpc"}}}}

	var fetches atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetches.Add(1)
		bts, _ := json.Marshal(list)
		_, _ = w.Write(bts)
	}))
	t.Cleanup(srv.Close)

	var probes atomic.Int64
	countingProbe := ProbeFunc(func(_ context.Context, _ Node) (Status, error) {
		probes.Add(1)
		return Status{Reachable: true, Height: 100}, nil
	})

	rpc := make(chan Seed, 8)
	rest := make(chan Seed, 8)
	grpc := make(chan Seed, 8)

	seeder := New(Config{
		EnableRemote:          true,
		SeedRefreshInterval:   10 * time.Second, // never fires within the test window
		HealthRefreshInterval: 5 * time.Millisecond,
		SeedURL:               srv.URL,
		ChainID:               chainID,
	}, slog.New(slog.NewTextHandler(io.Discard, nil)), rpc, rest, grpc)
	seeder.rpcProbe = countingProbe

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	seeder.Start(ctx)

	go func() {
		for {
			select {
			case <-rpc:
			case <-rest:
			case <-grpc:
			case <-ctx.Done():
				return
			}
		}
	}()

	require.Eventually(t, func() bool { return probes.Load() >= 3 }, time.Second, 5*time.Millisecond,
		"probing must run repeatedly on the health interval")
	require.Equal(t, int64(1), fetches.Load(), "the seed list is fetched once, not on every probe")
}
