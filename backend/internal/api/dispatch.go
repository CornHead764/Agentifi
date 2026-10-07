package api

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Calling this server's own API from inside it: how the assistant reaches the
// application. A tool issues GET /goals against the same handler a browser
// reaches, so a new resource is reachable the day it lands.
//
// It is not a hole in tenancy. Only `Read` and `Write` routes are mounted, with
// the caller's own resolved space, and a viewer is refused a write exactly as
// the router refuses one. Identity, administration and the assistant's own
// routes are unreachable.
//
// The middleware stack is deliberately absent: logging, CORS and rate limits
// belong to requests that crossed the network.

// dispatchDenied names the prefixes an in-process call may not reach: the
// assistant itself. A card applying another card is a model approving its own
// work, including cards a person left pending in another conversation.
var dispatchDenied = map[string]bool{
	"/assistant": true, "/assistant-actions": true, "/assistant-automations": true,
}

// dispatchDeniedRoutes names individual routes an in-process call may not
// reach, inside resources it otherwise can: those where the act is using a
// credential (bill and merchant sign-ins, challenges, kept passwords and
// sessions, the mailbox's sign-in and secret). None is a ledger change a person
// could review and undo.
//
// A route here covers everything under it, whatever the method.
var dispatchDeniedRoutes = []string{
	"/bills/connections/{connection_id}/sign-in",
	"/bills/connections/{connection_id}/session",
	// Not a credential, but it takes a lock off the profiles volume: the
	// owner's job, at the settings screen.
	"/bills/connections/{connection_id}/browser",
	"/bills/connections/{connection_id}/credential",
	"/bills/challenges/{challenge_id}/answer",
	"/email/connections/{connection_id}/sign-in",
	"/email/connections/{connection_id}/secret",
	// Drafting a rule is a question to the model, so from a conversation it is
	// the model asking itself.
	"/email/messages/{message_id}/suggest-rule",
	"/merchants/{merchant}/accounts/{id}/sign-in",
	"/merchants/{merchant}/accounts/{id}/credential",
	"/merchants/{merchant}/accounts/{id}/session",
	// Not a credential, but it opens the merchant's site once for every order
	// still without an invoice, far more than a pull: the person's call.
	"/merchants/{merchant}/accounts/{id}/backfill",
	// Not a credential either, but it fills a whole space from a file only the
	// person has, and nothing short of deleting the space undoes it.
	"/simplifi-import",
}

// deniedDispatchPath reports a route no in-process call may reach.
func deniedDispatchPath(path string) bool {
	for _, pattern := range dispatchDeniedRoutes {
		if underRoutePattern(pattern, path) {
			return true
		}
	}
	return false
}

// underRoutePattern reports whether path is that route or one below it, with a
// placeholder segment matching anything, so both the registry's pattern and a
// real path answer.
func underRoutePattern(pattern, path string) bool {
	want := strings.Split(strings.Trim(pattern, "/"), "/")
	got := strings.Split(strings.Trim(path, "/"), "/")
	if len(got) < len(want) {
		return false
	}
	for i, segment := range want {
		if strings.HasPrefix(segment, "{") {
			continue
		}
		if got[i] != segment {
			return false
		}
	}
	return true
}

type dispatchKeys struct{}

type dispatchState struct {
	env *Env
	sp  auth.SpaceContext
}

// dispatched reports whether the request is the assistant's in-process call
// rather than a person's.
func dispatched(ctx context.Context) bool {
	_, ok := ctx.Value(dispatchKeys{}).(dispatchState)
	return ok
}

var (
	dispatchOnce sync.Once
	dispatchMux  *chi.Mux
)

// dispatchRouter mounts every tenant-scoped route against the state carried in
// the request context. Built once; the registry is fixed after init.
func dispatchRouter() *chi.Mux {
	dispatchOnce.Do(func() {
		mux := chi.NewRouter()
		mux.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, errNotFound("Endpoint"))
		})
		mux.MethodNotAllowed(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, r, errBadRequest("%s is not a method that path accepts", r.Method))
		})

		for _, res := range registered() {
			// The loops below filter on kind anyway; skipping the resource
			// keeps "not mounted" and "not listed" from drifting.
			if dispatchDenied[res.Prefix] || adminPrefixes[res.Prefix] {
				continue
			}
			resource := res
			mux.Route(resource.Prefix, func(sub chi.Router) {
				for _, one := range resource.Routes {
					route := one
					if route.kind != kindRead && route.kind != kindWrite {
						continue
					}
					if deniedDispatchPath(route.Path()) {
						continue
					}
					sub.Method(route.Method, route.Pattern, dispatchHandler(route))
				}
			})
		}
		dispatchMux = mux
	})
	return dispatchMux
}

