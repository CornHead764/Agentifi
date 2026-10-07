package connector

import (
	"cmp"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// The sign-in loop a bill connect, a bill pull's kept password, a merchant
// sign-in and a merchant pull's re-sign-in all drive.

// signInRounds is email, password, a way to verify, sending the code and the
// challenge, with one reading to spare.
const signInRounds = 6

// maxFactorSteps is how many factor pages one pass of the loop presses
// through: the way to verify, then the button that sends the code. A third is
// the same page again, and pressing send again only sends more codes.
const maxFactorSteps = 2

// A password form still showing after the password went in is read again
// every passwordRecheck until passwordAnswerWait has passed: the form stays up
// while the site posts the password and redirects, and only a form still
// there once that has had its time has turned the password down.
const (
	passwordAnswerWait = 20 * time.Second
	passwordRecheck    = time.Second
)

// passwordNotAccepted is the loop's finding for a password form still up once
// passwordAnswerWait has passed.
const passwordNotAccepted = "the password was not accepted"

// A page nothing here recognises, once the password has gone in, is most often
// one still on its way somewhere: an identity provider's hand-off, a widget's
// spinner, a press whose navigation outlasted its click. It is read again,
// settled, every passwordRecheck until arrivalWait has passed, so an account
// area the page reaches late is still found.
const arrivalWait = 20 * time.Second

// stalledRounds is how many rounds in a row may leave the page unchanged
// before the sign-in gives up. A round that changed nothing will do the same
// thing again; the one retry is for a widget that was still rendering.
const stalledRounds = 2

// advance drives past the username, the password and the factor menu, and
// stops at the first thing only a person can answer. The password is typed at
// most once per try (session.typed).
func (e *Engine) advance(s *session) (where billers.State, err error) {
	defer func() { e.notePressFailure(s, err) }()
	module := s.signIn
	if module == nil {
		return billers.State{State: billers.StateFailed, Error: "there is no page to sign in on"}, nil
	}
	var waited, arrived time.Duration
	stalled, factors := 0, 0
	// The browser is asked for afresh each round: a cancel closes it under
	// the goroutine.
	for round := 0; round < signInRounds; round++ {
		opened := s.Opened()
		if opened == nil || opened.Page == nil {
			return billers.State{State: billers.StateFailed, Error: "there is no page to sign in on"}, nil
		}
		page := opened.Page
		e.noteBrowser(s, page)
		page.Settle()
		if billers.AwaitPageCheck(page) {
			s.notes.Addf("a check that had not cleared within %s stopped the sign-in for you", billers.PageCheckWait)
		}
		where, err := module.Classify(page)
		if err != nil {
			return billers.State{}, err
		}
		login := s.login()
		if where.State == billers.StatePassword && login.Password != "" && s.passwordTyped() {
			if waited < passwordAnswerWait {
				page.Sleep(passwordRecheck)
				waited += passwordRecheck
				round--
				continue
			}
			e.noteRound(s, "sign-in", where)
			return billers.State{
				State:  billers.StateFailed,
				Prompt: s.name + " asked for the password again",
				Error:  passwordNotAccepted,
			}, nil
		}
		if where.State == billers.StateInteractive && s.passwordTyped() {
			if arrived < arrivalWait {
				page.Sleep(passwordRecheck)
				arrived += passwordRecheck
				round--
				continue
			}
			s.notes.Addf("%s's sign-in ended at a page it did not recognise, at %s",
				s.name, cmp.Or(hostPath(page.URL()), "an address that could not be read"))
		}
		e.noteRound(s, "sign-in", where)
		s.says(e.now(), doing(s.name, where))
		var step agent.Step
		switch where.State {
		case billers.StateEmail:
			if step, err = module.FillEmail(page, login.Username); err != nil {
				return billers.State{}, err
			}
		case billers.StatePassword:
			if login.Password == "" {
				return billers.State{
					State:  billers.StateFailed,
					Prompt: s.name + " asked for the password",
					Error:  "no password given",
				}, nil
			}
			if step, err = module.FillPassword(page, login.Password, login.Username); err != nil {
				return billers.State{}, err
			}
			s.markTyped()
		case billers.StateFactor:
			if factors == maxFactorSteps {
				return e.withFactorChoices(s, where), nil
			}
			factor, err := e.chooseFactor(s, page, where, login.SecondFactor)
			if err != nil {
				return billers.State{}, err
			}
			if factor.Kind == "" {
				if len(factor.Choices) > 0 {
					where.Choices = factor.Choices
				}
				if login.SecondFactor != "" {
					return notOffered(s.name, login.SecondFactor, where.Choices), nil
				}
				// The dialog's failure sentence is written from the state.
				return where, nil
			}
			factors++
			stalled = 0
			continue
		default:
			return where, nil
		}
		e.noteDid(s, step)
		e.logRound(s, round, where, step)
		if step.Changed {
			stalled = 0
			continue
		}
		if stalled++; stalled >= stalledRounds {
			return e.wentNowhere(s, step), nil
		}
	}
	return billers.State{
		State:  billers.StateFailed,
		Prompt: s.name + " kept asking for the same thing",
		Error:  "sign-in loop",
	}, nil
}

// noteBrowser says once per session which browser the sign-in ran in: the
// two are held to separate checks of their own.
func (e *Engine) noteBrowser(s *session, page browser.Page) {
	s.mu.Lock()
	noted := s.browserNoted
	s.browserNoted = true
	s.mu.Unlock()
	if noted {
		return
	}
	which := "Chrome"
	if browser.InFirefox(page) {
		which = "Camoufox"
	}
	s.notes.Addf("%s's sign-in ran in %s", s.name, which)
}

// chooseFactor takes a way to verify from the factor menu: the login's own
// choice, or the ranking when it made none. A Factor with no Kind is a menu
// with nothing this sign-in may take.
func (e *Engine) chooseFactor(
	s *session, page browser.Page, where billers.State, prefer string,
) (agent.Factor, error) {
	factor, err := s.signIn.ChooseFactor(page, prefer)
	unmet := unmetPreference(prefer, factor)
	// Noted before the error is answered: a module that fails while choosing
	// returns what it found alongside the error.
	e.noteChose(s, factor, unmet)
	e.logFactor(s, where, factor)
	if err != nil {
		return factor, err
	}
	if factor.Kind == "" {
		if unmet != "" {
			s.notes.Addf("%s asked which second factor to use and offered %s; %s",
				s.name, offeredList(factor.Choices), unmet)
			return factor, nil
		}
		s.notes.Addf(
			"%s asked which second factor to use and offered %s, none of which this sign-in can answer",
			s.name, offeredList(factor.Choices))
		return factor, nil
	}
	s.notes.Addf("%s asked which second factor to use; chose %s", s.name, factor.Kind)
	e.noteDid(s, factor.Step)
	e.note(s, "factor: "+factor.Kind, where)
	s.says(e.now(), s.name+" asked which way to verify; chose "+factor.Kind)
	return factor, nil
}

// refusalWords is a provider saying the login was turned down, in words that
// name what was typed or the account itself. A complaint alone is not enough:
// an account page can say "error" about anything.
var refusalWords = regexp.MustCompile(
	`(?i)\b(?:password|credentials?|user\s?(?:name|id)|e-?mail(?: address)?|login)\b.{0,60}?` +
		`\b(?:incorrect|invalid|wrong|not (?:valid|correct|recogni[sz]ed)|do(?:es)?(?: not|n't) match)\b|` +
		`\b(?:incorrect|invalid|wrong|unrecogni[sz]ed)\b.{0,60}?` +
		`\b(?:password|credentials?|user\s?(?:name|id)|e-?mail(?: address)?|login)\b|` +
		`\baccount (?:is|has been) (?:locked|disabled|suspended)\b`)

// refusedPassword says whether a sign-in with the kept password that ended at
// where shows the password turned down: a sign-in form asking again, or the
// provider's own words saying so. Only that pauses a kept password. A page
// nothing here recognises is evidence of nothing, and is often the account
// itself, reached after the reading gave up.
func (e *Engine) refusedPassword(s *session, where billers.State) bool {
	last, complaint := where.State, ""
	e.mu.Lock()
	if n := len(s.trail); n > 0 {
		last, complaint = s.trail[n-1].State, s.trail[n-1].Error
	}
	e.mu.Unlock()
	switch {
	case where.Error == passwordNotAccepted,
		where.State == billers.StateEmail, where.State == billers.StatePassword,
		last == billers.StateEmail, last == billers.StatePassword:
		return true
	}
	return refusalWords.MatchString(complaint + "\n" + where.Error + "\n" + where.Prompt)
}

func (e *Engine) wentNowhere(s *session, step agent.Step) billers.State {
	reason := "the page did not change after pressing the form's own button"
	switch {
	case step.Pressed == agent.PressedEnter:
		reason = "nothing on the page matched a button to press, and the Enter key moved nothing"
	case step.Words != "":
		reason = "the page did not change after pressing " + strconv.Quote(step.Words)
	}
	return billers.State{
		State:  billers.StateFailed,
		Prompt: s.name + " showed the same page again",
		Error:  reason,
	}
}

// logRound puts the round on the server log, since the trail dies with the
// session. Same rule as the trail: the provider's words, the address without
// its query, nothing typed.
func (e *Engine) logRound(s *session, round int, where billers.State, step agent.Step) {
	e.log().Debug("connector sign-in round",
		"provider", s.name, "round", round, "state", where.State, "url", s.address(),
		"pressed", step.Pressed, "words", step.Words, "waited", step.Waited, "changed", step.Changed,
		"dismissed", step.Dismissed)
}

// logFactor is the second-factor choice on the server log, by logRound's rule.
func (e *Engine) logFactor(s *session, where billers.State, factor agent.Factor) {
	e.log().Debug("connector sign-in factor",
		"provider", s.name, "state", where.State, "url", s.address(),
		"offered", offeredWords(factor.Choices), "chose", factor.Chose,
		"kind", factor.Kind, "confirmed", factor.Confirmed,
		"pressed", factor.Step.Pressed, "words", factor.Step.Words)
}

// address is the page's address without its query, or "".
func (s *session) address() string {
	if opened := s.Opened(); opened != nil && opened.Page != nil {
		return browser.WithoutQuery(opened.Page.URL())
	}
	return ""
}

func offeredWords(choices []billers.FactorChoice) []string {
	out := make([]string, 0, len(choices))
	for _, choice := range choices {
		out = append(out, choice.Words)
	}
	return out
}

// withFactorChoices puts the menu on a factor state read afresh (the poll, a
// misplaced answer, a menu that kept coming back) before it is settled into a
// failure.
func (e *Engine) withFactorChoices(s *session, where billers.State) billers.State {
	if where.State != billers.StateFactor || len(where.Choices) > 0 {
		return where
	}
	opened := s.Opened()
	if opened == nil || opened.Page == nil {
		return where
	}
	if choices, err := billers.FactorChoices(opened.Page); err == nil {
		where.Choices = choices
	}
	return where
}

// offeredList is the menu for a sentence. Choices that read as nothing are a
// finding of their own: controls drawn where this engine does not see them
// (a shadow root, another frame, no words).
func offeredList(choices []billers.FactorChoice) string {
	if len(choices) == 0 {
		return "no choice this sign-in could read"
	}
	return strings.Join(billers.Quoted(offeredWords(choices)), ", ")
}

// unmetPreference is the login's own choice of factor when the menu did not
// offer it, as a sentence, or "".
func unmetPreference(prefer string, factor agent.Factor) string {
	if prefer == "" || factor.Kind == prefer {
		return ""
	}
	if factor.Kind != "" {
		// A module with its own order (Costco's) takes no preference.
		return factorWords(prefer) + " was chosen for this login; the provider's own order took " +
			factorWords(factor.Kind)
	}
	return factorWords(prefer) + " was chosen for this login and not offered, so nothing was taken"
}

// notOffered is the failure of a sign-in whose chosen way to verify the
// factor page did not offer. Another way is never taken in its place: it sends
// a code where nobody is waiting for one.
func notOffered(name, prefer string, choices []billers.FactorChoice) billers.State {
	return billers.State{
		State: billers.StateFailed,
		Prompt: name + " asked which way to verify and offered " + offeredList(choices) +
			". This login's second factor is set to " + factorWords(prefer) + ", which " + name +
			" did not offer. Choose one it offers under Second factor in the sign-in form and sign in again.",
		Error:   factorWords(prefer) + " was chosen and not offered; offered " + offeredList(choices),
		Choices: choices,
	}
}

func factorWords(kind string) string {
	switch kind {
	case "email":
		return "a code sent by e-mail"
	case "totp":
		return "an authenticator app"
	case "sms":
		return "a text message"
	}
	return kind
}

// doing is the round just read, as a status line for the dialog. By the
// trail's rule it says what the provider asked for, never the address or what
// was typed.
func doing(name string, where billers.State) string {
	switch where.State {
	case billers.StateEmail:
		return "Filling in the username"
	case billers.StatePassword:
		return "Filling in the password"
	case billers.StateFactor:
		return name + " asked which way to verify"
	case billers.StateOTP:
		return name + " asked for a code"
	case billers.StateCaptcha:
		return name + " showed a picture to answer"
	case billers.StateApproval:
		return name + " is waiting for a tap on your phone"
	case billers.StateSignedIn:
		return "Signed in; reading what this login bills"
	}
	return "Waiting for " + name + " to show the next page"
}

// settled turns a state the dialog has no screen for into a named failure,
// which carries the trail. The dialog can show a code, a picture, a tap on a
// phone, the list of accounts, signed in, or failed; everything else ends here:
//
//   - `email`/`password` at the end of the loop mean the form came back.
//   - `interactive` means somebody is driving the page, and in a typed sign-in
//     nobody is.
//   - `factor` is the loop's own state and never leaves it.
//
// `signing_in` never reaches here: it is the engine's state, not a module's.
func settled(name string, where billers.State) billers.State {
	switch where.State {
	case billers.StateOTP, billers.StateCaptcha, billers.StateApproval,
		billers.StateSignedIn, billers.StateFailed, "accounts":
		return where
	case billers.StateEmail, billers.StatePassword:
		return billers.State{
			State: billers.StateFailed,
			Prompt: name + " showed its sign-in form again. The username and password were " +
				"typed into it and it came back, so it either refused them or never " +
				"received them. What the page showed is below.",
			Error: "the sign-in form came back",
		}
	case billers.StateFactor:
		return billers.State{
			State: billers.StateFailed,
			Prompt: name + " asked which second factor to use and offered " +
				offeredList(where.Choices) +
				". With no second factor chosen a sign-in takes an authenticator app or a text message, " +
				"and an e-mailed code only when asked to: choose the way you want under Second factor " +
				"in the sign-in form and sign in again.",
			Error: "unrecognised factor page; offered " + offeredList(where.Choices),
		}
	case billers.StateInteractive:
		return billers.State{
			State: billers.StateFailed,
			Prompt: name + " showed a page this sign-in did not recognise — no username box, " +
				"no password box and nothing it was asked to answer. What the page showed is below.",
			Error: "unrecognised page",
		}
	}
	return billers.State{
		State:  billers.StateFailed,
		Prompt: name + " ended the sign-in somewhere this dialog has no screen for.",
		Error:  "unanswerable state: " + where.State,
	}
}
