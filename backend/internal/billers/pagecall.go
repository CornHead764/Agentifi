package billers

import (
	"context"
	"encoding/base64"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// A provider whose credential lives in the page rather than in anything a
// browser.PageFetcher can send asks through browser.PageCallScript. Every call
// runs under a deadline: an endpoint may hang rather than refuse, and a call
// that never settles holds the profile lock.

// pageCallTimeout is the longest one page call may take when the call's
// context sets no sooner deadline.
const pageCallTimeout = 30 * time.Second

// plainPageCall is a page call that needs nothing but the page's own cookies.
var plainPageCall = browser.PageCallScript("")

// AskPage makes one page call under the call's context, with the page's
// cookies sent to any host unless the request says otherwise. The page aborts
// its own fetch at the context's deadline or after pageCallTimeout, whichever
// is sooner, and the wait here ends when the context does.
func AskPage(call Call, script string, request browser.PageCall) (PageAnswer, error) {
	ctx := call.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	limited, cancel := context.WithTimeout(ctx, pageCallTimeout)
	defer cancel()
	if request.Credentials == "" {
		request.Credentials = "include"
	}
	var answer PageAnswer
	err := browser.EvaluatePageCall(limited, call.Page, script, request, &answer)
	return answer, err
}

// PageAnswer is what one page call came back with.
type PageAnswer struct {
	// Status is 0 when no answer arrived: see TimedOut, NoToken and Excerpt.
	Status int            `json:"status"`
	JSON   map[string]any `json:"json"`
	// Excerpt is the start of the body, for the note a failure writes, or
	// what went wrong when there was no body.
	Excerpt string `json:"excerpt"`
	Type    string `json:"type"`
	// Base64 is the body as bytes, for a call that asked for them.
	Base64 string `json:"base64"`
	// TimedOut is the endpoint having hung rather than refused.
	TimedOut bool `json:"timed_out"`
	// NoToken is the page holding nothing to ask with: the call was not made.
	NoToken bool `json:"no_token"`
}

// Refused is the provider saying the kept session no longer opens anything.
func (a PageAnswer) Refused() bool { return a.Status == 401 || a.Status == 403 }

func (a PageAnswer) At(path ...string) any { return field(a.JSON, path...) }

func (a PageAnswer) Rows(path ...string) []map[string]any {
	list, ok := a.At(path...).([]any)
	if !ok {
		return nil
	}
	out := make([]map[string]any, 0, len(list))
	for _, one := range list {
		if row, ok := one.(map[string]any); ok {
			out = append(out, row)
		}
	}
	return out
}

func (a PageAnswer) Object(path ...string) map[string]any {
	object, _ := a.At(path...).(map[string]any)
	return object
}

func (a PageAnswer) Bytes() ([]byte, error) { return base64.StdEncoding.DecodeString(a.Base64) }

func field(row map[string]any, path ...string) any {
	var node any = row
	for _, key := range path {
		object, ok := node.(map[string]any)
		if !ok {
			return nil
		}
		node = object[key]
	}
	return node
}
