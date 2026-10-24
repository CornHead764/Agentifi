package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func newSQLiteSpace(t *testing.T, st *Store) SpaceID {
	t.Helper()
	space := &Space{Name: t.Name()}
	require.NoError(t, st.CreateSpace(t.Context(), space))
	return space.ID
}

// Invitations have no space to scope by, so the user id is in the WHERE clause.

func invite(t *testing.T, st *Store, spaceID SpaceID, userID uuid.UUID, role Role) *Membership {
	t.Helper()
	invited := time.Now().UTC()
	membership := &Membership{UserID: userID, Role: role, InvitedAt: &invited}
	require.NoError(t, st.CreateMembership(t.Context(), spaceID, membership))
	return membership
}

func TestAnInvitationIsListedUntilItIsTakenAndThenIsNot(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	spaceID := newSQLiteSpace(t, st)
	user := newUserIn(t, st, false, true)
	membership := invite(t, st, spaceID, user.ID, RoleMember)

	pending, err := st.ListPendingInvitations(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, membership.ID, pending[0].Membership.ID)
	require.Equal(t, t.Name(), pending[0].SpaceName)

	// It grants nothing while it is outstanding.
	_, _, err = st.ResolveSpace(ctx, user.ID, &spaceID)
	require.ErrorIs(t, err, ErrNotFound)

	accepted, err := st.AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, accepted.IsAccepted())

	space, resolved, err := st.ResolveSpace(ctx, user.ID, &spaceID)
	require.NoError(t, err)
	require.Equal(t, spaceID, space.ID)
	require.Equal(t, RoleMember, resolved.Role)

	pending, err = st.ListPendingInvitations(ctx, user.ID)
	require.NoError(t, err)
	require.Empty(t, pending)

	// No pending row is left, so a second attempt finds nothing.
	_, err = st.AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAnInvitationIsNotSomebodyElsesToTake(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	spaceID := newSQLiteSpace(t, st)
	invitee := newUserIn(t, st, false, true)
	stranger := newUserIn(t, st, false, true)
	membership := invite(t, st, spaceID, invitee.ID, RoleMember)

	_, err := st.AcceptInvitation(ctx, membership.ID, stranger.ID, time.Now().UTC())
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, st.DeclineInvitation(ctx, membership.ID, stranger.ID), ErrNotFound)

	still, err := st.GetMembership(ctx, spaceID, invitee.ID)
	require.NoError(t, err)
	require.False(t, still.IsAccepted())
}

func TestADeclinedInvitationIsGone(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	spaceID := newSQLiteSpace(t, st)
	user := newUserIn(t, st, false, true)
	membership := invite(t, st, spaceID, user.ID, RoleMember)

	require.NoError(t, st.DeclineInvitation(ctx, membership.ID, user.ID))
	_, err := st.GetMembership(ctx, spaceID, user.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, st.DeclineInvitation(ctx, membership.ID, user.ID), ErrNotFound)
}

func TestAJoinedSpaceIsNotDeclined(t *testing.T) {
	st := newSQLiteTest(t)
	// Leaving a space has its own rule (an owner may not abandon a space), so
	// declining reaches pending rows only.
	ctx := t.Context()
	spaceID := newSQLiteSpace(t, st)
	user := newUserIn(t, st, false, true)
	membership := invite(t, st, spaceID, user.ID, RoleOwner)
	_, err := st.AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.NoError(t, err)

	require.ErrorIs(t, st.DeclineInvitation(ctx, membership.ID, user.ID), ErrNotFound)
	_, _, err = st.ResolveSpace(ctx, user.ID, &spaceID)
	require.NoError(t, err)
}

// TestAnAdminAdministersWithoutOwning: both may change membership; only the
// owner's membership is off-limits to an admin and must be kept by the space.
func TestAnAdminAdministersWithoutOwning(t *testing.T) {
	require.True(t, RoleAdmin.IsOwner())
	require.False(t, RoleAdmin.Owns())
	require.True(t, RoleOwner.IsOwner())
	require.True(t, RoleOwner.Owns())
	require.False(t, RoleMember.IsOwner())
	require.False(t, RoleMember.Owns())
	require.False(t, RoleViewer.Owns())
}
