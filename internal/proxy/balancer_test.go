package proxy

import (
	"fmt"
	"log/slog"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/seed"
)

func TestRoundRobin_NextServer(t *testing.T) {
	servers := []*Server{
		{
			name:         "a",
			Url:          nil,
			pings:        avg.Moving(1),
			successes:    nil,
			failures:     nil,
			requestCount: atomic.Int64{},
			log:          nil,
			node: seed.Node{
				Status: seed.Status{
					Reachable:     true,
					CatchingUp:    false,
					IsLatestBlock: true,
					Latency:       0,
				},
			},
		},
		{
			name:         "b",
			Url:          nil,
			pings:        avg.Moving(1),
			successes:    nil,
			failures:     nil,
			requestCount: atomic.Int64{},
			log:          nil,
			node: seed.Node{
				Status: seed.Status{
					Reachable:     true,
					CatchingUp:    false,
					IsLatestBlock: true,
					Latency:       0,
				},
			},
		},
	}

	lb := NewRoundRobin(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)
	n := 5 // Number of times to loop in each goroutine
	goroutines := 2
	var wg sync.WaitGroup

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < n; j++ {
				t.Logf("goroutine %d: ", id)
				lb.NextServer(nil)
			}
		}(i)
	}

	wg.Wait()

	if lb.round != n*goroutines {
		t.Errorf("expected %d rounds but got %d", n*goroutines, lb.round)
	}
}

func TestLatencyBased_NextServer_EmptyServers(t *testing.T) {
	lb := NewLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update([]*Server{})

	// Should return nil when no servers are available
	server := lb.NextServer(nil)
	if server != nil {
		t.Errorf("expected nil server when no servers available, got %v", server)
	}
}

func TestLatencyBased_NextServer(t *testing.T) {
	servers := []*Server{
		{
			name:         "a",
			Url:          nil,
			pings:        avg.Moving(1),
			successes:    nil,
			failures:     nil,
			requestCount: atomic.Int64{},
			log:          nil,
			node: seed.Node{
				Status: seed.Status{
					Reachable:     true,
					CatchingUp:    false,
					IsLatestBlock: true,
					Latency:       10 * time.Millisecond,
				},
			},
		},
		{
			name:         "b",
			Url:          nil,
			pings:        avg.Moving(1),
			successes:    nil,
			failures:     nil,
			requestCount: atomic.Int64{},
			log:          nil,
			node: seed.Node{
				Status: seed.Status{
					Reachable:     true,
					CatchingUp:    false,
					IsLatestBlock: true,
					Latency:       90 * time.Millisecond,
				},
			},
		},
	}

	lb := NewLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.randomizer = rand.New(rand.NewSource(0))
	lb.Update(servers)

	sum := 0.0
	for _, server := range lb.servers {
		sum += server.Rate
	}

	if sum != 1.0 {
		t.Errorf("rates are not normalized, sum should be 1, got %f", sum)
	}

	n := 5 // Number of times to loop in each goroutine
	goroutines := 2
	var wg sync.WaitGroup

	selectedServers := make(map[string]int)
	queue := make(chan *Server, 1)

	for i := 0; i < goroutines; i++ {
		wg.Add(1)
		go func(id int) {
			for j := 0; j < n; j++ {
				t.Logf("goroutine %d: ", id)
				queue <- lb.NextServer(nil)
			}
			wg.Done()
		}(i)
	}

	go func() {
		wg.Wait()
		close(queue)
	}()

	for server := range queue {
		selectedServers[server.name] = selectedServers[server.name] + 1
	}

	if math.Abs(lb.servers[0].Rate-0.9) > epsilon {
		t.Errorf("expected server \"a\" to have a rate of 0.9, got %.2f instead", lb.servers[0].Rate)
	}

	if math.Abs(lb.servers[1].Rate-0.1) > epsilon {
		t.Errorf("expected server \"b\" to have a rate of 0.1, got %.2f instead", lb.servers[1].Rate)
	}

	if selectedServers["a"] != 9 {
		t.Errorf("expected server \"a\" to be selected 9 time, got selected %d times", selectedServers["a"])
	}

	if selectedServers["b"] != 1 {
		t.Errorf("expected server \"b\" to be selected 1 time, got selected %d times", selectedServers["b"])
	}
}

