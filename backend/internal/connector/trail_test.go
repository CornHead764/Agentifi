package connector

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/browser/agent"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The sign-in trail must say where the page was and what was on it, and never
// carry what was typed into it.

// readingPage is a stub that answers the page-reading as well as the fills: a
// login form with the provider complaining about what was last sent.
func readingPage(location string) *browser.StubPage {
	page := &browser.StubPage{Location: location + "?state=a-token-nobody-should-see"}
	moved := 0
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if !strings.Contains(script, "signOutLink") {
			// Everything else is the page signature, and this page moves: a provider
			// answering with its own complaint has changed the page.
			moved++
			return "signature " + strconv.Itoa(moved), nil
		}
		// What the reading script answers: the boxes and the words that name
		// them, undecided — the trail and the classifier read the one script.
		return map[string]any{
			"fields": []any{
				map[string]any{"type": "email", "id": "username", "label": "Email"},
				map[string]any{"type": "password", "id": "password", "label": "Password"},
				map[string]any{"type": "checkbox", "id": "checkbox_rememberemail", "label": "Remember my email"},
			},
			"signOutLink": false,
			"submits":     1,
			"heading":     "Sign On",
			"error":       "That password was not recognized. Try again.",
			"text":        "Let us get you logged in.",
		}, nil
	}
	return page
}

func TestASignInThatKeepsAskingLeavesATrailOfWhatItWasShown(t *testing.T) {
	module := newFakeBrowser()
	module.Draft.SignIn = "https://login.example.test/sso?state=a-token-nobody-should-see"
	page := readingPage("https://login.example.test/sso")
	// The classifier always answers password, so the loop fills it until it
	// gives up.
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StatePassword}, nil
	}
	engine := testEngine(t, module, page)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateFailed, state.State)

	// The failed state carries the trail: the session may be reaped before
	// anybody asks the route for it.
	require.NotEmpty(t, state.Trail)
	require.Equal(t, "sign-in", state.Trail[0].Step)
	require.Equal(t, billers.StatePassword, state.Trail[0].State)
	require.Equal(t, "https://login.example.test/sso", state.Trail[0].URL,
		"the query string is off: a sign-in address carries tokens in one")
	require.True(t, state.Trail[0].Form.Password)
	require.True(t, state.Trail[0].Form.Username)
	require.Equal(t, map[string]int{"email": 1, "password": 1, "checkbox": 1}, state.Trail[0].Inputs)
	require.Contains(t, state.Trail[0].Error, "not recognized")
	require.False(t, state.Trail[0].At.IsZero())
	require.Equal(t, "failed", state.Trail[len(state.Trail)-1].Step)

	// This page shows the fill no control the submit rule knows, so it pressed
	// Enter.
	require.True(t, state.Trail[0].Did.Acted)
	require.Equal(t, agent.PressedEnter, state.Trail[0].Did.Pressed)
	require.True(t, state.Trail[0].Did.Changed, "the provider answered with a different page")

	// And nothing in it is a credential.
	written, err := json.Marshal(state.Trail)
	require.NoError(t, err)
	require.NotContains(t, string(written), "invented")
	require.NotContains(t, string(written), "someone@example.test")
	require.NotContains(t, string(written), "a-token-nobody-should-see")

	// The same list is on the route while the session is still open.
	asked, err := engine.ConnectTrail(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, state.Trail, asked)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A bridge is noted as its own round: the press happens inside the classify,
// so without this the trail reads as though the sign-in had stood on the page
// it was pressed onto all along.
func TestAPressedBridgeIsARoundOfTheTrailOfItsOwn(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateInteractive, Bridged: 2}, nil
	}
	engine := testEngine(t, module, readingPage("https://gateway.example.test/my.policy"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	steps := make([]string, 0, len(state.Trail))
	for _, round := range state.Trail {
		steps = append(steps, round.Step)
	}
	require.Equal(t, []string{"bridge", "bridge", "sign-in", "failed"}, steps)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A page whose only control the submit rule has no word for: every round
// presses Enter at a form that ignores it. The loop gives up after
// stalledRounds and says what the round pressed.
func TestASignInThatMovesNothingGivesUpAndSaysWhatItPressed(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateEmail}, nil
	}
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		return agent.Step{Acted: true, Pressed: agent.PressedEnter}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/signin/v2/"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "nothing on the page matched a button to press, and the Enter key moved nothing",
		state.Error)
	rounds := 0
	for _, round := range state.Trail {
		if round.Step == "sign-in" {
			rounds++
		}
	}
	require.Equal(t, stalledRounds, rounds, "it does not spend four rounds finding that out")
	require.Equal(t, agent.PressedEnter, state.Trail[0].Did.Pressed)
	require.False(t, state.Trail[0].Did.Changed)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The page has a control, it is pressed, and the provider ignores it: the
