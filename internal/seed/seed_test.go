package seed

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
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
			RPC: []Provider{
				{
					Address:  "http://rpc.local",
					Provider: "rpc-provider",
					Status:   Status{CatchingUp: false, Reachable: true},
				},
			},
			Rest: []Provider{
				{
					Address:  "http://rest.local",
					Provider: "rest-provider",
					Status:   Status{CatchingUp: false, Reachable: true},
				},
			},
			GRPC: []Provider{
				{
					Address:  "http://grpc.local",
					Provider: "grpc-provider",
					Status:   Status{CatchingUp: false, Reachable: true},
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
		SeedRefreshInterval: time.Millisecond,
		SeedURL:             srv.URL,
		ChainID:             chainID,
	}, slog.New(slog.NewTextHandler(os.Stdin, nil)), rpc, rest, grpc)
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

func MockProbe(_ Provider) (Status, error) {
	return Status{
		Reachable:  true,
		CatchingUp: false,
	}, nil
}
