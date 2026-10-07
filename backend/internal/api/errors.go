package api

import (
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

// factsWriter adds fields beside the detail of the error body written
// through it.
type factsWriter struct {
	http.ResponseWriter
	facts map[string]any
}

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

// writeError maps an error to a status and a body. Anything unmapped is a 500
// whose detail says nothing but the request id, so the log line can be found.
func writeError(w http.ResponseWriter, r *http.Request, err error) {
	var (
		schema  *invalid
		clash   *conflict
		missing *notFound
		bad     *badRequest
		remote  *upstream
		origin  *auth.OriginError
	)

	var kept screenshotKept
	if errors.As(err, &kept) {
		w = factsWriter{ResponseWriter: w, facts: map[string]any{"has_failure_screenshot": true}}
	}

	switch {
	// The caller went away, which the register does on every click. Keyed on
	// the request's context rather than the error, because cancellation
	// surfaces differently by layer (the token lookup turns it into a 401).
	// r.Context() is cancelled only by the client hanging up or the server
	// shutting down, so this cannot swallow a real fault.
	case r.Context().Err() != nil:
		w.WriteHeader(clientClosedRequest)
		return

	case errors.As(err, &schema):
		writeJSONHeaders(w, http.StatusUnprocessableEntity, nil, map[string]any{
			"detail": []map[string]any{{
				"loc":  schema.location(),
				"msg":  schema.Msg,
				"type": schema.Kind,
			}},
		})

	case errors.As(err, &missing):
		writeDetail(w, http.StatusNotFound, missing.Error())
	case errors.Is(err, store.ErrNotFound):
		writeDetail(w, http.StatusNotFound, "Not found")
	case errors.Is(err, auth.ErrNoSpace):
		writeDetail(w, http.StatusNotFound, "Space not found")

	case errors.As(err, &clash) && clash.Code != "":
		writeJSONHeaders(w, http.StatusConflict, nil, map[string]any{
			"detail": clash.Message, "code": clash.Code,
		})
	case errors.As(err, &clash):
		writeDetail(w, http.StatusConflict, clash.Message)
	case errors.Is(err, service.ErrAssistantUnavailable):
		writeJSONHeaders(w, http.StatusConflict, nil, map[string]any{
			"detail": "The assistant is not set up, or it is switched off",
			"code":   service.AssistantUnavailableCode,
		})

	// Plain service errors that are a request the ledger will not accept, the
	// same 409 as a conflict.
	case errors.Is(err, service.ErrMonthClosedOut):
		writeDetail(w, http.StatusConflict, err.Error())
	case errors.Is(err, service.ErrInvalidFilter):
		writeDetail(w, http.StatusConflict, err.Error())

	case errors.As(err, &remote):
		writeDetail(w, http.StatusBadGateway, remote.Message)
	case errors.Is(err, service.ErrValuationNeedsCamoufox):
		writeDetail(w, http.StatusServiceUnavailable, service.ErrValuationNeedsCamoufox.Error())

	case errors.Is(err, auth.ErrReadOnly):
		writeDetail(w, http.StatusForbidden, "This space is read-only for you")
	case errors.Is(err, auth.ErrInactiveUser):
		writeDetail(w, http.StatusForbidden, "This account is inactive")
	case errors.Is(err, errPasswordChangeRequired):
		// The code is the contract: the client routes on it rather than
		// pattern-matching the sentence, which is free to change.
		writeJSONHeaders(w, http.StatusForbidden, nil, map[string]any{
			"detail": "Set a new password before continuing",
			"code":   "password_change_required",
		})
	case errors.Is(err, errOIDCRefused):
		writeDetail(w, http.StatusForbidden, "OIDC login is not available for this account")
	case errors.Is(err, errNotAdministrator):
		writeDetail(w, http.StatusForbidden, "This is for the server's administrator")

	case errors.Is(err, auth.ErrInvalidCredentials):
		writeUnauthorized(w, "Could not validate credentials")
	case errors.Is(err, auth.ErrInvalidPasskey):
		writeUnauthorized(w, "Invalid passkey")

	case errors.Is(err, auth.ErrTooManyAttempts):
		writeDetail(w, http.StatusTooManyRequests, "Too many attempts. Try again later.")

	case errors.Is(err, auth.ErrInvalidCode):
		writeDetail(w, http.StatusBadRequest, "Invalid verification code")
	case errors.Is(err, auth.ErrInvalidChallenge):
		writeDetail(w, http.StatusBadRequest, "Invalid or expired challenge")
	case errors.Is(err, auth.ErrPasskeyRegistered):
		writeDetail(w, http.StatusBadRequest, "This passkey is already registered")
	case errors.Is(err, auth.ErrNoPendingEnrolment):
		writeDetail(w, http.StatusBadRequest, "Start the enrolment first")
	case errors.As(err, &origin):
		// The code, not just the message: the frontend switches on it to say
		// which of HTTPS, a domain name or WEBAUTHN_RP_ID needs changing.
		writeJSONHeaders(w, http.StatusBadRequest, nil, map[string]any{
			"detail": origin.Message,
			"code":   origin.Code,
		})
	case errors.As(err, &bad):
		writeDetail(w, http.StatusBadRequest, bad.Message)

	case errors.Is(err, auth.ErrOIDCDisabled):
		writeDetail(w, http.StatusNotFound, "OIDC login is not enabled")
	case errors.Is(err, auth.ErrOIDCLogin):
		writeDetail(w, http.StatusForbidden, "OIDC login could not be completed")
	case errors.Is(err, auth.ErrOIDCUnreachable):
		writeDetail(w, http.StatusBadGateway, "The OIDC provider could not be read")

	default:
		id := middleware.GetReqID(r.Context())
		slog.Error("unhandled request error",
			"method", r.Method, "path", r.URL.Path, "request_id", id, "error", err)
		writeDetail(w, http.StatusInternalServerError, "Internal server error (request "+id+")")
	}
}

// location is pydantic's `loc`, which names the part of the request at fault.
func (e *invalid) location() []string {
	if len(e.Loc) == 0 {
		return []string{"body"}
	}
	return e.Loc
}

func writeDetail(w http.ResponseWriter, status int, detail string) {
	writeJSONHeaders(w, status, nil, map[string]any{"detail": detail})
}

func writeUnauthorized(w http.ResponseWriter, detail string) {
	writeJSONHeaders(w, http.StatusUnauthorized,
		map[string]string{"WWW-Authenticate": "Bearer"},
		map[string]any{"detail": detail})
}
