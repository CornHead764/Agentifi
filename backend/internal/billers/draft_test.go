package billers

import (
	"encoding/json"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

// The generic classifier's one job is a positive signed-in finding that a
// sign-in form can never produce, and a form that is never mistaken for one.

var testArea = URLMatches(regexp.MustCompile(`(?i)example\.test/(?:account|billing)`))

func testForm(over Form) Form { return over }

func TestAPasswordBoxIsThePasswordStepWhateverTheURLSays(t *testing.T) {
	where := StateOf(testForm(Form{Password: true, Username: true}), "https://example.test/account", testArea)
	require.Equal(t, StatePassword, where.State)
}

func TestAUsernameBoxAloneIsTheEmailStep(t *testing.T) {
	require.Equal(t, StateEmail,
		StateOf(testForm(Form{Username: true}), "https://example.test/login", testArea).State)
}

func TestACodeBoxWithNoPasswordBoxIsACodeRequest(t *testing.T) {
	where := StateOf(testForm(Form{OTP: true}), "https://example.test/login/verify", testArea)
	require.Equal(t, StateOTP, where.State)
	require.NotEmpty(t, where.Prompt)
}

func TestTheAccountAreaOrASignOutLinkIsSignedIn(t *testing.T) {
	require.Equal(t, StateSignedIn,
		StateOf(testForm(Form{}), "https://example.test/billing/statements", testArea).State)
	require.Equal(t, StateSignedIn,
		StateOf(testForm(Form{SignOutLink: true}), "https://example.test/", testArea).State)
	require.Equal(t, StateSignedIn,
		StateOf(testForm(Form{Text: "Welcome back. Sign out"}), "https://example.test/", testArea).State)
}

func TestAPageThatAsksWhichSecondFactorToUseIsItsOwnState(t *testing.T) {
	asked := StateOf(testForm(Form{
		Text: "Choose how you would like to verify your identity\n\nAuthenticator app\nText message\nPasskey",
	}), "https://login.example.test/mfa", testArea)
	require.Equal(t, StateFactor, asked.State)
	require.NotEmpty(t, asked.Prompt)
}

// A page that lists the ways to verify is a choice before it is a code box:
// each control's words are about a code, and read as a code request the
// sign-in types six digits into a list of buttons.
func TestAPageOfferingTheWaysToVerifyIsAChoiceAndNotACodeRequest(t *testing.T) {
	offered := testForm(Form{
		OTP: true,
		Text: "How would you like us to verify it is you?\n\n" +
			"Authenticator app\nText message to the phone ending 04\nPasskey",
	})
	require.Equal(t, StateFactor, StateOf(offered, "https://login.example.test/mfa", testArea).State)

	// The page after the choice still asks for the code.
	asked := testForm(Form{OTP: true, Text: "Enter the 6-digit code from your authenticator app"})
	require.Equal(t, StateOTP, StateOf(asked, "https://login.example.test/mfa/code", testArea).State)
}

func TestTheFactorQuestionNeverOutranksAFormOrASignedInPage(t *testing.T) {
	// A password box is the password step even on a page offering the choice,
	// and a signed-in page that happens to describe the options is signed in.
	require.Equal(t, StatePassword, StateOf(testForm(Form{
		Password: true, Text: "Choose how to verify: authenticator app or text message",
	}), "https://login.example.test/mfa", testArea).State)

	require.Equal(t, StateSignedIn, StateOf(testForm(Form{
		SignOutLink: true,
		Text:        "Security settings: choose how to verify sign in — authenticator app or text message",
	}), "https://example.test/account", testArea).State)
}

func TestAPageWithNoneOfItIsThePersonStillDriving(t *testing.T) {
	require.Equal(t, StateInteractive,
		StateOf(testForm(Form{}), "https://example.test/", testArea).State)
}

func TestABlockedPageIsABlockingCheckBeforeAnythingElseOnThePage(t *testing.T) {
	where := StateOf(testForm(Form{
		Password: true, Text: "Access Denied. Reference #18.4f",
	}), "https://example.test/login", testArea)
	require.Equal(t, StateCaptcha, where.State)
	require.True(t, where.Blocking)
}

// A sign-in page's small print (copyright, a freephone number, policy links)
// is not a refusal.
func TestACopyrightLineAndAPhoneNumberAreNotABlockedPage(t *testing.T) {
	where := StateOf(testForm(Form{
		Password: true, Username: true,
		Text: "Let's get you logged in.\n*Email\n*Password\nRemember my email\nLOG IN\n" +
			"Reset password\nCall us (800) 555-0142\nPrivacy\nDisclaimer\nStates of Operation\n" +
			"Terms of Use\nSitemap\n©2010-2026 Example Indemnity Co.",
	}), "https://login.example.test/saml20/idp/sso", testArea)
	require.Equal(t, StatePassword, where.State)
	require.False(t, where.Blocking)
}

// The combined sign-in form: StateOf calls it the password step, so the
// password step has to fill the username too.

// combinedFormPage answers the module's own selectors and records what was
// typed, in order. `username` is whether the page shows a box for one.
func combinedFormPage(username bool) *browser.StubPage {
	page := &browser.StubPage{Location: "https://login.example.test/"}
	page.OnCount = func(selector string) (int, error) {
		if strings.Contains(selector, "submit") {
			return 1, nil
		}
		return 0, nil
	}
	page.OnCheck = func(string) (bool, error) { return true, nil }
	if !username {
		page.Missing = func(selector string) bool { return selector == editable(usernameSelector) }
	}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "LOG IN", Typed: true})
	return page
}

