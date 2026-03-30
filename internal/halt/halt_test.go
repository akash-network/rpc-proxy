package halt

import (
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/block"
)

func newTestDetector(t *testing.T, threshold time.Duration) (*Detector, *block.BlockManager) {
	t.Helper()
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	bm := block.NewBlockManager()
	d := NewDetector(threshold, 10*time.Millisecond, log, bm)
	return d, bm
}

func TestDetector_NoTripBeforeFirstProbe(t *testing.T) {
	d, _ := newTestDetector(t, 100*time.Millisecond)

	d.check()
	if got := d.State(); got != StateNormal {
		t.Fatalf("expected StateNormal, got %v", got)
	}
}

func TestDetector_TripsAfterThreshold(t *testing.T) {
	d, bm := newTestDetector(t, 100*time.Millisecond)

	_ = bm.SetLatestBlock(100)

	// Initially closed
	d.check()
	if d.State() != StateNormal {
		t.Fatal("expected StateNormal initially")
	}
	if d.IsHalted() {
		t.Fatal("expected IsHalted=false initially")
	}

	// Wait past threshold with no block advance
	time.Sleep(150 * time.Millisecond)

	// Set same block height again (triggers lastChecked update but not lastAdvanced)
	_ = bm.SetLatestBlock(100)

	d.check()
	if d.State() != StateHalted {
		t.Fatalf("expected StateHalted after threshold, got %v", d.State())
	}
	if !d.IsHalted() {
		t.Fatal("expected IsHalted=true after threshold")
	}
}

func TestDetector_RecoverAfterNewBlock(t *testing.T) {
	d, bm := newTestDetector(t, 100*time.Millisecond)

	_ = bm.SetLatestBlock(200)

	time.Sleep(150 * time.Millisecond)
	_ = bm.SetLatestBlock(200)
	d.check()
	if d.State() != StateHalted {
		t.Fatalf("expected StateHalted, got %v", d.State())
	}

	// New block arrives
	_ = bm.SetLatestBlock(201)
	d.check()
	if d.State() != StateNormal {
		t.Fatalf("expected StateNormal after recovery, got %v", d.State())
	}
	if d.IsHalted() {
		t.Fatal("expected IsHalted=false after recovery")
	}
}

func TestDetector_NoTripWhenNodesUnreachable(t *testing.T) {
	d, bm := newTestDetector(t, 100*time.Millisecond)

	// Simulate a successful probe
	_ = bm.SetLatestBlock(400)

	// Initially closed
	d.check()
	if d.State() != StateNormal {
		t.Fatal("expected StateNormal initially")
	}

	// Wait past threshold WITHOUT any further SetLatestBlock calls.
	// This simulates all nodes becoming unreachable — no probe succeeds,
	// so neither lastAdvanced nor lastChecked get updated.
	time.Sleep(150 * time.Millisecond)

	// check() should NOT trip because lastChecked is also stale,
	// meaning no node was reachable recently.
	d.check()
	if d.State() != StateNormal {
		t.Fatalf("expected StateNormal when nodes unreachable, got %v", d.State())
	}
	if d.IsHalted() {
		t.Fatal("expected IsHalted=false when nodes unreachable")
	}
}

func TestDetector_HaltMessage(t *testing.T) {
	d, bm := newTestDetector(t, 50*time.Millisecond)

	_ = bm.SetLatestBlock(500)
	time.Sleep(60 * time.Millisecond)
	_ = bm.SetLatestBlock(500)
	d.check()

	msg := d.HaltMessage()
	if !strings.Contains(msg, "network halt detected") {
		t.Fatalf("expected message to contain 'network halt detected', got %q", msg)
	}
	if !strings.Contains(msg, "500") {
		t.Fatalf("expected message to contain '500', got %q", msg)
	}
}

func TestState_String(t *testing.T) {
	tests := []struct {
		state State
		want  string
	}{
		{StateNormal, "normal"},
		{StateHalted, "halted"},
		{State(99), "unknown"},
	}
	for _, tt := range tests {
		if got := tt.state.String(); got != tt.want {
			t.Errorf("State(%d).String() = %q, want %q", tt.state, got, tt.want)
		}
	}
}
