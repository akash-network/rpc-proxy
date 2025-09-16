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
	}, logger, NewRoundRobin(logger))

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
	}, logger, NewRoundRobin(logger))

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
	srv := &Server{Url: targetURL}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
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
	srv := &Server{Url: targetURL}
	logger := slog.New(slog.NewTextHandler(io.Discard, nil))
	proxy := newRedirectFollowingReverseProxy(srv, logger, "rest")

	w := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/test", nil)
	testErr := errors.New("test error")

	proxy.ErrorHandler(w, req, testErr)

	require.Equal(t, http.StatusInternalServerError, w.Code)
	require.Contains(t, w.Body.String(), "could not proxy request")
}

func TestDoUpdate(t *testing.T) {
	tests := []struct {
		name            string
		providers       []seed.Node
		wantErr         bool
		expectedServers int
	}{
		{
			name: "Add new server",
			providers: []seed.Node{
				{
					Provider: "test1",
					Address:  "example.com",
					Status: seed.Status{
						CatchingUp:    false,
						Reachable:     true,
						IsLatestBlock: true,
					},
				},
			},
			wantErr:         false,
			expectedServers: 1,
		},
		{
			name: "Remove unhealthy server",
			providers: []seed.Node{
				{
					Provider: "test2",
					Address:  "example.com",
					Status: seed.Status{
						CatchingUp:    false,
						Reachable:     true,
						IsLatestBlock: true,
					},
				},
				{
					Provider: "test3",
					Address:  "example.com",
					Status: seed.Status{
						CatchingUp:    true,
						Reachable:     false,
						IsLatestBlock: false,
					},
				},
			},
			wantErr:         false,
			expectedServers: 1, // Unhealthy server should be removed
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Proxy{
				cfg:     config.HealthConfig{},
				log:     slog.Default(),
				servers: []*Server{},
				lb:      &MockLoadBalancer{},
			}

			err := p.doUpdate(tt.providers)
			if (err != nil) != tt.wantErr {
				t.Errorf("doUpdate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if !tt.wantErr {
				if !p.initialized.Load() {
					t.Error("proxy should be initialized after successful update")
				}
				require.Len(t, p.servers, tt.expectedServers)
			}
		})
	}
}

type MockLoadBalancer struct{}

func (m *MockLoadBalancer) Update(servers []*Server) {}

func (m *MockLoadBalancer) NextServer(*http.Request) *Server {
	return nil
}
