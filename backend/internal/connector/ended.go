package connector

import (
	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// How a sign-in a person started ends when it does not land: it fails, the
// person closes the dialog, or nobody comes back and the reaper shuts it. Each
// is reported once, through Engine.SignInEnded, with the page and the trail,
// so the connection keeps what happened after the session is gone.

// reportsEnd marks s as a sign-in a person started.
func (e *Engine) reportsEnd(s *session) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.unfinished = func() {
		where, _ := s.reading()
		e.ended(s, where, "")
	}
}

// landedIn marks s as signed in and kept, which is no end to report.
func (s *session) landedIn() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.completed = true
}

// failed notes the round a sign-in failed on and reports its end, with the
// picture the failure already took or, for "", the page as it stands.
func (e *Engine) failed(s *session, where billers.State, image string) {
	e.note(s, "failed", where)
	e.ended(s, where, image)
}

// ended reports s's end once, if s is a sign-in a person started that did not
// land.
func (e *Engine) ended(s *session, where billers.State, image string) {
	s.mu.Lock()
	report := s.unfinished != nil && !s.reported && !s.completed
	if report {
		s.reported = true
	}
	closed := s.cancelled
	s.mu.Unlock()
	if !report || e.SignInEnded == nil {
		return
	}
	trail := e.trailOf(s)
	if where.State == provider.BillConnectSigningIn && len(trail) > 0 {
		// A sign-in shut while the loop drove it stood where its last round did.
		where.State = trail[len(trail)-1].State
	}
	if image == "" {
		if opened := s.Opened(); opened != nil {
			image = agent.Screenshot(opened.Page)
		}
	}
	e.SignInEnded(provider.BillSignInEnded{
		SessionID: s.id, Detail: unfinishedDetail(s.name, where, closed), Image: image, Trail: trail,
	})
}

// unfinishedDetail is the sentence a connection keeps for a sign-in that did
// not land: a failure's own words, or where the sign-in stood when it was
// closed or left.
func unfinishedDetail(name string, where billers.State, closed bool) string {
	if where.State == billers.StateFailed {
		if where.Error != "" {
			return where.Error
		}
		if where.Prompt != "" {
			return where.Prompt
		}
		return "the sign-in failed"
	}
	how := "The sign-in was left unfinished"
	if closed {
		how = "The sign-in was closed before it finished"
	}
	return how + ", " + standing(name, where)
}

// standing is where a sign-in stood, as the end of a sentence.
func standing(name string, where billers.State) string {
	switch where.State {
	case billers.StateEmail:
		return "while " + name + " was asking for the username."
	case billers.StatePassword:
		return "while " + name + " was asking for the password."
	case billers.StateFactor:
		return "while " + name + " was asking which way to verify."
	case billers.StateOTP:
		return "while " + name + " was asking for a code."
	case billers.StateCaptcha:
		return "while " + name + " was showing a picture to answer."
	case billers.StateApproval:
		return "while " + name + " was waiting for a tap on a phone."
	case billers.StateSignedIn, "accounts":
		return "after it signed in and before the sign-in was kept."
	}
	return "while it was still working through " + name + "'s pages."
}
