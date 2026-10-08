package store

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newPasskey(t *testing.T, userID uuid.UUID, name string) Passkey {
	t.Helper()
	key := Passkey{
		ID:             uuid.New(),
		UserID:         userID,
		CredentialID:   []byte{0x00, 0xff, 0x10, 0x7f, 0x80},
		PublicKey:      []byte{0xa5, 0x01, 0x02, 0x00},
		SignCount:      7,
		Name:           name,
		Transports:     []string{"internal", "hybrid"},
		RPID:           "agentifi.test",
		IsDiscoverable: true,
		CreatedAt:      time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
	}
	// A credential id is unique across the table.
	key.CredentialID = append(key.CredentialID, key.ID[:]...)
	require.NoError(t, db(t).AddPasskey(t.Context(), key))
	return key
}

// TestPasskeyRoundTrip: the verifier compares raw bytes, so a column that
// re-encoded them would fail every assertion silently.
func TestPasskeyRoundTrip(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	key := newPasskey(t, user.ID, "YubiKey")

	read, err := db(t).GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, key.ID, read.ID)
	require.Equal(t, user.ID, read.UserID)
	require.Equal(t, key.CredentialID, read.CredentialID)
	require.Equal(t, key.PublicKey, read.PublicKey)
	require.Equal(t, uint32(7), read.SignCount)
	require.Equal(t, "YubiKey", read.Name)
	require.Equal(t, []string{"internal", "hybrid"}, read.Transports)
	require.Equal(t, "agentifi.test", read.RPID)
	require.True(t, read.IsDiscoverable)
	require.True(t, key.CreatedAt.Equal(read.CreatedAt))
	require.Nil(t, read.LastUsedAt)

	// The bytes are in the column as bytes, not as base64 text of them.
	var stored []byte
	require.NoError(t, db(t).db.QueryRow(ctx,
		`SELECT credential_id FROM passkeys WHERE id = $1`, key.ID).Scan(&stored))
	require.Equal(t, key.CredentialID, stored)
}

func TestGetPasskeyByUnknownCredential(t *testing.T) {
	db(t)
	_, err := db(t).GetPasskeyByCredential(t.Context(), []byte("no such credential"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestListPasskeysIsPerUser(t *testing.T) {
	ctx := t.Context()
	owner := newUser(t)
	stranger := newUser(t)
	first := newPasskey(t, owner.ID, "Laptop")
	second := newPasskey(t, owner.ID, "Phone")
	newPasskey(t, stranger.ID, "Not yours")

	// Oldest first, so the ceremony's exclusion list is stable between calls.
	_, err := db(t).db.Exec(ctx,
		`UPDATE passkeys SET created_at = ts_add(created_at, '1 day') WHERE id = $1`, second.ID)
	require.NoError(t, err)

	keys, err := db(t).ListPasskeys(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, first.ID, keys[0].ID)
	require.Equal(t, second.ID, keys[1].ID)

	strangers, err := db(t).ListPasskeys(ctx, stranger.ID)
	require.NoError(t, err)
	require.Len(t, strangers, 1)
}

// TestUpdatePasskeyUsePersistsCounter: the clone check needs the counter to
// move in the database.
func TestUpdatePasskeyUsePersistsCounter(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	key := newPasskey(t, user.ID, "YubiKey")

	used := time.Date(2026, 5, 4, 9, 30, 0, 0, time.UTC)
	require.NoError(t, db(t).UpdatePasskeyUse(ctx, key.ID, 42, used))

	read, err := db(t).GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, uint32(42), read.SignCount)
	require.NotNil(t, read.LastUsedAt)
	require.True(t, used.Equal(*read.LastUsedAt))
}

func TestUpdatePasskeyUseUnknownID(t *testing.T) {
	db(t)
	err := db(t).UpdatePasskeyUse(t.Context(), uuid.New(), 1, time.Now())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSignCountBeyondColumnRange(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	key := newPasskey(t, user.ID, "YubiKey")

	err := db(t).UpdatePasskeyUse(ctx, key.ID, math.MaxInt32+1, time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "sign_count")

	read, err := db(t).GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, uint32(7), read.SignCount)
}

func TestDeletePasskeyIsScopedToOwner(t *testing.T) {
	ctx := t.Context()
	owner := newUser(t)
	stranger := newUser(t)
	key := newPasskey(t, owner.ID, "YubiKey")

	deleted, err := db(t).DeletePasskey(ctx, stranger.ID, key.ID)
	require.NoError(t, err)
	require.False(t, deleted)

	_, err = db(t).GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err, "the stranger's delete must not have removed the row")

	deleted, err = db(t).DeletePasskey(ctx, owner.ID, key.ID)
	require.NoError(t, err)
	require.True(t, deleted)

	deleted, err = db(t).DeletePasskey(ctx, owner.ID, key.ID)
	require.NoError(t, err)
	require.False(t, deleted, "deleting a gone credential is not an error, just false")
}

// --- Recovery codes ----------------------------------------------------------

func digestOf(code string) string {
	sum := sha256.Sum256([]byte(code))
	return hex.EncodeToString(sum[:])
}

func digests(t *testing.T, count int) []string {
	t.Helper()
	out := make([]string, count)
	for i := range out {
		out[i] = digestOf(fmt.Sprintf("%s-%d-%s", t.Name(), i, uuid.NewString()))
	}
	return out
}

func spentCount(t *testing.T, userID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, db(t).db.QueryRow(t.Context(),
		`SELECT count(*) FROM recovery_codes WHERE user_id = $1 AND used_at IS NOT NULL`,
		userID).Scan(&count))
	return count
}

