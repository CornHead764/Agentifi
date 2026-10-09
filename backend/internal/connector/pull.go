package connector

import (
	"cmp"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A parked pull resumes on the same browser and transport: a fresh context is
// a fresh device to a provider that has just asked who this is.

// Pull reads one connection's bills.
func (e Bills) Pull(ctx context.Context, request provider.BillPullRequest) (provider.BillPull, error) {
	module, err := e.pick(request.Provider)
	if err != nil {
		return provider.BillPull{}, err
	}
	if module, err = e.aimed(module, request.Site); err != nil {
		return provider.BillPull{}, err
	}
	// The one entry that reaches a profile without a session lookup, so it
	// sweeps: an abandoned sign-in still holds the profile claim.
	e.Reap()
	return guarded(e.Engine, billerName(module), "the pull", func() (provider.BillPull, error) {
		return e.runPull(ctx, module, request, nil)
	})
}

func (e Bills) ResumePull(ctx context.Context, sessionID string) (provider.BillPull, error) {
	s, err := e.find(sessionID)
	if err != nil {
		return provider.BillPull{}, err
	}
	if s.pull == nil {
		return provider.BillPull{}, agentError(provider.ErrAgentConflict,
			"this session was not parked by a pull")
	}
	if s.answer != nil {
		return provider.BillPull{}, agentError(provider.ErrAgentConflict,
			"the challenge has not been answered yet")
	}
	out, err := guarded(e.Engine, s.name, "the resumed pull", func() (provider.BillPull, error) {
		return e.runPull(ctx, s.module, *s.pull, s)
	})
	if err == nil && out.Challenge == nil {
		e.close(s)
	}
	return out, err
}

// runPull is the whole of a pull. `parked` is the session a challenge stopped
// an earlier attempt on; its token is the one the answer minted, so nothing is
// refreshed. A failure carries the page it happened on, taken before the
// browser closes.
func (e Bills) runPull(
	ctx context.Context, module billers.Module, request provider.BillPullRequest, parked *session,
) (_ provider.BillPull, failed error) {
	s := parked
	if s == nil {
		s = e.open(module)
		s.profile = request.Profile
		s.site = request.Site
		if err := e.fetcher(module, s); err != nil {
			e.close(s)
			return provider.BillPull{}, err
		}
	}
	credentials := credentialsFrom(request.Credential)
	credentials.SecondFactor = request.SecondFactor
	s.setLogin(credentials)
	parking := false
	defer func() {
		failed = s.Pictured(failed)
		if !parking && parked == nil {
			e.close(s)
		}
	}()

	api, isAPI := module.(billers.APIModule)
	// A token refused right after minting is the provider saying no, not a
	// stale session; this also holds a pull to one try of the kept password.
	signedInNow := parked != nil && parked.passwordTried

	if isAPI {
		if len(s.kept) == 0 {
			kept, err := sessionStateFor(api, request.SessionState)
			if err != nil {
				return provider.BillPull{}, err
			}
			s.kept = kept
		}
		switch {
		case len(s.kept) == 0:
			if !credentials.HasLogin() {
				return e.needsSignIn(s, billerName(module)+" has no kept session", ""), nil
			}
			ended, err := e.pullSignIn(ctx, api, credentials, s, request, "")
			if err != nil {
				return provider.BillPull{}, err
			}
			if ended != nil {
				parking = ended.parks
				return ended.pull, nil
			}
			signedInNow = true
		case parked == nil && s.kept.Expired(e.now()):
			refreshed, needsSignIn, reason := api.Refresh(e.call(ctx, s))
			if !needsSignIn {
				s.kept = refreshed
				break
			}
			if !credentials.HasLogin() {
				return e.needsSignIn(s, cmp.Or(reason,
					billerName(module)+" asked to sign in again"), ""), nil
			}
			ended, err := e.pullSignIn(ctx, api, credentials, s, request, "")
			if err != nil {
				return provider.BillPull{}, err
			}
			if ended != nil {
				parking = ended.parks
				return ended.pull, nil
			}
			signedInNow = true
		}
	} else if s.Opened() == nil {
		// A profile on disk is a kept session even when the backend sent none,
		// and a kept password is a way in with neither (how a Camoufox provider,
		// which keeps no profile, is pulled every time).
		dir, dirErr := browser.ProfileDir(e.ProfilesRoot, request.Profile)
		onDisk := dirErr == nil && browser.ProfileExists(dir)
		if request.SessionState == "" && !onDisk && !credentials.HasLogin() {
			return e.needsSignIn(s, billerName(module)+" has no kept session", ""), nil
		}
		opened, err := e.opener(module, agent.Open{Profile: request.Profile, State: []byte(request.SessionState), Note: s.notes.Trace})
		if err != nil {
			return provider.BillPull{}, err
		}
		s.Attach(opened)
	}

	call := e.call(ctx, s)
	call.Subaccounts = request.Subaccounts

	result, err := module.FetchBills(call)
	if errors.Is(err, billers.ErrNeedsSignIn) {
		reason := billerName(module) + " refused the kept session"
		if signedInNow {
			reason = billerName(module) + " refused the session the kept password had just opened"
		}
		result, err = billers.Pull{NeedsSignIn: true, Reason: reason}, nil
	}
	if err != nil {
		return provider.BillPull{}, err
	}

	// A kept token refused before its stated expiry (clock drift, revoked in
	// the portal) is signed in past once when the password is on hand.
	if isAPI && result.NeedsSignIn && !signedInNow && credentials.HasLogin() {
		s.notes.Addf("%s refused the kept session; signing in with the kept password", billerName(module))
		ended, err := e.pullSignIn(ctx, api, credentials, s, request, result.Image)
		if err != nil {
			return provider.BillPull{}, err
		}
		if ended != nil {
			parking = ended.parks
			return ended.pull, nil
		}
		signedInNow = true
		if result, err = e.refetch(ctx, module, s, request); err != nil {
			return provider.BillPull{}, err
		}
	}

	// A signed-out browser provider: the kept password signs in on the page
	// this pull already has, and the profile keeps the result.
	opened := s.Opened()
	if !isAPI && result.NeedsSignIn && !signedInNow && opened != nil &&
		opened.Page != nil && credentials.HasLogin() {
		s.notes.Addf("%s asked to sign in again; trying the kept password", billerName(module))
		where, err := e.browserSignIn(s, credentials)
		if err != nil {
			return provider.BillPull{}, err
		}
		switch {
		case where.State == billers.StateSignedIn:
			signedInNow = true
			if result, err = e.refetch(ctx, module, s, request); err != nil {
				return provider.BillPull{}, err
			}
		case where.State == billers.StateOTP:
			// Parked like an api provider's challenge, on this page, so the
			// backend's answerers (the mailbox) get a chance before anybody is
			// told.
			s.notes.Addf("the kept password got as far as a code; parking the pull for it")
			parking = true
			s.passwordTried = true
			return e.parkPull(s, request, billers.Challenge{
				State: billers.StateOTP, Method: where.Method, Prompt: where.Prompt,
			}), nil
		case waitsOnAPerson(where.State):
			// Not a refusal, and not to be retried on a timer: each try is
			// another code sent.
			s.notes.Addf("the kept password got as far as %s; this one needs a person", where.State)
			reason := billerName(module) + " took the kept password and then asked for " +
				secondFactorWords(where.State)
			if where.Prompt != "" {
				reason += ": " + where.Prompt
			}
			out := e.needsSignIn(s, reason, result.Image)
			out.CodeNeeded = true
			return out, nil
		case where.Error == pageCheckNeedsPerson:
			s.notes.Addf("the sign-in page showed a check only a person can tick; the kept password was not tried")
			out := e.needsSignIn(s, billerName(module)+" showed a check that only a person can tick "+
				"(\"Verify you are human\") before its sign-in form", result.Image)
			out.PageCheck = true
			return out, nil
		case e.refusedPassword(s, where):
			s.notes.Addf("the kept password did not clear %s; this one needs a person", where.State)
			return e.passwordRefused(s, module, e.stoppedAt(s, where), result.Image), nil
		case where.State == billers.StateInteractive:
			// The page the reading gave up on may be the account under an
			// address the module does not know; the module's own reading of
			// its account page decides.
			s.notes.Addf("the kept password ended at a page the sign-in did not recognise; " +
				"reading the account page to see whether it got in")
			signedInNow = true
			if result, err = e.refetch(ctx, module, s, request); err != nil {
				return provider.BillPull{}, err
			}
			if result.NeedsSignIn {
				return e.unfinished(s, module, e.stoppedAt(s, where), result.Image), nil
			}
		default:
			return e.unfinished(s, module, e.stoppedAt(s, where), result.Image), nil
		}
	}

	if result.Challenge != nil {
		parking = true
		return e.parkPull(s, request, *result.Challenge), nil
	}
	if result.NeedsSignIn {
		return e.needsSignIn(s, cmp.Or(result.Reason,
			billerName(module)+" asked to sign in again"), result.Image), nil
	}

	if len(result.Session) > 0 {
		s.kept = result.Session
	}
	e.attachDocuments(ctx, s, module, result.Bills, request.KnownDocuments)

	if opened := s.Opened(); opened != nil && opened.PinCookies != nil {
		opened.PinCookies(s.notes.Trace)
	}

	bills, dropped := provider.CoerceBills(result.Bills)
	payments, unpaid := provider.CoercePayments(result.Payments)
	out := provider.BillPull{
		Bills: bills, Payments: payments,
		Notes:       append(append(s.notes.List(), dropped...), unpaid...),
		Trail:       e.trailOf(s),
		Subaccounts: result.Subaccounts,
	}
	if opened := s.Opened(); opened != nil && opened.StorageState != nil {
		state, err := opened.StorageState()
		if err != nil {
			return provider.BillPull{}, err
		}
		out.SessionState = state
	} else if len(s.kept) > 0 {
		out.SessionState = json.RawMessage(s.kept)
	}
	return out, nil
}

// endedSignIn is a pull's sign-in with the kept password that ended the pull:
// refused, or parked on a challenge (parks).
type endedSignIn struct {
	pull  provider.BillPull
	parks bool
}

// pullSignIn signs an api provider in with the kept password in the middle of
// a pull. Nil with no error means s holds the new session.
func (e Bills) pullSignIn(
	ctx context.Context, api billers.APIModule, credentials billers.Credentials, s *session,
	request provider.BillPullRequest, image string,
) (*endedSignIn, error) {
	outcome := e.credentialSignIn(ctx, api, credentials, s)
	switch {
	case outcome.Err != nil:
		return nil, outcome.Err
	case outcome.Challenge != nil:
		return &endedSignIn{pull: e.parkPull(s, request, *outcome.Challenge), parks: true}, nil
	case len(outcome.Session) == 0:
		return &endedSignIn{pull: e.passwordRefused(s, api, outcome.Failed, image)}, nil
	}
	s.kept = outcome.Session
	return nil, nil
}

// refetch is the pull again, on the session the kept password just opened.
// Its notes lead when it gets in, since the first try's are the story of a
// refusal.
func (e Bills) refetch(
	ctx context.Context, module billers.Module, s *session, request provider.BillPullRequest,
) (billers.Pull, error) {
	call := e.call(ctx, s)
	call.Subaccounts = request.Subaccounts
	retried := s.notes.Len()
	result, err := module.FetchBills(call)
	if errors.Is(err, billers.ErrNeedsSignIn) {
		result, err = billers.Pull{NeedsSignIn: true,
			Reason: billerName(module) + " refused the session the kept password had just opened"}, nil
	}
	if err != nil {
		return billers.Pull{}, err
	}
	if !result.NeedsSignIn && result.Challenge == nil {
		s.notes.Lead(retried)
	}
	return result, nil
}

func (e *Engine) needsSignIn(s *session, reason, image string) provider.BillPull {
	if opened := s.Opened(); image == "" && opened != nil && opened.Page != nil {
		if shot, err := opened.Page.Screenshot(); err == nil && len(shot) > 0 {
			image = base64.StdEncoding.EncodeToString(shot)
		}
	}
	return provider.BillPull{
		NeedsSignIn: true, Reason: reason, Image: image, Notes: s.notes.List(), Trail: e.trailOf(s),
	}
}

// passwordRefused is a sign-in with the kept password that the provider turned
// down (refusedPassword, at a browser provider). Distinct from "refused the
// kept session".
func (e *Engine) passwordRefused(s *session, module billers.Module, why, image string) provider.BillPull {
	reason := billerName(module) + " did not accept the kept password"
	if why != "" {
		reason += " (" + why + ")"
	}
	out := e.needsSignIn(s, reason, image)
	out.PasswordRefused = true
	return out
}

// unfinished is a sign-in with the kept password that ended neither signed in
// nor with the password turned down. Nothing is paused: the next scheduled
// pull tries the password again.
func (e *Engine) unfinished(s *session, module billers.Module, why, image string) provider.BillPull {
	reason := billerName(module) + " did not finish signing in with the kept password"
	if why != "" {
		reason += " (" + why + ")"
	}
	return e.needsSignIn(s, reason+"; nothing showed the password was refused, so the next update tries it again", image)
}

// stoppedAt is where a browser sign-in with the kept password ended. The
// state the loop answers is often only "failed", so the page comes from the
// trail.
func (e *Engine) stoppedAt(s *session, where billers.State) string {
	state, complaint, at := where.State, "", ""
	e.mu.Lock()
	if n := len(s.trail); n > 0 {
		state, complaint, at = s.trail[n-1].State, s.trail[n-1].Error, hostPath(s.trail[n-1].URL)
	}
	e.mu.Unlock()
	page := ""
	switch state {
	case billers.StateEmail:
		page = "stopped at the username page"
	case billers.StatePassword:
		page = "stopped at the password page"
	case billers.StateInteractive:
		page = "stopped at a page the sign-in did not recognise"
	}
	if page != "" && at != "" {
		page += " at " + at
	}
	said := cmp.Or(complaint, where.Prompt, where.Error)
	switch {
	case page == "":
		return said
	case said == "":
		return page
	}
	return page + ": " + said
}

func waitsOnAPerson(state string) bool {
	switch state {
	case billers.StateOTP, billers.StateApproval, billers.StateCaptcha, billers.StateFactor:
		return true
	}
	return false
}

func secondFactorWords(state string) string {
	switch state {
	case billers.StateApproval:
		return "a tap on a phone"
	case billers.StateCaptcha:
		return "a picture to be read"
	case billers.StateFactor:
		return "a choice of second factor"
	}
	return "a code"
}

func (e *Engine) parkPull(
	s *session, request provider.BillPullRequest, challenge billers.Challenge,
) provider.BillPull {
	kept := request
	s.pull = &kept
	where := e.park(s, challenge)
	return provider.BillPull{
		Challenge: &provider.BillChallengeState{
			SessionID: s.id, State: where.State, Method: where.Method,
			Prompt: where.Prompt, Image: where.Image,
		},
		Notes: s.notes.List(),
	}
}

func (e *Engine) credentialSignIn(
	ctx context.Context, module billers.APIModule, credentials billers.Credentials, s *session,
) billers.SignIn {
	if !credentials.HasLogin() {
		return billers.SignIn{}
	}
	code, err := e.codeFor(credentials)
	if err != nil {
		return billers.SignIn{Failed: err.Error()}
	}
	credentials.Code = code
	return module.Authenticate(ctx, credentials, e.call(ctx, s))
}

// browserSignIn is the connect's loop, on the page this pull already has.
func (e Bills) browserSignIn(s *session, credentials billers.Credentials) (billers.State, error) {
	s.setLogin(credentials)
	// A walk that was turned away can leave the page anywhere; anything that
	// is not the form goes to the sign-in page first.
	if module, ok := s.module.(billers.BrowserModule); ok {
		if opened := s.Opened(); opened != nil && opened.Page != nil {
			found, err := module.Classify(opened.Page)
			if err != nil {
				return billers.State{}, err
			}
			if found.State != billers.StateEmail && found.State != billers.StatePassword {
				if err := opened.Page.Goto(module.SignInURL()); err != nil {
					return billers.State{}, err
				}
			}
		}
	}
	where, err := e.advance(s)
	if err != nil {
		return billers.State{}, err
	}
	return e.pastSecondFactor(s, where)
}

func (e *Engine) attachDocuments(
	ctx context.Context, s *session, module billers.Module, bills []billers.Bill, known []string,
) {
	biller, found := domain.BillerByID(module.ID())
	if !found || !biller.HasDocuments {
		return
	}
	already := map[string]bool{}
	for _, one := range known {
		already[one] = true
	}
	call := e.call(ctx, s)
	for i := range bills {
		if already[bills[i].ExternalID] {
			continue
		}
		document, err := module.FetchDocument(call, bills[i])
		if err != nil {
			s.notes.Addf("the statement for %s could not be fetched: %v", bills[i].ExternalID, err)
			continue
		}
		if document == nil || len(document.Bytes) == 0 {
			continue
		}
		bills[i].Document = e.mint(document)
	}
}

// Keepalive touches a kept session so the provider does not forget it, and says
// whether it was still signed in. At an API provider it is the token refresh,
// so what comes back is a session to re-seal.
func (e Bills) Keepalive(
	ctx context.Context, providerID, profile, site, sessionState string,
) (provider.BillKeepalive, error) {
	module, err := e.pick(providerID)
	if err != nil {
		return provider.BillKeepalive{}, err
	}
	if module, err = e.aimed(module, site); err != nil {
		return provider.BillKeepalive{}, err
	}
	return guarded(e.Engine, billerName(module), "the keepalive", func() (provider.BillKeepalive, error) {
		return e.runKeepalive(ctx, module, profile, site, sessionState)
	})
}

func (e Bills) runKeepalive(
	ctx context.Context, module billers.Module, profile, site, sessionState string,
) (provider.BillKeepalive, error) {
	s := e.open(module)
	s.profile = profile
	s.site = site
	defer e.close(s)

	if api, ok := module.(billers.APIModule); ok {
		kept, err := sessionStateFor(api, sessionState)
		if err != nil {
			return provider.BillKeepalive{}, err
		}
		if len(kept) == 0 {
			s.notes.Addf("%s keeps no browser session; there is nothing to touch without a kept token",
				billerName(module))
			return provider.BillKeepalive{OK: true, Notes: s.notes.List()}, nil
		}
		s.kept = kept
		if err := e.fetcher(module, s); err != nil {
			return provider.BillKeepalive{}, err
		}
		refreshed, needsSignIn, reason := api.Refresh(e.call(ctx, s))
		if needsSignIn {
			return provider.BillKeepalive{OK: true, Reason: reason, Notes: s.notes.List()}, nil
		}
		return provider.BillKeepalive{
			OK: true, SignedIn: true, SessionState: json.RawMessage(refreshed), Notes: s.notes.List(),
		}, nil
	}

	browserModule, ok := module.(billers.BrowserModule)
	if !ok {
		return provider.BillKeepalive{OK: true, Notes: s.notes.List()}, nil
	}
	dir, dirErr := browser.ProfileDir(e.ProfilesRoot, profile)
	onDisk := dirErr == nil && browser.ProfileExists(dir)
	if sessionState == "" && !onDisk {
		s.notes.Addf("%s has no kept session to touch", billerName(module))
		return provider.BillKeepalive{OK: true, Notes: s.notes.List()}, nil
	}
	opened, err := e.opener(module, agent.Open{Profile: profile, State: []byte(sessionState), Note: s.notes.Trace})
	if err != nil {
		return provider.BillKeepalive{}, err
	}
	s.Attach(opened)
	if opened.Page == nil {
		return provider.BillKeepalive{OK: true, Notes: s.notes.List()}, nil
	}
	_ = opened.Page.Goto(browserModule.LandingURL())
	opened.Page.Settle()
	where, err := browserModule.Classify(opened.Page)
	if err != nil {
		return provider.BillKeepalive{}, err
	}
	signedIn := where.State == billers.StateSignedIn
	s.notes.Addf("%s's landing page reads %s", billerName(module), where.State)
	if signedIn && opened.PinCookies != nil {
		opened.PinCookies(s.notes.Trace)
	}
	out := provider.BillKeepalive{OK: true, SignedIn: signedIn, Notes: s.notes.List()}
	if !signedIn {
		out.Reason = cmp.Or(where.Prompt, where.State)
	}
	if opened.StorageState != nil {
		if state, err := opened.StorageState(); err == nil {
			out.SessionState = state
		}
	}
	return out, nil
}

// sessionStateFor refuses a kept session from another provider: a token
// handed to the wrong service is a leaked credential.
func sessionStateFor(module billers.APIModule, state string) (billers.Session, error) {
	if state == "" {
		return nil, nil
	}
	kept := billers.Session(state)
	kinds := module.SessionKinds()
	if len(kinds) == 0 {
		return kept, nil
	}
	for _, kind := range kinds {
		if kept.Kind() == kind {
			return kept, nil
		}
	}
	return nil, agentError(provider.ErrAgentBadRequest,
		"%s does not take a %q session", module.ID(), kept.Kind())
}

func credentialsFrom(credential map[string]string) billers.Credentials {
	return billers.Credentials{
		Username: credential["username"],
		Password: credential["password"],
		Code:     credential["totp"],
		Secret:   credential["totp_secret"],
	}
}
