package store

import (
	"context"
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"

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
	CreatedAt      time.Time
	LastUsedAt     *time.Time
}

const passkeyColumns = `id, user_id, credential_id, public_key, sign_count, name,
	transports, rp_id, is_discoverable, created_at, last_used_at`

func (s *Store) ListPasskeys(ctx context.Context, userID uuid.UUID) ([]Passkey, error) {
	return queryAll(ctx, s.db, "store: list passkeys", scanPasskey,
		`SELECT `+passkeyColumns+` FROM passkeys WHERE user_id = $1 ORDER BY created_at, id`,
		userID)
}

// GetPasskeyByCredential finds a credential by the bytes the authenticator
// signed with. No user id: a discoverable credential arrives before the user
// is known.
func (s *Store) GetPasskeyByCredential(ctx context.Context, credentialID []byte) (Passkey, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+passkeyColumns+` FROM passkeys WHERE credential_id = $1`, credentialID)
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
	_, err = s.db.Exec(ctx, `
		INSERT INTO passkeys (id, user_id, credential_id, public_key, sign_count, name,
			transports, rp_id, is_discoverable, created_at, last_used_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, COALESCE($10, now()), $11)`,
		key.ID, key.UserID, key.CredentialID, key.PublicKey, signCount, key.Name,
		dbconv.NullText(strings.Join(key.Transports, ",")), dbconv.NullText(key.RPID),
		key.IsDiscoverable, timePtr(key.CreatedAt), key.LastUsedAt,
	)
	return wrap("store: add passkey", err)
}

// UpdatePasskeyUse records a completed login's counter and time. Unscoped by
// user on purpose: id is the row the assertion just matched, never request
// input.
func (s *Store) UpdatePasskeyUse(ctx context.Context, id uuid.UUID, signCount uint32, usedAt time.Time) error {
	count, err := signCountArg(signCount)
	if err != nil {
		return err
	}
	return s.execOne(ctx, "store: update passkey use",
		`UPDATE passkeys SET sign_count = $2, last_used_at = $3, updated_at = now() WHERE id = $1`,
		id, count, usedAt)
}

// DeletePasskey revokes one credential, reporting whether there was one. The
// owner is in the WHERE clause, not checked after the read, because a passkey
// id travels in URLs.
func (s *Store) DeletePasskey(ctx context.Context, userID, id uuid.UUID) (bool, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM passkeys WHERE id = $1 AND user_id = $2`, id, userID)
	if err != nil {
		return false, wrap("store: delete passkey", err)
	}
	return tag.RowsAffected() > 0, nil
}

func scanPasskey(row scanner) (Passkey, error) {
	var key Passkey
	var signCount int32
	var transports, rpID *string
	err := row.Scan(&key.ID, &key.UserID, &key.CredentialID, &key.PublicKey, &signCount,
		&key.Name, &transports, &rpID, &key.IsDiscoverable, &key.CreatedAt, &key.LastUsedAt)
	if err != nil {
		return Passkey{}, err
	}
	if signCount < 0 {
		return Passkey{}, fmt.Errorf("store: passkey %s has a negative sign count", key.ID)
	}
	key.SignCount = uint32(signCount)
	key.Transports = splitTransports(Deref(transports))
	key.RPID = Deref(rpID)
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
		if err := tx.DiscardRecoveryCodes(ctx, userID); err != nil {
			return err
		}
		for _, digest := range digests {
			_, err := tx.db.Exec(ctx,
				`INSERT INTO recovery_codes (id, user_id, code_hash) VALUES ($1, $2, $3)`,
				uuid.New(), userID, digest)
			if err != nil {
				return wrap("store: insert recovery code", err)
			}
		}
		return nil
	})
}

func (s *Store) CountUnusedRecoveryCodes(ctx context.Context, userID uuid.UUID) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL`,
		userID).Scan(&count)
	return count, wrap("store: count unused recovery codes", err)
}

// SpendRecoveryCode marks one unused code used, matching user and digest
// together. False means there was no such unused code.
//
// One row, by id: a plain UPDATE would spend every duplicate of a digest. The
// statement's write lock keeps two concurrent attempts from both seeing it
// unused. The index
// comparison is not constant-time, which is fine: the digest is a SHA-256 of
// 79 bits of machine-chosen randomness.
func (s *Store) SpendRecoveryCode(ctx context.Context, userID uuid.UUID, digest string, usedAt time.Time) (bool, error) {
	tag, err := s.db.Exec(ctx, `
		UPDATE recovery_codes SET used_at = $3, updated_at = now()
		WHERE id = (
			SELECT id FROM recovery_codes
			WHERE user_id = $1 AND code_hash = $2 AND used_at IS NULL
			ORDER BY created_at, id
			LIMIT 1
		)`,
		userID, digest, usedAt)
	if err != nil {
		return false, wrap("store: spend recovery code", err)
	}
	return tag.RowsAffected() > 0, nil
}

// DiscardRecoveryCodes retires the unused codes and keeps the spent ones, for
// when the second factor is turned off.
func (s *Store) DiscardRecoveryCodes(ctx context.Context, userID uuid.UUID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM recovery_codes WHERE user_id = $1 AND used_at IS NULL`, userID)
	return wrap("store: discard recovery codes", err)
}
