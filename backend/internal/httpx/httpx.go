// Package httpx makes one outbound call and reads its answer the same way for
// every connector: a capped body, a status error that quotes the start of the
// answer, JSON numbers kept as the text that was sent, and one retry after a
// fresh sign-in when a call is refused.
package httpx

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Doer is *http.Client, a page in a browser, or anything else that sends a
// request.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// DefaultLimit is the body cap when a caller names none.
const DefaultLimit = 16 << 20

// ExcerptRunes is how much of an answer an error or a note quotes.
const ExcerptRunes = 300

// ErrTooLarge is an answer longer than the cap. It is refused rather than cut
// short, so a truncated document never reaches a parser.
var ErrTooLarge = errors.New("the answer is larger than this call accepts")

// Response is an answer read whole.
type Response struct {
	Status int
	Header http.Header
	// URL is the address that answered, after any redirect.
	URL  *url.URL
	Body []byte
}

func (r Response) OK() bool { return r.Status >= 200 && r.Status < 300 }

// Refused is the server turning away the credential rather than the request.
func (r Response) Refused() bool {
	return r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden
}

// Excerpt is the start of the body, for an error or a note.
func (r Response) Excerpt() string {
	return textutil.ClipMarked(strings.TrimSpace(string(r.Body)), ExcerptRunes)
}

// Err is a *StatusError for an answer outside 2xx, and nil otherwise.
func (r Response) Err() error {
	if r.OK() {
		return nil
	}
	return &StatusError{Status: r.Status, Excerpt: r.Excerpt()}
}

// StatusError is an answer outside 2xx.
type StatusError struct {
	Status  int
	Excerpt string
}

func (e *StatusError) Error() string {
	if e.Excerpt == "" {
		return fmt.Sprintf("HTTP %d", e.Status)
	}
	return fmt.Sprintf("HTTP %d: %s", e.Status, e.Excerpt)
}

// Read sends req and reads at most limit bytes of the answer (DefaultLimit
// when limit is 0 or less). The error is the transport's, the read's or
// ErrTooLarge, unwrapped, so a caller that must keep a secret URL out of an
// error string can redact it; an answer outside 2xx is not an error here.
func Read(doer Doer, req *http.Request, limit int64) (Response, error) {
	if limit <= 0 {
		limit = DefaultLimit
	}
	response, err := doer.Do(req)
	if err != nil {
		return Response{}, err
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, limit+1))
	out := Response{Status: response.StatusCode, Header: response.Header, Body: body}
	if response.Request != nil {
		out.URL = response.Request.URL
	}
	if err != nil {
		return out, err
	}
	if int64(len(body)) > limit {
		out.Body = body[:limit]
		return out, fmt.Errorf("%w (%d bytes)", ErrTooLarge, limit)
	}
	return out, nil
}

// DoJSON is Read, then a *StatusError for an answer outside 2xx, then the body
// decoded into out.
func DoJSON(doer Doer, req *http.Request, limit int64, out any) (Response, error) {
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	response, err := Read(doer, req, limit)
	if err != nil {
		return response, err
	}
	if err := response.Err(); err != nil {
		return response, err
	}
	if err := DecodeJSON(response.Body, out); err != nil {
		return response, fmt.Errorf("the answer is not the JSON expected: %w", err)
	}
	return response, nil
}

// GetJSON is DoJSON for a GET of address.
func GetJSON(ctx context.Context, doer Doer, address string, limit int64, out any) (Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return Response{}, err
	}
	return DoJSON(doer, req, limit, out)
}

// DecodeJSON decodes with json.Number for every number, so an amount decoded
// into an `any` never passes through a float64.
func DecodeJSON(raw []byte, out any) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	return decoder.Decode(out)
}

// RetryOnceOnAuth calls do, and when the answer is Refused, calls reauth and
// do once more. A second refusal is returned as it came: it means the
// credential itself is no longer accepted.
func RetryOnceOnAuth(
	ctx context.Context, do func(context.Context) (Response, error), reauth func(context.Context) error,
) (Response, error) {
	response, err := do(ctx)
	if err != nil || !response.Refused() {
		return response, err
	}
	if err := reauth(ctx); err != nil {
		return response, err
	}
	return do(ctx)
}
