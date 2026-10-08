package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The administration screen, which is the only part of the API that answers
// about every household on the install at once.
//
// Two properties carry most of these tests. The first is that the refusal is
// structural: it comes from the registry rather than from a handler, so the
// test that matters is that *every* route under /admin gives an ordinary
// account a 403 — including the ones added after this was written, which is
// what route_contract_test.go asserts alongside. The second is that the
// install can never be left without an administrator, because the repair for
// that is a database session on the production host and this screen exists for
// the person who does not have one.

func makeAdmin(t *testing.T) store.User {
	t.Helper()
	return makeUser(t, testPassword, func(user *store.User) { user.IsSuperuser = true })
}

// soloAdmin makes an administrator and stands every other one down.
//
// The package shares one schema, so an administrator another test left behind
// would satisfy the very count the last-administrator guard is checking, and
// the test would pass for the wrong reason on its own and fail when run beside
// something else.
func soloAdmin(t *testing.T) store.User {
	t.Helper()
	users, err := db(t).ListUsers(t.Context())
	require.NoError(t, err)
	for _, user := range users {
		if user.IsSuperuser && user.IsActive {
			require.NoError(t, db(t).SetUserActive(t.Context(), user.ID, false))
		}
	}
	return makeAdmin(t)
}

func TestEveryAdministrationRouteRefusesAnOrdinaryAccount(t *testing.T) {
	c := newClient(t).as(makeUser(t, testPassword))
	for _, route := range RegisteredRoutes() {
		if !adminPrefixes[route.Prefix] {
			continue
		}
		// The placeholders never have to resolve: the refusal happens before
		// the handler that would read them.
		path := strings.NewReplacer(
			"{user_id}", "00000000-0000-0000-0000-000000000000",
			"{membership_id}", "00000000-0000-0000-0000-000000000000",
		).Replace(route.Path())
		c.do(route.Method, path, map[string]any{}).
			requireStatus(http.StatusForbidden)
	}
}

func TestTheUserListingCarriesSignInMethodsAndSpaces(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	person := makeUser(t, testPassword)
	space := makeSpace(t, person, "Household", store.RoleOwner, true)
	invited := makeSpace(t, person, "Cabin", store.RoleMember, false)

	listed := c.get("/admin/users").requireStatus(http.StatusOK).list()
	var row map[string]any
	for _, one := range listed {
		if one["email"] == person.Email {
			row = one
		}
	}
	require.NotNil(t, row, "the account is missing from the listing")

	require.Equal(t, true, row["has_password"])
	require.Equal(t, false, row["has_totp"])
	require.Equal(t, false, row["has_oidc"])
	require.Equal(t, float64(0), row["passkey_count"])
	require.Equal(t, false, row["is_superuser"])

	memberships, ok := row["memberships"].([]any)
	require.True(t, ok)
	places := map[string]map[string]any{}
	for _, one := range memberships {
		membership := one.(map[string]any)
		places[membership["space_id"].(string)] = membership
	}
	require.Equal(t, "Household", places[space.ID.String()]["space_name"])
	require.Equal(t, "owner", places[space.ID.String()]["role"])
	require.Equal(t, true, places[space.ID.String()]["accepted"])
	// An invitation is listed as what it is, because an administrator looking
	// at "who can see this space" needs the two kept apart.
	require.Equal(t, false, places[invited.ID.String()]["accepted"])
}

func TestTheUserListingCarriesNoCredential(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	makeUser(t, testPassword, func(user *store.User) { user.TOTPSecret = "JBSWY3DPEHPK3PXP" })

	body := c.get("/admin/users").requireStatus(http.StatusOK).Body.String()
	require.NotContains(t, body, "hashed_password")
	require.NotContains(t, body, "totp_secret")
	require.NotContains(t, body, "JBSWY3DPEHPK3PXP")
	require.NotContains(t, body, "$2a$")
}

