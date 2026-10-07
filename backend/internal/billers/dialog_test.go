package billers

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// Sign-ins that end at a dialog or wall: a code dialog over a carrier's form,
// a setup dialog over an insurer's, and an interstitial check in front of another's.
// Every word is invented except the providers' own sentences, and every digit
// is invented.

// carrierCodeDialog is a carrier's code dialog as the page reading answers it:
// six one-character boxes in one run, a trusted-device box, and the dialog's
// own words. The form behind it is not in the reading at all.
func carrierCodeDialog() Reading {
	fields := make([]Field, 0, 7)
	for range 6 {
		fields = append(fields, Field{Type: "text", AriaLabel: "Digit", Group: 1})
	}
	fields = append(fields, Field{Type: "checkbox"})
	return Reading{
		Fields: fields, Heading: "We sent your code", Dialog: "We sent your code",
		Text: "We sent your code\n\nEnter the code we sent to +•••••••0000 below.\n\n" +
			"Having trouble? Resend code\n\n" +
			"Don’t require verification for future logins on this trusted device.\n\n" +
			"Verify another way\nConfirm",
	}
}

func TestACodeDialogIsACodeRequestInItsOwnWords(t *testing.T) {
	form := FormFrom(carrierCodeDialog())
	require.True(t, form.OTP)
	require.False(t, form.Password, "the password box behind the dialog is not read")
	require.False(t, form.Username, "a box for one digit is not a username box")
	require.Equal(t, 6, form.Segments)
	require.Equal(t, "We sent your code", form.Dialog)

	where := StateOf(form, "https://www.example.test/sign-in", nil)
	require.Equal(t, StateOTP, where.State)
	require.Equal(t, "Enter the code we sent to +•••••••0000 below.", where.Prompt,
		"the page's own sentence, for whoever answers it")
	require.Equal(t, "sms", where.Method,
		"a phone number is a text, and a kept authenticator key is never minted into it")
}

func TestOnlyARunOfFourToEightOneCharacterBoxesIsACode(t *testing.T) {
	run := func(sizes ...int) Form {
		var fields []Field
		for group, size := range sizes {
			for range size {
				fields = append(fields, Field{Type: "tel", Group: group + 1})
			}
		}
		return FormFrom(Reading{Fields: fields})
	}
	require.Equal(t, 4, run(4).Segments)
	require.Equal(t, 8, run(8).Segments)
	require.False(t, run(3).OTP, "a phone number split three ways is not a code")
	require.False(t, run(9).OTP)
	require.False(t, run(6, 6).OTP, "two runs is a question this reading does not guess at")

	masked := make([]Field, 6)
	for at := range masked {
		masked[at] = Field{Type: "password", Group: 1}
	}
	form := FormFrom(Reading{Fields: masked})
	require.True(t, form.OTP, "digits drawn as dots are still a code")
	require.False(t, form.Password)

	require.False(t, FormFrom(Reading{Fields: []Field{
		{Type: "text"}, {Type: "text"}, {Type: "text"}, {Type: "text"}, {Type: "text"},
	}}).OTP, "boxes in no run are not one")
}

func TestACodeRequestSaysWhereTheCodeWent(t *testing.T) {
	for _, tc := range []struct{ text, ask, channel string }{
		{"We sent your code\nEnter the code we sent to +•••••••0000 below.", "Enter the code we sent to +•••••••0000 below.", "sms"},
		{"Check your inbox\nWe emailed a code to j•••@example.test", "We emailed a code to j•••@example.test", "email"},
		{"Enter the 6-digit code from your authenticator app", "Enter the 6-digit code from your authenticator app", "totp"},
		{"We texted a verification code to your mobile phone.", "We texted a verification code to your mobile phone.", "sms"},
		{"Security check\nType the code", "Type the code", ""},
		// A stand-in code page: the heading says to enter a code, and the
		// sentence under it says whose.
		{"Enter Your Code\nEnter the 6-digit code from your authenticator app.\nCode\nVerify",
			"Enter the 6-digit code from your authenticator app.", "totp"},
		{"Enter your code\nWe emailed a code to j•••@example.test", "We emailed a code to j•••@example.test", "email"},
		{"Having trouble? Resend code", "", ""},
	} {
		ask := CodeAsk(tc.text)
		require.Equal(t, tc.ask, ask, tc.text)
		require.Equal(t, tc.channel, CodeChannel(ask), tc.text)
	}
	where := StateOf(Form{OTP: true, Text: "Verification"}, "https://example.test/", nil)
	require.Equal(t, "Enter the code the provider sent you.", where.Prompt, "our words when the page has none")
	require.Empty(t, where.Method)
}

