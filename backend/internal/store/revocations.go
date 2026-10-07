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
	_, err := s.db.Exec(ctx, `
		INSERT INTO revoked_tokens (jti, expires_at) VALUES ($1, $2)
		ON CONFLICT (jti) DO UPDATE SET expires_at = EXCLUDED.expires_at`,
		jti, expiresAt)
	return wrap("store: revoke token", err)
}

// TokenRevoked reports whether a jti has been signed out.
func (s *Store) TokenRevoked(ctx context.Context, jti string) (bool, error) {
	var revoked bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (SELECT 1 FROM revoked_tokens WHERE jti = $1)`, jti).Scan(&revoked)
	return revoked, wrap("store: read token revocation", err)
}

// PruneRevocations deletes marks whose tokens have expired anyway; a missed
// run costs nothing but space.
func (s *Store) PruneRevocations(ctx context.Context) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`DELETE FROM revoked_tokens WHERE expires_at < now()`)
	if err != nil {
		return 0, wrap("store: prune token revocations", err)
	}
	return tag.RowsAffected(), nil
}