func dispatchHandler(route Route) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		state, ok := r.Context().Value(dispatchKeys{}).(dispatchState)
		if !ok {
			writeError(w, r, fmt.Errorf("api: an in-process call carried no caller"))
			return
		}
		if route.kind == kindWrite {
			if err := state.sp.RequireWrite(); err != nil {
				writeError(w, r, err)
				return
			}
		}
		if err := route.space(state.env, w, r, state.sp); err != nil {
			writeError(w, r, err)
		}
	}
}

// recorder is the ResponseWriter an in-process call writes into. Bounded,
// because a tool result goes into a model's context; the reader is told the
// body was cut rather than handed truncated JSON.
type recorder struct {
	header http.Header
	body   bytes.Buffer
	status int
	// truncated is set when the handler wrote past the cap.
	truncated bool
}

// dispatchBodyLimit is how much of a response a tool may see.
const dispatchBodyLimit = 200 << 10

func newRecorder() *recorder {
	return &recorder{header: http.Header{}, status: http.StatusOK}
}

func (rec *recorder) Header() http.Header { return rec.header }

func (rec *recorder) WriteHeader(status int) { rec.status = status }

func (rec *recorder) Write(p []byte) (int, error) {
	room := dispatchBodyLimit - rec.body.Len()
	if room <= 0 {
		rec.truncated = true
		return len(p), nil
	}
	if len(p) > room {
		rec.body.Write(p[:room])
		rec.truncated = true
		return len(p), nil
	}
	return rec.body.Write(p)
}

// dispatchResponse is what one in-process call answered.
type dispatchResponse struct {
	Status int
	Body   string
	// ContentType matters because not every endpoint answers JSON: an
	// attachment answers its bytes and an export answers CSV.
	ContentType string
	Truncated   bool
}

// IsJSON reports a response a tool may hand to a model.
func (d dispatchResponse) IsJSON() bool {
	return strings.Contains(strings.ToLower(d.ContentType), "json")
}

// OK reports a 2xx, which is the only thing a caller should treat as success.
func (d dispatchResponse) OK() bool { return d.Status >= 200 && d.Status < 300 }

// dispatch issues one request against this server's own routes. `path` is
// under the API mount point, with real ids in place of placeholders.
func (e *Env) dispatch(
	ctx context.Context, sp auth.SpaceContext,
	method, path string, query url.Values, body []byte,
) (dispatchResponse, error) {
	path = normalizeDispatchPath(path)
	if path == "" {
		return dispatchResponse{}, fmt.Errorf("a path is required, for example /accounts")
	}
	if err := refuseDeniedPath(path); err != nil {
		return dispatchResponse{}, err
	}

	target := path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}
	var reader *bytes.Reader
	if len(body) > 0 {
		reader = bytes.NewReader(body)
	} else {
		reader = bytes.NewReader(nil)
	}
	// The chi route context has to go. chi's Mux reuses a route context it
	// finds rather than taking one from its pool, and the outer request's
	// holds its consumed path, so every dispatched request would 404. A typed
	// nil is what chi tests for.
	inner := context.WithValue(ctx, chi.RouteCtxKey, (*chi.Context)(nil))
	request, err := http.NewRequestWithContext(
		context.WithValue(inner, dispatchKeys{}, dispatchState{env: e, sp: sp}),
		strings.ToUpper(method), target, reader)
	if err != nil {
		return dispatchResponse{}, fmt.Errorf("%s %s is not a usable request: %w",
			method, path, err)
	}
	request.Header.Set("Content-Type", "application/json")

	rec := newRecorder()
	dispatchRouter().ServeHTTP(rec, request)
	return dispatchResponse{
		Status: rec.status, Body: rec.body.String(), Truncated: rec.truncated,
		ContentType: rec.header.Get("Content-Type"),
	}, nil
}

// normalizeDispatchPath forgives the shapes a model will produce: an "/api"
// prefix, a whole URL, or an attached query string.
func normalizeDispatchPath(path string) string {
	path = strings.TrimSpace(path)
	if path == "" {
		return ""
	}
	if parsed, err := url.Parse(path); err == nil && parsed.Host != "" {
		path = parsed.Path
	}
	path = browser.WithoutQuery(path)
	path = strings.TrimPrefix(path, "/api")
	if !strings.HasPrefix(path, "/") {
		path = "/" + path
	}
	if len(path) > 1 {
		path = strings.TrimSuffix(path, "/")
	}
	return path
}

