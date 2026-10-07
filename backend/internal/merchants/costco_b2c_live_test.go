package merchants

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/config"
)

// The Costco classifier over B2C sign-in pages in a real Chromium. The pages
// are invented, in the shape every B2C custom policy draws: a `#api` frame
// named by its step, error elements that sit hidden in the page until they
// speak, and the standard ids.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/merchants/ -run AgainstARealBrowser

const b2cPolicyPath = "/tenant-0000/B2C_1A_Invented_Policy/oauth2/v2.0/authorize"

// b2cPage wraps a step's form in the frame B2C draws it into. The frame is
// empty until the page's own script fills it, as B2C's is.
func b2cPage(step, form string) string {
	return `<!doctype html><html><head><title>Sign in</title></head><body>
<div id="api" data-name="` + step + `" role="main"></div>
<template id="step">` + form + `</template>
<script>
  const draw = () => document.getElementById('api').appendChild(document.getElementById('step').content.cloneNode(true));
  const delay = Number(new URLSearchParams(location.search).get('draw') || 0);
  if (delay) setTimeout(draw, delay); else draw();
</script>
</body></html>`
}

const b2cSignIn = `<div class="heading"><h1>Sign In</h1></div>
<form id="localAccountForm" onsubmit="event.preventDefault()">
  <div class="error pageLevel" aria-hidden="true" style="display: none;"><p role="alert"></p></div>
  <label for="signInName">Email Address</label>
  <div class="error itemLevel" aria-hidden="true" style="display: none;"><p role="alert">Please enter your Email Address</p></div>
  <input type="email" id="signInName" name="signInName">
  <label for="password">Password</label>
  <div class="error itemLevel" aria-hidden="true" style="display: none;"><p role="alert">Please enter your password</p></div>
  <input type="password" id="password" name="password">
  <input type="checkbox" id="rememberMe" name="rememberMe"><label for="rememberMe">Keep me signed in</label>
  <button id="next" type="submit">Sign In</button>
</form>`

var b2cSignInRefused = strings.Replace(b2cSignIn,
	`<div class="error pageLevel" aria-hidden="true" style="display: none;"><p role="alert"></p></div>`,
	`<div class="error pageLevel" aria-hidden="false" style="display: block;"><p role="alert">The email address or password you entered is not valid.</p></div>`, 1)

const b2cEmailVerification = `<div class="heading"><h1>Verify Your Email</h1></div>
<form onsubmit="event.preventDefault()">
  <div class="error pageLevel" aria-hidden="true" style="display: none;"></div>
  <label for="email">Email Address</label>
  <input type="email" id="email" name="email" value="someone@example.test" readonly>
  <div class="verificationInfoText" id="sent" aria-hidden="true" style="display: none;">Verification code has been sent to your email address.</div>
  <div class="verificationErrorText error" aria-hidden="true" style="display: none;">That code is incorrect. Please try again.</div>
  <input type="text" id="verificationCode" name="verificationCode" style="display: none;">
  <button id="emailVerificationControl_but_verify_code" type="button" style="display: none;">Verify code</button>
  <button id="emailVerificationControl_but_send_code" type="button">Send verification code</button>
  <button id="continue" type="submit">Continue</button>
</form>
<script>
  document.getElementById('emailVerificationControl_but_send_code').addEventListener('click', (event) => {
    event.target.style.display = 'none';
    for (const id of ['sent', 'verificationCode', 'emailVerificationControl_but_verify_code']) {
      const el = document.getElementById(id);
      el.style.display = 'block';
      el.setAttribute('aria-hidden', 'false');
    }
    window.sends = (window.sends || 0) + 1;
  });
</script>`

const b2cMethodChoice = `<div class="heading"><h1>How should we verify it's you?</h1></div>
<form onsubmit="event.preventDefault(); window.chosen = document.querySelector('input[name=mfaMethod]:checked').value; document.getElementById('api').setAttribute('data-name', 'Sent');">
  <input type="radio" id="phoneCall" name="mfaMethod" value="call"><label for="phoneCall">Call me</label>
  <input type="radio" id="phoneText" name="mfaMethod" value="sms"><label for="phoneText">Text me</label>
  <input type="radio" id="emailCode" name="mfaMethod" value="email"><label for="emailCode">Email me</label>
  <button id="continue" type="submit">Continue</button>
</form>`

const b2cUnknownStep = `<div class="heading"><h1>Update your details</h1></div>
<form onsubmit="event.preventDefault()">
  <label for="nickname">Nickname</label>
  <input type="text" id="nickname" name="nickname" value="typed-by-someone">
  <button id="save" type="submit">Save</button>
  <button id="cancel" type="button">Cancel</button>
</form>`

