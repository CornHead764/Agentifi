package auth

import (
	"context"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// fakeSpaces reproduces store.ResolveSpace's contract — oldest accepted
// membership, an explicit request filters, an unaccepted invitation is
// invisible — so the mapping this package does on top of it can be tested
// without a database. The query itself is covered in internal/store.
type fakeSpaces struct {
	rows []spaceRow
}

type spaceRow struct {
	space      store.Space
	membership store.Membership
}

func (f *fakeSpaces) ResolveSpace(_ context.Context, userID uuid.UUID, requested *store.SpaceID) (store.Space, store.Membership, error) {
	for _, row := range f.rows {
		switch {
		case row.membership.UserID != userID,
			!row.membership.IsAccepted(),
			row.space.IsDeleted,
			requested != nil && row.space.ID != *requested:
			continue
		}
		return row.space, row.membership, nil
	}
	return store.Space{}, store.Membership{}, store.ErrNotFound
}

func spaceFor(t *testing.T, userID uuid.UUID, name string, role store.Role, accepted bool, created time.Time) spaceRow {
	t.Helper()
	id := store.NewSpaceID()
	membership := store.Membership{
		ID:        uuid.New(),
		SpaceID:   id,
		UserID:    userID,
		Role:      role,
		CreatedAt: created,
	}
	if accepted {
		membership.AcceptedAt = &created
	}
	return spaceRow{
		space:      store.Space{ID: id, Name: name},
		membership: membership,
	}
}

func TestWithoutAHeaderTheOldestAcceptedMembershipWins(t *testing.T) {
	user := store.User{ID: uuid.New(), IsActive: true}
	older := spaceFor(t, user.ID, "Household", store.RoleOwner, true, time.Now().Add(-time.Hour))
	newer := spaceFor(t, user.ID, "Side project", store.RoleMember, true, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{older, newer}}

	resolved, err := ResolveSpace(context.Background(), spaces, user, "")
	require.NoError(t, err)
	require.Equal(t, "Household", resolved.Space.Name)
	require.Equal(t, store.RoleOwner, resolved.Role())
}

func TestTheHeaderSelectsExplicitly(t *testing.T) {
	user := store.User{ID: uuid.New(), IsActive: true}
	older := spaceFor(t, user.ID, "Household", store.RoleOwner, true, time.Now().Add(-time.Hour))
	newer := spaceFor(t, user.ID, "Side project", store.RoleViewer, true, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{older, newer}}

	request := httptest.NewRequest("GET", "/transactions", nil)
	request.Header.Set(HeaderSpaceID, newer.space.ID.String())

	resolved, err := ResolveSpaceForRequest(context.Background(), spaces, user, request)
	require.NoError(t, err)
	require.Equal(t, "Side project", resolved.Space.Name)
}

func TestASpaceTheUserIsNotAMemberOfIsNotFoundAndNeverForbidden(t *testing.T) {
	// A distinguishable "you may not" confirms the space exists, which is a
	// membership oracle. All three of these must be one answer.
	user := store.User{ID: uuid.New(), IsActive: true}
	mine := spaceFor(t, user.ID, "Household", store.RoleOwner, true, time.Now())
	someoneElse := spaceFor(t, uuid.New(), "Not yours", store.RoleOwner, true, time.Now())
	invited := spaceFor(t, user.ID, "Invitation", store.RoleMember, false, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{mine, someoneElse, invited}}

	for name, requested := range map[string]string{
		"someone else's space": someoneElse.space.ID.String(),
		"an invitation":        invited.space.ID.String(),
		"a space nobody has":   store.NewSpaceID().String(),
		"a malformed header":   "not-a-uuid",
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ResolveSpace(context.Background(), spaces, user, requested)
			require.ErrorIs(t, err, ErrNoSpace)
		})
	}
}

func TestAnOutstandingInvitationGrantsNothingWithoutAHeaderEither(t *testing.T) {
	// There is no fallback space: an unaccepted invitation must not become the
	// default just because it is the only row.
	user := store.User{ID: uuid.New(), IsActive: true}
	invited := spaceFor(t, user.ID, "Invitation", store.RoleMember, false, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{invited}}

	_, err := ResolveSpace(context.Background(), spaces, user, "")
	require.ErrorIs(t, err, ErrNoSpace)
}

func TestAViewerReadsAndDoesNotWrite(t *testing.T) {
	user := store.User{ID: uuid.New(), IsActive: true}
	viewer := spaceFor(t, user.ID, "Household", store.RoleViewer, true, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{viewer}}

	resolved, err := ResolveSpace(context.Background(), spaces, user, "")
	require.NoError(t, err)
	require.False(t, resolved.CanWrite())
	require.ErrorIs(t, resolved.RequireWrite(), ErrReadOnly)
	require.ErrorIs(t, resolved.RequireOwner(), ErrReadOnly)
}

func TestAMemberWritesButDoesNotOwn(t *testing.T) {
	user := store.User{ID: uuid.New(), IsActive: true}
	member := spaceFor(t, user.ID, "Household", store.RoleMember, true, time.Now())
	spaces := &fakeSpaces{rows: []spaceRow{member}}

	resolved, err := ResolveSpace(context.Background(), spaces, user, "")
	require.NoError(t, err)
	require.NoError(t, resolved.RequireWrite())
	require.ErrorIs(t, resolved.RequireOwner(), ErrReadOnly)
	require.Equal(t, user.ID, resolved.UserID())
	require.Equal(t, member.space.ID, resolved.ID())
}
