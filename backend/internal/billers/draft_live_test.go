package billers

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/config"
)

// The page reading against a real Chromium, the only place the reading script
// can be asserted. Guarded by AGENTIFI_BROWSER_TEST (see
// internal/browser/engine_test.go):
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/
//
// Both pages are invented: a login form with a display:none decoy above its
// username box and a complaint box whose wording carries none of the
// complaint words, and a second-factor page whose ways are radios named after
// codes.
func TestThePageReadingSeesWhatAPersonWouldAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const login = `<!doctype html><title>Sign On</title><body>
<form id="loginForm" novalidate>
  <input id="oldCookieName" name="oldCookieName" type="email" style="display:none">
  <label for="username">*Email</label>
  <input id="username" name="username" type="email" placeholder="name@example.test">
  <div class="error-message-animate" id="emailValidation">Please enter a valid email address.</div>
  <label for="password">*Password</label>
  <input id="password" name="password" type="password">
  <input type="checkbox" id="checkbox_rememberemail">
  <button id="loginbtn" type="submit">LOG IN</button>
</form></body>`
	const factor = `<!doctype html><title>Verify</title><body>
<h1>How would you like us to verify it is you?</h1>
<label><input type="radio" name="textCode" id="textCode"> Text message</label>
<label><input type="radio" name="authApp" id="authApp"> Authenticator app</label>
<button type="submit">Continue</button></body>`

	// Two pages an identity provider swaps in without changing the address:
	// the ways to verify as submit buttons, and a code box named only by its
	// label.
	const method = `<!doctype html><title>Sign On</title><body>
<h1>Select Method</h1>
<p>Select an MFA method to use to sign on to your account.</p>
<form id="methodForm">
  <button type="submit"><img alt=""><span>Phone app</span><span>Default</span>
    <span>Use an authenticator app of your choosing that generates a security code to authenticate. (Better)</span></button>
  <button type="submit"><img alt=""><span>This device</span>
    <span>Use a fingerprint, facial scan, or a security key to authenticate on your mobile device or desktop.</span></button>
</form>
<form id="hiddenForm"><input type="hidden" name="method"><input type="hidden" name="nonce"></form>
<a href="#reset">Reset multifactor authentication</a></body>`
	const passcode = `<!doctype html><title>Sign On</title><body>
<form id="codeForm" onsubmit="event.preventDefault()">
  <h1>Authenticator App</h1>
  <p>Enter the code from your authenticator app.</p>
  <label for="react-aria-42">Passcode</label>
  <input id="react-aria-42" type="text">
  <button type="submit">Continue</button>
</form>
<a href="#cancel">Cancel</a></body>`
	// The same code box with no visible label at all, named only by its own
	// aria-label.
	const passcodeAria = `<!doctype html><title>Sign On</title><body>
<form id="codeForm" onsubmit="event.preventDefault()">
  <h1>Authenticator App</h1>
  <p>Enter the code from your authenticator app.</p>
  <input id="react-aria-43" type="text" aria-label="Passcode">
  <button type="submit">Continue</button>
</form></body>`

	page, base, done := livePageOver(t, map[string]string{
		"/login": login, "/verify": factor, "/method": method, "/passcode": passcode,
		"/passcode-aria": passcodeAria,
	})
	defer done()

	require.NoError(t, page.Goto(base+"login"))
	form, err := ReadForm(page)
	require.NoError(t, err)
	require.True(t, form.Password)
	require.True(t, form.Username)
	require.False(t, form.OTP)
	require.Equal(t, map[string]int{"email": 1, "password": 1, "checkbox": 1}, form.Inputs,
		"the display:none decoy is not a box anybody could type in")
	require.Contains(t, form.Error, "valid email address",
		"a box the page built to hold a complaint is read whatever it says")
	require.Equal(t, StatePassword, StateOf(form, "https://example.test/login", nil).State)

	require.NoError(t, page.Goto(base+"verify"))
	choice, err := ReadForm(page)
	require.NoError(t, err)
	require.False(t, choice.OTP, "a radio named after a code is not a box to type one into")
	require.Equal(t, map[string]int{"radio": 2}, choice.Inputs)
	require.Equal(t, StateFactor, StateOf(choice, "https://example.test/verify", nil).State)

	require.NoError(t, page.Goto(base+"method"))
	method2, err := ReadForm(page)
	require.NoError(t, err)
	require.False(t, method2.OTP)
	require.Equal(t, 0, method2.Boxes, "two hidden inputs are nothing anybody can type into")
	require.Equal(t, 2, method2.Submits)
	require.Equal(t, StateFactor, StateOf(method2, "https://authnprd.example.test/saml20/idp/sso", nil).State,
		"a page that asks nothing and lists the ways is still the choice of factor")

	require.NoError(t, page.Goto(base+"passcode"))
	code, err := ReadForm(page)
	require.NoError(t, err)
	require.True(t, code.OTP, "the label over the box is the only thing that says what it is for")
	require.False(t, code.Username)
	require.Equal(t, map[string]int{"text": 1}, code.Inputs)
	require.Equal(t, StateOTP, StateOf(code, "https://authnprd.example.test/saml20/idp/sso", nil).State)

	require.NoError(t, page.Goto(base+"passcode-aria"))
	codeAria, err := ReadForm(page)
	require.NoError(t, err)
	require.True(t, codeAria.OTP, "an aria-label with no visible label still names the box")
	require.Equal(t, map[string]int{"text": 1}, codeAria.Inputs)
	require.Equal(t, StateOTP, StateOf(codeAria, "https://authnprd.example.test/saml20/idp/sso", nil).State)

	// And the code goes in it: the union of names finds nothing here, so the
	// box the label is over is the box that is filled.
	require.NoError(t, page.Goto(base+"passcode"))
	step, err := Draft{}.Answer(page, State{State: StateOTP}, "123456", "")
	require.NoError(t, err)
	require.True(t, step.Acted)
	typed, err := page.Evaluate(`() => document.getElementById('react-aria-42').value`, nil)
	require.NoError(t, err)
	require.Equal(t, "123456", typed)
}

