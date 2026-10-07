package auth

import (
	"sync"
	"time"
)

// SecretStore holds server-side state that outlives a request but not the
// login it guards: passkey challenges, OIDC state and code verifiers, pending
// logins, TOTP replay marks, and attempt counters. None of it is handed to the
// client.
//
// It is in-process by design: an install is one binary and Postgres.
// With two replicas a challenge issued by one is unknown to the other and
// attempt limits loosen by the replica count; running more than one replica
// means moving this state to Postgres first.
type SecretStore struct {
	mu      sync.Mutex
	entries map[string]secretEntry
	// Now is nil for the real clock.
	Now func() time.Time
	// sweptAt bounds how often the whole map is walked. Expiry is lazy on
	// read, so an abandoned challenge is only reclaimed by a sweep.
	sweptAt time.Time
}

type secretEntry struct {
	value any
	// expiresAt is the zero time for an entry that never expires.
	expiresAt time.Time
}

// sweepInterval is how often a write walks the map for expired entries.
const sweepInterval = time.Minute

// NewSecretStore returns an empty store. The zero value is not usable.
func NewSecretStore() *SecretStore {
	return &SecretStore{entries: map[string]secretEntry{}}
}

// Set writes a value that expires after ttl. A ttl of zero or less never
// expires.
func (s *SecretStore) Set(key string, value any, ttl time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.set(key, value, ttl)
}

// Get reads a value that has not expired.
func (s *SecretStore) Get(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.get(key)
}

// Take reads and deletes in one step, so two concurrent replays of the same
// challenge cannot both see it unused. Every one-time value goes through it.
func (s *SecretStore) Take(key string) (any, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	value, ok := s.get(key)
	delete(s.entries, key)
	return value, ok
}

func (s *SecretStore) Delete(key string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, key)
}

// Claim takes a key nobody holds. False means someone already has it. This is
// what burns a TOTP step.
func (s *SecretStore) Claim(key string, ttl time.Duration) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, taken := s.get(key); taken {
		return false
	}
	s.set(key, true, ttl)
	return true
}

// Hit counts one attempt in a fixed window and returns the running total. The
// window is not extended by later attempts, so a guesser cannot hold their own
// lockout open.
func (s *SecretStore) Hit(key string, ttl time.Duration) int {
	s.mu.Lock()
	defer s.mu.Unlock()

	count := 1
	if existing, ok := s.get(key); ok {
		if previous, isInt := existing.(int); isInt {
			count = previous + 1
		}
		s.entries[key] = secretEntry{value: count, expiresAt: s.entries[key].expiresAt}
		return count
	}
	s.set(key, count, ttl)
	return count
}

// Len reports how many unexpired entries the store holds.
func (s *SecretStore) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sweep(now(s.Now), true)
	return len(s.entries)
}

// set and get assume the lock is held.

func (s *SecretStore) set(key string, value any, ttl time.Duration) {
	moment := now(s.Now)
	s.sweep(moment, false)
	entry := secretEntry{value: value}
	if ttl > 0 {
		entry.expiresAt = moment.Add(ttl)
	}
	s.entries[key] = entry
}

func (s *SecretStore) get(key string) (any, bool) {
	entry, ok := s.entries[key]
	if !ok {
		return nil, false
	}
	if !entry.expiresAt.IsZero() && !entry.expiresAt.After(now(s.Now)) {
		delete(s.entries, key)
		return nil, false
	}
	return entry.value, true
}

func (s *SecretStore) sweep(moment time.Time, force bool) {
	if !force && moment.Sub(s.sweptAt) < sweepInterval {
		return
	}
	s.sweptAt = moment
	for key, entry := range s.entries {
		if !entry.expiresAt.IsZero() && !entry.expiresAt.After(moment) {
			delete(s.entries, key)
		}
	}
}

// TakeAs is Take with the type assertion. A value of the wrong type reads as
// absent, which refuses the ceremony.
func TakeAs[T any](s *SecretStore, key string) (T, bool) {
	value, ok := s.Take(key)
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}

// GetAs is Get with the same assertion. Prefer TakeAs for anything single-use.
func GetAs[T any](s *SecretStore, key string) (T, bool) {
	value, ok := s.Get(key)
	if !ok {
		var zero T
		return zero, false
	}
	typed, ok := value.(T)
	return typed, ok
}
