package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

func deleteSpacePath(space store.Space) string {
	return "/spaces/" + space.ID.String() + "/delete"
}

func TestAnOwnerDeletesASpaceForEveryMember(t *testing.T) {
	c := newClient(t)
	owner, partner := makeUser(t, testPassword), makeUser(t, testPassword)
	makeSpace(t, owner, "Kept", store.RoleOwner, true)
	makeSpace(t, partner, "Theirs", store.RoleOwner, true)
	shared := makeSpace(t, owner, "Old household", store.RoleOwner, true)
	joined := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(t.Context(), shared.ID, &store.Membership{
		UserID: partner.ID, Role: store.RoleMember, InvitedAt: &joined, AcceptedAt: &joined,
	}))
	storetest.NewAccount(t, shared.ID, "Checking")

	deleted := c.as(owner).post(deleteSpacePath(shared), map[string]any{
		"confirm_name": " Old household ",
	}).requireStatus(http.StatusOK).json()
	// The test server writes no backups.
	require.Nil(t, deleted["backup_set"])

	_, err := db(t).GetSpace(t.Context(), shared.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	for _, user := range []store.User{owner, partner} {
		listed := c.as(user).get("/spaces").requireStatus(http.StatusOK).list()
		require.Len(t, listed, 1)
		require.NotEqual(t, shared.ID.String(), listed[0]["id"])
	}
}

func TestOnlyAnOwnerMayDeleteASpace(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joined := time.Now().UTC()
	for _, role := range []store.Role{store.RoleAdmin, store.RoleMember, store.RoleViewer} {
		other := makeUser(t, testPassword)
		makeSpace(t, other, "Their own", store.RoleOwner, true)
		require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, &store.Membership{
			UserID: other.ID, Role: role, InvitedAt: &joined, AcceptedAt: &joined,
		}))
		c.as(other).post(deleteSpacePath(space), map[string]any{"confirm_name": "Household"}).
			requireStatus(http.StatusForbidden)
	}
	stranger := makeUser(t, testPassword)
	makeSpace(t, stranger, "Elsewhere", store.RoleOwner, true)
	c.as(stranger).post(deleteSpacePath(space), map[string]any{"confirm_name": "Household"}).
		requireStatus(http.StatusNotFound)

	_, err := db(t).GetSpace(t.Context(), space.ID)
	require.NoError(t, err)
}

func TestDeletingASpaceNeedsItsNameTypedOut(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	makeSpace(t, owner, "Kept", store.RoleOwner, true)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)

	for _, typed := range []string{"", "household", "Household 2"} {
		c.as(owner).post(deleteSpacePath(space), map[string]any{"confirm_name": typed}).
			requireStatus(http.StatusUnprocessableEntity)
	}
	_, err := db(t).GetSpace(t.Context(), space.ID)
	require.NoError(t, err)
}

func TestTheLastSpaceIsNotDeleted(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Only one", store.RoleOwner, true)

	refused := c.as(owner).post(deleteSpacePath(space), map[string]any{"confirm_name": "Only one"}).
		requireStatus(http.StatusConflict).json()
	require.Equal(t, "last_space", refused["code"])
	_, err := db(t).GetSpace(t.Context(), space.ID)
	require.NoError(t, err)
}