// The one rule both bill and merchant sign-ins answer a code box by: a box
// that names no channel is the kept key's unless the login's codes are mailed
// or texted.
func TestAKeptKeyAnswersABoxNamingNoChannelUnlessCodesAreSent(t *testing.T) {
	for _, tc := range []struct {
		channel, chose string
		answer         bool
	}{
		{"", "", true},
		{"", "totp", true},
		{"", "email", false},
		{"", "sms", false},
		{"totp", "email", true},
		{"sms", "totp", false},
		{"email", "", false},
	} {
		require.Equal(t, tc.answer, KeyAnswers(tc.channel, tc.chose), "%q, chose %q", tc.channel, tc.chose)
	}
}

// A setup dialog over the form, with one button. Pressing through it would be
// choosing a household's security settings unattended; the sign-in stops and
// says whose step it is.
func TestASecurityProfileSetupEndsTheSignInAndSaysWhoseStepItIs(t *testing.T) {
	welcome := Reading{
		Submits: 2, Heading: "Welcome", Dialog: "dialog",
		Text: "✕\nWelcome\n\nWe need to verify your identity and setup your security profile to ensure " +
			"your information remains secure. This process should only take a few minutes.\nGet Started",
	}
	where := StateOf(FormFrom(welcome), "https://login.northwesternmutual.com/login", nil)
	require.Equal(t, StateFailed, where.State)
	require.Equal(t, enrolmentError, where.Error)

	page := &browser.StubPage{Location: "https://login.northwesternmutual.com/login"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == readFormScript {
			return asAny(t, welcome), nil
		}
		return nil, nil
	}
	pressed := false
	page.OnClickText = func(string, *regexp.Regexp) (bool, error) { pressed = true; return true, nil }
	t.Cleanup(func() { Forget(page) })

	got, err := NewNorthwesternMutual().Classify(page)

	require.NoError(t, err)
	require.Equal(t, StateFailed, got.State)
	require.Equal(t, "Northwestern Mutual wants this login to set up its security profile before it lets "+
		"anybody in. That is a step to take yourself: sign in once on northwesternmutual.com, finish the "+
		"setup there, and then connect again.", got.Prompt)
	require.Empty(t, page.Clicked, "Get Started is not pressed")
	require.False(t, pressed, "and nothing is followed")
}

// A factor page as the page reading answers it: no box, one ARIA radio in a
// radio group, a Next that is not a submit, and words naming an authentication
// app. Every sentence is the portal's; nothing in it is the household's.
func nwmVerifyReading() Reading {
	return Reading{
		Radios: 1, Heading: "Verify Your Account · We’ll send a unique code to verify your identity.",
		Text: "Back to login\nVerify Your Account\nWe’ll send a unique code to verify your identity.\n" +
			"This keeps your login secure and ensures that no one else can gain access to your account.\n" +
			"Enter code from an Authentication App\n" +
			"Replace your authentication method with a new way to receive your code.\nNext",
	}
}

func TestARadioGroupWithNothingToTypeIsTheChoiceOfFactor(t *testing.T) {
	where := StateOf(FormFrom(nwmVerifyReading()), "https://login.northwesternmutual.com/mfaverify", nil)
	require.Equal(t, StateFactor, where.State,
		"a radio to pick, no box, and an authentication app named is the choice of factor")

	counted := nwmVerifyReading()
	counted.Radios = 0
	require.Equal(t, StateInteractive,
		StateOf(FormFrom(counted), "https://login.northwesternmutual.com/mfaverify", nil).State,
		"the radio is what makes it one: the words alone are a page nobody recognises")

	paperless := Reading{Radios: 2, Text: "How would you like your statements?\nPaper\nOnline only\nSave"}
	require.Equal(t, StateInteractive, StateOf(FormFrom(paperless), "https://example.test/prefs", nil).State,
		"radios that name no way to verify are somebody else's question")

	typed := nwmVerifyReading()
	typed.Fields = []Field{{Type: "text", Label: "Passcode"}}
	require.Equal(t, StateOTP, StateOf(FormFrom(typed), "https://example.test/mfa", nil).State,
		"a page with a code box on it is the code, radios or not")
}