// The sign-in itself, against a real Chromium and a portal that answers the
// post. Three ways a typed sign-in lands back on its form: the fill hits a
// hidden decoy above the real box; the widget never hears the value, because
// it keeps its own state and hears only typing's events; or the press is read
// before the answer arrives. So the page is that widget, posting what its
// state holds, and the assertion is what the server received. Every word is
// invented; the shape is a portal's.
func TestATypedSignInPostsWhatWasTypedAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const login = `<!doctype html><title>Sign On</title><body>
<form id="loginForm" method="post" action="/sso" novalidate>
  <h1>Sign On</h1>
  <input id="oldCookieName" name="oldCookieName" type="email" style="display:none" value="held@example.test">
  <label for="username">*Email</label>
  <input id="username" type="email">
  <label for="password">*Password</label>
  <input id="password" type="password">
  <input type="checkbox" id="checkbox_rememberemail">
  <label for="checkbox_rememberemail">Remember my email</label>
  <button id="loginbtn" type="submit">LOG IN</button>
  <input type="hidden" name="username" id="sentUsername">
  <input type="hidden" name="password" id="sentPassword">
</form>
<script>
  // The widget's own state: it hears a value only through the events typing
  // raises, and what it posts is what it heard.
  const held = { username: '', password: '' };
  for (const box of ['username', 'password']) {
    document.getElementById(box).addEventListener('input', (event) => {
      held[box] = event.target.value;
    });
  }
  document.getElementById('loginForm').addEventListener('submit', () => {
    document.getElementById('sentUsername').value = held.username;
    document.getElementById('sentPassword').value = held.password;
  });
</script></body>`
	const method = `<!doctype html><title>Sign On</title><body>
<h1>Select Method</h1>
<p>Select an MFA method to use to sign on to your account.</p>
<form id="methodForm">
  <button type="submit"><span>Phone app</span>
    <span>Use an authenticator app of your choosing that generates a security code.</span></button>
  <button type="submit"><span>This device</span>
    <span>Use a fingerprint, facial scan, or a security key.</span></button>
</form></body>`

	// The hop in front of the form: a hidden SAMLRequest under a single
	// Continue, which a script usually presses and this browser does not.
	const bridge = `<!doctype html><title>Sign On</title><body>
<form method="post" action="/sso">
  <input type="hidden" name="SAMLRequest" value="aW52ZW50ZWQ=">
  <input type="submit" value="Continue">
</form>
<p>Note: Your browser does not support JavaScript, Press Continue to proceed...</p></body>`

	var posted, crossed url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			// The bridge's post is answered with the form; the form's post
			// with the page after it.
			if r.PostForm.Get("SAMLRequest") != "" {
				crossed = r.PostForm
				_, _ = io.WriteString(w, login)
				return
			}
			posted = r.PostForm
			_, _ = io.WriteString(w, method)
			return
		}
		if r.URL.Path == "/policy" {
			_, _ = io.WriteString(w, bridge)
			return
		}
		_, _ = io.WriteString(w, login)
	})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"policy"))
	where, err := module.Classify(page)
	require.NoError(t, err)
	defer Forget(page)
	require.Equal(t, "aW52ZW50ZWQ=", crossed.Get("SAMLRequest"), "the bridge was pressed, not read")
	require.Equal(t, 1, where.Bridged, "and the trail is told that it was")
	require.Equal(t, StatePassword, where.State, "both boxes on one form is the password step")

	filled, err := module.FillPassword(page, "invented-password", "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, agent.PressedButton, filled.Pressed)
	require.Equal(t, "LOG IN", filled.Words, "the round says what it pressed, in the page's own words")
	require.True(t, filled.Changed)

	require.Equal(t, "someone@example.test", posted.Get("username"),
		"the address goes in the box a person could have typed in, and the widget hears it")
	require.Equal(t, "invented-password", posted.Get("password"))
	require.Equal(t, "held@example.test", posted.Get("oldCookieName"),
		"the decoy is left holding whatever it held")

	next, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFactor, next.State,
		"the page that came back is read, not the form that was sent")
}

