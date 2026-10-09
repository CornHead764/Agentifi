package billers

import (
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"testing"

	"github.com/stretchr/testify/require"
)

// MyChart's factor page against a real Chromium, once per way a login can be
// set to: radios named by their labels, a masked address and number, a
// "Trust this device" box, and a "Send code" that is a plain button posting
// the form from a script. Guarded by AGENTIFI_BROWSER_TEST (see
// draft_live_test.go). Every word and digit is invented.
func TestMyChartsFactorPageIsWalkedForEachChoiceAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const choose = `<!doctype html><title>MyChart - Verify Your Identity</title><body>
<main>
<h1>We need to verify your identity</h1>
<p>For your security, choose where we should send a verification code.</p>
<form id="sendCode" method="post" action="/MyChart/Authentication/SecondaryValidation">
  <fieldset>
    <legend>Send my code by</legend>
    <div class="option">
      <input type="radio" id="method-email" name="DeliveryMethod" value="email">
      <label for="method-email"><span class="method">Email</span> <span class="target">s•••••@example.test</span></label>
    </div>
    <div class="option">
      <input type="radio" id="method-sms" name="DeliveryMethod" value="sms">
      <label for="method-sms"><span class="method">Text message</span> <span class="target">(***) ***-0100</span></label>
    </div>
  </fieldset>
  <button type="button" class="button primary" onclick="document.getElementById('sendCode').submit()">Send code</button>
  <a href="/MyChart/Authentication/Login">Cancel</a>
</form>
</main></body>`
	const code = `<!doctype html><title>MyChart - Verify Your Identity</title><body>
<form>
  <h1>Verify Your Identity</h1>
  <label for="code">Enter the code we sent</label>
  <input id="code" name="code" type="text" autocomplete="one-time-code">
  <label><input type="checkbox" name="TrustDevice"> Trust this device</label>
  <button type="submit">Verify</button>
  <a href="#resend">Resend code</a>
</form></body>`

	for _, tc := range []struct {
		prefer, kind, posted string
	}{
		{prefer: "", kind: "sms", posted: "sms"},
		{prefer: "email", kind: "email", posted: "email"},
		{prefer: "totp"},
	} {
		t.Run("prefer "+tc.prefer, func(t *testing.T) {
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
			require.NoError(t, page.Goto(base+"MyChart/Authentication/SecondaryValidation"))
			defer Forget(page)
			where, err := module.Classify(page)
			require.NoError(t, err)
			require.Equal(t, StateFactor, where.State)

			offered, err := FactorChoices(page)
			require.NoError(t, err)
			said := make([]string, 0, len(offered))
			for _, choice := range offered {
				said = append(said, choice.Words)
			}
			require.Equal(t, []string{"Email s•••••@example.test", "Text message (***) ***-…"}, said,
				"the radios are the menu, named by their labels, with the number's digits off")

			factor, err := module.ChooseFactor(page, tc.prefer)
			require.NoError(t, err)
			require.Equal(t, tc.kind, factor.Kind)
			if tc.kind == "" {
				require.Nil(t, posted, "a way the page does not offer sends nothing")
				return
			}
			require.True(t, factor.Confirmed)
			require.Equal(t, "Send code", factor.Step.Words)
			require.Equal(t, tc.posted, posted.Get("DeliveryMethod"), "the chosen radio is the one the form sent")

			next, err := module.Classify(page)
			require.NoError(t, err)
			require.Equal(t, StateOTP, next.State, "and the page it landed on asks for the code")
		})
	}
}

// The same page as a portal drew it: two plain buttons, each sending the page
// on by itself, and no submit control anywhere. Every word is invented.
func TestMyChartsPlainButtonFactorPageIsWalkedAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	const choose = `<!doctype html><title>MyChart - Verify your identity</title><body><main>
<h1>Verify your identity</h1>
<p>For your protection, we require you to enter a unique code to verify your identity.</p>
<p>Step 1: Choose how you would like to receive your code. <button id="learnMore" type="button">Learn more</button></p>
<button id="totpCode" type="button" onclick="location.href='/code?way=totp'">Get from authenticator app</button>
<button id="emailCode" type="button" onclick="location.href='/code?way=email'">Send to my email</button>
</main></body>`
	const code = `<!doctype html><title>MyChart - Verify your identity</title><body><form>
<h1>Verify your identity</h1>
<label for="code">Enter your code</label><input id="code" name="code" type="text" autocomplete="one-time-code">
<button type="submit">Verify</button></form></body>`

	for _, tc := range []struct{ prefer, kind string }{
		{prefer: "totp", kind: "totp"},
		{prefer: "email", kind: "email"},
		{prefer: "", kind: "totp"},
	} {
		t.Run("prefer "+tc.prefer, func(t *testing.T) {
			var way string
			page, base, done := livePageServing(t, func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/html; charset=utf-8")
				if r.URL.Path == "/code" {
					way = r.URL.Query().Get("way")
					_, _ = io.WriteString(w, code)
					return
				}
				_, _ = io.WriteString(w, choose)
			})
			defer done()

			module := Draft{AccountArea: URLMatches(regexp.MustCompile(`example\.invalid/account`))}
			require.NoError(t, page.Goto(base+"MyChart/Authentication/SecondaryValidation"))
			defer Forget(page)
			where, err := module.Classify(page)
			require.NoError(t, err)
			require.Equal(t, StateFactor, where.State)

			factor, err := module.ChooseFactor(page, tc.prefer)
			require.NoError(t, err)
			require.Equal(t, tc.kind, factor.Kind)
			if tc.kind == "" {
				require.Empty(t, way, "a way the page does not offer presses nothing")
				return
			}
			require.Equal(t, tc.kind, way, "the button pressed is the chosen way's")
			next, err := module.Classify(page)
			require.NoError(t, err)
			require.Equal(t, StateOTP, next.State)
		})
	}
}
