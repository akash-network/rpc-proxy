package proxy

import (
	"sync"
	"time"
)

// breaker is a per-peer circuit breaker keyed on consecutive upstream transport
// failures. It ejects a peer after threshold consecutive failures and holds it
// out for cooldown, after which the peer becomes eligible again. A threshold of
// zero or less disables ejection.
type breaker struct {
	threshold int
	cooldown  time.Duration

	mu          sync.Mutex
	consecutive int
	openUntil   time.Time
}

func newBreaker(threshold int, cooldown time.Duration) *breaker {
	return &breaker{threshold: threshold, cooldown: cooldown}
}

// recordSuccess clears the failure streak. Any completed upstream response,
// including an application-level error, proves the transport works. It does not
// close an already-open breaker: an in-flight success from a flapping peer must
// not re-admit it before the cooldown expires. open handles that on expiry.
func (b *breaker) recordSuccess() {
	if b == nil {
		return
	}
	b.mu.Lock()
	b.consecutive = 0
	b.mu.Unlock()
}

func (b *breaker) recordFailure(now time.Time) {
	if b == nil || b.threshold <= 0 {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.openUntil.IsZero() {
		if now.Before(b.openUntil) {
			// Already ejected. Ignore stragglers so they can't extend the cooldown.
			return
		}
		// Cooldown expired; start a fresh streak.
		b.consecutive = 0
		b.openUntil = time.Time{}
	}
	b.consecutive++
	if b.consecutive >= b.threshold {
		b.openUntil = now.Add(b.cooldown)
	}
}

// open reports whether the peer is currently ejected. Reaching the cooldown
// closes the breaker and resets the streak, giving the peer a fresh trial.
func (b *breaker) open(now time.Time) bool {
	if b == nil || b.threshold <= 0 {
		return false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.openUntil.IsZero() {
		return false
	}
	if now.Before(b.openUntil) {
		return true
	}
	b.consecutive = 0
	b.openUntil = time.Time{}
	return false
}
