package proxy

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"github.com/stretchr/testify/require"
)

// resettingUpstream answers by hijacking and closing the connection without a
// response, which surfaces to the proxy transport as a reset stream. This is the
// AKT-733 failure mode: a peer that probes healthy but never completes a proxied
// request.
func resettingUpstream(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		hj, ok := w.(http.Hijacker)
		require.True(t, ok)
		conn, _, err := hj.Hijack()
		require.NoError(t, err)
		_ = conn.Close()
	}))
	t.Cleanup(srv.Close)
	return srv
}

func healthyNode(name, addr string) seed.Node {
	return seed.Node{
		Address:  addr,
		Provider: name,
		Status:   seed.Status{Reachable: true, IsLatestBlock: true},
	}
}

func TestEjectsPeerOnConsecutiveTransportFailures(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var goodHits, badHits atomic.Int64
	good := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		goodHits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(good.Close)
	bad := resettingUpstream(t, &badHits)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan seed.Seed, 1)
	proxy := NewRPCProxy(ch, config.HealthConfig{
		ProxyRequestTimeout: time.Second,
		EjectionThreshold:   5,
		EjectionCooldown:    time.Minute,
	}, logger, NewStickyLatencyBased(logger, time.Minute), nil)
	proxy.Start(ctx)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{
		healthyNode("good", good.URL),
		healthyNode("bad", bad.URL),
	}}}
	require.Eventually(t, func() bool { return proxy.initialized.Load() }, time.Second, time.Millisecond)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	send := func() {
		req, err := http.NewRequest(http.MethodGet, proxySrv.URL, nil)
		require.NoError(t, err)
		resp, err := proxySrv.Client().Do(req)
		if err == nil {
			_ = resp.Body.Close()
		}
	}

	for i := 0; i < 100; i++ {
		send()
	}

	require.Eventually(t, func() bool {
		for _, s := range proxy.Stats() {
			if s.Name == "bad" {
				return s.Degraded
			}
		}
		return false
	}, 2*time.Second, 10*time.Millisecond, "bad peer must be ejected after consecutive transport failures")

	drained := badHits.Load()
	for i := 0; i < 50; i++ {
		send()
	}
	require.Equal(t, drained, badHits.Load(), "ejected peer must receive no new requests")
	require.Positive(t, goodHits.Load())
}

func TestProxyRequestTimeoutCutsStalledUpstream(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	release := make(chan struct{})
	stalled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-release:
		case <-r.Context().Done():
		}
	}))
	t.Cleanup(func() { close(release); stalled.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan seed.Seed, 1)
	proxy := NewRPCProxy(ch, config.HealthConfig{
		ProxyRequestTimeout: 200 * time.Millisecond,
	}, logger, NewStickyLatencyBased(logger, time.Minute), nil)
	proxy.Start(ctx)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{healthyNode("stalled", stalled.URL)}}}
	require.Eventually(t, func() bool { return proxy.initialized.Load() }, time.Second, time.Millisecond)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	client := proxySrv.Client()
	client.Timeout = 3 * time.Second
	start := time.Now()
	resp, err := client.Get(proxySrv.URL)
	require.NoError(t, err, "request must be cut by the proxy, not hang until the client timeout")
	defer resp.Body.Close()

	require.Equal(t, http.StatusInternalServerError, resp.StatusCode)
	require.Less(t, time.Since(start), time.Second, "request must be cut near the proxy timeout")
}
