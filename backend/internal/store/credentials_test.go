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

func newPasskey(t *testing.T, st *Store, userID uuid.UUID, name string) Passkey {
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
	require.NoError(t, st.AddPasskey(t.Context(), key))
	return key
}

// TestPasskeyRoundTrip: the verifier compares raw bytes, so a column that
// re-encoded them would fail every assertion silently.
func TestPasskeyRoundTrip(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	key := newPasskey(t, st, user.ID, "YubiKey")

	read, err := st.GetPasskeyByCredential(ctx, key.CredentialID)
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
	require.Nil(t, read.BackupEligible, "a credential stored without the flag reads back as unknown")

	// The bytes are in the column as bytes, not as base64 text of them.
	var stored []byte
	require.NoError(t, st.sqlite.server.Read().QueryRowContext(ctx,
		`SELECT credential_id FROM passkeys WHERE id = ?1`, key.ID).Scan(&stored))
	require.Equal(t, key.CredentialID, stored)
}

func TestGetPasskeyByUnknownCredential(t *testing.T) {
	st := newSQLiteTest(t)
	_, err := st.GetPasskeyByCredential(t.Context(), []byte("no such credential"))
	require.ErrorIs(t, err, ErrNotFound)
}

func TestListPasskeysIsPerUser(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	owner := newUserIn(t, st, false, true)
	stranger := newUserIn(t, st, false, true)
	first := newPasskey(t, st, owner.ID, "Laptop")
	second := newPasskey(t, st, owner.ID, "Phone")
	newPasskey(t, st, stranger.ID, "Not yours")

	// Oldest first, so the ceremony's exclusion list is stable between calls.
	_, err := st.sqlite.server.Write().ExecContext(ctx,
		`UPDATE passkeys SET created_at = created_at + 86400000 WHERE id = ?1`, second.ID)
	require.NoError(t, err)

	keys, err := st.ListPasskeys(ctx, owner.ID)
	require.NoError(t, err)
	require.Len(t, keys, 2)
	require.Equal(t, first.ID, keys[0].ID)
	require.Equal(t, second.ID, keys[1].ID)

	strangers, err := st.ListPasskeys(ctx, stranger.ID)
	require.NoError(t, err)
	require.Len(t, strangers, 1)
}

// TestUpdatePasskeyUsePersistsCounter: the clone check needs the counter to
// move in the database.
func TestUpdatePasskeyUsePersistsCounter(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	key := newPasskey(t, st, user.ID, "YubiKey")

	used := time.Date(2026, 5, 4, 9, 30, 0, 0, time.UTC)
	require.NoError(t, st.UpdatePasskeyUse(ctx, key.ID, 42, true, used))

	read, err := st.GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, uint32(42), read.SignCount)
	require.NotNil(t, read.BackupEligible)
	require.True(t, *read.BackupEligible)
	require.NotNil(t, read.LastUsedAt)
	require.True(t, used.Equal(*read.LastUsedAt))
}

func TestUpdatePasskeyUseUnknownID(t *testing.T) {
	st := newSQLiteTest(t)
	err := st.UpdatePasskeyUse(t.Context(), uuid.New(), 1, false, time.Now())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestSignCountBeyondColumnRange(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	key := newPasskey(t, st, user.ID, "YubiKey")

	err := st.UpdatePasskeyUse(ctx, key.ID, math.MaxInt32+1, false, time.Now())
	require.Error(t, err)
	require.Contains(t, err.Error(), "sign_count")

	read, err := st.GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, uint32(7), read.SignCount)
}

func TestDeletePasskeyIsScopedToOwner(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	owner := newUserIn(t, st, false, true)
	stranger := newUserIn(t, st, false, true)
	key := newPasskey(t, st, owner.ID, "YubiKey")

	deleted, err := st.DeletePasskey(ctx, stranger.ID, key.ID)
	require.NoError(t, err)
	require.False(t, deleted)

	_, err = st.GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err, "the stranger's delete must not have removed the row")

	deleted, err = st.DeletePasskey(ctx, owner.ID, key.ID)
	require.NoError(t, err)
	require.True(t, deleted)

	deleted, err = st.DeletePasskey(ctx, owner.ID, key.ID)
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

