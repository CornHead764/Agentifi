package billers

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// MyChart's "which way should we send your code" page, in the shapes its
// public help describes: a question that names only where the code goes, the
// ways as radios or as buttons, an address and a phone number printed masked,
// and a "Send code" to press. Every address and digit here is invented.

const myChartFactorURL = "https://mychart.examplehealth.example/MyChart/Authentication/SecondaryValidation"

func TestMyChartsFactorPageIsTheChoiceOfFactorInEitherShape(t *testing.T) {
	radios := Form{
		Radios: 2, Submits: 2,
		Heading: "We need to verify your identity",
		Text: "We need to verify your identity\nFor your security, choose where we should send a " +
			"verification code.\nEmail: s•••@example.test\nText message: (***) ***-0100\nSend code\nCancel",
	}
	require.Equal(t, StateFactor, StateOf(radios, myChartFactorURL, testArea).State)

	buttons := Form{
		Submits: 3,
		Heading: "Verify Your Identity",
		Text: "Verify Your Identity\nWe'll send a code to confirm it's you.\n" +
			"Send to my email s***@example.test\nSend to my phone ***-***-0100\nCancel",
	}
	require.Equal(t, StateFactor, StateOf(buttons, myChartFactorURL, testArea).State,
		"the question names no way, but the choices under it do")

	phoneOnly := Form{
		Submits: 2,
		Heading: "Verify Your Identity",
		Text:    "Verify Your Identity\nWhere should we send your code?\n(***) ***-0100\nCancel",
	}
	require.Equal(t, StateFactor, StateOf(phoneOnly, myChartFactorURL, testArea).State,
		"a masked number alone names the text")

	// The shape a portal really drew: no question naming a way, the ways as
	// plain type=button controls with no submit on the page at all.
	plain := Form{
		Heading: "Verify your identity",
		Text: "Verify your identity\nFor your protection, we require you to enter a unique code to " +
			"verify your identity.\nStep 1: Choose how you would like to receive your code.\nLearn more\n" +
			"Get from authenticator app\nSend to my email",
		Buttons: []string{"Learn more", "Get from authenticator app", "Send to my email"},
	}
	require.Equal(t, StateFactor, StateOf(plain, myChartFactorURL, testArea).State,
		"buttons that name the ways are the choice, submit or not")

	// An approval prompt mentions the app and draws no button naming a way.
	approval := Form{
		Heading: "Verify your identity",
		Text:    "Verify your identity\nApprove the sign-in in your authenticator app to get your code.\nResend",
		Buttons: []string{"Resend"},
	}
	require.NotEqual(t, StateFactor, StateOf(approval, myChartFactorURL, testArea).State)

	// The page after it asks for the code, and is that.
	code := Form{
		OTP: true, Boxes: 1, Submits: 1,
		Text: "Verify Your Identity\nEnter the code we sent to (***) ***-0100\nTrust this device\nVerify\nResend code",
	}
	require.Equal(t, StateOTP, StateOf(code, myChartFactorURL, testArea).State)

	// A page of buttons that only mentions a code is not the choice.
	help := Form{Submits: 2, Text: "Didn't get a code?\nContact the help desk\nBack"}
	require.NotEqual(t, StateFactor, StateOf(help, myChartFactorURL, testArea).State)
}

func TestMyChartsChoicesAreClassifiedByWhatTheyName(t *testing.T) {
	for _, tc := range []struct{ words, kind string }{
		{"Email", "email"},
		{"Email: s•••@example.test", "email"},
		{"Send to s***@example.test", "email"},
		{"Send to my email s***@example.test", "email"},
		{"Text message", "sms"},
		{"Text message: (***) ***-…", "sms"},
		{"Text", "sms"},
		{"Text to phone", "sms"},
		{"Send to my phone", "sms"},
		{"Send to my phone ***-***-…", "sms"},
		{"***-***-…", "sms"},
		{"(***) ***-…", "sms"},
		{"XXX-XXX-…", "sms"},
		{"Mobile phone ending in …", "sms"},
		{"Authenticator app", "totp"},
		{"Use the app on your phone", ""},
		{"Send a notification to your phone", ""},
		{"Call (***) ***-…", ""},
		{"Send code", ""},
		{"Cancel", ""},
	} {
		require.Equalf(t, tc.kind, FactorKind(tc.words), "the kind of %q", tc.words)
	}
}

