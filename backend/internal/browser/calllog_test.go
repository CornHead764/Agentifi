package browser

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/playwright-community/playwright-go"
	"github.com/stretchr/testify/require"
)

// Every log here is invented, in the shape playwright-go hands one over.
func timedOut(log ...string) error {
	return fmt.Errorf("playwright: %w: locator.click: Timeout 5000ms exceeded.\nCall log:\n%s",
		playwright.ErrTimeout, strings.Join(log, "\n"))
}

func TestAClickLogKeepsPlaywrightsStepsAndElementsByTagIDAndClass(t *testing.T) {
	err := timedOut(
		`  - waiting for getByText('Pat Example')`,
		`    - locator resolved to <button id="go" type="submit" data-user="pat@example.test" class="mat-mdc-button  primary">Sign in as Pat</button>`,
		`  - attempting click action`,
		`    2 × waiting for element to be visible, enabled and stable`,
		`      - element is visible, enabled and stable`,
		`      - scrolling into view if needed`,
		`      - done scrolling`,
		`      - <div class="cdk-overlay-backdrop"></div> intercepts pointer events`,
		`      - <span class="label">Sign</span> from <form id="login" action="/x?token=abc">…</form> subtree intercepts pointer events`,
		`    - retrying click action, attempt #3`,
		`    - waiting 100ms`,
	)

	require.Equal(t, `waiting for the element; locator resolved to <button id="go" class="mat-mdc-button primary">; `+
		`attempting click action; 2 × waiting for element to be visible, enabled and stable; `+
		`element is visible, enabled and stable; scrolling into view if needed; done scrolling; `+
		`<div class="cdk-overlay-backdrop"> intercepts pointer events; `+
		`<span class="label"> from <form id="login"> subtree intercepts pointer events; `+
		`retrying click action, attempt #3; waiting 100ms`, ClickLog(err))
	require.False(t, ClickPerformed(err), "the click was retried, never performed")
}

// A line that is not one of Playwright's steps, or that carries anything
// beside its elements, is dropped whole: a value typed into a field can only
// reach the log that way.
func TestAClickLogDropsEveryLineThatCouldCarryAValue(t *testing.T) {
	err := timedOut(
		`  - locator resolved to <input type="password" name="pw" value="invented-secret-7">`,
		`  - fill("invented-secret-7")`,
		`  - element is not visible: invented-secret-7`,
		`  - <div>invented<secret</div> intercepts pointer events`,
		`  - attempting click action`,
		`  - navigated to "https://portal.example.test/home?session=invented"`,
	)

	said := ClickLog(err)
	require.Equal(t, `locator resolved to <input>; attempting click action`, said)
	require.NotContains(t, said, "invented")
}

func TestAClickLogKeepsTheTailAndIsCapped(t *testing.T) {
	var log []string
	for attempt := 1; attempt <= 30; attempt++ {
		log = append(log, `  - retrying click action, attempt #`+fmt.Sprint(attempt), `    - waiting 500ms`)
	}
	said := ClickLog(timedOut(log...))
	require.True(t, strings.HasPrefix(said, "…; retrying click action, attempt #25; "), said)
	require.True(t, strings.HasSuffix(said, "retrying click action, attempt #30; waiting 500ms"), said)

	long := strings.Repeat("a", 59)
	var wide []string
	for range 20 {
		wide = append(wide, `  - <div class="`+long+` `+long+`"></div> intercepts pointer events`)
		wide = append(wide, `  - retrying click action`)
	}
	said = ClickLog(timedOut(wide...))
	require.LessOrEqual(t, len([]rune(said)), callLogChars+1)
	require.True(t, strings.HasPrefix(said, "…"))
	require.True(t, strings.HasSuffix(said, "; retrying click action"))
}

func TestAStepRepeatedBackToBackIsSaidOnce(t *testing.T) {
	err := timedOut(`  - waiting 20ms`, `  - waiting 20ms`, `  - retrying click action`)
	require.Equal(t, "waiting 20ms; retrying click action", ClickLog(err))
}

func TestAClickWhoseLogShowsItDoneWasPerformed(t *testing.T) {
	for _, last := range []string{"click action done", "waiting for scheduled navigations to finish"} {
		err := timedOut(`  - attempting click action`, `    - performing click action`, `    - `+last)
		require.True(t, ClickPerformed(err), last)
	}
	require.False(t, ClickPerformed(timedOut(`  - attempting click action`, `    - performing click action`)),
		"a click still being performed is not counted as done")
}

func TestAnErrorWithoutACallLogSaysNothing(t *testing.T) {
	require.Equal(t, "", ClickLog(nil))
	require.Equal(t, "", ClickLog(errors.New("locator.click: Timeout 5000ms exceeded.")))
	require.False(t, ClickPerformed(playwright.ErrTimeout))
}
