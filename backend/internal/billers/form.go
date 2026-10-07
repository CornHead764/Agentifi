package billers

import (
	"regexp"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// readFormScript reads the page and does not decide: every box somebody could
// type into with all the words attached to it (its attributes, its bound
// label, the headings of its form) and the page around them. Whether a box is
// a code box is FormFrom's answer, in Go, where a test can drive it; a code
// box may carry no attributes at all and be named only by its label.
const readFormScript = `() => {` + agent.DialogScopeJS + codeGroupsJS + `
  const root = agentifiScope;
  const inDialog = root !== document;
` + agent.VisibleJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + agent.HeadingsJS + agent.PageTextJS + `
  const inputs = [...root.querySelectorAll('input')].filter(visible);
  const text = pageTextOf(root);

  // What the form the box sits in is about: its headings and its sentences,
  // and the page's own — or the dialog's — when the box is in no form or a
  // form that says nothing.
  const contextOf = (input) => {
    const form = (input.closest ? input.closest('form') : null);
    let said = form && root.contains(form) ? headingsOf(form, 'h1, h2, h3, legend, p') : [];
    if (said.length === 0) said = headingsOf(root, 'h1, h2, h3, legend, p');
    return clean(said.join(' · ')).slice(0, 300);
  };

  // What the dialog calls itself: its own name, from outside, or 'dialog'
  // where it has none.
  const dialogLabel = () => inDialog ? (nameOf(root).slice(0, 120) || 'dialog') : '';

  const headings = headingsOf(root, inDialog ? 'h1, h2, h3, [role="heading"]' : 'h1, h2, h3');
  const submits = [...root.querySelectorAll(` + agent.SubmitSelectorJS + `)].filter(visible).length;
  // Every pressable control's own words: a portal draws its ways to a code as
  // plain buttons, which the submit count above never sees.
  const buttons = [...root.querySelectorAll('button, [role="button"], input[type="button"]')].filter(visible)
    .map((el) => clean(el.innerText || el.value || el.getAttribute('aria-label') || '')).filter((said) => said !== '')
    .slice(0, 30).map((said) => said.slice(0, 120));
  // The radios a person could pick from: the input, and the ARIA one — a
  // div with role=radio in a role=radiogroup — which is no input at all and
  // so is nowhere in the fields above.
  const radios = [...root.querySelectorAll('input[type="radio"], [role="radio"]')].filter((el) =>
    visible(el) || visible(el.closest ? el.closest('label') : null)).length;

  // What the provider is complaining about, from two directions: the lines
  // that read like a complaint, and whatever is visible in a box the page
  // built to hold one. The second is what catches a wording nobody predicted
  // — "Please enter a valid email address" is a refusal with none of the
  // words below in it. Visible, both times: a widget keeps its error box in
  // the document from the first paint and shows it on a refusal.
  //
  // The one class name here, .messages-container, is a widget's rather
  // than a provider's — PingOne DaVinci draws it, and the portals that draw
  // it are whoever bought that widget. A selector belonging to one provider
  // would belong in that provider's file instead.
  const boxes = [...root.querySelectorAll(
    '[class*="error" i], [id*="error" i], [class*="validation" i], [id*="validation" i], [role="alert"], .messages-container')]
    .filter(visible)
    .map((el) => clean(el.innerText))
    .filter((line) => line !== '' && line.length <= 200);
  const said = text.split('\n').map((line) => line.trim()).filter(
    (line) => line !== '' && line.length <= 200 && ` + agent.TroublePatternJS + `.test(line));
  const trouble = [...new Set([...boxes, ...said])];
  return {
    fields: inputs.map((i) => ({
      type: (i.type || 'text').toLowerCase(),
      name: i.name || '',
      id: i.id || '',
      autocomplete: i.autocomplete || '',
      placeholder: i.placeholder || '',
      ariaLabel: i.getAttribute('aria-label') || '',
      label: nameOf(i).slice(0, 200),
      context: contextOf(i),
      group: agentifiGroupOf(i),
    })),
    signOutLink: !!document.querySelector('a[href*="logout"], a[href*="signout"], a[href*="sign-out"], a[href*="log-out"], button[data-testid*="logout" i]'),
    submits,
    buttons,
    radios,
    heading: [...new Set([dialogLabel(), ...headings])].filter((line) => line !== '' && line !== 'dialog')
      .slice(0, 2).join(' · ').slice(0, 200),
    error: trouble.slice(0, 3).join(' · ').slice(0, 200),
    text,
    title: String(document.title || '').slice(0, 200),
    dialog: dialogLabel(),
  };
}`

// somethingOnPageScript is what a page that is still painting has none of: a
// visible box, a visible button (a bridge page has only that, and would
// otherwise cost the full wait at every hop) or a sign-out link. Read in the
// dialog in front of the page when there is one: a dialog that is only a
// spinner is still painting.
const somethingOnPageScript = `() => {` + agent.DialogScopeJS + `
` + agent.VisibleJS + `
  return [...agentifiScope.querySelectorAll('input')].some(visible) ||
    [...agentifiScope.querySelectorAll('button, input[type="submit"]')].some(visible) ||
    !!document.querySelector('a[href*="logout"], a[href*="signout"], a[href*="sign-out"], a[href*="log-out"]');
}`

type Reading struct {
	Fields      []Field `json:"fields"`
	SignOutLink bool    `json:"signOutLink"`
	// Submits is how many buttons the page offers to press. A page with buttons
	// and no boxes is a page offering a choice.
	Submits int `json:"submits"`
	// Buttons is every visible pressable control's words, submit or not.
	Buttons []string `json:"buttons"`
	// Radios counts radio inputs and role=radio elements.
	Radios  int    `json:"radios"`
	Heading string `json:"heading"`
	Error   string `json:"error"`
	Text    string `json:"text"`
	Title   string `json:"title"`
	// Dialog is what the dialog in front of the page calls itself, "dialog"
	// for one that names itself nothing, and "" when there is none. When
	// there is one, everything above but the sign-out link is the dialog's.
	Dialog string `json:"dialog"`
}

// Field is one visible box and every word that names it.
type Field struct {
	Type         string `json:"type"`
	Name         string `json:"name"`
	ID           string `json:"id"`
	Autocomplete string `json:"autocomplete"`
	Placeholder  string `json:"placeholder"`
	AriaLabel    string `json:"ariaLabel"`
	// Label is the text of the label bound to this box, by `for`, by wrapping it,
	// or by aria-labelledby.
	Label string `json:"label"`
	// Context is the headings and sentences of the form the box sits in.
	Context string `json:"context"`
	// Group is which run of one-character boxes this one belongs to, from 1, or 0.
	Group int `json:"group"`
}

// Form is what the page amounts to; StateOf is pure over it.
type Form struct {
	Password    bool
	Username    bool
	OTP         bool
	SignOutLink bool
	// Boxes is how many visible inputs somebody could type into (not tick boxes
	// or radios), and Submits how many buttons: together, the shape of a page
	// that asks a question by listing the answers.
	Boxes   int
	Submits int
	// Buttons is the words on every visible button, submit or not.
	Buttons []string
	Radios  int
	Heading string
	// Inputs is how many visible boxes of each type the page shows, for the trail.
	Inputs map[string]int
	// Error is the provider's own complaint, capped. Shown to the household;
	// nothing branches on it.
	Error  string
	Text   string
	Title  string
	Dialog string
	// Segments is how many one-character boxes a split code is spread over.
	Segments int
}

// Blank is a page with nothing on it this reading has a name for, which is
// what a widget looks like while it is still painting.
func (f Form) Blank() bool {
	return !f.Password && !f.Username && !f.OTP && !f.SignOutLink && f.Boxes == 0
}

// typedBox is a box somebody types into. A radio called "textCode" is how a
// page *offers* a code, which is a different question and a different answer.
func typedBox(kind string) bool {
	switch kind {
	case "text", "tel", "number", "search", "email", "password", "url", "":
		return true
	}
	return false
}

// FormFrom is the decision the page reading deliberately does not make. A box
// is a code box when it says so itself or when only the page does: its label,
// or the heading and sentence of its form.
func FormFrom(read Reading) Form {
	form := Form{
		SignOutLink: read.SignOutLink,
		Submits:     read.Submits,
		Buttons:     read.Buttons,
		Radios:      read.Radios,
		Heading:     read.Heading,
		Inputs:      map[string]int{},
		Error:       read.Error,
		Text:        read.Text,
		Title:       read.Title,
		Dialog:      read.Dialog,
	}
	segmented := codeGroup(read.Fields)
	for _, field := range read.Fields {
		kind := field.Type
		if kind == "" {
			kind = "text"
		}
		form.Inputs[kind]++
		if !typedBox(field.Type) {
			continue
		}
		form.Boxes++
		switch {
		case segmented > 0 && field.Group == segmented:
			form.OTP = true
			form.Segments++
		case field.Type == "password":
			form.Password = true
		case codeBox(field):
			form.OTP = true
		case usernameBox(field):
			form.Username = true
		}
	}
	return form
}

// Segmented codes are four to eight boxes: three is a phone number split the
// American way, and nothing asks for nine.
const (
	minSegments = 4
	maxSegments = 8
)

// codeGroup is the run of one-character boxes that is a code, or 0. No single
// box of a split code says "code"; a run of four to eight is one, and a page
// with two such runs is not guessed at.
func codeGroup(fields []Field) int {
	sizes := map[int]int{}
	for _, field := range fields {
		if field.Group > 0 && typedBox(field.Type) {
			sizes[field.Group]++
		}
	}
	found := 0
	for group, size := range sizes {
		if size < minSegments || size > maxSegments {
			continue
		}
		if found > 0 {
			return 0
		}
		found = group
	}
	return found
}

func describe(field Field) string {
	return strings.Join([]string{
		field.Name, field.ID, field.Autocomplete, field.Placeholder, field.AriaLabel,
	}, " ")
}

func codeBox(field Field) bool {
	if field.Type == "password" {
		return false
	}
	if codeAttrWords.MatchString(describe(field)) {
		return true
	}
	return codeSaidWords.MatchString(field.Label) || codeSaidWords.MatchString(field.Context)
}

// usernameBox: a box asking for an email is one whatever it is called (Azure
// AD B2C names its own `signInName`).
func usernameBox(field Field) bool {
	if field.Type == "email" {
		return true
	}
	if field.Type != "text" && field.Type != "tel" {
		return false
	}
	return usernameWords.MatchString(describe(field)) || usernameWords.MatchString(field.Label)
}

// ReadForm is readFormScript over a live page. The trail reads with it too, so
// the trail and the classifier never disagree about what was on the screen.
func ReadForm(page browser.Page) (Form, error) {
	var read Reading
	if err := browser.EvaluateInto(page, readFormScript, nil, &read); err != nil {
		return Form{}, err
	}
	return FormFrom(read), nil
}

// InsideAccount says whether an address is one of the provider's signed-in
// pages. A function rather than a pattern because Go's regular expressions have
// no lookahead, and a sign-on host can count as inside only past its own login
// and logout pages.
type InsideAccount func(url string) bool

func URLMatches(pattern *regexp.Regexp) InsideAccount {
	return func(url string) bool { return pattern.MatchString(url) }
}

// StateOf is the state a page is in, from what the reading saw and where the
// browser stands.
func StateOf(form Form, url string, accountArea InsideAccount) State {
	if isInterstitial(form) {
		return State{State: StateCaptcha, Blocking: true, Prompt: interstitialPrompt}
	}
	if blockedWords.MatchString(form.Text) {
		return State{
			State: StateCaptcha, Blocking: true,
			Prompt: "The site showed a check before its sign-in that did not clear within the wait. " +
				"Nothing typed here answers it; try again later.",
		}
	}
	// Before everything that would act, because each would act on an enrolment:
	// a dialog with one button is a bridge, and a page with a box is a form.
	if enrolmentPage(form) {
		return State{State: StateFailed, Error: enrolmentError, Prompt: enrolmentPrompt("The provider", "its own site")}
	}
	// The choice of factor is read before the code: a control called "Text me a
	// code" is a code box to a reading that only asks what the words are, and
	// answering that page with a code types into a radio button.
	if factorPage(form) {
		return State{State: StateFactor, Prompt: "Choose how the provider should check it is you."}
	}
	if form.OTP && !form.Password {
		return codeState(form)
	}
	if form.Password {
		return State{State: StatePassword}
	}
	if form.Username {
		return State{State: StateEmail}
	}
	inside := (accountArea != nil && accountArea(url)) ||
		form.SignOutLink || signedOutWords.MatchString(form.Text)
	if inside {
		return State{State: StateSignedIn}
	}
	return State{State: StateInteractive}
}

// isInterstitial is a full-page interstitial check. Its widget draws inside
// its own frame, where the page's text does not reach, so the page's other
// words and its title are read instead.
func isInterstitial(form Form) bool {
	return interstitialWords.MatchString(form.Text) || interstitialTitle.MatchString(form.Title)
}

// interstitialPrompt: there is no picture, and a typed answer reaches nothing.
const interstitialPrompt = "The site showed a check in front of its sign-in page that did not " +
	"clear within the wait. Nothing typed here answers it; " +
	"try again later."

const enrolmentError = "the provider asked to set up a security profile first"

func enrolmentPrompt(name, site string) string {
	return name + " wants this login to set up its security profile before it lets anybody in. " +
		"That is a step to take yourself: sign in once on " + site + ", finish the setup there, " +
		"and then connect again."
}

// enrolmentPage is a provider setting up how a login will be verified from now
// on rather than asking for one. It often has a single "Get Started" button,
// which the bridge rule would press; that would choose a household's security
// settings unattended, so the sign-in stops. Only where nothing is being asked
// for already, and only at a dialog or a page with nothing to type into: an
// ordinary sign-in page may mention two-step verification in its small print.
func enrolmentPage(form Form) bool {
	if form.Password || form.OTP || form.SignOutLink {
		return false
	}
	if form.Dialog == "" && form.Boxes > 0 {
		return false
	}
	return enrolmentWords.MatchString(form.Heading + "\n" + form.Text)
}

// codeState is a page asking for a code, in its own words where it has any.
// The channel those words name is the Method, which keeps a kept authenticator
// key from being minted into a box waiting for a text.
func codeState(form Form) State {
	where := State{State: StateOTP, Prompt: "Enter the code the provider sent you."}
	if ask := CodeAsk(form.Text); ask != "" {
		where.Prompt = ask
		where.Method = CodeChannel(ask)
	}
	return where
}

// CodeAsk is the line of a page that asks for a code, or "". A line naming the
// channel beats one that does not, since a heading like "Enter Your Code" names
// none: first an enter line naming a channel, then a sent line naming one, then
// either kind.
func CodeAsk(text string) string {
	var enter, sent, sentNamed string
	for _, line := range strings.Split(text, "\n") {
		line = strings.Join(strings.Fields(line), " ")
		if line == "" || len(line) > 200 {
			continue
		}
		switch {
		case codeEnterWords.MatchString(line):
			if CodeChannel(line) != "" {
				return line
			}
			if enter == "" {
				enter = line
			}
		case codeSentWords.MatchString(line):
			if sentNamed == "" && CodeChannel(line) != "" {
				sentNamed = line
			}
			if sent == "" {
				sent = line
			}
		}
	}
	for _, one := range []string{sentNamed, enter, sent} {
		if one != "" {
			return one
		}
	}
	return ""
}

// CodeChannel is the second factor a code request's words name: totp, email,
// sms, or "". The authenticator first, because "the authenticator app on your
// phone" names a phone too; then e-mail, because an address is not a phone.
func CodeChannel(ask string) string {
	switch {
	case authenticatorWords.MatchString(ask):
		return "totp"
	case codeEmailWords.MatchString(ask):
		return "email"
	case codePhoneWords.MatchString(ask):
		return "sms"
	}
	return ""
}

// KeyAnswers is whether a kept authenticator key answers a code box whose words
// name channel. A box naming no channel is the authenticator's unless the
// household said this login's codes come by e-mail or text: minting into a box
// waiting for a sent code spends a try.
func KeyAnswers(channel, secondFactor string) bool {
	return channel == "totp" ||
		(channel == "" && secondFactor != string(domain.SecondFactorEmail) &&
			secondFactor != string(domain.SecondFactorSMS))
}

// MintedCode mints a kept setup key's code as late as possible: under five
// seconds left in the thirty-second step, it sleeps into the next one, or the
// code expires in transit. The code and key are never logged.
func MintedCode(secret string, now func() time.Time, sleep func(time.Duration)) (string, error) {
	if left := totp.Remaining(now()); left < 5*time.Second {
		sleep(left + time.Second)
	}
	return totp.Code(secret, now())
}

// factorPage is the page between the password and the code: it asks which way
// to verify, offers at least one this engine has a name for, and is not a form
// or a signed-in page. Portals write it four ways: a question naming the
// ways; a heading like "Select Method" over submit buttons with no box; a
// radio group (possibly role=radio divs) naming a known way, with no question
// or heading at all; or only where to send a code, over the ways as buttons.
func factorPage(form Form) bool {
	if form.Password || form.Username || form.SignOutLink {
		return false
	}
	if factorQuestion.MatchString(form.Text) &&
		(authenticatorWords.MatchString(form.Text) || textMessageWords.MatchString(form.Text)) {
		return true
	}
	if form.Boxes == 0 && form.Radios > 0 && namesAWay(form.Text) {
		return true
	}
	if form.Boxes == 0 && form.Submits > 0 && codeDeliveryWords.MatchString(form.Text) && namesAWay(form.Text) {
		return true
	}
	// Where to send a code, with the ways as plain buttons ("Get from
	// authenticator app", "Send to my email"): a button that itself names a way
	// is the choice, which a page only mentioning an app never draws.
	if form.Boxes == 0 && codeDeliveryWords.MatchString(form.Heading+"\n"+form.Text) {
		for _, words := range form.Buttons {
			if namesAWay(words) {
				return true
			}
		}
	}
	return form.Boxes == 0 && form.Submits > 0 &&
		factorChoiceWords.MatchString(form.Heading+"\n"+form.Text)
}

// namesAWay is a page naming at least one way a code could reach somebody.
func namesAWay(text string) bool {
	return authenticatorWords.MatchString(text) || textMessageWords.MatchString(text) ||
		emailWords.MatchString(text) || maskedPhoneWords.MatchString(text)
}

// controlTextsScript is every visible control of a selector, as its words.
const controlTextsScript = `(selector) => {
` + agent.VisibleJS + agent.CleanJS + `
  return [...document.querySelectorAll(selector)].filter(visible)
    .map((el) => clean(el.innerText || el.textContent || ''))
    .filter((said) => said !== '').slice(0, 200);
}`

// SignInControls is what the way-in step would follow on this page, in order,
// without pressing it. It reads the classifier's own selector and pattern, for
// the sign-in probe: a probe answering for a different rule would be worse
// than none.
func SignInControls(page browser.Page) ([]string, error) {
	var said []string
	if err := browser.EvaluateInto(page, controlTextsScript, signInControlSelector, &said); err != nil {
		return nil, err
	}
	var found []string
	for _, words := range said {
		if signInControlWords.MatchString(words) {
			found = append(found, words)
		}
	}
	return found, nil
}