// offersControls makes a stub page answer the control census with the controls
// given, and mark whichever the rule picked. A stub that is asked nothing
// answers nothing, which is a page showing a fill no control at all.
func offersControls(page *browser.StubPage, controls ...SubmitControl) {
	inner := page.OnEvaluate
	for at := range controls {
		controls[at].At = at
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case submitControlsScript:
			encoded, err := json.Marshal(controls)
			if err != nil {
				return nil, err
			}
			var out any
			return out, json.Unmarshal(encoded, &out)
		case markSubmitScript:
			return true, nil
		}
		if inner != nil {
			return inner(script, arg)
		}
		return nil, nil
	}
}

// offersChoices makes a stub page answer the factor-page reading with the
// menu given, and mark whichever the ranking picked. A stub that is asked
// nothing answers nothing, which is a factor page with no choices on it.
func offersChoices(page *browser.StubPage, choices ...FactorChoice) {
	inner := page.OnEvaluate
	for at := range choices {
		choices[at].At = at
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case factorChoicesScript:
			encoded, err := json.Marshal(choices)
			if err != nil {
				return nil, err
			}
			var out any
			return out, json.Unmarshal(encoded, &out)
		case markFactorScript:
			return true, nil
		}
		if inner != nil {
			return inner(script, arg)
		}
		return nil, nil
	}
}

func testDraft() Draft {
	return Draft{
		BillerID: domain.BillerErie, Home: "https://example.test",
		SignIn: "https://example.test/login", Landing: "https://example.test/account",
		AccountArea: testArea,
	}
}

func TestAFormWithBothBoxesGetsTheUsernameTypedBeforeThePassword(t *testing.T) {
	page := combinedFormPage(true)
	_, err := testDraft().FillPassword(page, "invented", "someone@example.test")
	require.NoError(t, err)

	require.Len(t, page.Filled, 2)
	require.Contains(t, page.Filled[0].Selector, `input[type="email"]`)
	require.Equal(t, "someone@example.test", page.Filled[0].Value)
	require.Equal(t, passwordSelector, page.Filled[1].Selector)
	require.Equal(t, "invented", page.Filled[1].Value)
	require.Equal(t, []string{submitMark}, page.Clicked)
}

func TestAPasswordOnlyPageFillsThePasswordAlone(t *testing.T) {
	page := combinedFormPage(false)
	_, err := testDraft().FillPassword(page, "invented", "someone@example.test")
	require.NoError(t, err)
	require.Len(t, page.Filled, 1)
	require.Equal(t, passwordSelector, page.Filled[0].Selector)
}

func TestAPasswordStepWithNoEmailToTypeFillsNothingItWasNotGiven(t *testing.T) {
	page := combinedFormPage(true)
	_, err := testDraft().FillPassword(page, "invented", "")
	require.NoError(t, err)
	require.Len(t, page.Filled, 1)
	require.Equal(t, passwordSelector, page.Filled[0].Selector)
}

func TestTheEmailStepTicksRememberAndSubmits(t *testing.T) {
	page := combinedFormPage(true)
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Len(t, page.Filled, 1)
	require.Equal(t, "someone@example.test", page.Filled[0].Value)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.Equal(t, agent.PressedButton, step.Pressed)
	require.Equal(t, "LOG IN", step.Words, "the trail is told what the round pressed")
}

func TestASubmitButtonNobodyRenderedIsTheEnterKey(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page)
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Empty(t, page.Clicked)
	require.Equal(t, []string{"Enter"}, page.Pressed)
	require.Equal(t, agent.PressedEnter, step.Pressed,
		"and told that it pressed Enter because nothing matched")
}

// A two-step sign-in's first page offers a type-less "Next" beside "Sign up",
// and its only type="submit" is an invisible "OK". Pressing that falls through
// to Enter, which a React form ignores.
func TestTheButtonOfATwoStepSignInIsPressedByWhatItSays(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page,
		SubmitControl{Kind: agent.PressedButton, Words: "Sign up"},
		SubmitControl{Kind: agent.PressedButton, Words: "Next"})
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.Equal(t, "Next", step.Words, "the other door is not pressed")
}

