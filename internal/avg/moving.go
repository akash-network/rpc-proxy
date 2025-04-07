// Package avg provides a thread-safe moving average calculator for time.Duration values.
package avg

import (
	"sync"
	"time"
)

// Moving creates and returns a new MovingAverage with the specified window size.
//
// The window determines how many recent duration values will be considered
// when calculating the moving average.
func Moving(window int) *MovingAverage {
	return &MovingAverage{
		window: window,
	}
}

// MovingAverage computes a moving average over a fixed-size sliding window
// of time.Duration values. It is safe for concurrent use.
type MovingAverage struct {
	mu        sync.Mutex      // protects durations and sum
	window    int             // max number of durations to keep in the window
	durations []time.Duration // recent durations within the window
	sum       time.Duration   // sum of durations in the window

	avgMu   sync.RWMutex  // protects access to lastAvg
	lastAvg time.Duration // last computed average
}

// Reset clears all recorded durations and resets the moving average to zero.
func (m *MovingAverage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.sum = 0
	m.avgMu.Lock()
	m.lastAvg = 0
	m.avgMu.Unlock()
	m.durations = []time.Duration{}
}

// Last returns the most recently computed moving average without updating it.
//
// This method is safe to call concurrently.
func (m *MovingAverage) Last() time.Duration {
	m.avgMu.RLock()
	defer m.avgMu.RUnlock()
	return m.lastAvg
}

// Next adds a new duration value and updates the moving average.
//
// If the window is already full, the oldest duration is removed before
// the new value is added. The method returns the updated moving average.
//
// This method is safe to call concurrently.
func (m *MovingAverage) Next(d time.Duration) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()

	if len(m.durations) < m.window {
		m.sum += d
		m.durations = append(m.durations, d)
	} else {
		m.sum -= m.durations[0]
		m.durations = m.durations[1:]
		m.sum += d
		m.durations = append(m.durations, d)
	}

	m.avgMu.Lock()
	m.lastAvg = time.Duration(int(m.sum.Nanoseconds()) / len(m.durations))
	m.avgMu.Unlock()

	return m.lastAvg
}
