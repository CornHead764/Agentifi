package api

import (
	"net/http"
	"regexp"
	"strings"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
)

// The procedures' half of route_contract_test.go: who may call a method is
// declared beside it in proto, so it is checked over the descriptors, which
// include every method added after this was written.

// personalWriteProcedures are the READ methods that change only the caller's
// own rows, so a viewer may call them.
var personalWriteProcedures = map[string]bool{}

func agentifiServices(t *testing.T) []protoreflect.ServiceDescriptor {
	t.Helper()
	var out []protoreflect.ServiceDescriptor
	protoregistry.GlobalFiles.RangeFilesByPackage("agentifi.v1", func(file protoreflect.FileDescriptor) bool {
		services := file.Services()
		for i := range services.Len() {
			out = append(out, services.Get(i))
		}
		return true
	})
	require.NotEmpty(t, out)
	return out
}

func methodsOf(svc protoreflect.ServiceDescriptor) []protoreflect.MethodDescriptor {
	methods := svc.Methods()
	out := make([]protoreflect.MethodDescriptor, 0, methods.Len())
	for i := range methods.Len() {
		out = append(out, methods.Get(i))
	}
	return out
}

func procedureName(method protoreflect.MethodDescriptor) string {
	return "/" + string(method.Parent().FullName()) + "/" + string(method.Name())
}

func TestProcedureAccessMatchesScope(t *testing.T) {
	t.Parallel()
	allowed := map[agentifiv1.Scope]map[agentifiv1.Access]bool{
		agentifiv1.Scope_SCOPE_TENANT: {
			agentifiv1.Access_ACCESS_READ: true, agentifiv1.Access_ACCESS_WRITE: true,
		},
		agentifiv1.Scope_SCOPE_IDENTITY: {
			agentifiv1.Access_ACCESS_READ: true, agentifiv1.Access_ACCESS_WRITE: true,
			agentifiv1.Access_ACCESS_USER: true, agentifiv1.Access_ACCESS_PUBLIC: true,
		},
		agentifiv1.Scope_SCOPE_ADMIN: {agentifiv1.Access_ACCESS_SUPERUSER: true},
	}
	for _, svc := range agentifiServices(t) {
		scope := proto.GetExtension(svc.Options(), agentifiv1.E_Scope).(agentifiv1.Scope)
		require.Contains(t, allowed, scope, "%s declares no scope", svc.FullName())
		for _, method := range methodsOf(svc) {
			access := proto.GetExtension(method.Options(), agentifiv1.E_Access).(agentifiv1.Access)
			require.True(t, allowed[scope][access],
				"%s declares %s, which a %s service may not", procedureName(method), access, scope)
		}
	}
}

func TestTenantProcedureResolvesSpace(t *testing.T) {
	t.Parallel()
	l := buildLedger(t).withOtherHousehold()
	outsider := l.as("alex")
	outsider.spaceID = l.str("other_space")
	for name, proc := range procedures {
		if proc.access != agentifiv1.Access_ACCESS_READ && proc.access != agentifiv1.Access_ACCESS_WRITE {
			continue
		}
		t.Run(name, func(t *testing.T) {
			res := outsider.rpc(name, `{}`)
			p := problemFromWire(res.Code, res.Body.Bytes())
			require.Equal(t, http.StatusNotFound, p.status)
			require.Equal(t, "Space not found", p.detail)
		})
	}
}

func TestReadMethodsHaveNoSideEffects(t *testing.T) {
	t.Parallel()
	// A viewer may call a READ method, so one that writes would let a viewer
	// change the household. The idempotency level is what a client (and a GET
	// over Connect) relies on.
	for _, svc := range agentifiServices(t) {
		scope := proto.GetExtension(svc.Options(), agentifiv1.E_Scope).(agentifiv1.Scope)
		for _, method := range methodsOf(svc) {
			name := procedureName(method)
			access := proto.GetExtension(method.Options(), agentifiv1.E_Access).(agentifiv1.Access)
			pure := method.Options().(*descriptorpb.MethodOptions).GetIdempotencyLevel() ==
				descriptorpb.MethodOptions_NO_SIDE_EFFECTS
			if access == agentifiv1.Access_ACCESS_WRITE {
				require.False(t, pure, "%s writes and says it has no side effects", name)
			}
			if scope != agentifiv1.Scope_SCOPE_TENANT {
				continue
			}
			if access == agentifiv1.Access_ACCESS_READ && !personalWriteProcedures[name] {
				require.True(t, pure, "%s is READ, so a viewer may call it, but declares side effects", name)
			}
			if personalWriteProcedures[name] {
				require.False(t, pure, "%s is listed as a personal write but has no side effects", name)
			}
		}
	}
}

func TestServicesMountedAndDescribed(t *testing.T) {
	t.Parallel()
	described := map[string]bool{}
	for _, svc := range agentifiServices(t) {
		path := "/" + string(svc.FullName()) + "/"
		described[path] = true
		require.Contains(t, rpcServices, path, "%s has no RegisterService call", svc.FullName())
	}
	for path := range rpcServices {
		require.True(t, described[path], "%s is mounted with no agentifi.v1 descriptor", path)
	}

	// And reachable through the router, under its own path.
	l := newClient(t)
	for path := range rpcServices {
		answer := l.raw(http.MethodPost, path+"NoSuchMethod", `{}`)
		require.Equal(t, http.StatusNotFound, answer.Code)
		require.NotContains(t, answer.Body.String(), "Endpoint not found",
			"%s fell through to the router's not-found", path)
	}
}

