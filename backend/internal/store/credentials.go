package store

import (
	"context"
	"database/sql"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"
)

// Passkeys and recovery codes.
//
// These tables are not space-scoped, and these are the only queries in this
// package taking a user id instead of a SpaceID: a credential authenticates a
// person, one passkey opens all their spaces, and at login there is no user
// yet, let alone a space.
//
// They carry the shape of auth.PasskeyStore and auth.RecoveryCodeStore without
// naming them, since internal/auth imports this package; the passkey methods
// need a conversion in the layer above.

// Passkey is one registered WebAuthn credential. CredentialID and PublicKey
// are raw bytes, as the verifier compares them; base64url exists only at the
// edges.
type Passkey struct {
	ID           uuid.UUID
	UserID       uuid.UUID
	CredentialID []byte
	PublicKey    []byte
	// SignCount must be written back on every successful login, or the clone
	// check compares against zero forever.
	SignCount uint32
	Name      string
	// Transports is stored comma-separated.
	Transports     []string
	RPID           string
	IsDiscoverable bool
	// BackupEligible is nil for a credential stored before the flag was kept.
	BackupEligible *bool
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

const passkeyColumns = `id, user_id, credential_id, public_key, sign_count, name,
	transports, rp_id, is_discoverable, backup_eligible, created_at, last_used_at`

func (s *Store) ListPasskeys(ctx context.Context, userID uuid.UUID) ([]Passkey, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list passkeys", scanPasskey,
		`SELECT `+passkeyColumns+` FROM passkeys WHERE user_id = ? ORDER BY created_at, id`, userID)
}

// GetPasskeyByCredential finds a credential by the bytes the authenticator
// signed with. No user id: a discoverable credential arrives before the user
// is known.
func (s *Store) GetPasskeyByCredential(ctx context.Context, credentialID []byte) (Passkey, error) {
	db, err := s.server()
	if err != nil {
		return Passkey{}, err
	}
	row := db.Read().QueryRowContext(ctx,
		`SELECT `+passkeyColumns+` FROM passkeys WHERE credential_id = ?`, credentialID)
	key, err := scanPasskey(row)
	return key, wrap("store: get passkey by credential", err)
}

