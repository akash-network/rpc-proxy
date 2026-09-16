package proxy

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestBreakerTripsAndRecoversAfterCooldown(t *testing.T) {
	base := time.Unix(0, 0)
	b := newBreaker(3, 30*time.Second)

	b.recordFailure(base)
	b.recordFailure(base)
	require.False(t, b.open(base), "must not trip before the threshold")

	b.recordFailure(base)
	require.True(t, b.open(base), "must trip on the threshold-th consecutive failure")
	require.True(t, b.open(base.Add(29*time.Second)), "must stay open during cooldown")

	require.False(t, b.open(base.Add(31*time.Second)), "must close after cooldown")
	b.recordFailure(base.Add(31 * time.Second))
	require.False(t, b.open(base.Add(31*time.Second)), "streak resets after cooldown, one failure must not re-trip")
}

func TestBreakerSuccessResetsStreak(t *testing.T) {
	base := time.Unix(0, 0)
	b := newBreaker(3, 30*time.Second)

	b.recordFailure(base)
	b.recordFailure(base)
	b.recordSuccess()
	b.recordFailure(base)
	b.recordFailure(base)
	require.False(t, b.open(base), "a success must clear the streak so two more failures do not trip")
}

func TestBreakerSuccessDoesNotReadmitBeforeCooldown(t *testing.T) {
	base := time.Unix(0, 0)
	b := newBreaker(3, 30*time.Second)

	b.recordFailure(base)
	b.recordFailure(base)
	b.recordFailure(base)
	require.True(t, b.open(base), "breaker must be open after the threshold")

	// An in-flight request that started before ejection completes successfully.
	b.recordSuccess()
	require.True(t, b.open(base.Add(10*time.Second)), "a late success must not re-admit the peer during cooldown")
	require.False(t, b.open(base.Add(31*time.Second)), "peer becomes eligible only after cooldown")
}

func TestBreakerLateFailureDoesNotExtendCooldown(t *testing.T) {
	base := time.Unix(0, 0)
	b := newBreaker(3, 30*time.Second)

	b.recordFailure(base)
	b.recordFailure(base)
	b.recordFailure(base)
	require.True(t, b.open(base), "breaker must be open after the threshold")

	// Stragglers dispatched before ejection fail during the cooldown.
	b.recordFailure(base.Add(5 * time.Second))
	b.recordFailure(base.Add(10 * time.Second))
	require.False(t, b.open(base.Add(31*time.Second)), "late failures must not push the cooldown out")
}

func TestBreakerDisabled(t *testing.T) {
	base := time.Unix(0, 0)
	b := newBreaker(0, 30*time.Second)

	for i := 0; i < 100; i++ {
		b.recordFailure(base)
	}
	require.False(t, b.open(base), "a non-positive threshold disables ejection")
}