func TestAnswerTypesTheCodeOnlyWhenThePageIsAskingForOne(t *testing.T) {
	page := combinedFormPage(true)
	step, err := testDraft().Answer(page, State{State: StatePassword}, "123456", "")
	require.NoError(t, err)
	require.False(t, step.Acted)
	require.Empty(t, page.Filled)

	step, err = testDraft().Answer(page, State{State: StateOTP}, "123456", "")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, otpSelector, page.Filled[0].Selector)
	require.Equal(t, "123456", page.Filled[0].Value)
}

// The rule, over what a page reported. Each row is a control a real portal
// showed.
func TestWhatTheSubmitRuleWillAndWillNotPress(t *testing.T) {
	for _, tc := range []struct {
		words string
		typed bool
		rank  int
	}{
		{words: "Next", rank: 1},
		{words: "Continue", rank: 1},
		{words: "LOG IN", rank: 1},
		{words: "Sign In", rank: 1},
		{words: "Log On", rank: 1},
		{words: "Continue →", rank: 1},
		{words: "Verify", rank: 1},
		{words: "OK", typed: true, rank: 2},
		{words: "Search", typed: true, rank: 2},
		{words: "Sign in to your account", rank: 3},
		{words: "Continue to verification", rank: 3},
		{words: "Sign up", rank: 0},
		{words: "Sign out", rank: 0},
		{words: "Cancel", rank: 0},
		{words: "Forgot password?", rank: 0},
		{words: "Signup", rank: 0},
		{words: "", rank: 0},
	} {
		require.Equalf(t, tc.rank, SubmitRank(SubmitControl{Words: tc.words, Typed: tc.typed}),
			"the rank of %q", tc.words)
	}
}

// A hidden submit must not win over a visible Next, and a disabled control of
// the right rank must not lose to an enabled one of the wrong rank.
func TestTheBestSubmitPrefersTheWordsAndThenWhatCanBePressed(t *testing.T) {
	best, found := BestSubmit([]SubmitControl{
		{At: 0, Words: "Search", Typed: true},
		{At: 1, Words: "Next", Disabled: true},
	})
	require.True(t, found)
	require.Equal(t, "Next", best.Words, "a disabled control of the right rank is waited for")

	best, found = BestSubmit([]SubmitControl{
		{At: 0, Words: "Continue", Disabled: true},
		{At: 1, Words: "Continue"},
	})
	require.True(t, found)
	require.Equal(t, 1, best.At, "and within one rank the pressable one wins")

	_, found = BestSubmit([]SubmitControl{{Words: "Sign up"}, {Words: "Cancel"}})
	require.False(t, found)
}

// The widget enables its own button on the event the fill raised, so a control
// that is disabled when it is found is given a moment rather than pressed or
// skipped.
func TestAControlThatIsNotPressableYetIsWaitedForAndThenPressed(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Next", Disabled: true})
	waited := false
	page.OnWaitFor = func(script string, _ time.Duration) error {
		if script == submitEnabledScript {
			waited = true
		}
		return nil
	}
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.True(t, waited)
	require.Equal(t, []string{submitMark}, page.Clicked)
	require.True(t, step.Waited)
}

// And one that never becomes pressable is not pressed and not counted as
// pressed: the round falls through to Enter and says so.
func TestAControlThatNeverBecomesPressableIsNotPressed(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Next", Disabled: true})
	page.OnWaitFor = func(script string, _ time.Duration) error {
		if script == submitEnabledScript {
			return errors.New("still disabled")
		}
		return nil
	}
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Empty(t, page.Clicked)
	require.Equal(t, agent.PressedEnter, step.Pressed)
}

// A round says whether the page moved, which is what the loop guard gives up
// on and what the trail reports.
func TestAStepSaysWhetherThePageChanged(t *testing.T) {
	page := combinedFormPage(true)
	signature := "before"
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if strings.Contains(script, "signOutLink") || script == submitControlsScript ||
			script == markSubmitScript || script == commitFieldScript {
			return inner(script, arg)
		}
		return signature, nil
	}
	step, err := testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.False(t, step.Changed, "the page answered the same signature both times")

	page = combinedFormPage(true)
	moved := 0
	inner = page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == submitControlsScript || script == markSubmitScript || script == commitFieldScript {
			return inner(script, arg)
		}
		moved++
		return "page " + strconv.Itoa(moved), nil
	}
	step, err = testDraft().FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.True(t, step.Changed)
}