// The two-step sign-in, against a real Chromium. The only control a person
// can press is a type-less `<button>` saying Next, disabled until the box
// holds something, beside "Sign up"; the only type="submit" on the page is an
// invisible "OK", and the form ignores Enter. Every word is invented; the
// shape is a portal's.
func TestATwoStepSignInPressesTheButtonThatSaysNextAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const username = `<!doctype html><title>Log In</title><body>
<h1>Log in</h1>
<form id="step" method="post" action="/next">
  <label for="usr">Email or phone</label>
  <input id="usr" name="usr" type="text" autocomplete="off">
  <button id="next" disabled>Next</button>
  <button id="up" onclick="event.preventDefault()">Sign up</button>
  <button type="submit" style="display:none">OK</button>
</form>
<script>
  // The widget hears the value through the events typing raises and enables
  // its own button on them, and nothing it draws answers the Enter key.
  const box = document.getElementById('usr');
  box.addEventListener('input', () => {
    // Enabled a beat later, the way a form that validates on a debounce does:
    // the button is there and not pressable at the moment the fill looks.
    setTimeout(() => { document.getElementById('next').disabled = box.value === ''; }, 300);
  });
  document.getElementById('step').addEventListener('keydown', (event) => {
    if (event.key === 'Enter') event.preventDefault();
  });
</script></body>`
	const password = `<!doctype html><title>Log In</title><body>
<h1>Enter your password</h1>
<form method="post" action="/done">
  <label for="pwd">Password</label>
  <input id="pwd" name="pwd" type="password">
  <button>Sign in</button>
</form></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, password)
			return
		}
		_, _ = io.WriteString(w, username)
	})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"signin"))
	where, err := module.Classify(page)
	require.NoError(t, err)
	defer Forget(page)
	require.Equal(t, StateEmail, where.State, "one box and no password is the username step")

	// The probe's view: the hidden OK is not among the controls, and Next is
	// there and not yet pressable.
	controls, err := SubmitControls(page)
	require.NoError(t, err)
	said := make([]string, 0, len(controls))
	for _, control := range controls {
		said = append(said, control.Words)
	}
	require.Equal(t, []string{"Next", "Sign up"}, said, "a control nobody can see is not a control")
	best, found := BestSubmit(controls)
	require.True(t, found)
	require.Equal(t, "Next", best.Words)
	require.True(t, best.Disabled, "the widget enables it on the event the fill raises")

	step, err := module.FillEmail(page, "someone@example.test")
	require.NoError(t, err)

	require.Equal(t, "someone@example.test", posted.Get("usr"), "the form was sent, not ignored")
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.Equal(t, "Next", step.Words)
	require.True(t, step.Waited, "it was not pressable when it was found")
	require.True(t, step.Changed, "and the page it landed on is a different page")

	next, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StatePassword, next.State)
}

// The choice of second factor, against a real Chromium: three radios whose
// labels wrap their inputs, one an authenticator app, and a Continue. Both
// halves are asserted, because getting either wrong is a stall: the right
// radio is selected, and Continue is then pressed. Every word is invented; the
// shape is a portal's.
func TestAFactorPageOfRadiosIsChosenAndThenConfirmedAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	// The radio is named by the wrapping label and nothing of its own, and the
	// third choice by aria-labelledby.
	const choose = `<!doctype html><title>Verify</title><body>
