package browser

import (
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Typing into the box somebody could have typed into, against a real Chromium.
//
// Guarded by AGENTIFI_BROWSER_TEST for the reason engine_test.go gives: the
// image carries no browser, so a test that skips itself when one is missing
// would pass without exercising anything.
//
//	AGENTIFI_BROWSER_TEST=1 go test ./internal/browser/
//
// The page below is invented, in a real site's shape: a sign-in form whose
// first `input[type=email]` is a display:none field the site keeps the
// remembered address in, with the box a person actually types in after it.
// `First()` on the union a provider module works through resolves to the
// decoy, so the username is never typed and the page shows no error.
func TestFillTypesIntoTheBoxSomebodyCouldHaveUsed(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	site := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		_, _ = io.WriteString(w, `<!doctype html><title>Sign On</title><body>
<form id="loginForm" novalidate>
  <input id="oldCookieName" name="oldCookieName" type="email" style="display: none;">
  <label for="username">Email</label>
  <input id="username" name="username" type="email">
  <label for="password">Password</label>
  <input id="password" name="password" type="password">
  <button id="hiddenSubmit" type="submit" style="display: none;">Log in</button>
  <button id="loginbtn" type="submit">LOG IN</button>
</form>
<script>
  document.getElementById('loginForm').addEventListener('submit', (event) => {
    event.preventDefault();
    document.title = 'sent ' + document.getElementById('username').value;
  });
</script>
</body>`)
	}))
	t.Cleanup(site.Close)

	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	opened, err := OpenPage(context)
	require.NoError(t, err)
	page := Wrap(opened)
	require.NoError(t, page.Goto(site.URL))

	const usernames = `input[type="email"], input[autocomplete="username"], input[name*="user" i], input[type="text"]`
	filled, err := page.FillVisible(usernames, "someone@example.test")
	require.NoError(t, err)
	require.True(t, filled, "the page does show a username box")

	// The decoy is untouched and the real box has it.
	read, err := page.Evaluate(`() => [document.getElementById('oldCookieName').value,
		document.getElementById('username').value]`, nil)
	require.NoError(t, err)
	require.Equal(t, []any{"", "someone@example.test"}, read)

	// And the button a person would press is the one that is pressed: the
	// hidden submit before it would have swallowed the click.
	clicked, err := page.ClickVisible(`button[type="submit"], input[type="submit"]`)
	require.NoError(t, err)
	require.True(t, clicked)
	title, err := page.Title()
	require.NoError(t, err)
	require.Equal(t, "sent someone@example.test", title)
}

// A wait gives up when it was told to. A timeout handed to playwright-go in
// the expression's argument slot is ignored, and every wait runs to the
// context's default instead.
func TestAWaitGivesUpWhenItWasToldToAgainstARealBrowser(t *testing.T) {
	if os.Getenv("AGENTIFI_BROWSER_TEST") != "1" {
		t.Skip("set AGENTIFI_BROWSER_TEST=1 to drive a real Chromium")
	}
	engine := NewEngine(testSettings(t))
	t.Cleanup(func() { require.NoError(t, engine.Close()) })
	context, err := engine.NewContext("", DefaultViewport)
	require.NoError(t, err)
	t.Cleanup(func() { _ = context.Close() })
	opened, err := OpenPage(context)
	require.NoError(t, err)
	page := Wrap(opened)

	started := time.Now()
	err = page.WaitForFunction(`() => false`, 300*time.Millisecond)
	require.True(t, IsTimeout(err), "a wait that never comes true is a timeout: %v", err)
	require.Less(t, time.Since(started), 5*time.Second)

	require.NoError(t, page.WaitForFunction(`() => true`, 300*time.Millisecond))
}
