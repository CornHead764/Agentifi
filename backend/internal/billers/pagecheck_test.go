package billers

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// What covers a button is named by its markup, and a check is named as one.
func TestACoverIsNamedByItsMarkupAndACheckAsACheck(t *testing.T) {
	cases := []struct {
		name  string
		cover Cover
		check bool
		says  string
	}{
		{"a Turnstile frame", Cover{Tag: "iframe", Src: "https://challenges.cloudflare.com/cdn-cgi/challenge-platform/x"},
			true, "a frame from challenges.cloudflare.com"},
		{"inside a widget", Cover{Tag: "div", Classes: "cf-turnstile wide", Check: true}, true, "a <div> of class cf-turnstile"},
		{"a cookie banner", Cover{Tag: "DIV", ID: "consent-banner", Classes: "sheet"}, false, "a <div> with id consent-banner"},
		{"a class only", Cover{Tag: "section", Classes: "  overlay  modal"}, false, "a <section> of class overlay"},
		{"a bare element", Cover{Tag: "span"}, false, "a <span>"},
		{"another site's frame", Cover{Tag: "iframe", Src: "https://chat.example.test/widget?visitor=1"},
			false, "a frame from chat.example.test"},
		{"a frame with no address", Cover{Tag: "iframe", ID: "chat"}, false, "a <iframe> with id chat"},
		{"nothing to go on", Cover{}, false, "a <element>"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.check, c.cover.IsPageCheck())
			require.Equal(t, c.says, c.cover.Describe())
		})
	}
}

func TestACoverNamesALongIdOnlyInPart(t *testing.T) {
	cover := Cover{Tag: "div", ID: "an-overlay-whose-generated-id-goes-on-and-on-and-on"}
	require.Equal(t, "a <div> with id an-overlay-whose-generated-id-goes-on-an…", cover.Describe())
}

// pageCheckPage is a Camoufox stub whose check is shown or not, and clears or
// not, and which records what it was asked to wait for.
func pageCheckPage(shown, clears bool) (*browser.StubPage, *[]time.Duration) {
	var waits []time.Duration
	cleared := false
	page := &browser.StubPage{Firefox: true}
	page.OnWaitFor = func(script string, timeout time.Duration) error {
		switch script {
		case pageCheckShownScript:
			waits = append(waits, timeout)
			if !shown {
				return playwright.ErrTimeout
			}
		case pageCheckClearedScript:
			waits = append(waits, timeout)
			if !clears {
				return playwright.ErrTimeout
			}
			cleared = true
		}
		return nil
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != pageCheckScript {
			return nil, nil
		}
		switch {
		case !shown:
			return "", nil
		case cleared:
			return "cleared", nil
		}
		return "pending", nil
	}
	return page, &waits
}

func TestThePageCheckIsWaitedForOnlyWhereItCanClear(t *testing.T) {
	t.Run("Chrome is not kept waiting", func(t *testing.T) {
		page, waits := pageCheckPage(true, false)
		page.Firefox = false
		require.False(t, AwaitPageCheck(page))
		require.Empty(t, *waits)
	})
	t.Run("a page with no check is given a moment to draw one, and no more", func(t *testing.T) {
		page, waits := pageCheckPage(false, false)
		require.False(t, AwaitPageCheck(page))
		require.Equal(t, []time.Duration{pageCheckAppears}, *waits)
	})
	t.Run("a check that clears by itself is waited out", func(t *testing.T) {
		page, waits := pageCheckPage(true, true)
		require.False(t, AwaitPageCheck(page))
		require.Equal(t, []time.Duration{pageCheckAppears, turnstileAutoWait}, *waits)
	})
	t.Run("a check that does not auto-clear is clicked and then waited out", func(t *testing.T) {
		page, waits := pageCheckPage(true, false)
		require.True(t, AwaitPageCheck(page))
		require.Equal(t, []time.Duration{pageCheckAppears, turnstileAutoWait, turnstileClickWait}, *waits)
	})
	t.Run("a check that clears after the click is not said to be pending", func(t *testing.T) {
		clearCalls := 0
		page := &browser.StubPage{Firefox: true}
		page.OnWaitFor = func(script string, timeout time.Duration) error {
			switch script {
			case pageCheckShownScript:
			case pageCheckClearedScript:
				clearCalls++
				if clearCalls < 2 {
					return playwright.ErrTimeout
				}
			}
			return nil
		}
		page.OnEvaluate = func(script string, arg any) (any, error) {
			if script == pageCheckScript {
				if clearCalls >= 2 {
					return "cleared", nil
				}
				return "pending", nil
			}
			return nil, nil
		}
		require.False(t, AwaitPageCheck(page))
	})
	t.Run("a check still pending after the wait is said to be", func(t *testing.T) {
		page, _ := pageCheckPage(true, false)
		require.True(t, AwaitPageCheck(page))
	})
}

