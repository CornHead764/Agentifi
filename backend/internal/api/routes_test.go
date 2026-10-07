package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The route listing says which routes the assistant's dispatch refuses, so the
// MCP server can refuse the same ones rather than keep a copy that drifts.
func TestTheRouteListingMarksWhatTheDispatchRefuses(t *testing.T) {
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
