package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Which spaces the caller can see, and who else is in them.
//
// Every assertion here is about the caller's own memberships being the
// authority. A space id travels in a header, in URLs and in bug reports, so it
// is an argument to the query and never the reason for the answer.

func makeSpace(t *testing.T, user store.User, name string, role store.Role, accepted bool) store.Space {
	t.Helper()
	space := &store.Space{Name: name, PrimaryCurrency: "USD"}
	require.NoError(t, db(t).CreateSpace(t.Context(), space))

	membership := &store.Membership{UserID: user.ID, Role: role}
	invited := time.Now().UTC()
	membership.InvitedAt = &invited
	if accepted {
		membership.AcceptedAt = &invited
	}
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, membership))
	return *space
}

func TestTheListingIsTheCallersMemberships(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	mine := makeSpace(t, user, "Mine", store.RoleOwner, true)
	makeSpace(t, stranger, "Theirs", store.RoleOwner, true)

	listed := c.as(user).get("/spaces").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, mine.ID.String(), listed[0]["id"])
	require.Equal(t, "owner", listed[0]["role"])
	require.Equal(t, true, listed[0]["can_write"])
	require.Equal(t, true, listed[0]["is_owner"])
}

func TestAnUnacceptedInvitationGrantsNothing(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	makeSpace(t, user, "Pending", store.RoleMember, false)

	require.Empty(t, c.as(user).get("/spaces").requireStatus(http.StatusOK).list())
	c.get("/spaces/current").requireStatus(http.StatusNotFound)
}

func TestADeletedSpaceDisappearsFromTheListing(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Retired", store.RoleOwner, true)

	space.IsDeleted = true
	require.NoError(t, db(t).UpdateSpace(t.Context(), &space))
	require.Empty(t, c.as(user).get("/spaces").requireStatus(http.StatusOK).list())
}

func TestAViewerIsListedAsReadOnly(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	makeSpace(t, user, "Household", store.RoleViewer, true)

	listed := c.as(user).get("/spaces").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, false, listed[0]["can_write"])
	require.Equal(t, false, listed[0]["is_owner"])
}

func TestTheCurrentSpaceFollowsTheHeader(t *testing.T) {
	// A client that guesses which space the server resolved will eventually
	// guess wrong and write to the other one, which is why this has its own
	// endpoint.
	c := newClient(t)
	user := makeUser(t, testPassword)
	first := makeSpace(t, user, "First", store.RoleOwner, true)
	second := makeSpace(t, user, "Second", store.RoleOwner, true)

	c.as(user)
	require.Equal(t, "First",
		c.get("/spaces/current").requireStatus(http.StatusOK).json()["name"],
		"the oldest membership is the default")

	c.inSpace(second.ID)
	require.Equal(t, "Second", c.get("/spaces/current").requireStatus(http.StatusOK).json()["name"])

	c.inSpace(first.ID)
	require.Equal(t, "First", c.get("/spaces/current").requireStatus(http.StatusOK).json()["name"])
}

func TestASpaceTheCallerIsNotInIsNotThere(t *testing.T) {
	// 404 and not 403, and the same 404 an id nobody has ever used gets: a
	// distinguishable refusal confirms the space exists.
	c := newClient(t)
	user := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	makeSpace(t, user, "Mine", store.RoleOwner, true)
	theirs := makeSpace(t, stranger, "Theirs", store.RoleOwner, true)

	c.as(user)
	real := c.get("/spaces/" + theirs.ID.String() + "/members").requireStatus(http.StatusNotFound)
	invented := c.get("/spaces/" + uuid.NewString() + "/members").requireStatus(http.StatusNotFound)
	require.Equal(t, real.Body.String(), invented.Body.String())

	c.inSpace(theirs.ID)
	c.get("/spaces/current").requireStatus(http.StatusNotFound)
}

func TestMembersIncludeOutstandingInvitations(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)

	invited := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, &store.Membership{
		UserID: invitee.ID, Role: store.RoleMember, InvitedAt: &invited,
	}))

	members := c.as(owner).get("/spaces/" + space.ID.String() + "/members").
		requireStatus(http.StatusOK).list()
	require.Len(t, members, 2)

	byEmail := map[string]map[string]any{}
	for _, member := range members {
		byEmail[member["email"].(string)] = member
	}
	require.NotNil(t, byEmail[owner.Email]["accepted_at"])
	require.Nil(t, byEmail[invitee.Email]["accepted_at"])
	require.NotNil(t, byEmail[invitee.Email]["invited_at"])
}

