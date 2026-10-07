package browser

import (
	"regexp"
	"sync"
	"time"
)

// Recorded is every response the page received at a matching address, for an
// app whose own call cannot be made again from outside it. The browser hands
// the response over, so nothing in the page is patched and it serves in
// Camoufox as in Chrome.
//
// The handler runs on the driver's own goroutine, so it only keeps the
// response: a body read from there deadlocks the driver.
type Recorded struct {
	mu        sync.Mutex
	responses []Response
}

// recordPoll is how often Await looks again.
const recordPoll = 250 * time.Millisecond

// RecordResponses starts recording before the page is sent anywhere; a
// response that arrived earlier is not seen.
func RecordResponses(page Page, address *regexp.Regexp) *Recorded {
	recorded := &Recorded{}
	page.OnResponse(func(response Response) {
		if !address.MatchString(response.URL()) {
			return
		}
		recorded.mu.Lock()
		defer recorded.mu.Unlock()
		recorded.responses = append(recorded.responses, response)
	})
	return recorded
}

// Responses is what has arrived so far, in order.
func (r *Recorded) Responses() []Response {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]Response(nil), r.responses...)
}

// Await is the first recorded response, waiting up to within for one, or
// false when none arrived.
func (r *Recorded) Await(page Page, within time.Duration) (Response, bool) {
	return r.AwaitWhere(page, within, func(Response) bool { return true })
}

// AwaitWhere is the first recorded response wanted answers true for, waiting
// up to within for one, or false when none arrived. It is for an app that
// sends every question to one address, so an answer is told apart by what it
// holds. wanted runs on the caller's goroutine, so it may read the body; each
// response is offered to it once.
func (r *Recorded) AwaitWhere(page Page, within time.Duration, wanted func(Response) bool) (Response, bool) {
	offered := 0
	for waited := time.Duration(0); ; waited += recordPoll {
		responses := r.Responses()
		for ; offered < len(responses); offered++ {
			if wanted(responses[offered]) {
				return responses[offered], true
			}
		}
		if waited >= within {
			return nil, false
		}
		page.Sleep(recordPoll)
	}
}
