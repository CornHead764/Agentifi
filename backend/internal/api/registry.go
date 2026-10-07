package api

import (
	"fmt"
	"net/http"
	"sort"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// How a resource joins the API: one file that registers itself from an init
// function, mounted by Router without anybody editing a shared file.
//
//	func init() {
//		Register(Resource{Prefix: "/budgets", Routes: func(rt *Routes) {
//			rt.Read(http.MethodGet, "/", listBudgets)
//			rt.Write(http.MethodPatch, "/{budget_id}", updateBudget)
//		}})
//	}
//
// Read and Write are the only ways to add a tenant-scoped route, and both hand
// the handler a resolved space; Write refuses a viewer before the handler runs.
// There is deliberately no way to register a ledger route that resolves
// neither, because a route with no tenant serves some other household's rows.
//
// The two exceptions are named lists in this file: identity routes, which must
// work before a tenant exists, and Superuser routes, which answer about every
// household on the install at once.
//
// A handler returns error rather than writing a failure status; errors.go maps
// every refusal.

// SpaceHandler serves a request whose tenant is already resolved.
type SpaceHandler func(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error

// UserHandler serves a request that has a caller but no tenant. Identity only.
type UserHandler func(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error

// PublicHandler serves a request with neither. Identity only.
type PublicHandler func(env *Env, w http.ResponseWriter, r *http.Request) error

// kind is recorded per route so route_contract_test.go can assert the mounting
// property over routes that do not exist yet.
type kind int

const (
	kindRead kind = iota
	kindWrite
	kindUser
	kindPublic
	kindSuperuser
)

func (k kind) String() string {
	switch k {
	case kindRead:
		return "read"
	case kindWrite:
		return "write"
	case kindUser:
		return "user"
	case kindSuperuser:
		return "superuser"
	default:
		return "public"
	}
}

// Route is one mounted endpoint, as the registry records it.
type Route struct {
	Prefix  string
	Method  string
	Pattern string
	kind    kind
	space   SpaceHandler
	user    UserHandler
	public  PublicHandler
}

// Path is the route's full path under the API mount point, for diagnostics.
func (r Route) Path() string {
	if r.Pattern == "/" {
		return r.Prefix
	}
	return r.Prefix + r.Pattern
}

// mode is what a resource may register. A resource declares it once, by which
// Register function it calls, and every route it adds is held to it.
type mode int

const (
	modeTenant mode = iota
	modeIdentity
	modeAdmin
)

// Routes collects one resource's endpoints.
type Routes struct {
	prefix  string
	mode    mode
	entries []Route
}

// Read mounts a route that resolves the space and refuses nobody in it: a
// viewer must be able to read the register.
func (rt *Routes) Read(method, pattern string, h SpaceHandler) {
	rt.refuseSpace(method, pattern)
	rt.add(Route{Method: method, Pattern: pattern, kind: kindRead, space: h})
}

// Write mounts a route that resolves the space and refuses a viewer. The
// refusal lives here so it cannot be left out of one endpoint.
func (rt *Routes) Write(method, pattern string, h SpaceHandler) {
	rt.refuseSpace(method, pattern)
	rt.add(Route{Method: method, Pattern: pattern, kind: kindWrite, space: h})
}

// User mounts a route that has a caller and no tenant — reserved for identity.
func (rt *Routes) User(method, pattern string, h UserHandler) {
	rt.requireIdentity(method, pattern)
	rt.add(Route{Method: method, Pattern: pattern, kind: kindUser, user: h})
}

// Public mounts a route with no caller at all — reserved for identity.
func (rt *Routes) Public(method, pattern string, h PublicHandler) {
	rt.requireIdentity(method, pattern)
	rt.add(Route{Method: method, Pattern: pattern, kind: kindPublic, public: h})
}

// Superuser mounts a route that has a caller, resolves no tenant, and refuses
// anybody who does not administer this server. The check lives here so no
// administrative handler can skip it.
func (rt *Routes) Superuser(method, pattern string, h UserHandler) {
	if rt.mode != modeAdmin {
		panic(fmt.Sprintf(
			"api: %s %s%s may not be a superuser route; only the administration "+
				"resource serves whoever runs the server", method, rt.prefix, pattern))
	}
	rt.add(Route{Method: method, Pattern: pattern, kind: kindSuperuser, user: h})
}

func (rt *Routes) requireIdentity(method, pattern string) {
	if rt.mode != modeIdentity {
		panic(fmt.Sprintf(
			"api: %s %s%s is tenant-scoped and must use Read or Write; only the identity "+
				"resources may serve a request with no space", method, rt.prefix, pattern))
	}
}

// refuseSpace keeps the administration resource off Read and Write: a resolved
// space would silently be whichever the administrator happened to be viewing.
func (rt *Routes) refuseSpace(method, pattern string) {
	if rt.mode == modeAdmin {
		panic(fmt.Sprintf(
			"api: %s %s%s administers the server and has no space to resolve; use Superuser",
			method, rt.prefix, pattern))
	}
}

func (rt *Routes) add(route Route) {
	route.Prefix = rt.prefix
	rt.entries = append(rt.entries, route)
}

// Resource is one mountable slice of the API.
type Resource struct {
	// Prefix is the resource root, with a leading slash and no trailing one:
	// "/accounts". Patterns are relative to it, "/" for the collection.
	Prefix string
	Routes func(*Routes)
}

type resource struct {
	Prefix string
	Routes []Route
}

// identityPrefixes is the closed list of resources allowed to serve a request
// that resolves no space. A new entry is a deliberate edit that shows up in
// review.
var identityPrefixes = map[string]bool{"/auth": true, "/spaces": true}

// adminPrefixes is the closed list of resources that serve whoever runs the
// server. These routes answer about every household, and the only thing
// between them and an ordinary caller is store.User.IsSuperuser.
var adminPrefixes = map[string]bool{"/admin": true, "/admin/backups": true, "/admin/server": true}

var registry = map[string]resource{}

// Register adds a tenant-scoped resource. Call it from an init function.
func Register(res Resource) { register(res, modeTenant) }

// RegisterIdentity adds an identity resource, whose routes may resolve a user
// or nobody. Only the prefixes in identityPrefixes are accepted.
func RegisterIdentity(res Resource) { register(res, modeIdentity) }

// RegisterAdmin adds the server-administration resource, whose routes resolve
// a user, resolve no space, and refuse everybody but a superuser. Only the
// prefixes in adminPrefixes are accepted.
func RegisterAdmin(res Resource) { register(res, modeAdmin) }

func register(res Resource, m mode) {
	reserved := identityPrefixes[res.Prefix] || adminPrefixes[res.Prefix]
	switch {
	case m == modeIdentity && !identityPrefixes[res.Prefix]:
		panic(fmt.Sprintf("api: %q is not an identity resource; use Register", res.Prefix))
	case m == modeAdmin && !adminPrefixes[res.Prefix]:
		panic(fmt.Sprintf("api: %q is not an administration resource; use Register", res.Prefix))
	case m == modeTenant && reserved:
		panic(fmt.Sprintf("api: %q is reserved for RegisterIdentity or RegisterAdmin", res.Prefix))
	}
	if _, taken := registry[res.Prefix]; taken {
		panic(fmt.Sprintf("api: %q is registered twice", res.Prefix))
	}
	rt := &Routes{prefix: res.Prefix, mode: m}
	res.Routes(rt)
	registry[res.Prefix] = resource{Prefix: res.Prefix, Routes: rt.entries}
}

// registered returns every resource, sorted, so the URL space does not depend
// on package initialization order.
func registered() []resource {
	prefixes := make([]string, 0, len(registry))
	for prefix := range registry {
		prefixes = append(prefixes, prefix)
	}
	sort.Strings(prefixes)

	out := make([]resource, 0, len(prefixes))
	for _, prefix := range prefixes {
		out = append(out, registry[prefix])
	}
	return out
}

// RegisteredRoutes is every route the router will mount, for the contract test
// and for anything that wants to describe the API without serving it.
func RegisteredRoutes() []Route {
	var out []Route
	for _, resource := range registered() {
		out = append(out, resource.Routes...)
	}
	return out
}

// passwordChangeExempt names the only routes an account still carrying an
// operator-chosen password may reach: read yourself, replace the password,
// sign out. Enforced here so a route added later cannot forget it.
func passwordChangeExempt(route Route) bool {
	if route.Prefix != "/auth" {
		return false
	}
	switch route.Pattern {
	case "/me", "/password", "/logout":
		return true
	}
	return false
}

// adapt turns a registered route into an http.HandlerFunc, resolving whatever
// the route's kind says it needs and mapping whatever the handler returns.
func (e *Env) adapt(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := e.serve(route, w, r); err != nil {
			writeError(w, r, err)
		}
	}
}

func (e *Env) serve(route Route, w http.ResponseWriter, r *http.Request) error {
	if route.kind == kindPublic {
		return route.public(e, w, r)
	}

	user, err := e.currentUser(r)
	if err != nil {
		return err
	}
	if user.MustChangePassword && !passwordChangeExempt(route) {
		return errPasswordChangeRequired
	}
	if route.kind == kindSuperuser {
		if !user.IsSuperuser {
			return errNotAdministrator
		}
		return route.user(e, w, r, user)
	}
	if route.kind == kindUser {
		return route.user(e, w, r, user)
	}

	space, err := auth.ResolveSpaceForRequest(r.Context(), e.DB, user, r)
	if err != nil {
		return err
	}
	if route.kind == kindWrite {
		if err := space.RequireWrite(); err != nil {
			return err
		}
	}
	return route.space(e, w, r, space)
}
