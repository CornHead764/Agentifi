package billers

import (
	"os"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// Sign-ins that end at a dialog or wall, against a real Chromium. The pages
// are invented, each shaped like a real portal's.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/billers/ -run AgainstARealBrowser

func requireBrowser(t *testing.T) {
	t.Helper()
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
}

// signInBehind is a sign-in form a dialog will stand in front of.
const signInBehind = `<form onsubmit="event.preventDefault(); window.signIns = (window.signIns || 0) + 1">
  <h1>Sign in</h1>
  <label for="email">Email</label><input id="email" type="email">
  <label for="password">Password</label><input id="password" type="password">
  <button type="submit">Continue</button>
</form>`

// codeDialog is a carrier's code dialog: six one-character boxes over the form,
// a trusted-device box, and a Confirm that stays disabled until every box is
// full. `advance` is whether a filled box moves the cursor to the next one.
func codeDialog(advance bool) string {
	moves := "false"
	if advance {
		moves = "true"
	}
	var boxes strings.Builder
	for range 6 {
		boxes.WriteString(`<input maxlength="1" inputmode="numeric" style="width:32px" aria-label="Digit">`)
	}
	return `<!doctype html><title>Sign in</title><body>` + signInBehind + `
<div style="position:fixed;inset:0;background:rgba(0,0,0,.5)"></div>
<div role="dialog" aria-modal="true" aria-labelledby="sent" style="position:fixed;top:40px;left:40px;width:420px;background:#fff;padding:20px">
  <h2 id="sent">We sent your code</h2>
  <p>Enter the code we sent to +•••••••0000 below.</p>
  <div class="digits">` + boxes.String() + `</div>
  <p>Having trouble? <a href="#resend">Resend code</a></p>
  <label><input type="checkbox" id="c7"> Don’t require verification for future logins on this trusted device.</label>
  <button type="button">Verify another way</button>
  <button type="button" id="confirm" disabled>Confirm</button>
</div>
<script>
  const digits = [...document.querySelectorAll('.digits input')];
  digits.forEach((box, at) => box.addEventListener('input', () => {
    if (` + moves + ` && box.value && digits[at + 1]) digits[at + 1].focus();
    document.getElementById('confirm').disabled = !digits.every((one) => one.value);
  }));
  document.getElementById('confirm').addEventListener('click', () => {
    window.verified = digits.map((one) => one.value).join('') + (document.getElementById('c7').checked ? ' trusted' : '');
    document.querySelector('[role="dialog"]').innerHTML = '<h2>Verified</h2>';
  });
</script></body>`
}

// A carrier's code dialog over its own sign-in form: read as the code request it
// is, in its own words, and answered one character per box — both ways a
// widget draws that — with the trusted-device box ticked and the dialog's
// Confirm pressed, and the form behind it never touched.
func TestACodeDialogOverTheFormIsReadAndAnsweredAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	for _, advance := range []bool{true, false} {
		page, base, done := livePageOver(t, map[string]string{"/sign-in": codeDialog(advance)})
		require.NoError(t, page.Goto(base+"sign-in"))
		module := testDraft()

		where, err := module.Classify(page)
		require.NoError(t, err)
		require.Equal(t, StateOTP, where.State, "advance=%v", advance)
		require.Equal(t, "Enter the code we sent to +•••••••0000 below.", where.Prompt)
		require.Equal(t, "sms", where.Method)

		step, err := module.Answer(page, where, "482913", "")
		require.NoError(t, err)
		require.Equal(t, "Confirm", step.Words)
		require.True(t, step.Changed)

		verified, err := page.Evaluate("() => window.verified", nil)
		require.NoError(t, err)
		require.Equal(t, "482913 trusted", verified, "advance=%v", advance)
		signIns, err := page.Evaluate("() => window.signIns || 0", nil)
		require.NoError(t, err)
		require.EqualValues(t, 0, signIns, "the form behind the dialog is never sent")
		Forget(page)
		done()
	}
}

// A setup dialog over the form: the sign-in ends with a sentence saying whose
// step it is, and Get Started is never pressed.
func TestASetupDialogOverTheFormEndsTheSignInAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	const welcome = `<!doctype html><title>Log In</title><body>` + signInBehind + `
<div style="position:fixed;inset:0;background:rgba(0,0,0,.4)"></div>
<div role="dialog" style="position:fixed;top:30px;left:30px;width:500px;background:#fff;padding:20px">
  <button aria-label="Close">✕</button>
  <h2>Welcome</h2>
  <p>We need to verify your identity and setup your security profile to ensure your information remains secure.</p>
  <button onclick="window.started = true">Get Started</button>
</div></body>`
	page, base, done := livePageOver(t, map[string]string{"/login": welcome})
	defer done()
	defer Forget(page)
	require.NoError(t, page.Goto(base+"login"))

	where, err := NewNorthwesternMutual().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StateFailed, where.State)
	require.Contains(t, where.Prompt, "sign in once on northwesternmutual.com")
	started, err := page.Evaluate("() => window.started === true", nil)
	require.NoError(t, err)
	require.Equal(t, false, started)
}