func spentCount(t *testing.T, st *Store, userID uuid.UUID) int {
	t.Helper()
	var count int
	require.NoError(t, st.sqlite.server.Read().QueryRowContext(t.Context(),
		`SELECT count(*) FROM recovery_codes WHERE user_id = ?1 AND used_at IS NOT NULL`,
		userID).Scan(&count))
	return count
}

func TestRecoveryCodeIsSingleUse(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	codes := digests(t, 3)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, user.ID, codes))

	unused, err := st.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 3, unused)

	used := time.Date(2026, 6, 1, 8, 0, 0, 0, time.UTC)
	spent, err := st.SpendRecoveryCode(ctx, user.ID, codes[1], used)
	require.NoError(t, err)
	require.True(t, spent)

	spent, err = st.SpendRecoveryCode(ctx, user.ID, codes[1], used)
	require.NoError(t, err)
	require.False(t, spent)

	unused, err = st.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
	require.Equal(t, 1, spentCount(t, st, user.ID))

	var storedAt int64
	require.NoError(t, st.sqlite.server.Read().QueryRowContext(ctx,
		`SELECT used_at FROM recovery_codes WHERE user_id = ?1 AND code_hash = ?2`,
		user.ID, codes[1]).Scan(&storedAt))
	require.True(t, used.Equal(fromUnixMillis(storedAt)))
}

// TestSpendRecoveryCodeMatchesUserAndDigest: the column has no unique
// constraint, so the user must be matched too.
func TestSpendRecoveryCodeMatchesUserAndDigest(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	owner := newUserIn(t, st, false, true)
	stranger := newUserIn(t, st, false, true)
	shared := digests(t, 1)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, owner.ID, shared))

	spent, err := st.SpendRecoveryCode(ctx, stranger.ID, shared[0], time.Now())
	require.NoError(t, err)
	require.False(t, spent)

	unused, err := st.CountUnusedRecoveryCodes(ctx, owner.ID)
	require.NoError(t, err)
	require.Equal(t, 1, unused)
}

func TestReplaceRecoveryCodesKeepsSpent(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	first := digests(t, 3)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, user.ID, first))

	spent, err := st.SpendRecoveryCode(ctx, user.ID, first[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)

	second := digests(t, 2)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, user.ID, second))

	unused, err := st.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
	require.Equal(t, 1, spentCount(t, st, user.ID), "the spent code is the audit trail")

	// An unused code from the retired set is gone, not merely used.
	spent, err = st.SpendRecoveryCode(ctx, user.ID, first[1], time.Now())
	require.NoError(t, err)
	require.False(t, spent)

	spent, err = st.SpendRecoveryCode(ctx, user.ID, second[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)
}

func TestDiscardRecoveryCodesKeepsSpent(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	codes := digests(t, 3)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, user.ID, codes))
	spent, err := st.SpendRecoveryCode(ctx, user.ID, codes[0], time.Now())
	require.NoError(t, err)
	require.True(t, spent)

	require.NoError(t, st.DiscardRecoveryCodes(ctx, user.ID))

	unused, err := st.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 0, unused)
	require.Equal(t, 1, spentCount(t, st, user.ID))
}

// TestCredentialsSurviveTheProcess: a second connection sees what the first
// wrote.
func TestCredentialsSurviveTheProcess(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	user := newUserIn(t, st, false, true)
	key := newPasskey(t, st, user.ID, "YubiKey")
	codes := digests(t, 2)
	require.NoError(t, st.ReplaceRecoveryCodes(ctx, user.ID, codes))

	st.Close()
	fresh, err := OpenSQLite(ctx, st.sqlite.dataDir, time.Now)
	require.NoError(t, err)
	defer fresh.Close()

	read, err := fresh.GetPasskeyByCredential(ctx, key.CredentialID)
	require.NoError(t, err)
	require.Equal(t, key.ID, read.ID)

	unused, err := fresh.CountUnusedRecoveryCodes(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, 2, unused)
}
