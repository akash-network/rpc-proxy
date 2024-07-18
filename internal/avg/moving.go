package avg

import (
	"sync"
	"time"
)

func Moving(window int) *MovingAverage {
	return &MovingAverage{
		window: window,
	}
}

type MovingAverage struct {
	mu        sync.Mutex
	window    int
	durations []time.Duration
	sum       time.Duration
	avgMu     sync.RWMutex
	lastAvg   time.Duration
}

func (m *MovingAverage) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sum = 0
	m.lastAvg = 0
	m.durations = []time.Duration{}
}

func (m *MovingAverage) Last() time.Duration {
	m.avgMu.RLock()
	defer m.avgMu.RUnlock()
	return m.lastAvg
}

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