func TestTheListingNeedsACaller(t *testing.T) {
	newClient(t).get("/spaces").requireStatus(http.StatusUnauthorized)
}

// --- Managing a space and its people -----------------------------------------
//
// Three rules are checked here rather than trusted to the screen, because the
// screen is not the only caller: an invitation grants nothing until it is
// accepted, only an owner may change who is in a space, and no change may
// leave a space without an accepted owner — a space in that state can never be
// reached again, since membership is the only way in and only an owner hands
// it out.

func joinSpace(t *testing.T, space store.Space, user store.User, role store.Role, accepted bool) store.Membership {
	t.Helper()
	membership := &store.Membership{UserID: user.ID, Role: role}
	at := time.Now().UTC()
	membership.InvitedAt = &at
	if accepted {
		membership.AcceptedAt = &at
	}
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, membership))
	return *membership
}

func TestCreatingASpaceMakesTheCallerItsOwner(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)

	created := c.as(user).post("/spaces", map[string]any{"name": "  Cabin  "}).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, "Cabin", created["name"], "the name is trimmed")
	require.Equal(t, "owner", created["role"])
	require.NotNil(t, created["joined_at"], "the creator has accepted, not been invited")

	listed := c.get("/spaces").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, created["id"], listed[0]["id"])
}

func TestASpaceNeedsAName(t *testing.T) {
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	c.post("/spaces", map[string]any{"name": "   "}).requireStatus(http.StatusUnprocessableEntity)
}

func TestOnlyAnOwnerRenamesASpace(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	viewer := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, viewer, store.RoleViewer, true)

	renamed := c.as(owner).patch("/spaces/"+space.ID.String(), map[string]any{"name": "The House"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "The House", renamed["name"])

	c.as(viewer).patch("/spaces/"+space.ID.String(), map[string]any{"name": "Mine now"}).
		requireStatus(http.StatusForbidden)
	// Not a 403: a refusal a non-member can tell apart confirms the space exists.
	c.as(stranger).patch("/spaces/"+space.ID.String(), map[string]any{"name": "Mine now"}).
		requireStatus(http.StatusNotFound)
}

func TestAnInvitationGrantsNothingUntilItIsAccepted(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)

	invited := c.as(owner).
		post("/spaces/"+space.ID.String()+"/members",
			map[string]any{"email": invitee.Email, "role": "member"}).
		requireStatus(http.StatusCreated).json()
	require.NotNil(t, invited["invited_at"])
	require.Nil(t, invited["accepted_at"], "an invitation is pending, not membership")

	// The invitee can see nothing at all until they accept.
	require.Empty(t, c.as(invitee).get("/spaces").requireStatus(http.StatusOK).list())
	c.get("/spaces/" + space.ID.String() + "/members").requireStatus(http.StatusNotFound)
}

