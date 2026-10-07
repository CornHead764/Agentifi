package service

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

func firstAccount(email string) NewAccount {
	return NewAccount{
		Email:     email,
		Password:  "a long enough passphrase",
		Placement: Placement{SpaceName: "Household", Currency: "USD"},
	}
}

func TestTheFirstAccountAdministersTheServerAndOwnsItsSpace(t *testing.T) {
	empty := storetest.Empty(t)
	ctx := t.Context()

	created, err := CreateFirstAccount(ctx, empty, firstAccount("first@example.test"), time.Now())
	require.NoError(t, err)
	require.True(t, created.IsSuperuser)
	require.False(t, created.MustChangePassword)

	spaces, err := empty.ListSpacesForUser(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, spaces, 1)
	require.Equal(t, "Household", spaces[0].Name)
	membership, err := empty.GetMembership(ctx, spaces[0].ID, created.ID)
	require.NoError(t, err)
	require.Equal(t, store.RoleOwner, membership.Role)
}

func TestTheFirstAccountSignUpClosesOnceAnyAccountExists(t *testing.T) {
	empty := storetest.Empty(t)
	ctx := t.Context()

	_, err := CreateAccount(ctx, empty, firstAccount("made-by-the-cli@example.test"), time.Now())
	require.NoError(t, err)

	_, err = CreateFirstAccount(ctx, empty, firstAccount("second@example.test"), time.Now())
	require.ErrorIs(t, err, ErrNotFirstAccount)
	// Refused before the address is looked at, so an address already taken
	// gets the same answer.
	_, err = CreateFirstAccount(ctx, empty, firstAccount("made-by-the-cli@example.test"), time.Now())
	require.ErrorIs(t, err, ErrNotFirstAccount)

	users, err := empty.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
}

func TestASecondFirstAccountWaitsForTheFirstAndIsRefused(t *testing.T) {
	empty := storetest.Empty(t)
	ctx := t.Context()

	claimed, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	letGo := func() { releaseOnce.Do(func() { close(release) }) }
	// Before the schema's own cleanup, which waits for the held connection.
	t.Cleanup(letGo)
	firstDone := make(chan error, 1)
	go func() {
		firstDone <- empty.InTx(ctx, func(tx *store.Store) error {
			first, err := tx.ClaimFirstAccount(ctx)
			if err != nil || !first {
				close(claimed)
				return fmt.Errorf("the first claim saw an account: %v", err)
			}
			close(claimed)
			<-release
			return tx.CreateUser(ctx, &store.User{Email: "first@example.test", IsActive: true})
		})
	}()
	<-claimed

	// Started while the first is between its claim and its commit, which is
	// where an unlocked check would also find the table empty.
	secondDone := make(chan error, 1)
	go func() {
		_, err := CreateFirstAccount(ctx, empty, firstAccount("second@example.test"), time.Now())
		secondDone <- err
	}()
	select {
	case err := <-secondDone:
		t.Fatalf("the second sign-up finished while the first held its claim: %v", err)
	case <-time.After(300 * time.Millisecond):
	}
	letGo()

	require.NoError(t, <-firstDone)
	require.ErrorIs(t, <-secondDone, ErrNotFirstAccount)
	users, err := empty.ListUsers(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "first@example.test", users[0].Email)
}
