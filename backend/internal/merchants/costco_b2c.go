package merchants

import (
	"cmp"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// Costco's sign-in is an Azure AD B2C custom policy. Every step of one draws
// into a `#api` container whose `data-name` names the step ("Unified",
// "SelfAsserted", "Phonefactor", ...), shows its errors in `.error.pageLevel`
// and `.error.itemLevel` elements that stay in the page hidden until they
// speak, and covers the form with `.working` while a press is posted. The
// classifier reads those rather than one step's layout, so a step it has no
// rule for still says what it was.

// costcoReading is the shared reading with what B2C draws beside it, taken by
// the same one script.
type costcoReading struct {
	reading
	B2C b2cReading `json:"b2c"`
}

type b2cReading struct {
	// Step is `#api`'s data-name, "" off a B2C page.
	Step string `json:"step"`
	// Framed is a `#api` on the page; Drawn is one with something in it.
	Framed bool `json:"framed"`
	Drawn  bool `json:"drawn"`
	// Busy is a document still loading or a press still being posted.
	Busy       bool     `json:"busy"`
	PageErrors []string `json:"pageErrors"`
	ItemErrors []string `json:"itemErrors"`
	// Sent is the verification control's word on the code it sent, shown
	// once the send has been answered.
	Sent    []string    `json:"sent"`
	Heading string      `json:"heading"`
	Buttons []b2cButton `json:"buttons"`
	Radios  []b2cRadio  `json:"radios"`
}

type b2cButton struct {
	// Selector is `#id`, or "" for a button with none.
	Selector string `json:"selector"`
	ID       string `json:"id"`
	Words    string `json:"words"`
	Disabled bool   `json:"disabled"`
}

// b2cRadio is one way to verify. Target is what a person presses (its label
// when it has one) and Radio the input itself, for the tick Choose falls back
// to.
type b2cRadio struct {
	Words   string `json:"words"`
	Target  string `json:"target"`
	Radio   string `json:"radio"`
	Checked bool   `json:"checked"`
}

// costcoReadScript runs the shared reading and adds the B2C facts. An error
// element counts only while it is drawn and not aria-hidden: B2C keeps every
// field's "This information is required." in the page from the start. A
// button's words are its label, never a field's value.
const costcoReadScript = `(groups) => {` + agent.DrawnJS + agent.CleanJS + agent.EscapedJS + agent.NameOfJS + `
  const read = (` + readPageScript + `)(groups);
  const shown = (el) => agentifiDrawn(el) && el.getAttribute('aria-hidden') !== 'true';
  const said = (selector) => [...document.querySelectorAll(selector)]
    .filter(shown).map((el) => clean(el.innerText)).filter((t) => t !== '');
  const api = document.getElementById('api');
  const heading = [...document.querySelectorAll('h1, h2, [role="heading"]')].find(agentifiDrawn);
  // A button's own content names it, never an external label.
  const ownWords = (el) => el.tagName === 'INPUT'
    ? clean(el.value || el.getAttribute('aria-label'))
    : clean(el.innerText || el.getAttribute('aria-label'));
  const buttons = [...document.querySelectorAll(
    'button, input[type="submit"], input[type="button"], [role="button"]')]
    .filter(agentifiDrawn)
    .map((el) => ({
      selector: el.id ? '#' + escaped(el.id) : '',
      id: el.id || '',
      words: ownWords(el).slice(0, 60),
      disabled: el.disabled === true || el.getAttribute('aria-disabled') === 'true',
    }))
    .filter((one) => one.words !== '')
    .slice(0, 12);
  const radios = [...document.querySelectorAll('input[type="radio"]')].flatMap((el) => {
    const bound = boundTo(el);
    const label = labelOf(el);
    if (!agentifiDrawn(el) && !agentifiDrawn(label)) return [];
    const radio = el.id ? '#' + escaped(el.id)
      : 'input[type="radio"][name="' + escaped(el.name || '') + '"][value="' + escaped(el.value || '') + '"]';
    const target = bound && agentifiDrawn(bound) ? 'label[for="' + escaped(el.id) + '"]' : radio;
    const words = nameOf(el);
    return words === '' ? [] : [{ words: words.slice(0, 120), target, radio, checked: el.checked === true }];
  }).slice(0, 10);
  read.b2c = {
    step: api ? (api.getAttribute('data-name') || '') : '',
    framed: Boolean(api),
    drawn: Boolean(api && clean(api.innerText) !== ''),
    busy: document.readyState !== 'complete' ||
      [...document.querySelectorAll('.working, #simplemodal-overlay, [aria-busy="true"]')].some(agentifiDrawn),
    pageErrors: said('.error.pageLevel'),
    itemErrors: said('.error.itemLevel'),
    sent: said('.verificationInfoText'),
    heading: heading ? clean(heading.innerText).slice(0, 80) : '',
    buttons,
    radios,
  };
  return read;
}`

func readCostco(page browser.Page) (costcoReading, error) {
	var out costcoReading
	if err := browser.EvaluateInto(page, costcoReadScript, costcoGroups, &out); err != nil {
		return costcoReading{}, err
	}
	out.complete(page)
	return out, nil
}

// A step still drawing is read again every costcoRecheck, costcoLooks times
// at most, after the first settle.
const (
	costcoLooks   = 10
	costcoRecheck = time.Second
)

// unsettled is a sign-in page with nothing to go on yet: B2C's frame empty, a
// press being posted, or the document still loading.
func (r costcoReading) unsettled() bool {
	return r.B2C.Busy || (r.B2C.Framed && !r.B2C.Drawn) ||
		(costcoAuthPage.MatchString(r.URL) && strings.TrimSpace(r.Text) == "")
}

// siteError is what B2C wrote about the last press: the page-level message
// when there is one, the fields' own otherwise.
func (r costcoReading) siteError() string {
	if len(r.B2C.PageErrors) > 0 {
		return strings.Join(r.B2C.PageErrors, " ")
	}
	return strings.Join(r.B2C.ItemErrors, " ")
}

// codePrompt is what the code box is asked for with: B2C's sent-to message
// when it shows one, the page's own words otherwise. The prompt group's text
// is its first visible match in document order, which on a B2C step is the
// heading above the message.
func (r costcoReading) codePrompt() string {
	if len(r.B2C.Sent) > 0 {
		return strings.Join(r.B2C.Sent, " ")
	}
	return r.textOf("prompt")
}

var costcoSendCode = regexp.MustCompile(`(?i)\bsend\b.*\bcode\b|\bsend (?:me )?(?:an? |the )?(?:e-?mail|text)\b|^(?:e-?mail|text) me\b`)

// sendButton is the enabled button that asks B2C to send a code: the email
// verification control's, the phone factor's, or one that says so.
func (r costcoReading) sendButton() (b2cButton, bool) {
	for _, button := range r.B2C.Buttons {
		if button.Disabled {
			continue
		}
		if costcoSendCodeID.MatchString(button.ID) || costcoSendCode.MatchString(button.Words) {
			return button, true
		}
	}
	return b2cButton{}, false
}

var costcoSendCodeID = regexp.MustCompile(`(?i)^sendCode$|_but_send_code$|^email_ver_but_send$`)

// factorChoices is the menu as the loop reports it: the radios when there are
// any, the send button otherwise.
func (r costcoReading) factorChoices() []FactorChoice {
	var out []FactorChoice
	for at, radio := range r.B2C.Radios {
		out = append(out, FactorChoice{
			At: at, Kind: billers.FactorKind(radio.Words), Words: costcoMasked(radio.Words),
			Selects: true, Checked: radio.Checked,
		})
	}
	if len(out) > 0 {
		return out
	}
	if button, found := r.sendButton(); found {
		out = append(out, FactorChoice{Kind: billers.FactorKind(button.Words), Words: costcoMasked(button.Words)})
	}
	return out
}

// costcoFactorRank orders the ways Costco offers to verify: an e-mailed code
// first, which the household's mailbox can answer on a pull, then an
// authenticator app, which a kept key answers, then a text. A phone call has
// no code to type, and a choice with no words this app knows is not taken.
func costcoFactorRank(words string) int {
	switch billers.FactorKind(words) {
	case "email":
		return 1
	case "totp":
		return 2
	case "sms":
		return 3
	}
	return 0
}

func bestRadio(radios []b2cRadio) (b2cRadio, bool) {
	best, bestRank := b2cRadio{}, 0
	for _, radio := range radios {
		rank := costcoFactorRank(radio.Words)
		if rank != 0 && (bestRank == 0 || rank < bestRank) {
			best, bestRank = radio, rank
		}
	}
	return best, bestRank != 0
}

// costcoContinue is the button that sends a chosen way on: the send button
// when the step has one, then B2C's own.
const costcoContinue = `#sendCode, [id$="_but_send_code"], #email_ver_but_send, #continue, #next, button[type="submit"]`

// ChooseFactor takes the best way to verify by Costco's own order
// (costcoFactorRank, whatever the login prefers) and asks for the code, or
// presses the step's send button. A Factor with no Kind is a page with nothing
// this app can take.
func (m *costcoModule) ChooseFactor(page browser.Page, _ string) (agent.Factor, error) {
	read, err := readCostco(page)
	if err != nil {
		return agent.Factor{}, err
	}
	factor := agent.Factor{Choices: read.factorChoices()}
	if len(read.B2C.Radios) > 0 {
		pick, found := bestRadio(read.B2C.Radios)
		if !found {
			return factor, nil
		}
		if !pick.Checked {
			took, err := page.Choose(pick.Target, pick.Radio)
			if err != nil || took == "" {
				return factor, err
			}
			factor.Forced = took == browser.TookForce
			page.Sleep(agent.Settle)
		}
		step, err := agent.Submit(page, costcoContinue)
		if err != nil {
			return factor, err
		}
		factor.Kind, factor.Chose = billers.FactorKind(pick.Words), costcoMasked(pick.Words)
		factor.Confirmed, factor.Step = true, step
		return factor, nil
	}
	button, found := read.sendButton()
	if !found {
		return factor, nil
	}
	before := agent.Signature(page)
	step := agent.Step{Acted: true, Pressed: agent.PressedButton, Words: costcoMasked(button.Words)}
	pressed := false
	if button.Selector != "" {
		if pressed, err = page.ClickVisible(button.Selector); err != nil {
			return factor, err
		}
	}
	if !pressed {
		if pressed, err = page.ClickText(`button, input[type="submit"], input[type="button"], [role="button"]`,
			costcoSendCode); err != nil || !pressed {
			return factor, err
		}
	}
	factor.Kind = cmp.Or(billers.FactorKind(button.Words), costcoSentCode)
	factor.Chose = costcoMasked(button.Words)
	factor.Step = agent.Changed(page, before, step)
	return factor, nil
}

// costcoSentCode is the Kind of a send button whose words name no channel:
// the step asks for a code, and the next page says where it went.
const costcoSentCode = "code"

// costcoVerifyButton confirms a typed code where the step has a button for
// that, which comes before the step's own Continue.
const costcoVerifyButton = `#verifyCode, [id$="_but_verify_code"], #email_ver_but_verify`

// costcoMasked keeps an address or a number a page printed about the member
// out of what a failure says.
func costcoMasked(words string) string {
	words = costcoAddress.ReplaceAllString(words, "…")
	return strings.TrimSpace(costcoDigits.ReplaceAllString(words, "…"))
}

var (
	costcoAddress = regexp.MustCompile(`\S+@\S+`)
	costcoDigits  = regexp.MustCompile(`\d{2,}`)
)

// unrecognised says what was on a page the classifier has no rule for: where
// it was (host and path only, since a sign-in page's query carries the site's
// own state), the B2C step, its heading and its buttons, so the next report
// says what to add.
func (r costcoReading) unrecognised() State {
	said := "unrecognised page at " + hostAndPath(r.URL)
	var parts []string
	if r.B2C.Step != "" {
		parts = append(parts, "B2C step "+strconv.Quote(r.B2C.Step))
	}
	if heading := costcoMasked(cmp.Or(r.B2C.Heading, r.Title)); heading != "" {
		parts = append(parts, "heading "+strconv.Quote(heading))
	}
	var buttons []string
	for _, button := range r.B2C.Buttons {
		buttons = append(buttons, strconv.Quote(costcoMasked(button.Words)))
	}
	if len(buttons) > 0 {
		parts = append(parts, "buttons "+strings.Join(buttons, ", "))
	}
	if len(parts) > 0 {
		said += ": " + strings.Join(parts, "; ")
	}
	return State{
		State:  StateFailed,
		Prompt: "Costco showed a page the agent does not recognise",
		Error:  said,
	}
}

func hostAndPath(at string) string {
	parsed, err := url.Parse(at)
	if err != nil || parsed.Host == "" {
		return "an unreadable address"
	}
	return parsed.Host + parsed.Path
}
