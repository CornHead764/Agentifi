package store

import (
	"context"
	"sync"
)

// The data-directory instance lock excludes other processes. Named locks
// serialize overlapping jobs within this Store and all its scoped clones.
func (s *Store) namedLock(ctx context.Context, key string, wait bool) (func(), bool, error) {
	if _, err := s.server(); err != nil {
		return nil, false, err
	}
	state := s.sqlite
	state.mu.Lock()
	if state.namedLocks == nil {
		state.namedLocks = make(map[string]chan struct{})
	}
	lock := state.namedLocks[key]
	if lock == nil {
		lock = make(chan struct{}, 1)
		state.namedLocks[key] = lock
	}
	state.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	if wait {
		select {
		case lock <- struct{}{}:
		case <-ctx.Done():
			return nil, false, ctx.Err()
		}
	} else {
		select {
		case lock <- struct{}{}:
		default:
			return nil, false, nil
		}
	}
	var once sync.Once
	return func() { once.Do(func() { <-lock }) }, true, nil
}

// TryNamedLock skips a job if another holder already owns its key.
func (s *Store) TryNamedLock(ctx context.Context, key string) (func(), bool, error) {
	return s.namedLock(ctx, key, false)
}

// NamedLock waits for the key or for cancellation.
func (s *Store) NamedLock(ctx context.Context, key string) (func(), error) {
	release, _, err := s.namedLock(ctx, key, true)
	return release, err
}