// The rule, over the words one choice carries. The ones that rank nothing are
// the ways nobody unattended can answer. "Call me with a code from my app" and
// "Text me a code" carry the app's words, so they are read before the app.
func TestWhatTheFactorRuleWillAndWillNotChoose(t *testing.T) {
	for _, tc := range []struct {
		words string
		kind  string
	}{
		{words: "Authenticator app", kind: "totp"},
		{words: "Use your authenticator app", kind: "totp"},
		{words: "Google Authenticator", kind: "totp"},
		{words: "Okta Verify", kind: "totp"},
		{words: "Authy", kind: "totp"},
		{words: "Verification app", kind: "totp"},
		{words: "App-based verification", kind: "totp"},
		{words: "Enter a code from your app", kind: "totp"},
		{words: "One-time passcode app", kind: "totp"},
		{words: "TOTP", kind: "totp"},
		{words: "Text message", kind: "sms"},
		{words: "Text me a code", kind: "sms"},
		{words: "SMS to the number on file", kind: "sms"},
		{words: "Call me with a code from your authenticator app", kind: ""},
		{words: "Voice call", kind: ""},
		{words: "Text me a code from the app", kind: "sms"},
		{words: "Passkey", kind: ""},
		{words: "Security key", kind: ""},
		{words: "Email", kind: "email"},
		{words: "Email me a code at j•••@example.test", kind: "email"},
		{words: "Reset multifactor authentication", kind: ""},
		{words: "Answer your security questions", kind: ""},
		{words: "", kind: ""},
	} {
		require.Equalf(t, tc.kind, FactorKind(tc.words), "the kind of %q", tc.words)
	}
}

func TestChooseFactorPrefersTheAuthenticatorAndFallsBackToTheText(t *testing.T) {
	// Both on one menu: the app is taken, wherever on the page it sits.
	best, found := BestFactor([]FactorChoice{
		{At: 0, Kind: "radio", Words: "Text message", Selects: true},
		{At: 1, Kind: "radio", Words: "Authenticator app", Selects: true},
		{At: 2, Kind: "radio", Words: "Security key", Selects: true},
	}, "")
	require.True(t, found)
	require.Equal(t, "Authenticator app", best.Words)
	require.Equal(t, 1, best.At)

	// The text is what is left when the app is not offered, because parking a
	// challenge for a person beats failing the pull.
	best, found = BestFactor([]FactorChoice{
		{At: 0, Kind: "radio", Words: "Security key", Selects: true},
		{At: 1, Kind: "radio", Words: "Text message", Selects: true},
	}, "")
	require.True(t, found)
	require.Equal(t, "Text message", best.Words)

	// And a menu of things nobody unattended can answer picks none of them.
	_, found = BestFactor([]FactorChoice{
		{At: 0, Kind: "radio", Words: "Passkey", Selects: true},
		{At: 1, Kind: "radio", Words: "Call me", Selects: true},
	}, "")
	require.False(t, found)
}

// Three radios, one an app, and a Continue: a radio selects and sends nothing,
// so the page's own button has to be pressed after it.
func TestChoosingARadioSelectsItAndThenPressesThePagesOwnButton(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Continue"})
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Text message", Selects: true},
		FactorChoice{Kind: "radio", Words: "Authenticator app", Selects: true},
		FactorChoice{Kind: "radio", Words: "Security key", Selects: true})

	factor, err := testDraft().ChooseFactor(page, "")

	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind)
	require.Equal(t, "Authenticator app", factor.Chose)
	require.True(t, factor.Confirmed, "a selection is not a submission")
	require.Equal(t, "Continue", factor.Step.Words)
	require.Equal(t, []string{factorMark, submitMark}, page.Clicked,
		"the choice, and then the button that sends it")
	require.Len(t, factor.Choices, 3, "and the trail is told what the whole menu was")
}

// Each choice is itself a `button[type=submit]`, so it has sent the page on and
// a second press would land on whatever it sent it to.
func TestChoosingASubmitButtonPressesNothingAfterIt(t *testing.T) {
	page := combinedFormPage(true)
	offersChoices(page,
		FactorChoice{Kind: agent.PressedButton, Words: "Phone app Use an authenticator app of your choosing"},
		FactorChoice{Kind: agent.PressedButton, Words: "This device Use a fingerprint or a security key"},
		FactorChoice{Kind: "link", Words: "Reset multifactor authentication"})

	factor, err := testDraft().ChooseFactor(page, "")

	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind)
	require.False(t, factor.Confirmed)
	require.Equal(t, []string{factorMark}, page.Clicked, "once, and only once")
	require.False(t, factor.Step.Acted)
}

