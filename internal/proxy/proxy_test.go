package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"github.com/stretchr/testify/require"
	"golang.org/x/sync/errgroup"
)

func TestRPCProxy(t *testing.T) {
	serverList := generateServerList(t)

	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan seed.Seed, 1)
	proxy := NewRPCProxy(ch, config.HealthConfig{
		HealthyThreshold:    10 * time.Millisecond,
		ProxyRequestTimeout: time.Second,
	}, logger, NewRoundRobin(logger), nil)

	proxy.Start(ctx)

	sendSeed(ch, serverList)

	require.Eventually(t, func() bool { return proxy.initialized.Load() }, time.Second, time.Millisecond)

	require.Len(t, proxy.servers, 3)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	generateProxyTraffic(t, proxySrv)

	// stop the proxy
	cancel()

	stats := proxy.Stats()
	require.Len(t, stats, 3)
}

func TestRestProxy(t *testing.T) {
	serverList := generateServerList(t)
	logger := slog.New(slog.NewTextHandler(os.Stdout, nil))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	ch := make(chan seed.Seed, 1)
	proxy := NewRestProxy(ch, config.HealthConfig{
		HealthyThreshold:    10 * time.Millisecond,
		ProxyRequestTimeout: time.Second,
	}, logger, NewRoundRobin(logger), nil)

	proxy.Start(ctx)

	sendSeed(ch, serverList)

	require.Eventually(t, func() bool { return proxy.initialized.Load() }, time.Second, time.Millisecond)

	require.Len(t, proxy.servers, 3)

	proxySrv := httptest.NewServer(proxy)
	t.Cleanup(proxySrv.Close)

	generateProxyTraffic(t, proxySrv)

	// stop the proxy
	cancel()
}

func generateServerList(t *testing.T) []seed.Node {
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
	t.Cleanup(srv3.Close)

	serverList := []seed.Node{
		{
			Address:  srv1.URL,
			Provider: "srv1",
			Status:   seed.Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
		},
		{
			Address:  srv2.URL,
			Provider: "srv2",
			Status:   seed.Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
		},
		{
			Address:  srv3.URL,
			Provider: "srv3",
			Status:   seed.Status{CatchingUp: false, Reachable: true, IsLatestBlock: true},
		},
	}
	return serverList
}

func generateProxyTraffic(t *testing.T, proxySrv *httptest.Server) {
	var wg errgroup.Group
	wg.SetLimit(20)
	for i := 0; i < 100; i++ {
		wg.Go(func() error {
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
}

func sendSeed(ch chan seed.Seed, serverList []seed.Node) {
	ch <- seed.Seed{
		APIs: seed.Apis{
			Rest: serverList,
			RPC:  serverList,
			GRPC: serverList,
		},
	}
}

func TestNewReverseProxy(t *testing.T) {
	tests := []struct {
		name       string
		serverURL  string
		reqPath    string
		wantScheme string
		wantHost   string
		wantPath   string
	}{
		{
			name:       "basic proxy test",
			serverURL:  "http://node.com/base",
			reqPath:    "/test",
			wantScheme: "http",
			wantHost:   "node.com",
			wantPath:   "/base/test",
		},
		{
			name:       "https proxy test",
			serverURL:  "https://api.node.com",
			reqPath:    "/v1/data",
			wantScheme: "https",
			wantHost:   "api.node.com",
			wantPath:   "/v1/data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test server
			targetURL, err := url.Parse(tt.serverURL)
			require.NoError(t, err)

			srv := &Server{
				Url:  targetURL,
				name: "test-server",
			}

			logger := slog.New(slog.NewTextHandler(io.Discard, nil))
			proxy := newRedirectFollowingReverseProxy(srv, logger, "rest")

			// Create test request
			req := httptest.NewRequest("GET", tt.reqPath, nil)

			// Test Director function
			proxy.Director(req)

			require.Equal(t, tt.wantScheme, req.URL.Scheme)
			require.Equal(t, tt.wantHost, req.URL.Host)
			require.Equal(t, tt.wantPath, req.URL.Path)
		})
	}
}

func TestReverseProxy_ModifyResponse(t *testing.T) {
	targetURL, _ := url.Parse("http://node.com")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer("node", targetURL, logger, seed.Node{}, newBreaker(0, 0))
	proxy := newRedirectFollowingReverseProxy(srv, logger, "rest")

	resp := &http.Response{Header: make(http.Header)}
	resp.Header.Set("Access-Control-Allow-Origin", "*")
	resp.Header.Set("Access-Control-Allow-Methods", "GET,POST")
	resp.Header.Set("Access-Control-Allow-Headers", "Content-Type")

	err := proxy.ModifyResponse(resp)
	require.NoError(t, err)

	require.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Methods"))
	require.Empty(t, resp.Header.Get("Access-Control-Allow-Headers"))
}

func TestReverseProxy_ErrorHandler(t *testing.T) {
	targetURL, _ := url.Parse("http://node.com")
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := newServer("node", targetURL, logger, seed.Node{}, newBreaker(0, 0))
	proxy := newRedirectFollowingReverseProxy(srv, logger, "rest")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	testErr := errors.New("test error")

	proxy.ErrorHandler(w, req, testErr)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "could not proxy request")
}

func TestDoUpdate(t *testing.T) {
	// Servers stay in the pool as long as they are in the seed list. Whether they
	// receive traffic is decided by Healthy, not by pool membership, so an unhealthy
	// or lagging peer keeps its breaker and stats instead of being torn down.
	tests := []struct {
		name          string
		tolerance     int64
		providers     []seed.Node
		wantServers   int
		wantUnhealthy []string
	}{
		{
			name:      "keeps every listed server",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: true, Height: 100}},
			},
			wantServers: 2,
		},
		{
			name:      "unreachable or catching-up peer stays but is gated",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: false, CatchingUp: true}},
			},
			wantServers:   2,
			wantUnhealthy: []string{"b"},
		},
		{
			name:      "peer lagging beyond tolerance is gated",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: true, Height: 90}},
			},
			wantServers:   2,
			wantUnhealthy: []string{"b"},
		},
		{
			name:      "single node is its own tip and never gated",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 500}},
			},
			wantServers: 1,
		},
		{
			name:      "negative tolerance is clamped so the tip is never gated",
			tolerance: -5,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: true, Height: 100}},
			},
			wantServers: 2,
		},
		{
			name:      "peer exactly at the tolerance boundary stays fresh",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: true, Height: 98}},
			},
			wantServers: 2, // b at tip-tolerance is still fresh (>=)
		},
		{
			name:      "catching-up peer ahead of the pool does not set the tip",
			tolerance: 2,
			providers: []seed.Node{
				{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, CatchingUp: true, Height: 1010}},
				{Provider: "b", Address: "b.example.com", Status: seed.Status{Reachable: true, Height: 1000}},
			},
			wantServers:   2,
			wantUnhealthy: []string{"a"}, // b must stay healthy; a (catching up) must not gate it
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Proxy{
				cfg:     config.HealthConfig{FreshnessTolerance: tt.tolerance},
				log:     slog.Default(),
				servers: []*Server{},
				lb:      &MockLoadBalancer{},
			}

			p.doUpdate(tt.providers)
			require.True(t, p.initialized.Load())
			require.Len(t, p.servers, tt.wantServers)

			unhealthy := make(map[string]bool, len(tt.wantUnhealthy))
			for _, name := range tt.wantUnhealthy {
				unhealthy[name] = true
			}
			for _, s := range p.servers {
				if unhealthy[s.name] {
					require.False(t, s.Healthy(), "server %q should be gated", s.name)
				} else {
					require.True(t, s.Healthy(), "server %q should be healthy", s.name)
				}
			}
		})
	}
}