func TestAnInvitationNeedsAnAccountAndCannotBeRepeated(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	c.as(owner)

	// Only the server administrator makes logins, so the refusal says to ask
	// them — and does not repeat the address back.
	refused := c.post("/spaces/"+space.ID.String()+"/members",
		map[string]any{"email": "nobody@example.test", "role": "member"}).
		requireStatus(http.StatusConflict).json()
	require.Contains(t, refused["detail"], "server administrator")
	require.NotContains(t, refused["detail"], "nobody@example.test")

	body := map[string]any{"email": invitee.Email, "role": "member"}
	c.post("/spaces/"+space.ID.String()+"/members", body).requireStatus(http.StatusCreated)
	c.post("/spaces/"+space.ID.String()+"/members", body).requireStatus(http.StatusConflict)

	c.post("/spaces/"+space.ID.String()+"/members",
		map[string]any{"email": makeUser(t, testPassword).Email, "role": "chief"}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestOnlyAnOwnerChangesWhoIsInASpace(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	viewer := makeUser(t, testPassword)
	outsider := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	membership := joinSpace(t, space, viewer, store.RoleViewer, true)

	c.as(viewer)
	c.post("/spaces/"+space.ID.String()+"/members",
		map[string]any{"email": outsider.Email, "role": "member"}).
		requireStatus(http.StatusForbidden)
	c.patch("/spaces/"+space.ID.String()+"/members/"+membership.ID.String(),
		map[string]any{"role": "owner"}).
		requireStatus(http.StatusForbidden)

	promoted := c.as(owner).
		patch("/spaces/"+space.ID.String()+"/members/"+membership.ID.String(),
			map[string]any{"role": "member"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "member", promoted["role"])

	pending := joinSpace(t, space, outsider, store.RoleMember, false)
	changed := c.patch("/spaces/"+space.ID.String()+"/members/"+pending.ID.String(),
		map[string]any{"role": "viewer"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "viewer", changed["role"])
	require.Nil(t, changed["accepted_at"], "a role change does not accept an invitation")
}

func TestAnOwnerCannotCreateALogin(t *testing.T) {
	// Anybody signed in can create a space and own it, so a login made from a
	// space would be a login anybody could make.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	membership := ownMembership(t, c.as(owner), space, owner)
	address := "made-by-an-owner-" + uuid.NewString()[:8] + "@example.test"
	body := map[string]any{"email": address, "role": "member", "password": testPassword}

	for _, path := range []string{
		"/spaces/" + space.ID.String() + "/members/accounts",
		"/admin/users",
	} {
		status := c.post(path, body).Code
		require.GreaterOrEqual(t, status, 400, "%s answered %d", path, status)
	}
	status := c.post("/spaces/"+space.ID.String()+"/members/"+membership+"/password",
		map[string]any{}).Code
	require.GreaterOrEqual(t, status, 400, "an owner resets no password from a space")

	_, err := db(t).GetUserByEmail(t.Context(), address)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestAnAdminInvitesAndAMemberOrViewerCannot(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	admin := makeUser(t, testPassword)
	editor := makeUser(t, testPassword)
	viewer := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, admin, store.RoleAdmin, true)
	joinSpace(t, space, editor, store.RoleMember, true)
	joinSpace(t, space, viewer, store.RoleViewer, true)
	members := "/spaces/" + space.ID.String() + "/members"

	for _, refused := range []store.User{editor, viewer} {
		c.as(refused).post(members,
			map[string]any{"email": makeUser(t, testPassword).Email, "role": "member"}).
			requireStatus(http.StatusForbidden)
	}

	invitee := makeUser(t, testPassword)
	invited := c.as(admin).post(members,
		map[string]any{"email": invitee.Email, "role": "member"}).
		requireStatus(http.StatusCreated).json()
	require.Nil(t, invited["accepted_at"], "an admin's invitation grants nothing until accepted")
}

func TestTheLastOwnerCannotDemoteOrRemoveThemselves(t *testing.T) {
	// A space with no accepted owner can never be reached again: membership is
	// the only way in, and only an owner hands one out.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	member := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, member, store.RoleMember, true)

	members := c.as(owner).get("/spaces/" + space.ID.String() + "/members").
		requireStatus(http.StatusOK).list()
	var mine string
	for _, one := range members {
		if one["user_id"] == owner.ID.String() {
			mine = one["id"].(string)
		}
	}
	require.NotEmpty(t, mine)

	c.patch("/spaces/"+space.ID.String()+"/members/"+mine, map[string]any{"role": "member"}).
		requireStatus(http.StatusConflict)
	c.del("/spaces/" + space.ID.String() + "/members/" + mine).
		requireStatus(http.StatusConflict)
}

func TestAnInvitedOwnerIsNotAnOwnerYet(t *testing.T) {
	// The successor has been invited as an owner but has not accepted, so the
	// space would be left with nobody who can administer it.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	successor := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, successor, store.RoleOwner, false)

	mine := ownMembership(t, c.as(owner), space, owner)
	c.del("/spaces/" + space.ID.String() + "/members/" + mine).requireStatus(http.StatusConflict)
}

func TestAnOwnerLeavesOnceThereIsAnother(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	successor := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, successor, store.RoleOwner, true)

	mine := ownMembership(t, c.as(owner), space, owner)
	c.del("/spaces/" + space.ID.String() + "/members/" + mine).requireStatus(http.StatusNoContent)
	require.Empty(t, c.get("/spaces").requireStatus(http.StatusOK).list())
	c.get("/spaces/" + space.ID.String() + "/members").requireStatus(http.StatusNotFound)
}

func TestAnybodyMayLeaveTheirOwnSpace(t *testing.T) {
	// Leaving is not an owner's privilege; taking somebody else out is.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	viewer := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	membership := joinSpace(t, space, viewer, store.RoleViewer, true)

	c.as(viewer).del("/spaces/" + space.ID.String() + "/members/" + membership.ID.String()).
		requireStatus(http.StatusNoContent)
	require.Empty(t, c.get("/spaces").requireStatus(http.StatusOK).list())
}

func TestAMembershipFromAnotherSpaceIsNotThere(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	mine := makeSpace(t, owner, "Mine", store.RoleOwner, true)
	theirs := makeSpace(t, stranger, "Theirs", store.RoleOwner, true)
	elsewhere := ownMembership(t, newClient(t).as(stranger), theirs, stranger)

	c.as(owner).del("/spaces/" + mine.ID.String() + "/members/" + elsewhere).
		requireStatus(http.StatusNotFound)
	c.del("/spaces/" + theirs.ID.String() + "/members/" + elsewhere).
		requireStatus(http.StatusNotFound)
}

// ownMembership is the caller's own membership id in one space, which is what
// leaving and self-demotion address.
func ownMembership(t *testing.T, c *client, space store.Space, user store.User) string {
	t.Helper()
	for _, one := range c.get("/spaces/" + space.ID.String() + "/members").
		requireStatus(http.StatusOK).list() {
		if one["user_id"] == user.ID.String() {
			return one["id"].(string)
		}
	}
	t.Fatalf("no membership for %s", user.Email)
	return ""
}

// --- Taking an invitation -----------------------------------------------------
//
// The other side of inviting somebody. An invitation grants nothing until it
// is accepted, which means the invitee has to be able to accept it and nobody
// else may: the caller has no space here — that is what they are being invited
// into — so the membership row is scoped by the user id rather than by a
// space, and these are what hold it to that.

func TestAnInvitationIsAcceptedByItsOwnInvitee(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)

	invited := c.as(owner).
		post("/spaces/"+space.ID.String()+"/members",
			map[string]any{"email": invitee.Email, "role": "member"}).
		requireStatus(http.StatusCreated).json()

	// Until it is taken, the invitation is the only thing that names the
	// space: the listing is empty and resolving it is a 404.
	c.as(invitee)
	require.Empty(t, c.get("/spaces").requireStatus(http.StatusOK).list())
	pending := c.get("/spaces/invitations").requireStatus(http.StatusOK).list()
	require.Len(t, pending, 1)
	require.Equal(t, invited["id"], pending[0]["id"])
	require.Equal(t, space.ID.String(), pending[0]["space_id"])
	require.Equal(t, "Household", pending[0]["space_name"])
	require.Equal(t, "member", pending[0]["role"])
	require.NotNil(t, pending[0]["invited_at"])

	accepted := c.post("/spaces/invitations/"+invited["id"].(string)+"/accept", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, space.ID.String(), accepted["id"])
	require.Equal(t, "member", accepted["role"])
	require.NotNil(t, accepted["joined_at"], "accepting is what joins")

	require.Len(t, c.get("/spaces").requireStatus(http.StatusOK).list(), 1)
	require.Empty(t, c.get("/spaces/invitations").requireStatus(http.StatusOK).list())
	c.inSpace(space.ID)
	require.Equal(t, "Household",
		c.get("/spaces/current").requireStatus(http.StatusOK).json()["name"])

	// Taken once. A second accept has no pending row to stamp.
	c.post("/spaces/invitations/"+invited["id"].(string)+"/accept", nil).
		requireStatus(http.StatusNotFound)
}

func TestAnInvitationIsNotSomebodyElsesToTake(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	pending := joinSpace(t, space, invitee, store.RoleMember, false)

	// The same 404 an id nobody has ever used gets: a refusal a stranger can
	// tell apart confirms the invitation exists.
	c.as(stranger)
	real := c.post("/spaces/invitations/"+pending.ID.String()+"/accept", nil).
		requireStatus(http.StatusNotFound)
	invented := c.post("/spaces/invitations/"+uuid.NewString()+"/accept", nil).
		requireStatus(http.StatusNotFound)
	require.Equal(t, real.Body.String(), invented.Body.String())
	c.del("/spaces/invitations/" + pending.ID.String()).requireStatus(http.StatusNotFound)
	require.Empty(t, c.get("/spaces").requireStatus(http.StatusOK).list())
	require.Empty(t, c.get("/spaces/invitations").requireStatus(http.StatusOK).list())

	// And it is untouched for the person it was addressed to.
	require.Len(t, c.as(invitee).get("/spaces/invitations").
		requireStatus(http.StatusOK).list(), 1)
}

func TestADeclinedInvitationIsGone(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	pending := joinSpace(t, space, invitee, store.RoleMember, false)

	c.as(invitee).del("/spaces/invitations/" + pending.ID.String()).
		requireStatus(http.StatusNoContent)
	require.Empty(t, c.get("/spaces/invitations").requireStatus(http.StatusOK).list())
	require.Empty(t, c.get("/spaces").requireStatus(http.StatusOK).list())

	// It was the membership row rather than a copy of one, so it is gone from
	// the owner's side too.
	require.Len(t, c.as(owner).get("/spaces/"+space.ID.String()+"/members").
		requireStatus(http.StatusOK).list(), 1)
}

func TestAJoinedSpaceIsNotLeftThroughTheInvitationRoutes(t *testing.T) {
	// Leaving is addressed by space id and is somebody else's rule — an owner
	// may not walk out on a space with nobody left to administer it. Declining
	// must not be a way around that, so these reach pending rows only.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	mine := ownMembership(t, c.as(owner), space, owner)

	c.del("/spaces/invitations/" + mine).requireStatus(http.StatusNotFound)
	c.post("/spaces/invitations/"+mine+"/accept", nil).requireStatus(http.StatusNotFound)
	require.Len(t, c.get("/spaces").requireStatus(http.StatusOK).list(), 1)
}

func TestAnInvitationIntoADeletedSpaceIsNotOffered(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Retired", store.RoleOwner, true)
	pending := joinSpace(t, space, invitee, store.RoleMember, false)

	space.IsDeleted = true
	require.NoError(t, db(t).UpdateSpace(t.Context(), &space))

	c.as(invitee)
	require.Empty(t, c.get("/spaces/invitations").requireStatus(http.StatusOK).list())
	c.post("/spaces/invitations/"+pending.ID.String()+"/accept", nil).
		requireStatus(http.StatusNotFound)
}

// --- An admin is not an owner --------------------------------------------------
//
// Both roles administer a space, which is what is_owner reports and what the
// membership routes check. The difference is the owner's own membership: an
// admin manages the people in a space and the owner is not one of the people
// they manage, because an admin cannot make another owner and so cannot pass
// the space on.

func TestAnAdminManagesMembersButNotTheOwner(t *testing.T) {
	c := newClient(t)
	owner := makeUser(t, testPassword)
	admin := makeUser(t, testPassword)
	member := makeUser(t, testPassword)
	outsider := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	adminship := joinSpace(t, space, admin, store.RoleAdmin, true)
	membership := joinSpace(t, space, member, store.RoleMember, true)
	ownership := ownMembership(t, c.as(owner), space, owner)

	c.as(admin)
	members := "/spaces/" + space.ID.String() + "/members"
	require.Equal(t, "viewer",
		c.patch(members+"/"+membership.ID.String(), map[string]any{"role": "viewer"}).
			requireStatus(http.StatusOK).json()["role"],
		"managing everybody else is what an admin is for")

	c.patch(members+"/"+ownership, map[string]any{"role": "member"}).
		requireStatus(http.StatusForbidden)
	c.del(members + "/" + ownership).requireStatus(http.StatusForbidden)
	c.patch(members+"/"+adminship.ID.String(), map[string]any{"role": "owner"}).
		requireStatus(http.StatusForbidden)
	c.patch(members+"/"+membership.ID.String(), map[string]any{"role": "owner"}).
		requireStatus(http.StatusForbidden)
	c.post(members, map[string]any{"email": outsider.Email, "role": "owner"}).
		requireStatus(http.StatusForbidden)

	// The owner is still an owner after all of that.
	require.Equal(t, "owner", c.as(owner).get("/spaces").
		requireStatus(http.StatusOK).list()[0]["role"])
}

func TestAnAdminIsNotTheOwnerASpaceHasToKeep(t *testing.T) {
	// Administering a space is not being able to pass it on: an admin left
	// alone could never make another owner, so the space could never be
	// shared or recovered again.
	c := newClient(t)
	owner := makeUser(t, testPassword)
	admin := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	joinSpace(t, space, admin, store.RoleAdmin, true)

	mine := ownMembership(t, c.as(owner), space, owner)
	c.patch("/spaces/"+space.ID.String()+"/members/"+mine, map[string]any{"role": "member"}).
		requireStatus(http.StatusConflict)
	c.del("/spaces/" + space.ID.String() + "/members/" + mine).
		requireStatus(http.StatusConflict)
}

// The primary currency.
//
// The one preference that is not a display choice: it is the currency every
// other figure on every screen is reported in.

func TestChangingThePrimaryCurrencyRestampsWhatWasAlreadyConverted(t *testing.T) {
	// The stamping pass only ever fills a null, so a converted row would keep
	// a figure made against the currency that has just stopped being primary —
	// dollars on a page that now says euros, with nothing to say so.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	id := seedTxn(l, "foreign", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 5),
		Amount: domain.MustFromString("-90.00"), Currency: "EUR",
		StatementName: "BERLIN CAFE", Payee: "Berlin Cafe",
	})
	require.NoError(t, l.env.DB.SetTransactionConversion(t.Context(), space, id,
		domain.MustFromString("-100.00"), decimal.RequireFromString("1.1111")))

	before := l.alex.get("/transactions/" + id.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, "-100.00", before["amount_primary"])

	l.alex.patch("/spaces/current/preferences", map[string]any{"primary_currency": "eur"}).
		requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + id.String()).requireStatus(http.StatusOK).json()
	require.Nil(t, after["amount_primary"],
		"the stale conversion survived the currency change")
	require.Nil(t, after["fx_rate_used"])

	current := l.alex.get("/spaces/current").requireStatus(http.StatusOK).json()
	require.Equal(t, "EUR", current["primary_currency"], "stored upper-cased")
}