// The one choice arrives ticked. It is not clicked again — a custom radio that
// toggles would untick — and the page's own Next is what sends it.
func TestARadioThePageAlreadyTickedIsSentWithoutBeingClicked(t *testing.T) {
	page := &browser.StubPage{Location: "https://login.northwesternmutual.com/mfaverify"}
	offersControls(page,
		SubmitControl{Kind: agent.PressedButton, Words: "Back to login"},
		SubmitControl{Kind: agent.PressedButton, Words: "Next"})
	offersChoices(page, FactorChoice{
		Kind: "radio", Words: "Enter code from an Authentication App", Selects: true, Checked: true,
	})

	factor, err := NewNorthwesternMutual().ChooseFactor(page, "totp")

	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind)
	require.Equal(t, "Enter code from an Authentication App", factor.Chose)
	require.True(t, factor.Confirmed)
	require.Equal(t, "Next", factor.Step.Words)
	require.Equal(t, []string{submitMark}, page.Clicked, "Next, and nothing before it")
}

func TestARadioNotYetTickedIsClickedAndThenSent(t *testing.T) {
	page := &browser.StubPage{Location: "https://login.example.test/mfaverify"}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Next"})
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "Text me a code", Selects: true, Checked: true},
		FactorChoice{Kind: "radio", Words: "Enter code from an Authentication App", Selects: true})

	factor, err := testDraft().ChooseFactor(page, "")

	require.NoError(t, err)
	require.Equal(t, "totp", factor.Kind, "the ticked text is the page's default, not the choice")
	require.Equal(t, []string{factorMark, submitMark}, page.Clicked)
}

func TestAnOrdinarySignInPageMayMentionTwoStepVerification(t *testing.T) {
	where := StateOf(FormFrom(Reading{
		Fields: []Field{{Type: "email", Name: "email"}},
		Text:   "Sign in\nEmail\nNext\nTip: set up two-step verification in your profile.",
	}), "https://login.example.test/", nil)
	require.Equal(t, StateEmail, where.State, "a form with a box on it and no dialog is a form")
}

// A sign-in address answered with a full-page interstitial check, waited on
// in case it clears.
func TestAnInterstitialIsABlockingCheck(t *testing.T) {
	for _, form := range []Form{
		{Title: "Just a moment...", Text: "app.example.test\nPerforming security verification\nRay ID: 8c0f"},
		{Text: "Verifying you are human. This may take a few seconds."},
		{Title: "Just a moment..."},
		{Text: "app.example.test needs to review the security of your connection before proceeding."},
	} {
		where := StateOf(form, "https://app.example.test/login", nil)
		require.Equal(t, StateCaptcha, where.State, "%+v", form)
		require.True(t, where.Blocking)
		require.Contains(t, where.Prompt, "did not clear within the wait")
	}
	require.Equal(t, StatePassword, StateOf(Form{
		Password: true, Title: "Log in", Text: "Protected by a security service. Log in",
	}, "https://app.example.test/login", nil).State)
}

func TestTheInterstitialIsWaitedOnAndThePageBehindItRead(t *testing.T) {
	page := &browser.StubPage{Location: "https://app.example.test/login"}
	cleared := false
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		if cleared {
			return asAny(t, Reading{Fields: []Field{{Type: "email"}, {Type: "password"}}, Title: "Log in"}), nil
		}
		return asAny(t, Reading{Title: "Just a moment...", Text: "Performing security verification"}), nil
	}
	var waited []time.Duration
	page.OnWaitFor = func(script string, timeout time.Duration) error {
		if script == interstitialGoneScript {
			waited = append(waited, timeout)
			cleared = true
		}
		return nil
	}
	t.Cleanup(func() { Forget(page) })

	where, err := testDraft().Classify(page)

	require.NoError(t, err)
	require.Equal(t, []time.Duration{interstitialWait}, waited)
	require.Equal(t, StatePassword, where.State, "nothing stood in front, the form behind it is the page")
}