func costcoLivePage(t *testing.T, pages map[string]string) (browser.Page, string) {
	t.Helper()
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, found := pages[r.URL.Path]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(site.Close)
	settings, err := config.LoadBrowser()
	require.NoError(t, err)
	engine := browser.NewEngine(settings)
	t.Cleanup(func() { _ = engine.Close() })
	context, err := engine.NewContext("", browser.DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	opened, err := browser.OpenPage(context)
	require.NoError(t, err)
	return browser.Wrap(opened), site.URL
}

func TestTheCostcoClassifierReadsB2CStepsAgainstARealBrowser(t *testing.T) {
	for _, one := range []struct {
		name  string
		page  string
		query string
		want  string
		error string
	}{
		{name: "the sign-in, whose hidden errors say nothing", page: b2cPage("CombinedSigninAndSignup", b2cSignIn),
			want: StatePassword},
		{name: "the sign-in, drawn late", page: b2cPage("CombinedSigninAndSignup", b2cSignIn), query: "?draw=1500",
			want: StatePassword},
		{name: "a refused password", page: b2cPage("CombinedSigninAndSignup", b2cSignInRefused),
			want: StateFailed, error: "The email address or password you entered is not valid."},
		{name: "the email verification step", page: b2cPage("SelfAsserted", b2cEmailVerification),
			want: StateFactor},
		{name: "a choice of ways", page: b2cPage("SelfAsserted", b2cMethodChoice), want: StateFactor},
		{name: "a step with no rule", page: b2cPage("SelfAsserted", b2cUnknownStep), want: StateFailed,
			error: "unrecognised page at 127.0.0.1"},
	} {
		t.Run(one.name, func(t *testing.T) {
			page, base := costcoLivePage(t, map[string]string{b2cPolicyPath: one.page})
			require.NoError(t, page.Goto(base+b2cPolicyPath+one.query))
			where, err := Costco().Classify(page)
			require.NoError(t, err)
			require.Equal(t, one.want, where.State, where.Error)
			require.True(t, strings.HasPrefix(where.Error, one.error), where.Error)
		})
	}
}

func TestAnUnrecognisedB2CStepIsDescribedWithoutWhatWasTypedAgainstARealBrowser(t *testing.T) {
	page, base := costcoLivePage(t, map[string]string{b2cPolicyPath: b2cPage("SelfAsserted", b2cUnknownStep)})
	require.NoError(t, page.Goto(base+b2cPolicyPath+"?state=invented-state"))
	where, err := Costco().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFailed, where.State)
	require.True(t, strings.HasSuffix(where.Error,
		b2cPolicyPath+`: B2C step "SelfAsserted"; heading "Update your details"; buttons "Save", "Cancel"`),
		where.Error)
	require.NotContains(t, where.Error, "typed-by-someone")
	require.NotContains(t, where.Error, "invented-state")
}

func TestTheEmailVerificationStepIsSentThenAskedForAgainstARealBrowser(t *testing.T) {
	page, base := costcoLivePage(t, map[string]string{b2cPolicyPath: b2cPage("SelfAsserted", b2cEmailVerification)})
	require.NoError(t, page.Goto(base+b2cPolicyPath))
	module := Costco()

	where, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFactor, where.State)
	factor, err := module.ChooseFactor(page, "")
	require.NoError(t, err)
	require.NotEmpty(t, factor.Kind)
	sends, err := page.Evaluate(`() => window.sends`, nil)
	require.NoError(t, err)
	require.EqualValues(t, 1, sends)

	where, err = module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateOTP, where.State, where.Error)
	require.Equal(t, "Verification code has been sent to your email address.", where.Prompt)
	require.Equal(t, "email", where.Method)
}

func TestTheEmailedWayIsChosenAndSentAgainstARealBrowser(t *testing.T) {
	page, base := costcoLivePage(t, map[string]string{b2cPolicyPath: b2cPage("SelfAsserted", b2cMethodChoice)})
	require.NoError(t, page.Goto(base+b2cPolicyPath))
	module := Costco()

	where, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFactor, where.State)
	factor, err := module.ChooseFactor(page, "")
	require.NoError(t, err)
	require.NotEmpty(t, factor.Kind)
	chosen, err := page.Evaluate(`() => window.chosen`, nil)
	require.NoError(t, err)
	require.Equal(t, "email", chosen)
}

