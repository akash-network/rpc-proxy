package ttlslice

import (
	"sync"
	"time"
)

type item[T any] struct {
	value  T
	expiry time.Time
}

func (i item[V]) isExpired() bool {
	return time.Now().After(i.expiry)
}

func New[T any]() *Slice[T] {
	m := &Slice[T]{
		items: []item[T]{},
	}

	go func() {
		for range time.Tick(time.Second) {
			var newItems []item[T]
			m.mu.Lock()
			for _, v := range m.items {
				if v.isExpired() {
					continue
				}
				newItems = append(newItems, v)
			}
			m.items = newItems
			m.mu.Unlock()
		}
	}()

	return m
}

type Slice[T any] struct {
	items []item[T]
	mu    sync.Mutex
}

func (m *Slice[T]) Append(t T, ttl time.Duration) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.items = append(m.items, item[T]{
		value:  t,
		expiry: time.Now().Add(ttl),
	})
}

func (m *Slice[T]) List() []T {
	var tt []T
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.items {
		if t.isExpired() {
			continue
		}
		tt = append(tt, t.value)
	}
	return tt
}