func TestSettingTheSameCurrencyLeavesTheConversionsAlone(t *testing.T) {
	// Restamping on every preferences save would clear the whole ledger's
	// conversions whenever somebody changed their default date range.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	id := seedTxn(l, "foreign", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 5),
		Amount: domain.MustFromString("-90.00"), Currency: "EUR",
		StatementName: "BERLIN CAFE", Payee: "Berlin Cafe",
	})
	require.NoError(t, l.env.DB.SetTransactionConversion(t.Context(), space, id,
		domain.MustFromString("-100.00"), decimal.RequireFromString("1.1111")))

	l.alex.patch("/spaces/current/preferences",
		map[string]any{"primary_currency": "USD", "default_date_range": "3M"}).
		requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + id.String()).requireStatus(http.StatusOK).json()
	require.Equal(t, "-100.00", after["amount_primary"])
}

func TestACurrencyThatIsNotACodeIsRefused(t *testing.T) {
	l := buildLedger(t)
	for _, bad := range []string{"dollars", "US", ""} {
		l.alex.patch("/spaces/current/preferences", map[string]any{"primary_currency": bad}).
			requireStatus(http.StatusUnprocessableEntity)
	}
}

func TestACurrencyTheDeploymentQuotesNoRatesForIsRefused(t *testing.T) {
	// Not a display choice: the rate table would never hold a quote into it,
	// so every foreign row would stay unconverted and be counted at face
	// value with nothing on screen to say so.
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)
	c := currencyClient(t, "USD", "EUR").as(user).inSpace(space.ID)

	c.patch("/spaces/current/preferences", map[string]any{"primary_currency": "ZWL"}).
		requireStatus(http.StatusUnprocessableEntity)
	c.patch("/spaces/current/preferences", map[string]any{"primary_currency": "eur"}).
		requireStatus(http.StatusOK)
	require.Equal(t, "EUR",
		c.get("/spaces/current").requireStatus(http.StatusOK).json()["primary_currency"])
}