func (s *Store) AddPasskey(ctx context.Context, key Passkey) error {
	if key.ID == uuid.Nil {
		key.ID = uuid.New()
	}
	signCount, err := signCountArg(key.SignCount)
	if err != nil {
		return err
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	created := db.Now()
	if !key.CreatedAt.IsZero() {
		created = unixMillis(key.CreatedAt)
	}
	_, err = db.Write().ExecContext(ctx, `
		INSERT INTO passkeys (id, user_id, credential_id, public_key, sign_count, name,
			transports, rp_id, is_discoverable, backup_eligible, created_at, last_used_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		key.ID, key.UserID, key.CredentialID, key.PublicKey, signCount, key.Name,
		pgconv.NullText(strings.Join(key.Transports, ",")), pgconv.NullText(key.RPID),
		key.IsDiscoverable, key.BackupEligible, created, nullableMillis(key.LastUsedAt), db.Now())
	return wrap("store: add passkey", err)
}

// UpdatePasskeyUse records a completed login's counter, backup-eligible flag
// and time. The flag never changes for a credential, so writing it every time
// only fills in a row stored without it. Unscoped by
// user on purpose: id is the row the assertion just matched, never request
// input.
func (s *Store) UpdatePasskeyUse(ctx context.Context, id uuid.UUID, signCount uint32, backupEligible bool, usedAt time.Time) error {
	count, err := signCountArg(signCount)
	if err != nil {
		return err
	}
	db, err := s.server()
	if err != nil {
		return err
	}
	return sqliteExecOne(ctx, db.Write(), "store: update passkey use",
		`UPDATE passkeys SET sign_count = ?, backup_eligible = ?, last_used_at = ?, updated_at = ? WHERE id = ?`,
		count, backupEligible, unixMillis(usedAt), db.Now(), id)
}

// DeletePasskey revokes one credential, reporting whether there was one. The
// owner is in the WHERE clause, not checked after the read, because a passkey
// id travels in URLs.
func (s *Store) DeletePasskey(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	db, err := s.server()
	if err != nil {
		return false, err
	}
	tag, err := db.Write().ExecContext(ctx, `DELETE FROM passkeys WHERE id = ? AND user_id = ?`, id, userID)
	if err != nil {
		return false, wrap("store: delete passkey", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, err
}

func scanPasskey(row scanner) (Passkey, error) {
	var key Passkey
	var signCount int32
	var transports, rpID sql.Null[string]
	var backup sql.Null[bool]
	var created int64
	var used sql.Null[int64]
	err := row.Scan(&key.ID, &key.UserID, &key.CredentialID, &key.PublicKey, &signCount,
		&key.Name, &transports, &rpID, &key.IsDiscoverable, &backup, &created, &used)
	key.BackupEligible = nullValue(backup)
	key.CreatedAt, key.LastUsedAt = fromUnixMillis(created), nullableTime(used)
	if err != nil {
		return Passkey{}, err
	}
	if signCount < 0 {
		return Passkey{}, fmt.Errorf("store: passkey %s has a negative sign count", key.ID)
	}
	key.SignCount = uint32(signCount)
	key.Transports = splitTransports(transports.V)
	key.RPID = rpID.V
	return key, nil
}

// signCountArg narrows WebAuthn's uint32 counter to the `integer` column,
// refusing past 2^31 rather than wrapping to a negative no assertion can beat.
func signCountArg(count uint32) (int32, error) {
	if count > math.MaxInt32 {
		return 0, fmt.Errorf("store: sign count %d does not fit passkeys.sign_count", count)
	}
	return int32(count), nil
}

func splitTransports(joined string) []string {
	if joined == "" {
		return nil
	}
	return strings.Split(joined, ",")
}

// timePtr writes NULL for the zero time, so the column default applies.
func timePtr(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	return &t
}

// --- Recovery codes ----------------------------------------------------------

// ReplaceRecoveryCodes retires the user's unused codes and inserts the new
// digests. Spent rows are kept, so "already used" stays answerable.
func (s *Store) ReplaceRecoveryCodes(ctx context.Context, userID uuid.UUID, digests []string) error {
	return s.InTx(ctx, func(tx *Store) error {
		db, err := tx.server()
		if err != nil {
			return err
		}
		if err := tx.DiscardRecoveryCodes(ctx, userID); err != nil {
			return err
		}
		for _, digest := range digests {
			_, err := db.Write().ExecContext(ctx,
				`INSERT INTO recovery_codes (id, user_id, code_hash, created_at, updated_at) VALUES (?, ?, ?, ?, ?)`,
				uuid.New(), userID, digest, db.Now(), db.Now())
			if err != nil {
				return wrap("store: insert recovery code", err)
			}
		}
		return nil
	})
}

func (s *Store) CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	db, err := s.server()
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID).Scan(&count)
	return count, wrap("store: count unused recovery codes", err)
}

// SpendRecoveryCode marks one unused code used, matching user and digest
// together. False means there was no such unused code.
//
// A single write statement selects and spends one row. SQLite's single writer
// serializes competing attempts without row locks. The index comparison is not
// constant-time, which is fine for a SHA-256 digest of 79 random bits.
func (s *Store) SpendRecoveryCode(ctx context.Context, userID uuid.UUID, digest string, usedAt time.Time) (bool, error) {
	db, err := s.server()
	if err != nil {
		return false, err
	}
	tag, err := db.Write().ExecContext(ctx, `
		UPDATE recovery_codes SET used_at = ?, updated_at = ?
		WHERE id = (
			SELECT id FROM recovery_codes
			WHERE user_id = ? AND code_hash = ? AND used_at IS NULL
			ORDER BY created_at, id LIMIT 1
		)`, unixMillis(usedAt), db.Now(), userID, digest)
	if err != nil {
		return false, wrap("store: spend recovery code", err)
	}
	n, err := tag.RowsAffected()
	return n > 0, err
}

// DiscardRecoveryCodes retires the unused codes and keeps the spent ones, for
// when the second factor is turned off.
func (s *Store) DiscardRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`DELETE FROM recovery_codes WHERE user_id = ? AND used_at IS NULL`, userID)
	return wrap("store: discard recovery codes", err)
}
