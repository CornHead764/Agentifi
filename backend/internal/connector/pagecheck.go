package connector

import (
	"context"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A page check the page does not clear by itself (Turnstile's "Verify you are
// human" in Camoufox) is never answered or clicked here. A sign-in a person is
// watching is parked on it, in the `interactive` state with the page's picture,
// until the person ticks it in the live view; one nobody watches stops and
// says so.

// PageCheckPersonWait is how long a parked sign-in waits for the person.
const PageCheckPersonWait = 5 * time.Minute

// pageCheckRecheck is how often a parked sign-in looks for the check to have
// cleared.
const pageCheckRecheck = time.Second

// pageCheckNeedsPerson is the finding of a sign-in stopped by a check with no
// person to tick it. The pull reads it to pause unattended sign-ins.
const pageCheckNeedsPerson = "a page check needs a person"

const (
	pageCheckPrompt      = "Tick the box that says you're human, then the sign-in continues."
	pageCheckUntickedErr = "the page check was not ticked in time"
)

// setAttended says a person is at this session's sign-in: the rounds goroutine
// of a connect the dialog is polling.
func (s *session) setAttended(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.attended = on
}

// liveView is the view a person acts through on this session's page, made on
// first use, or nil when the session is not attended or its browser has none.
func (s *session) liveView() *browser.LiveView {
	s.mu.Lock()
	attended, view := s.attended, s.view
	s.mu.Unlock()
	if view != nil || !attended {
		return view
	}
	opened := s.Opened()
	if opened == nil || opened.Live == nil {
		return nil
	}
	made, err := opened.Live()
	if err != nil || made == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.view != nil {
		made.Close()
		return s.view
	}
	s.view = made
	return made
}

func (s *session) closeView() {
	s.mu.Lock()
	view := s.view
	s.view = nil
	s.mu.Unlock()
	if view != nil {
		view.Close()
	}
}

func (s *session) parkAtCheck(on bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.atCheck = on
}

// parkedAtCheck is the view a session parked on a page check is showing, or
// nil when it is not parked.
func (s *session) parkedAtCheck() *browser.LiveView {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.atCheck {
		return nil
	}
	return s.view
}

func (s *session) wasCancelled() bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.cancelled
}

// awaitCheck is what advance does with a page check that outlasted its wait.
// ok says the check cleared and the sign-in goes on; otherwise stopped is where
// it ended.
func (e *Engine) awaitCheck(s *session, page browser.Page) (stopped billers.State, ok bool) {
	view := s.liveView()
	if view == nil {
		s.notes.Addf("a check that had not cleared within %s stopped the sign-in: "+
			"only a person can tick it, and nobody was at this sign-in", billers.PageCheckWait)
		return billers.State{
			State: billers.StateFailed,
			Prompt: s.name + " showed a check that only a person can tick (\"Verify you are human\"), " +
				"and nobody was at this sign-in to tick it.",
			Error: pageCheckNeedsPerson,
		}, false
	}
	s.notes.Addf("a check had not cleared within %s; waiting up to %s for the person to tick it",
		billers.PageCheckWait, PageCheckPersonWait)
	s.parkAtCheck(true)
	defer s.parkAtCheck(false)
	deadline := e.now().Add(PageCheckPersonWait)
	for {
		if s.Opened() == nil || s.wasCancelled() {
			return billers.State{
				State:  billers.StateFailed,
				Prompt: "The sign-in at " + s.name + " was closed while it waited for the check.",
				Error:  "the sign-in was closed",
			}, false
		}
		if !billers.PageCheckPending(page) {
			s.notes.Addf("the person cleared the check")
			s.says(e.now(), "Check cleared; going on with the sign-in")
			return billers.State{}, true
		}
		if e.now().After(deadline) {
			s.notes.Addf("the check was still showing after %s", PageCheckPersonWait)
			return billers.State{
				State:  billers.StateFailed,
				Prompt: s.name + " waited for the check to be ticked, and it was not.",
				Error:  pageCheckUntickedErr,
			}, false
		}
		s.says(e.now(), pageCheckPrompt)
		e.sleep(pageCheckRecheck)
	}
}

// checkState is where a parked sign-in stands: `interactive`, with the page's
// latest picture and the size its clicks are in.
func (e Bills) checkState(s *session, view *browser.LiveView) provider.BillConnectState {
	out := e.state(s, billers.State{State: billers.StateInteractive, Prompt: pageCheckPrompt})
	frame := view.Frame(0)
	out.Image, out.Width, out.Height = frame.Image, frame.Width, frame.Height
	return out
}

// SignInInput plays what the person did in a parked sign-in's live view.
func (e Bills) SignInInput(ctx context.Context, sessionID string, events []provider.BillLiveInput) error {
	s, err := e.find(sessionID)
	if err != nil {
		return err
	}
	view := s.parkedAtCheck()
	if view == nil {
		return agentError(provider.ErrAgentConflict, "that sign-in is not waiting for you to act on its page")
	}
	played := make([]browser.LiveInput, len(events))
	for i, event := range events {
		// Ticking a box needs the pointer and nothing else; a key or typed
		// text would reach the provider's sign-in form.
		if !pageCheckInputs[strings.ToLower(event.Type)] {
			return agentError(provider.ErrAgentConflict, "only clicks reach a page waiting for its check")
		}
		played[i] = browser.LiveInput(event)
	}
	view.Play(played)
	return nil
}

// pageCheckInputs are the live-view events a parked page check accepts.
var pageCheckInputs = map[string]bool{"move": true, "down": true, "up": true, "click": true}
