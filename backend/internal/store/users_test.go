package store

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestTheTOTPSecretReachesPostgresSealed(t *testing.T) {
	user := newUser(t)
	const seed = "JBSWY3DPEHPK3PXP"

	require.NoError(t, sealedDB(t).SetTOTPSecret(t.Context(), user.ID, seed))

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT totp_secret FROM users WHERE id = $1`, user.ID).Scan(&stored))
	require.NotContains(t, stored, seed)
	require.True(t, strings.HasPrefix(stored, cipherVersion+":"))

	opened, err := sealedDB(t).OpenTOTPSecret(t.Context(), user.ID)
	require.NoError(t, err)
	require.Equal(t, seed, opened)
}

func TestClearingTheTOTPSecretNullsTheColumn(t *testing.T) {
	user := newUser(t)
	sealed := sealedDB(t)

	require.NoError(t, sealed.SetTOTPSecret(t.Context(), user.ID, "JBSWY3DPEHPK3PXP"))
	require.NoError(t, sealed.SetTOTPSecret(t.Context(), user.ID, ""))

	var stored *string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT totp_secret FROM users WHERE id = $1`, user.ID).Scan(&stored))
	require.Nil(t, stored)
}

func TestAnAddressIsStoredLowerCasedAndTrimmed(t *testing.T) {
	local := uuid.NewString()
	user := &User{Email: "  Mixed." + local + "@Example.TEST ", IsActive: true}
	require.NoError(t, db(t).CreateUser(t.Context(), user))
	require.Equal(t, "mixed."+local+"@example.test", user.Email)

	var stored string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT email FROM users WHERE id = $1`, user.ID).Scan(&stored))
	require.Equal(t, user.Email, stored)
}

func TestTwoAddressesDifferingOnlyInCaseCannotBothExist(t *testing.T) {
	local := uuid.NewString()
	first := &User{Email: "twice." + local + "@example.test", IsActive: true}
	require.NoError(t, db(t).CreateUser(t.Context(), first))

	second := &User{Email: "Twice." + local + "@Example.Test", IsActive: true}
	require.Error(t, db(t).CreateUser(t.Context(), second))

	found, err := db(t).GetUserByEmail(t.Context(), "TWICE."+local+"@EXAMPLE.TEST")
	require.NoError(t, err)
	require.Equal(t, first.ID, found.ID)
}

func TestALoginStampCannotRestoreAReplacedPassword(t *testing.T) {
	// A login stamps last_login_at after a slow bcrypt check. Rewriting the row
	// it loaded would undo a password change that landed in between, restoring
	// the old hash and clearing the session cutoff.
	ctx := t.Context()
	user := newUser(t)
	require.NoError(t, db(t).SetUserPassword(ctx, user.ID, "the-old-hash", false, time.Time{}))

	// What the login read before the change.
	stale, err := db(t).GetUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "the-old-hash", stale.HashedPassword)

	// The password change, from another request.
	cutoff := time.Now().UTC().Truncate(time.Second).Add(time.Second)
	require.NoError(t, db(t).SetUserPassword(ctx, user.ID, "the-new-hash", false, cutoff))

	// The login, writing after it with the row it loaded before it.
	require.NoError(t, db(t).TouchLastLogin(ctx, stale.ID, time.Now()))

	after, err := db(t).GetUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "the-new-hash", after.HashedPassword)
	require.NotNil(t, after.SessionsValidFrom)
	require.WithinDuration(t, cutoff, *after.SessionsValidFrom, time.Second)
	require.NotNil(t, after.LastLoginAt)
}

func TestAProfileSaveTouchesNoCredential(t *testing.T) {
	// A profile save must not write a password, second factor or is_active.
	ctx := t.Context()
	user := newUser(t)
	require.NoError(t, db(t).SetUserPassword(ctx, user.ID, "the-hash", true, time.Now()))

	require.NoError(t, db(t).UpdateUserProfile(ctx, user.ID, UserProfile{
		FullName:            "Renamed Person",
		Locale:              "en-GB",
		Theme:               "dark",
		PrivacyMode:         true,
		AnimationDurationMs: 0,
		ToastDurationMs:     9000,
	}))

	after, err := db(t).GetUser(ctx, user.ID)
	require.NoError(t, err)
	require.Equal(t, "Renamed Person", after.FullName)
	require.Equal(t, "en-GB", after.Locale)
	require.Equal(t, "dark", after.Theme)
	require.True(t, after.PrivacyMode)
	// Zero is a real choice, not an unset field for the default to replace.
	require.Equal(t, 0, after.AnimationDurationMs)
	require.Equal(t, 9000, after.ToastDurationMs)
	require.Equal(t, "the-hash", after.HashedPassword)
	require.True(t, after.MustChangePassword)
	require.True(t, after.IsActive)
}

func TestANarrowWriteToAMissingUserIsNotFound(t *testing.T) {
	// A silent no-op would report a saved preference nobody stored.
	err := db(t).TouchLastLogin(t.Context(), uuid.New(), time.Now())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLinkingAnOIDCIdentityLeavesTheRestAlone(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	require.NoError(t, db(t).SetUserPassword(ctx, user.ID, "the-hash", false, time.Now()))

	require.NoError(t, db(t).LinkOIDCIdentity(ctx, user.ID, "https://idp.example", "subject-1"))

	found, err := db(t).GetUserByOIDC(ctx, "https://idp.example", "subject-1")
	require.NoError(t, err)
	require.Equal(t, user.ID, found.ID)
	require.Equal(t, "the-hash", found.HashedPassword)
}

// errUndo rolls a test's transaction back, so a test that needs the install
// to have exactly the superusers it made leaves the shared schema as it was.
var errUndo = errors.New("undo the test's writes")

// onlySuperusers runs fn in a transaction where nobody is a superuser until fn
// makes them, then rolls everything back.
func onlySuperusers(t *testing.T, fn func(tx *Store)) {
	t.Helper()
	err := db(t).InTx(t.Context(), func(tx *Store) error {
		_, err := tx.db.Exec(t.Context(), `UPDATE users SET is_superuser = false WHERE is_superuser`)
		require.NoError(t, err)
		fn(tx)
		return errUndo
	})
	require.ErrorIs(t, err, errUndo)
}

func newUserIn(t *testing.T, tx *Store, superuser, active bool) *User {
	t.Helper()
	user := &User{
		Email:       fmt.Sprintf("%s-%s@example.test", t.Name(), uuid.NewString()),
		IsActive:    active,
		IsSuperuser: superuser,
	}
	require.NoError(t, tx.CreateUser(t.Context(), user))
	return user
}

func isSuperuser(t *testing.T, tx *Store, id uuid.UUID) bool {
	t.Helper()
	user, err := tx.GetUser(t.Context(), id)
	require.NoError(t, err)
	return user.IsSuperuser
}

func TestTheLastActiveSuperuserCannotBeDemoted(t *testing.T) {
	onlySuperusers(t, func(tx *Store) {
		ctx := t.Context()
		first := newUserIn(t, tx, true, true)

		require.ErrorIs(t, tx.SetUserSuperuser(ctx, first.ID, false), ErrLastSuperuser)
		require.True(t, isSuperuser(t, tx, first.ID))

		second := newUserIn(t, tx, true, true)
		require.NoError(t, tx.SetUserSuperuser(ctx, first.ID, false))
		require.False(t, isSuperuser(t, tx, first.ID))

		require.ErrorIs(t, tx.SetUserSuperuser(ctx, second.ID, false), ErrLastSuperuser)
		require.True(t, isSuperuser(t, tx, second.ID))
	})
}

func TestADeactivatedSuperuserDoesNotCountAsAnotherAdministrator(t *testing.T) {
	onlySuperusers(t, func(tx *Store) {
		ctx := t.Context()
		active := newUserIn(t, tx, true, true)
		inactive := newUserIn(t, tx, true, false)

		require.ErrorIs(t, tx.SetUserSuperuser(ctx, active.ID, false), ErrLastSuperuser,
			"a deactivated account cannot administer anything")
		require.NoError(t, tx.SetUserSuperuser(ctx, inactive.ID, false),
			"taking the flag from an account that cannot sign in leaves the active one")
		require.False(t, isSuperuser(t, tx, inactive.ID))
	})
}

func TestPromotingAndDemotingSayWhenThereIsNoSuchAccount(t *testing.T) {
	require.ErrorIs(t, db(t).SetUserSuperuser(t.Context(), uuid.New(), true), ErrNotFound)
	require.ErrorIs(t, db(t).SetUserSuperuser(t.Context(), uuid.New(), false), ErrNotFound)
}

func TestAPromotedAccountIsListedAsASuperuser(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	require.NoError(t, db(t).SetUserSuperuser(ctx, user.ID, true))

	users, err := db(t).ListUsers(ctx)
	require.NoError(t, err)
	var listed *User
	for i := range users {
		if users[i].ID == user.ID {
			listed = &users[i]
		}
	}
	require.NotNil(t, listed)
	require.True(t, listed.IsSuperuser)
	require.True(t, listed.IsActive)
}