// refuseDeniedPath reports a path an in-process call may not reach. Checked
// here and again at proposal time, so the refusal reaches the model while it
// can still act.
func refuseDeniedPath(path string) error {
	for prefix := range adminPrefixes {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return fmt.Errorf(
				"%s administers the server and is not reachable from here", prefix)
		}
	}
	for prefix := range dispatchDenied {
		if path == prefix || strings.HasPrefix(path, prefix+"/") {
			return fmt.Errorf(
				"%s is the assistant's own machinery and is not reachable from here", prefix)
		}
	}
	if deniedDispatchPath(path) {
		return humanOnlyRefusal(path)
	}
	return nil
}

// humanOnlyRefusal is the answer for a route a person has to take themself,
// with a Markdown link to the control for the model to hand on. Drafting a mail
// rule is denied for a different reason, and the model can do that job
// directly.
func humanOnlyRefusal(path string) error {
	if underRoutePattern("/email/messages/{message_id}/suggest-rule", path) {
		return fmt.Errorf("%s asks the model to draft a rule, which from here is asking "+
			"yourself. Read the message with read_endpoint and propose the rule with "+
			"change_endpoint POST /email/rules instead", path)
	}
	if underRoutePattern("/simplifi-import", path) {
		return fmt.Errorf("%s fills this space from a Simplifi export the person uploads "+
			"themself, so it is not reachable from here. Give them this link, which opens it: "+
			"[Import from Simplifi](/settings/accounts)", path)
	}
	if underRoutePattern("/merchants/{merchant}/accounts/{id}/backfill", path) {
		label, link := humanOnlyLink(path)
		return fmt.Errorf("%s opens the merchant's site once for every order still without an "+
			"invoice, so the person starts it themself and it is not reachable from here. Give "+
			"them this link, which opens it: [%s](%s)", path, label, link)
	}
	label, link := humanOnlyLink(path)
	return fmt.Errorf("%s uses a sign-in or a credential, so the person does it themself "+
		"and it is not reachable from here. Give them this link, which opens it: [%s](%s)",
		path, label, link)
}

// humanOnlyLink is the page, and where possible the control, for one
// human-only route. An id that is not an id is left out of the link.
func humanOnlyLink(path string) (string, string) {
	segments := strings.Split(strings.Trim(path, "/"), "/")
	at := func(i int) string {
		if i < len(segments) {
			return segments[i]
		}
		return ""
	}
	withID := func(page, param, id string) string {
		if _, err := uuid.Parse(id); err != nil {
			return page
		}
		return page + "?" + param + "=" + id
	}
	switch {
	case at(0) == "bills" && at(1) == "challenges":
		return "Answer the sign-in code", withID("/settings/bills", "challenge", at(2))
	case at(0) == "bills" && (at(3) == "sign-in" || at(3) == "credential"):
		return "Sign in to the bill provider", withID("/settings/bills", "sign-in", at(2))
	case at(0) == "bills":
		return "Open Bill providers", "/settings/bills"
	case at(0) == "email" && at(3) == "sign-in":
		return "Sign in to the mailbox", withID("/settings/email", "sign-in", at(2))
	case at(0) == "email":
		return "Open Email", "/settings/email"
	case at(0) == "merchants":
		merchant, known := domain.MerchantByID(domain.MerchantID(at(1)))
		if !known {
			return "Open Merchants", "/settings/merchants"
		}
		if at(4) == "sign-in" || at(4) == "credential" {
			return "Sign in to " + merchant.Name, withID(merchant.SettingsPath, "sign-in", at(3))
		}
		if at(4) == "backfill" {
			return "Backfill invoices at " + merchant.Name, withID(merchant.SettingsPath, "backfill", at(3))
		}
		return "Open " + merchant.Name, merchant.SettingsPath
	}
	return "Open Settings", "/settings"
}

// dispatchableRoutes is every route an in-process call can reach, sorted, read
// from the registry so what the assistant is told and what it can reach agree.
func dispatchableRoutes() []Route {
	var out []Route
	for _, resource := range registered() {
		if dispatchDenied[resource.Prefix] || adminPrefixes[resource.Prefix] {
			continue
		}
		for _, route := range resource.Routes {
			if deniedDispatchPath(route.Path()) {
				continue
			}
			if route.kind == kindRead || route.kind == kindWrite {
				out = append(out, route)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path() != out[j].Path() {
			return out[i].Path() < out[j].Path()
		}
		return out[i].Method < out[j].Method
	})
	return out
}
