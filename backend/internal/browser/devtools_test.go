package browser

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// A developer steer moves a signed-in browser, so it may only ever go
// somewhere on the provider's own site.

func TestASteerNeverLeavesTheProvidersOwnSite(t *testing.T) {
	const home = "https://www.example-utility.test"
	const standing = "https://secure.example-utility.test/account/summary"

	for _, allowed := range []struct{ asked, want string }{
		{"https://www.example-utility.test/bills", "https://www.example-utility.test/bills"},
		// A subdomain of the registrable host is the same site; the home's
		// own `www.` is stripped before the comparison, which is what makes
		// `secure.` a subdomain of it rather than a stranger.
		{"https://secure.example-utility.test/api/x", "https://secure.example-utility.test/api/x"},
		// Relative addresses resolve against where the browser stands, the
		// way a link on the page would.
		{"/account/bills", "https://secure.example-utility.test/account/bills"},
		{"invoices", "https://secure.example-utility.test/account/invoices"},
	} {
		require.Equal(t, allowed.want, WithinSite(allowed.asked, home, standing), allowed.asked)
	}

	for _, refused := range []string{
		"https://example-utility.test.attacker.test/steal",
		"https://notexample-utility.test/",
		"https://example.test/",
		// A scheme that is not the web at all: a signed-in browser reading a
		// local file is a signed-in browser reading a local file.
		"file:///etc/passwd",
		"javascript:fetch('/x')",
		"data:text/html,<h1>hi",
		"",
	} {
		require.Empty(t, WithinSite(refused, home, standing), refused)
	}
}

func TestThePageReadingIsLabelsAndNeverValues(t *testing.T) {
	read := PageReading{
		Text: "Sign in to your account",
		Elements: []PageElement{
			{Tag: "input", Type: "email", Name: "username", ID: "user", Text: "Email address"},
			{Tag: "input", Type: "password", Name: "password", ID: "pass", Text: "Password"},
			{Tag: "button", Type: "submit", Text: "Sign  in\n  now"},
			{Tag: "a", Href: "/forgot", Text: "Forgot your password?"},
		},
	}

	shaped := ShapePage("example-utility", "https://www.example-utility.test/login", "Sign in", read)

	require.Equal(t, "example-utility", shaped.Provider)
	require.Equal(t, "Sign in", shaped.Title)
	require.Equal(t, "Sign in to your account\n\n--- interactive elements ---\n"+
		"input type=email name=username id=user  Email address\n"+
		"input type=password name=password id=pass  Password\n"+
		"button type=submit  Sign in now\n"+
		"a href=/forgot  Forgot your password?", shaped.Text)
}

func TestAPageWithNoElementsIsJustItsWords(t *testing.T) {
	shaped := ShapePage("example-utility", "https://www.example-utility.test/", "",
		PageReading{Text: "Nothing to press here."})
	require.Equal(t, "Nothing to press here.", shaped.Text)
}

func TestTheDOMReaderAnswersNoValueAndNothingNamedLikeASecret(t *testing.T) {
	// What a portal's sign-in form looks like once somebody has typed into
	// it. Every figure invented.
	found := Redact([]DOMElement{
		{
			Tag: "input",
			Attributes: map[string]string{
				"type": "password", "name": "password", "id": "pass",
				"value": "hunter-invented", "data-csrf-token": "abc123",
				"data-session-secret": "zzz", "autocomplete": "current-password",
			},
			Text: "Password",
			HTML: `<input type="password" name="password" value="hunter-invented">`,
		},
		{
			Tag:        "input",
			Attributes: map[string]string{"type": "email", "VALUE": "someone@example.test"},
			HTML:       `<input type="email" VALUE="someone@example.test" class="x">`,
		},
	})

	require.Len(t, found, 2)
	require.Equal(t, map[string]string{
		"type": "password", "name": "password", "id": "pass",
		"autocomplete": "current-password",
	}, found[0].Attributes)
	require.Equal(t, `<input type="password" name="password" value="">`, found[0].HTML)
	require.Equal(t, map[string]string{"type": "email"}, found[1].Attributes)

	// And nothing that was typed survives anywhere in the answer, whatever
	// shape it was in.
	encoded, err := json.Marshal(found)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), "hunter-invented")
	require.NotContains(t, string(encoded), "someone@example.test")
	require.NotContains(t, string(encoded), "abc123")
}

func TestAControlIsPressedByItsWordsHoweverThePageSplitsThem(t *testing.T) {
	pattern, err := ClickPattern("View  bill\ndetails")
	require.NoError(t, err)
	// A control's text is often split over child elements and lines.
	require.True(t, pattern.MatchString("View bill details"))
	require.True(t, pattern.MatchString("VIEW\n  BILL\tDETAILS"))
	require.False(t, pattern.MatchString("View details"))

	// A word with regular-expression punctuation in it is a word, not a
	// pattern: "Pay $128.40 now" must not match "Pay $128x40 now".
	amount, err := ClickPattern("Pay $128.40 now")
	require.NoError(t, err)
	require.True(t, amount.MatchString("Pay $128.40 now"))
	require.False(t, amount.MatchString("Pay $128x40 now"))

	_, err = ClickPattern("   ")
	require.Error(t, err)
}

func TestAStubbedPageIsSteeredWithoutCapturingAnything(t *testing.T) {
	// A page with no browser behind it still answers the steer: there is
	// nothing to listen to, and the snapshot is what was wanted anyway.
	page := &StubPage{Location: "https://www.example-utility.test/bills"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		return map[string]any{"text": "Your bills", "elements": []any{}}, nil
	}
	ran := false
	traffic := Capture(page, func() { ran = true })
	require.True(t, ran)
	require.Empty(t, traffic.Requests)
	require.Nil(t, traffic.Download)

	read := Snapshot("example-utility", page)
	require.Equal(t, "Your bills", read.Text)
	require.Equal(t, "https://www.example-utility.test/bills", read.URL)
}
