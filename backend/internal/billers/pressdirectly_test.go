package billers

import (
	"errors"
	"reflect"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// A provider whose button Playwright never sees as stable presses it with a
// forced click straight away, where the page shows it clear; anywhere else,
// and for every other provider, the press is the ordinary one.

// directPage offers a "Sign in" button whose ordinary click lands, and answers
// the cover and pressability readings as told.
func directPage(cover, pressable any) *browser.StubPage {
	page := &browser.StubPage{
		Firefox: true,
		OnEvaluate: func(script string, arg any) (any, error) {
			switch script {
			case coverScript:
				return cover, nil
			case submitEnabledScript:
				return pressable, nil
			}
			return nil, nil
		},
	}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})
	return page
}

func TestAClearButtonIsPressedDirectlyWithoutWaitingOutTheClick(t *testing.T) {
	page := directPage(nil, true)

	step, err := Draft{BillerID: "town-portal", PressDirectly: true}.press(page)
	require.NoError(t, err)
	require.Equal(t, []string{submitMark}, page.Forced)
	require.Empty(t, page.Clicked, "the click Playwright would wait out is never made")
	require.True(t, step.Forced)
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.Equal(t, "Sign in", step.Words)
	require.Equal(t, `the "Sign in" button was pressed directly, past Playwright's checks, `+
		`with nothing on top of it and pressable`, step.Note)
}

// Covered, unreadable, or a forced click that did not land: the ordinary press
// runs as it does for any provider.
func TestADirectPressFallsBackToTheOrdinaryPressUnlessThePageShowsTheButtonClear(t *testing.T) {
	cases := []struct {
		name  string
		page  func() *browser.StubPage
		force bool
	}{
		{"covered", func() *browser.StubPage {
			return directPage(map[string]any{"tag": "div", "classes": "cdk-overlay-backdrop"}, true)
		}, false},
		{"pressability unread", func() *browser.StubPage { return directPage(nil, nil) }, false},
		{"cover unread", func() *browser.StubPage {
			page := directPage(nil, true)
			inner := page.OnEvaluate
			page.OnEvaluate = func(script string, arg any) (any, error) {
				if script == coverScript {
					return nil, errors.New("the page went away")
				}
				return inner(script, arg)
			}
			return page
		}, false},
		{"forced click refused", func() *browser.StubPage {
			page := directPage(nil, true)
			page.OnForceClick = func(string) error { return errors.New("element is detached") }
			return page
		}, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			page := c.page()

			step, err := Draft{BillerID: "town-portal", PressDirectly: true}.press(page)
			require.NoError(t, err)
			require.Equal(t, []string{submitMark}, page.Clicked, "the ordinary click is made")
			if c.force {
				require.Equal(t, []string{submitMark}, page.Forced)
			} else {
				require.Empty(t, page.Forced)
			}
			require.False(t, step.Forced)
			require.Empty(t, step.Note)
			require.Equal(t, agent.PressedButton, step.Pressed)
		})
	}
}

func TestNothingIsPressedDirectlyWhileThePageCheckIsPending(t *testing.T) {
	page, _ := pageCheckPage(true, false)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})

	_, err := Draft{BillerID: "town-portal", PressDirectly: true}.press(page)
	require.EqualError(t, err,
		`town-portal's page check had not cleared after 45s, so the "Sign in" button was not pressed`)
	require.Empty(t, page.Forced)
	require.Empty(t, page.Clicked)
}

func TestAProviderThatDoesNotOptInIsPressedTheOrdinaryWay(t *testing.T) {
	page := directPage(nil, true)

	step, err := Draft{BillerID: "town-portal"}.press(page)
	require.NoError(t, err)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.Empty(t, page.Forced)
	require.False(t, step.Forced)
}

// Our Community Connect is the one provider that presses directly, aimed or
// not.
func TestOnlyOurCommunityConnectPressesDirectly(t *testing.T) {
	registry := New()
	for _, id := range registry.IDs() {
		module, err := registry.Pick(string(id))
		require.NoError(t, err)
		value := reflect.Indirect(reflect.ValueOf(module))
		field := value.FieldByName("PressDirectly")
		direct := field.IsValid() && field.Bool()
		require.Equal(t, id == NewCommunityConnect().ID(), direct, "%s", id)
	}
	aimed := NewCommunityConnect().WithSite("example-town").(*CommunityConnect)
	require.True(t, aimed.PressDirectly)
}
