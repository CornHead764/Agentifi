package browser

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAPageCallReadsJSONUnlessToldOtherwise(t *testing.T) {
	arg, err := PageCall{URL: "https://service.example.test/api", Method: "post"}.Argument(t.Context())
	require.NoError(t, err)
	require.NoError(t, Serializable(arg))
	require.Equal(t, "json", arg["read"])
	require.Equal(t, "POST", arg["method"])
	require.EqualValues(t, FetchTimeout.Milliseconds(), arg["timeoutMs"])
	require.NotContains(t, arg, "credentials", "the browser's default unless a caller asks")
	require.NotContains(t, arg, "token")
}

func TestAPageCallHandsTheTokenSpecToThePage(t *testing.T) {
	var asked map[string]any
	page := &StubPage{OnEvaluate: func(script string, arg any) (any, error) {
		require.Contains(t, script, "await fetch(arg.url")
		asked = arg.(map[string]any)
		return map[string]any{"status": float64(0), "no_token": true, "keys": []any{"sessionStorage:theme"}}, nil
	}}
	answer, err := CallFromPage(t.Context(), page, PageCall{
		URL: "https://api.example.test/x",
		Token: &StorageToken{Stores: []string{"sessionStorage"}, Key: "^auth:",
			Headers: []string{"authorization"}, Scheme: "Bearer "},
	})
	require.NoError(t, err)
	require.True(t, answer.NoToken)
	require.Equal(t, []string{"sessionStorage:theme"}, answer.Keys)
	token := asked["token"].(map[string]any)
	require.Equal(t, "^auth:", token["key"])
	require.Equal(t, []any{"authorization"}, token["headers"])
}

func TestAWaitOnAStorageTokenCarriesItsSpec(t *testing.T) {
	held := StorageTokenHeld(StorageToken{Stores: []string{"sessionStorage"}, Key: "^firebase:authUser:"})
	require.True(t, strings.HasPrefix(held, "Boolean("))
	require.Contains(t, held, `"key":"^firebase:authUser:"`)
}