// A page whose menu nothing recognised says what was on it rather than
// guessing.
func TestAFactorPageNothingRecognisesStillSaysWhatWasOnOffer(t *testing.T) {
	page := combinedFormPage(true)
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Passkey", Selects: true},
		FactorChoice{Kind: "radio", Words: "Answer your security questions", Selects: true})

	factor, err := testDraft().ChooseFactor(page, "")

	require.NoError(t, err)
	require.Empty(t, factor.Kind)
	require.Empty(t, factor.Chose)
	require.Empty(t, page.Clicked, "nothing is pressed at a page nothing was recognised on")
	require.Equal(t, []string{"Passkey", "Answer your security questions"},
		[]string{factor.Choices[0].Words, factor.Choices[1].Words})
}

// A choice the page will not let anybody take ends the round, naming the
// choice and carrying the menu, instead of Playwright's thirty-second retry log.
func TestAChoiceThePageWillNotTakeFailsWithTheChoiceItTried(t *testing.T) {
	page := combinedFormPage(true)
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Text me a code", Selects: true},
		FactorChoice{Kind: "radio", Words: "Use Google Authenticator", Selects: true})
	page.OnChoose = func(string, string) (string, error) {
		return "", errors.New("playwright: timeout: Timeout 5000ms exceeded.\nCall log:\n  - retrying click action")
	}

	factor, err := testDraft().ChooseFactor(page, "")

	require.Error(t, err)
	require.Contains(t, err.Error(), `"Use Google Authenticator"`)
	require.NotContains(t, err.Error(), "Call log", "the driver's own account stays off this sentence")
	require.Equal(t, "Use Google Authenticator", factor.Chose)
	require.Equal(t, "totp", factor.Kind)
	require.Len(t, factor.Choices, 2, "and the whole menu is still answered")
}

// The last resort, and the reason it is on the trail: a page that let nothing
// through until Playwright's actionability checks were skipped is a page to
// look at.
func TestAChoiceTakenPastThePagesOwnChecksSaysSo(t *testing.T) {
	page := combinedFormPage(true)
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Continue"})
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Text me a code", Selects: true},
		FactorChoice{Kind: "radio", Words: "Use Google Authenticator", Selects: true})
	page.OnChoose = func(string, string) (string, error) { return browser.TookForce, nil }

	factor, err := testDraft().ChooseFactor(page, "")

	require.NoError(t, err)
	require.True(t, factor.Forced)
	require.True(t, factor.Confirmed, "and the page's own button is still what sends it")
}

// A page that answers no menu at all is its own finding: the controls are
// somewhere this reading does not go.
func TestAFactorPageWithNoReadableChoicesIsAnEmptyMenuAndNotAnError(t *testing.T) {
	page := combinedFormPage(true)
	factor, err := testDraft().ChooseFactor(page, "")
	require.NoError(t, err)
	require.Empty(t, factor.Kind)
	require.Empty(t, factor.Choices)
}

func TestADraftAnswersNoBillsAndANoteRatherThanASignIn(t *testing.T) {
	notes := &Notes{}
	out, err := testDraft().FetchBills(Call{Notes: notes})
	require.NoError(t, err)
	require.Empty(t, out.Bills)
	require.False(t, out.NeedsSignIn, "a draft never reports a sign-in the household does not owe")
	require.Contains(t, notes.List()[0], "is a draft")
}

// A code page whose box is named only by its label. Every word is invented;
// the shape is a real identity provider's.
//
//	<form>
//	  <h1>Authenticator App</h1>
//	  <p>Enter the code from your authenticator app.</p>
//	  <label for="react-aria-42">Passcode</label>
//	  <input id="react-aria-42" type="text">
//	  <button type="submit">Continue</button>
//	</form>
func erieCodeReading() Reading {
	return Reading{
		Fields: []Field{{
			Type: "text", ID: "react-aria-42", Label: "Passcode",
			Context: "Authenticator App · Enter the code from your authenticator app.",
		}},
		Submits: 1,
		Heading: "Authenticator App",
		Text:    "Authenticator App\nEnter the code from your authenticator app.\nPasscode\nContinue\nCancel",
	}
}

func TestABoxTheLabelCallsAPasscodeIsACodeBoxHoweverItIsNamed(t *testing.T) {
	form := FormFrom(erieCodeReading())

	require.True(t, form.OTP, "the label is the only thing on the page that says what the box is for")
	require.False(t, form.Username)
	require.False(t, form.Password)
	require.Equal(t, 1, form.Boxes)
	require.Equal(t, map[string]int{"text": 1}, form.Inputs)
	require.Equal(t, StateOTP, StateOf(form, "https://authnprd.example.test/saml20/idp/sso", testArea).State)
}

func TestTheFormsOwnHeadingNamesABoxItsLabelDoesNot(t *testing.T) {
	// The same page with the label taken off: what the form is for is still
	// written above the box.
	read := erieCodeReading()
	read.Fields[0].Label = ""
	require.True(t, FormFrom(read).OTP)

	// And a box in a form that says nothing about codes is nobody's code box,
	// whatever else is on the page.
	read.Fields[0].Context = "Sign On · Enter your email address to continue."
	require.False(t, FormFrom(read).OTP)
}

