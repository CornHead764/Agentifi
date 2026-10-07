package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Changing a password, and the state an account is in before it has.
//
// Two properties carry the weight. An account still holding a password
// somebody else chose can reach nothing but the three endpoints it needs to
// replace it — asserted over the whole route registry, not over a list of
// endpoints somebody remembered to add. And changing a password ends every
// session, which has to be true of tokens this process never issued, because
// the process that issued them may no longer be running.

const newPassword = "a much longer replacement"

func setNewPassword(c *client, current, next string) *response {
	return c.post("/auth/password", map[string]string{
		"current_password": current,
		"new_password":     next,
	})
}

func TestAChangedPasswordIsTheOneThatWorks(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)

	body := setNewPassword(c.as(user), testPassword, newPassword).
		requireStatus(http.StatusOK).json()
	require.NotEmpty(t, body["access_token"])

	login(newClient(t), user.Email, testPassword).requireStatus(http.StatusUnauthorized)
	login(newClient(t), user.Email, newPassword).requireStatus(http.StatusOK)
}

func TestTheSessionThatChangedThePasswordKeepsWorking(t *testing.T) {
	// The cutoff ends every session including this one, so the response has to
	// hand back a token minted after it — otherwise changing your password
	// signs you out of the tab you did it in.
	c := newClient(t)
	user := makeUser(t, testPassword)

	body := setNewPassword(c.as(user), testPassword, newPassword).
		requireStatus(http.StatusOK).json()

	c.get("/auth/me").requireStatus(http.StatusUnauthorized)
	c.token = body["access_token"].(string)
	c.get("/auth/me").requireStatus(http.StatusOK)
}

func TestAChangedPasswordSignsOutTheOtherSessions(t *testing.T) {
	// The other session's token is valid, unrevoked and unexpired. Only the
	// cutoff stops it, which is the point: revocation is per-process memory
	// and would not survive the restart this has to work across.
	user := makeUser(t, testPassword)
	elsewhere := newClient(t).as(user)
	elsewhere.get("/auth/me").requireStatus(http.StatusOK)

	setNewPassword(newClient(t).as(user), testPassword, newPassword).
		requireStatus(http.StatusOK)

	elsewhere.get("/auth/me").requireStatus(http.StatusUnauthorized)
}

func TestAWrongCurrentPasswordDoesNotEndTheSession(t *testing.T) {
	// A 401 here would be read by the client as "signed out" and throw the
	// user back to the login screen over a typo.
	c := newClient(t)
	user := makeUser(t, testPassword)

	setNewPassword(c.as(user), "not it", newPassword).requireStatus(http.StatusBadRequest)
	c.get("/auth/me").requireStatus(http.StatusOK)
	login(newClient(t), user.Email, testPassword).requireStatus(http.StatusOK)
}

func TestAPasswordlessAccountCanSetOneWithoutProvingAnything(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, "")

	setNewPassword(c.as(user), "", newPassword).requireStatus(http.StatusOK)
	login(newClient(t), user.Email, newPassword).requireStatus(http.StatusOK)
}

func TestAShortPasswordIsRefused(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)

	setNewPassword(c.as(user), testPassword, "short").requireStatus(http.StatusBadRequest)
	login(newClient(t), user.Email, testPassword).requireStatus(http.StatusOK)
}

func TestTheNewPasswordCannotBeTheOldOne(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	setNewPassword(c.as(user), testPassword, testPassword).requireStatus(http.StatusBadRequest)
}

func mustChangeUser(t *testing.T) store.User {
	t.Helper()
	return makeUser(t, testPassword, func(u *store.User) { u.MustChangePassword = true })
}

func TestAnAccountOwingAPasswordChangeCanReachNothingElse(t *testing.T) {
	// Walked over the registry rather than a handwritten list: the property is
	// about routes that do not exist yet as much as the ones that do.
	c := newClient(t)
	user := mustChangeUser(t)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)
	c.as(user).inSpace(space.ID)

	for _, route := range RegisteredRoutes() {
		if route.kind == kindPublic || passwordChangeExempt(route) {
			continue
		}
		if hasPathParameter(route) {
			continue
		}
		got := c.do(route.Method, route.Path(), nil)
		require.Equal(t, http.StatusForbidden, got.Code,
			"%s %s should be refused until the password is the user's own", route.Method, route.Path())
		require.Equal(t, "password_change_required", got.json()["code"])
	}
}

func TestTheThreeExemptRoutesAreReachable(t *testing.T) {
	c := newClient(t)
	user := mustChangeUser(t)

	me := c.as(user).get("/auth/me").requireStatus(http.StatusOK).json()
	require.Equal(t, true, me["must_change_password"])

	setNewPassword(c, testPassword, newPassword).requireStatus(http.StatusOK)
}

func TestChangingThePasswordClearsTheObligation(t *testing.T) {
	c := newClient(t)
	user := mustChangeUser(t)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)

	body := setNewPassword(c.as(user), testPassword, newPassword).
		requireStatus(http.StatusOK).json()
	c.token = body["access_token"].(string)
	c.inSpace(space.ID)

	c.get("/accounts").requireStatus(http.StatusOK)
	require.Equal(t, false, c.get("/auth/me").json()["must_change_password"])
}

func TestLoginStillSucceedsWhileAPasswordChangeIsOwed(t *testing.T) {
	// The obligation is not a login failure. A 401 at the door would leave the
	// user with no way to satisfy it — the token is what authorizes the change.
	user := mustChangeUser(t)
	body := login(newClient(t), user.Email, testPassword).
		requireStatus(http.StatusOK).json()
	require.NotEmpty(t, body["access_token"])
}

func TestAPasswordSetByAnOperatorIsNotSilentlyAccepted(t *testing.T) {
	// The store round-trips the flag: a column that reads back false would
	// make every test above pass against an account that owes nothing.
	user := mustChangeUser(t)
	stored, err := db(t).GetUser(t.Context(), user.ID)
	require.NoError(t, err)
	require.True(t, stored.MustChangePassword)
	require.Nil(t, stored.SessionsValidFrom)
}

func TestThePolicyRefusesWhatItSays(t *testing.T) {
	require.ErrorIs(t, auth.ValidatePassword("123456789"), auth.ErrPasswordTooShort)
	require.NoError(t, auth.ValidatePassword("1234567890"))
	require.ErrorIs(t, auth.ValidatePassword(string(make([]byte, auth.MaxPasswordLength+1))),
		auth.ErrPasswordTooLong)
}

// hasPathParameter skips the routes whose path is a template: they need a real
// id to reach their handler, and this test is about the gate that runs before
// any handler does.
func hasPathParameter(route Route) bool {
	for _, c := range route.Path() {
		if c == '{' {
			return true
		}
	}
	return false
}