<h1>How would you like us to verify it is you?</h1>
<form method="post" action="/verified">
  <label class="choice"><input type="radio" name="method" value="sms"> <span>Text message</span></label>
  <label class="choice"><input type="radio" name="method" value="app"> <span>Authenticator app</span></label>
  <span id="keyLabel">Security key</span>
  <input type="radio" name="method" value="key" aria-labelledby="keyLabel">
  <button type="submit">Continue</button>
</form></body>`
	const code = `<!doctype html><title>Verify</title><body>
<form>
  <h1>Authenticator App</h1>
  <label for="react-aria-7">Passcode</label>
  <input id="react-aria-7" type="text">
  <button type="submit">Continue</button>
</form></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, code)
			return
		}
		_, _ = io.WriteString(w, choose)
	})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"choosemethod"))
	where, err := module.Classify(page)
	require.NoError(t, err)
	defer Forget(page)
	require.Equal(t, StateFactor, where.State, "three radios and no box to type in is the choice of factor")

	// The menu as a person reads it, however each option is named.
	offered, err := FactorChoices(page)
	require.NoError(t, err)
	said := make([]string, 0, len(offered))
	for _, choice := range offered {
		said = append(said, choice.Words)
		require.Truef(t, choice.Selects, "%q selects rather than sends", choice.Words)
		require.Equal(t, "radio", choice.Kind)
	}
	require.Equal(t, []string{"Text message", "Authenticator app", "Security key"}, said,
		"a label that wraps its input names it, and so does aria-labelledby — "+
			"and the Continue beside them is how the menu is sent, not a way to verify")

	factor, err := module.ChooseFactor(page, "")
	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind)
	require.Equal(t, "Authenticator app", factor.Chose)
	require.Len(t, factor.Choices, 3, "the whole menu is answered, not only the one taken")
	require.True(t, factor.Confirmed, "a radio selects; the page's own button is what sends it")
	require.Equal(t, "Continue", factor.Step.Words)

	require.Equal(t, "app", posted.Get("method"),
		"the radio that was selected is the one the form sent")

	next, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateOTP, next.State, "and the page it landed on is the code box")
}

