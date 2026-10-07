package browser

import (
	"net/http"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// The argument a page is handed, and the checker that says it is safe to hand
// over. Nothing here needs Chromium.

func TestTheCheckerRefusesTheShapesTheSerializerCannotTake(t *testing.T) {
	require.NoError(t, Serializable(nil))
	require.NoError(t, Serializable("a string"))
	require.NoError(t, Serializable(map[string]any{
		"n": 3, "ok": true, "list": []any{"one", nil, 2.5},
		"nested": map[string]any{"deeper": "still fine"},
	}))

	// The one that panicked: a map that is not map[string]any, nested inside
	// an argument that is.
	refused := Serializable(map[string]any{"headers": map[string]string{"Accept": "application/json"}})
	require.ErrorContains(t, refused, "the argument.headers")
	require.ErrorContains(t, refused, "map[string]any")

	require.ErrorContains(t, Serializable([]StoredEntry{{Name: "a", Value: "b"}}), "a page takes []any")
	require.ErrorContains(t, Serializable(map[string]any{"item": StoredEntry{}}), "undefined")
	require.ErrorContains(t, Serializable(map[string]any{"list": []string{"one"}}), "a page takes []any")
	require.ErrorContains(t, Serializable([]any{map[string]int{"n": 1}}), "the argument[0]")
}

func TestAPageShapesWhateverItIsHanded(t *testing.T) {
	var seen any
	page := &StubPage{OnEvaluate: func(_ string, arg any) (any, error) { seen = arg; return nil, nil }}

	_, err := page.Evaluate("() => null", map[string]string{"Accept": "application/json"})

	require.NoError(t, err)
	require.Equal(t, map[string]any{"Accept": "application/json"}, seen,
		"a test reads the shape the provider's JavaScript would have been given")
	require.NoError(t, Serializable(seen))
	// What the caller built is kept as it was, which is what the checker is
	// asserted against.
	require.Len(t, page.Args, 1)
	require.IsType(t, map[string]string{}, page.Args[0])
}

func TestEveryCallTheTransportMakesIsHandedSerializableShapes(t *testing.T) {
	page := &StubPage{
		OnEvaluate: func(string, any) (any, error) {
			return pageAnswer(200, "application/json", `{"ok":true}`), nil
		},
	}
	fetcher := &PageFetcher{
		Origin: "https://portal.example.test",
		Open:   func() (FetchSurface, error) { return FetchSurface{Page: page}, nil },
	}

	req, err := http.NewRequest(http.MethodPost, "https://service.example.test/bill/Current",
		strings.NewReader(`{"accountNumbers":["1234"]}`))
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer invented")
	_, err = fetcher.Do(req)
	require.NoError(t, err)

	require.Len(t, page.Args, 1)
	require.NoError(t, Serializable(page.Args[0]))
}
