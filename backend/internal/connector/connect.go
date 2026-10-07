package connector

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A browser provider's typed connect does not run inside the request that
// started it: the session is answered `signing_in` and the rounds drive it from
// a goroutine while the dialog's poll reports what they are doing.

// StartConnect signs in with credentials somebody typed.
func (e Bills) StartConnect(
	ctx context.Context, request provider.BillConnectStart,
) (provider.BillConnectState, error) {
	module, err := e.pick(request.Provider)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	if module, err = e.aimed(module, request.Site); err != nil {
		return provider.BillConnectState{}, err
	}
	credentials := billers.Credentials{
		Username: request.Username, Password: request.Password,
		Code: request.Code, Secret: request.Secret, SecondFactor: request.SecondFactor,
	}
	if api, ok := module.(billers.APIModule); ok {
		if !credentials.HasLogin() {
			return provider.BillConnectState{}, agentError(provider.ErrAgentBadRequest,
				"username and password are required")
		}
		return guarded(e.Engine, billerName(module), "the sign-in", func() (provider.BillConnectState, error) {
			return e.startAPIConnect(ctx, api, request, credentials)
		})
	}
	browserModule, ok := module.(billers.BrowserModule)
	if !ok {
		return provider.BillConnectState{}, agentError(provider.ErrAgentBadRequest,
			"%s cannot be signed in to by this build", module.ID())
	}
	if !credentials.HasLogin() {
		return provider.BillConnectState{}, agentError(provider.ErrAgentBadRequest,
			"username and password are required")
	}
	return guarded(e.Engine, billerName(module), "the sign-in", func() (provider.BillConnectState, error) {
		return e.startTypedConnect(ctx, browserModule, request, credentials)
	})
}

func (e Bills) startAPIConnect(
	ctx context.Context, module billers.APIModule, request provider.BillConnectStart,
	credentials billers.Credentials,
) (provider.BillConnectState, error) {
	s := e.open(module)
	s.profile = request.Profile
	s.site = request.Site
	s.setLogin(credentials)
	e.reportsEnd(s)
	if err := e.fetcher(module, s); err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}

	code, err := e.codeFor(credentials)
	if err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}
	credentials.Code = code
	outcome := module.Authenticate(ctx, credentials, e.call(ctx, s))
	if outcome.Challenge != nil {
		return e.state(s, e.park(s, *outcome.Challenge)), nil
	}
	if len(outcome.Session) == 0 {
		reason := outcome.Failed
		if reason == "" {
			reason = "the sign-in was refused"
		}
		defer e.close(s)
		return e.state(s, billers.State{
			State:  billers.StateFailed,
			Prompt: billerName(module) + " refused the sign-in",
			Error:  reason,
		}), nil
	}
	s.kept = outcome.Session
	return e.state(s, e.afterAPISignIn(ctx, s)), nil
}

// afterAPISignIn answers `accounts` when the provider bills more than one,
// `signed_in` otherwise. The walk is done here so the dialog can show the list
// without a second round trip; CompleteConnect hands back what this found.
func (e Bills) afterAPISignIn(ctx context.Context, s *session) billers.State {
	found, err := e.subaccounts(ctx, s)
	if err != nil {
		s.notes.Addf("the account walk failed: %v", err)
		found = nil
	}
	if len(found) > 1 {
		return billers.State{
			State: "accounts",
			Prompt: fmt.Sprintf("%s bills %d accounts; choose which matter",
				s.name, len(found)),
		}
	}
	return billers.State{State: billers.StateSignedIn}
}

func (e Bills) subaccounts(ctx context.Context, s *session) ([]billers.Subaccount, error) {
	if s.subs != nil {
		return s.subs, nil
	}
	found, err := s.module.Subaccounts(e.call(ctx, s))
	if err != nil {
		return nil, err
	}
	s.subs = found
	return found, nil
}

func (e Bills) startTypedConnect(
	ctx context.Context, module billers.BrowserModule, request provider.BillConnectStart,
	credentials billers.Credentials,
) (provider.BillConnectState, error) {
	s := e.open(module)
	s.profile = request.Profile
	s.site = request.Site
	s.setLogin(credentials)
	e.reportsEnd(s)
	opened, err := e.opener(module, agent.Open{Profile: request.Profile, State: []byte(""), Note: s.notes.Trace})
	if err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}
	s.Attach(opened)
	if opened.Page == nil {
		e.close(s)
		return provider.BillConnectState{}, agentError(provider.ErrAgentFailed,
			"the browser opened with no page")
	}
	if err := opened.Page.Goto(module.SignInURL()); err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}
	line := "Opening the sign-in page at " + billerName(module)
	s.drives(e.now(), line)
	// Answered before the rounds start, so a poll can never find a session
	// nothing is driving yet and read the page out from under the loop.
	out := e.state(s, billers.State{State: provider.BillConnectSigningIn, Prompt: line})
	e.rounds(s)
	return out, nil
}

