package api

import (
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoregistry"
	"google.golang.org/protobuf/types/descriptorpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
)

// The bridge's rules over shapes no converted service has yet, on a service
// built from descriptors at test time and never registered.

type testMethod struct {
	name          string
	access        agentifiv1.Access
	rest          *agentifiv1.Rest
	input, output string
}

func testService(t *testing.T, scope agentifiv1.Scope, messages []*descriptorpb.DescriptorProto, methods ...testMethod) []procedure {
	t.Helper()
	serviceOptions := &descriptorpb.ServiceOptions{}
	proto.SetExtension(serviceOptions, agentifiv1.E_Scope, scope)
	service := &descriptorpb.ServiceDescriptorProto{Name: proto.String("TestService"), Options: serviceOptions}
	for _, method := range methods {
		options := &descriptorpb.MethodOptions{}
		proto.SetExtension(options, agentifiv1.E_Access, method.access)
		proto.SetExtension(options, agentifiv1.E_Rest, method.rest)
		service.Method = append(service.Method, &descriptorpb.MethodDescriptorProto{
			Name:       proto.String(method.name),
			InputType:  proto.String(".agentifi.bridgetest." + method.input),
			OutputType: proto.String(".agentifi.bridgetest." + method.output),
			Options:    options,
		})
	}
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:        proto.String("agentifi/bridgetest/test.proto"),
		Package:     proto.String("agentifi.bridgetest"),
		Syntax:      proto.String("proto3"),
		Dependency:  []string{"agentifi/v1/options.proto", "agentifi/v1/common.proto"},
		MessageType: messages,
		Service:     []*descriptorpb.ServiceDescriptorProto{service},
	}, protoregistry.GlobalFiles)
	require.NoError(t, err)
	procs, err := serviceProcedures(&rpcService{
		path: "/agentifi.bridgetest.TestService/", desc: file.Services().Get(0),
	})
	require.NoError(t, err)
	return procs
}

func testMessage(name string, fields ...*descriptorpb.FieldDescriptorProto) *descriptorpb.DescriptorProto {
	for i, field := range fields {
		field.Number = proto.Int32(int32(i + 1))
		field.JsonName = field.Name
	}
	return &descriptorpb.DescriptorProto{Name: proto.String(name), Field: fields}
}

func testField(name string, kind descriptorpb.FieldDescriptorProto_Type, typeName string) *descriptorpb.FieldDescriptorProto {
	field := &descriptorpb.FieldDescriptorProto{
		Name:  proto.String(name),
		Type:  kind.Enum(),
		Label: descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL.Enum(),
	}
	if typeName != "" {
		field.TypeName = proto.String(typeName)
	}
	return field
}

func stringField(name string) *descriptorpb.FieldDescriptorProto {
	return testField(name, descriptorpb.FieldDescriptorProto_TYPE_STRING, "")
}

func TestBridgeFilesEachURLUnderItsOwnPrefix(t *testing.T) {
	t.Parallel()
	read := agentifiv1.Access_ACCESS_READ
	procs := testService(t, agentifiv1.Scope_SCOPE_TENANT,
		[]*descriptorpb.DescriptorProto{testMessage("WidgetRequest", stringField("widget_id")), testMessage("Empty")},
		testMethod{"GetWidget", read, &agentifiv1.Rest{Method: "GET", Path: "/widgets/{widget_id}"}, "WidgetRequest", "Empty"},
		testMethod{"ListGadgets", read, &agentifiv1.Rest{Method: "GET", Path: "/gadgets"}, "Empty", "Empty"},
	)
	want := []struct{ prefix, pattern string }{{"/widgets", "/{widget_id}"}, {"/gadgets", "/"}}
	require.Len(t, procs, len(want))
	for i, proc := range procs {
		prefix, m, routes, err := bridgeRoutes(proc)
		require.NoError(t, err)
		require.Equal(t, want[i].prefix, prefix)
		require.Equal(t, modeTenant, m)
		require.Len(t, routes, 1)
		require.Equal(t, want[i].pattern, routes[0].Pattern)
		require.Equal(t, kindRead, routes[0].kind)
		require.Equal(t, proc.name, routes[0].procedure)
	}
}

func TestBridgeRefusesATenantURLUnderAnIdentityPrefix(t *testing.T) {
	t.Parallel()
	procs := testService(t, agentifiv1.Scope_SCOPE_TENANT,
		[]*descriptorpb.DescriptorProto{testMessage("Empty")},
		testMethod{"ReadCurrent", agentifiv1.Access_ACCESS_READ,
			&agentifiv1.Rest{Method: "GET", Path: "/spaces/current"}, "Empty", "Empty"},
	)
	_, _, _, err := bridgeRoutes(procs[0])
	require.ErrorContains(t, err, `"/spaces" is an identity prefix`)
}

func TestBridgeScopePerPrefix(t *testing.T) {
	t.Parallel()
	tenant, identity, admin := agentifiv1.Scope_SCOPE_TENANT, agentifiv1.Scope_SCOPE_IDENTITY, agentifiv1.Scope_SCOPE_ADMIN
	read, user, public, superuser := agentifiv1.Access_ACCESS_READ, agentifiv1.Access_ACCESS_USER,
		agentifiv1.Access_ACCESS_PUBLIC, agentifiv1.Access_ACCESS_SUPERUSER
	for _, c := range []struct {
		name   string
		scope  agentifiv1.Scope
		access agentifiv1.Access
		prefix string
		ok     bool
	}{
		{"tenant under its own prefix", tenant, read, "/tags", true},
		{"tenant under identity", tenant, read, "/spaces", false},
		{"tenant under admin", tenant, read, "/admin", false},
		{"identity user under identity", identity, user, "/spaces", true},
		{"identity read under a tenant prefix", identity, read, "/setup-guide", true},
		{"identity user under a tenant prefix", identity, user, "/setup-guide", false},
		{"identity public under a tenant prefix", identity, public, "/setup-guide", false},
		{"identity under admin", identity, user, "/admin/server", false},
		{"admin under admin", admin, superuser, "/admin/backups", true},
		{"admin under identity", admin, superuser, "/auth", false},
		{"admin under a tenant prefix", admin, superuser, "/tags", false},
	} {
		err := checkBridgeScope(c.scope, c.access, c.prefix)
		require.Equal(t, c.ok, err == nil, "%s: %v", c.name, err)
	}
}

func TestRestPrefixOf(t *testing.T) {
	t.Parallel()
	for path, want := range map[string]string{
		"/tags":                      "/tags",
		"/tags/{tag_id}":             "/tags",
		"/spaces/current/dashboard":  "/spaces",
		"/admin/users/{user_id}":     "/admin",
		"/admin/backups/run":         "/admin/backups",
		"/admin/server":              "/admin/server",
		"/admin-notes":               "/admin-notes",
		"/account-balance-history":   "/account-balance-history",
		"/setup-guide":               "/setup-guide",
		"/spaces/{space_id}/members": "/spaces",
	} {
		got, err := restPrefixOf(path)
		require.NoError(t, err, path)
		require.Equal(t, want, got, path)
	}
	for _, path := range []string{"", "tags", "/", "/{tag_id}"} {
		_, err := restPrefixOf(path)
		require.Error(t, err, path)
	}
}
