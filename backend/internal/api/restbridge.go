package api

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"connectrpc.com/connect"
	"github.com/go-chi/chi/v5"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
)

// The REST bridge: a converted method's old URL, served from its
// (agentifi.v1.rest) annotation by calling the procedure in-process, so every
// REST caller (the web app, the MCP server, the assistant's dispatcher and its
// stored actions, the tests) keeps working while callers move.
//
// The URL is filed in the registry under its old prefix and kind, so /routes,
// the dispatcher and route_contract_test.go still see it. The access
// interceptor resolves the caller; adapt resolves nothing for a bridged route.
//
// A request becomes the method's JSON message: path and query parameters
// named after request fields, then the body's keys, a money string becoming a
// Money message and arbitrary JSON the text of its *_json field. For a method with update_mask, the keys the body sent are the
// mask and a null among them is a field to clear. The answer goes back as the
// REST wire had it: Money as its string, int64 as a number, an enum as its
// lower-case suffix, a *_json field as the JSON it holds, response_body
// answered bare, the annotation's status. A
// refusal is the body errors.go writes, rebuilt from the Problem detail.

type bridge struct {
	proc procedure
	// params are the path parameters, each a request field.
	params []string
	// maskable is a request with update_mask, which the bridge fills.
	maskable bool
}

func registerBridge(proc procedure) {
	prefix, _ := proto.GetExtension(proc.service.desc.Options(), agentifiv1.E_RestPrefix).(string)
	path := proc.rest.GetPath()
	if prefix == "" || (path != prefix && !strings.HasPrefix(path, prefix+"/")) {
		panic(fmt.Sprintf("api: %s has REST path %q outside its service's rest_prefix %q",
			proc.name, path, prefix))
	}
	pattern := strings.TrimPrefix(path, prefix)
	if pattern == "" {
		pattern = "/"
	}

	input := proc.method.Input()
	b := bridge{proc: proc}
	for _, segment := range strings.Split(pattern, "/") {
		name, isParam := strings.CutPrefix(segment, "{")
		if !isParam {
			continue
		}
		name = strings.TrimSuffix(name, "}")
		field := input.Fields().ByName(protoreflect.Name(name))
		if field == nil || field.Kind() != protoreflect.StringKind || field.IsList() {
			panic(fmt.Sprintf("api: %s path parameter {%s} is not a string field of %s",
				proc.name, name, input.FullName()))
		}
		b.params = append(b.params, name)
	}
	if mask := input.Fields().ByName("update_mask"); mask != nil && mask.Message() != nil &&
		mask.Message().FullName() == "google.protobuf.FieldMask" {
		b.maskable = true
	}
	if body := protoreflect.Name(proc.rest.GetResponseBody()); body != "" &&
		proc.method.Output().Fields().ByName(body) == nil &&
		proc.method.Output().Oneofs().ByName(body) == nil {
		panic(fmt.Sprintf("api: %s response_body %q is not a field or oneof of %s",
			proc.name, body, proc.method.Output().FullName()))
	}

	m := modeOf(proc)
	checkPrefix(prefix, m)
	rt := &Routes{prefix: prefix, mode: m}
	rt.bridge(kindOf(proc), strings.ToUpper(proc.rest.GetMethod()), pattern, b.serve)
	for i := range rt.entries {
		rt.entries[i].procedure = proc.name
	}
	addRoutes(prefix, m, rt.entries, false)
}

func modeOf(proc procedure) mode {
	switch proc.scope {
	case agentifiv1.Scope_SCOPE_TENANT:
		return modeTenant
	case agentifiv1.Scope_SCOPE_IDENTITY:
		return modeIdentity
	case agentifiv1.Scope_SCOPE_ADMIN:
		return modeAdmin
	}
	panic(fmt.Sprintf("api: %s's service declares no scope", proc.name))
}

func kindOf(proc procedure) kind {
	switch proc.access {
	case agentifiv1.Access_ACCESS_READ:
		return kindRead
	case agentifiv1.Access_ACCESS_WRITE:
		return kindWrite
	case agentifiv1.Access_ACCESS_USER:
		return kindUser
	case agentifiv1.Access_ACCESS_PUBLIC:
		return kindPublic
	case agentifiv1.Access_ACCESS_SUPERUSER:
		return kindSuperuser
	}
	panic(fmt.Sprintf("api: %s declares no access", proc.name))
}