// The custom radio: the real input is visible, and an empty label bound to it
// by `for` is stretched over it and swallows every click on the input. Both
// halves are asserted, and so is the time: a click that cannot land is a
// failure to report in a moment, not thirty seconds of retries. Every word is
// invented; the shape is a portal's.
func TestARadioUnderItsOwnLabelIsChosenThroughItAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	// The label carries no text, so the name has to come off the input's own
	// aria-label.
	const choose = `<!doctype html><title>Verify</title>
<style>
  .choice { position: relative; display: block; padding: 12px 12px 12px 36px; }
  .choice label { position: absolute; inset: 0; cursor: pointer; }
</style>
<body>
<h1>How would you like us to verify it is you?</h1>
<form method="post" action="/verified">
  <div class="choice">
    <input type="radio" id="sms_otp" name="choosemethod" value="sms" aria-label="Text me a code">
    <span>Text message</span>
    <label for="sms_otp"></label>
  </div>
  <div class="choice">
    <input type="radio" id="google_otp" name="choosemethod" value="app" aria-label="Use Google Authenticator">
    <span>Authenticator app</span>
    <label for="google_otp"></label>
  </div>
  <div class="choice">
    <input type="radio" id="key_otp" name="choosemethod" value="key" aria-label="Use a security key">
    <span>Security key</span>
    <label for="key_otp"></label>
  </div>
  <button type="submit">Continue</button>
</form></body>`
	const code = `<!doctype html><title>Verify</title><body>
<form>
  <h1>Authenticator App</h1>
  <label for="react-aria-11">Passcode</label>
  <input id="react-aria-11" type="text">
  <button type="submit">Continue</button>
</form></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, code)
			return
		}
		_, _ = io.WriteString(w, choose)
	})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"choosemethod"))
	where, err := module.Classify(page)
	require.NoError(t, err)
	defer Forget(page)
	require.Equal(t, StateFactor, where.State)

	offered, err := FactorChoices(page)
	require.NoError(t, err)
	said := make([]string, 0, len(offered))
	for _, choice := range offered {
		said = append(said, choice.Words)
	}
	require.Equal(t, []string{"Text me a code", "Use Google Authenticator", "Use a security key"}, said,
		"an empty label names nothing; the input's own aria-label is what a screen reader reads")

	started := time.Now()
	factor, err := module.ChooseFactor(page, "")
	require.NoError(t, err)
	require.Less(t, time.Since(started), 10*time.Second,
		"a choice is taken or given up on in a moment, not in half a minute")
	require.Equal(t, "totp", factor.Kind)
	require.Equal(t, "Use Google Authenticator", factor.Chose)
	require.True(t, factor.Confirmed, "a radio selects; the page's own button is what sends it")
	require.False(t, factor.Forced, "the label is what a person clicks, so nothing had to be forced")
	require.Equal(t, "Continue", factor.Step.Words)

	require.Equal(t, "app", posted.Get("choosemethod"),
		"the radio under the label that was clicked is the one the form sent")

	next, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateOTP, next.State, "and the page it landed on is the code box")
}

// The same page with nothing bound to its radios and a sheet over them: a
// choice that genuinely cannot be taken. The click, the tick and the forced
// tick are all refused, and the answer is a sentence naming the choice, in a
// moment, not Playwright's call log.
func TestAChoiceThatCannotBeClickedFailsFastAndSaysWhichItWasAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const choose = `<!doctype html><title>Verify</title>
<style>
  .sheet { position: fixed; inset: 0; z-index: 9; }
</style>
<body>
<h1>How would you like us to verify it is you?</h1>
<form method="post" action="/verified">
  <input type="radio" name="choosemethod" value="sms" aria-label="Text me a code">
  <input type="radio" name="choosemethod" value="app" aria-label="Use Google Authenticator">
  <button type="submit">Continue</button>
</form>
<div class="sheet"></div></body>`

	page, base, done := livePageOver(t, map[string]string{"/choosemethod": choose})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"choosemethod"))
	defer Forget(page)

	started := time.Now()
	factor, err := module.ChooseFactor(page, "")
	require.Error(t, err)
	require.Less(t, time.Since(started), 25*time.Second,
		"a click that cannot land is given up on, not retried for half a minute")
	require.Contains(t, err.Error(), `"Use Google Authenticator"`,
		"the failure names the choice it tried")
	require.NotContains(t, err.Error(), "Call log",
		"and says it in the trail's own voice rather than the driver's")
	require.Equal(t, "Use Google Authenticator", factor.Chose)
	require.Len(t, factor.Choices, 2,
		"the round that failed while choosing still says what it had found")
}

// Each choice is itself a `button[type=submit]`, so the click has already sent
// the page on and a confirm press would land on whatever it sent it to. The
// link under the buttons is not a way to verify.
func TestAFactorPageOfSubmitButtonsIsPressedOnceAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const method = `<!doctype html><title>Sign On</title><body>
<h1>Select Method</h1>
<p>Select an MFA method to use to sign on to your account.</p>
<form method="post" action="/chosen">
  <button type="submit" name="method" value="app"><span>Phone app</span>
    <span>Use an authenticator app of your choosing that generates a security code.</span></button>
  <button type="submit" name="method" value="device"><span>This device</span>
    <span>Use a fingerprint, facial scan, or a security key.</span></button>
