package main

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/api"
	"github.com/CornHead764/agentifi/backend/internal/config"
)

// TestTheOuterRouterKeepsTheRealPeerAddress: chi's RealIP overwrites
// RemoteAddr from X-Real-IP, True-Client-IP or the first X-Forwarded-For entry
// for any client, which hands the login rate limiter a key the caller chooses.
// Resolving the client address is the /api stack's job, and it believes a
// forwarded header only from a configured trusted proxy.
func TestTheOuterRouterKeepsTheRealPeerAddress(t *testing.T) {
	router := newRouter(t)

	var seen string
	probe := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		seen = r.RemoteAddr
	})

	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	request.RemoteAddr = "192.0.2.10:44321"
	request.Header.Set("X-Real-IP", "203.0.113.7")
	request.Header.Set("X-Forwarded-For", "203.0.113.8")
	request.Header.Set("True-Client-IP", "203.0.113.9")
	chi.Chain(router.Middlewares()...).Handler(probe).
		ServeHTTP(httptest.NewRecorder(), request)

	require.Equal(t, "192.0.2.10:44321", seen,
		"the outer stack must not rewrite RemoteAddr from a client-supplied header")
}

func TestSchemaComplaint(t *testing.T) {
	require.ErrorContains(t, schemaComplaint(0, 7), "no schema")
	require.ErrorContains(t, schemaComplaint(6, 7), "agentifi migrate")
	require.NoError(t, schemaComplaint(7, 7))
	// A database ahead of the binary is a rollback, not a missing migration:
	// the older queries still name columns that exist.
	require.NoError(t, schemaComplaint(8, 7))
}

// TestSecurityHeadersAreOnEveryResponse: the SPA and the API are served by one
// mux, and a header set on only one of them protects only one of them. The
// token lives in localStorage, so the CSP is the compensating control — a
// response that arrives without it is the regression this pins.
func TestSecurityHeadersAreOnEveryResponse(t *testing.T) {
	router := newRouter(t)

	for _, path := range []string{"/", "/transactions", "/api/accounts"} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, path, nil))

		header := recorder.Header()
		require.Equal(t, "nosniff", header.Get("X-Content-Type-Options"), path)
		require.Equal(t, "DENY", header.Get("X-Frame-Options"), path)
		require.Equal(t, "strict-origin-when-cross-origin", header.Get("Referrer-Policy"), path)
		require.Contains(t, header.Get("Permissions-Policy"), "camera=()", path)

		csp := header.Get("Content-Security-Policy")
		require.Contains(t, csp, "frame-ancestors 'none'", path)
		// The built bundle has no inline script, so nothing may relax this one
		// without the dist being re-read.
		require.Contains(t, csp, "script-src 'self';", path)
		require.NotContains(t, csp, "script-src 'self' 'unsafe-inline'", path)
	}
}

// TestAnUnknownAPIPathIsAJSONNotFound: without this, the app is the outer
// router's not-found handler, and the API router inherits it — a client
// asking a server older than itself for a new endpoint would get index.html
// with a 200 and render it.
func TestAnUnknownAPIPathIsAJSONNotFound(t *testing.T) {
	router := newRouter(t)

	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/no-such-resource", nil))
	require.Equal(t, http.StatusNotFound, recorder.Code)
	require.Contains(t, recorder.Header().Get("Content-Type"), "application/json")

	recorder = httptest.NewRecorder()
	router.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/transactions", nil))
	// A test binary embeds no built frontend, so the app's own answer here is
	// "frontend not built"; what matters is that it is not the API's.
	require.NotContains(t, recorder.Header().Get("Content-Type"), "application/json", "a deep link is still the app")
}

// TestAProcedureIsServedUnderAPI: a Connect handler matches the whole path
// against its procedures, so the /api it is mounted under has to come off.
func TestAProcedureIsServedUnderAPI(t *testing.T) {
	router := newRouter(t)

	request := httptest.NewRequest(http.MethodPost, "/api/agentifi.v1.TagService/ListTags", strings.NewReader(`{}`))
	request.Header.Set("Content-Type", "application/json")
	recorder := httptest.NewRecorder()
	router.ServeHTTP(recorder, request)
	require.Equal(t, http.StatusUnauthorized, recorder.Code)
	require.Contains(t, recorder.Body.String(), `"code":"unauthenticated"`)
}

// TestHealthcheckURLIsDialable: the image sets HTTP_ADDR=:8000 and a wildcard
// host is not an address a probe inside the container can connect to.
func TestHealthcheckURLIsDialable(t *testing.T) {
	require.Equal(t, "http://127.0.0.1:8000/health", healthcheckURL(":8000"))
	require.Equal(t, "http://127.0.0.1:8000/health", healthcheckURL("0.0.0.0:8000"))
	require.Equal(t, "http://127.0.0.1:9000/health", healthcheckURL("[::]:9000"))
	require.Equal(t, "http://10.0.0.5:8000/health", healthcheckURL("10.0.0.5:8000"))
}

func newRouter(t *testing.T) *chi.Mux {
	t.Helper()
	cfg := &config.Config{}
	return newRouterWith(cfg, nil, api.NewEnv(cfg, nil))
}
