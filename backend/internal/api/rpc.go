package api

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/reflect/protoregistry"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// How a service joins the API: one file whose init function hands
// RegisterService the generated constructor, mounted by RouterFor without
// anybody editing a shared file.
//
//	func init() {
//		RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
//			return agentifiv1connect.NewTagServiceHandler(tagService{env}, opts...)
//		})
//	}
//
// Who may call a method is declared beside it in proto (options.proto), and
// the access interceptor resolves the caller and the space before the handler
// runs, the same way Env.serve does for a REST route. A method that declares
// no access is refused. A handler returns error as a REST handler does, and
// errors.go maps it.

// ServiceFactory builds one service's handler. RegisterService also calls it
// with a nil Env to learn its path, so it must not use env until a request.
type ServiceFactory func(env *Env, opts ...connect.HandlerOption) (string, http.Handler)

type rpcService struct {
	// path is the mount path, "/agentifi.v1.TagService/".
	path    string
	desc    protoreflect.ServiceDescriptor
	factory ServiceFactory
}

// procedure is one method as the access interceptor and the bridge read it.
type procedure struct {
	// name is the full procedure, "/agentifi.v1.TagService/ListTags".
	name    string
	service *rpcService
	method  protoreflect.MethodDescriptor
	access  agentifiv1.Access
	scope   agentifiv1.Scope
	exempt  bool
	// dispatch is the method's, else the service's, else allowed.
	dispatch agentifiv1.Dispatch
	// rest is nil for a method that never had a REST URL.
	rest *agentifiv1.Rest
}

var (
	rpcServices = map[string]*rpcService{}
	procedures  = map[string]procedure{}
)

// RegisterService adds a Connect service. Call it from an init function.
func RegisterService(factory ServiceFactory) {
	path, _ := factory(nil)
	name := protoreflect.FullName(strings.Trim(path, "/"))
	found, err := protoregistry.GlobalFiles.FindDescriptorByName(name)
	if err != nil {
		panic(fmt.Sprintf("api: service %q has no descriptor: %v", name, err))
	}
	desc, ok := found.(protoreflect.ServiceDescriptor)
	if !ok {
		panic(fmt.Sprintf("api: %q is not a service", name))
	}
	if _, taken := rpcServices[path]; taken {
		panic(fmt.Sprintf("api: service %q is registered twice", name))
	}
	svc := &rpcService{path: path, desc: desc, factory: factory}
	rpcServices[path] = svc

	scope, _ := proto.GetExtension(desc.Options(), agentifiv1.E_Scope).(agentifiv1.Scope)
	serviceDispatch, _ := proto.GetExtension(desc.Options(), agentifiv1.E_ServiceDispatch).(agentifiv1.Dispatch)
	methods := desc.Methods()
	for i := range methods.Len() {
		method := methods.Get(i)
		options := method.Options()
		access, _ := proto.GetExtension(options, agentifiv1.E_Access).(agentifiv1.Access)
		exempt, _ := proto.GetExtension(options, agentifiv1.E_PasswordChangeExempt).(bool)
		dispatch, _ := proto.GetExtension(options, agentifiv1.E_Dispatch).(agentifiv1.Dispatch)
		if dispatch == agentifiv1.Dispatch_DISPATCH_UNSPECIFIED {
			dispatch = serviceDispatch
		}
		if dispatch == agentifiv1.Dispatch_DISPATCH_UNSPECIFIED {
			dispatch = agentifiv1.Dispatch_DISPATCH_ALLOWED
		}
		rest, _ := proto.GetExtension(options, agentifiv1.E_Rest).(*agentifiv1.Rest)
		if rest.GetMethod() == "" {
			rest = nil
		}
		proc := procedure{
			name:    "/" + string(desc.FullName()) + "/" + string(method.Name()),
			service: svc, method: method,
			access: access, scope: scope, exempt: exempt, dispatch: dispatch, rest: rest,
		}
		procedures[proc.name] = proc
		if rest != nil {
			registerBridge(proc)
		}
	}
}

// registeredServices returns every service, sorted, so the URL space does not
// depend on package initialization order.
func registeredServices() []*rpcService {
	out := make([]*rpcService, 0, len(rpcServices))
	for _, svc := range rpcServices {
		out = append(out, svc)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].path < out[j].path })
	return out
}

// rpcState is each Env's built service handlers: the interceptors close over
// the Env, so they are built once per Env rather than once per process.
type rpcState struct {
	once     sync.Once
	handlers map[string]http.Handler
}

func (e *Env) serviceHandler(path string) http.Handler {
	e.rpc.once.Do(func() {
		e.rpc.handlers = make(map[string]http.Handler, len(rpcServices))
		for _, svc := range registeredServices() {
			_, handler := svc.factory(e, e.handlerOptions()...)
			e.rpc.handlers[svc.path] = handler
		}
	})
	return e.rpc.handlers[path]
}

