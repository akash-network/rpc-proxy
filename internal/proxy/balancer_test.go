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

func TestRoundRobin_AllUnhealthyReturnsNil(t *testing.T) {
	unhealthy := func(name string) *Server {
		return &Server{
			name:  name,
			pings: avg.Moving(1),
			node:  seed.Node{Status: seed.Status{Reachable: false}},
		}
	}
	lb := NewRoundRobin(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update([]*Server{unhealthy("a"), unhealthy("b")})
	if s := lb.NextServer(nil); s != nil {
		t.Fatalf("expected nil when all peers are unhealthy, got %s", s.name)
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

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session123")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

	req2 := createTestRequest()
	req2.Header.Set(ProxyKeyHeader, "session123")
	server2 := lb.NextServer(req2)

	if server2.name != server1.name {
		t.Errorf("expected same server %s, got %s", server1.name, server2.name)
	}

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

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	req1 := createTestRequest()

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}
	if !server1.Healthy() {
		t.Errorf("expected a healthy server, got unhealthy server %s", server1.name)
	}

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

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session123")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

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

func TestStickyLatencyBased_ProxyKeyHeader(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	req1 := createTestRequest()
	req1.Header.Set(ProxyKeyHeader, "session-priority")

	server1 := lb.NextServer(req1)
	if server1 == nil {
		t.Fatal("expected a server to be selected")
	}

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

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	var wg sync.WaitGroup
	const numGoroutines = 10
	const requestsPerGoroutine = 20

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

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(initialServers)

	req := createTestRequest()
	req.Header.Set(ProxyKeyHeader, "session123")

	pinned := lb.NextServer(req)
	if pinned == nil {
		t.Fatal("expected a server to be selected")
	}

	updatedServers := []*Server{
		createTestServer("server3", 15*time.Millisecond),
		createTestServer("server4", 25*time.Millisecond),
	}
	lb.Update(updatedServers)

	afterUpdate := lb.NextServer(req)
	if afterUpdate == nil {
		t.Fatal("expected a server to be selected after update")
	}
	if afterUpdate.name == pinned.name {
		t.Fatalf("pinned server %s should no longer be selectable after removal", pinned.name)
	}

	valid := false
	for _, s := range updatedServers {
		if s.name == afterUpdate.name {
			valid = true
			break
		}
	}
	if !valid {
		t.Fatalf("selected server %s is not in the updated server list", afterUpdate.name)
	}

	repeat := lb.NextServer(req)
	if repeat.name != afterUpdate.name {
		t.Errorf("expected stable pick %s across repeated calls, got %s", afterUpdate.name, repeat.name)
	}
}

func TestStickyLatencyBased_DeterministicAcrossInstances(t *testing.T) {
	names := []struct {
		name    string
		latency time.Duration
	}{
		{"server1", 10 * time.Millisecond},
		{"server2", 20 * time.Millisecond},
		{"server3", 30 * time.Millisecond},
		{"server4", 5 * time.Millisecond},
	}

	build := func() *StickyLatencyBased {
		lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
		servers := make([]*Server, len(names))
		for i, n := range names {
			servers[i] = createTestServer(n.name, n.latency)
		}
		lb.Update(servers)
		return lb
	}

	replicaA := build()
	replicaB := build()

	for i := 0; i < 20; i++ {
		key := fmt.Sprintf("key-%d", i)
		req := createTestRequest()
		req.Header.Set(ProxyKeyHeader, key)

		serverA := replicaA.NextServer(req)
		serverB := replicaB.NextServer(req)

		if serverA == nil || serverB == nil {
			t.Fatalf("key %s: expected both replicas to select a server", key)
		}
		if serverA.name != serverB.name {
			t.Errorf("key %s: replica A picked %s, replica B picked %s", key, serverA.name, serverB.name)
		}
	}
}

func TestStickyLatencyBased_KeyDistribution(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 30*time.Millisecond),
		createTestServer("server4", 5*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	counts := make(map[string]int)
	const numKeys = 1000

	for i := 0; i < numKeys; i++ {
		key := fmt.Sprintf("key-%d", i)
		req := createTestRequest()
		req.Header.Set(ProxyKeyHeader, key)

		first := lb.NextServer(req)
		if first == nil {
			t.Fatalf("key %s: expected a server to be selected", key)
		}
		second := lb.NextServer(req)
		if second.name != first.name {
			t.Errorf("key %s: expected repeatable pick %s, got %s", key, first.name, second.name)
		}
		counts[first.name]++
	}

	for _, s := range servers {
		if counts[s.name] == 0 {
			t.Errorf("server %s received no keys out of %d", s.name, numKeys)
		}
	}
}

func TestStickyLatencyBased_SkipsUnhealthy(t *testing.T) {
	servers := []*Server{
		createTestServer("server1", 10*time.Millisecond),
		createTestServer("server2", 20*time.Millisecond),
		createTestServer("server3", 30*time.Millisecond),
		createTestServer("server4", 5*time.Millisecond),
	}

	lb := NewStickyLatencyBased(slog.New(slog.NewTextHandler(os.Stdout, nil)))
	lb.Update(servers)

	req := createTestRequest()
	req.Header.Set(ProxyKeyHeader, "session-unhealthy-test")

	original := lb.NextServer(req)
	if original == nil {
		t.Fatal("expected a server to be selected")
	}

	var target *Server
	for _, s := range servers {
		if s.name == original.name {
			target = s
			break
		}
	}
	if target == nil {
		t.Fatalf("could not locate selected server %s in server list", original.name)
	}

	healthyNode := target.node
	target.node = seed.Node{Status: seed.Status{Reachable: false}}

	rerouted := lb.NextServer(req)
	if rerouted == nil {
		t.Fatal("expected a healthy server to be selected after marking the pin unhealthy")
	}
	if rerouted.name == original.name {
		t.Fatalf("expected key to route away from unhealthy server %s", original.name)
	}
	if !rerouted.Healthy() {
		t.Fatalf("rerouted server %s is not healthy", rerouted.name)
	}

	target.node = healthyNode

	restored := lb.NextServer(req)
	if restored.name != original.name {
		t.Errorf("expected original pick %s restored, got %s", original.name, restored.name)
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