func TestCreatingAnAccountMakesItsSpaceAndMintsAPasswordOnce(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	email := "made-" + uuid.NewString() + "@example.test"

	body := c.post("/admin/users", map[string]any{
		"email": email, "full_name": "Made Person", "space_name": "Their House",
	}).requireStatus(http.StatusCreated).json()

	temporary, _ := body["temporary_password"].(string)
	require.NotEmpty(t, temporary, "an omitted password is minted and returned once")

	user := body["user"].(map[string]any)
	require.Equal(t, email, user["email"])
	require.Equal(t, true, user["must_change_password"])
	require.Equal(t, false, user["is_superuser"])
	require.Equal(t, true, user["has_password"])

	memberships := user["memberships"].([]any)
	require.Len(t, memberships, 1)
	membership := memberships[0].(map[string]any)
	require.Equal(t, "Their House", membership["space_name"])
	// Owner, accepted: an account whose only membership is an invitation
	// nobody can accept is an account that signs in and sees nothing.
	require.Equal(t, "owner", membership["role"])
	require.Equal(t, true, membership["accepted"])

	// The minted password is a password, not a token shaped like one.
	login(c, email, temporary).requireStatus(http.StatusOK)
}

func TestCreatingAnAccountIntoASpaceThatExists(t *testing.T) {
	admin := makeAdmin(t)
	c := newClient(t).as(admin)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Shared", store.RoleOwner, true)
	email := "joiner-" + uuid.NewString() + "@example.test"

	body := c.post("/admin/users", map[string]any{
		"email":    email,
		"password": "a long enough password",
		"space_id": space.ID.String(),
		"role":     "viewer",
	}).requireStatus(http.StatusCreated).json()

	// Nothing is minted when the caller chose the password, so there is
	// nothing to leak into a log or a screenshot.
	require.Empty(t, body["temporary_password"])

	memberships := body["user"].(map[string]any)["memberships"].([]any)
	require.Len(t, memberships, 1)
	membership := memberships[0].(map[string]any)
	require.Equal(t, space.ID.String(), membership["space_id"])
	require.Equal(t, "viewer", membership["role"])
	require.Equal(t, true, membership["accepted"])
}

