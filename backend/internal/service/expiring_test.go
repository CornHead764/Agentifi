package service

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestAnExpiringEntryIsGoneOnceItLapses(t *testing.T) {
	var m expiring[string, int]
	m.Put("lapsed", 1, time.Now().Add(-time.Second))
	m.Put("live", 2, time.Now().Add(time.Minute))
	m.Put("kept", 3, time.Time{})

	_, found := m.Get("lapsed")
	require.False(t, found)
	value, found := m.Get("live")
	require.True(t, found)
	require.Equal(t, 2, value)

	seen := map[string]int{}
	m.Each(func(key string, value int) bool {
		seen[key] = value
		return true
	})
	require.Equal(t, map[string]int{"live": 2, "kept": 3}, seen, "a zero time never lapses")
	require.NotContains(t, m.entries, "lapsed", "a walk drops what lapsed")
}

func TestTakingAnExpiringEntryRemovesIt(t *testing.T) {
	var m expiring[string, string]
	m.Put("session", "secret", time.Now().Add(time.Minute))
	value, found := m.Take("session")
	require.True(t, found)
	require.Equal(t, "secret", value)
	_, found = m.Take("session")
	require.False(t, found, "a value is taken once")

	m.Put("lapsed", "secret", time.Now().Add(-time.Second))
	_, found = m.Take("lapsed")
	require.False(t, found)
	require.Empty(t, m.entries)
}

func TestAnExpiringMapWalkMayWriteBack(t *testing.T) {
	var m expiring[int, int]
	for i := range 10 {
		m.Put(i, i, time.Time{})
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			m.Each(func(key, _ int) bool {
				m.Delete(key)
				return true
			})
		}()
	}
	wg.Wait()
	require.Empty(t, m.entries)
}