func TestStickyLatencyBased_SessionAffinity(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 30*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(servers)

	// Test X-PROXY-KEY header
	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session123")

	// First request should select a server (using weighted random selection)
	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}
	// Note: Due to weighted random selection, any healthy server could be selected

	// Second request with same session ID should go to same server
	req2 := createTestRequest()
	req2.Header.Set(ProxyKeyHeader, "session123")
	server2 := lb.NextServer(req2)

	if server2.name != server1.name {
		t.Errorf("expected same server %s, got %s", server1.name, server2.name)
	}

	// Different session ID should potentially select different server (but likely same due to latency)
	req3 := createTestRequest()
	req3.Header.Set(ProxyKeyHeader, "session456")
	server3 := lb.NextServer(req3)

	if server3 == nil {
		t.Fatal("expected a server to be selected")
	}
}

func TestStickyLatencyBased_NoProxyKey(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 30*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(servers)

	// Test requests without X-PROXY-KEY header should use normal latency-based selection
	req1 := createTestRequest()
	// No X-PROXY-KEY header set

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}
	// Should select a healthy server (using weighted random selection)
	if !server1.Healthy() {
		t.Errorf("expected a healthy server, got unhealthy server %s", server1.name)
	}

	// Second request without X-PROXY-KEY should also use latency-based selection
	req2 := createTestRequest()
	server2 := lb.NextServer(req2)

	if server2 == nil {
		t.Fatal("expected a server to be selected")
	}
	if !server2.Healthy() {
		t.Errorf("expected a healthy server, got unhealthy server %s", server2.name)
	}
}

func TestStickyLatencyBased_SessionPersistence(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(servers)

	// Create session with first server
	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session123")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	// Second request with same session should go to same server
	req2 := createTestRequest()
	req2.Header.Set(ProxyKeyHeader, "session123")
	server2 := lb.NextServer(req2)

	if server2 == nil {
		t.Fatal("expected a server to be selected")
	}
	if server2.name != server1.name {
		t.Errorf("expected same server %s, got %s", server1.name, server2.name)
	}
}

func TestStickyLatencyBased_SessionTimeout(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}

	// Short session timeout for testing
	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 50*time.Millisecond)
	defer lb.Stop()

	// Override the cleanup ticker with a faster one for testing
	lb.sessionCleanupTicker.Stop()
	lb.sessionCleanupTicker = time.NewTicker(25 * time.Millisecond)
	go lb.cleanupExpiredSessions()

	lb.Update(servers)

	// Create session
	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session123")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	// Verify session exists
	lb.sessionMu.RLock()
	_, exists := lb.sessionMap["session123"]
	lb.sessionMu.RUnlock()
	if !exists {
		t.Error("session should exist")
	}

	// Wait for session to expire and cleanup to run
	time.Sleep(150 * time.Millisecond)

	// Session should be cleaned up
	lb.sessionMu.RLock()
	_, exists = lb.sessionMap["session123"]
	lb.sessionMu.RUnlock()
	if exists {
		t.Error("session should have been cleaned up")
	}
}

func TestStickyLatencyBased_CacheHitMiss(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelDebug}))
	lb := NewStickyLatencyBased(log, 100*time.Millisecond) // Short timeout for testing
	defer lb.Stop()

	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}
	lb.Update(servers)

	req, _ := http.NewRequest("GET", "/test", nil)
	req.Header.Set(ProxyKeyHeader, "cache-test")

	// First request - cache miss, should create new session
	server1 := lb.NextServer(req)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	// Verify session was created (cache populated)
	lb.sessionMu.RLock()
	cachedServer, exists := lb.sessionMap["cache-test"]
	lb.sessionMu.RUnlock()

	if !exists {
		t.Fatal("expected session to be created")
	}
	if cachedServer != server1 {
		t.Error("cached server should match returned server")
	}

	// Second request within timeout - cache hit, should return same server
	server2 := lb.NextServer(req)
	if server2 != server1 {
		t.Error("expected same server for cache hit")
	}

	// Wait for session to timeout
	time.Sleep(150 * time.Millisecond)

	// Third request after timeout - cache miss due to expiry, should trigger inline cleanup
	server3 := lb.NextServer(req)
	if server3 == nil {
		t.Fatal("expected a server to be selected after timeout")
	}

	// Verify new session was created after timeout cleanup
	lb.sessionMu.RLock()
	newCachedServer, exists := lb.sessionMap["cache-test"]
	lb.sessionMu.RUnlock()

	if !exists {
		t.Fatal("expected new session to be created after timeout")
	}
	if newCachedServer != server3 {
		t.Error("new cached server should match returned server")
	}

	// Verify the old session was cleaned up during the timeout check
	// (This tests the inline cleanup behavior we just implemented)
	if server1 == server3 {
		// If we got the same server, it should be a new session, not the old one
		// We can't directly test this without more complex session tracking,
		// but the fact that we created a new session entry confirms the old one was cleaned up
		t.Logf("Got same server but as new session (expected behavior)")
	}
}