// currencyClient is a deployment that quotes rates for a named list, which is
// what the reporting currency has to be one of.
func currencyClient(t *testing.T, supported ...string) *client {
	t.Helper()
	cfg := testConfig()
	cfg.SupportedCurrencies = supported
	env := NewEnv(cfg, db(t))
	return &client{t: t, env: env, handler: RouterFor(env)}
}

func TestAViewerCannotChangeTheCurrencyEveryFigureIsReportedIn(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").patch("/spaces/current/preferences",
		map[string]any{"primary_currency": "EUR"}).requireStatus(http.StatusForbidden)
}

// A person's own display name.

func TestSomebodyCanChangeTheirOwnDisplayName(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.patch("/auth/me", map[string]any{"full_name": "  Alex L  "}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "Alex L", body["full_name"], "trimmed")

	// And cleared back to nothing, which reads as the email address again
	// rather than as an empty header.
	cleared := l.alex.patch("/auth/me", map[string]any{"full_name": nil}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["full_name"])
	require.Equal(t, l.alex.get("/auth/me").requireStatus(http.StatusOK).json()["email"],
		body["email"], "the login is not editable here")
}

func TestAViewerStillOwnsTheirOwnName(t *testing.T) {
	// It is theirs, not the space's: a read-only member is read-only about
	// the household's money, not about themselves.
	l := buildLedger(t)
	l.as("vera").patch("/auth/me", map[string]any{"full_name": "Vera"}).
		requireStatus(http.StatusOK)
}

