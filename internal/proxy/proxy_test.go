package proxy

import (
	"context"
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
	for name, kind := range map[string]ProxyKind{
		"rpc":  RPC,
		"rest": Rest,
	} {
		t.Run(name, func(t *testing.T) {
			testProxy(t, kind)
		})
	}
}

func testProxy(tb testing.TB, kind ProxyKind) {
	srv1 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "srv1 replied")
	}))
	tb.Cleanup(srv1.Close)
	srv2 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(time.Millisecond * 500)
		_, _ = io.WriteString(w, "srv2 replied")
	}))
	tb.Cleanup(srv2.Close)
	srv3 := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))
	tb.Cleanup(srv2.Close)

	ch := make(chan seed.Seed, 1)
	proxy := New(kind, ch, config.Config{
		HealthyThreshold:                10 * time.Millisecond,
		ProxyRequestTimeout:             time.Second,
		UnhealthyServerRecoverChancePct: 1,
		HealthyErrorRateThreshold:       10,
		HealthyErrorRateBucketTimeout:   time.Second * 10,
	})

	ctx, cancel := context.WithCancel(context.Background())
	tb.Cleanup(cancel)
	proxy.Start(ctx)

	serverList := []seed.Provider{
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
	}

	ch <- seed.Seed{
		APIs: seed.Apis{
			Rest: serverList,
			RPC:  serverList,
		},
	}

	require.Eventually(tb, func() bool { return proxy.initialized.Load() }, time.Second, time.Millisecond)

	require.Len(tb, proxy.servers, 3)

	proxySrv := httptest.NewServer(proxy)
	tb.Cleanup(proxySrv.Close)

	var wg errgroup.Group
	wg.SetLimit(20)
	for i := 0; i < 100; i++ {
		wg.Go(func() error {
			tb.Log("go")
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
	require.NoError(tb, wg.Wait())

	// stop the proxy
	cancel()

	stats := proxy.Stats()
	require.Len(tb, stats, 3)

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
	require.Zero(tb, srv1Stats.ErrorRate)
	require.Zero(tb, srv2Stats.ErrorRate)
	require.Equal(tb, float64(100), srv3Stats.ErrorRate)
	require.Greater(tb, srv1Stats.Requests, srv2Stats.Requests)
	require.Greater(tb, srv2Stats.Avg, srv1Stats.Avg)
	require.False(tb, srv1Stats.Degraded)
	require.True(tb, srv2Stats.Degraded)
	require.True(tb, srv1Stats.Initialized)
	require.True(tb, srv2Stats.Initialized)
}