</form>
<a href="#reset">Reset multifactor authentication</a></body>`
	const code = `<!doctype html><title>Sign On</title><body>
<form>
  <h1>Authenticator App</h1>
  <label for="react-aria-9">Passcode</label>
  <input id="react-aria-9" type="text">
  <button type="submit">Continue</button>
</form></body>`

	posts := 0
	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posts++
			posted = r.PostForm
			_, _ = io.WriteString(w, code)
			return
		}
		_, _ = io.WriteString(w, method)
	})
	defer done()

	module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
	require.NoError(t, page.Goto(base+"method"))
	where, err := module.Classify(page)
	require.NoError(t, err)
	defer Forget(page)
	require.Equal(t, StateFactor, where.State)

	factor, err := module.ChooseFactor(page, "")
	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind)
	require.False(t, factor.Confirmed, "a submit button has already sent the page on")
	require.False(t, factor.Step.Acted, "so nothing is pressed after it")
	require.Equal(t, "app", posted.Get("method"))
	require.Equal(t, 1, posts, "once, and only once")

	// The link was read as a choice and ranked nothing.
	require.Equal(t, "Reset multifactor authentication",
		factor.Choices[len(factor.Choices)-1].Words)
	require.Equal(t, 0, FactorRank(factor.Choices[len(factor.Choices)-1]))
}

// livePageOver serves the given documents and answers a page standing at the
// server's root, with the root's address to build the others from.
func livePageOver(t *testing.T, documents map[string]string) (browser.Page, string, func()) {
	t.Helper()
	return livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		body, found := documents[r.URL.Path]
		if r.URL.Path == "/" {
			body, found = "<!doctype html><title>root</title>", true
		}
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	})
}

// liveBrowserSettings is the browser settings the environment gives, as serve
// reads them.
func liveBrowserSettings(t *testing.T) config.Browser {
	t.Helper()
	settings, err := config.LoadBrowser()
	require.NoError(t, err)
	return settings
}

// livePageServing is the same over a site that answers for itself, for the
// tests where what the portal was sent matters as much as what it showed.
func livePageServing(
	t *testing.T, answer func(w http.ResponseWriter, r *http.Request),
) (browser.Page, string, func()) {
	t.Helper()
	site := httptest.NewServer(http.HandlerFunc(answer))
	engine := browser.NewEngine(liveBrowserSettings(t))
	context, err := engine.NewContext("", browser.DefaultViewport)
	require.NoError(t, err)
	opened, err := browser.OpenPage(context)
	require.NoError(t, err)
	page := browser.Wrap(opened)
	require.NoError(t, page.Goto(site.URL+"/"))
	return page, site.URL + "/", func() {
		_ = context.Close()
		_ = engine.Close()
		site.Close()
	}
}

// The cookie banner in front of a sign-in form, against a real Chromium: a
// OneTrust banner with a filter over the page takes every click. The fill
// declines it before typing, and what is asserted is what the server was sent:
// the form, and a rejection rather than a consent. Every word is invented; the
// shape is the platform's.
func TestACookieBannerIsDeclinedAndTheFormSentAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const login = `<!doctype html><title>Log In</title><body>
<form method="post" action="/login">
  <h1>Log In</h1>
  <label for="username">Username</label><input id="username" name="username" autocomplete="username">
  <label for="password">Password</label><input id="password" name="password" type="password">
  <input type="hidden" name="consent" id="consent" value="unanswered">
  <button id="login" type="submit">Log In</button>
</form>
<div class="onetrust-pc-dark-filter" style="position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:1000"></div>
<div id="onetrust-banner-sdk" style="position:fixed;bottom:0;left:0;right:0;z-index:1001;background:#fff;padding:20px">
  <p>We use cookies.</p>
  <button id="onetrust-reject-all-handler">Opt Out</button>
  <button id="onetrust-accept-btn-handler">Accept Cookies</button>
  <div id="onetrust-close-btn-container"><button class="onetrust-close-btn-handler" aria-label="Close"></button></div>
