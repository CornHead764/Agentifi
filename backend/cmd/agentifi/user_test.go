package main

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The store's half of these rules is tested against Postgres in
// internal/store; these are what the command makes of its answers.

type fakeAccounts struct {
	users []store.User
	// refuse answers every demotion with the store's last-superuser refusal.
	refuse bool
	set    map[uuid.UUID]bool
}

func (f *fakeAccounts) GetUserByEmail(_ context.Context, email string) (store.User, error) {
	for _, user := range f.users {
		if user.Email == email {
			return user, nil
		}
	}
	return store.User{}, fmt.Errorf("store: get user by email: %w", store.ErrNotFound)
}

func (f *fakeAccounts) SetUserSuperuser(_ context.Context, id uuid.UUID, superuser bool) error {
	if f.refuse && !superuser {
		return fmt.Errorf("store: set user superuser: %w", store.ErrLastSuperuser)
	}
	if f.set == nil {
		f.set = map[uuid.UUID]bool{}
	}
	f.set[id] = superuser
	return nil
}

func (f *fakeAccounts) ListUsers(context.Context) ([]store.User, error) { return f.users, nil }

func someone(email string, active, superuser bool) store.User {
	return store.User{ID: uuid.New(), Email: email, IsActive: active, IsSuperuser: superuser}
}

func TestUserAdminOnMakesAnAccountASuperuser(t *testing.T) {
	user := someone("first@example.test", true, false)
	accounts := &fakeAccounts{users: []store.User{user}}
	var out strings.Builder

	require.NoError(t, setSuperuser(t.Context(), accounts, "  First@Example.TEST ", true, &out))

	require.Equal(t, map[uuid.UUID]bool{user.ID: true}, accounts.set)
	require.Contains(t, out.String(), "first@example.test administers the server")
}

func TestUserAdminOffTakesItAway(t *testing.T) {
	user := someone("second@example.test", true, true)
	accounts := &fakeAccounts{users: []store.User{user}}
	var out strings.Builder

	require.NoError(t, setSuperuser(t.Context(), accounts, "second@example.test", false, &out))

	require.Equal(t, map[uuid.UUID]bool{user.ID: false}, accounts.set)
	require.Equal(t, "second@example.test does not administer the server\n", out.String())
}

func TestUserAdminOffRefusesTheLastActiveSuperuser(t *testing.T) {
	accounts := &fakeAccounts{users: []store.User{someone("only@example.test", true, true)}, refuse: true}
	var out strings.Builder

	err := setSuperuser(t.Context(), accounts, "only@example.test", false, &out)

	require.ErrorContains(t, err, "only@example.test is the last active superuser")
	require.ErrorContains(t, err, "agentifi user admin --email <address> --on")
	require.Empty(t, out.String(), "nothing is reported done")
}

func TestUserAdminWritesNothingWhenTheFlagIsAlreadySo(t *testing.T) {
	accounts := &fakeAccounts{users: []store.User{someone("same@example.test", true, true)}}
	var out strings.Builder

	require.NoError(t, setSuperuser(t.Context(), accounts, "same@example.test", true, &out))

	require.Empty(t, accounts.set)
	require.Equal(t, "same@example.test administers the server; nothing changed\n", out.String())
}

func TestUserAdminNamesAnAddressWithNoAccount(t *testing.T) {
	err := setSuperuser(t.Context(), &fakeAccounts{}, "nobody@example.test", true, &strings.Builder{})
	require.EqualError(t, err, "agentifi user: no account for nobody@example.test")
}

func TestUserListShowsEachAccountActiveAndSuperuser(t *testing.T) {
	accounts := &fakeAccounts{users: []store.User{
		someone("admin@example.test", true, true),
		someone("member@example.test", true, false),
		someone("gone@example.test", false, false),
	}}
	var out strings.Builder

	require.NoError(t, listUsers(t.Context(), accounts, &out))

	require.Equal(t, ""+
		"EMAIL                ACTIVE  SUPERUSER\n"+
		"admin@example.test   yes     yes\n"+
		"member@example.test  yes     no\n"+
		"gone@example.test    no      no\n", out.String())
}

func TestUserListOnAFreshInstallSaysHowToMakeTheFirst(t *testing.T) {
	var out strings.Builder
	require.NoError(t, listUsers(t.Context(), &fakeAccounts{}, &out))
	require.Contains(t, out.String(), "agentifi user add --email <address> --superuser")
}