// The dashboard arrangement.
//
// One person's arrangement of one space, which is neither a user preference
// nor a space one. It lives in the browser, so it does not follow anybody to
// a second device, and clearing the site data loses it.

func TestADashboardArrangementIsKeptPerPersonPerSpace(t *testing.T) {
	l := buildLedger(t)

	// Nothing stored reads as null, which the client turns into its own
	// built-in order rather than an empty dashboard.
	empty := l.alex.get("/spaces/current/dashboard").requireStatus(http.StatusOK).json()
	require.Nil(t, empty["layout"])

	saved := l.alex.put("/spaces/current/dashboard", map[string]any{
		"layout": []any{
			map[string]any{"id": "net_worth", "on": true},
			map[string]any{"id": "spending", "on": false},
		},
	}).requireStatus(http.StatusOK).json()
	require.Len(t, saved["layout"].([]any), 2)

	read := l.alex.get("/spaces/current/dashboard").requireStatus(http.StatusOK).json()
	require.Equal(t, saved["layout"], read["layout"])

	// Vera shares the space and has her own. Two people sharing a household
	// each arrange their own dashboard.
	require.Nil(t, l.as("vera").get("/spaces/current/dashboard").
		requireStatus(http.StatusOK).json()["layout"])
}

func TestAViewerArrangesTheirOwnDashboard(t *testing.T) {
	// Not a change to the household's money, so not a write in the sense the
	// role is about.
	l := buildLedger(t)
	l.as("vera").put("/spaces/current/dashboard",
		map[string]any{"layout": []any{map[string]any{"id": "net_worth", "on": true}}}).
		requireStatus(http.StatusOK)
}

func TestADashboardArrangementIsClearedBackToTheDefault(t *testing.T) {
	l := buildLedger(t)
	l.alex.put("/spaces/current/dashboard",
		map[string]any{"layout": []any{map[string]any{"id": "net_worth", "on": true}}}).
		requireStatus(http.StatusOK)

	cleared := l.alex.put("/spaces/current/dashboard", map[string]any{"layout": nil}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["layout"])
}

func TestSomethingThatIsNotALayoutIsRefused(t *testing.T) {
	// The widgets themselves are the client's question — a server that
	// validated the list would need redeploying to add a widget — but a layout
	// that is not a list is not a layout at all.
	l := buildLedger(t)
	l.alex.put("/spaces/current/dashboard", map[string]any{"layout": "net_worth"}).
		requireStatus(http.StatusUnprocessableEntity)
}
