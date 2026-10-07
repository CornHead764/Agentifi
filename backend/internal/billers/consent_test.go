package billers

import (
	"fmt"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// The cookie banner in front of a sign-in form: a OneTrust banner with a
// filter over the page, behind which the press of "Log In" times out.

const (
	oneTrustReject = "#onetrust-reject-all-handler"
	oneTrustClose  = "#onetrust-close-btn-container button"
	oneTrustAccept = "#onetrust-accept-btn-handler"
)

// bannerPage is a combined form under a banner offering the controls given,
// answering the consent reading every time it is asked, and recording every
// act on the page in one list so the order of the fill and the press shows.
func bannerPage(t *testing.T, controls ...ConsentControl) (*browser.StubPage, *[]string) {
	t.Helper()
	page := combinedFormPage(true)
	var acts []string
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == consentScript {
			return asAny(t, controls), nil
		}
		return inner(script, arg)
	}
	page.OnClick = func(selector string) error { acts = append(acts, "click "+selector); return nil }
	page.OnFill = func(selector, _ string) error { acts = append(acts, "fill "+selector); return nil }
	return page, &acts
}

func TestTheCookieRuleRejectsBeforeItClosesAndNeverAccepts(t *testing.T) {
	reject := ConsentControl{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Opt Out"}
	closer := ConsentControl{Manager: "OneTrust", Kind: "close", Selector: oneTrustClose}

	best, found := BestConsent([]ConsentControl{closer, reject})
	require.True(t, found)
	require.Equal(t, oneTrustReject, best.Selector, "a reject outranks a close wherever it sits")

	best, found = BestConsent([]ConsentControl{closer})
	require.True(t, found)
	require.Equal(t, oneTrustClose, best.Selector, "a banner with no reject is closed")

	relabelled := ConsentControl{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Accept Cookies"}
	_, found = BestConsent([]ConsentControl{relabelled})
	require.False(t, found, "a control that says it accepts is never pressed, whatever list it is in")

	_, found = BestConsent(nil)
	require.False(t, found)
}

// The list of banners carries no accept control at all, so no reading of it
// can press one.
func TestNoKnownBannerListsAControlThatAccepts(t *testing.T) {
	for _, manager := range consentManagers {
		for _, selector := range append(append([]string{}, manager.Reject...), manager.Close...) {
			require.NotContains(t, selector, "accept", manager.Name)
			require.NotEqual(t, oneTrustAccept, selector)
		}
	}
}

func TestTheFillDeclinesACookieBannerBeforeItTypes(t *testing.T) {
	page, acts := bannerPage(t,
		ConsentControl{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Opt Out"},
		ConsentControl{Manager: "OneTrust", Kind: "close", Selector: oneTrustClose},
	)

	step, err := testDraft().FillPassword(page, "invented", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, []string{
		"click " + oneTrustReject,
		"fill " + editable(usernameSelector),
		"fill " + passwordSelector,
		"click " + submitMark,
	}, *acts, "the banner goes first, once, and then the form is filled and sent")
	require.Equal(t, "OneTrust “Opt Out”", step.Dismissed, "the trail says what was declined")
	require.True(t, step.Acted)
}

func TestABannerWithNoRejectIsClosedAndOneThatOnlyAcceptsIsLeftAlone(t *testing.T) {
	page, acts := bannerPage(t, ConsentControl{Manager: "OneTrust", Kind: "close", Selector: oneTrustClose})
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, "click "+oneTrustClose, (*acts)[0])
	require.Equal(t, "OneTrust close", step.Dismissed)

	page, acts = bannerPage(t,
		ConsentControl{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Accept All"})
	step, err = testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.NotContains(t, *acts, "click "+oneTrustReject)
	require.Empty(t, step.Dismissed)
}

func TestAPageWithNoBannerIsFilled(t *testing.T) {
	page, acts := bannerPage(t)
	step, err := testDraft().FillPassword(page, "invented", "")
	require.NoError(t, err)
	require.Equal(t, []string{"fill " + passwordSelector, "click " + submitMark}, *acts)
	require.Empty(t, step.Dismissed)
}

// A banner that will not go is pressed once per fill and then typed past: the
// press is not retried, and a failed one is not a reason to stop.
func TestABannerThatStaysIsPressedOnceAndNeverLoopedOn(t *testing.T) {
	page, acts := bannerPage(t,
		ConsentControl{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Opt Out"})
	page.OnClick = func(selector string) error {
		*acts = append(*acts, "click "+selector)
		if selector == oneTrustReject {
			return fmt.Errorf("playwright: %w: Timeout 5000ms exceeded.", playwright.ErrTimeout)
		}
		return nil
	}

	step, err := testDraft().FillPassword(page, "invented", "")

	require.NoError(t, err)
	require.Equal(t, []string{"click " + oneTrustReject, "fill " + passwordSelector, "click " + submitMark}, *acts)
	require.Empty(t, step.Dismissed, "a press that did not land declined nothing")
}

// The submit that timed out, said in words rather than as the driver's
// timeout, and claiming no cover when the page names none.
func TestASubmitThatTimesOutFailsInWords(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Log In"})
	page.OnClick = func(string) error {
		return fmt.Errorf("playwright: %w: Timeout 5000ms exceeded.", playwright.ErrTimeout)
	}

	_, err := testDraft().FillPassword(page, "invented", "")

	require.EqualError(t, err,
		`the "Log In" button on Erie Insurance's page could not be pressed`)
}

// An "Opt Out" that is not the platform's own reject button, beside the
// platform's close ✕: the words say which one declines.
func TestABannersOwnOptOutIsPreferredToItsClose(t *testing.T) {
	controls := []ConsentControl{
		{Manager: "OneTrust", Kind: "close", Selector: oneTrustClose, Words: "Close"},
		{Manager: "OneTrust", Kind: "other", Selector: `[data-agentifi-consent="0"]`, Words: "Cookie Settings"},
		{Manager: "OneTrust", Kind: "other", Selector: `[data-agentifi-consent="1"]`, Words: "Opt Out"},
		{Manager: "OneTrust", Kind: "other", Selector: `[data-agentifi-consent="2"]`, Words: "Accept"},
	}
	best, found := BestConsent(controls)
	require.True(t, found)
	require.Equal(t, "Opt Out", best.Words)

	best, _ = BestConsent(append([]ConsentControl{
		{Manager: "OneTrust", Kind: "reject", Selector: oneTrustReject, Words: "Reject All"},
	}, controls...))
	require.Equal(t, oneTrustReject, best.Selector, "the platform's own reject still comes first")

	for _, words := range []string{"Reject all", "Decline", "Opt-out", "Necessary cookies only",
		"Only necessary", "Do Not Sell or Share My Personal Information", "Deny all cookies"} {
		require.True(t, consentRejects.MatchString(words), words)
	}
	for _, words := range []string{"Cookie Settings", "Opt out of the newsletter", "Accept necessary",
		"More options", "Privacy policy"} {
		require.False(t, consentRejects.MatchString(words), words)
	}
}

// A banner whose "Opt Out" opens the preference centre rather than answering
// is declined twice: the banner, and then the centre's own reject.
func TestAPreferenceCentreOpenedByTheBannerIsDeclinedToo(t *testing.T) {
	page := combinedFormPage(true)
	inner := page.OnEvaluate
	asked := 0
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != consentScript {
			return inner(script, arg)
		}
		asked++
		switch asked {
		case 1:
			return asAny(t, []ConsentControl{{Manager: "OneTrust", Banner: "#onetrust-banner-sdk", Kind: "other",
				Selector: `[data-agentifi-consent="0"]`, Words: "Opt Out"}}), nil
		case 2:
			return asAny(t, []ConsentControl{
				{Manager: "OneTrust", Banner: "#onetrust-banner-sdk", Kind: "other",
					Selector: `[data-agentifi-consent="0"]`, Words: "Opt Out"},
				{Manager: "OneTrust", Banner: "#onetrust-pc-sdk", Kind: "reject",
					Selector: ".ot-pc-refuse-all-handler", Words: "Reject All"},
			}), nil
		}
		return asAny(t, []ConsentControl{}), nil
	}

	step, err := testDraft().FillPassword(page, "invented", "someone@example.test")

	require.NoError(t, err)
	require.Equal(t, `OneTrust “Opt Out”, then OneTrust “Reject All”`, step.Dismissed)
	require.Equal(t, []string{`[data-agentifi-consent="0"]`, ".ot-pc-refuse-all-handler", submitMark}, page.Clicked)
}
