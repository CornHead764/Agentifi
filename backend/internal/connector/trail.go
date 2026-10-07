package connector

import (
	"cmp"
	"context"
	"errors"
	"net/url"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// The trail: what the provider showed, round by round of a sign-in and page by
// page of a pull, so a failed sign-in, or a pull that read less than the
// household expected, can be accounted for after its page is gone. Never a
// field value, a screenshot or a cookie: a trail is something the household
// pastes to somebody.

// trailLimit caps the lines kept, so a loop cannot grow a session without
// bound; a reading snapshot is at most billers' snapshotLimit characters.
const trailLimit = 80

// notePage appends a line a pull's module asked for: a sentence, and with
// look the page as a pull reads it.
func (e *Engine) notePage(s *session, note string, look bool) {
	if s == nil {
		return
	}
	entry := provider.BillTrailEntry{
		At: e.now().UTC(), Step: "read", State: "note", Inputs: map[string]int{},
		Note: textutil.Clip(note, 300),
	}
	if opened := s.Opened(); look && opened != nil && opened.Page != nil {
		page := opened.Page
		entry.State = "page"
		entry.URL = browser.WithoutQuery(page.URL())
		if title, err := page.Title(); err == nil {
			entry.Title = textutil.Clip(title, 120)
		}
		entry.Snapshot = billers.ReadingSnapshot(page)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(s.trail) >= trailLimit {
		return
	}
	s.trail = append(s.trail, entry)
}

// noteRound notes the bridges the reading pressed through, each as its own
// line at the address it landed on, and then the round itself.
func (e *Engine) noteRound(s *session, step string, where billers.State) {
	for pressed := 0; pressed < where.Bridged; pressed++ {
		e.note(s, "bridge", where)
	}
	e.note(s, step, where)
}

// note appends one round to the session's trail. The page is read with the
// classifier's own script, so the trail and the state cannot disagree.
func (e *Engine) note(s *session, step string, where billers.State) {
	if s == nil {
		return
	}
	entry := provider.BillTrailEntry{
		At: e.now().UTC(), Step: step, State: where.State,
		Inputs: map[string]int{}, Error: where.Error,
	}
	if opened := s.Opened(); opened != nil && opened.Page != nil {
		page := opened.Page
		entry.URL = browser.WithoutQuery(page.URL())
		if title, err := page.Title(); err == nil {
			entry.Title = textutil.Clip(title, 120)
		}
		if form, err := billers.ReadForm(page); err == nil {
			entry.Form = provider.BillSignInFormFlags{
				Password: form.Password, Username: form.Username,
				OTP: form.OTP, SignOutLink: form.SignOutLink,
			}
			if form.Inputs != nil {
				entry.Inputs = form.Inputs
			}
			entry.Error = cmp.Or(form.Error, where.Error)
		}
		if step == "failed" {
			entry.Snapshot = billers.Snapshot(page)
		}
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(s.trail) >= trailLimit {
		return
	}
	s.trail = append(s.trail, entry)
}

// noteDid records what the round just noted did, on the same line: a trail is
// one line per round.
func (e *Engine) noteDid(s *session, step agent.Step) {
	if s == nil || !step.Acted {
		return
	}
	if step.Dismissed != "" {
		s.notes.Addf("dismissed the cookie banner on %s's sign-in page (%s)", s.name, step.Dismissed)
	}
	if step.Note != "" {
		s.notes.Add(step.Note)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(s.trail) == 0 {
		return
	}
	last := &s.trail[len(s.trail)-1]
	last.Did = provider.BillSignInAction{
		Acted: true, Pressed: step.Pressed, Words: step.Words,
		Waited: step.Waited, Changed: step.Changed, Dismissed: step.Dismissed,
	}
	last.Forced = last.Forced || step.Forced
	if step.Note != "" {
		last.Note = step.Note
	}
}

// notePressFailure puts a press that did not land, with Playwright's account
// of the wait, in the notes and on the round's line of the trail: the error
// itself says only what a person reads.
func (e *Engine) notePressFailure(s *session, err error) {
	var failure *billers.PressFailure
	if s == nil || !errors.As(err, &failure) || failure.Note == "" {
		return
	}
	s.notes.Add(failure.Note)
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(s.trail) == 0 {
		return
	}
	s.trail[len(s.trail)-1].Note = failure.Note
}

// noteChose records the menu a factor page offered and which of it was taken,
// on that round's line. `unmet` is the login's own choice of factor when the
// menu did not offer it, as a sentence, or "".
func (e *Engine) noteChose(s *session, factor agent.Factor, unmet string) {
	if s == nil || len(factor.Choices) == 0 {
		return
	}
	offered := make([]provider.BillSignInChoice, 0, len(factor.Choices))
	for _, choice := range factor.Choices {
		offered = append(offered, provider.BillSignInChoice{
			Kind: choice.Kind, Words: textutil.Clip(choice.Words, 120),
		})
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	if len(s.trail) == 0 {
		return
	}
	s.trail[len(s.trail)-1].Choices = offered
	s.trail[len(s.trail)-1].Chose = factor.Chose
	s.trail[len(s.trail)-1].Forced = factor.Forced
	s.trail[len(s.trail)-1].Note = unmet
}

// plainly is an error as a person reads it: the first line, without the
// Playwright call log, which stays on the server log. Applied where an error
// becomes something a person reads, not where each error is made.
func plainly(err error) string {
	said := err.Error()
	if cut := strings.Index(said, "Call log:"); cut >= 0 {
		said = said[:cut]
	}
	if cut := strings.IndexByte(said, '\n'); cut >= 0 {
		said = said[:cut]
	}
	return textutil.Clip(strings.TrimSpace(said), 200)
}

func (e Bills) ConnectTrail(ctx context.Context, sessionID string) ([]provider.BillTrailEntry, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return nil, err
	}
	return e.trailOf(s), nil
}

// trailOf is a copy, taken under the lock: the session goes on appending.
func (e *Engine) trailOf(s *session) []provider.BillTrailEntry {
	e.mu.Lock()
	defer e.mu.Unlock()
	out := make([]provider.BillTrailEntry, len(s.trail))
	copy(out, s.trail)
	return out
}

// hostPath is an address as a note names it: its host and path, never the
// query or fragment, which carry a sign-in's tokens. "" for an address with
// no host.
func hostPath(at string) string {
	parsed, err := url.Parse(at)
	if err != nil || parsed.Host == "" {
		return ""
	}
	return parsed.Host + parsed.Path
}
