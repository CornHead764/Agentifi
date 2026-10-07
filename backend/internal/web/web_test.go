package web

import (
	"mime"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/fstest"

	"github.com/stretchr/testify/require"
)

// The built application, as far as this package is concerned: an index and one
// asset beside it. Invented, and deliberately not the real build — see
// handlerOver.
var built = fstest.MapFS{
	"index.html":             {Data: []byte("<!doctype html><html><body>app</body></html>")},
	"assets/index-abc123.js": {Data: []byte("console.log('app')")},
	"manifest.webmanifest":   {Data: []byte(`{"name":"Agentifi"}`)},
}

func serve(t *testing.T, target string) *httptest.ResponseRecorder {
	t.Helper()
	rec := httptest.NewRecorder()
	handlerOver(built).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, target, nil))
	return rec
}

func TestTheRootServesTheApplication(t *testing.T) {
	require.Equal(t, http.StatusOK, serve(t, "/").Code)
}

// The router lives in the browser, so a deep link is a path the server has
// never heard of and must answer with the app rather than a 404. Without this
// every refresh on a sub-page breaks.
func TestADeepLinkFallsThroughToTheApplication(t *testing.T) {
	rec := serve(t, "/transactions/2026-08")
	require.Equal(t, http.StatusOK, rec.Code)
	require.Contains(t, rec.Body.String(), "<html")
}

// A browser that caches index.html keeps loading a bundle that points at asset
// filenames which no longer exist, and the app fails to start with no clue why.
func TestIndexIsNeverCached(t *testing.T) {
	require.Equal(t, "no-cache", serve(t, "/").Header().Get("Cache-Control"))
}

// Go ships no MIME type for .webmanifest, so the file server falls back to
// sniffing and DetectContentType reads JSON as text/plain. A manifest served
// as text is one a browser may ignore, which costs the installed app its name
// and its icon — and nothing about the page would look wrong, so this is the
// only place it would be noticed.
func TestTheManifestHasAManifestContentType(t *testing.T) {
	require.Equal(t, "application/manifest+json", mime.TypeByExtension(".webmanifest"))
}
