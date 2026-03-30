package halt

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/akash-network/rpc-proxy/internal/block"
	"github.com/akash-network/rpc-proxy/internal/metrics"
	"github.com/prometheus/client_golang/prometheus"
)

// State represents the circuit breaker state.
type State int

const (
	// StateNormal is the normal operating state. It means the circuit breaker is closed. Requests flow through.
	StateNormal State = iota
	// StateHalted means a network halt was detected. It means the circuit breaker is open. Requests are rejected.
	StateHalted
)

func (s State) String() string {
	switch s {
	case StateNormal:
		return "normal"
	case StateHalted:
		return "halted"
	default:
		return "unknown"
	}
}

var (
	networkHalted = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "proxy_network_halted",
		Help: "Whether the network is detected as halted (1 = halted, 0 = normal)",
	})

	haltDetectedTotal = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "proxy_halt_detected_total",
		Help: "Total number of times a network halt has been detected",
	})
)

func init() {
	metrics.RegisterMetric(networkHalted)
	metrics.RegisterMetric(haltDetectedTotal)
}

// Detector monitors block production and trips a circuit breaker when the network halts.
type Detector struct {
	threshold    time.Duration
	checkPeriod  time.Duration
	log          *slog.Logger
	blockManager *block.BlockManager

	mu    sync.RWMutex
	state State
}

// NewDetector creates a halt detector.
// threshold is how long block height must be stale before declaring a halt.
// checkPeriod is how often to check for staleness.
func NewDetector(threshold time.Duration, checkPeriod time.Duration, log *slog.Logger, bm *block.BlockManager) *Detector {
	return &Detector{
		threshold:    threshold,
		checkPeriod:  checkPeriod,
		log:          log.With("component", "halt-detector"),
		blockManager: bm,
		state:        StateNormal,
	}
}

// Start begins the periodic halt check loop.
func (d *Detector) Start(ctx context.Context) {
	go func() {
		t := time.NewTicker(d.checkPeriod)
		defer t.Stop()
		for {
			select {
			case <-t.C:
				d.check()
			case <-ctx.Done():
				return
			}
		}
	}()
}

func (d *Detector) check() {
	lastAdvanced := d.blockManager.LastAdvancedAt()
	lastChecked := d.blockManager.LastCheckedAt()

	// Don't trip until we've done at least one successful probe
	if lastAdvanced.IsZero() || lastChecked.IsZero() {
		return
	}

	// If no node has been reachable recently (no successful probe within the threshold),
	// this is a connectivity issue, not a network halt. Don't trip.
	if time.Since(lastChecked) >= d.threshold {
		return
	}

	staleDuration := time.Since(lastAdvanced)
	halted := staleDuration >= d.threshold

	d.mu.Lock()
	prev := d.state
	if halted {
		if prev == StateNormal {
			d.state = StateHalted
			d.log.Warn("network halt detected",
				"last_block_advance", lastAdvanced,
				"stale_for", staleDuration,
				"block_height", d.blockManager.GetLatestBlock())
			haltDetectedTotal.Inc()
		}
	} else {
		if prev != StateNormal {
			d.state = StateNormal
			d.log.Info("network recovered",
				"last_block_advance", lastAdvanced,
				"block_height", d.blockManager.GetLatestBlock())
		}
	}
	d.mu.Unlock()

	if halted {
		networkHalted.Set(1)
	} else {
		networkHalted.Set(0)
	}
}

// State returns the current circuit breaker state.
func (d *Detector) State() State {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.state
}

// IsHalted returns true when the network is detected as halted (circuit open).
func (d *Detector) IsHalted() bool {
	return d.State() == StateHalted
}

// HaltMessage returns a human-readable message describing the current halt status.
func (d *Detector) HaltMessage() string {
	lastAdvanced := d.blockManager.LastAdvancedAt()
	return fmt.Sprintf(
		"network halt detected: no new blocks since %s (block height %d, stale for %s)",
		lastAdvanced.UTC().Format(time.RFC3339),
		d.blockManager.GetLatestBlock(),
		time.Since(lastAdvanced).Truncate(time.Second),
	)
}