// What a failed sign-in stopped on, read from a real page: controls inside
// shadow roots and a same-origin frame, and never a value.
func TestASnapshotReadsShadowRootsAndFramesAndNoValueAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	const page1 = `<!doctype html><title>Stopped</title><body>
<h1>Confirm Your Identity</h1><p>Call us at 800-555-0100 about account 1234567890.</p>
<mds-button id="next"></mds-button>
<input type="text" name="username" value="someone@example.test">
<input type="hidden" name="csrf_token" value="a-token-nobody-should-see">
<iframe srcdoc="<button id='inframe'>Frame button</button><input type='password' name='pw' value='invented-password'>"></iframe>
<script>
  customElements.define('mds-button', class extends HTMLElement {
    connectedCallback() { this.attachShadow({ mode: 'open' }).innerHTML = '<button type="button">Next</button>'; }
  });
</script></body>`
	page, base, done := livePageOver(t, map[string]string{"/stopped": page1})
	defer done()
	require.NoError(t, page.Goto(base+"stopped"))
	_, err := page.Evaluate("() => new Promise((ok) => setTimeout(ok, 500))", nil)
	require.NoError(t, err)

	said := Snapshot(page)

	require.Contains(t, said, "headings: Confirm Your Identity")
	require.Contains(t, said, "mds-button#next")
	require.Contains(t, said, "#shadow-root")
	require.Contains(t, said, `button type=button "Next"`)
	require.Contains(t, said, "#frame")
	require.Contains(t, said, `button#inframe "Frame button"`)
	require.Contains(t, said, "input type=password name=pw")
	for _, secret := range []string{"someone@example.test", "a-token-nobody-should-see", "invented-password", "1234567890"} {
		require.NotContains(t, said, secret)
	}
}

// An "Opt Out" that is not the platform's own reject button, beside its close
// ✕. The opt-out is pressed.
func TestABannersOwnOptOutIsPressedRatherThanItsCloseAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	const login = `<!doctype html><title>Log In</title><body>` + signInBehind + `
<div id="onetrust-banner-sdk" style="position:fixed;bottom:0;left:0;right:0;background:#fff;padding:12px">
  <p>We use cookies.</p>
  <button id="onetrust-pc-btn-handler">Cookie Settings</button>
  <button class="site-opt-out">Opt Out</button>
  <button id="onetrust-accept-btn-handler">Accept</button>
  <div id="onetrust-close-btn-container"><button class="onetrust-close-btn-handler" aria-label="Close"></button></div>
</div>
<script>
  const answer = (said) => () => { window.consent = said; document.getElementById('onetrust-banner-sdk').style.display = 'none'; };
  document.querySelector('.site-opt-out').onclick = answer('opted out');
  document.querySelector('.onetrust-close-btn-handler').onclick = answer('closed');
  document.getElementById('onetrust-accept-btn-handler').onclick = answer('accepted');
</script></body>`
	page, base, done := livePageOver(t, map[string]string{"/login": login})
	defer done()
	require.NoError(t, page.Goto(base+"login"))

	step, err := Draft{}.FillPassword(page, "invented-password", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, "OneTrust “Opt Out”", step.Dismissed)
	consent, err := page.Evaluate("() => window.consent", nil)
	require.NoError(t, err)
	require.Equal(t, "opted out", consent)
}

// A full-page interstitial check, read as a blocking check — and, where it
// clears, waited out and the form behind it read.
func TestAnInterstitialIsWaitedOutAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	const wall = `<!doctype html><title>Just a moment...</title><body>
<h1>app.example.test</h1><h2>Performing security verification</h2>
<p>This website uses a security service to protect against malicious bots.</p><div>Ray ID: 8c0ffee</div>
<script>setTimeout(() => { location.href = '/login'; }, 1500);</script></body>`
	const stuck = `<!doctype html><title>Just a moment...</title><body>
<h2>Performing security verification</h2><div>Ray ID: 8c0ffee</div></body>`
	page, base, done := livePageOver(t, map[string]string{
		"/wall": wall, "/stuck": stuck, "/login": `<!doctype html><title>Log in</title><body>` + signInBehind + `</body>`,
	})
	defer done()
	defer Forget(page)

	require.NoError(t, page.Goto(base+"wall"))
	where, err := testDraft().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State, "nothing stood in front, the form is the page")

	require.NoError(t, page.Goto(base+"stuck"))
	form, err := ReadForm(page)
	require.NoError(t, err)
	where = StateOf(form, page.URL(), nil)
	require.Equal(t, StateCaptcha, where.State)
	require.True(t, where.Blocking)
}