// trail names the control in the provider's words.
func TestASignInThatPressesAButtonToNoEffectNamesTheButton(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateEmail}, nil
	}
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		return agent.Step{Acted: true, Pressed: agent.PressedButton, Words: "Next"}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/signin/v2/"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, `the page did not change after pressing "Next"`, state.Error)
	require.Equal(t, agent.PressedButton, state.Trail[0].Did.Pressed)
	require.Equal(t, "Next", state.Trail[0].Did.Words)
	require.False(t, state.Trail[0].Did.Changed)

	// The provider's own words are safe in a trail; nothing typed is.
	written, err := json.Marshal(state.Trail)
	require.NoError(t, err)
	require.NotContains(t, string(written), "invented")
	require.NotContains(t, string(written), "someone@example.test")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A round that got somewhere is not given up on: the guard counts rounds that
// left the page alone, in a row, and a page that moved resets it.
func TestARoundThatMovedThePageDoesNotCountTowardsTheGuard(t *testing.T) {
	module := newFakeBrowser()
	rounds := []billers.State{
		{State: billers.StateEmail}, {State: billers.StateEmail}, {State: billers.StateSignedIn},
	}
	module.classify = func(browser.Page) (billers.State, error) {
		where := rounds[0]
		if len(rounds) > 1 {
			rounds = rounds[1:]
		}
		return where, nil
	}
	moved := []bool{false, true}
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		changed := moved[0]
		if len(moved) > 1 {
			moved = moved[1:]
		}
		return agent.Step{Acted: true, Pressed: agent.PressedButton, Words: "Next", Changed: changed}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/signin/v2/"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	require.Equal(t, billers.StateSignedIn, awaitSignIn(t, engine, state).State)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A factor page nothing here can answer: the menu, in the provider's own
// words, goes on the line that showed the page and into the sentence the
// household reads.
func TestAFactorPageNobodyCanAnswerSaysWhatItWasOffering(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{Choices: []billers.FactorChoice{
			{Kind: "radio", Words: "Text message", Selects: true},
			{Kind: "radio", Words: "Email", Selects: true},
			{Kind: "radio", Words: "Answer your security questions", Selects: true},
		}}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/sap/v2/actions/choosemethod"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t,
		`unrecognised factor page; offered "Text message", "Email", "Answer your security questions"`,
		state.Error)
	require.Contains(t, state.Prompt, `"Text message"`)
	require.Contains(t, state.Prompt, "sign in again")

	// The menu is on the line that showed the page, kinds and all.
	factor := state.Trail[len(state.Trail)-2]
	require.Equal(t, billers.StateFactor, factor.State)
	require.Equal(t, []provider.BillSignInChoice{
		{Kind: "radio", Words: "Text message"},
		{Kind: "radio", Words: "Email"},
		{Kind: "radio", Words: "Answer your security questions"},
	}, factor.Choices)
	require.Empty(t, factor.Chose, "nothing on it was taken")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A factor page whose controls the reading cannot name at all is its own
// finding: the controls are somewhere the reading does not go.
func TestAFactorPageWithNoReadableMenuSaysThatInstead(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/mfa"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, "unrecognised factor page; offered no choice this sign-in could read", state.Error)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The choice goes on the line that showed the menu, with the press that
// confirmed it: a radio selects and sends nothing.
func TestAFactorRoundSaysWhichChoiceItTookAndWhatItPressedAfterwards(t *testing.T) {
	module := newFakeBrowser()
	rounds := []billers.State{{State: billers.StateFactor}, {State: billers.StateSignedIn}}
	module.classify = func(browser.Page) (billers.State, error) {
		where := rounds[0]
		if len(rounds) > 1 {
			rounds = rounds[1:]
		}
		return where, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{
			Kind: "totp", Chose: "Authenticator app", Confirmed: true,
			Step: agent.Step{
				Acted: true, Pressed: agent.PressedButton, Words: "Continue", Changed: true,
			},
			Choices: []billers.FactorChoice{
				{Kind: "radio", Words: "Text message", Selects: true},
				{Kind: "radio", Words: "Authenticator app", Selects: true},
			},
		}, nil
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/sap/v2/actions/choosemethod"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateSignedIn, state.State)

	asked, err := engine.ConnectTrail(context.Background(), state.SessionID)
	require.NoError(t, err)
	chose := asked[0]
	require.Equal(t, billers.StateFactor, chose.State)
	require.Equal(t, "Authenticator app", chose.Chose)
	require.Len(t, chose.Choices, 2)
	require.Equal(t, "Continue", chose.Did.Words)
	require.True(t, chose.Did.Changed)
	require.Equal(t, "factor: totp", asked[1].Step, "and then the page it landed on")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A round that fails *while choosing* still records the menu and the choice
// (here the click on the chosen radio could not land).
func TestAFactorRoundThatFailedWhileChoosingStillSaysWhatItOffered(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{
				Kind: "totp", Chose: "Use Google Authenticator",
				Choices: []billers.FactorChoice{
					{Kind: "radio", Words: "Text me a code", Selects: true},
					{Kind: "radio", Words: "Use Google Authenticator", Selects: true},
					{Kind: "radio", Words: "Use a security key", Selects: true},
				},
			},
			errors.New(`T-Mobile would not let "Use Google Authenticator" be chosen`)
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/sap/v2/actions/choosemethod"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, `T-Mobile would not let "Use Google Authenticator" be chosen`, state.Error)

	factor := state.Trail[len(state.Trail)-2]
	require.Equal(t, billers.StateFactor, factor.State)
	require.Len(t, factor.Choices, 3, "what it had found")
	require.Equal(t, "Use Google Authenticator", factor.Chose, "and which of it it took")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The driver's call log is a developer's artefact: the sentence reaches the
// trail, the call log does not.
func TestADriversRetryLogNeverReachesTheTrail(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{}, errors.New(
			"playwright: timeout: Timeout 5000ms exceeded.\nCall log:\n" +
				"  - waiting for locator('[data-agentifi-factor]')\n  - retrying click action")
	}
	engine := testEngine(t, module, readingPage("https://account.example.test/mfa"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, "playwright: timeout: Timeout 5000ms exceeded.", state.Error)
	require.NotContains(t, state.Trail[len(state.Trail)-1].Error, "Call log")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

func TestATrailIsCappedSoALoopCannotGrowASessionWithoutBound(t *testing.T) {
	module := newFakeBrowser()
	page := readingPage("https://login.example.test/sso")
	engine := testEngine(t, module, page)
	s := engine.open(module)
	s.Attach(&OpenBrowser{Page: page, Close: func() {}})

	for i := 0; i < trailLimit+10; i++ {
		engine.note(s, "sign-in", billers.State{State: billers.StatePassword})
	}
	require.Len(t, engine.trailOf(s), trailLimit)
	engine.close(s)
}

// A cookie banner a fill dismissed goes on the round's line and in the notes.
func TestADeclinedCookieBannerIsOnTheTrailAndInTheNotes(t *testing.T) {
	module := newFakeBrowser()
	rounds := []billers.State{{State: billers.StateEmail}, {State: billers.StateSignedIn}}
	module.classify = func(browser.Page) (billers.State, error) {
		where := rounds[0]
		if len(rounds) > 1 {
			rounds = rounds[1:]
		}
		return where, nil
	}
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		return agent.Step{
			Acted: true, Pressed: agent.PressedButton, Words: "Log In", Changed: true,
			Dismissed: "OneTrust “Opt Out”",
		}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/login"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateSignedIn, state.State)

	trail, err := engine.ConnectTrail(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.Equal(t, "OneTrust “Opt Out”", trail[0].Did.Dismissed)
	s, err := engine.find(state.SessionID)
	require.NoError(t, err)
	require.Contains(t, s.notes.List(),
		"dismissed the cookie banner on Erie Insurance's sign-in page (OneTrust “Opt Out”)")
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A press that took more than a click goes on the round's line, forced and
// with its note, and in the notes.
func TestAForcedPressIsOnTheTrailWithPlaywrightsAccount(t *testing.T) {
	module := newFakeBrowser()
	rounds := []billers.State{{State: billers.StateEmail}, {State: billers.StateSignedIn}}
	module.classify = func(browser.Page) (billers.State, error) {
		where := rounds[0]
		if len(rounds) > 1 {
			rounds = rounds[1:]
		}
		return where, nil
	}
	const note = `the "Sign in" button was pressed past Playwright's checks; Playwright: attempting click action; element is not stable`
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		return agent.Step{
			Acted: true, Pressed: agent.PressedButton, Words: "Sign in", Changed: true, Forced: true, Note: note,
		}, nil
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/login"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.Equal(t, billers.StateSignedIn, state.State)

	trail, err := engine.ConnectTrail(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.True(t, trail[0].Forced)
	require.Equal(t, note, trail[0].Note)
	s, err := engine.find(state.SessionID)
	require.NoError(t, err)
	require.Contains(t, s.notes.List(), note)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A press that did not land fails with its sentence, and Playwright's account
// goes on the round's line and in the notes, not in the error.
func TestAnUnpressedButtonsLogIsOnTheTrailAndNotInTheError(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateEmail}, nil
	}
	const note = `the "Sign in" button could not be pressed; Playwright: attempting click action; retrying click action`
	module.fillEmail = func(browser.Page, string) (agent.Step, error) {
		return agent.Step{}, &billers.PressFailure{
			Said: `the "Sign in" button on Erie Insurance's page could not be pressed`, Note: note,
		}
	}
	engine := testEngine(t, module, readingPage("https://login.example.test/login"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateFailed, state.State)
	require.Equal(t, `the "Sign in" button on Erie Insurance's page could not be pressed`, state.Error)
	require.Equal(t, note, state.Trail[len(state.Trail)-2].Note, "on the round that pressed")
	s, err := engine.find(state.SessionID)
	require.NoError(t, err)
	require.Contains(t, s.notes.List(), note)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// A factor page answered with a push is a sign-in waiting on a phone, which
// the dialog has a screen for — not an unrecognised factor page.
func TestAPushChosenAtAFactorPageEndsTheRoundsAtTheApproval(t *testing.T) {
	module := newFakeBrowser()
	chosen := false
	module.classify = func(browser.Page) (billers.State, error) {
		if chosen {
			return billers.State{
				State: billers.StateApproval, Method: string(domain.ChallengePush),
				Prompt: "Approve the sign-in in the app on your phone.",
			}, nil
		}
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		chosen = true
		return agent.Factor{
			Kind: string(domain.ChallengePush), Chose: "Confirm using our mobile app",
			Choices: []billers.FactorChoice{{Kind: "button", Words: "Confirm using our mobile app"}},
		}, nil
	}
	engine := testEngine(t, module, readingPage("https://secure.example.test/challenge"))

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)

	require.Equal(t, billers.StateApproval, state.State)
	require.Equal(t, string(domain.ChallengePush), state.Method)
	require.Equal(t, "Approve the sign-in in the app on your phone.", state.Prompt)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}

// The round that ends a sign-in carries what the page was made of, read before
// the browser is let go, under the trail's rules and with no long run of
// digits.
func TestTheRoundThatEndsASignInCarriesWhatThePageWasMadeOf(t *testing.T) {
	module := newFakeBrowser()
	module.classify = func(browser.Page) (billers.State, error) {
		return billers.State{State: billers.StateFactor}, nil
	}
	module.chooseFactor = func(browser.Page) (agent.Factor, error) {
		return agent.Factor{}, errors.New(`Example Bank would not let "Approve in the mobile app" be chosen`)
	}
	page := readingPage("https://secure.example.test/confirm")
	reading := page.OnEvaluate
	released := false
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if strings.Contains(script, "#shadow-root") {
			require.False(t, released, "the page is read before the browser goes")
			return map[string]any{
				"headings": []any{"Confirm Your Identity"},
				"text":     "Confirm Your Identity. Account ending 004321.",
				"tree":     []any{"mds-list-item#inAppSend", "  #shadow-root", `    button type=button "Confirm using our mobile app"`},
			}, nil
		}
		return reading(script, arg)
	}
	engine := testEngine(t, module, page)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "erie", Profile: "connection-9", Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	released = true

	require.Equal(t, billers.StateFailed, state.State)
	last := state.Trail[len(state.Trail)-1]
	require.Equal(t, "failed", last.Step)
	require.Contains(t, last.Snapshot, "headings: Confirm Your Identity")
	require.Contains(t, last.Snapshot, `    button type=button "Confirm using our mobile app"`)
	require.NotContains(t, last.Snapshot, "004321")
	for _, entry := range state.Trail[:len(state.Trail)-1] {
		require.Empty(t, entry.Snapshot, "only the round that ended it")
	}
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}