// The fills are aimed inside the dialog in front of the page, so a password
// box behind it is never typed into.
func TestAStepActsInsideTheDialogInFrontOfThePage(t *testing.T) {
	page := combinedFormPage(true)
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == markScopeScript {
			return true, nil
		}
		return inner(script, arg)
	}

	_, err := testDraft().FillPassword(page, "invented", "someone@example.test")
	require.NoError(t, err)

	require.Len(t, page.Filled, 2)
	for _, fill := range page.Filled {
		for _, part := range splitUnion(fill.Selector) {
			require.True(t, strings.HasPrefix(part, scopeMark+" "), part)
		}
	}
	require.Equal(t, scopeMark+` input[type="password"]`, page.Filled[1].Selector)
}

func TestASelectorUnionIsCutOnlyAtItsOwnCommas(t *testing.T) {
	require.Equal(t, []string{`input[name="a,b"]`, `a:is(b, c)`, `d`}, splitUnion(`input[name="a,b"], a:is(b, c), d`))
	require.Equal(t, `x`, pageScope{}.sel(`x`))
	require.Equal(t, scopeMark+` a, `+scopeMark+` b`, pageScope{dialog: true}.sel(`a, b`))
}

// A code a box per character: typed into the first box, which is what a
// person does and what a box that moves the cursor on wants; and one box at a
// time when typing past the first went nowhere.
func segmentedPage(t *testing.T, filledAfterTyping, filledAfterFills int) *browser.StubPage {
	t.Helper()
	page := &browser.StubPage{Location: "https://www.example.test/sign-in"}
	asked := 0
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case markSegmentsScript:
			return 6, nil
		case segmentsFilledScript:
			asked++
			if asked == 1 {
				return filledAfterTyping, nil
			}
			return filledAfterFills, nil
		}
		return nil, nil
	}
	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Verify another way"},
		SubmitControl{Kind: agent.PressedButton, Words: "Confirm"})
	return page
}

func TestASegmentedCodeIsTypedIntoTheFirstBox(t *testing.T) {
	page := segmentedPage(t, 6, 6)

	step, err := testDraft().Answer(page, State{State: StateOTP}, "482913", "")

	require.NoError(t, err)
	require.Equal(t, []string{digitMark(0), submitMark}, page.Clicked)
	require.Equal(t, []string{"482913"}, page.Typed)
	require.Empty(t, page.Filled, "every box filled from the first; none filled again")
	require.Equal(t, "Confirm", step.Words)
}

func TestASegmentedCodeTheFirstBoxKeptIsFilledABoxAtATime(t *testing.T) {
	page := segmentedPage(t, 1, 6)

	_, err := testDraft().Answer(page, State{State: StateOTP}, "482913", "")

	require.NoError(t, err)
	require.Len(t, page.Filled, 6)
	for at, fill := range page.Filled {
		require.Equal(t, browser.StubFill{Selector: digitMark(at), Value: string("482913"[at])}, fill)
	}
}

func TestASegmentedCodeTheBoxesWillNotTakeFailsInWords(t *testing.T) {
	page := segmentedPage(t, 1, 1)

	_, err := Draft{BillerID: "example-mobile"}.Answer(page, State{State: StateOTP}, "482913", "")

	require.EqualError(t, err, "example-mobile's code boxes would not take the code")
}

// "Remember this device" by the words beside the box.
func TestTheBoxThatKeepsThisDeviceIsTickedByItsWords(t *testing.T) {
	for _, words := range []string{
		"Don’t require verification for future logins on this trusted device.",
		"Don't ask again on this computer",
		"Remember this device",
		"Trust this browser",
		"Keep me signed in",
		"Skip verification next time on this device",
	} {
		_, found := BestRemember([]RememberBox{{Words: words}})
		require.True(t, found, words)
	}
	for _, words := range []string{
		"Remember my email", "I agree to the terms", "Send me offers", "Use token", "Show password",
	} {
		_, found := BestRemember([]RememberBox{{Words: words}})
		require.False(t, found, words)
	}
	_, found := BestRemember([]RememberBox{{Words: "Remember this device", Checked: true}})
	require.False(t, found, "a ticked box is left alone: ticking it again unticks it")

	page := segmentedPage(t, 6, 6)
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case rememberBoxesScript:
			return asAny(t, []RememberBox{
				{At: 0, Words: "Remember my email"},
				{At: 1, Words: "Don’t require verification for future logins on this trusted device."},
			}), nil
		case markRememberScript:
			require.Equal(t, map[string]any{"at": float64(1)}, arg)
			return "check", nil
		}
		return inner(script, arg)
	}
	page.OnCheck = func(string) (bool, error) { return true, nil }

	_, err := testDraft().Answer(page, State{State: StateOTP}, "482913", "")

	require.NoError(t, err)
	require.Equal(t, []string{rememberSelector, rememberMark}, page.Checked)
}

