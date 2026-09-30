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

// countingUpstream answers every request with 200 and counts the hits. Its
// transport is healthy; only its reported block height decides whether the proxy
// routes to it.
func countingUpstream(t *testing.T, hits *atomic.Int64) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.WriteString(w, "ok")
	}))
	t.Cleanup(srv.Close)
	return srv
}

func nodeAtHeight(name, addr string, height int64) seed.Node {
	return seed.Node{
		Address:  addr,
		Provider: name,
		Status:   seed.Status{Reachable: true, Height: height},
	}
}

func waitInitialized(t *testing.T, p interface{ Ready() bool }) {
	t.Helper()
	require.Eventually(t, p.Ready, time.Second, time.Millisecond)
}

func degraded(stats []ServerStat, name string) bool {
	for _, s := range stats {
		if s.Name == name {
			return s.Degraded
		}
	}
	// Not found: report not-degraded so a waiter for "degraded" does not pass vacuously
	// on an absent server.
	return false
}

// TestRoutingAvoidsLaggingNode is the regression for the tx-signer pages: a peer
// that answers fine at the transport level but has fallen behind chain head must
// stop receiving reads, so it cannot serve tx_search / state queries that miss
// txs already on chain.
func TestRoutingAvoidsLaggingNode(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var freshHits, laggingHits atomic.Int64
	fresh := countingUpstream(t, &freshHits)
	lagging := countingUpstream(t, &laggingHits)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan seed.Seed, 1)
	proxy := NewRPCProxy(ch, config.HealthConfig{
		ProxyRequestTimeout: time.Second,
		FreshnessTolerance:  2,
	}, logger, NewRoundRobin(logger), nil)
	proxy.Start(ctx)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{
		nodeAtHeight("fresh", fresh.URL, 1000),
		nodeAtHeight("lagging", lagging.URL, 980),
	}}}
	waitInitialized(t, proxy)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	for i := 0; i < 50; i++ {
		resp, err := proxySrv.Client().Get(proxySrv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	require.Zero(t, laggingHits.Load(), "a node lagging beyond tolerance must receive no traffic")
	require.Equal(t, int64(50), freshHits.Load())
}

// TestLaggingNodeReadmittedWhenCaughtUp proves the gate self-heals: once the peer
// reports back at the tip it is eligible again, without any pool rebuild.
func TestLaggingNodeReadmittedWhenCaughtUp(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var freshHits, laggingHits atomic.Int64
	fresh := countingUpstream(t, &freshHits)
	lagging := countingUpstream(t, &laggingHits)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan seed.Seed, 1)
	proxy := NewRPCProxy(ch, config.HealthConfig{
		ProxyRequestTimeout: time.Second,
		FreshnessTolerance:  2,
	}, logger, NewRoundRobin(logger), nil)
	proxy.Start(ctx)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{
		nodeAtHeight("fresh", fresh.URL, 1000),
		nodeAtHeight("lagging", lagging.URL, 980),
	}}}
	waitInitialized(t, proxy)
	require.Eventually(t, func() bool { return degraded(proxy.Stats(), "lagging") }, time.Second, time.Millisecond)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{
		nodeAtHeight("fresh", fresh.URL, 1006),
		nodeAtHeight("lagging", lagging.URL, 1006),
	}}}
	require.Eventually(t, func() bool { return !degraded(proxy.Stats(), "lagging") }, time.Second, time.Millisecond)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	for i := 0; i < 50; i++ {
		resp, err := proxySrv.Client().Get(proxySrv.URL)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	require.Positive(t, laggingHits.Load(), "a caught-up node must receive traffic again")
}

// TestStickySessionReroutesAfterRebuild covers the sticky path production actually
// uses (StickyLatencyBased, X-PROXY-KEY). When a provider's address changes and its
// server is rebuilt, a pinned session must follow the new server, not stay stuck on
// the orphaned old instance serving a frozen-healthy verdict at the stale endpoint.
func TestStickySessionReroutesAfterRebuild(t *testing.T) {
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))

	var b1, b2 atomic.Int64
	backend1 := countingUpstream(t, &b1)
	backend2 := countingUpstream(t, &b2)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := make(chan seed.Seed, 1)
	// Long session timeout so any reroute comes from pruning the rebuilt server, not
	// from the session expiring on its own.
	proxy := NewRPCProxy(ch, config.HealthConfig{
		ProxyRequestTimeout: time.Second,
		FreshnessTolerance:  2,
	}, logger, NewStickyLatencyBased(logger, time.Minute), nil)
	proxy.Start(ctx)

	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{nodeAtHeight("a", backend1.URL, 1000)}}}
	waitInitialized(t, proxy)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	send := func() {
		req, err := http.NewRequest(http.MethodGet, proxySrv.URL, nil)
		require.NoError(t, err)
		req.Header.Set(ProxyKeyHeader, "session-key")
		resp, err := proxySrv.Client().Do(req)
		require.NoError(t, err)
		_ = resp.Body.Close()
	}

	send() // pins session-key -> backend1
	require.Positive(t, b1.Load())

	// Provider "a" now maps to backend2: the server is rebuilt.
	ch <- seed.Seed{APIs: seed.Apis{RPC: []seed.Node{nodeAtHeight("a", backend2.URL, 1006)}}}
	require.Eventually(t, func() bool {
		for _, s := range proxy.Stats() {
			if s.Name == "a" {
				return s.URL == backend2.URL
			}
		}
		return false
	}, time.Second, time.Millisecond)

	before := b2.Load()
	send() // same key must now reach backend2, not the orphaned backend1 server
	require.Greater(t, b2.Load(), before, "sticky session must follow the rebuilt server, not stay pinned to the stale one")
}
