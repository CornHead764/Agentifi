package billers

import (
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A browser provider before its reader is written (see
// docs/connectors/adding-a-bill-provider.md). Classify is generic on purpose: a
// password box, a username box, a code box, a sign-out link, a page check, and
// the provider's account area (a URL pattern) as the positive sign of being
// in. A page showing none of those is interactive. A draft pull answers no
// bills and a note, never a sign-in the household does not owe.

// interstitialPattern is a full-page interstitial check by its page's words,
// one alternative per spelling it has shipped. Plain alternations, so the same
// pattern can be handed to the page as a JavaScript one.
const interstitialPattern = `performing security verification|verifying you are human|` +
	`checking (?:your browser|if the site connection is secure)|` +
	`needs to review the security of your connection|cloudflare ray id`

var (
	blockedWords = regexp.MustCompile(
		`(?i)access denied|pardon our interruption|reference\s*#\s*\d|unusual (?:traffic|activity)|are you a (?:human|robot)|verify you are human|request unsuccessful`)
	interstitialWords = regexp.MustCompile(`(?i)` + interstitialPattern)
	interstitialTitle = regexp.MustCompile(`(?i)^\s*just a moment\b`)

	// A provider setting up how a login is verified, rather than verifying it.
	// Not "register this device", which is the "remember me" of a code page and is
	// ticked rather than refused.
	enrolmentWords = regexp.MustCompile(
		`(?i)\bsecurity profile\b|\bset\s?up (?:your |a )?(?:security (?:profile|questions)|` +
			`two-(?:step|factor)|2-step|multi-?factor|mfa)\b`)

	// What a page asking for a code says: to enter it, and where it was sent. A
	// carrier's dialog heads itself "We sent your code" and asks in the sentence
	// under it, which is the one worth showing.
	codeEnterWords = regexp.MustCompile(`(?i)\b(?:enter|type)\b.{0,80}\b(?:code|passcode)\b`)
	codeSentWords  = regexp.MustCompile(
		`(?i)\b(?:sent|texted|emailed|e-mailed)\b.{0,80}\b(?:code|passcode)\b|\b(?:code|passcode)\b.{0,80}\b(?:sent|texted|emailed|e-mailed)\b`)
	codeEmailWords = regexp.MustCompile(`(?i)e-?mail|\S@\S`)
	codePhoneWords = regexp.MustCompile(`(?i)\btext(?:ed)?\b|\bsms\b|\bphone\b|\bmobile\b|\+[\s•·*x\d]`)

	// The words of a control only a signed-in page carries. "Sign off" is a
	// bank's; its signed-out form says "Sign On".
	signedOutWords = regexp.MustCompile(`(?i)\b(sign out|log out|logout|signout|sign off)\b`)

	// The page between the password and the code, where a portal offering more
	// than one second factor asks which. Only an authenticator app can be
	// answered unattended, so the choice is made rather than handed to a person.
	//
	// One vocabulary, read twice: factorPage asks whether the page is that page,
	// and FactorKind asks what one of its choices is. A second spelling would
	// classify a factor page whose choices nothing then recognised.
	authenticatorWords = regexp.MustCompile(
		`(?i)authenticator|authentication app|verification app|security app|app-?based|` +
			`one-?time (?:pass)?code app|passcode app|\btotp\b|\bauthy\b|okta verify|duo mobile|` +
			`code from (?:your |the |an )?(?:mobile |phone )?app`)
	textMessageWords = regexp.MustCompile(
		`(?i)text message|\btext me\b|\bsms\b|\btext\s+(?:to|a code|my)\b|\ba text\b|^\s*text\s*$`)
	// A phone a code goes to, named by its masked number ("(***) ***-1234",
	// "***-***-1234") or by the phone itself ("Send to my phone"). A choice is
	// read for it after the app's words, because "the app on your phone" is the
	// app's; a page is read only for the masked number, because a page's footer
	// names a phone to call.
	maskedPhoneWords = regexp.MustCompile(`[*•·xX]{2,}\)?[\s.-]*[*•·xX]{2,}[\s.-]*[*•·xX\d…]`)
	phoneWords       = regexp.MustCompile(`(?i)\b(?:mobile|cell|phone)\b|` + maskedPhoneWords.String())
	// A tap on a phone, or an app nothing here holds a key for: neither is
	// answered by a code anybody types.
	pushWords = regexp.MustCompile(`(?i)\bpush\b|notification|\bapprove\b|\bapp\b`)

	// The ways nothing unattended can answer, told apart before the app is looked
	// for: "Call me with a code" and "Text me a code from the app" both carry the
	// app's words.
	spokenWords = regexp.MustCompile(`(?i)\bcall\b|voice|phone me`)

	factorQuestion = regexp.MustCompile(
		`(?is)(?:choose|select|pick|how).{0,60}(?:verify|verification|authenticate|security|sign in|log in|second factor|continue)`)

	// The same page when the portal asks only where to send a code ("We need
	// to verify your identity" over e-mail and text choices and a
	// "Send code"), naming no way in the question.
	codeDeliveryWords = regexp.MustCompile(
		`(?is)\b(?:send|receive|get|deliver)\b.{0,60}\b(?:code|passcode)\b|\bverify your identity\b|\bverify (?:it'?s|it is) you\b`)

	// The same page when the portal asks nothing and lists the ways as buttons
	// ("Select Method"), with no question and no box to type in.
	factorChoiceWords = regexp.MustCompile(
		`(?is)(?:select|choose|pick)\s+(?:an?\s+|your\s+)?(?:mfa\s+|authentication\s+|verification\s+|security\s+)?method|mfa method|multi-?factor authentication`)

	// The way in from a public page with no form and a "Sign in" link. Anchored on
	// the control's whole text: "Sign up" is the other door and "Sign in with
	// Google" is somebody else's.
	signInControlWords = regexp.MustCompile(`(?i)^(?:sign|log)\s*in$`)

	// What a step's own button says. The first page of a multi-page sign-in may
	// say "Next" with no type attribute, so a selector union is not
	// enough. Case-insensitive and anchored on the control's whole text, which
	// keeps "Sign up", "Sign out" and "Cancel" out; two patterns because a page
	// carrying "Sign in" and "Sign in to your account" should press the plain one.
	// SubmitRank ranks what SubmitControls read.
	submitWords = regexp.MustCompile(
		`(?i)^\s*(?:next|continue|submit|proceed|confirm|verify|(?:sign|log)\s*(?:in|on)|` +
			`send(?:\s+(?:me\s+)?(?:the\s+|my\s+|a\s+)?(?:verification\s+|security\s+|login\s+)?(?:code|passcode))?)\s*[.›»→>]*\s*$`)
	submitLeadWords = regexp.MustCompile(
		`(?i)^\s*(?:next|continue|submit|proceed|confirm|verify|(?:sign|log)\s*(?:in|on))\b`)

	usernameWords = regexp.MustCompile(`(?i)user|email|login|\bid\b|account`)
	codeAttrWords = regexp.MustCompile(`(?i)one-time-code|\botp\b|passcode|verification|security code|\bcode\b`)
	codeSaidWords = regexp.MustCompile(`(?i)passcode|one-time|verification code|security code|authenticator`)
)

const (
	// usernameSelector is one locator because the same box is filled at the email
	// step and in a combined form, where the password step must fill the username
	// too or it submits an empty one. The autocomplete and formcontrolname halves
	// are for a box with no type attribute at all (Angular Material), which
	// input[type="text"] does not match.
	usernameSelector = `input[type="email"], input[autocomplete="username"], input[autocomplete="email"], ` +
		`input[name*="user" i], input[id*="user" i], input[name*="email" i], input[id*="email" i], ` +
		`input[formcontrolname*="user" i], input[formcontrolname*="email" i], input[type="text"]`
	passwordSelector = `input[type="password"]`
	otpSelector      = `input[autocomplete="one-time-code"], input[name*="otp" i], input[id*="otp" i], ` +
		`input[name*="code" i], input[id*="code" i], input[name*="passcode" i]`
	rememberSelector = `input[type="checkbox"][name*="remember" i], input[type="checkbox"][id*="remember" i], ` +
		`input[type="checkbox"][name*="trust" i], input[type="checkbox"][id*="trust" i]`
	// submitControlSelector is everything a step could press; which one is
	// decided in the page, by its words and whether it can be pressed.
	submitControlSelector = `button, input[type="submit"], input[type="button"], [role="button"]`
	// submitAttribute marks the one control the ranking picked, so the press
	// lands on it and not on the first thing a union matches.
	submitAttribute = `data-agentifi-submit`
	submitMark      = `[` + submitAttribute + `]`
	// factorChoiceSelector is every control a factor page could offer a way to
	// verify as. One union, because the menu is read whole before anything is
	// pressed. The radio is the input itself, not an adjacent label: a label
	// commonly wraps its input, or the radio is named by aria-label.
	factorChoiceSelector = `button, a, [role="button"], [role="radio"], input[type="radio"]`
	// factorAttribute marks what the click lands on (the label a radio stands
	// behind, when there is one); factorRadioAttribute marks the radio itself,
	// which the fallback ticks while the click presses the label.
	factorAttribute      = `data-agentifi-factor`
	factorMark           = `[` + factorAttribute + `]`
	factorRadioAttribute = `data-agentifi-factor-radio`
	factorRadioMark      = `[` + factorRadioAttribute + `]`
	// signInControlSelector is what a "Sign in" on a page with no form is: a
	// link, or a button that goes to one.
	signInControlSelector = `a, button, [role="button"]`
	// otpFallbackSelector is the code box on a page whose boxes are named only by
	// their labels: the first text box in the form.
	otpFallbackSelector = `form input[type="text"], form input:not([type]), input[type="text"]`
)

// formWait is how long a page that read as empty is given to paint itself.
// Longer than agent.ChangeWait: it covers a cold sign-in page behind redirects.
const formWait = 15 * time.Second

// signInSteps is how many times one reading of a page may be nudged along
// before it is reported as it stands: the way in, and the bridge behind it.
const signInSteps = 2

// Draft is a browser module with a generic classifier and no reader, embedded
// by the modules that have one so they keep the same sign-in behaviour.
type Draft struct {
	BillerID    domain.BillerID
	Home        string
	SignIn      string
	Landing     string
	Prompt      string
	AccountArea InsideAccount
	// AccountPage is a page inside the account area, opened once when a sign-in
	// lands somewhere that is neither a form nor the account area, such as a
	// provider that sends a signed-in session to an error page of its own.
	AccountPage string
	// PressDirectly presses the sign-in's button with a forced click as soon as
	// the page reads it pressable with nothing on top, for a provider whose
	// button Playwright's actionability checks never see as stable (an Angular
	// Material button in Camoufox). Without that reading the press is the
	// ordinary one.
	PressDirectly bool
}

func (d Draft) ID() domain.BillerID { return d.BillerID }

func (d Draft) SignInURL() string { return d.SignIn }

func (d Draft) LandingURL() string { return d.Landing }

func (d Draft) SignInPrompt() string {
	if d.Prompt != "" {
		return d.Prompt
	}
	return "Sign in to " + d.Name() + ` in the browser below, and tick "remember this device" if it is offered.`
}

// Name is the catalogue's spelling, or the bare id for a module the catalogue
// does not carry (a test's).
func (d Draft) Name() string {
	if biller, known := domain.BillerByID(d.BillerID); known {
		return biller.Name
	}
	return string(d.BillerID)
}

// Classify reads the page and answers what it is showing. At a page nothing
// recognised, outside the account area, it takes one step a person would:
//
//   - The way in: a control whose whole text is "Sign in" or "Log in" on a
//     public page with no form is followed once.
//   - The bridge: a page with nothing to type into and exactly one button is a
//     hop a script usually takes, such as a hidden SAMLRequest under a single
//     Continue. It is pressed.
//   - The account page: when neither moves the page, a module that names an
//     AccountPage is sent there, once per page.
func (d Draft) Classify(page browser.Page) (State, error) {
	form, where, err := d.classify(page)
	if err != nil {
		return where, err
	}
	bridged := 0
	for step := 0; step < signInSteps; step++ {
		if where.State != StateInteractive || d.inside(page.URL()) {
			break
		}
		moved, bridge := d.nudge(page, form)
		if !moved {
			if d.AccountPage == "" || !takeAccountHop(page) || page.Goto(d.AccountPage) != nil {
				break
			}
		}
		if bridge {
			bridged++
		}
		if form, where, err = d.classify(page); err != nil {
			return where, err
		}
	}
	where.Bridged = bridged
	if where.State != StateInteractive {
		// Whatever it took to get here is spent; the next press starts fresh.
		Forget(page)
	}
	return where, nil
}

// classify is one reading of the page, taken twice when the first came to
// nothing: a widget sign-in renders after the network goes quiet, so
// "settled" is not "painted", and a blank read would be reported interactive.
func (d Draft) classify(page browser.Page) (Form, State, error) {
	page.Settle()
	form, err := ReadForm(page)
	if err != nil {
		// A page that cannot be read is a page somebody is standing on; the
		// loop keeps looking rather than calling the sign-in failed.
		return Form{}, State{State: StateInteractive}, nil
	}
	if form.Blank() && !d.inside(page.URL()) {
		if err := page.WaitForFunction(somethingOnPageScript, formWait); err == nil {
			page.Sleep(agent.Settle)
			if painted, err := ReadForm(page); err == nil {
				form = painted
			}
		}
	}
	if isInterstitial(form) {
		// The interstitial is given interstitialWait to clear; one still showing
		// after it is reported as a captcha for a person.
		if err := page.WaitForFunction(interstitialGoneScript, interstitialWait); err == nil {
			page.Settle()
			if cleared, err := ReadForm(page); err == nil {
				form = cleared
			}
		}
	}
	where := StateOf(form, page.URL(), d.AccountArea)
	if where.Error == enrolmentError {
		where.Prompt = enrolmentPrompt(d.Name(), d.site())
	}
	return form, where, nil
}

// interstitialWait is how long an interstitial check is given to clear.
const interstitialWait = 20 * time.Second

const interstitialGoneScript = `() => !new RegExp(` + "`" + interstitialPattern + "`" + `, 'i')
  .test(String(document.body ? document.body.innerText : '')) && !/^\s*just a moment\b/i.test(document.title || '')`

func (d Draft) site() string {
	host := strings.TrimPrefix(strings.TrimPrefix(d.Home, "https://"), "http://")
	host = strings.TrimPrefix(strings.TrimSuffix(host, "/"), "www.")
	if host == "" {
		return "the provider's own site"
	}
	return host
}

func (d Draft) inside(url string) bool {
	return d.AccountArea != nil && d.AccountArea(url)
}

func (d Draft) AccountHint(page browser.Page) (string, error) { return "", nil }

// FillEmail and FillPassword type into the dialog in front of the page when
// there is one, and into the page when there is not: a box behind a dialog is
// one nobody can reach.
func (d Draft) FillEmail(page browser.Page, email string) (agent.Step, error) {
	dismissed := d.dismissConsent(page)
	scope := scopeOf(page)
	if err := page.Fill(scope.sel(usernameSelector), email); err != nil {
		return agent.Step{}, err
	}
	d.tickRemember(page, scope)
	return d.submitAfter(page, dismissed)
}

// FillPassword fills a combined form whole: the username first when the page
// shows a box for it that takes typing, then the password, and one submit. A
// username-first sign-in's password page shows the username locked beside an
// Edit link, a box a fill would wait on until it times out.
func (d Draft) FillPassword(page browser.Page, password, email string) (agent.Step, error) {
	dismissed := d.dismissConsent(page)
	scope := scopeOf(page)
	if email != "" {
		if _, err := page.FillVisible(scope.sel(editable(usernameSelector)), email); err != nil {
			return agent.Step{}, err
		}
	}
	if err := page.Fill(scope.sel(passwordSelector), password); err != nil {
		return agent.Step{}, err
	}
	d.tickRemember(page, scope)
	return d.submitAfter(page, dismissed)
}

// editable narrows each alternative of a selector union to the boxes that take
// typing.
func editable(selector string) string {
	parts := splitUnion(selector)
	for at, part := range parts {
		parts[at] = part + `:not([readonly]):not([disabled]):not([aria-readonly="true"])`
	}
	return strings.Join(parts, ", ")
}

func (d Draft) CaptchaImage(page browser.Page) (string, error) { return "", nil }

// Answer types the code in; a Step that did not act is a page that was not
// asking for one. The union first, then the box the label is over, for a
// portal whose code box has no name, placeholder or stable id.
func (d Draft) Answer(page browser.Page, where State, code, _ string) (agent.Step, error) {
	if where.State != StateOTP {
		return agent.Step{}, nil
	}
	dismissed := d.dismissConsent(page)
	scope := scopeOf(page)
	segmented, err := d.answerSegments(page, code)
	if err != nil {
		return agent.Step{}, err
	}
	if !segmented {
		filled, err := page.FillVisible(scope.sel(otpSelector), code)
		if err != nil {
			return agent.Step{}, err
		}
		if !filled {
			if err := page.Fill(scope.sel(otpFallbackSelector), code); err != nil {
				return agent.Step{}, err
			}
		}
	}
	d.tickRemember(page, scope)
	return d.submitAfter(page, dismissed)
}

// NoPage is a browser provider reached without a browser: this application
// misconfigured, answered by asking for the sign-in that would open one.
func (d Draft) NoPage() Pull {
	return Pull{
		NeedsSignIn: true,
		Reason:      d.Name() + " is read through its kept browser profile, and this pull opened none",
	}
}

// SignInAgain is the portal no longer answering for the household. `found` is
// what had been read before, which the session lapsing does not make wrong.
func (d Draft) SignInAgain(found []Bill) Pull {
	return Pull{Bills: found, NeedsSignIn: true, Reason: d.Name() + " asked to sign in again"}
}

// Subaccounts and FetchBills are the honest half: a draft says what it is
// rather than reporting a sign-in the household does not owe.
func (d Draft) Subaccounts(call Call) ([]Subaccount, error) {
	call.Notes.Addf("%s; the accounts it bills are added when the reader is written", d.draftNote())
	return nil, nil
}

func (d Draft) FetchBills(call Call) (Pull, error) {
	call.Notes.Addf("%s", d.draftNote())
	return Pull{}, nil
}

func (d Draft) FetchDocument(call Call, bill Bill) (*Document, error) { return nil, nil }

func (d Draft) draftNote() string {
	return d.Name() + " is a draft: the sign-in is kept, and its billing page is not read yet"
}

// tickRemember ticks "remember this device" by the box's name, then by the
// words beside it (remember.go).
func (d Draft) tickRemember(page browser.Page, scope pageScope) {
	// A box whose state cannot be read is left alone; ticking a ticked one
	// unticks it.
	_, _ = page.CheckIfUnchecked(scope.sel(rememberSelector))
	tickRememberedByWords(page)
}