func TestRecoveryCodeIsSingleUse(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	codes := digests(t, 3)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, user.ID, codes))

	unused, err := db(t).CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 3, unused)

	used := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	spent, err := db(t).SpendRecoveryCode(ctx, user.ID, codes[1], used)
	require.NoError(t, err)
	require.True(t, spent)

	spent, err = db(t).SpendRecoveryCode(ctx, user.ID, codes[1], used)
	require.NoError(t, err)
	require.False(t, spent)

	unused, err = db(t).CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
	require.Equal(t, 1, spentCount(t, user.ID))

	var storedAt time.Time
	require.NoError(t, db(t).db.QueryRow(ctx,
		`SELECT used_at FROM recovery_codes WHERE user_id = $1 AND code_hash = $2`,
		user.ID, codes[1]).Scan(&storedAt))
	require.True(t, used.Equal(storedAt))
}

// TestSpendRecoveryCodeMatchesUserAndDigest: the column has no unique
// constraint, so the user must be matched too.
func TestSpendRecoveryCodeMatchesUserAndDigest(t *testing.T) {
	ctx := t.Context()
	owner := newUser(t)
	stranger := newUser(t)
	shared := digests(t, 1)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, owner.ID, shared))

	spent, err := db(t).SpendRecoveryCode(ctx, stranger.ID, shared[0], time.Now())
	require.NoError(t, err)
	require.False(t, spent)

	unused, err := db(t).CountUnusedRecoveryCodes(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, 1, unused)
}

func TestReplaceRecoveryCodesKeepsSpent(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	first := digests(t, 3)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, user.ID, first))

	spent, err := db(t).SpendRecoveryCode(ctx, user.ID, first[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)

	second := digests(t, 2)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, user.ID, second))

	unused, err := db(t).CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
	require.Equal(t, 1, spentCount(t, user.ID), "the spent code is the audit trail")

	// An unused code from the retired set is gone, not merely used.
	spent, err = db(t).SpendRecoveryCode(ctx, user.ID, first[1], time.Now())
	require.NoError(t, err)
	require.False(t, spent)

	spent, err = db(t).SpendRecoveryCode(ctx, user.ID, second[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)
}

func TestDiscardRecoveryCodesKeepsSpent(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	codes := digests(t, 3)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, user.ID, codes))
	spent, err := db(t).SpendRecoveryCode(ctx, user.ID, codes[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)

	require.NoError(t, db(t).DiscardRecoveryCodes(ctx, user.ID))

	unused, err := db(t).CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 0, unused)
	require.Equal(t, 1, spentCount(t, user.ID))
}

// TestCredentialsSurviveTheProcess: a second connection sees what the first
// wrote.
func TestCredentialsSurviveTheProcess(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	key := newPasskey(t, user.ID, "YubiKey")
	codes := digests(t, 2)
	require.NoError(t, db(t).ReplaceRecoveryCodes(ctx, user.ID, codes))

	fresh, err := Open(ctx, testPath)
	require.NoError(t, err)
	defer fresh.Close()

	read, err := fresh.GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, key.ID, read.ID)

	unused, err := fresh.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
}