func TestTheEdgeChallengeAndAnUnansweredWidgetArePendingAndATokenIsNot(t *testing.T) {
	for state, pending := range map[string]bool{"waf": true, "pending": true, "cleared": false, "": false} {
		page := &browser.StubPage{OnEvaluate: func(script string, arg any) (any, error) {
			require.Equal(t, pageCheckScript, script)
			return state, nil
		}}
		require.Equal(t, pending, PageCheckPending(page), "state %q", state)
	}
	unreadable := &browser.StubPage{OnEvaluate: func(string, any) (any, error) {
		return nil, errors.New("the page went away")
	}}
	require.False(t, PageCheckPending(unreadable))
}

// The press waits for the check first, and a check that does not clear is
// said plainly rather than pressed through.
func TestTheSignInButtonIsNotPressedWhileThePageCheckIsPending(t *testing.T) {
	page, _ := pageCheckPage(true, false)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})
	module := Draft{BillerID: "town-portal"}

	_, err := module.press(page)
	require.EqualError(t, err,
		`town-portal's page check had not cleared after 45s, so the "Sign in" button was not pressed`)
	require.Empty(t, page.Clicked)
}

// The button is read once the check has cleared, not before: a page holds it
// disabled until the token is in.
func TestTheSignInButtonIsReadAfterThePageCheckClears(t *testing.T) {
	page, _ := pageCheckPage(true, true)
	var order []string
	waitFor := page.OnWaitFor
	page.OnWaitFor = func(script string, timeout time.Duration) error {
		switch script {
		case pageCheckClearedScript:
			order = append(order, "check cleared")
		case submitEnabledScript:
			order = append(order, "waited to be pressable")
		}
		return waitFor(script, timeout)
	}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})
	evaluate := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == submitControlsScript {
			order = append(order, "read the button")
		}
		return evaluate(script, arg)
	}

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Equal(t, []string{"check cleared", "read the button", "waited to be pressable"}, order)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.Equal(t, "Sign in", step.Words)
}

// Right before the click the control is waited on to be pressable, for the
// whole window and whatever the reading said: a form disables its button
// again while it validates, and Playwright would wait the click out.
func TestTheSignInButtonIsWaitedOnRightBeforeTheClick(t *testing.T) {
	for _, disabled := range []bool{false, true} {
		page := &browser.StubPage{}
		var waits []time.Duration
		page.OnWaitFor = func(script string, timeout time.Duration) error {
			if script == submitEnabledScript {
				waits = append(waits, timeout)
			}
			return nil
		}
		offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Disabled: disabled})

		step, err := Draft{BillerID: "town-portal"}.press(page)
		require.NoError(t, err)
		require.Equal(t, []time.Duration{submitWait}, waits, "read as disabled: %v", disabled)
		require.Equal(t, []string{submitMark}, page.Clicked)
		require.Equal(t, agent.PressedButton, step.Pressed)
		require.Equal(t, disabled, step.Waited, "waited says it was not pressable when it was read")
	}
}

// A control read as pressable that is not pressable by the click is not
// clicked into Playwright's timeout.
func TestAButtonThatTurnsDisabledBeforeTheClickIsNotClicked(t *testing.T) {
	page := &browser.StubPage{OnWaitFor: func(script string, _ time.Duration) error {
		if script == submitEnabledScript {
			return playwright.ErrTimeout
		}
		return nil
	}}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in"})

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Empty(t, page.Clicked)
	require.Equal(t, agent.PressedEnter, step.Pressed)
	require.Equal(t, "Sign in", step.Words, "the step names the button that never became pressable")
	require.True(t, step.Waited)
}

