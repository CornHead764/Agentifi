package agent

import "github.com/CornHead764/agentifi/backend/internal/browser"

// SignInModule is a connector signed in to in a page: what one site's sign-in
// looks like, and nothing about sessions, HTTP or the browser. Bill providers
// (billers.BrowserModule) and merchants (merchants.Module) both answer it, and
// internal/connector's one sign-in loop runs over either.
type SignInModule interface {
	// SignInURL is where a sign-in starts.
	SignInURL() string
	// LandingURL is where a signed-in session shows itself, and the page to
	// stand on when the cookies are taken.
	LandingURL() string
	SignInPrompt() string
	// Classify says what the page is showing: email, password, otp, captcha,
	// approval, factor, signed_in, interactive or failed.
	Classify(page browser.Page) (State, error)
	// FillEmail and FillPassword type into the form and send it on, and each
	// says what it did with the form's own controls, which is what the trail
	// and the loop guard read.
	FillEmail(page browser.Page, email string) (Step, error)
	FillPassword(page browser.Page, password, email string) (Step, error)
	// CaptchaImage is the puzzle cut out of the page, base64, where the module
	// can point at one; "" leaves the engine to send the whole screen.
	CaptchaImage(page browser.Page) (string, error)
	// Answer types a code in and says what it did; a Step that did not act
	// means the page was not asking for one. The password comes with it
	// because a page that puts a puzzle in front of the sign-in may clear what
	// was typed under it.
	Answer(page browser.Page, where State, code, password string) (Step, error)
	// ChooseFactor picks a second factor and says what the page was offering
	// either way: the menu it drew, and which of it was taken. A Factor with
	// no Kind is a menu nothing on it could answer, and its Choices are the
	// whole account of why. `prefer` is the login's own choice — "", "email",
	// "sms" or "totp" — and, where the module honours one, the only way taken
	// when it is not "".
	ChooseFactor(page browser.Page, prefer string) (Factor, error)
	// AccountHint is the name the site greets the person by, for a login
	// nobody typed a username into.
	AccountHint(page browser.Page) (string, error)
}

// What a round pressed, for the trail. A control the page drew, or the key a
// plain form is sent with because the page drew nothing that matched.
const (
	PressedButton = "button"
	PressedInput  = "input"
	PressedEnter  = "enter"
)

// Step is what a round did to the page, after it read it, so the trail can
// tell a loop from a round pressing Enter at a form that ignores it. Only the
// site's own words, never the household's.
type Step struct {
	// Acted says the round did something to the page. An Answer at a page that
	// was not asking for a code did not.
	Acted bool
	// Pressed is the kind of control: button, input, or enter for the key
	// pressed because nothing on the page matched or what matched never
	// became pressable.
	Pressed string
	// Words is the control's own text, capped by the page reading. With Enter
	// it is the control that never became pressable, empty when none matched.
	Words string
	// Waited says the control was there and not pressable when read. With a
	// control it became pressable before it was pressed; with Enter it never
	// did.
	Waited bool
	// Changed says the page differed afterwards. A round that changed nothing
	// is the round the loop guard gives up on.
	Changed bool
	// Dismissed is the cookie banner the round declined before it typed —
	// the platform and the words on the control — or "" for none.
	Dismissed string
	// Forced says the control was pressed past Playwright's actionability
	// checks after its click timed out.
	Forced bool
	// Note is how the press landed when a plain click did not, with
	// Playwright's account of the wait (browser.ClickLog), or "".
	Note string
}

// Factor is what a factor page offered and what was taken from it, answered
// whether or not anything was recognised, so a failure can say why.
type Factor struct {
	// Kind is the second factor chosen: "totp", "sms", "email", "push", or
	// "" for a menu nothing on it could answer.
	Kind    string
	Choices []FactorChoice
	Chose   string
	// Confirmed says the choice selected rather than sent, and the page's own
	// button was pressed after it.
	Confirmed bool
	Step      Step
	// Forced says the choice was taken past the page's actionability checks. On
	// the trail because a sign-in that only works forced is one to look at.
	Forced bool
}
