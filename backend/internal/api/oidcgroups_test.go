package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
)

func oidcPerson(subject string, groups ...string) auth.OIDCIdentity {
	return auth.OIDCIdentity{
		Issuer: "https://idp.example.test", Subject: subject,
		Email: subject + "@example.test", EmailVerified: true, Groups: groups,
	}
}

func TestAdminGroupsMakeTheSuperuserFlagFollowTheProvider(t *testing.T) {
	t.Parallel()
	c := emptyServer(t)
	c.env.OIDC.Configure(auth.OIDCSettings{
		Enabled: true, AutoRegister: true, AdminGroups: []string{"finance-admins"},
	})

	first, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("one", "finance-admins"))
	require.NoError(t, err)
	require.True(t, first.IsSuperuser)

	second, created, err := loginWithOIDC(t.Context(), c.env, oidcPerson("two", "family", "finance-admins"))
	require.NoError(t, err)
	require.True(t, created)
	require.True(t, second.IsSuperuser, "a member is promoted on arrival")

	second, _, err = loginWithOIDC(t.Context(), c.env, oidcPerson("two", "family"))
	require.NoError(t, err)
	require.False(t, second.IsSuperuser, "and demoted once the provider drops them")
	stored, err := c.env.DB.GetUserByOIDC(t.Context(), "https://idp.example.test", "two")
	require.NoError(t, err)
	require.False(t, stored.IsSuperuser)

	second, _, err = loginWithOIDC(t.Context(), c.env, oidcPerson("two", "finance-admins"))
	require.NoError(t, err)
	require.True(t, second.IsSuperuser)
}

func TestWithoutAdminGroupsASignInLeavesTheSuperuserFlagAlone(t *testing.T) {
	t.Parallel()
	c := emptyServer(t)
	c.env.OIDC.Configure(auth.OIDCSettings{Enabled: true, AutoRegister: true})

	first, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("one"))
	require.NoError(t, err)
	require.True(t, first.IsSuperuser, "the first account still administers the server")

	again, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("one", "family"))
	require.NoError(t, err)
	require.True(t, again.IsSuperuser)

	second, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("two", "finance-admins"))
	require.NoError(t, err)
	require.False(t, second.IsSuperuser)
}

func TestAdminGroupsNeverDemoteTheLastAdministrator(t *testing.T) {
	t.Parallel()
	c := emptyServer(t)
	c.env.OIDC.Configure(auth.OIDCSettings{Enabled: true, AutoRegister: true})
	first, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("one"))
	require.NoError(t, err)
	require.True(t, first.IsSuperuser)

	c.env.OIDC.Configure(auth.OIDCSettings{
		Enabled: true, AutoRegister: true, AdminGroups: []string{"finance-admins"},
	})
	again, _, err := loginWithOIDC(t.Context(), c.env, oidcPerson("one", "family"))
	require.NoError(t, err)
	require.True(t, again.IsSuperuser)
	stored, err := c.env.DB.GetUserByOIDC(t.Context(), "https://idp.example.test", "one")
	require.NoError(t, err)
	require.True(t, stored.IsSuperuser)
}

func TestTheGroupSettingsRoundTripThroughServerAdmin(t *testing.T) {
	t.Parallel()
	c := newClient(t).as(makeAdmin(t))
	clearOIDC(t, c.env)

	before := c.get("/admin/oidc").requireStatus(http.StatusOK).json()
	require.Equal(t, "groups", before["groups_claim"])
	require.Equal(t, []any{}, before["admin_groups"])

	c.put("/admin/oidc", map[string]any{
		"groups_claim": " roles ",
		"admin_groups": []string{" finance-admins ", "", "Household Owners"},
	}).requireStatus(http.StatusOK)

	read := c.get("/admin/oidc").requireStatus(http.StatusOK).json()
	require.Equal(t, "roles", read["groups_claim"])
	require.Equal(t, []any{"finance-admins", "Household Owners"}, read["admin_groups"])
	sources := read["sources"].(map[string]any)
	require.Equal(t, "database", sources["admin_groups"])
	require.Equal(t, []string{"finance-admins", "Household Owners"}, c.env.OIDC.Settings().AdminGroups)

	c.put("/admin/oidc", map[string]any{
		"admin_groups": []string{"a,b"},
	}).requireStatus(http.StatusUnprocessableEntity)
}