// rounds drives the rest of a typed sign-in on its own. Everything it touches
// on the session goes through the accessors, because the dialog is polling the
// same session concurrently; it runs under the guard because a module panic in
// a goroutine would take the process down.
func (e Bills) rounds(s *session) {
	go func() {
		where, err := guarded(e.Engine, s.name, "the sign-in", func() (billers.State, error) {
			return e.resume(s)
		})
		if err != nil {
			where = billers.State{
				State:  billers.StateFailed,
				Prompt: s.name + " stopped the sign-in before it reached anything answerable.",
				Error:  plainly(err),
			}
			if opened := s.Opened(); opened != nil {
				where.Image = agent.Screenshot(opened.Page)
			}
		}
		if where.State == billers.StateFailed {
			// Noted here rather than left to the poll, so the page it ended on
			// is read while it is still there.
			e.failed(s, where, where.Image)
		}
		if err != nil {
			// The browser goes at once and the session stays: the profile
			// claim is what refuses the next sign-in, and the failure is what
			// the dialog is waiting to read.
			s.release()
		}
		s.landed(where)
	}()
}

func (e Bills) ConnectStatus(ctx context.Context, sessionID string) (provider.BillConnectState, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	return guarded(e.Engine, s.name, "the sign-in status", func() (provider.BillConnectState, error) {
		if s.live {
			// Not settled into a failure: in the live browser the person is
			// choosing, which is what `interactive` says.
			return e.state(s, e.liveState(s)), nil
		}
		if busy, line := s.busy(e.now()); busy {
			// Never read the page while the loop drives it: a classify taken
			// mid-fill reads a page halfway between two forms.
			return e.state(s, billers.State{State: provider.BillConnectSigningIn, Prompt: line}), nil
		}
		if last, _ := s.reading(); last.State == billers.StateFailed {
			// Keep the loop's own account: reading the page again afterwards
			// answers "the sign-in form came back" at most failures.
			return e.state(s, last), nil
		}
		if opened := s.Opened(); opened != nil && opened.Page != nil {
			module, ok := s.module.(billers.BrowserModule)
			if ok {
				// `factor` is this engine's own state and the backend has no
				// name for it, so the poll settles it too.
				where, err := module.Classify(opened.Page)
				if err != nil {
					return provider.BillConnectState{}, err
				}
				return e.state(s, settled(s.name, e.withFactorChoices(s, where))), nil
			}
		}
		last, _ := s.reading()
		if last.State == "" {
			return e.state(s, billers.State{State: billers.StateSignedIn}), nil
		}
		return e.state(s, last), nil
	})
}

func (e Bills) AnswerConnect(ctx context.Context, sessionID, code string) (provider.BillConnectState, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	if busy, _ := s.busy(e.now()); busy {
		// A second hand on a form the rounds are still driving; the dialog
		// never sends one while the sign-in is working.
		return provider.BillConnectState{}, agentError(provider.ErrAgentConflict,
			"that sign-in is still working; it will say when it needs an answer")
	}
	return guarded(e.Engine, s.name, "the answered sign-in", func() (provider.BillConnectState, error) {
		// An api provider has no page to type into; it parked its own answer.
		if s.answer != nil {
			outcome := s.answer(ctx, code, s.notes)
			if outcome.Challenge != nil {
				return e.state(s, e.park(s, *outcome.Challenge)), nil
			}
			if len(outcome.Session) > 0 {
				s.kept = outcome.Session
				s.answer = nil
				if s.pull != nil {
					return e.state(s, billers.State{State: billers.StateSignedIn}), nil
				}
				return e.state(s, e.afterAPISignIn(ctx, s)), nil
			}
			reason := outcome.Failed
			if reason == "" {
				reason = "the code was refused"
			}
			return e.state(s, billers.State{
				State: billers.StateFailed, Prompt: s.name + " refused the code", Error: reason,
			}), nil
		}

		module, ok := s.module.(billers.BrowserModule)
		opened := s.Opened()
		if !ok || opened == nil || opened.Page == nil {
			return provider.BillConnectState{}, agentError(provider.ErrAgentConflict,
				"%s has nothing to answer", s.name)
		}
		where, err := module.Classify(opened.Page)
		if err != nil {
			return provider.BillConnectState{}, err
		}
		e.noteRound(s, "answer", where)
		// A person's answer is a fresh try, which may type the password again.
		s.freshTry()
		step, err := module.Answer(opened.Page, where, code, s.login().Password)
		if err != nil {
			return provider.BillConnectState{}, err
		}
		e.noteDid(s, step)
		if !step.Acted {
			// The page is not asking for a code. If it wants the form's own
			// fields the loop drives it, since the dialog cannot type a
			// username; anything else is settled into a failure with its trail.
			if where.State == billers.StateEmail || where.State == billers.StatePassword {
				driven, err := e.resume(s)
				if err != nil {
					return provider.BillConnectState{}, err
				}
				return e.state(s, driven), nil
			}
			return e.state(s, settled(s.name, e.withFactorChoices(s, where))), nil
		}
		next, err := e.resume(s)
		if err != nil {
			return provider.BillConnectState{}, err
		}
		return e.state(s, next), nil
	})
}