// What a failed sign-in stopped on, as the trail carries it.
func TestASnapshotKeepsThePagesShapeAndNoLongNumbers(t *testing.T) {
	said := SnapshotText(PageSnapshot{
		Dialog:   "We sent your code",
		Headings: []string{"Confirm Your Identity"},
		Text:     "Enter the code we sent to +•••••••0000 for account 00123456789.",
		Tree: []string{
			`ds-list-item#appPush`,
			`  #shadow-root`,
			`    button type=button "Approve in the mobile app"`,
			`iframe`,
			`  #frame`,
			`    input type=password name=pw`,
		},
	})
	require.Contains(t, said, "dialog in front: We sent your code")
	require.Contains(t, said, "headings: Confirm Your Identity")
	require.Contains(t, said, `    button type=button "Approve in the mobile app"`)
	require.NotContains(t, said, "0000", "a phone number's tail comes off")
	require.NotContains(t, said, "00123456789")
	require.Contains(t, said, "+•••••••… for account …")

	long := SnapshotText(PageSnapshot{Tree: []string{strings.Repeat("button ", 40), strings.Repeat("a ", 3000)}})
	require.LessOrEqual(t, len([]rune(long)), snapshotLimit+1)
	require.Empty(t, SnapshotText(PageSnapshot{}))
}

// A billing page's dates keep their years; every other long number still
// comes off, and a read page's words get more room than a sign-in page's.
func TestASnapshotKeepsTheYearADatePrints(t *testing.T) {
	said := SnapshotText(PageSnapshot{
		Text: "Guarantor #00001234 Statement 03/03/2026 $150.00 Mar 15, 2025 Jan 6 2024 Feb122026 call 2026 today",
		Tree: []string{`a href=/MyChart/Billing/Details/00001234 "View statement 3/3/2026"`},
	})
	require.Contains(t, said, "Statement 03/03/2026 $150.00 Mar 15, 2025 Jan 6 2024 Feb122026 call … today")
	require.Contains(t, said, `a href=/MyChart/Billing/Details/… "View statement 3/3/2026"`)
	require.NotContains(t, said, "00001234")

	words := strings.Repeat("word ", 400)
	require.Less(t, len(SnapshotText(PageSnapshot{Text: words})), 700)
	require.Greater(t, len(SnapshotText(PageSnapshot{Text: words, Paths: true})), 1400)
}

// The login's own choice of second factor is the only one taken, and the
// ranking decides only for a login that made none. An e-mail is never taken
// for a login that did not ask for one: the mailbox that answers it is not
// every household's.
func TestTheLoginsOwnSecondFactorIsTheOnlyOneTaken(t *testing.T) {
	menu := []FactorChoice{
		{At: 0, Kind: "radio", Words: "Text message", Selects: true},
		{At: 1, Kind: "radio", Words: "Email", Selects: true},
		{At: 2, Kind: "radio", Words: "Authenticator app", Selects: true},
	}
	for _, tc := range []struct{ prefer, want string }{
		{"", "Authenticator app"},
		{"totp", "Authenticator app"},
		{"email", "Email"},
		{"sms", "Text message"},
	} {
		best, found := BestFactor(menu, tc.prefer)
		require.True(t, found, tc.prefer)
		require.Equal(t, tc.want, best.Words, tc.prefer)
	}

	_, found := BestFactor([]FactorChoice{{Words: "Text message"}, {Words: "Passkey"}}, "email")
	require.False(t, found, "the choice not offered, nothing else is taken in its place")
	_, found = BestFactor([]FactorChoice{{Words: "Email"}, {Words: "Text message"}}, "totp")
	require.False(t, found, "an authenticator chosen is not swapped for a text")

	_, found = BestFactor([]FactorChoice{{Words: "Email"}, {Words: "Passkey"}}, "")
	require.False(t, found, "an e-mail is taken only for a login that asked for it")
}
