package store

import (
	"context"

	"github.com/jackc/pgx/v5/pgxpool"
)

// Session-scoped advisory locks for runs that must not overlap, such as the
// scheduler and "Sync now" picking the same connection: concurrent syncs race
// the external-id unique constraint, and the loser's ConnectionActive write can
// erase the winner's rate-limit park.
//
// Each holds a dedicated connection until release, since a pooled unlock could
// land on a different session and do nothing.

// TryNamedLock takes the advisory lock named by key, if it is free. ok is false
// when another holder has it; the caller skips rather than queues.
func (s *Store) TryNamedLock(ctx context.Context, key string) (release func(), ok bool, err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, false, wrap("store: advisory lock acquire", err)
	}
	var got bool
	err = conn.QueryRow(ctx, `SELECT pg_try_advisory_lock(hashtext($1))`, key).Scan(&got)
	if err != nil {
		conn.Release()
		return nil, false, wrap("store: advisory lock", err)
	}
	if !got {
		conn.Release()
		return nil, false, nil
	}
	return unlocker(conn, key), true, nil
}

// NamedLock takes the advisory lock named by key, waiting for another holder
// to let it go or for ctx to end.
func (s *Store) NamedLock(ctx context.Context, key string) (release func(), err error) {
	conn, err := s.pool.Acquire(ctx)
	if err != nil {
		return nil, wrap("store: advisory lock acquire", err)
	}
	if _, err := conn.Exec(ctx, `SELECT pg_advisory_lock(hashtext($1))`, key); err != nil {
		conn.Release()
		return nil, wrap("store: advisory lock", err)
	}
	return unlocker(conn, key), nil
}

func unlocker(conn *pgxpool.Conn, key string) func() {
	return func() {
		// Background context: the unlock must run even if the caller's request
		// is being torn down.
		_, _ = conn.Exec(context.Background(), `SELECT pg_advisory_unlock(hashtext($1))`, key)
		conn.Release()
	}
}
