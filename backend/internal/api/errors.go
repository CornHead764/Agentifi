package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5/middleware"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Turning a refusal into a status code, in one place. No handler writes a
// status for a failure.
//
// The body is `{"detail": "..."}` everywhere except 422, where `detail` is the
// list of field errors in pydantic's shape, which the client reads.

// notFound is a row that is missing or is in another space. The same answer on
// purpose: a 403 for the second would be an id oracle.
type notFound struct{ What string }

func (e *notFound) Error() string { return e.What + " not found" }

func errNotFound(what string) error { return &notFound{What: what} }

// notFoundAs names the resource a lookup missed and passes any other error,
// nil included, through unchanged.
func notFoundAs(err error, what string) error {
	if isNotFound(err) {
		return errNotFound(what)
	}
	return err
}

// conflict is a write the ledger will not accept: splits that do not sum, a
// tag from another space, a renamed system category. 409, not 422, so a
// refused request stays distinguishable from a malformed one. Code, when set,
// is written beside the detail for a client that offers the way out.
type conflict struct {
	Message string
	Code    string
}

func (e *conflict) Error() string { return e.Message }

func errConflict(format string, args ...any) error {
	return &conflict{Message: fmt.Sprintf(format, args...)}
}

func errConflictCode(code, format string, args ...any) error {
	return &conflict{Message: fmt.Sprintf(format, args...), Code: code}
}

// upstream is something outside this app refusing or failing, such as the model
// provider: a 502 carrying the provider's own words.
type upstream struct{ Message string }

func (e *upstream) Error() string { return e.Message }

func errBadGateway(format string, args ...any) error {
	return &upstream{Message: fmt.Sprintf(format, args...)}
}

// invalid is a request the schema will not accept: an unknown field, a money
// value sent as a JSON number, an inverted date window.
type invalid struct {
	Loc  []string
	Msg  string
	Kind string
}

func (e *invalid) Error() string { return e.Msg }

func errInvalid(kind string, loc []string, format string, args ...any) error {
	return &invalid{Loc: loc, Msg: fmt.Sprintf(format, args...), Kind: kind}
}

// badRequest is a ceremony that cannot proceed, such as an expired challenge.
// Distinct from invalid because the request was well-formed and from conflict
// because nothing was being written.
type badRequest struct{ Message string }

func (e *badRequest) Error() string { return e.Message }

func errBadRequest(format string, args ...any) error {
	return &badRequest{Message: fmt.Sprintf(format, args...)}
}

// screenshotKept is a connector failure that left the page it stopped on for
// the failure-screenshot route. Its body carries has_failure_screenshot beside
// the detail, so the toast that reports the failure offers the page as the
// account's card does.
type screenshotKept struct{ error }

func (e screenshotKept) Unwrap() error { return e.error }

// isNotFound covers this package's sentinel and internal/store's, so a caller
// that mixes the two has one thing to check.
func isNotFound(err error) bool {
	var missing *notFound
	return errors.As(err, &missing) || errors.Is(err, store.ErrNotFound)
}

func isConflict(err error) bool {
	var refused *conflict
	return errors.As(err, &refused)
}

// clientClosedRequest is nginx's 499: the client hung up before the response.
const clientClosedRequest = 499

// problem is one refusal, decided once and then written either as a REST body
// (writeProblem) or as a Connect error (connectError).
type problem struct {
	status int
	detail string
	// code is the machine-readable reason a client routes on, beside the
	// sentence, which is free to change.
	code string
	// fields is set for a 422 only, whose detail is pydantic's list of field
	// errors.
	fields     []fieldError
	screenshot bool
	// unhandled is an error nothing here names: a 500 whose detail says only
	// the request id, so the log line can be found.
	unhandled bool
}

type fieldError struct {
	Loc  []string `json:"loc"`
	Msg  string   `json:"msg"`
	Type string   `json:"type"`
}

// writeError maps an error to a status and a body.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	p := classify(r.Context(), err)
	if p.unhandled {
		logUnhandled(r.Context(), r.Method, r.URL.Path, err)
	}
	writeProblem(w, p)
}

func logUnhandled(ctx context.Context, method, path string, err error) {
	slog.Error("unhandled request error", "method", method, "path", path,
		"request_id", middleware.GetReqID(ctx), "error", err)
}

func writeProblem(w http.ResponseWriter, p problem) {
	if p.status == clientClosedRequest {
		w.WriteHeader(clientClosedRequest)
		return
	}
	var headers map[string]string
	if p.status == http.StatusUnauthorized {
		headers = map[string]string{"WWW-Authenticate": "Bearer"}
	}
	writeJSONHeaders(w, p.status, headers, p.body())
}

// body is the REST error body: `{"detail": "..."}`, with the list of field
// errors as the detail on a 422.
func (p problem) body() map[string]any {
	body := map[string]any{"detail": p.detail}
	if len(p.fields) > 0 {
		body["detail"] = p.fields
	}
	if p.code != "" {
		body["code"] = p.code
	}
	if p.screenshot {
		body["has_failure_screenshot"] = true
	}
	return body
}

// classify is the one mapping from an error to what the caller is told.
func classify(ctx context.Context, err error) problem {
	p := classifyStatus(ctx, err)
	var kept screenshotKept
	p.screenshot = errors.As(err, &kept)
	return p
}