// The digits come off before the words are classified, so the reading a
// person's phone number reaches the trail through is the same one that is
// ranked.
func TestAMaskedNumberStillReadsAsATextOnceItsDigitsAreOff(t *testing.T) {
	page := combinedFormPage(true)
	offersChoices(page,
		FactorChoice{Kind: "radio", Words: "***-***-0100", Selects: true},
		FactorChoice{Kind: "radio", Words: "s***@example.test", Selects: true})

	choices, err := FactorChoices(page)

	require.NoError(t, err)
	require.Equal(t, "***-***-…", choices[0].Words)
	require.Equal(t, "sms", FactorKind(choices[0].Words))
	require.Equal(t, "email", FactorKind(choices[1].Words))
}

func myChartRadios() []FactorChoice {
	return []FactorChoice{
		{Kind: "radio", Words: "Email: s•••@example.test", Selects: true},
		{Kind: "radio", Words: "Text message: (***) ***-0100", Selects: true},
	}
}

// Each preference over MyChart's radios, then its "Send code": the chosen way
// is selected and sent, an unchosen one never is, and a way the page does not
// offer presses nothing at all.
func TestMyChartsRadiosAreChosenByTheLoginsOwnChoice(t *testing.T) {
	for _, tc := range []struct {
		prefer, kind, chose string
	}{
		{prefer: "", kind: "sms", chose: "Text message: (***) ***-…"},
		{prefer: "email", kind: "email", chose: "Email: s•••@example.test"},
		{prefer: "totp"},
	} {
		page := combinedFormPage(true)
		offersControls(page,
			SubmitControl{Kind: agent.PressedButton, Words: "Send code"},
			SubmitControl{Kind: agent.PressedButton, Words: "Cancel"})
		offersChoices(page, myChartRadios()...)

		factor, err := testDraft().ChooseFactor(page, tc.prefer)

		require.NoError(t, err, tc.prefer)
		require.Len(t, factor.Choices, 2, "the whole menu is answered for %q", tc.prefer)
		require.Equal(t, tc.kind, factor.Kind, tc.prefer)
		require.Equal(t, tc.chose, factor.Chose, tc.prefer)
		if tc.kind == "" {
			require.Empty(t, page.Clicked, "nothing is pressed for a way the page does not offer")
			continue
		}
		require.True(t, factor.Confirmed, tc.prefer)
		require.Equal(t, "Send code", factor.Step.Words, "the page's own button sends the choice")
		require.Equal(t, []string{factorMark, submitMark}, page.Clicked, tc.prefer)
	}
}

func TestMyChartsButtonsAreChosenByTheLoginsOwnChoice(t *testing.T) {
	for _, tc := range []struct{ prefer, chose string }{
		{prefer: "", chose: "Send to my phone ***-***-…"},
		{prefer: "email", chose: "Send to my email s***@example.test"},
		{prefer: "totp"},
	} {
		page := combinedFormPage(true)
		offersChoices(page,
			FactorChoice{Kind: agent.PressedButton, Words: "Send to my email s***@example.test"},
			FactorChoice{Kind: agent.PressedButton, Words: "Send to my phone ***-***-0100"},
			FactorChoice{Kind: agent.PressedButton, Words: "Cancel"})

		factor, err := testDraft().ChooseFactor(page, tc.prefer)

		require.NoError(t, err, tc.prefer)
		require.Equal(t, tc.chose, factor.Chose, tc.prefer)
		if tc.chose == "" {
			require.Empty(t, page.Clicked)
			continue
		}
		require.False(t, factor.Confirmed, "a button sends itself")
		require.Equal(t, []string{factorMark}, page.Clicked, tc.prefer)
	}
}

func TestSendCodeIsTheButtonThatSendsAChoice(t *testing.T) {
	for _, words := range []string{"Send code", "Send", "Send me a code", "Send verification code", "Send the code"} {
		require.Equalf(t, 1, SubmitRank(SubmitControl{Words: words}), "%q sends the step", words)
	}
	for _, words := range []string{"Send to my phone", "Send feedback"} {
		require.Equalf(t, 0, SubmitRank(SubmitControl{Words: words}), "%q is not the step's own button", words)
	}
}
