package api

import (
	"net/http"
	"sort"

	"github.com/CornHead764/agentifi/backend/internal/auth"
)

// What this server offers, served from the registry so a tool (the MCP server
// in tools/agentifi-mcp) describes the deployment rather than a copy of the
// route list that drifts.
//
// Tenant-scoped although the answer is the same in every space, so this is
// not an exception to the closed identity-prefix list.

func init() {
	Register(Resource{Prefix: "/routes", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listRoutes)
	}})
}

// RouteResponse is one mounted route.
type RouteResponse struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	// Scope is "space" for one household's data, "user" for the caller alone,
	// and "public" for routes with no caller.
	Scope string `json:"scope"`
	// Writes is true for anything that is not a GET, so a reader can tell at a
	// glance which half of the list is safe to explore.
	Writes bool `json:"writes"`
	// Denied is a route the assistant's in-process dispatch refuses. A tool
	// that hands a model this API honours the same list.
	Denied bool `json:"denied"`
}

func listRoutes(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	routes := RegisteredRoutes()
	out := make([]RouteResponse, 0, len(routes))
	for _, route := range routes {
		out = append(out, RouteResponse{
			Method: route.Method,
			Path:   route.Path(),
			Scope:  scopeOf(route),
			Writes: route.Method != http.MethodGet,
			Denied: refuseDeniedPath(route.Path()) != nil,
		})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return writeJSON(w, http.StatusOK, out)
}

func scopeOf(route Route) string {
	switch route.kind {
	case kindPublic:
		return "public"
	case kindUser:
		return "user"
	default:
		return "space"
	}
}