func TestStickyLatencyBased_ProxyKeyHeader(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(servers)

	// Test X-PROXY-KEY header
	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session-priority")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	// Same X-PROXY-KEY should stick to same server
	req2 := createTestRequest()
	req2.Header.Set(ProxyKeyHeader, "session-priority")

	server2 := lb.NextServer(req2)
	if server2.name != server1.name {
		t.Errorf("expected same server %s, got %s", server1.name, server2.name)
	}
}

func TestStickyLatencyBased_ConcurrentAccess(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 30*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(servers)

	var wg sync.WaitGroup
	const numGoroutines = 10
	const requestsPerGoroutine = 20

	// Test concurrent access with multiple sessions
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			sessionID := fmt.Sprintf("session-%d", goroutineID)
			var lastServer *Server

			for j := 0; j < requestsPerGoroutine; j++ {
				req := createTestRequest()
				req.Header.Set(ProxyKeyHeader, sessionID)

				server := lb.NextServer(req)
				if server == nil {
					t.Errorf("goroutine %d: expected a server to be selected", goroutineID)
					return
				}

				// After first request, all subsequent requests should go to same server
				if j > 0 && lastServer != nil && server.name != lastServer.name {
					t.Errorf("goroutine %d: expected same server %s, got %s",
						goroutineID, lastServer.name, server.name)
					return
				}

				lastServer = server
			}
		}(i)
	}

	wg.Wait()
}

func TestStickyLatencyBased_ServerUpdate(t *testing.T) {
	initialServers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)), 5*time.Minute)
	defer lb.Stop()
	lb.Update(initialServers)

	// Create session with server1
	req := createTestRequest()
	req.Header.Set(ProxyKeyHeader, "session123")

	server1 := lb.NextServer(req)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	// Verify session was created
	lb.sessionMu.RLock()
	originalServer, sessionExists := lb.sessionMap["session123"]
	lb.sessionMu.RUnlock()
	if !sessionExists {
		t.Fatal("expected session to be created")
	}

	// Update servers - remove server1, add server3
	updatedServers := []*Server{
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 15*time.Millisecond),
	}
	lb.Update(updatedServers)

	// If the original server was removed, the session should be cleaned up
	serverWasRemoved := true
	for _, s := range updatedServers {
		if s.name == originalServer.name {
			serverWasRemoved = false
			break
		}
	}

	if serverWasRemoved {
		// Session should have been removed
		lb.sessionMu.RLock()
		_, sessionStillExists := lb.sessionMap["session123"]
		lb.sessionMu.RUnlock()
		if sessionStillExists {
			t.Error("expected session to be removed when server was removed")
		}

		// Request with same session should create a new session with an available server
		server2 := lb.NextServer(req)
		if server2 == nil {
			t.Fatal("expected a server to be selected")
		}

		// Verify the selected server is one of the available servers
		serverIsValid := false
		for _, s := range updatedServers {
			if s.name == server2.name {
				serverIsValid = true
				break
			}
		}
		if !serverIsValid {
			t.Fatalf("selected server %s is not in the updated server list", server2.name)
		}

		// Verify new session was created
		lb.sessionMu.RLock()
		newServer, newSessionExists := lb.sessionMap["session123"]
		lb.sessionMu.RUnlock()
		if !newSessionExists {
			t.Error("expected new session to be created")
		}
		if newServer == nil {
			t.Error("new session points to nil server")
		}
	}
}

// Helper functions for tests

func createTestServer(name string, latency time.Duration) *Server {
	testURL, _ := url.Parse("http://example.com")
	return &Server{
		name:         name,
		Url:          testURL,
		pings:        avg.Moving(1),
		successes:    nil,
		failures:     nil,
		requestCount: atomic.Int64{},
		log:          nil,
		node: seed.Node{
			Status: seed.Status{
				Reachable:     true,
				CatchingUp:    false,
				IsLatestBlock: true,
				Latency:       latency,
			},
		},
	}
}

func createTestRequest() *http.Request {
	req, _ := http.NewRequest("GET", "http://example.com", nil)
	return req
}