// The sign-in form as a page that watches how it is filled writes it down:
// every key and pointer event, whether the browser made it, and how long after
// the last key the form was sent. It writes into the DOM, which a script in
// Camoufox's isolated world can read where it cannot read the page's globals.
const b2cWatchedSignIn = `<div class="heading"><h1>Sign In</h1></div>
<form id="localAccountForm">
  <label for="signInName">Email Address</label>
  <input type="email" id="signInName" name="signInName" value="stale@example.test">
  <label for="password">Password</label>
  <input type="password" id="password" name="password">
  <input type="checkbox" id="rememberMe" name="rememberMe"><label for="rememberMe">Keep me signed in</label>
  <button id="next" type="submit">Sign In</button>
</form>
<pre id="watched" hidden></pre>
<script>
  const seen = { moves: 0, keys: { signInName: 0, password: 0 }, untrusted: 0, pressed: [], sent: 0, sinceKey: -1 };
  let lastKey = 0;
  const note = () => { document.getElementById('watched').textContent = JSON.stringify(seen); };
  const trust = (event) => { if (!event.isTrusted) seen.untrusted++; };
  document.addEventListener('mousemove', (event) => { trust(event); seen.moves++; note(); }, true);
  document.addEventListener('mousedown', (event) => { trust(event); seen.pressed.push(event.target.id); note(); }, true);
  document.addEventListener('keydown', (event) => {
    trust(event);
    if (event.target.id in seen.keys) seen.keys[event.target.id]++;
    lastKey = performance.now();
    note();
  }, true);
  document.getElementById('localAccountForm').addEventListener('submit', (event) => {
    event.preventDefault();
    trust(event);
    seen.sent++;
    seen.sinceKey = performance.now() - lastKey;
    document.querySelector('h1').textContent = 'Sent';
    note();
  });
  note();
</script>`

type watchedSignIn struct {
	Moves     int            `json:"moves"`
	Keys      map[string]int `json:"keys"`
	Untrusted int            `json:"untrusted"`
	Pressed   []string       `json:"pressed"`
	Sent      int            `json:"sent"`
	SinceKey  float64        `json:"sinceKey"`
}

// costcoSignInPage is the browser Costco signs in with: Camoufox when
// CAMOUFOX_URL is set, otherwise Chromium under AGENTIFI_BROWSER_TEST=1.
func costcoSignInPage(t *testing.T, pages map[string]string) (browser.Page, string, bool) {
	t.Helper()
	settings, err := config.LoadBrowser()
	require.NoError(t, err)
	if settings.CamoufoxURL == "" {
		page, base := costcoLivePage(t, pages)
		return page, base, false
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, found := pages[r.URL.Path]
		if !found {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = io.WriteString(w, body)
	}))
	t.Cleanup(site.Close)
	engine := browser.NewEngine(settings)
	t.Cleanup(func() { _ = engine.Close() })
	context, page, err := engine.OpenFirefoxPage(browser.StorageState{})
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	return page, site.URL, true
}

// It runs against a Camoufox server:
//
//	CAMOUFOX_URL=ws://127.0.0.1:9333/<path> go test ./internal/merchants/ -run TypedAndPressed
func TestTheSignInIsTypedAndPressedAgainstARealBrowser(t *testing.T) {
	page, base, camoufox := costcoSignInPage(t, map[string]string{
		b2cPolicyPath: b2cPage("CombinedSigninAndSignup", b2cWatchedSignIn),
	})
	require.NoError(t, page.Goto(base+b2cPolicyPath))
	module := Costco()
	where, err := module.Classify(page)
	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State, where.Error)

	const email, password = "someone@example.test", "an-Invented-pass/word"
	_, err = module.FillPassword(page, password, email)
	require.NoError(t, err)

	var typed struct{ Email, Password string }
	require.NoError(t, browser.EvaluateInto(page, `() => ({
		email: document.getElementById('signInName').value,
		password: document.getElementById('password').value,
	})`, nil, &typed))
	require.Equal(t, email, typed.Email, "the stale address is replaced")
	require.Equal(t, password, typed.Password)

	var seen watchedSignIn
	require.NoError(t, browser.EvaluateInto(page,
		`() => JSON.parse(document.getElementById('watched').textContent)`, nil, &seen))
	require.Zero(t, seen.Untrusted, "every event came from the browser's own input")
	require.Equal(t, len(email)+3, seen.Keys["signInName"],
		"a key for each character, after Control, A and Backspace clear the old address")
	require.Equal(t, len(password), seen.Keys["password"])
	require.Equal(t, []string{"signInName", "password", "rememberMe", "next"}, seen.Pressed)
	require.Equal(t, 1, seen.Sent)
	require.GreaterOrEqual(t, seen.SinceKey, float64(browser.TypingPace.Wait[0].Milliseconds()),
		"the press waits after the last key")
	if camoufox {
		require.Greater(t, seen.Moves, 4*len(seen.Pressed), "the cursor moves along a path to every press")
	}
	t.Logf("camoufox=%v moves=%d pressed=%v sinceKey=%.0fms", camoufox, seen.Moves, seen.Pressed, seen.SinceKey)
}