// mfaVerify is a factor page of ARIA radios (divs, not inputs) whose ids carry
// colons, a Next that is a plain button, and a close link. `ticked` is whether
// the authenticator arrives already chosen; the other shape adds a text beside
// it and ticks neither. Each radio toggles on a click. Next swaps in a code
// page and records which radio was chosen. The code page is invented.
func mfaVerify(ticked bool) string {
	radios := `<div id="mfa-fancy-button-token:software:totp" role="radio" aria-checked="true" tabindex="0">` +
		`<span>Enter code from an Authentication App</span></div>`
	if !ticked {
		radios = `<div id="mfa-fancy-button-token:sms" role="radio" aria-checked="false" tabindex="0">` +
			`<span>Text me a code</span></div>` +
			`<div id="mfa-fancy-button-token:software:totp" role="radio" aria-checked="false" tabindex="-1">` +
			`<span>Enter code from an Authentication App</span></div>`
	}
	return `<!doctype html><title>Verify Your Account</title><body>
<a role="button" aria-label="Close Icon" href="#login">Back to login</a>
<h1>Verify Your Account</h1>
<h2>We’ll send a unique code to verify your identity.</h2>
<p>This keeps your login secure and ensures that no one else can gain access to your account.</p>
<div role="radiogroup" aria-label="We’ll send a unique code to verify your identity.">` + radios + `</div>
<p>Replace your authentication method with a new way to receive your code.</p>
<button type="button" id="next">Next</button>
<script>
  for (const radio of document.querySelectorAll('[role="radio"]')) {
    radio.addEventListener('click', () => {
      const was = radio.getAttribute('aria-checked') === 'true';
      for (const other of document.querySelectorAll('[role="radio"]')) other.setAttribute('aria-checked', 'false');
      radio.setAttribute('aria-checked', was ? 'false' : 'true');
      window.clicks = (window.clicks || 0) + 1;
    });
  }
  document.getElementById('next').addEventListener('click', () => {
    const chosen = document.querySelector('[role="radio"][aria-checked="true"]');
    window.sent = chosen ? chosen.id : 'nothing';
    document.body.innerHTML = '<form onsubmit="event.preventDefault(); window.code = document.getElementById(\'otp\').value">' +
      '<h1>Enter Your Code</h1><p>Enter the 6-digit code from your authenticator app.</p>' +
      '<label for="otp">Code</label><input id="otp" type="text" inputmode="numeric">' +
      '<button type="submit">Verify</button></form>';
  });
</script></body>`
}

// That factor page, both shapes: read as the choice of factor, the
// authenticator taken (left alone where already ticked), Next pressed, and the
// code page after it read as the authenticator's and answered.
func TestAnAriaRadioFactorPageIsChosenAndSentAgainstARealBrowser(t *testing.T) {
	requireBrowser(t)
	for _, ticked := range []bool{true, false} {
		page, base, done := livePageOver(t, map[string]string{"/mfaverify": mfaVerify(ticked)})
		require.NoError(t, page.Goto(base+"mfaverify"))
		module := NewNorthwesternMutual()

		where, err := module.Classify(page)
		require.NoError(t, err)
		require.Equal(t, StateFactor, where.State, "ticked=%v", ticked)

		offered, err := FactorChoices(page)
		require.NoError(t, err)
		last := offered[len(offered)-1]
		require.Equal(t, "Enter code from an Authentication App", last.Words, "an ARIA radio is named by its content")
		require.True(t, last.Selects)
		require.Equal(t, ticked, last.Checked)

		factor, err := module.ChooseFactor(page, "totp")
		require.NoError(t, err, "ticked=%v", ticked)
		require.Equal(t, "totp", factor.Kind)
		require.True(t, factor.Confirmed)
		require.Equal(t, "Next", factor.Step.Words)

		sent, err := page.Evaluate("() => window.sent", nil)
		require.NoError(t, err)
		require.Equal(t, "mfa-fancy-button-token:software:totp", sent,
			"ticked=%v: the authenticator was the chosen radio when Next was pressed", ticked)
		clicks, err := page.Evaluate("() => window.clicks || 0", nil)
		require.NoError(t, err)
		if ticked {
			require.EqualValues(t, 0, clicks, "a radio already ticked is not clicked again")
		} else {
			require.EqualValues(t, 1, clicks)
		}

		where, err = module.Classify(page)
		require.NoError(t, err)
		require.Equal(t, StateOTP, where.State)
		require.Equal(t, "Enter the 6-digit code from your authenticator app.", where.Prompt,
			"the line naming the channel, not the heading that only says to enter a code")
		require.Equal(t, "totp", where.Method)
		_, err = module.Answer(page, where, "604183", "")
		require.NoError(t, err)
		code, err := page.Evaluate("() => window.code", nil)
		require.NoError(t, err)
		require.Equal(t, "604183", code)
		Forget(page)
		done()
	}
}