// A press that times out says what the page shows: what is on top of the
// button when something is, the button disabled when it is, and neither when
// the page says neither.
func TestAPressThatTimesOutSaysWhatStoppedIt(t *testing.T) {
	cases := []struct {
		name      string
		cover     any
		pressable any
		says      string
	}{
		{"a check", map[string]any{"tag": "iframe", "src": "https://challenges.cloudflare.com/x"}, true,
			`town-portal's page check had not cleared and covered the "Sign in" button, so it could not be pressed`},
		{"a banner", map[string]any{"tag": "div", "id": "consent-banner"}, true,
			`a <div> with id consent-banner on town-portal's page covered the "Sign in" button, so it could not be pressed`},
		{"a banner over a disabled button", map[string]any{"tag": "div", "id": "consent-banner"}, false,
			`a <div> with id consent-banner on town-portal's page covered the "Sign in" button, so it could not be pressed`},
		{"nothing on top of a disabled button", nil, false,
			`the "Sign in" button on town-portal's page stayed disabled, so it could not be pressed`},
		{"nothing on top of a pressable button", nil, true,
			`the "Sign in" button on town-portal's page could not be pressed`},
		{"a page that cannot say", nil, nil,
			`the "Sign in" button on town-portal's page could not be pressed`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := &browser.StubPage{
				OnEvaluate: func(script string, arg any) (any, error) {
					switch script {
					case coverScript:
						return c.cover, nil
					case submitEnabledScript:
						return c.pressable, nil
					}
					return nil, nil
				},
				OnClick:      func(string) error { return playwright.ErrTimeout },
				OnForceClick: func(string) error { return playwright.ErrTimeout },
			}
			offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})

			_, err := Draft{BillerID: "town-portal"}.press(page)
			require.EqualError(t, err, c.says)
		})
	}
}

// clickTimeout is a click Playwright gave up on, in the shape playwright-go
// hands it over: the message, then its call log. Every line is invented.
func clickTimeout(log ...string) error {
	return fmt.Errorf("playwright: %w: locator.click: Timeout 5000ms exceeded.\nCall log:\n%s",
		playwright.ErrTimeout, strings.Join(log, "\n"))
}

// timingOutPage offers a "Sign in" button whose click times out with the log
// given, and answers the cover and pressability readings as told.
func timingOutPage(cover, pressable any, log ...string) *browser.StubPage {
	page := &browser.StubPage{
		OnEvaluate: func(script string, arg any) (any, error) {
			switch script {
			case coverScript:
				return cover, nil
			case submitEnabledScript:
				return pressable, nil
			}
			return nil, nil
		},
		OnClick: func(string) error { return clickTimeout(log...) },
	}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})
	return page
}

var waitedOutLog = []string{
	`  - waiting for locator('[data-agentifi-submit]').locator('visible=true').first()`,
	`    - locator resolved to <button id="login-go" type="submit" class="mdc-button go">Sign in</button>`,
	`  - attempting click action`,
	`    2 × waiting for element to be visible, enabled and stable`,
	`      - element is not stable`,
	`    - retrying click action`,
	`    - waiting 20ms`,
}

