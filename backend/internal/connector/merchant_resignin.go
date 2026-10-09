package connector

import (
	"cmp"
	"context"
	"encoding/json"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Signing in again with the kept password, once, in the middle of a pull whose
// session has lapsed. An authenticator code or a mailed code is answered once;
// anything else a person must answer pauses the pull as
// SignInPausedCodeNeeded, and a refused password as
// SignInPausedPasswordRefused. A browser or page that fails is an error, not
// a pause: a failed pull is tried again tomorrow.

// keyAnswers follows the bill sign-in's rule. A box the module could not read
// the words of names no channel, whatever the prompt this package wrote for it
// says.
func keyAnswers(where merchants.State, credential provider.MerchantCredential) bool {
	if where.State != merchants.StateOTP || credential.TOTPSecret == "" {
		return false
	}
	channel := "totp"
	if !where.Authenticator() {
		if channel = billers.CodeChannel(where.Prompt); channel == "totp" {
			channel = ""
		}
	}
	return billers.KeyAnswers(channel, string(credential.SecondFactor))
}

// signInAgain hands back the session it opened, or the stopped pull. The
// browser is seeded with a lapsed storage state because a merchant that
// recognises the device asks for less. The loop runs on a session nobody else
// can reach, whose notes are the pull's. A failure carries the page it
// happened on.
func (e Merchants) signInAgain(
	ctx context.Context, module merchants.Module, lapsed json.RawMessage,
	credential provider.MerchantCredential, notes *merchants.Notes,
) (_ json.RawMessage, _ *provider.MerchantFetchResult, failed error) {
	var seed json.RawMessage
	if _, handedOver := handedOverKind(lapsed); !handedOver {
		seed = lapsed
	}
	opened, err := e.opener(module, agent.Open{State: seed})
	if err != nil {
		return nil, nil, err
	}
	s := shopSession(module, notes)
	s.setLogin(billers.Credentials{
		Username: credential.Email, Password: credential.Password,
		SecondFactor: string(credential.SecondFactor),
	})
	s.Attach(opened)
	defer s.Shut()
	defer func() { failed = s.Pictured(failed) }()
	if opened.Page == nil {
		return nil, nil, agentError(provider.ErrAgentFailed, "the browser opened with no page")
	}
	page := opened.Page
	module.Attach(page)
	if err := page.Goto(module.SignInURL()); err != nil {
		return nil, nil, err
	}
	where, err := e.advance(s)
	if err != nil {
		return nil, nil, err
	}

	var stopped *provider.MerchantFetchResult
	switch {
	case keyAnswers(where, credential):
		code, err := billers.MintedCode(credential.TOTPSecret, e.now, e.sleep)
		if err != nil {
			return nil, e.paused(s, provider.SignInPausedCodeNeeded,
				s.name+" asked for an authenticator code and the kept key could not make one; "+
					"sign in to answer it"), nil
		}
		where, stopped, err = e.answerOnce(s, where, code,
			" asked for an authenticator code; the kept key answered it",
			" did not accept the code made from the kept authenticator key; sign in to answer it")
		if err != nil || stopped != nil {
			return nil, stopped, err
		}
	case where.State == merchants.StateOTP && !where.Authenticator() && credential.MailedCode != nil:
		code, found := credential.MailedCode(ctx, e.now())
		if !found {
			return nil, e.paused(s, provider.SignInPausedCodeNeeded,
				s.name+" sent a code and none reached the mailbox in time; sign in to answer it"), nil
		}
		var err error
		where, stopped, err = e.answerOnce(s, where, code,
			" sent a code; the mailbox answered it",
			" did not accept the code read from the mailbox; sign in to answer it")
		if err != nil || stopped != nil {
			return nil, stopped, err
		}
	}

	if where.Error == pageCheckNeedsPerson {
		return nil, e.paused(s, provider.SignInPausedPageCheck,
			s.name+" showed a check that only a person can tick (\"Verify you are human\"); "+
				"it is not tried again until you sign in"), nil
	}
	switch where.State {
	case merchants.StateSignedIn:
		session, _, err := e.sessionOf(module, opened)
		if err != nil {
			return nil, nil, err
		}
		notes.Addf("%s signed in again with the kept password", s.name)
		return session, nil, nil
	case merchants.StateOTP:
		return nil, e.paused(s, provider.SignInPausedCodeNeeded,
			s.name+" asked for a code; sign in to answer it"), nil
	case merchants.StateApproval:
		return nil, e.paused(s, provider.SignInPausedCodeNeeded,
			s.name+" asked for approval on a phone; sign in to approve it"), nil
	case merchants.StateCaptcha:
		return nil, e.paused(s, provider.SignInPausedCodeNeeded,
			s.name+" showed a check before its sign-in; sign in to answer it"), nil
	}
	refused := e.refusedPassword(s, where)
	reason := s.name + " did not accept the kept password"
	if !refused {
		reason = s.name + " did not finish signing in with the kept password"
	}
	if at := stoppedAt(where, page.URL()); at != "" {
		reason += " (" + at + ")"
	}
	if !refused {
		return nil, e.paused(s, "", reason+"; nothing showed the password was refused, so the next sync tries it again"), nil
	}
	return nil, e.paused(s, provider.SignInPausedPasswordRefused, reason), nil
}

// answerOnce types one code, from whichever source, and reads where it left the
// sign-in. answered and refused follow the merchant's name: the first is noted
// when the box took the code, the second pauses the pull when the box still
// asks after it. A code the engine found is not a person's answer, so the
// password is still not typed twice.
func (e Merchants) answerOnce(
	s *session, where merchants.State, code, answered, refused string,
) (merchants.State, *provider.MerchantFetchResult, error) {
	opened := s.Opened()
	step, err := s.shop.Answer(opened.Page, where, code, s.login().Password)
	if err != nil || !step.Acted {
		return where, nil, err
	}
	s.notes.Addf("%s%s", s.name, answered)
	if where, err = e.advance(s); err != nil {
		return where, nil, err
	}
	if where.State == merchants.StateOTP {
		return where, e.paused(s, provider.SignInPausedCodeNeeded, s.name+refused), nil
	}
	return where, nil, nil
}

func (e Merchants) paused(s *session, why, reason string) *provider.MerchantFetchResult {
	image := ""
	if opened := s.Opened(); opened != nil {
		image = agent.Screenshot(opened.Page)
	}
	return &provider.MerchantFetchResult{
		NeedsSignIn: true, Paused: why, Reason: reason, Image: image, Notes: s.notes.List(),
	}
}

// stoppedAt shows the page's host and path only: a sign-in page's query
// carries the merchant's own tokens.
func stoppedAt(where merchants.State, at string) string {
	said, page := cmp.Or(where.Prompt, where.Error), hostPath(at)
	switch {
	case page == "":
		return said
	case said == "":
		return "stopped at " + page
	}
	return said + ", at " + page
}