func (e Bills) resume(s *session) (billers.State, error) {
	where, err := e.advance(s)
	if err != nil {
		return billers.State{}, err
	}
	if where, err = e.pastSecondFactor(s, where); err != nil {
		return billers.State{}, err
	}
	where = settled(s.name, where)
	if where.State == billers.StateSignedIn {
		s.forgetSecrets()
	}
	return where, nil
}

// CompleteConnect is the session to keep, and the accounts this login bills.
func (e Bills) CompleteConnect(ctx context.Context, sessionID string) (provider.BillConnectComplete, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.BillConnectComplete{}, err
	}
	if busy, _ := s.busy(e.now()); busy {
		return provider.BillConnectComplete{}, agentError(provider.ErrAgentConflict,
			"that sign-in is still working; it will say when it is in")
	}
	return guarded(e.Engine, s.name, "the finished sign-in", func() (provider.BillConnectComplete, error) {
		if _, ok := s.module.(billers.APIModule); ok {
			if len(s.kept) == 0 {
				return provider.BillConnectComplete{}, agentError(provider.ErrAgentConflict,
					"the sign-in is not finished")
			}
			found, err := e.subaccounts(ctx, s)
			if err != nil {
				return provider.BillConnectComplete{}, err
			}
			answer := provider.BillConnectComplete{
				Provider: string(s.module.ID()), SessionState: json.RawMessage(s.kept),
				Subaccounts: found, AccountHint: accountHintOf(s.kept),
			}
			s.landedIn()
			e.close(s)
			return answer, nil
		}

		module, ok := s.module.(billers.BrowserModule)
		opened := s.Opened()
		if !ok || opened == nil || opened.Page == nil {
			return provider.BillConnectComplete{}, agentError(provider.ErrAgentConflict,
				"the sign-in is not finished")
		}
		where, err := module.Classify(opened.Page)
		if err != nil {
			return provider.BillConnectComplete{}, err
		}
		if where.State != billers.StateSignedIn {
			// Somebody who signed in by hand may be standing on any page.
			_ = opened.Page.Goto(module.LandingURL())
			opened.Page.Settle()
			if where, err = module.Classify(opened.Page); err != nil {
				return provider.BillConnectComplete{}, err
			}
		}
		if where.State != billers.StateSignedIn {
			detail := where.Prompt
			if detail == "" {
				detail = where.State
			}
			return provider.BillConnectComplete{}, agentError(provider.ErrAgentConflict,
				"the sign-in is not finished: %s", detail)
		}
		// Land on a normal page so the session carries the cookies the site sets
		// after sign-in, then take them.
		if err := opened.Page.Goto(module.LandingURL()); err != nil {
			return provider.BillConnectComplete{}, err
		}
		opened.Page.Settle()
		hint, _ := module.AccountHint(opened.Page)
		found, err := e.subaccounts(ctx, s)
		if err != nil {
			return provider.BillConnectComplete{}, err
		}
		// Pinned before the storage state is read, so the profile and the
		// sealed copy both keep the sign-in.
		if opened.PinCookies != nil {
			opened.PinCookies(s.notes.Trace)
		}
		state, err := opened.StorageState()
		if err != nil {
			return provider.BillConnectComplete{}, err
		}
		answer := provider.BillConnectComplete{
			Provider: string(s.module.ID()), SessionState: state,
			Subaccounts: found, AccountHint: hint,
		}
		s.landedIn()
		e.close(s)
		return answer, nil
	})
}

