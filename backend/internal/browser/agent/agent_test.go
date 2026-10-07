package agent

import (
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

type inFirefox struct{}

func (inFirefox) RunsInFirefox() bool { return true }

func TestAConnectorRunsInTheBrowserItAsksForAndNeverFallsBackToChrome(t *testing.T) {
	var opened []string
	opener := func(name string) Opener {
		return func(Open) (*Browser, error) {
			opened = append(opened, name)
			return &Browser{}, nil
		}
	}
	chrome, firefox := opener("chrome"), opener("firefox")

	_, _ = For(inFirefox{}, chrome, firefox)(Open{})
	_, _ = For(struct{}{}, chrome, firefox)(Open{})
	_, err := For(inFirefox{}, chrome, nil)(Open{})

	require.ErrorIs(t, err, browser.ErrNoFirefox)
	require.Equal(t, []string{"firefox", "chrome"}, opened)
}

func TestAConnectorsCallsGoThroughAPlainClientOrAPageAtItsOrigin(t *testing.T) {
	fetchers := Fetchers(nil)

	plain, release, err := fetchers(true, "", "")
	require.NoError(t, err)
	release()
	require.IsType(t, &http.Client{}, plain)

	_, _, err = fetchers(true, "https://merchant.test", "/")
	require.ErrorIs(t, err, browser.ErrNoFirefox, "a Camoufox origin with no Camoufox server")

	page, release, err := Fetchers(&browser.Engine{})(false, "https://portal.test", "/login")
	require.NoError(t, err)
	defer release()
	require.Equal(t, "https://portal.test", page.(*browser.PageFetcher).Origin)
}

func TestAHeldBrowserClosesOnceAndIsForgottenFirst(t *testing.T) {
	var hold Hold
	var said []string
	page := &browser.StubPage{}
	hold.Attach(&Browser{Page: page, Close: func() { said = append(said, "closed") }})

	forget := func(forgotten browser.Page) {
		require.Same(t, page, forgotten)
		said = append(said, "forgotten")
	}
	hold.Release(forget)
	hold.Release(forget)

	require.Nil(t, hold.Opened())
	require.Equal(t, []string{"forgotten", "closed"}, said)
}

func TestSubmitWaitsForThePageToDifferFromWhatItWasBeforeThePress(t *testing.T) {
	var waited string
	var bound time.Duration
	page := &browser.StubPage{
		OnEvaluate: func(script string, arg any) (any, error) {
			return "https://portal.test/login|Sign in", nil
		},
		OnWaitFor: func(script string, timeout time.Duration) error {
			waited, bound = script, timeout
			return nil
		},
	}

	step, err := Submit(page, "#go")
	require.NoError(t, err)

	require.Equal(t, []string{"#go"}, page.Clicked)
	require.Equal(t, Step{Acted: true, Pressed: PressedButton}, step,
		"a page that reads the same after the press did not change")
	require.True(t, strings.HasSuffix(waited, ` !== "https://portal.test/login|Sign in"`), waited)
	require.Equal(t, ChangeWait, bound)
	require.Equal(t, Settle, page.Slept)
}

func TestSubmitPressesEnterWhereThePageShowsNoButtonAndSettlesOnAnUnreadablePage(t *testing.T) {
	waits := 0
	page := &browser.StubPage{
		Missing:   func(string) bool { return true },
		OnWaitFor: func(string, time.Duration) error { waits++; return nil },
	}

	step, err := Submit(page, "#go")
	require.NoError(t, err)

	require.Equal(t, []string{"Enter"}, page.Pressed)
	require.Equal(t, Step{Acted: true, Pressed: PressedEnter, Changed: true}, step)
	require.Zero(t, waits, "a page that could not be read has nothing to differ from")
	require.Equal(t, Settle, page.Slept)
}

func TestTheSignInStatesAreTheOnesTheMerchantAPIReports(t *testing.T) {
	require.Equal(t, provider.MerchantSignInOTP, StateOTP)
	require.Equal(t, provider.MerchantSignInCaptcha, StateCaptcha)
	require.Equal(t, provider.MerchantSignInApproval, StateApproval)
	require.Equal(t, provider.MerchantSignInSignedIn, StateSignedIn)
	require.Equal(t, provider.MerchantSignInInteractive, StateInteractive)
	require.Equal(t, provider.MerchantSignInFailed, StateFailed)
}