func (e *Env) handlerOptions() []connect.HandlerOption {
	interceptors := []connect.Interceptor{problemInterceptor(), e.accessInterceptor()}
	if e.Cfg != nil && e.Cfg.Debug {
		interceptors = append(interceptors, moneySetInterceptor())
	}
	return []connect.HandlerOption{
		connect.WithCodec(jsonCodec{name: "json"}),
		connect.WithCodec(jsonCodec{name: "json; charset=utf-8"}),
		connect.WithInterceptors(interceptors...),
		connect.WithReadMaxBytes(maxBodyBytes),
	}
}

// mountServices mounts every service under its path. The handler matches the
// whole URL path against its procedures, so the prefix this router is mounted
// under (/api) is stripped first.
func mountServices(r chi.Router, env *Env) {
	for _, svc := range registeredServices() {
		handler := env.serviceHandler(svc.path)
		r.Handle(svc.path+"*", http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			if routed := chi.RouteContext(req.Context()); routed != nil && routed.RoutePath != "" {
				http.StripPrefix(strings.TrimSuffix(req.URL.Path, routed.RoutePath), handler).ServeHTTP(w, req)
				return
			}
			handler.ServeHTTP(w, req)
		}))
	}
}

// jsonCodec is the wire: field names as the proto declares them, every field
// emitted (a client tests a null, never a missing key), and an unknown field
// refused rather than silently dropped.
type jsonCodec struct{ name string }

func (c jsonCodec) Name() string { return c.name }

func (jsonCodec) Marshal(message any) ([]byte, error) {
	m, ok := message.(proto.Message)
	if !ok {
		return nil, fmt.Errorf("api: %T is not a proto message", message)
	}
	return protojson.MarshalOptions{UseProtoNames: true, EmitUnpopulated: true}.Marshal(m)
}

func (jsonCodec) Unmarshal(data []byte, message any) error {
	m, ok := message.(proto.Message)
	if !ok {
		return fmt.Errorf("api: %T is not a proto message", message)
	}
	return protojson.Unmarshal(data, m)
}

// --- The caller, in the context ---------------------------------------------

type callerKey struct{}

type caller struct {
	user     store.User
	space    auth.SpaceContext
	hasSpace bool
}

// spaceFrom is the space a READ or WRITE procedure was resolved against.
func spaceFrom(ctx context.Context) auth.SpaceContext {
	who, ok := ctx.Value(callerKey{}).(caller)
	if !ok || !who.hasSpace {
		panic("api: spaceFrom in a procedure that resolves no space")
	}
	return who.space
}

// userFrom is the caller of any procedure but a PUBLIC one.
func userFrom(ctx context.Context) store.User {
	who, ok := ctx.Value(callerKey{}).(caller)
	if !ok {
		panic("api: userFrom in a procedure that resolves no caller")
	}
	return who.user
}

// --- The access interceptor -------------------------------------------------

// authorizedKey marks a call the REST bridge has already authorized for the
// procedure it names, with the caller in the context.
type authorizedKey struct{}

var errUndeclaredAccess = errors.New("api: the procedure declares no access")

// accessInterceptor does for a procedure what Env.serve does for a route.
func (e *Env) accessInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			proc, ok := procedures[req.Spec().Procedure]
			if !ok || proc.access == agentifiv1.Access_ACCESS_UNSPECIFIED {
				return nil, fmt.Errorf("%w: %s", errUndeclaredAccess, req.Spec().Procedure)
			}
			if authorized, _ := ctx.Value(authorizedKey{}).(string); authorized == proc.name {
				return next(ctx, req)
			}
			ctx, err := e.authorize(ctx, proc, req.Header())
			if err != nil {
				return nil, err
			}
			return next(ctx, req)
		}
	}
}

func (e *Env) authorize(ctx context.Context, proc procedure, header http.Header) (context.Context, error) {
	// An in-process call carries a space already resolved for its caller, and
	// reaches only what the dispatcher may.
	if state, ok := ctx.Value(dispatchKeys{}).(dispatchState); ok {
		tenant := proc.access == agentifiv1.Access_ACCESS_READ || proc.access == agentifiv1.Access_ACCESS_WRITE
		if !tenant || proc.dispatch != agentifiv1.Dispatch_DISPATCH_ALLOWED {
			return ctx, errNotFound("Endpoint")
		}
		if proc.access == agentifiv1.Access_ACCESS_WRITE {
			if err := state.sp.RequireWrite(); err != nil {
				return ctx, err
			}
		}
		return context.WithValue(ctx, callerKey{}, caller{user: state.sp.User, space: state.sp, hasSpace: true}), nil
	}

	if proc.access == agentifiv1.Access_ACCESS_PUBLIC {
		return ctx, nil
	}
	user, err := e.userFromToken(ctx, bearerFrom(header))
	if err != nil {
		return ctx, err
	}
	if user.MustChangePassword && !proc.exempt {
		return ctx, errPasswordChangeRequired
	}
	switch proc.access {
	case agentifiv1.Access_ACCESS_SUPERUSER:
		if !user.IsSuperuser {
			return ctx, errNotAdministrator
		}
		return context.WithValue(ctx, callerKey{}, caller{user: user}), nil
	case agentifiv1.Access_ACCESS_USER:
		return context.WithValue(ctx, callerKey{}, caller{user: user}), nil
	}

	space, err := auth.ResolveSpace(ctx, e.DB, user, header.Get(auth.HeaderSpaceID))
	if err != nil {
		return ctx, err
	}
	if proc.access == agentifiv1.Access_ACCESS_WRITE {
		if err := space.RequireWrite(); err != nil {
			return ctx, err
		}
	}
	return context.WithValue(ctx, callerKey{}, caller{user: user, space: space, hasSpace: true}), nil
}

