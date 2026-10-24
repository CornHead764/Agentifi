package store

import (
	"context"
	"time"
)

// Durable token revocations, so a signed-out token stays signed out across a
// restart. auth.Tokens keeps an in-process copy for the fast path; this table
// is the source of truth read on an in-memory miss. Rows carry the expiry of
// the token they retire and are pruned once past it.

// RevokeToken records a jti as signed out until expiresAt.
func (s *Store) RevokeToken(ctx context.Context, jti string, expiresAt time.Time) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO revoked_tokens (jti, expires_at, created_at) VALUES (?, ?, ?)
		ON CONFLICT (jti) DO UPDATE SET expires_at = excluded.expires_at`,
		jti, unixMillis(expiresAt), db.Now())
	return wrap("store: revoke token", err)
}

// TokenRevoked reports whether a jti has been signed out.
func (s *Store) TokenRevoked(ctx context.Context, jti string) (bool, error) {
	db, err := s.server()
	if err != nil {
		return false, err
	}
	var revoked bool
	err = db.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM revoked_tokens WHERE jti = ?)`, jti).Scan(&revoked)
	return revoked, wrap("store: read token revocation", err)
}

// PruneRevocations deletes marks whose tokens have expired anyway; a missed
// run costs nothing but space.
func (s *Store) PruneRevocations(ctx context.Context) (int64, error) {
	db, err := s.server()
	if err != nil {
		return 0, err
	}
	result, err := db.Write().ExecContext(ctx,
		`DELETE FROM revoked_tokens WHERE expires_at < ?`, db.Now())
	if err != nil {
		return 0, wrap("store: prune token revocations", err)
	}
	return result.RowsAffected()
}
