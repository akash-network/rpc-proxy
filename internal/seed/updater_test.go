package seed

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
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
				},
			},
			Rest: []Provider{
				{
					Address:  "http://rest.local",
					Provider: "rest-provider",
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

	up := New(config.Config{
		SeedRefreshInterval: time.Millisecond,
		SeedURL:             srv.URL,
		ChainID:             chainID,
	}, rpc, rest)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	up.Start(ctx)

	go func() {
		time.Sleep(time.Millisecond * 500)
		cancel()
	}()

	var rpcUpdates, restUpdates atomic.Uint32

outer:
	for {
		select {
		case got := <-rpc:
			rpcUpdates.Add(1)
			require.Equal(t, seed, got)
		case got := <-rest:
			restUpdates.Add(1)
			require.Equal(t, seed, got)
		case <-ctx.Done():
			break outer
		}
	}

	require.NotZero(t, rpcUpdates.Load())
	require.NotZero(t, restUpdates.Load())
}