// --- Errors -----------------------------------------------------------------

// problemInterceptor maps a handler's error, or the access interceptor's,
// through classify: one table for REST and Connect alike.
func problemInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			if err != nil {
				return nil, connectError(ctx, req.Spec().Procedure, err)
			}
			return res, nil
		}
	}
}

func connectError(ctx context.Context, procedure string, err error) *connect.Error {
	var mapped *connect.Error
	if errors.As(err, &mapped) {
		return mapped
	}
	p := classify(ctx, err)
	if p.unhandled {
		logUnhandled(ctx, "POST", procedure, err)
	}
	out := connect.NewError(connectCode(p.status), errors.New(p.detail))
	if detail, detailErr := connect.NewErrorDetail(p.proto()); detailErr == nil {
		out.AddDetail(detail)
	}
	return out
}

func (p problem) proto() *agentifiv1.Problem {
	out := &agentifiv1.Problem{Code: p.code, Status: int32(p.status), HasFailureScreenshot: p.screenshot}
	for _, field := range p.fields {
		out.Fields = append(out.Fields, &agentifiv1.FieldError{Loc: field.Loc, Msg: field.Msg, Type: field.Type})
	}
	return out
}

func connectCode(status int) connect.Code {
	switch status {
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return connect.CodeInvalidArgument
	case http.StatusUnauthorized:
		return connect.CodeUnauthenticated
	case http.StatusForbidden:
		return connect.CodePermissionDenied
	case http.StatusNotFound:
		return connect.CodeNotFound
	case http.StatusConflict:
		return connect.CodeFailedPrecondition
	case http.StatusTooManyRequests:
		return connect.CodeResourceExhausted
	case http.StatusBadGateway, http.StatusServiceUnavailable:
		return connect.CodeUnavailable
	case clientClosedRequest:
		return connect.CodeCanceled
	default:
		return connect.CodeInternal
	}
}

// --- Money is never left out ------------------------------------------------

// moneySetInterceptor fails a response with a Money field left unset, in
// development and tests. Unset Money reads as nothing at all on a client, and
// a figure that may be absent is NullableMoney.
func moneySetInterceptor() connect.UnaryInterceptorFunc {
	return func(next connect.UnaryFunc) connect.UnaryFunc {
		return func(ctx context.Context, req connect.AnyRequest) (connect.AnyResponse, error) {
			res, err := next(ctx, req)
			if err != nil {
				return res, err
			}
			if message, ok := res.Any().(proto.Message); ok {
				if at := unsetMoney(message.ProtoReflect(), ""); at != "" {
					return nil, fmt.Errorf("api: %s answered with Money %s unset; "+
						"an amount that may be absent is NullableMoney", req.Spec().Procedure, at)
				}
			}
			return res, nil
		}
	}
}

var moneyName = (&agentifiv1.Money{}).ProtoReflect().Descriptor().FullName()

// unsetMoney is the path of the first Money field under m that is unset or
// empty, or "".
func unsetMoney(m protoreflect.Message, path string) string {
	fields := m.Descriptor().Fields()
	for i := range fields.Len() {
		field := fields.Get(i)
		if field.Kind() != protoreflect.MessageKind || field.IsMap() {
			continue
		}
		at := path + "." + string(field.Name())
		isMoney := field.Message().FullName() == moneyName
		if field.IsList() {
			list := m.Get(field).List()
			for j := range list.Len() {
				if found := unsetMoneyIn(list.Get(j).Message(), isMoney, fmt.Sprintf("%s[%d]", at, j)); found != "" {
					return found
				}
			}
			continue
		}
		if !m.Has(field) {
			if isMoney {
				return at
			}
			continue
		}
		if found := unsetMoneyIn(m.Get(field).Message(), isMoney, at); found != "" {
			return found
		}
	}
	return ""
}

func unsetMoneyIn(m protoreflect.Message, isMoney bool, at string) string {
	if isMoney {
		if m.Interface().(*agentifiv1.Money).GetAmount() == "" {
			return at
		}
		return ""
	}
	return unsetMoney(m, at)
}