</div>
<script>
  const answer = (said) => () => {
    document.getElementById('consent').value = said;
    document.getElementById('onetrust-banner-sdk').style.display = 'none';
    document.querySelector('.onetrust-pc-dark-filter').style.display = 'none';
  };
  document.getElementById('onetrust-reject-all-handler').onclick = answer('rejected');
  document.getElementById('onetrust-accept-btn-handler').onclick = answer('accepted');
</script></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, `<!doctype html><title>Welcome</title><body><a href="/logout">Log out</a></body>`)
			return
		}
		_, _ = io.WriteString(w, login)
	})
	defer done()
	require.NoError(t, page.Goto(base+"login"))

	step, err := Draft{}.FillPassword(page, "invented-password", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, "OneTrust “Opt Out”", step.Dismissed)
	require.Equal(t, "Log In", step.Words)
	require.Equal(t, "rejected", posted.Get("consent"), "the banner was declined, never accepted")
	require.Equal(t, "someone@example.test", posted.Get("username"))
	require.Equal(t, "invented-password", posted.Get("password"))
}

// A OneTrust banner whose "Opt Out" opens a preference centre with no
// refuse-all, against a real Chromium: one switch, drawn over a hidden
// checkbox and on by default, a Confirm that saves the switches as they stand,
// and a close that accepts everything. The fill turns the switch off and
// confirms, and what is asserted is what the server was sent. Every word is
// invented; the shape is the platform's.
func TestAPreferenceCentreIsUntickedAndConfirmedAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const login = `<!doctype html><title>Log In</title><body>
<form method="post" action="/login">
  <h1>Log In</h1>
  <label for="username">Username</label><input id="username" name="username" autocomplete="username">
  <label for="password">Password</label><input id="password" name="password" type="password">
  <input type="hidden" name="consent" id="consent" value="unanswered">
  <button id="login" type="submit">Log In</button>
</form>
<div class="onetrust-pc-dark-filter" style="position:fixed;inset:0;background:rgba(0,0,0,.5);z-index:1000"></div>
<div id="onetrust-banner-sdk" style="position:fixed;bottom:0;left:0;right:0;z-index:1001;background:#fff;padding:20px">
  <p>We use cookies. Closing this banner accepts them all.</p>
  <button id="onetrust-pc-btn-handler">Opt Out</button>
  <button id="onetrust-accept-btn-handler">Accept Cookies</button>
  <button class="onetrust-close-btn-handler" aria-label="Close"></button>
</div>
<div id="onetrust-pc-sdk" style="display:none;position:fixed;top:0;right:0;bottom:0;width:400px;z-index:1002;background:#fff;padding:20px">
  <button id="close-pc-btn-handler" aria-label="Close preference center"></button>
  <label>Marketing Cookies
    <input type="checkbox" id="ot-group-id-M1" class="category-switch-handler" checked style="position:absolute;opacity:0;width:0;height:0">
    <span class="ot-switch-nob" style="display:inline-block;width:40px;height:20px;background:#3c3"></span>
  </label>
  <p>Strictly Necessary Cookies: Always Active</p>
  <button class="save-preference-btn-handler onetrust-close-btn-handler">Confirm</button>
</div>
<script>
  const banner = document.getElementById('onetrust-banner-sdk');
  const centre = document.getElementById('onetrust-pc-sdk');
  const marketing = document.getElementById('ot-group-id-M1');
  const done = (said) => {
    document.getElementById('consent').value = said;
    banner.style.display = 'none';
    centre.style.display = 'none';
    document.querySelector('.onetrust-pc-dark-filter').style.display = 'none';
  };
  document.getElementById('onetrust-pc-btn-handler').onclick = () => { centre.style.display = 'block'; };
  document.getElementById('onetrust-accept-btn-handler').onclick = () => done('accepted');
  document.querySelector('#onetrust-banner-sdk .onetrust-close-btn-handler').onclick = () => done('accepted');
  document.getElementById('close-pc-btn-handler').onclick = () => done('accepted');
  document.querySelector('.save-preference-btn-handler').onclick = () => done(marketing.checked ? 'accepted' : 'rejected');
</script></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, `<!doctype html><title>Welcome</title><body><a href="/logout">Log out</a></body>`)
			return
		}
		_, _ = io.WriteString(w, login)
	})
	defer done()
	require.NoError(t, page.Goto(base+"login"))

	step, err := Draft{}.FillPassword(page, "invented-password", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, "OneTrust “Opt Out”, then OneTrust “Confirm”", step.Dismissed)
	require.Equal(t, "rejected", posted.Get("consent"), "the switch was off when the choice was saved")
	require.Equal(t, "someone@example.test", posted.Get("username"))
	require.Equal(t, "invented-password", posted.Get("password"))
}

// A component library's sign-in button, against a real Chromium: no disabled
// property and no aria attribute, only a class whose pointer-events is none,
// lifted later than a click is given, the way a form that validates on the
// server does. The press waits it out and sends the form rather than clicking
// into Playwright's timeout. Every word is invented; the shape is a component
// library's.
func TestAButtonHeldByItsDisabledClassIsWaitedForAndPressedAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const form = `<!doctype html><title>Sign in</title>
<style>.lib-button-disabled { pointer-events: none; opacity: 0.4; }</style><body>
<form method="post" action="/done">
  <input name="usr" type="text" value="someone">
  <input name="pwd" type="password" value="invented">
  <button id="go" class="lib-button lib-button-disabled">Sign in</button>