func (b bridge) serve(env *Env, w http.ResponseWriter, r *http.Request) {
	// Authorized before the request is read, as the REST routes were: a caller
	// who may not reach the route is told that, whatever they sent.
	ctx, err := env.authorize(r.Context(), b.proc, r.Header)
	if err != nil {
		writeError(w, r, err)
		return
	}
	message, err := b.request(r)
	if err != nil {
		writeError(w, r, err)
		return
	}
	ctx = context.WithValue(ctx, authorizedKey{}, b.proc.name)
	call, err := http.NewRequestWithContext(ctx, http.MethodPost, b.proc.name, bytes.NewReader(message))
	if err != nil {
		writeError(w, r, err)
		return
	}
	call.Header = r.Header.Clone()
	// The answer is read here, not by the client, so it must come back plain.
	for _, name := range []string{
		"Accept-Encoding", "Content-Encoding", "Content-Length",
		"Connect-Accept-Encoding", "Connect-Content-Encoding", "Connect-Timeout-Ms",
	} {
		call.Header.Del(name)
	}
	call.Header.Set("Content-Type", "application/json")
	call.Header.Set("Connect-Protocol-Version", "1")
	call.Host, call.RemoteAddr, call.TLS = r.Host, r.RemoteAddr, r.TLS

	answer := &bridgeRecorder{header: http.Header{}, status: http.StatusOK}
	env.serviceHandler(b.proc.service.path).ServeHTTP(answer, call)
	if answer.status != http.StatusOK {
		if r.Context().Err() != nil {
			writeProblem(w, problem{status: clientClosedRequest})
			return
		}
		writeProblem(w, problemFromWire(answer.status, answer.body.Bytes()))
		return
	}
	b.respond(w, r, answer.body.Bytes())
}

type bridgeRecorder struct {
	header http.Header
	status int
	body   bytes.Buffer
}

func (rec *bridgeRecorder) Header() http.Header         { return rec.header }
func (rec *bridgeRecorder) WriteHeader(status int)      { rec.status = status }
func (rec *bridgeRecorder) Write(p []byte) (int, error) { return rec.body.Write(p) }