func TestARadioNamedAfterACodeIsStillNotABoxToTypeOneInto(t *testing.T) {
	form := FormFrom(Reading{Fields: []Field{
		{Type: "radio", Name: "textCode", Label: "Text message"},
		{Type: "radio", Name: "authApp", Label: "Authenticator app"},
	}, Submits: 1})

	require.False(t, form.OTP)
	require.Equal(t, 0, form.Boxes)
	require.Equal(t, map[string]int{"radio": 2}, form.Inputs)
}

func TestABoxAskingForAnEmailIsAUsernameBoxWhateverThePortalCallsIt(t *testing.T) {
	// Azure AD B2C names its own box `signInName`, which carries none of the
	// words a username box is recognised by.
	form := FormFrom(Reading{Fields: []Field{
		{Type: "email", ID: "signInName", Label: "Email"},
		{Type: "password", ID: "password", Label: "Password"},
	}, Submits: 1, Heading: "Sign in"})

	require.True(t, form.Username)
	require.True(t, form.Password)
	require.Equal(t, StatePassword, StateOf(form, "https://login.example.test/tenant/oauth2/v2.0/authorize", testArea).State)
}

// The page that offers the ways to verify as buttons and asks nothing.
//
//	<h1>Select Method</h1>
//	<p>Select an MFA method to use to sign on to your account.</p>
//	<form>
//	  <button type="submit">Phone app · Use an authenticator app of your choosing…</button>
//	  <button type="submit">This device · Use a fingerprint, facial scan…</button>
//	  <input type="hidden" name="method">
//	</form>
//	<a href="#">Reset multifactor authentication</a>
func erieFactorReading() Reading {
	return Reading{
		Submits: 2,
		Heading: "Select Method",
		Text: "Select Method\nSelect an MFA method to use to sign on to your account.\n" +
			"Phone app\nDefault\nUse an authenticator app of your choosing that generates a " +
			"security code to authenticate. (Better)\n" +
			"This device\nUse a fingerprint, facial scan, or a security key to authenticate on " +
			"your mobile device or desktop.\nReset multifactor authentication",
	}
}

func TestAPageOfButtonsWithNoBoxesThatNamesTheMethodsIsTheFactorPage(t *testing.T) {
	form := FormFrom(erieFactorReading())

	require.Equal(t, 0, form.Boxes, "the hidden inputs are nothing anybody can type into")
	require.Equal(t, StateFactor,
		StateOf(form, "https://authnprd.example.test/saml20/idp/sso", testArea).State,
		"a page that asks nothing and lists the ways is still the choice of factor")
}

func TestAPageOfButtonsThatIsNotAboutMethodsIsLeftAlone(t *testing.T) {
	read := erieFactorReading()
	read.Heading = "Your policies"
	read.Text = "Your policies\nPay a bill\nView documents"
	require.Equal(t, StateInteractive,
		StateOf(FormFrom(read), "https://example.test/", testArea).State)
}

// A "Reset multifactor authentication" link on the same page is not a factor:
// it carries no word the rule reads.
func TestTheResetLinkOnAFactorPageIsNotAChoice(t *testing.T) {
	require.Equal(t, 0, FactorRank(FactorChoice{
		Kind: "link", Words: "Reset multifactor authentication",
	}))
}

// What the provider wrote into a choice about the household comes off: a trail
// is pasted further than a screen.
func TestTheHouseholdsOwnDigitsDoNotReachTheTrailThroughAMenu(t *testing.T) {
	page := combinedFormPage(true)
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Text message to (***) ***-0142", Selects: true},
		FactorChoice{Kind: "radio", Words: "Authenticator app", Selects: true})

	choices, err := FactorChoices(page)

	require.NoError(t, err)
	require.Equal(t, "Text message to (***) ***-…", choices[0].Words)
	require.Equal(t, "Authenticator app", choices[1].Words,
		"a choice with nothing of theirs in it is left as the provider wrote it")
	require.Equal(t, "sms", FactorKind(choices[0].Words),
		"and what is left still reads as a text message")
}

func TestAnswerFillsTheBoxTheLabelNamedWhenNothingIsCalledACode(t *testing.T) {
	page := combinedFormPage(true)
	page.Missing = func(selector string) bool { return selector == otpSelector }

	step, err := testDraft().Answer(page, State{State: StateOTP}, "123456", "")

	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Len(t, page.Filled, 1)
	require.Equal(t, otpFallbackSelector, page.Filled[0].Selector)
	require.Equal(t, "123456", page.Filled[0].Value)
}