</form>
<script>
  setTimeout(() => document.getElementById('go').classList.remove('lib-button-disabled'), 6000);
</script></body>`

	sent := make(chan struct{}, 1)
	page, _, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			select {
			case sent <- struct{}{}:
			default:
			}
			_, _ = io.WriteString(w, `<!doctype html><title>Account</title><h1>Welcome</h1>`)
			return
		}
		_, _ = io.WriteString(w, form)
	})
	defer done()

	controls, err := SubmitControls(page)
	require.NoError(t, err)
	require.Len(t, controls, 1)
	require.True(t, controls[0].Disabled, "a class whose pointer-events is none holds the button")

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.True(t, step.Waited)
	select {
	case <-sent:
	case <-time.After(5 * time.Second):
		t.Fatal("the form was not sent")
	}
}

// A username-first sign-in's password page, against a real Chromium: the
// username sits locked beside an Edit link and the password box was on the
// first page too, hidden by visibility until Continue. The first page reads as
// asking for the username, and the password page is filled without typing into
// the locked box. Every word is invented; the shape is a patient portal's.
func TestAUsernameFirstSignInFillsThePasswordBesideALockedUsernameAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const login = `<!doctype html><title>Log in</title><body><form method="post" action="/login">
<label for="Login">Username</label><input id="Login" name="Login" type="text" autocomplete="username">
<div id="secret" style="visibility:hidden"><label for="Password">Password</label>
<input id="Password" name="Password" type="password" autocomplete="current-password"></div>
<input id="submit" type="submit" value="Continue">
<script>
  document.querySelector('form').onsubmit = (e) => {
    if (document.getElementById('secret').style.visibility === 'hidden') {
      e.preventDefault();
      document.getElementById('Login').readOnly = true;
      document.getElementById('secret').style.visibility = 'visible';
      document.getElementById('submit').value = 'Log in';
    }
  };
</script></form></body>`

	var posted url.Values
	page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			require.NoError(t, r.ParseForm())
			posted = r.PostForm
			_, _ = io.WriteString(w, `<!doctype html><title>Welcome</title><body><a href="/logout">Log out</a></body>`)
			return
		}
		_, _ = io.WriteString(w, login)
	})
	defer done()
	require.NoError(t, page.Goto(base+"login"))

	form, err := ReadForm(page)
	require.NoError(t, err)
	require.Equal(t, StateEmail, StateOf(form, page.URL(), nil).State, "the hidden password box is not on the page yet")
	_, err = Draft{}.FillEmail(page, "someone@example.test")
	require.NoError(t, err)

	form, err = ReadForm(page)
	require.NoError(t, err)
	require.Equal(t, StatePassword, StateOf(form, page.URL(), nil).State)
	step, err := Draft{}.FillPassword(page, "invented-password", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, "Log in", step.Words)
	require.Equal(t, "someone@example.test", posted.Get("Login"))
	require.Equal(t, "invented-password", posted.Get("Password"))
}