// problemFromWire reads a Connect error body back into the refusal it was
// classified as. One without a Problem was refused by the codec, before any
// handler or interceptor: a body the schema will not take.
func problemFromWire(status int, body []byte) problem {
	var wire struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Details []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"details"`
	}
	if err := json.Unmarshal(body, &wire); err != nil {
		return problem{status: http.StatusInternalServerError, detail: "Internal server error", unhandled: true}
	}
	problemType := string((&agentifiv1.Problem{}).ProtoReflect().Descriptor().FullName())
	for _, detail := range wire.Details {
		if detail.Type != problemType {
			continue
		}
		raw, err := base64.RawStdEncoding.DecodeString(strings.TrimRight(detail.Value, "="))
		if err != nil {
			continue
		}
		var pb agentifiv1.Problem
		if proto.Unmarshal(raw, &pb) == nil {
			return problemOf(&pb, wire.Message)
		}
	}
	var code connect.Code
	if code.UnmarshalText([]byte(wire.Code)) == nil && code == connect.CodeInvalidArgument {
		return problem{
			status: http.StatusUnprocessableEntity, detail: wire.Message,
			fields: []fieldError{{Loc: []string{"body"}, Msg: wire.Message, Type: "json_invalid"}},
		}
	}
	return problem{status: status, detail: wire.Message}
}

func problemOf(pb *agentifiv1.Problem, message string) problem {
	p := problem{
		status: int(pb.GetStatus()), detail: message,
		code: pb.GetCode(), screenshot: pb.GetHasFailureScreenshot(),
	}
	if p.status == 0 {
		p.status = http.StatusInternalServerError
	}
	for _, field := range pb.GetFields() {
		p.fields = append(p.fields, fieldError{Loc: field.GetLoc(), Msg: field.GetMsg(), Type: field.GetType()})
	}
	return p
}

// --- The request -------------------------------------------------------------

func (b bridge) request(r *http.Request) ([]byte, error) {
	fields := b.proc.method.Input().Fields()
	reserved := map[string]bool{}
	for _, name := range b.params {
		reserved[name] = true
	}
	if b.maskable {
		reserved["update_mask"] = true
	}
	message := map[string]any{}

	// A query parameter the request does not name is ignored, as every REST
	// handler ignored one it did not read.
	query := r.URL.Query()
	keys := make([]string, 0, len(query))
	for key := range query {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, key := range keys {
		field := fields.ByName(protoreflect.Name(key))
		if field == nil || reserved[key] {
			continue
		}
		value, keep, err := queryValue(field, key, query[key])
		if err != nil {
			return nil, err
		}
		if keep {
			message[key] = value
		}
	}

	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		required := r.Method != http.MethodDelete && !b.proc.rest.GetBodyOptional()
		order, body, err := readObject(r, required)
		if err != nil {
			return nil, err
		}
		var mask []string
		for _, key := range order {
			name := key
			field := fields.ByName(protoreflect.Name(key))
			if field == nil {
				if text := fields.ByName(protoreflect.Name(key + jsonTextSuffix)); text != nil && isJSONText(text) {
					name, field = key+jsonTextSuffix, text
				}
			}
			if field == nil || reserved[name] {
				return nil, errInvalid("extra_forbidden", []string{"body", key},
					"%s is not a field on this request", key)
			}
			mask = append(mask, name)
			if body[key] == nil {
				delete(message, name)
				continue
			}
			if name != key {
				text, err := json.Marshal(body[key])
				if err != nil {
					return nil, err
				}
				message[name] = string(text)
				continue
			}
			value, err := bodyValue(field, body[key], []string{"body", key})
			if err != nil {
				return nil, err
			}
			message[key] = value
		}
		if b.maskable {
			message["update_mask"] = maskJSON(mask)
		}
	}

	for _, name := range b.params {
		message[name] = chi.URLParam(r, name)
	}
	return json.Marshal(message)
}

// readObject reads a JSON object body, keeping its keys in the order sent so
// the first wrong one is the one reported, as the REST decoder did.
func readObject(r *http.Request, required bool) ([]string, map[string]any, error) {
	values := map[string]any{}
	if r.Body == nil {
		return nil, values, missingBody(required)
	}
	data, err := io.ReadAll(http.MaxBytesReader(nil, r.Body, maxBodyBytes))
	if err != nil {
		return nil, nil, errInvalid("json_invalid", []string{"body"}, "the request body is not valid JSON: %s", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil, values, missingBody(required)
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	notJSON := func(err error) error {
		if errors.Is(err, io.EOF) {
			err = io.ErrUnexpectedEOF
		}
		return errInvalid("json_invalid", []string{"body"}, "the request body is not valid JSON: %s", err)
	}
	opening, err := decoder.Token()
	if err != nil {
		return nil, nil, notJSON(err)
	}
	if opening == nil {
		return nil, values, nil
	}
	if delim, ok := opening.(json.Delim); !ok || delim != '{' {
		return nil, nil, notJSON(fmt.Errorf("json: cannot unmarshal %s into an object", jsonKind(opening)))
	}
	var order []string
	for decoder.More() {
		token, err := decoder.Token()
		if err != nil {
			return nil, nil, notJSON(err)
		}
		key, _ := token.(string)
		var value any
		if err := decoder.Decode(&value); err != nil {
			return nil, nil, notJSON(err)
		}
		if _, seen := values[key]; !seen {
			order = append(order, key)
		}
		values[key] = value
	}
	if _, err := decoder.Token(); err != nil {
		return nil, nil, notJSON(err)
	}
	return order, values, nil
}

func jsonKind(token json.Token) string {
	switch token.(type) {
	case json.Delim:
		return "array"
	case string:
		return "string"
	case bool:
		return "bool"
	default:
		return "number"
	}
}

func missingBody(required bool) error {
	if required {
		return errInvalid("missing", []string{"body"}, "a request body is required")
	}
	return nil
}

func queryValue(field protoreflect.FieldDescriptor, key string, values []string) (any, bool, error) {
	if field.Kind() == protoreflect.MessageKind && field.Message().FullName() == idSetName {
		return map[string]any{"ids": splitList(values)}, true, nil
	}
	if field.IsList() {
		out := []any{}
		for _, part := range splitList(values) {
			value, keep, err := queryScalar(field, key, part.(string))
			if err != nil {
				return nil, false, err
			}
			if keep {
				out = append(out, value)
			}
		}
		return out, len(out) > 0, nil
	}
	if field.IsMap() || len(values) == 0 {
		return nil, false, nil
	}
	return queryScalar(field, key, values[0])
}

func splitList(values []string) []any {
	out := []any{}
	for _, value := range values {
		for _, part := range strings.Split(value, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func queryScalar(field protoreflect.FieldDescriptor, key, raw string) (any, bool, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return nil, false, nil
	}
	loc := []string{"query", key}
	switch field.Kind() {
	case protoreflect.StringKind:
		return raw, true, nil
	case protoreflect.BoolKind:
		value, err := strconv.ParseBool(trimmed)
		if err != nil {
			return nil, false, errInvalid("bool_parsing", loc, "%s must be true or false", key)
		}
		return value, true, nil
	case protoreflect.EnumKind:
		value, err := enumFromREST(field.Enum(), trimmed, loc)
		return value, value != nil, err
	case protoreflect.MessageKind:
		if isMoneyMessage(field.Message()) {
			if _, err := parseMoney(trimmed); err != nil {
				return nil, false, errInvalid("decimal_parsing", loc, "%s", err)
			}
			return map[string]any{"amount": trimmed}, true, nil
		}
		return nil, false, nil
	case protoreflect.FloatKind, protoreflect.DoubleKind:
		if _, err := strconv.ParseFloat(trimmed, 64); err != nil {
			return nil, false, errInvalid("float_parsing", loc, "%s must be a number", key)
		}
		return json.Number(trimmed), true, nil
	case protoreflect.BytesKind:
		return trimmed, true, nil
	default:
		if _, err := strconv.ParseInt(trimmed, 10, 64); err != nil {
			return nil, false, errInvalid("int_parsing", loc, "%s must be a whole number", key)
		}
		return json.Number(trimmed), true, nil
	}
}

func bodyValue(field protoreflect.FieldDescriptor, value any, loc []string) (any, error) {
	switch {
	case field.IsMap():
		object, ok := value.(map[string]any)
		if !ok {
			return nil, typeError(loc, "object")
		}
		out := make(map[string]any, len(object))
		for key, entry := range object {
			converted, err := bodyScalar(field.MapValue(), entry, loc)
			if err != nil {
				return nil, err
			}
			out[key] = converted
		}
		return out, nil
	case field.IsList():
		list, ok := value.([]any)
		if !ok {
			return nil, typeError(loc, "list")
		}
		out := make([]any, 0, len(list))
		for _, entry := range list {
			converted, err := bodyScalar(field, entry, loc)
			if err != nil {
				return nil, err
			}
			out = append(out, converted)
		}
		return out, nil
	}
	return bodyScalar(field, value, loc)
}

func bodyScalar(field protoreflect.FieldDescriptor, value any, loc []string) (any, error) {
	if value == nil {
		return nil, nil
	}
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return bodyMessage(field.Message(), value, loc)
	case protoreflect.EnumKind:
		text, ok := value.(string)
		if !ok {
			return nil, typeError(loc, "string")
		}
		return enumFromREST(field.Enum(), text, loc)
	case protoreflect.StringKind, protoreflect.BytesKind:
		if _, ok := value.(string); !ok {
			return nil, typeError(loc, "string")
		}
	case protoreflect.BoolKind:
		if _, ok := value.(bool); !ok {
			return nil, typeError(loc, "bool")
		}
	default:
		if _, ok := value.(json.Number); !ok {
			return nil, typeError(loc, field.Kind().String())
		}
	}
	return value, nil
}

func bodyMessage(desc protoreflect.MessageDescriptor, value any, loc []string) (any, error) {
	switch {
	case isMoneyMessage(desc):
		text, ok := value.(string)
		if !ok {
			raw, _ := json.Marshal(value)
			return nil, errInvalid("string_type", loc, "%s",
				(&domain.AmountJSONError{Raw: raw, NotString: true}).Reason())
		}
		if _, err := domain.FromString(text); err != nil {
			return nil, errInvalid("decimal_parsing", loc, "%s", domain.NotAnAmount(text))
		}
		return map[string]any{"amount": text}, nil
	case desc.FullName() == idSetName:
		if _, ok := value.([]any); !ok {
			return nil, typeError(loc, "list")
		}
		return map[string]any{"ids": value}, nil
	case isWellKnown(desc):
		return value, nil
	}

	object, ok := value.(map[string]any)
	if !ok {
		return nil, typeError(loc, "object")
	}
	keys := make([]string, 0, len(object))
	for key := range object {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make(map[string]any, len(object))
	for _, key := range keys {
		at := append(append([]string{}, loc...), key)
		field := desc.Fields().ByName(protoreflect.Name(key))
		if field == nil {
			return nil, errInvalid("extra_forbidden", at, "%s is not a field on this request", key)
		}
		if object[key] == nil {
			continue
		}
		converted, err := bodyValue(field, object[key], at)
		if err != nil {
			return nil, err
		}
		out[key] = converted
	}
	return out, nil
}

func typeError(loc []string, want string) error {
	name := strings.Join(loc[1:], ".")
	return errInvalid("type_error", loc, "%s must be a %s", name, want)
}

// maskJSON is a FieldMask in protojson's form: lowerCamelCase paths joined
// by commas.
func maskJSON(paths []string) string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		var camel strings.Builder
		upper := false
		for _, r := range path {
			if r == '_' {
				upper = true
				continue
			}
			if upper {
				r = unicode.ToUpper(r)
				upper = false
			}
			camel.WriteRune(r)
		}
		out = append(out, camel.String())
	}
	return strings.Join(out, ",")
}

// --- The response ------------------------------------------------------------

func (b bridge) respond(w http.ResponseWriter, r *http.Request, data []byte) {
	status := int(b.proc.rest.GetStatus())
	if status == 0 {
		status = http.StatusOK
	}
	if status == http.StatusNoContent {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var raw any
	if err := decoder.Decode(&raw); err != nil {
		writeError(w, r, fmt.Errorf("api: %s answered unreadable JSON: %w", b.proc.name, err))
		return
	}
	output := b.proc.method.Output()
	value := restMessage(output, raw)
	if field := b.proc.rest.GetResponseBody(); field != "" {
		if object, ok := value.(restObject); ok {
			value = object.get(field)
			// A oneof is answered as whichever of its fields is set: a REST
			// route whose answer took one of several shapes.
			if oneof := output.Oneofs().ByName(protoreflect.Name(field)); oneof != nil {
				for i := range oneof.Fields().Len() {
					if set := object.get(string(oneof.Fields().Get(i).Name())); set != nil {
						value = set
					}
				}
			}
		}
	}
	writeJSONHeaders(w, status, nil, value)
}

// restObject is a message's fields in declaration order, which is the order
// the REST structs wrote them in.
type restObject []restField

type restField struct {
	name  string
	value any
}

func (o restObject) get(name string) any {
	for _, field := range o {
		if field.name == name {
			return field.value
		}
	}
	return nil
}

func (o restObject) MarshalJSON() ([]byte, error) {
	var out bytes.Buffer
	out.WriteByte('{')
	for i, field := range o {
		if i > 0 {
			out.WriteByte(',')
		}
		key, err := json.Marshal(field.name)
		if err != nil {
			return nil, err
		}
		value, err := json.Marshal(field.value)
		if err != nil {
			return nil, err
		}
		out.Write(key)
		out.WriteByte(':')
		out.Write(value)
	}
	out.WriteByte('}')
	return out.Bytes(), nil
}

func restMessage(desc protoreflect.MessageDescriptor, raw any) any {
	if raw == nil {
		return nil
	}
	if isMoneyMessage(desc) {
		if object, ok := raw.(map[string]any); ok {
			return object["amount"]
		}
		return raw
	}
	if isWellKnown(desc) {
		return raw
	}
	object, ok := raw.(map[string]any)
	if !ok {
		return raw
	}
	fields := desc.Fields()
	out := make(restObject, 0, fields.Len())
	for i := range fields.Len() {
		field := fields.Get(i)
		name := string(field.Name())
		// protojson leaves out an unset optional field, which sits in a
		// synthetic oneof, even when emitting unpopulated fields; the REST
		// wire wrote it as null.
		value := restValue(field, object[name])
		if isJSONText(field) {
			name = strings.TrimSuffix(name, jsonTextSuffix)
			if text, _ := value.(string); text != "" {
				value = json.RawMessage(text)
			} else {
				value = nil
			}
		}
		out = append(out, restField{name: name, value: value})
	}
	return out
}

func restValue(field protoreflect.FieldDescriptor, raw any) any {
	if raw == nil {
		return nil
	}
	switch {
	case field.IsList():
		list, _ := raw.([]any)
		out := make([]any, 0, len(list))
		for _, entry := range list {
			out = append(out, restScalar(field, entry))
		}
		return out
	case field.IsMap():
		object, _ := raw.(map[string]any)
		out := make(map[string]any, len(object))
		for key, entry := range object {
			out[key] = restScalar(field.MapValue(), entry)
		}
		return out
	}
	return restScalar(field, raw)
}

func restScalar(field protoreflect.FieldDescriptor, raw any) any {
	switch field.Kind() {
	case protoreflect.MessageKind, protoreflect.GroupKind:
		return restMessage(field.Message(), raw)
	case protoreflect.EnumKind:
		name, ok := raw.(string)
		if !ok {
			return raw
		}
		return enumToREST(field.Enum(), name)
	case protoreflect.Int64Kind, protoreflect.Sint64Kind, protoreflect.Sfixed64Kind,
		protoreflect.Uint64Kind, protoreflect.Fixed64Kind:
		if text, ok := raw.(string); ok {
			return json.Number(text)
		}
	}
	return raw
}

// --- Shared ------------------------------------------------------------------

var (
	nullableMoneyName = (&agentifiv1.NullableMoney{}).ProtoReflect().Descriptor().FullName()
	idSetName         = (&agentifiv1.IdSet{}).ProtoReflect().Descriptor().FullName()
)

// jsonTextSuffix names a string field that holds arbitrary JSON as text, which
// the REST wire carried as the JSON itself under the name without the suffix:
// options_json was "options": {...}.
const jsonTextSuffix = "_json"

func isJSONText(field protoreflect.FieldDescriptor) bool {
	return field.Kind() == protoreflect.StringKind && !field.IsList() && !field.IsMap() &&
		strings.HasSuffix(string(field.Name()), jsonTextSuffix)
}

func isMoneyMessage(desc protoreflect.MessageDescriptor) bool {
	return desc.FullName() == moneyName || desc.FullName() == nullableMoneyName
}

// isWellKnown is a google.protobuf type, whose JSON form (a timestamp string,
// arbitrary JSON) is already the REST one.
func isWellKnown(desc protoreflect.MessageDescriptor) bool {
	return desc.ParentFile().Package() == "google.protobuf"
}

// An enum crosses the REST wire as its value's lower-case suffix:
// ACCOUNT_KIND_CREDIT_CARD is "credit_card", and the zero value is null.
func enumPrefix(desc protoreflect.EnumDescriptor) string {
	var out strings.Builder
	for i, r := range string(desc.Name()) {
		if unicode.IsUpper(r) && i > 0 {
			out.WriteByte('_')
		}
		out.WriteRune(unicode.ToUpper(r))
	}
	out.WriteByte('_')
	return out.String()
}

func enumToREST(desc protoreflect.EnumDescriptor, name string) any {
	value := desc.Values().ByName(protoreflect.Name(name))
	if value == nil || value.Number() == 0 {
		return nil
	}
	return strings.ToLower(strings.TrimPrefix(name, enumPrefix(desc)))
}

func enumFromREST(desc protoreflect.EnumDescriptor, text string, loc []string) (any, error) {
	if text == "" {
		return nil, nil
	}
	name := enumPrefix(desc) + strings.ToUpper(text)
	if value := desc.Values().ByName(protoreflect.Name(name)); value == nil || value.Number() == 0 {
		return nil, errInvalid("enum", loc, "%s is not one of the values %s accepts", text, loc[len(loc)-1])
	}
	return name, nil
}
