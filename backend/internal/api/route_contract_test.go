package api

import (
	"net/http"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Tenancy, checked at the wiring rather than at the query.
//
// Every ledger route resolves a space before its handler runs, and that is
// what puts a space id in scope for the store call beneath it. A route that
// resolves neither has no tenant, and no amount of care inside a handler can
// recover one. That is structural, so it is checked structurally — including
// for routes added after this was written.
//
// The registry is walked rather than the chi tree, because the registry is
// what the router mounts from; the last test here is what keeps the two the
// same, so a route smuggled onto the chi router directly cannot hide.

var writeMethods = map[string]bool{
	http.MethodPost:   true,
	http.MethodPut:    true,
	http.MethodPatch:  true,
	http.MethodDelete: true,
}

func TestEveryRouteResolvesASpaceOrIsIdentity(t *testing.T) {
	for _, route := range RegisteredRoutes() {
		switch route.kind {
		case kindRead, kindWrite:
		case kindUser, kindPublic:
			require.True(t, identityPrefixes[route.Prefix],
				"%s %s resolves no space and is not an identity route",
				route.Method, route.Path())
		case kindSuperuser:
			require.True(t, adminPrefixes[route.Prefix],
				"%s %s resolves no space and is not an administration route",
				route.Method, route.Path())
		}
	}
}

// The administration routes are the second exception to the tenancy rule, and
// the one with a check the registry has to make rather than the query: they
// resolve a caller and no space, so what stands between them and an ordinary
// user is store.User.IsSuperuser and nothing else.

func TestEveryAdminRouteRefusesANonSuperuser(t *testing.T) {
	// Walked from the registry so a route added to admin.go next month is
	// covered by this without anybody adding a case. The 403 itself is
	// exercised over HTTP in admin_test.go; this is the structural half.
	for _, route := range RegisteredRoutes() {
		if !adminPrefixes[route.Prefix] {
			continue
		}
		require.Equal(t, kindSuperuser, route.kind,
			"%s %s administers the server without checking that its caller may",
			route.Method, route.Path())
	}
}

func TestOnlyTheAdminResourceMayRegisterSuperuserRoutes(t *testing.T) {
	require.Panics(t, func() {
		rt := &Routes{prefix: "/budgets"}
		rt.Superuser(http.MethodGet, "/", func(*Env, http.ResponseWriter, *http.Request, store.User) error {
			return nil
		})
	})
	require.Panics(t, func() {
		RegisterAdmin(Resource{Prefix: "/budgets", Routes: func(*Routes) {}})
	})
}

func TestTheAdminResourceCannotResolveASpace(t *testing.T) {
	// A space resolved here would silently be whichever one the administrator
	// happened to be looking at, on a route that is about every household.
	require.Panics(t, func() {
		rt := &Routes{prefix: "/admin", mode: modeAdmin}
		rt.Read(http.MethodGet, "/users", func(*Env, http.ResponseWriter, *http.Request, auth.SpaceContext) error {
			return nil
		})
	})
}

func TestTheAdminRoutesAreNotReachableInProcess(t *testing.T) {
	// The assistant reaches the application through dispatch.go. A model that
	// could reach this resource could make itself an account.
	for _, route := range dispatchableRoutes() {
		require.False(t, adminPrefixes[route.Prefix],
			"%s %s is reachable from an in-process call", route.Method, route.Path())
	}
	require.Error(t, refuseDeniedPath("/admin/users"))
}

// personalWrites are the mutating routes that touch only the caller's own
// rows — reading or clearing their own bell — and so are mounted as Read: a viewer may
// not change the household's money, and these change none of it. Named here
// rather than inferred, so a new one is a deliberate edit that shows up in
// review beside the rule it is excepted from.
var personalWrites = map[string]bool{
	"POST /notifications/read":                   true,
	"POST /notifications/{notification_id}/read": true,
	"DELETE /notifications":                      true,
	"DELETE /notifications/{notification_id}":    true,
}

func TestEveryWriteRouteRequiresAWritableSpace(t *testing.T) {
	for _, route := range RegisteredRoutes() {
		if identityPrefixes[route.Prefix] || adminPrefixes[route.Prefix] {
			continue
		}
		if !writeMethods[route.Method] {
			continue
		}
		if personalWrites[route.Method+" "+route.Path()] {
			continue
		}
		require.Equal(t, kindWrite, route.kind,
			"%s %s writes from a space it never checked write access on",
			route.Method, route.Path())
	}
}

func TestReadRoutesDoNotDemandWriteAccess(t *testing.T) {
	// A viewer can read the register. Requiring write access to list would
	// lock them out of the application.
	for _, route := range RegisteredRoutes() {
		if writeMethods[route.Method] {
			continue
		}
		require.NotEqual(t, kindWrite, route.kind,
			"GET %s refuses viewers", route.Path())
	}
}

func TestTheLedgerResourcesAreAllRegistered(t *testing.T) {
	// Named individually so that deleting a resource's file is a failing test
	// rather than a silently smaller API.
	for _, prefix := range []string{
		"/accounts", "/categories", "/tags", "/filters", "/transactions", "/auth", "/spaces",
		"/admin",
	} {
		require.Contains(t, registry, prefix, "%s is not registered", prefix)
	}
}

func TestARegisteredTenantResourceCannotServeWithoutASpace(t *testing.T) {
	// The panic is the mechanism: Read and Write are the only two ways to add
	// a tenant-scoped route, and a resource that reaches for Public gets told
	// at startup rather than serving somebody else's rows.
	require.Panics(t, func() {
		rt := &Routes{prefix: "/budgets"}
		rt.Public(http.MethodGet, "/", func(*Env, http.ResponseWriter, *http.Request) error {
			return nil
		})
	})
}

func TestOnlyTheIdentityPrefixesMayRegisterAsIdentity(t *testing.T) {
	require.Panics(t, func() {
		RegisterIdentity(Resource{Prefix: "/budgets", Routes: func(*Routes) {}})
	})
}

func TestTheMountedRouterMatchesTheRegistry(t *testing.T) {
	// Without this, the registry could describe one API and chi serve another.
	env := NewEnv(testConfig(), nil)
	router, ok := RouterFor(env).(*chi.Mux)
	require.True(t, ok)

	mounted := map[string]bool{}
	require.NoError(t, chi.Walk(router,
		func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
			mounted[method+" "+strings.TrimSuffix(route, "/")] = true
			return nil
		}))

	for _, route := range RegisteredRoutes() {
		key := route.Method + " " + strings.TrimSuffix(route.Path(), "/")
		require.True(t, mounted[key], "%s is registered but not mounted", key)
		delete(mounted, key)
	}
	require.Empty(t, mounted, "routes are mounted that the registry does not describe")
}