// A click whose log shows it done timed out waiting for the navigation it
// started: the press happened, and nothing is pressed again.
func TestAClickThatTimesOutInItsNavigationWaitCountsAsPressed(t *testing.T) {
	page := timingOutPage(nil, true,
		`  - waiting for locator('[data-agentifi-submit]').locator('visible=true').first()`,
		`    - locator resolved to <button id="login-go" type="submit" class="mdc-button go">Sign in</button>`,
		`  - attempting click action`,
		`    - waiting for element to be visible, enabled and stable`,
		`    - element is visible, enabled and stable`,
		`    - scrolling into view if needed`,
		`    - done scrolling`,
		`    - performing click action`,
		`    - click action done`,
		`    - waiting for scheduled navigations to finish`)

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.Equal(t, "Sign in", step.Words)
	require.False(t, step.Forced)
	require.Empty(t, page.Forced)
	require.Equal(t, `the "Sign in" button was pressed and the page it led to outlasted the click's wait; Playwright: `+
		`waiting for the element; locator resolved to <button id="login-go" class="mdc-button go">; `+
		`attempting click action; waiting for element to be visible, enabled and stable; `+
		`element is visible, enabled and stable; scrolling into view if needed; done scrolling; `+
		`performing click action; click action done; waiting for scheduled navigations to finish`, step.Note)
}

// A click that timed out before it was performed, at a button the page shows
// pressable with nothing on top, is pressed once more past Playwright's
// checks, and the trail says so.
func TestAPressableUncoveredButtonIsForcedOnceAfterItsClickTimesOut(t *testing.T) {
	page := timingOutPage(nil, true, waitedOutLog...)

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.Equal(t, []string{submitMark}, page.Forced)
	require.True(t, step.Forced)
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.Equal(t, `the "Sign in" button was pressed past Playwright's checks after its click timed out `+
		`with nothing on top of it and pressable; Playwright: waiting for the element; `+
		`locator resolved to <button id="login-go" class="mdc-button go">; attempting click action; `+
		`2 × waiting for element to be visible, enabled and stable; element is not stable; `+
		`retrying click action; waiting 20ms`, step.Note)
}

// A forced click that fails too is the neutral sentence for the person, with
// both of Playwright's accounts in the note.
func TestAFailedForcedClickIsTheNeutralFailureWithTheLogInItsNote(t *testing.T) {
	page := timingOutPage(nil, true, waitedOutLog...)
	page.OnForceClick = func(string) error {
		return clickTimeout(`  - attempting click action`, `    - forcing action`, `    - performing click action`)
	}

	_, err := Draft{BillerID: "town-portal"}.press(page)
	require.EqualError(t, err, `the "Sign in" button on town-portal's page could not be pressed`)
	var failure *PressFailure
	require.True(t, errors.As(err, &failure))
	require.Contains(t, failure.Note, "; Playwright: waiting for the element;")
	require.True(t, strings.HasSuffix(failure.Note,
		"; the forced click: attempting click action; forcing action; performing click action"), failure.Note)
}

// Nothing is forced at a button something sits on, at one still disabled, or
// at one the page could not read: a forced click at a cover presses the cover.
func TestNothingIsForcedUnlessThePageShowsTheButtonClear(t *testing.T) {
	cases := []struct {
		name             string
		cover, pressable any
		says             string
	}{
		{"covered", map[string]any{"tag": "div", "classes": "cdk-overlay-backdrop"}, true,
			`a <div> of class cdk-overlay-backdrop on town-portal's page covered the "Sign in" button, so it could not be pressed`},
		{"disabled", nil, false,
			`the "Sign in" button on town-portal's page stayed disabled, so it could not be pressed`},
		{"unreadable", nil, nil,
			`the "Sign in" button on town-portal's page could not be pressed`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := timingOutPage(c.cover, c.pressable, waitedOutLog...)

			_, err := Draft{BillerID: "town-portal"}.press(page)
			require.EqualError(t, err, c.says)
			require.Empty(t, page.Forced)
			var failure *PressFailure
			require.True(t, errors.As(err, &failure))
			require.True(t, strings.HasPrefix(failure.Note,
				`the "Sign in" button could not be pressed; Playwright: waiting for the element;`), failure.Note)
		})
	}
}

// A cover the page cannot read is not taken for no cover.
func TestAnUnreadableCoverForcesNothing(t *testing.T) {
	page := timingOutPage(nil, true, waitedOutLog...)
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == coverScript {
			return nil, errors.New("the page went away")
		}
		return inner(script, arg)
	}

	_, err := Draft{BillerID: "town-portal"}.press(page)
	require.EqualError(t, err, `the "Sign in" button on town-portal's page could not be pressed`)
	require.Empty(t, page.Forced)
}
