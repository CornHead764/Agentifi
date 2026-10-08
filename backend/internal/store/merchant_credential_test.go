package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A merchant login's kept password: sealed in the bill connections' shape,
// said only as whether, and the pause that keeps the scheduler off it. Every
// login here is invented.

func newSignedInMerchantAccount(t *testing.T, space SpaceID, label string) MerchantAccount {
	t.Helper()
	account := MerchantAccount{Merchant: domain.MerchantAmazon, Label: label}
	require.NoError(t, sealedDB(t).CreateMerchantAccount(t.Context(), space, &account))
	require.NoError(t, sealedDB(t).SaveMerchantSession(t.Context(), space, account.ID,
		"alex@example.com", `{"cookies":[]}`, true))
	return account
}

func TestAMerchantPasswordIsSealedAndOnlySaidToBeKept(t *testing.T) {
	space := newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")

	_, err := sealedDB(t).MerchantCredentialOf(t.Context(), space, account.ID)
	require.ErrorIs(t, err, ErrNotFound)

	login := MerchantCredential{Username: "alex@example.com", Password: "hunter2", TOTPSecret: "JBSWY3DPEHPK3PXP"}
	require.NoError(t, sealedDB(t).SaveMerchantCredential(t.Context(), space, account.ID, login))
	kept, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.True(t, kept.HasPassword)
	require.True(t, kept.HasTOTP)

	var raw string
	require.NoError(t, sealedDB(t).Pool().QueryRow(t.Context(),
		`SELECT credential_sealed FROM merchant_accounts WHERE id = $1`, account.ID).Scan(&raw))
	require.NotContains(t, raw, "hunter2", "the column holds ciphertext")

	opened, err := sealedDB(t).MerchantCredentialOf(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, login, opened)

	// Sealed to this account: the same ciphertext on another row opens nothing.
	other := newSignedInMerchantAccount(t, space, "Casey")
	_, err = sealedDB(t).Pool().Exec(t.Context(),
		`UPDATE merchant_accounts SET credential_sealed = $1 WHERE id = $2`, raw, other.ID)
	require.NoError(t, err)
	_, err = sealedDB(t).MerchantCredentialOf(t.Context(), space, other.ID)
	require.Error(t, err)

	require.NoError(t, sealedDB(t).ClearMerchantCredential(t.Context(), space, account.ID))
	forgotten, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.False(t, forgotten.HasPassword)
	require.False(t, forgotten.HasTOTP)
	require.True(t, forgotten.HasSession, "forgetting the password leaves the session")
}

func TestAMerchantAccountThatNeedsASignInIsDueOnlyWithAnUnpausedPassword(t *testing.T) {
	space := newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")

	// The due list spans every space, so the check is on this row alone.
	isDue := func() bool {
		due, err := sealedDB(t).ListMerchantAccountsDue(t.Context(), time.Now())
		require.NoError(t, err)
		for _, one := range due {
			if one.ID == account.ID {
				return true
			}
		}
		return false
	}
	// A pull stamps last_synced_at; the window is what this test moves.
	yesterday := func() {
		_, err := sealedDB(t).Pool().Exec(t.Context(),
			`UPDATE merchant_accounts SET last_synced_at = ts_add(now(), '-2 days') WHERE id = $1`, account.ID)
		require.NoError(t, err)
	}
	require.True(t, isDue(), "signed in and never pulled")

	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID,
		MerchantSyncNeedsSignIn, "Amazon asked to sign in again", nil))
	yesterday()
	require.False(t, isDue(), "a lapsed session with no password waits for a person")

	require.NoError(t, sealedDB(t).SaveMerchantCredential(t.Context(), space, account.ID,
		MerchantCredential{Username: "alex@example.com", Password: "hunter2"}))
	require.True(t, isDue(), "a kept password is what signs in again")

	pause := func(reason string) {
		require.NoError(t, sealedDB(t).PauseMerchantSignIn(t.Context(), space, account.ID, reason))
		require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID,
			MerchantSyncNeedsSignIn, "stopped", nil))
		yesterday()
		paused, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
		require.NoError(t, err)
		require.NotNil(t, paused.SignInPausedAt)
		require.Equal(t, reason, paused.SignInPausedFor)
		require.False(t, isDue(), "a paused password waits for a person: "+reason)
	}

	pause(provider.SignInPausedPasswordRefused)
	require.NoError(t, sealedDB(t).SaveMerchantCredential(t.Context(), space, account.ID,
		MerchantCredential{Username: "alex@example.com", Password: "a-new-one"}))
	require.True(t, isDue(), "a new password")

	pause(provider.SignInPausedCodeNeeded)
	require.NoError(t, sealedDB(t).SaveMerchantSession(t.Context(), space, account.ID, "", `{"cookies":[]}`, true))
	require.True(t, isDue(), "a person's sign-in")

	pause(provider.SignInPausedPasswordRefused)
	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID, MerchantSyncFailed, "timeout", nil))
	still, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.NotNil(t, still.SignInPausedAt, "a failed pull is not a pull that got in")
	require.NoError(t, sealedDB(t).MarkMerchantSync(t.Context(), space, account.ID, MerchantSyncOK, "", nil))
	cleared, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Nil(t, cleared.SignInPausedAt, "a pull that got in")
	require.Empty(t, cleared.SignInPausedFor)

	pause(provider.SignInPausedCodeNeeded)
	require.NoError(t, sealedDB(t).ClearMerchantCredential(t.Context(), space, account.ID))
	forgotten, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Nil(t, forgotten.SignInPausedAt, "a forgotten password")
	require.False(t, isDue(), "and with it forgotten, a lapsed session waits for a person again")
}

func TestForgettingAMerchantSessionAsksForASignInAndLeavesThePullOn(t *testing.T) {
	space := newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")
	require.NoError(t, sealedDB(t).SaveMerchantCredential(t.Context(), space, account.ID,
		MerchantCredential{Username: "alex@example.com", Password: "hunter2"}))

	require.NoError(t, sealedDB(t).ClearMerchantSession(t.Context(), space, account.ID))
	forgot, err := sealedDB(t).GetMerchantAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.False(t, forgot.HasSession)
	require.True(t, forgot.NeedsSignIn, "the same as a bill connection whose session is forgotten")
	require.True(t, forgot.SyncEnabled, "the switch is the person's, not the session's")
	require.True(t, forgot.HasPassword, "the password stays")

	due, err := sealedDB(t).ListMerchantAccountsDue(t.Context(), time.Now())
	require.NoError(t, err)
	for _, one := range due {
		require.NotEqual(t, account.ID, one.ID, "nothing to pull with until a person signs in")
	}
}

func TestAPausedMerchantAccountIsNotDueEvenWhenItsSessionIsGood(t *testing.T) {
	space := newSpace(t)
	account := newSignedInMerchantAccount(t, space, "Alex")
	require.NoError(t, sealedDB(t).PauseMerchantSignIn(t.Context(), space, account.ID, provider.SignInPausedCodeNeeded))

	due, err := sealedDB(t).ListMerchantAccountsDue(t.Context(), time.Now())
	require.NoError(t, err)
	for _, one := range due {
		require.NotEqual(t, account.ID, one.ID, "a pause holds scheduled pulls, as it does for a bill connection")
	}
}
