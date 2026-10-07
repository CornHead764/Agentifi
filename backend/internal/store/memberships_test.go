package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Invitations have no space to scope by, so the user id is in the WHERE clause.

func invite(t *testing.T, spaceID SpaceID, userID uuid.UUID, role Role) *Membership {
	t.Helper()
	invited := time.Now().UTC()
	membership := &Membership{UserID: userID, Role: role, InvitedAt: &invited}
	require.NoError(t, db(t).CreateMembership(t.Context(), spaceID, membership))
	return membership
}

func TestAnInvitationIsListedUntilItIsTakenAndThenIsNot(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	user := newUser(t)
	membership := invite(t, spaceID, user.ID, RoleMember)

	pending, err := db(t).ListPendingInvitations(ctx, user.ID)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, membership.ID, pending[0].Membership.ID)
	require.Equal(t, t.Name(), pending[0].SpaceName)

	// It grants nothing while it is outstanding.
	_, _, err = db(t).ResolveSpace(ctx, user.ID, &spaceID)
	require.ErrorIs(t, err, ErrNotFound)

	accepted, err := db(t).AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.NoError(t, err)
	require.True(t, accepted.IsAccepted())

	space, resolved, err := db(t).ResolveSpace(ctx, user.ID, &spaceID)
	require.NoError(t, err)
	require.Equal(t, spaceID, space.ID)
	require.Equal(t, RoleMember, resolved.Role)

	pending, err = db(t).ListPendingInvitations(ctx, user.ID)
	require.NoError(t, err)
	require.Empty(t, pending)

	// No pending row is left, so a second attempt finds nothing.
	_, err = db(t).AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.ErrorIs(t, err, ErrNotFound)
}

func TestAnInvitationIsNotSomebodyElsesToTake(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	invitee := newUser(t)
	stranger := newUser(t)
	membership := invite(t, spaceID, invitee.ID, RoleMember)

	_, err := db(t).AcceptInvitation(ctx, membership.ID, stranger.ID, time.Now().UTC())
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, db(t).DeclineInvitation(ctx, membership.ID, stranger.ID), ErrNotFound)

	still, err := db(t).GetMembership(ctx, spaceID, invitee.ID)
	require.NoError(t, err)
	require.False(t, still.IsAccepted())
}

func TestADeclinedInvitationIsGone(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	user := newUser(t)
	membership := invite(t, spaceID, user.ID, RoleMember)

	require.NoError(t, db(t).DeclineInvitation(ctx, membership.ID, user.ID))
	_, err := db(t).GetMembership(ctx, spaceID, user.ID)
	require.ErrorIs(t, err, ErrNotFound)
	require.ErrorIs(t, db(t).DeclineInvitation(ctx, membership.ID, user.ID), ErrNotFound)
}

func TestAJoinedSpaceIsNotDeclined(t *testing.T) {
	// Leaving a space has its own rule (an owner may not abandon a space), so
	// declining reaches pending rows only.
	ctx := t.Context()
	spaceID := newSpace(t)
	user := newUser(t)
	membership := invite(t, spaceID, user.ID, RoleOwner)
	_, err := db(t).AcceptInvitation(ctx, membership.ID, user.ID, time.Now().UTC())
	require.NoError(t, err)

	require.ErrorIs(t, db(t).DeclineInvitation(ctx, membership.ID, user.ID), ErrNotFound)
	_, _, err = db(t).ResolveSpace(ctx, user.ID, &spaceID)
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
