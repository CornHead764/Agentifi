package merchants

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// What the Costco module asks the page for, and what it makes of the answer.
// Every token here is invented.

func TestADirectQueryCarriesThePagesTokenBothWaysTheEndpointReadsIt(t *testing.T) {
	var asked map[string]any
	page := &browser.StubPage{OnEvaluate: func(_ string, arg any) (any, error) {
		asked = arg.(map[string]any)
		return map[string]any{"status": float64(200), "json": map[string]any{"data": map[string]any{}}}, nil
	}}

	answer, err := queryFromPage(t.Context(), page, "query receipts { x }", map[string]any{"pageNumber": 1})
	require.NoError(t, err)
	require.True(t, answer.OK)

	require.Equal(t, costcoGraphQL, asked["url"])
	require.Equal(t, "POST", asked["method"])
	require.Equal(t, "include", asked["credentials"])
	token := asked["token"].(map[string]any)
	require.ElementsMatch(t, []any{"authorization", "costco-x-authorization"}, token["headers"])
	require.Equal(t, "Bearer ", token["scheme"])
	require.Equal(t, costcoClientIdentifier, asked["headers"].(map[string]any)["client-identifier"])
	require.Equal(t, "query receipts { x }", asked["body"].(map[string]any)["query"])
}

func TestADirectQueryWithNoTokenInThePageSaysSoAndNamesTheKeys(t *testing.T) {
	page := &browser.StubPage{OnEvaluate: func(string, any) (any, error) {
		return map[string]any{"status": float64(0), "no_token": true, "keys": []any{"localStorage:theme"}}, nil
	}}

	answer, err := queryFromPage(t.Context(), page, "query x", nil)
	require.NoError(t, err)
	require.False(t, answer.OK)
	require.Equal(t, "no id token in browser storage", answer.Error)
	require.Equal(t, []string{"localStorage:theme"}, answer.Keys)
}

func TestADirectQueryRefusedWithAPageIsNotOK(t *testing.T) {
	page := &browser.StubPage{OnEvaluate: func(string, any) (any, error) {
		return map[string]any{"status": float64(403), "excerpt": "<html>Access Denied</html>"}, nil
	}}

	answer, err := queryFromPage(t.Context(), page, "query x", nil)
	require.NoError(t, err)
	require.False(t, answer.OK)
	require.Equal(t, 403, answer.Status)
	require.Contains(t, answer.Excerpt, "Access Denied")
}

func TestMSALsEntriesAreSortedByTheirKeys(t *testing.T) {
	cache := costcoMSALCache([]browser.StorageEntry{
		{Store: "localStorage", Key: "uid.tenant-signin.costco.com-refreshtoken-client--", Value: "refresh"},
		{Store: "localStorage", Key: "uid.tenant-signin.costco.com-idtoken-client-tenant-", Value: "id"},
		{Store: "localStorage", Key: "uid.tenant-signin.costco.com-", Value: "account"},
		{Store: "localStorage", Key: "idToken", Value: "plain-id"},
		{Store: "localStorage", Key: "azure_token", Value: "azure"},
	})
	require.Equal(t, CostcoMSALCache{
		Refresh: "refresh", ID: "id", Account: "account", IDToken: "plain-id", AzureToken: "azure",
	}, cache)
}

func TestAPageWithNoMSALCacheHandsOverNoSession(t *testing.T) {
	page := &browser.StubPage{OnEvaluate: func(_ string, arg any) (any, error) {
		require.Equal(t, costcoMSALKeys, arg.(map[string]any)["pattern"])
		return map[string]any{"found": []any{}, "keys": []any{}}, nil
	}}
	_, found, err := (&costcoModule{}).SessionFromPage(page, time.Now())
	require.NoError(t, err)
	require.False(t, found)
}
