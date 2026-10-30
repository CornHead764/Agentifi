package api

import (
	"net/http"
	"strings"

	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
)

// The date window every list endpoint takes, resolved in one place (trap 5).
// The register and the account summary share one screen and must describe the
// same rows, so both reach one resolver through WindowFromRequest and every
// response echoes the window it used. An omitted bound means unbounded.

// Window is a resolved, inclusive date range and which of a transaction's two
// dates it applies to: posted (what the register shows) or effective (what
// reports read). The bounds mean nothing without the mode.
type Window struct {
	From    domain.Date
	HasFrom bool
	To      domain.Date
	HasTo   bool
	Mode    domain.DateMode
}

// ResolveWindow is the one place a request's `from` and `to` become a window.
// An inverted range is refused rather than answering an empty register.
func ResolveWindow(from domain.Date, hasFrom bool, to domain.Date, hasTo bool, mode domain.DateMode) (Window, error) {
	if hasFrom && hasTo && from.After(to) {
		return Window{}, errInvalid("window_inverted", []string{"query", "from"},
			"window starts after it ends: %s .. %s", from, to)
	}
	return Window{From: from, HasFrom: hasFrom, To: to, HasTo: hasTo, Mode: mode}, nil
}

// resolveWindow is an indirection so window_test.go can spy on it and prove the
// register and the account summary use the same resolver.
var resolveWindow = ResolveWindow

// WindowFromRequest reads `from`, `to` and `date_field` off the query string.
// For a card charge the two dates can be a statement cycle apart, so which one
// a list filters on is never assumed.
func WindowFromRequest(r *http.Request) (Window, error) {
	mode, err := dateFieldFromRequest(r)
	if err != nil {
		return Window{}, err
	}
	return windowOn(r, mode)
}

// windowOn reads `from` and `to` for a list that only one of the two dates
// can mean, ignoring any `date_field`.
func windowOn(r *http.Request, mode domain.DateMode) (Window, error) {
	from, hasFrom, err := queryDate(r, "from")
	if err != nil {
		return Window{}, err
	}
	to, hasTo, err := queryDate(r, "to")
	if err != nil {
		return Window{}, err
	}
	return resolveWindow(from, hasFrom, to, hasTo, mode)
}

func dateFieldFromRequest(r *http.Request) (domain.DateMode, error) {
	switch raw := r.URL.Query().Get("date_field"); raw {
	case "", string(domain.DatePosted):
		return domain.DatePosted, nil
	case string(domain.DateEffective):
		return domain.DateEffective, nil
	default:
		return "", errInvalid("enum", []string{"query", "date_field"},
			"date_field must be posted or effective, got %q", raw)
	}
}

// DayBeforeStart is the last day outside the window, or false when it is open
// at that end. The opening balance is the balance at the end of this day;
// using the start would count the first day twice.
func (w Window) DayBeforeStart() (domain.Date, bool) {
	if !w.HasFrom {
		return domain.Date{}, false
	}
	return w.From.AddDays(-1), true
}

// Contains reports whether a date falls inside the window.
func (w Window) Contains(on domain.Date) bool {
	if w.HasFrom && on.Before(w.From) {
		return false
	}
	return !(w.HasTo && on.After(w.To))
}

// WindowResponse is the window a response was computed over, echoed so a
// client can check two responses used the same one.
type WindowResponse struct {
	From *Date `json:"from"`
	To   *Date `json:"to"`
	// DateField is `posted` or `effective`.
	DateField string `json:"date_field"`
}

func windowResponse(w Window) WindowResponse {
	out := WindowResponse{DateField: string(w.Mode)}
	if w.HasFrom {
		from := Date(w.From)
		out.From = &from
	}
	if w.HasTo {
		to := Date(w.To)
		out.To = &to
	}
	return out
}

// windowOf is a procedure's window: what WindowFromRequest is to a REST
// route, with the same refusals. Unset is open at both ends, on the posted
// date.
func windowOf(w *agentifiv1.Window) (Window, error) {
	var mode domain.DateMode
	switch raw := w.GetDateField(); raw {
	case "", string(domain.DatePosted):
		mode = domain.DatePosted
	case string(domain.DateEffective):
		mode = domain.DateEffective
	default:
		return Window{}, errInvalid("enum", []string{"window", "date_field"},
			"date_field must be posted or effective, got %q", raw)
	}
	bound := func(key, raw string) (domain.Date, bool, error) {
		if strings.TrimSpace(raw) == "" {
			return domain.Date{}, false, nil
		}
		parsed, err := parseDate(strings.TrimSpace(raw))
		if err != nil {
			return domain.Date{}, false, errInvalid("date_parsing", []string{"window", key}, "%s", err)
		}
		return parsed, true, nil
	}
	from, hasFrom, err := bound("from", w.GetFrom())
	if err != nil {
		return Window{}, err
	}
	to, hasTo, err := bound("to", w.GetTo())
	if err != nil {
		return Window{}, err
	}
	return resolveWindow(from, hasFrom, to, hasTo, mode)
}

// windowProto is the window a procedure's response echoes.
func windowProto(w Window) *agentifiv1.Window {
	out := &agentifiv1.Window{DateField: string(w.Mode)}
	if w.HasFrom {
		out.From = proto.String(w.From.String())
	}
	if w.HasTo {
		out.To = proto.String(w.To.String())
	}
	return out
}

// storeQuery is the window as internal/store wants it. A zero Date there means
// "no bound", which is the same convention.
func (w Window) storeQuery() (from, to domain.Date, mode domain.DateMode) {
	if w.HasFrom {
		from = w.From
	}
	if w.HasTo {
		to = w.To
	}
	return from, to, w.Mode
}