// A widget renders itself after the page has settled, and a classifier that
// reads the gap sees a page with nothing on it.
func TestAPageThatIsStillPaintingIsReadAgainRatherThanCalledUnrecognised(t *testing.T) {
	page := combinedFormPage(true)
	page.Location = "https://login.example.test/tenant/oauth2/v2.0/authorize"
	readings := []Reading{
		{Text: ""},
		{Fields: []Field{
			{Type: "email", ID: "signInName", Label: "Email"},
			{Type: "password", ID: "password", Label: "Password"},
		}, Submits: 1, Heading: "Sign in", Text: "Sign in\nEmail\nPassword"},
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		next := readings[0]
		if len(readings) > 1 {
			readings = readings[1:]
		}
		return asAny(t, next), nil
	}
	waited := 0
	page.OnWaitFor = func(script string, timeout time.Duration) error {
		require.Equal(t, somethingOnPageScript, script)
		waited++
		return nil
	}

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State)
	require.Equal(t, 1, waited, "the empty page is waited on once, not read twice for nothing")
}

func TestAPageThatNeverPaintsIsStillAnswered(t *testing.T) {
	page := combinedFormPage(true)
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		return asAny(t, Reading{Text: "Loading…"}), nil
	}
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StateInteractive, where.State)
}

// The public page in front of the sign-in page: a search box, no form, and a
// link to where the form is. What a person does there is press the link.
func TestAPageWhoseOnlyWayInIsASignInLinkIsFollowedOnce(t *testing.T) {
	page := combinedFormPage(true)
	page.Location = "https://www.example.test/myaccount"
	readings := []Reading{
		{Fields: []Field{{Type: "search", Name: "q", Label: "Search"}},
			Heading: "My Account", Text: "My Account\nSearch\nSign in\nSign up"},
		{Fields: []Field{
			{Type: "email", ID: "signInName", Label: "Email"},
			{Type: "password", ID: "password", Label: "Password"},
		}, Submits: 1, Heading: "Sign in", Text: "Sign in\nEmail\nPassword"},
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		next := readings[0]
		if len(readings) > 1 {
			readings = readings[1:]
		}
		return asAny(t, next), nil
	}
	var pressed []*regexp.Regexp
	page.OnClickText = func(selectors string, pattern *regexp.Regexp) (bool, error) {
		require.Equal(t, signInControlSelector, selectors)
		pressed = append(pressed, pattern)
		return true, nil
	}

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State)
	require.Len(t, pressed, 1, "the link is followed once, not once per reading")
	require.Regexp(t, pressed[0], "Sign In")
	require.NotRegexp(t, pressed[0], "Sign up")
	require.NotRegexp(t, pressed[0], "Sign in with Google")
}

func TestAPageWithNoSignInLinkOnItIsLeftWhereItIs(t *testing.T) {
	page := combinedFormPage(true)
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		return asAny(t, Reading{Text: "We are sorry. This page is not available."}), nil
	}
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	page.OnClickText = func(string, *regexp.Regexp) (bool, error) { return false, nil }

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StateInteractive, where.State)
}

// The hop a script usually takes: a page with nothing to type into and one
// button (a hidden SAMLRequest under a single Continue) is not asking anything.
func TestABridgePageIsPressedThroughAndCountedInTheTrail(t *testing.T) {
	// The bridge twice: a page with no box on it is read, waited on once in
	// case it was still painting, and read again before it is believed.
	bridge := Reading{
		Fields: []Field{{Type: "hidden", Name: "SAMLRequest"}}, Submits: 1,
		Text: "Note: Your browser does not support JavaScript, Press Continue to proceed...",
	}
	page := bridgePageStub(t, []Reading{
		bridge, bridge,
		{Fields: []Field{
			{Type: "email", ID: "username", Label: "Email"},
			{Type: "password", ID: "password", Label: "Password"},
		}, Submits: 1, Heading: "Sign On", Text: "Sign On\nEmail\nPassword"},
	})
	defer Forget(page)

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State)
	require.Equal(t, 1, where.Bridged, "the trail says a bridge was pressed through")
	require.Equal(t, []string{agent.SubmitSelector}, page.Clicked)
}

// A page that answers a press with the same one button is a wall: two presses
// and the sign-in stops, however often the poll asks again.
func TestAPageThatKeepsOfferingOneButtonIsPressedTwiceAndNoMore(t *testing.T) {
	page := bridgePageStub(t, []Reading{
		{Fields: []Field{{Type: "hidden", Name: "nonce"}}, Submits: 1, Text: "One moment…"},
	})
	defer Forget(page)

	for asked := 0; asked < 4; asked++ {
		where, err := testDraft().Classify(page)
		require.NoError(t, err)
		require.Equal(t, StateInteractive, where.State)
	}

	require.Len(t, page.Clicked, bridgeLimit, "two fruitless presses, and then it is left alone")
}

