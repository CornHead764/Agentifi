package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"maps"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The wire, in both directions.
//
// Money is a decimal string, never a JSON number, which every client parses
// as a float. A date is a calendar day ("2026-08-05"), never a timestamp,
// whose month would depend on the reader's zone.

// maxBodyBytes caps a request body, far above the largest legitimate body (a
// filter with its items or a transaction with its splits).
const maxBodyBytes = 1 << 20

func writeJSON(w http.ResponseWriter, status int, body any) error {
	writeJSONHeaders(w, status, nil, body)
	return nil
}

func writeJSONHeaders(w http.ResponseWriter, status int, headers map[string]string, body any) {
	if extra, ok := w.(factsWriter); ok {
		if fields, ok := body.(map[string]any); ok {
			maps.Copy(fields, extra.facts)
		}
	}
	for name, value := range headers {
		w.Header().Set(name, value)
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if body != nil {
		_ = json.NewEncoder(w).Encode(body)
	}
}

func writeNoContent(w http.ResponseWriter) error {
	w.WriteHeader(http.StatusNoContent)
	return nil
}

// decodeBody reads a JSON request body into target, refusing unknown fields so
// a client is told rather than believing a dropped field landed. Every refusal
// is a 422 carrying the field at fault.
func decodeBody(r *http.Request, target any) error {
	var read bytes.Buffer
	decoder := json.NewDecoder(io.TeeReader(http.MaxBytesReader(nil, r.Body, maxBodyBytes), &read))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return decodeError(err, read.Bytes())
	}
	return nil
}

// decodeError turns a decode failure into a 422. body is what the decoder
// read, which names the field of an error encoding/json reports without one.
func decodeError(err error, body []byte) error {
	if errors.Is(err, io.EOF) {
		return errInvalid("missing", []string{"body"}, "a request body is required")
	}

	var amount *domain.AmountJSONError
	if errors.As(err, &amount) {
		loc := locateJSONValue(body, amount.Raw)
		if amount.NotString {
			return errInvalid("string_type", loc, "%s", amount.Reason())
		}
		return errInvalid("decimal_parsing", loc, "%s", amount.Reason())
	}

	message := err.Error()
	if field, found := strings.CutPrefix(message, `json: unknown field `); found {
		name := strings.Trim(field, `"`)
		return errInvalid("extra_forbidden", []string{"body", name},
			"%s is not a field on this request", name)
	}
	var mismatch *json.UnmarshalTypeError
	if errors.As(err, &mismatch) && mismatch.Field != "" {
		return errInvalid("type_error", []string{"body", mismatch.Field},
			"%s must be a %s", mismatch.Field, mismatch.Type)
	}
	return errInvalid("json_invalid", []string{"body"}, "the request body is not valid JSON: %s", message)
}

// Opt is one field of a patch body: absent, explicitly null, or a value. A
// PATCH omitting `notes` must leave it alone while `null` clears it, which a
// plain pointer cannot tell apart.
type Opt[T any] struct {
	Set   bool
	Null  bool
	Value T
}

// UnmarshalJSON records that the field was present. encoding/json calls this
// for a JSON null too, which is the only reason the distinction survives.
func (o *Opt[T]) UnmarshalJSON(data []byte) error {
	o.Set = true
	if string(data) == "null" {
		o.Null = true
		return nil
	}
	return json.Unmarshal(data, &o.Value)
}

// Present reports a field that was sent with a value.
func (o Opt[T]) Present() bool { return o.Set && !o.Null }

// Cleared reports a field that was sent as null.
func (o Opt[T]) Cleared() bool { return o.Set && o.Null }

// Date is a calendar day on the wire: "2026-08-05".
type Date domain.Date

func (d Date) MarshalJSON() ([]byte, error) {
	return json.Marshal(domain.Date(d).String())
}

func (d *Date) UnmarshalJSON(data []byte) error {
	var raw string
	if err := json.Unmarshal(data, &raw); err != nil {
		return fmt.Errorf("expected a date as \"YYYY-MM-DD\", got %s", data)
	}
	parsed, err := parseDate(raw)
	if err != nil {
		return err
	}
	*d = Date(parsed)
	return nil
}

func parseDate(raw string) (domain.Date, error) {
	parsed, err := time.Parse(time.DateOnly, raw)
	if err != nil {
		return domain.Date{}, fmt.Errorf("%q is not a date as \"YYYY-MM-DD\"", raw)
	}
	return domain.DateOf(parsed), nil
}

func parseMonth(raw string) (domain.Month, error) {
	parsed, ok := domain.ParseMonth(raw)
	if !ok {
		return domain.Month{}, fmt.Errorf("%q is not a month as \"YYYY-MM\"", raw)
	}
	return parsed, nil
}