func classifyStatus(ctx context.Context, err error) problem {
	var (
		schema  *invalid
		clash   *conflict
		missing *notFound
		bad     *badRequest
		remote  *upstream
		origin  *auth.OriginError
	)
	switch {
	// The caller went away, which the register does on every click. Keyed on
	// the request's context rather than the error, because cancellation
	// surfaces differently by layer (the token lookup turns it into a 401).
	// The request's context is cancelled only by the client hanging up or the
	// server shutting down, so this cannot swallow a real fault.
	case ctx.Err() != nil:
		return problem{status: clientClosedRequest, detail: "The request was cancelled"}

	case errors.As(err, &schema):
		return problem{
			status: http.StatusUnprocessableEntity, detail: schema.Msg,
			fields: []fieldError{{Loc: schema.location(), Msg: schema.Msg, Type: schema.Kind}},
		}

	case errors.As(err, &missing):
		return detail(http.StatusNotFound, missing.Error())
	case errors.Is(err, store.ErrNotFound):
		return detail(http.StatusNotFound, "Not found")
	case errors.Is(err, auth.ErrNoSpace):
		// The code is what a client keys on to forget the space it asked for;
		// any other 404 must not drop the choice.
		return problem{status: http.StatusNotFound, detail: "Space not found", code: "space_not_found"}

	case errors.As(err, &clash):
		return problem{status: http.StatusConflict, detail: clash.Message, code: clash.Code}
	case errors.Is(err, service.ErrAssistantUnavailable):
		return problem{
			status: http.StatusConflict, detail: "The assistant is not set up, or it is switched off",
			code: service.AssistantUnavailableCode,
		}

	// Plain service errors that are a request the ledger will not accept, the
	// same 409 as a conflict.
	case errors.Is(err, service.ErrMonthClosedOut):
		return detail(http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrInvalidFilter):
		return detail(http.StatusConflict, err.Error())

	case errors.As(err, &remote):
		return detail(http.StatusBadGateway, remote.Message)
	case errors.Is(err, service.ErrValuationNeedsCamoufox):
		return detail(http.StatusServiceUnavailable, service.ErrValuationNeedsCamoufox.Error())

	case errors.Is(err, auth.ErrReadOnly):
		return detail(http.StatusForbidden, "This space is read-only for you")
	case errors.Is(err, auth.ErrInactiveUser):
		return detail(http.StatusForbidden, "This account is inactive")
	case errors.Is(err, errPasswordChangeRequired):
		// The code is the contract: the client routes on it rather than
		// pattern-matching the sentence, which is free to change.
		return problem{
			status: http.StatusForbidden, detail: "Set a new password before continuing",
			code: "password_change_required",
		}
	case errors.Is(err, errOIDCRefused):
		return detail(http.StatusForbidden, "OIDC login is not available for this account")
	case errors.Is(err, errNotAdministrator):
		return detail(http.StatusForbidden, "This is for the server's administrator")

	case errors.Is(err, auth.ErrInvalidCredentials):
		return detail(http.StatusUnauthorized, "Could not validate credentials")
	case errors.Is(err, auth.ErrInvalidPasskey):
		return detail(http.StatusUnauthorized, "Invalid passkey")

	case errors.Is(err, auth.ErrTooManyAttempts):
		return detail(http.StatusTooManyRequests, "Too many attempts. Try again later.")

	case errors.Is(err, auth.ErrInvalidCode):
		return detail(http.StatusBadRequest, "Invalid verification code")
	case errors.Is(err, auth.ErrInvalidChallenge):
		return detail(http.StatusBadRequest, "Invalid or expired challenge")
	case errors.Is(err, auth.ErrPasskeyRegistered):
		return detail(http.StatusBadRequest, "This passkey is already registered")
	case errors.Is(err, auth.ErrNoPendingEnrolment):
		return detail(http.StatusBadRequest, "Start the enrolment first")
	case errors.As(err, &origin):
		// The code, not just the message: the frontend switches on it to say
		// which of HTTPS, a domain name or WEBAUTHN_RP_ID needs changing.
		return problem{status: http.StatusBadRequest, detail: origin.Message, code: origin.Code}
	case errors.As(err, &bad):
		return detail(http.StatusBadRequest, bad.Message)

	case errors.Is(err, auth.ErrOIDCDisabled):
		return detail(http.StatusNotFound, "OIDC login is not enabled")
	case errors.Is(err, auth.ErrOIDCLogin):
		return detail(http.StatusForbidden, "OIDC login could not be completed")
	case errors.Is(err, auth.ErrOIDCUnreachable):
		return detail(http.StatusBadGateway, "The OIDC provider could not be read")

	default:
		id := middleware.GetReqID(ctx)
		return problem{
			status: http.StatusInternalServerError, detail: "Internal server error (request " + id + ")",
			unhandled: true,
		}
	}
}

func detail(status int, sentence string) problem {
	return problem{status: status, detail: sentence}
}

// location is pydantic's `loc`, which names the part of the request at fault.
func (e *invalid) location() []string {
	if len(e.Loc) == 0 {
		return []string{"body"}
	}
	return e.Loc
}
