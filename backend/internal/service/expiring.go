package service

import (
	"sync"
	"time"
)

// expiring is an in-memory map whose entries lapse at a time given when each
// is stored; a zero time never lapses. A lapsed entry is never returned, and
// is dropped on the next write or walk. The zero value is ready to use and safe
// for concurrent use.
//
// It holds what a process keeps between one request and the next and must not
// outlive a restart or reach the database: who started a sign-in, a typed password
// on its way to being sealed, a mailed code.
type expiring[K comparable, V any] struct {
	mu      sync.Mutex
	entries map[K]expiringEntry[V]
}

type expiringEntry[V any] struct {
	value V
	until time.Time
}

func (e expiringEntry[V]) lapsed(now time.Time) bool {
	return !e.until.IsZero() && now.After(e.until)
}

// Put stores value under key until the given time.
func (m *expiring[K, V]) Put(key K, value V, until time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sweep(time.Now())
	if m.entries == nil {
		m.entries = map[K]expiringEntry[V]{}
	}
	m.entries[key] = expiringEntry[V]{value: value, until: until}
}

func (m *expiring[K, V]) Get(key K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.live(key)
}

// Take is Get that also removes the entry.
func (m *expiring[K, V]) Take(key K) (V, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	value, ok := m.live(key)
	delete(m.entries, key)
	return value, ok
}

func (m *expiring[K, V]) Delete(key K) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.entries, key)
}

// Each calls fn with every live entry until fn returns false. It walks a copy,
// so fn may call back into the map.
func (m *expiring[K, V]) Each(fn func(key K, value V) bool) {
	m.mu.Lock()
	m.sweep(time.Now())
	keys := make([]K, 0, len(m.entries))
	values := make([]V, 0, len(m.entries))
	for key, entry := range m.entries {
		keys = append(keys, key)
		values = append(values, entry.value)
	}
	m.mu.Unlock()
	for i := range keys {
		if !fn(keys[i], values[i]) {
			return
		}
	}
}

func (m *expiring[K, V]) live(key K) (V, bool) {
	entry, ok := m.entries[key]
	if !ok || entry.lapsed(time.Now()) {
		var zero V
		return zero, false
	}
	return entry.value, true
}

func (m *expiring[K, V]) sweep(now time.Time) {
	for key, entry := range m.entries {
		if entry.lapsed(now) {
			delete(m.entries, key)
		}
	}
}
