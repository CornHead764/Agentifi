package store

import (
	"context"
	"database/sql"
	"time"

	"github.com/google/uuid"
)

// SessionKind is what a session was issued to: a browser, a native app, or a
// person's own script.
type SessionKind string

const (
	SessionWeb      SessionKind = "web"
	SessionDevice   SessionKind = "device"
	SessionPersonal SessionKind = "personal"
)

// Session is one signed-in token. The table holds the token's SHA-256 and never
// the token, so a copy of server.db signs nobody in.
type Session struct {
	ID        uuid.UUID
	UserID    uuid.UUID
	TokenHash []byte
	Kind      SessionKind
	// Name is the browser and OS, or the name the person gave a token.
	Name       string
	CreatedAt  time.Time
	LastUsedAt time.Time
	ExpiresAt  time.Time
	RevokedAt  *time.Time
}

const sessionColumns = `id, user_id, token_hash, kind, name, created_at, last_used_at,
	expires_at, revoked_at`

func (s *Store) CreateSession(ctx context.Context, session Session) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO sessions (`+sessionColumns+`) VALUES (?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		session.ID, session.UserID, session.TokenHash, string(session.Kind), session.Name,
		unixMillis(session.CreatedAt), unixMillis(session.LastUsedAt), unixMillis(session.ExpiresAt))
	return wrap("store: create session", err)
}

// SessionByTokenHash finds a session whatever its state; the caller decides
// whether a revoked or expired one still counts.
func (s *Store) SessionByTokenHash(ctx context.Context, hash []byte) (Session, error) {
	db, err := s.server()
	if err != nil {
		return Session{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+sessionColumns+` FROM sessions WHERE token_hash = ?`, hash)
	session, err := scanSession(row)
	return session, wrap("store: get session", err)
}

// TouchSession records a use at usedAt and slides the expiry, but only if the
// session was last used at or before staleBy. The check and the write are one
// statement because concurrent requests race to touch the same session, and a
// slower one must not move either time backwards. touched reports that this
// use was written.
func (s *Store) TouchSession(
	ctx context.Context, id uuid.UUID, usedAt, expiresAt, staleBy time.Time,
) (touched bool, err error) {
	db, err := s.server()
	if err != nil {
		return false, err
	}
	result, err := db.Write().ExecContext(ctx,
		`UPDATE sessions SET last_used_at = ?, expires_at = ? WHERE id = ? AND last_used_at <= ?`,
		unixMillis(usedAt), unixMillis(expiresAt), id, unixMillis(staleBy))
	if err != nil {
		return false, wrap("store: touch session", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, wrap("store: touch session", err)
	}
	return rows == 1, nil
}

// ListSessions is a user's sessions that are neither revoked nor expired at
// now, most recently used first.
func (s *Store) ListSessions(ctx context.Context, userID uuid.UUID, now time.Time) ([]Session, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list sessions", scanSession,
		`SELECT `+sessionColumns+` FROM sessions
		WHERE user_id = ? AND revoked_at IS NULL AND expires_at > ?
		ORDER BY last_used_at DESC, id`, userID, unixMillis(now))
}

// RevokeSession ends one of the user's sessions. One that is not theirs, or is
// already revoked, is ErrNotFound.
func (s *Store) RevokeSession(ctx context.Context, userID, id uuid.UUID) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: revoke session",
		`UPDATE sessions SET revoked_at = ? WHERE id = ? AND user_id = ? AND revoked_at IS NULL`,
		db.Now(), id, userID)
}

// RevokeUserSessions ends every session the user has but keep, which is
// uuid.Nil to end them all.
func (s *Store) RevokeUserSessions(ctx context.Context, userID, keep uuid.UUID) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE sessions SET revoked_at = ? WHERE user_id = ? AND id != ? AND revoked_at IS NULL`,
		db.Now(), userID, keep)
	return wrap("store: revoke user sessions", err)
}

// PruneSessions deletes sessions that expired or were revoked before the
// cutoff; a missed run costs nothing but space.
func (s *Store) PruneSessions(ctx context.Context, before time.Time) (int64, error) {
	db, err := s.server()
	if err != nil {
		return 0, err
	}
	cutoff := unixMillis(before)
	result, err := db.Write().ExecContext(ctx,
		`DELETE FROM sessions WHERE expires_at < ? OR revoked_at < ?`, cutoff, cutoff)
	if err != nil {
		return 0, wrap("store: prune sessions", err)
	}
	return result.RowsAffected()
}

func scanSession(row scanner) (Session, error) {
	var session Session
	var kind string
	var created, used, expires int64
	var revoked sql.Null[int64]
	err := row.Scan(&session.ID, &session.UserID, &session.TokenHash, &kind, &session.Name,
		&created, &used, &expires, &revoked)
	if err != nil {
		return Session{}, err
	}
	session.Kind = SessionKind(kind)
	session.CreatedAt, session.LastUsedAt = fromUnixMillis(created), fromUnixMillis(used)
	session.ExpiresAt, session.RevokedAt = fromUnixMillis(expires), nullableTime(revoked)
	return session, nil
}