func TestAdminAndAssistantNotDispatchable(t *testing.T) {
	t.Parallel()
	for _, svc := range agentifiServices(t) {
		scope := proto.GetExtension(svc.Options(), agentifiv1.E_Scope).(agentifiv1.Scope)
		dispatch := proto.GetExtension(svc.Options(), agentifiv1.E_ServiceDispatch).(agentifiv1.Dispatch)
		if scope == agentifiv1.Scope_SCOPE_ADMIN {
			require.Equal(t, agentifiv1.Dispatch_DISPATCH_DENIED, dispatch,
				"%s administers the server and must say it is not dispatchable", svc.FullName())
		}
	}
	reachable := map[string]bool{}
	for _, route := range dispatchableRoutes() {
		reachable[route.Method+" "+route.Path()] = true
	}
	for _, route := range RegisteredRoutes() {
		if route.procedure == "" {
			continue
		}
		proc := procedures[route.procedure]
		key := route.Method + " " + route.Path()
		if proc.dispatch != agentifiv1.Dispatch_DISPATCH_ALLOWED {
			require.False(t, reachable[key], "%s (%s) is denied in proto but dispatchable", key, proc.name)
		}
		// The REST-era lists and the proto must agree, so neither can be
		// edited alone while the bridge exists.
		deniedByList := deniedDispatchPath(route.Path()) || dispatchDenied[route.Prefix] ||
			adminPrefixes[route.Prefix]
		require.Equal(t, deniedByList, proc.dispatch != agentifiv1.Dispatch_DISPATCH_ALLOWED,
			"%s: dispatch.go's lists and %s's dispatch option disagree", key, proc.name)
	}
}

func TestRestAnnotationsUniqueAndBridged(t *testing.T) {
	t.Parallel()
	seen := map[string]string{}
	for name, proc := range procedures {
		if proc.rest == nil {
			continue
		}
		key := strings.ToUpper(proc.rest.GetMethod()) + " " + proc.rest.GetPath()
		require.NotContains(t, seen, key, "%s and %s both claim %s", seen[key], name, key)
		seen[key] = name
	}
	for _, route := range RegisteredRoutes() {
		if route.procedure == "" {
			continue
		}
		key := route.Method + " " + route.Path()
		require.Equal(t, route.procedure, seen[key], "%s is bridged to the wrong procedure", key)
		delete(seen, key)
	}
	require.Empty(t, seen, "REST annotations no bridged route serves")
}

func TestRegistrationRefusesWhatTheInterceptorCannotEnforce(t *testing.T) {
	t.Parallel()
	tenant, identity, admin := agentifiv1.Scope_SCOPE_TENANT, agentifiv1.Scope_SCOPE_IDENTITY, agentifiv1.Scope_SCOPE_ADMIN
	read, user, public := agentifiv1.Access_ACCESS_READ, agentifiv1.Access_ACCESS_USER, agentifiv1.Access_ACCESS_PUBLIC

	require.NoError(t, checkMethodAccess(tenant, read, false, false))
	require.Error(t, checkMethodAccess(tenant, read, false, true), "a streaming method runs past the interceptor")
	require.Error(t, checkMethodAccess(tenant, agentifiv1.Access_ACCESS_UNSPECIFIED, false, false))
	require.Error(t, checkMethodAccess(tenant, public, false, false), "a tenant method must resolve a space")
	require.Error(t, checkMethodAccess(admin, read, false, false), "an admin method has no space to resolve")
	require.Error(t, checkMethodAccess(tenant, read, true, false), "only identity methods escape a password change")
	require.NoError(t, checkMethodAccess(identity, user, true, false))

	require.NoError(t, checkServiceScope(tenant, "/tags"))
	require.NoError(t, checkServiceScope(tenant, ""))
	require.Error(t, checkServiceScope(tenant, "/auth"))
	require.Error(t, checkServiceScope(identity, ""), "identity services are a closed list")
	require.Error(t, checkServiceScope(identity, "/budgets"))
	require.NoError(t, checkServiceScope(identity, "/auth"))
	require.Error(t, checkServiceScope(admin, "/spaces"))
	require.NoError(t, checkServiceScope(admin, "/admin"))
	require.Error(t, checkServiceScope(agentifiv1.Scope_SCOPE_UNSPECIFIED, "/tags"))
}

func TestEveryWriteProcedureRefusesAViewerBeforeItsHandler(t *testing.T) {
	t.Parallel()
	// An empty request would fail inside any handler, with a 404 or a 422, so
	// the 403 shows the refusal came first. Walked from the registry, so a
	// method added later is covered.
	l := buildLedger(t)
	vera := l.as("vera")
	for name, proc := range procedures {
		if proc.access != agentifiv1.Access_ACCESS_WRITE {
			continue
		}
		res := vera.rpc(name, `{}`)
		p := problemFromWire(res.Code, res.Body.Bytes())
		require.Equal(t, http.StatusForbidden, p.status, "%s: %s", name, res.Body.String())
		require.Equal(t, "This space is read-only for you", p.detail, name)
	}
	for _, route := range RegisteredRoutes() {
		if route.procedure == "" || route.kind != kindWrite {
			continue
		}
		path := placeholder.ReplaceAllString(route.Path(), "00000000-0000-0000-0000-000000000001")
		vera.raw(route.Method, path, `not json`).requireStatus(http.StatusForbidden)
	}
}

var placeholder = regexp.MustCompile(`\{[^}]+\}`)

func TestEveryProcedureButAPublicOneRefusesAnUnknownCaller(t *testing.T) {
	t.Parallel()
	anonymous := newClient(t)
	for name, proc := range procedures {
		if proc.access == agentifiv1.Access_ACCESS_PUBLIC {
			continue
		}
		anonymous.rpc(name, `{}`).requireCode(connect.CodeUnauthenticated).requireStatus(http.StatusUnauthorized)
	}
}