func TestCreatingAnAccountNeedsSomewhereToPutIt(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	c.post("/admin/users", map[string]any{
		"email": "nowhere-" + uuid.NewString() + "@example.test",
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestAnAddressCanOnlyHaveOneAccount(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	existing := makeUser(t, testPassword)
	c.post("/admin/users", map[string]any{
		"email": existing.Email, "space_name": "Second",
	}).requireStatus(http.StatusConflict)
}

func TestAnAdministratorCannotTakeTheirOwnRightsAway(t *testing.T) {
	admin := makeAdmin(t)
	// A second administrator, so this is the self rule and not the count.
	makeAdmin(t)
	c := newClient(t).as(admin)

	c.patch("/admin/users/"+admin.ID.String(), map[string]any{"is_superuser": false}).
		requireStatus(http.StatusConflict)
	c.patch("/admin/users/"+admin.ID.String(), map[string]any{"is_active": false}).
		requireStatus(http.StatusConflict)

	// Everything else about their own account is still theirs to change.
	c.patch("/admin/users/"+admin.ID.String(), map[string]any{"full_name": "Renamed"}).
		requireStatus(http.StatusOK)
}

func TestTheInstallAlwaysKeepsAnAdministrator(t *testing.T) {
	admin := soloAdmin(t)
	second := makeAdmin(t)
	c := newClient(t).as(admin)

	// With two, either may stand the other down.
	c.patch("/admin/users/"+second.ID.String(), map[string]any{"is_superuser": false}).
		requireStatus(http.StatusOK)

	// With one, every route into that change is closed. It closes as the self
	// rule rather than as the count, and that is not a gap: reaching this route
	// needs an active administrator, so a caller who is somebody else is
	// themselves the administrator that would remain.
	c.patch("/admin/users/"+admin.ID.String(), map[string]any{"is_superuser": false}).
		requireStatus(http.StatusConflict)
	c.patch("/admin/users/"+admin.ID.String(), map[string]any{"is_active": false}).
		requireStatus(http.StatusConflict)

	// And the count the guard reads agrees about what standing them down would
	// leave behind.
	remaining, err := db(t).CountActiveSuperusers(t.Context(), admin.ID)
	require.NoError(t, err)
	require.Zero(t, remaining)
}

func TestSettingATemporaryPasswordEndsTheAccountsSessions(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	person := makeUser(t, testPassword)
	theirs := newClient(t).as(person)
	theirs.get("/auth/me").requireStatus(http.StatusOK)

	body := c.post("/admin/users/"+person.ID.String()+"/password", map[string]any{}).
		requireStatus(http.StatusOK).json()
	temporary, _ := body["temporary_password"].(string)
	require.NotEmpty(t, temporary)

	// The session held before the reset is the reason an operator resets a
	// password; it does not survive one.
	theirs.get("/auth/me").requireStatus(http.StatusUnauthorized)

	// The new one works, and the account owes a change before it can read
	// anything but itself.
	login(c, person.Email, temporary).requireStatus(http.StatusOK)
	after, err := db(t).GetUser(t.Context(), person.ID)
	require.NoError(t, err)
	require.True(t, after.MustChangePassword)
}

func TestAnAdministratorAddsAndRemovesSpaceMembership(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	owner := makeUser(t, testPassword)
	person := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)

	added := c.post("/admin/users/"+person.ID.String()+"/memberships", map[string]any{
		"space_id": space.ID.String(), "role": "member",
	}).requireStatus(http.StatusCreated).json()
	// Accepted, not invited: nobody is here to answer an invitation on behalf
	// of an account that has never signed in.
	require.Equal(t, true, added["accepted"])
	require.Equal(t, "Household", added["space_name"])

	c.post("/admin/users/"+person.ID.String()+"/memberships", map[string]any{
		"space_id": space.ID.String(), "role": "member",
	}).requireStatus(http.StatusConflict)

	c.del("/admin/users/" + person.ID.String() + "/memberships/" + added["id"].(string)).
		requireStatus(http.StatusNoContent)

	// The space's own owner is a different matter: removing the last one
	// leaves a space nobody can share or rename again.
	members, err := db(t).ListMemberships(t.Context(), space.ID)
	require.NoError(t, err)
	require.Len(t, members, 1)
	c.del("/admin/users/" + owner.ID.String() + "/memberships/" + members[0].ID.String()).
		requireStatus(http.StatusConflict)
}

func TestTheSpaceListingCarriesItsMembers(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Listed House", store.RoleOwner, true)

	var row map[string]any
	for _, one := range c.get("/admin/spaces").requireStatus(http.StatusOK).list() {
		if one["id"] == space.ID.String() {
			row = one
		}
	}
	require.NotNil(t, row)
	require.Equal(t, "Listed House", row["name"])
	require.Equal(t, "USD", row["primary_currency"])

	members := row["members"].([]any)
	require.Len(t, members, 1)
	member := members[0].(map[string]any)
	require.Equal(t, owner.Email, member["email"])
	require.Equal(t, "owner", member["role"])
	require.Equal(t, true, member["accepted"])
}

// --- Single sign-on -----------------------------------------------------------

// clearOIDC forgets whatever an earlier test in this package saved. The
// settings are the server's, not a space's, so they are the one thing here two
// tests can hand each other.
func clearOIDC(t *testing.T, env *Env) {
	t.Helper()
	_, err := env.DB.Pool().Exec(t.Context(),
		`DELETE FROM server_settings WHERE key IN (SELECT value FROM json_each($1))`, store.OIDCSettingKeys)
	require.NoError(t, err)
}

func TestTheProviderSettingsRoundTripWithoutEchoingTheSecret(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearOIDC(t, c.env)

	before := c.get("/admin/oidc").requireStatus(http.StatusOK).json()
	require.Equal(t, false, before["has_client_secret"])
	sources := before["sources"].(map[string]any)
	require.Equal(t, "environment", sources["discovery_url"])
	require.NotEmpty(t, before["callback_url"])

	saved := c.put("/admin/oidc", map[string]any{
		"enabled":                true,
		"provider_name":          "Authentik",
		"discovery_url":          "https://idp.example.test/.well-known/openid-configuration",
		"client_id":              "agentifi",
		"client_secret":          "a-real-secret",
		"scopes":                 []string{"openid", "email"},
		"auto_register":          true,
		"require_verified_email": false,
		"link_existing_email":    true,
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, true, saved["has_client_secret"])
	require.NotContains(t, c.get("/admin/oidc").Body.String(), "a-real-secret")

	read := c.get("/admin/oidc").requireStatus(http.StatusOK).json()
	require.Equal(t, "Authentik", read["provider_name"])
	require.Equal(t, "agentifi", read["client_id"])
	require.Equal(t, true, read["auto_register"])
	require.Equal(t, false, read["require_verified_email"])
	require.Equal(t, true, read["link_existing_email"])
	require.Equal(t, []any{"openid", "email"}, read["scopes"])
	require.Equal(t, true, read["has_client_secret"])
	require.Equal(t, true, read["configured"])

	// What was saved is now answered for by the database; what was not is
	// still the environment's, which is what a screen tells an administrator
	// before they overwrite a deployment's choice.
	after := read["sources"].(map[string]any)
	require.Equal(t, "database", after["discovery_url"])
	require.Equal(t, "database", after["client_id"])
	require.Equal(t, "database", after["client_secret"])

	clearOIDC(t, c.env)
}

func TestAnOmittedClientSecretKeepsTheStoredOne(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearOIDC(t, c.env)

	form := map[string]any{
		"enabled":       true,
		"provider_name": "Authentik",
		"discovery_url": "https://idp.example.test/.well-known/openid-configuration",
		"client_id":     "agentifi",
		"client_secret": "a-real-secret",
	}
	c.put("/admin/oidc", form).requireStatus(http.StatusOK)

	// The field is blank on every visit to the screen, because the stored
	// value never comes back to a browser. Treating blank as a clear would
	// erase the secret every time somebody edited the display name.
	form["client_secret"] = ""
	form["provider_name"] = "Renamed"
	saved := c.put("/admin/oidc", form).requireStatus(http.StatusOK).json()
	require.Equal(t, "Renamed", saved["provider_name"])
	require.Equal(t, true, saved["has_client_secret"])
	require.Equal(t, "a-real-secret", c.env.OIDC.Settings().ClientSecret)

	clearOIDC(t, c.env)
}

func TestSavingTheProviderReconfiguresTheRunningServer(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearOIDC(t, c.env)

	// Nothing is configured, so the login screen draws no provider button.
	before := c.get("/auth/oidc/config").requireStatus(http.StatusOK).json()
	require.Equal(t, false, before["enabled"])

	c.put("/admin/oidc", map[string]any{
		"enabled":       true,
		"provider_name": "Authentik",
		"discovery_url": "https://idp.example.test/.well-known/openid-configuration",
		"client_id":     "agentifi",
		"client_secret": "a-real-secret",
	}).requireStatus(http.StatusOK)

	// Live, without a restart: the administrator's next act is usually to open
	// a private window and press the button they have just turned on.
	after := c.get("/auth/oidc/config").requireStatus(http.StatusOK).json()
	require.Equal(t, true, after["enabled"])
	require.Equal(t, "Authentik", after["provider_name"])

	// And a restart keeps it, which is what reading the settings at startup is
	// for — a second environment over the same database sees the same thing.
	fresh := NewEnv(testConfig(), db(t))
	require.NoError(t, fresh.ReloadOIDC(t.Context()))
	require.True(t, fresh.OIDC.IsConfigured())
	require.Equal(t, "Authentik", fresh.OIDC.Settings().ProviderName)

	clearOIDC(t, c.env)
}

func TestTurningTheProviderOnNeedsSomewhereToSendPeople(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearOIDC(t, c.env)

	// Saved and left broken, this is a sign-in button that answers 404 to
	// everybody who presses it.
	c.put("/admin/oidc", map[string]any{"enabled": true, "provider_name": "Authentik"}).
		requireStatus(http.StatusUnprocessableEntity)
	c.put("/admin/oidc", map[string]any{
		"enabled": true, "discovery_url": "https://idp.example.test/.well-known/openid-configuration",
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestTestingAProviderReportsTheFailureRatherThanRaisingIt(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))

	// A 502 would reach the screen as a generic failure toast; the provider's
	// own complaint is the thing an administrator needs to read.
	body := c.post("/admin/oidc/test", map[string]any{
		"discovery_url": "http://127.0.0.1:1/.well-known/openid-configuration",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, false, body["valid"])
	require.NotEmpty(t, body["message"])
}