// pastSecondFactor answers the one second factor this engine can answer for
// itself: an authenticator code minted from the kept setup key. Anything else
// is left for a person. The code and the key are never noted.
func (e *Engine) pastSecondFactor(s *session, where billers.State) (billers.State, error) {
	if s.signIn == nil {
		return where, nil
	}
	login := s.login()
	// The browser is asked for afresh each round, for advance's reason.
	for round := 0; round < 3; round++ {
		opened := s.Opened()
		if opened == nil || opened.Page == nil {
			break
		}
		mintable := where.State == billers.StateOTP && login.Secret != "" &&
			billers.KeyAnswers(where.Method, login.SecondFactor)
		if !mintable {
			break
		}
		s.says(e.now(), "Answering the code "+s.name+" asked for, from the authenticator key")
		code, err := mintedCode(login.Secret)
		if err != nil {
			return billers.State{}, err
		}
		step, err := s.signIn.Answer(opened.Page, where, code, login.Password)
		if err != nil {
			return billers.State{}, err
		}
		e.noteDid(s, step)
		if !step.Acted {
			break
		}
		if where, err = e.advance(s); err != nil {
			return billers.State{}, err
		}
	}
	return where, nil
}

func mintedCode(secret string) (string, error) {
	return billers.MintedCode(secret, time.Now, time.Sleep)
}

// codeFor mints a code from the key when none was typed.
func (e *Engine) codeFor(credentials billers.Credentials) (string, error) {
	if credentials.Code != "" || credentials.Secret == "" {
		return credentials.Code, nil
	}
	return mintedCode(credentials.Secret)
}

func (e *Engine) park(s *session, challenge billers.Challenge) billers.State {
	s.answer = challenge.Answer
	where := billers.State{
		State:  challenge.State,
		Method: challenge.Method,
		Prompt: challenge.Prompt,
		Image:  challenge.Image,
	}
	if where.State == "" {
		where.State = billers.StateOTP
	}
	s.mark(where)
	return where
}

func (e *Engine) state(s *session, where billers.State) provider.BillConnectState {
	was, _ := s.reading()
	s.mark(where)
	out := provider.BillConnectState{
		SessionID: s.id, Provider: string(s.module.ID()),
		State: where.State, Prompt: where.Prompt, Image: where.Image,
		Error: where.Error, Method: where.Method,
		Accounts: append([]provider.BillSubaccountRef(nil), s.subs...),
	}
	if out.Accounts == nil {
		out.Accounts = []provider.BillSubaccountRef{}
	}
	opened := s.Opened()
	if out.Image == "" && opened != nil && opened.Page != nil &&
		(where.State == billers.StateCaptcha || where.State == billers.StateFailed) {
		if shot, err := opened.Page.Screenshot(); err == nil && len(shot) > 0 {
			out.Image = base64.StdEncoding.EncodeToString(shot)
		}
	}
	// A failure carries its own trail, since the session may be reaped before
	// anybody asks the trail route.
	if where.State == billers.StateFailed {
		// Noted once: the poll answers the same failure repeatedly.
		if was.State != billers.StateFailed {
			e.failed(s, where, out.Image)
		}
		out.Trail = e.trailOf(s)
	}
	return out
}

// aimed points the module at the deployment the connection names. A NeedsSite
// provider with no site is refused before a browser opens, rather than
// building addresses against an empty string; a site on a module that takes
// none is ignored.
func (e Bills) aimed(module billers.Module, site string) (billers.Module, error) {
	if biller, known := domain.BillerByID(module.ID()); known && biller.NeedsSite && site == "" {
		return nil, agentError(provider.ErrAgentBadRequest,
			"%s is deployed once per customer and this connection has not been told which "+
				"deployment to use; set it on the connection first", billerName(module))
	}
	if site == "" {
		return module, nil
	}
	if sited, ok := module.(billers.SiteModule); ok {
		return sited.WithSite(site), nil
	}
	return module, nil
}

func accountHintOf(session billers.Session) string {
	var shape struct {
		AccountHint string `json:"account_hint"`
	}
	if len(session) == 0 || json.Unmarshal(session, &shape) != nil {
		return ""
	}
	return shape.AccountHint
}

func (e *Engine) call(ctx context.Context, s *session) billers.Call {
	out := billers.Call{
		Ctx: ctx, Session: s.kept, Fetch: s.fetcher, Notes: s.notes, Now: e.Now,
		Site:  s.site,
		Trail: func(note string, look bool) { e.notePage(s, note, look) },
	}
	if opened := s.Opened(); opened != nil {
		out.Page = opened.Page
	}
	return out
}

// billerName is the catalogue's name for the provider (domain.Billers), or the
// bare id for a module in a test.
func billerName(module billers.Module) string {
	if biller, known := domain.BillerByID(module.ID()); known {
		return biller.Name
	}
	return string(module.ID())
}
