package proxy

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestProxy(t *testing.T) {
	const chainID = "unittest"
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "srv1 replied")
	}))
	t.Cleanup(srv1.Close)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 500)
		_, _ = io.WriteString(w, "srv2 replied")
	}))
	t.Cleanup(srv2.Close)
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	t.Cleanup(srv2.Close)

	seed := seed.Seed{
		ChainID: chainID,
		APIs: seed.Apis{
			RPC: []seed.Provider{
				{
					Address:  srv1.URL,
					Provider: "srv1",
				},
				{
					Address:  srv2.URL,
					Provider: "srv2",
				},
				{
					Address:  srv3.URL,
					Provider: "srv3",
				},
			},
		},
	}

	t.Logf("%+v", seed)

	seedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		bts, _ := json.Marshal(seed)
		_, _ = w.Write(bts)
	}))
	t.Cleanup(seedSrv.Close)

	proxy := New(config.Config{
		SeedURL:                         seedSrv.URL,
		SeedRefreshInterval:             500 * time.Millisecond,
		ChainID:                         chainID,
		HealthyThreshold:                10 * time.Millisecond,
		ProxyRequestTimeout:             time.Second,
		UnhealthyServerRecoverChancePct: 1,
		HealthyErrorRateThreshold:       10,
		HealthyErrorRateBucketTimeout:   time.Second * 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	proxy.Start(ctx)

	require.Len(t, proxy.servers, 3)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	var wg errgroup.Group
	wg.SetLimit(20)
	for i := 0; i < 100; i++ {
		wg.Go(func() error {
			t.Log("go")
			req, err := http.NewRequest(http.MethodGet, proxySrv.URL, nil)
			if err != nil {
				return err
			}
			resp, err := proxySrv.Client().Do(req)
			if err != nil {
				return err
			}
			defer resp.Body.Close()
			// only two status codes accepted
			if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusTeapot {
				bts, _ := io.ReadAll(resp.Body)
				return fmt.Errorf("bad status code: %v: %s", resp.StatusCode, string(bts))
			}
			return nil
		})
	}
	require.NoError(t, wg.Wait())

	// stop the proxy
	cancel()

	stats := proxy.Stats()
	require.Len(t, stats, 3)

	var srv1Stats ServerStat
	var srv2Stats ServerStat
	var srv3Stats ServerStat
	for _, st := range stats {
		if st.Name == "srv1" {
			srv1Stats = st
		}
		if st.Name == "srv2" {
			srv2Stats = st
		}
		if st.Name == "srv3" {
			srv3Stats = st
		}
	}
	require.Zero(t, srv1Stats.ErrorRate)
	require.Zero(t, srv2Stats.ErrorRate)
	require.Equal(t, float64(100), srv3Stats.ErrorRate)
	require.Greater(t, srv1Stats.Requests, srv2Stats.Requests)
	require.Greater(t, srv2Stats.Avg, srv1Stats.Avg)
	require.False(t, srv1Stats.Degraded)
	require.True(t, srv2Stats.Degraded)
	require.True(t, srv1Stats.Initialized)
	require.True(t, srv2Stats.Initialized)
}
