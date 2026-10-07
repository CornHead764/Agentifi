package connector

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The kept password at Erie's sign-in, read by Erie's own classifier over a
// stub page: after the password the browser passes through the portal's
// gateway, which shows nothing, before it lands on the account. Every address
// past the host is invented.

const (
	erieGateway = "https://custsso.erieinsurance.com/my.policy?token=a-token-nobody-should-see"
	erieLanding = "https://www.erieinsurance.com/Customer/ManageAccount/account"
)

// erieLateLanding is the stub portal. The page reaches `landsAt` once
// `arriveAfter` has passed on the page's clock since the password went in;
// until then it is the gateway, saying `complaint` if anything. The pull reads
// the account page the way Erie's reader does: the landing, which only a
// signed-in browser is let stay on.
type erieLateLanding struct {
	module *fakeBrowser
	page   *browser.StubPage
	typed  int
}

func newErieLateLanding(arriveAfter time.Duration, landsAt, complaint string) *erieLateLanding {
	erie := billers.NewErie()
	stub := &erieLateLanding{
		module: &fakeBrowser{Draft: erie.Draft},
		page:   &browser.StubPage{Location: erie.SignInURL()},
	}
	page, module := stub.page, stub.module
	module.classify = module.Draft.Classify

	stage, typedAt, signature := "form", time.Duration(0), 0
	page.OnFill = func(selector, value string) error {
		if value == keptLogin["password"] {
			stub.typed++
			stage, typedAt, page.Location = "gateway", page.Slept, erieGateway
		}
		return nil
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if !strings.Contains(script, "signOutLink") {
			signature++
			return "signature " + string(rune('a'+signature%26)), nil
		}
		if stage == "gateway" && page.Slept-typedAt >= arriveAfter {
			stage, page.Location = "account", landsAt
		}
		switch stage {
		case "form":
			return map[string]any{
				"fields": []any{
					map[string]any{"type": "text", "id": "username", "label": "Username"},
					map[string]any{"type": "password", "id": "password", "label": "Password"},
				},
				"submits": 1, "heading": "Sign In", "text": "Sign in to your account",
			}, nil
		case "gateway":
			return map[string]any{"fields": []any{}, "error": complaint}, nil
		}
		return map[string]any{
			"fields": []any{}, "submits": 4, "heading": "My Account",
			"text": "My Account\nOVERVIEW\nMY POLICIES\nBilling details\nMake a payment\nDocuments",
		}, nil
	}
	module.fetchBills = func(call billers.Call) (billers.Pull, error) {
		_ = call.Page.Goto(erieLanding)
		if stage != "account" {
			page.Location = module.SignInURL()
		}
		if !module.Draft.AccountArea(call.Page.URL()) {
			return billers.Pull{}, billers.ErrNeedsSignIn
		}
		return billers.Pull{Bills: []billers.Bill{fakeBill()}}, nil
	}
	return stub
}

func (stub *erieLateLanding) pull(t *testing.T) provider.BillPull {
	t.Helper()
	engine := testEngine(t, stub.module, stub.page)
	out, err := engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "erie", Profile: "connection-9", Subaccounts: []string{"one"},
		SessionState: `{"cookies":[],"origins":[]}`, Credential: keptLogin,
	})
	require.NoError(t, err)
	return out
}

func TestAKeptPasswordWhoseAccountPageArrivesLateIsSignedIn(t *testing.T) {
	stub := newErieLateLanding(6*time.Second, erieLanding, "")

	out := stub.pull(t)

	require.False(t, out.NeedsSignIn, out.Reason)
	require.False(t, out.PasswordRefused)
	require.Len(t, out.Bills, 1)
	require.Equal(t, 1, stub.typed, "the password is typed once")
	require.Equal(t, 2, stub.module.fetched, "the pull, and the pull again on the session the password opened")
}

func TestAPageNothingRecognisesAfterThePasswordIsNotARefusedPassword(t *testing.T) {
	stub := newErieLateLanding(time.Hour, erieLanding, "")

	out := stub.pull(t)

	require.True(t, out.NeedsSignIn)
	require.False(t, out.PasswordRefused, "a page nobody recognised says nothing about the password")
	require.False(t, out.CodeNeeded)
	require.Contains(t, out.Reason, "Erie Insurance did not finish signing in with the kept password "+
		"(stopped at a page the sign-in did not recognise at custsso.erieinsurance.com/my.policy")
	require.NotContains(t, out.Reason, "a-token", "the page is named without its query")
	require.Contains(t, out.Notes,
		"Erie Insurance's sign-in ended at a page it did not recognise, at custsso.erieinsurance.com/my.policy")
	require.GreaterOrEqual(t, stub.page.Slept, arrivalWait, "the page is given its time to arrive")
	require.Equal(t, 1, stub.typed)
	require.Equal(t, 2, stub.module.fetched, "the account page is read once more before the pull gives up")
}

// The account under an address the module's area does not list: the reading
// gives up on it, and the module's own reading of its account page gets in.
func TestAnAccountUnderAnAddressTheAreaDoesNotKnowIsFoundByReadingTheAccountPage(t *testing.T) {
	stub := newErieLateLanding(3*time.Second, "https://www.erieinsurance.com/MyPortal/Overview", "")

	out := stub.pull(t)

	require.False(t, out.NeedsSignIn, out.Reason)
	require.Len(t, out.Bills, 1)
	require.Contains(t, out.Notes,
		"Erie Insurance's sign-in ended at a page it did not recognise, at www.erieinsurance.com/MyPortal/Overview")
	require.Equal(t, 1, stub.typed)
}

func TestAPageThatSaysThePasswordWasWrongIsARefusedPassword(t *testing.T) {
	stub := newErieLateLanding(time.Hour, erieLanding, "Incorrect username or password. Try again.")

	out := stub.pull(t)

	require.True(t, out.NeedsSignIn)
	require.True(t, out.PasswordRefused)
	require.Contains(t, out.Reason, "Erie Insurance did not accept the kept password")
	require.Equal(t, 1, stub.typed)
	require.Equal(t, 1, stub.module.fetched, "a refused password reads nothing more")
}

func TestRefusalWordsNameWhatWasTyped(t *testing.T) {
	for _, said := range []string{
		"Your password is incorrect",
		"Invalid username or password",
		"That password was not recognized. Try again.",
		"The email address and password you entered don't match",
		"Your account has been locked",
	} {
		require.Truef(t, refusalWords.MatchString(said), "%q", said)
	}
	for _, said := range []string{
		"",
		"An error occurred loading your documents. Try again.",
		"Billing details unavailable",
		"Make a payment",
	} {
		require.Falsef(t, refusalWords.MatchString(said), "%q", said)
	}
}
