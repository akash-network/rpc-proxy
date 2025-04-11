package proxy

import (
	"github.com/akash-network/rpc-proxy/internal/avg"
	"github.com/akash-network/rpc-proxy/internal/config"
	"github.com/akash-network/rpc-proxy/internal/seed"
	"log/slog"
	"math"
	"math/rand"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRoundRobin_Next(t *testing.T) {
	servers := []*Server{
		{
			cfg:          config.Config{},
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
			cfg:          config.Config{},
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
				lb.Next()
			}
		}(i)
	}

	wg.Wait()

	if lb.round != n*goroutines {
		t.Errorf("expected %d rounds but got %d", n*goroutines, lb.round)
	}
}

func TestLatencyBased_Next(t *testing.T) {
	servers := []*Server{
		{
			cfg:          config.Config{},
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
			cfg:          config.Config{},
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
				queue <- lb.Next()
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
