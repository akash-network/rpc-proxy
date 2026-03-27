package halt

import (
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/akash-network/rpc-proxy/internal/block"
	"github.com/stretchr/testify/require"
)

func TestDetector_NoTripBeforeFirstProbe(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := NewDetector(100*time.Millisecond, 10*time.Millisecond, log)

	d.check()
	require.Equal(t, StateClosed, d.State())
}

func TestDetector_TripsAfterThreshold(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := NewDetector(100*time.Millisecond, 10*time.Millisecond, log)

	bm := block.GetInstance()
	_ = bm.SetLatestBlock(100)

	// Initially closed
	d.check()
	require.Equal(t, StateClosed, d.State())
	require.False(t, d.IsHalted())

	// Wait past threshold with no block advance
	time.Sleep(150 * time.Millisecond)

	// Set same block height again (triggers lastChecked update but not lastAdvanced)
	_ = bm.SetLatestBlock(100)

	d.check()
	require.Equal(t, StateOpen, d.State())
	require.True(t, d.IsHalted())
}

func TestDetector_RecoverAfterNewBlock(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := NewDetector(100*time.Millisecond, 10*time.Millisecond, log)

	bm := block.GetInstance()
	_ = bm.SetLatestBlock(200)

	time.Sleep(150 * time.Millisecond)
	_ = bm.SetLatestBlock(200)
	d.check()
	require.Equal(t, StateOpen, d.State())

	// New block arrives
	_ = bm.SetLatestBlock(201)
	d.check()
	require.Equal(t, StateClosed, d.State())
	require.False(t, d.IsHalted())
}

func TestDetector_NoTripWhenNodesUnreachable(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := NewDetector(100*time.Millisecond, 10*time.Millisecond, log)

	bm := block.GetInstance()
	// Simulate a successful probe
	_ = bm.SetLatestBlock(400)

	// Initially closed
	d.check()
	require.Equal(t, StateClosed, d.State())

	// Wait past threshold WITHOUT any further SetLatestBlock calls.
	// This simulates all nodes becoming unreachable — no probe succeeds,
	// so neither lastAdvanced nor lastChecked get updated.
	time.Sleep(150 * time.Millisecond)

	// check() should NOT trip because lastChecked is also stale,
	// meaning no node was reachable recently.
	d.check()
	require.Equal(t, StateClosed, d.State())
	require.False(t, d.IsHalted())
}

func TestDetector_HaltMessage(t *testing.T) {
	log := slog.New(slog.NewTextHandler(os.Stdout, nil))
	d := NewDetector(50*time.Millisecond, 10*time.Millisecond, log)

	bm := block.GetInstance()
	_ = bm.SetLatestBlock(500)
	time.Sleep(60 * time.Millisecond)
	_ = bm.SetLatestBlock(500)
	d.check()

	msg := d.HaltMessage()
	require.Contains(t, msg, "network halt detected")
	require.Contains(t, msg, "500")
}

func TestState_String(t *testing.T) {
	require.Equal(t, "closed", StateClosed.String())
	require.Equal(t, "open", StateOpen.String())
	require.Equal(t, "half-open", StateHalfOpen.String())
	require.Equal(t, "unknown", State(99).String())
}