func TestDoUpdatePreservesServerAcrossCycles(t *testing.T) {
	// A peer that stays in the seed list must keep the same Server instance across
	// health cycles so its transport breaker does not reset every few seconds.
	p := &Proxy{
		cfg:     config.HealthConfig{FreshnessTolerance: 2, EjectionThreshold: 1, EjectionCooldown: time.Minute},
		log:     slog.Default(),
		servers: []*Server{},
		lb:      &MockLoadBalancer{},
	}

	provider := seed.Node{Provider: "a", Address: "a.example.com", Status: seed.Status{Reachable: true, Height: 100}}
	p.doUpdate([]seed.Node{provider})
	require.Len(t, p.servers, 1)
	first := p.servers[0]

	first.recordFailure() // opens the breaker at threshold 1
	require.False(t, first.Healthy(), "breaker should be open after a failure")

	provider.Status.Height = 106
	p.doUpdate([]seed.Node{provider})
	require.Len(t, p.servers, 1)
	require.Same(t, first, p.servers[0], "server instance must be reused across cycles")
	require.False(t, p.servers[0].Healthy(), "reused server must keep its open breaker")
}

func TestDoUpdateRebuildsOnAddressChange(t *testing.T) {
	// A provider whose seed-list address changes must have its server rebuilt so
	// traffic follows the new endpoint instead of the stale creation-time URL.
	p := &Proxy{
		cfg:     config.HealthConfig{FreshnessTolerance: 2},
		log:     slog.Default(),
		servers: []*Server{},
		lb:      &MockLoadBalancer{},
	}

	p.doUpdate([]seed.Node{
		{Provider: "a", Address: "a1.example.com", Status: seed.Status{Reachable: true, Height: 100}},
	})
	require.Len(t, p.servers, 1)
	require.Equal(t, "https://a1.example.com", p.servers[0].Url.String())
	first := p.servers[0]

	p.doUpdate([]seed.Node{
		{Provider: "a", Address: "a2.example.com", Status: seed.Status{Reachable: true, Height: 101}},
	})
	require.Len(t, p.servers, 1)
	require.Equal(t, "https://a2.example.com", p.servers[0].Url.String())
	require.NotSame(t, first, p.servers[0], "server must be rebuilt when its address changes")
}

func TestDoUpdateSkipsUnparseableAddress(t *testing.T) {
	// One unparseable address must be skipped, not abort the whole update.
	p := &Proxy{
		cfg:     config.HealthConfig{FreshnessTolerance: 2},
		log:     slog.Default(),
		servers: []*Server{},
		lb:      &MockLoadBalancer{},
	}

	p.doUpdate([]seed.Node{
		{Provider: "good", Address: "good.example.com", Status: seed.Status{Reachable: true, Height: 100}},
		{Provider: "bad", Address: "http://\x7f", Status: seed.Status{Reachable: true, Height: 100}},
	})
	require.Len(t, p.servers, 1)
	require.Equal(t, "good", p.servers[0].name)
}

type MockLoadBalancer struct{}

func (m *MockLoadBalancer) Update(servers []*Server) {}

func (m *MockLoadBalancer) NextServer(*http.Request) *Server {
	return nil
}
