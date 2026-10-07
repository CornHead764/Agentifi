package connector

import (
	"cmp"
	"context"
	"encoding/json"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A merchant sign-in fills the email and password and stops at the first
// thing only a person can answer; the modules recognise only its screens.

// StartSignIn signs in as far as the merchant lets it without a person.
func (e Merchants) StartSignIn(
	ctx context.Context, merchant domain.MerchantID, email, password string,
) (provider.MerchantSignInState, error) {
	module, err := e.pick(merchant)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	if email == "" || password == "" {
		return provider.MerchantSignInState{}, agentError(provider.ErrAgentBadRequest,
			"email and password are required")
	}
	return guarded(e.Engine, merchants.Name(module), "the sign-in", func() (provider.MerchantSignInState, error) {
		s := e.add(shopSession(module, e.notes(merchants.Name(module))))
		s.setLogin(billers.Credentials{Username: email, Password: password})
		opened, err := e.opener(module, agent.Open{})
		if err != nil {
			e.close(s)
			return provider.MerchantSignInState{}, err
		}
		s.Attach(opened)
		if opened.Page == nil {
			e.close(s)
			return provider.MerchantSignInState{}, agentError(provider.ErrAgentFailed,
				"the browser opened with no page")
		}
		module.Attach(opened.Page)
		if err := opened.Page.Goto(module.SignInURL()); err != nil {
			e.close(s)
			return provider.MerchantSignInState{}, err
		}
		where, err := e.advance(s)
		if err != nil {
			return provider.MerchantSignInState{}, err
		}
		return e.landedAt(s, where), nil
	})
}

func (e Merchants) AnswerSignIn(
	ctx context.Context, sessionID, code string,
) (provider.MerchantSignInState, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return provider.MerchantSignInState{}, agentError(provider.ErrAgentConflict,
			"%s has nothing to answer", s.name)
	}
	return guarded(e.Engine, s.name, "the answered sign-in", func() (provider.MerchantSignInState, error) {
		where, err := s.shop.Classify(opened.Page)
		if err != nil {
			return provider.MerchantSignInState{}, err
		}
		// A person's answer is a fresh try, which may type the password again.
		s.freshTry()
		step, err := s.shop.Answer(opened.Page, where, code, s.login().Password)
		if err != nil {
			return provider.MerchantSignInState{}, err
		}
		if !step.Acted {
			// Reported unchanged so the dialog draws what it asks for instead.
			return e.stateResponse(s, where), nil
		}
		next, err := e.advance(s)
		if err != nil {
			return provider.MerchantSignInState{}, err
		}
		return e.landedAt(s, next), nil
	})
}

// SignInStatus re-reads where a sign-in stands, for the approval state, where
// the page moves on by itself.
func (e Merchants) SignInStatus(
	ctx context.Context, sessionID string,
) (provider.MerchantSignInState, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.MerchantSignInState{}, err
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return provider.MerchantSignInState{}, agentError(provider.ErrAgentConflict,
			"that sign-in has no page")
	}
	return guarded(e.Engine, s.name, "the sign-in status", func() (provider.MerchantSignInState, error) {
		where, err := s.shop.Classify(opened.Page)
		if err != nil {
			return provider.MerchantSignInState{}, err
		}
		if where.State == merchants.StateFactor {
			// A page that went on to ask how to send a code is the loop's
			// to press through, never a question the dialog knows.
			if where, err = e.advance(s); err != nil {
				return provider.MerchantSignInState{}, err
			}
		}
		return e.stateResponse(s, where), nil
	})
}

func (e Merchants) CompleteSignIn(
	ctx context.Context, sessionID string,
) (json.RawMessage, string, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return nil, "", err
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return nil, "", agentError(provider.ErrAgentConflict, "that sign-in has no page")
	}
	type finished struct {
		state json.RawMessage
		hint  string
	}
	out, err := guarded(e.Engine, s.name, "the finished sign-in", func() (finished, error) {
		page := opened.Page
		where, err := s.shop.Classify(page)
		if err != nil {
			return finished{}, err
		}
		if where.State != merchants.StateSignedIn {
			// A person who signed in by hand may be standing on any page.
			_ = page.Goto(s.shop.LandingURL())
			page.Settle()
			if where, err = s.shop.Classify(page); err != nil {
				return finished{}, err
			}
		}
		if where.State != merchants.StateSignedIn {
			return finished{}, agentError(provider.ErrAgentConflict,
				"the sign-in is not finished: %s", cmp.Or(where.Prompt, where.State))
		}
		state, hint, err := e.sessionOf(s.shop, opened)
		if err != nil {
			return finished{}, err
		}
		return finished{state: state, hint: hint}, nil
	})
	if err != nil {
		return nil, "", err
	}
	e.close(s)
	return out.state, out.hint, nil
}

// landedAt is where the loop stopped, settled into what the dialog has a
// screen for. The password is dropped the moment the merchant says the
// session is in.
func (e Merchants) landedAt(s *session, where merchants.State) provider.MerchantSignInState {
	where = settled(s.name, where)
	if where.State == merchants.StateSignedIn {
		s.forgetSecrets()
	}
	return e.stateResponse(s, where)
}

// sessionOf lands on a normal page first so the session carries the cookies
// the site sets after a sign-in, and prefers the merchant's own handed-over
// session where the page holds one.
func (e Merchants) sessionOf(module merchants.Module, opened *OpenBrowser) (json.RawMessage, string, error) {
	page := opened.Page
	if err := page.Goto(module.LandingURL()); err != nil {
		return nil, "", err
	}
	page.Settle()
	hint, _ := module.AccountHint(page)
	if handing, ok := module.(merchants.PageSessionModule); ok {
		if session, found, err := handing.SessionFromPage(page, e.now()); err != nil {
			return nil, "", err
		} else if found {
			return session, hint, nil
		}
	}
	if opened.StorageState == nil {
		return nil, "", agentError(provider.ErrAgentFailed, "that browser cannot hand back a session")
	}
	state, err := opened.StorageState()
	if err != nil {
		return nil, "", err
	}
	return state, hint, nil
}

// stateResponse attaches a picture only to a CAPTCHA, which cannot be answered
// unseen, and to a failure, whose page is gone by the time anybody asks.
func (e Merchants) stateResponse(s *session, where merchants.State) provider.MerchantSignInState {
	out := provider.MerchantSignInState{
		SessionID: s.id, State: where.State, Prompt: where.Prompt, Error: where.Error,
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return out
	}
	switch where.State {
	case merchants.StateCaptcha:
		if shot, err := s.shop.CaptchaImage(opened.Page); err == nil && shot != "" {
			out.Image = shot
		} else {
			out.Image = agent.Screenshot(opened.Page)
		}
	case merchants.StateFailed:
		out.Image = agent.Screenshot(opened.Page)
	}
	return out
}