// Two buttons is a question — accept or decline, this way or that — and a
// page with a box on it is a form somebody is meant to fill in.
func TestAPageOfferingAChoiceOrAFormIsNotABridge(t *testing.T) {
	require.True(t, bridgePage(Form{Submits: 1}))
	require.False(t, bridgePage(Form{Submits: 2}), "two buttons is a question")
	require.False(t, bridgePage(Form{Submits: 0}), "nothing to press is nothing to press")
	require.False(t, bridgePage(Form{Submits: 1, Boxes: 1}), "a box is a form to fill in")
	require.False(t, bridgePage(Form{Submits: 1, SignOutLink: true}), "a signed-in page is not a hop")
}

// The probe's reading of the way-in step is the follow rule's own words: "Sign
// up" is the other door, "Sign in with Google" is somebody else's, and a
// control merely mentioning signing in is prose. It must never press one.
func TestTheWayInIsReportedByItsWordsAndNothingIsPressed(t *testing.T) {
	page := &browser.StubPage{Location: "https://portal.example.test/myaccount"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != controlTextsScript {
			return nil, nil
		}
		return asAny(t, []string{
			"Sign in", "Sign up", "Log In", "Sign in with Google",
			"Sign in to see your bill", "Search",
		}), nil
	}

	found, err := SignInControls(page)

	require.NoError(t, err)
	require.Equal(t, []string{"Sign in", "Log In"}, found)
	require.Empty(t, page.Clicked, "a probe reports the way in; it does not take it")
}

// bridgePageStub answers the readings in order and records what was pressed.
// The last reading stands for every later one, which is how a page that never
// becomes anything is written.
func bridgePageStub(t *testing.T, readings []Reading) *browser.StubPage {
	t.Helper()
	page := &browser.StubPage{Location: "https://gateway.example.test/my.policy"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		next := readings[0]
		if len(readings) > 1 {
			readings = readings[1:]
		}
		return asAny(t, next), nil
	}
	page.OnClickText = func(string, *regexp.Regexp) (bool, error) { return false, nil }
	return page
}

// asAny is a reading as the driver would hand it back: maps and slices of any.
func asAny(t *testing.T, value any) any {
	t.Helper()
	encoded, err := json.Marshal(value)
	require.NoError(t, err)
	var out any
	require.NoError(t, json.Unmarshal(encoded, &out))
	return out
}

// A signed-in session sent from the sign-in entry to an error page with nothing
// on it: asking for the account page lands inside when the session is good and
// on the form when it is not.
func TestAnUnplaceablePageIsSentToTheAccountPageOnce(t *testing.T) {
	stranded := Reading{Heading: "Looks like we got our wires crossed.",
		Text: "We weren't able to process your request at this time. Return to dashboard"}
	module := testDraft()
	module.AccountPage = "https://example.test/billing/history"

	t.Run("a good session lands inside", func(t *testing.T) {
		page := bridgePageStub(t, []Reading{stranded, stranded, {Heading: "Bills", Text: "Your bills"}})
		page.Location = "https://example.test/errors/503"
		defer Forget(page)

		where, err := module.Classify(page)

		require.NoError(t, err)
		require.Equal(t, StateSignedIn, where.State)
		require.Equal(t, []string{module.AccountPage}, page.Visited)
	})

	t.Run("a lapsed session lands on the form", func(t *testing.T) {
		page := bridgePageStub(t, []Reading{stranded, stranded,
			{Fields: []Field{{Type: "email", ID: "username", Label: "Email"}}, Submits: 1}})
		page.Location = "https://example.test/errors/503"
		page.OnGoto = func(string) error { page.Location = "https://example.test/login"; return nil }
		defer Forget(page)

		where, err := module.Classify(page)

		require.NoError(t, err)
		require.Equal(t, StateEmail, where.State)
	})

	t.Run("a page that stays unplaceable is sent there once", func(t *testing.T) {
		page := bridgePageStub(t, []Reading{stranded})
		page.Location = "https://example.test/errors/503"
		page.OnGoto = func(string) error { page.Location = "https://example.test/errors/503"; return nil }
		defer Forget(page)

		for asked := 0; asked < 3; asked++ {
			where, err := module.Classify(page)
			require.NoError(t, err)
			require.Equal(t, StateInteractive, where.State)
		}
		require.Len(t, page.Visited, 1)
	})

	t.Run("a module with no account page goes nowhere", func(t *testing.T) {
		page := bridgePageStub(t, []Reading{stranded})
		page.Location = "https://example.test/errors/503"
		defer Forget(page)

		where, err := testDraft().Classify(page)

		require.NoError(t, err)
		require.Equal(t, StateInteractive, where.State)
		require.Empty(t, page.Visited)
	})
}