// errOutOfRange marks a whole number outside the bounds it was read against,
// as against one that was not a whole number at all.
var errOutOfRange = errors.New("out of range")

func parseBoundedInt(raw string, low, high int) (int, error) {
	value, err := strconv.Atoi(raw)
	if err != nil {
		return 0, fmt.Errorf("%q is not a whole number", raw)
	}
	if value < low || value > high {
		return 0, fmt.Errorf("%w: %d is not between %d and %d", errOutOfRange, value, low, high)
	}
	return value, nil
}

func parseMoney(raw string) (domain.Money, error) {
	amount, err := domain.FromString(raw)
	if err != nil {
		return domain.Zero, errors.New(domain.NotAnAmount(raw))
	}
	return amount, nil
}

// nullableDate renders the zero date as JSON null. A zero effective date means
// "same as the posted date".
func nullableDate(d domain.Date) *Date {
	if d.IsZero() {
		return nil
	}
	wire := Date(d)
	return &wire
}

// --- Query parameters --------------------------------------------------------

func pathUUID(r *http.Request, name, what string) (uuid.UUID, error) {
	id, err := uuid.Parse(chi.URLParam(r, name))
	if err != nil {
		// Not a 422: an unparseable id names no row this caller may see, and a
		// validation error would be one more thing to tell apart from a 404.
		return uuid.Nil, errNotFound(what)
	}
	return id, nil
}

// fromPath reads the row the path's id names in the caller's space. A
// malformed id and a row in another space are the same 404.
func fromPath[T any](
	r *http.Request, sp auth.SpaceContext, name, what string,
	get func(context.Context, store.SpaceID, uuid.UUID) (T, error),
) (T, error) {
	var none T
	id, err := pathUUID(r, name, what)
	if err != nil {
		return none, err
	}
	row, err := get(r.Context(), sp.ID(), id)
	if err != nil {
		return none, notFoundAs(err, what)
	}
	return row, nil
}

func queryUUID(r *http.Request, key string) (uuid.UUID, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return uuid.Nil, false, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, false, errInvalid("uuid_parsing", []string{"query", key},
			"%s must be a uuid", key)
	}
	return id, true, nil
}

// queryUUIDs reads a repeated parameter (?account_id=a&account_id=b). given
// reports whether the parameter was sent at all: sent with no ids asks for
// nothing, not sent asks for everything, and every caller has to decide.
func queryUUIDs(r *http.Request, key string) (ids []uuid.UUID, given bool, err error) {
	values := r.URL.Query()[key]
	for _, raw := range values {
		for _, part := range strings.Split(raw, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := uuid.Parse(part)
			if err != nil {
				return nil, false, errInvalid("uuid_parsing", []string{"query", key},
					"%s must be a uuid", key)
			}
			ids = append(ids, id)
		}
	}
	return ids, len(values) > 0, nil
}

func queryBool(r *http.Request, key string) (bool, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return false, false, nil
	}
	value, err := strconv.ParseBool(raw)
	if err != nil {
		return false, false, errInvalid("bool_parsing", []string{"query", key},
			"%s must be true or false", key)
	}
	return value, true, nil
}

func queryInt(r *http.Request, key string, fallback, low, high int) (int, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := parseBoundedInt(raw, low, high)
	if errors.Is(err, errOutOfRange) {
		return 0, errInvalid("out_of_range", []string{"query", key},
			"%s must be between %d and %d", key, low, high)
	}
	if err != nil {
		return 0, errInvalid("int_parsing", []string{"query", key}, "%s must be a whole number", key)
	}
	return value, nil
}

// queryLimit is the `limit` parameter: fallback when absent, refused outside
// 1..ceiling rather than clamped.
func queryLimit(r *http.Request, fallback, ceiling int) (int, error) {
	return queryInt(r, "limit", fallback, 1, ceiling)
}

func queryDate(r *http.Request, key string) (domain.Date, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return domain.Date{}, false, nil
	}
	parsed, err := parseDate(raw)
	if err != nil {
		return domain.Date{}, false, errInvalid("date_parsing", []string{"query", key}, "%s", err)
	}
	return parsed, true, nil
}

func queryMonth(r *http.Request, key string) (domain.Month, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return domain.Month{}, false, nil
	}
	parsed, err := parseMonth(raw)
	if err != nil {
		return domain.Month{}, false, errInvalid("date_parsing", []string{"query", key}, "%s", err)
	}
	return parsed, true, nil
}

func queryMoney(r *http.Request, key string) (domain.Money, bool, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return domain.Zero, false, nil
	}
	amount, err := parseMoney(raw)
	if err != nil {
		return domain.Zero, false, errInvalid("decimal_parsing", []string{"query", key}, "%s", err)
	}
	return amount, true, nil
}
