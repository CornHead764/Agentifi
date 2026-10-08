package store

import (
	"context"
	"sync"
)

// Named locks for runs that must not overlap, such as the scheduler and "Sync
// now" picking the same connection: concurrent syncs race the external-id
// unique constraint, and the loser's ConnectionActive write can erase the
// winner's rate-limit park.
//
// They are in-process because one process owns the database file. Every Store
// derived from one Open (a transaction's, a WithCipher copy) shares them.

type namedLocks struct {
	mu   sync.Mutex
	held map[string]chan struct{}
}

func newNamedLocks() *namedLocks {
	return &namedLocks{held: map[string]chan struct{}{}}
}

// TryNamedLock takes the lock named by key, if it is free. ok is false when
// another holder has it; the caller skips rather than queues.
func (s *Store) TryNamedLock(ctx context.Context, key string) (release func(), ok bool, err error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	s.locks.mu.Lock()
	defer s.locks.mu.Unlock()
	if _, taken := s.locks.held[key]; taken {
		return nil, false, nil
	}
	return s.locks.take(key), true, nil
}

// NamedLock takes the lock named by key, waiting for another holder to let it
// go or for ctx to end.
func (s *Store) NamedLock(ctx context.Context, key string) (release func(), err error) {
	for {
		s.locks.mu.Lock()
		freed, taken := s.locks.held[key]
		if !taken {
			release := s.locks.take(key)
			s.locks.mu.Unlock()
			return release, nil
		}
		s.locks.mu.Unlock()
		select {
		case <-freed:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

// take records key as held; the caller holds mu.
func (l *namedLocks) take(key string) func() {
	freed := make(chan struct{})
	l.held[key] = freed
	var once sync.Once
	return func() {
		once.Do(func() {
			l.mu.Lock()
			delete(l.held, key)
			l.mu.Unlock()
			close(freed)
		})
	}
}
