package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The route listing says which routes the assistant's dispatch refuses, so the
// MCP server can refuse the same ones rather than keep a copy that drifts.
func TestTheRouteListingMarksWhatTheDispatchRefuses(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	routes := l.as("alex").get("/routes").requireStatus(http.StatusOK).list()

	denied := map[string]bool{}
	for _, one := range routes {
		route := one
		denied[route["method"].(string)+" "+route["path"].(string)] = route["denied"].(bool)
	}
	require.True(t, denied["POST /bills/connections/{connection_id}/sign-in"])
	require.True(t, denied["DELETE /bills/connections/{connection_id}/credential"])
	require.True(t, denied["GET /assistant/conversations"])
	require.False(t, denied["GET /accounts"])
	require.False(t, denied["GET /routes"])
}

func TestTheRouteListingDeniesEveryIdentityRoute(t *testing.T) {
	t.Parallel()
	// The dispatch mounts only Read and Write routes, so the in-app assistant
	// cannot reach a user or public route at all; the listing says the same,
	// so the MCP server cannot mint a token or change a membership either.
	l := buildLedger(t)
	routes := l.as("alex").get("/routes").requireStatus(http.StatusOK).list()
	var identity int
	for _, route := range routes {
		if route["scope"] == "space" {
			continue
		}
		identity++
		require.Equal(t, true, route["denied"], "%s %s", route["method"], route["path"])
	}
	require.NotZero(t, identity)
	// Named outright too, so the walk cannot pass by finding nothing to check.
	paths := map[string]bool{}
	for _, route := range routes {
		if route["denied"] == true {
			paths[route["method"].(string)+" "+route["path"].(string)] = true
		}
	}
	for _, want := range []string{
		"POST /auth/personal-tokens", "POST /auth/password", "POST /auth/totp/disable",
		"POST /auth/sessions/revoke-all", "POST /spaces/{space_id}/delete",
		"POST /spaces/{space_id}/members",
	} {
		require.True(t, paths[want], want)
	}
}
