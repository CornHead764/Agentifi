package merchants

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// The Costco classifier over readings of Costco's Azure B2C sign-in, and what
// it presses. Every page, address and message here is invented, in B2C's
// shape.

const (
	b2cAuthorize = "https://signin.costco.com/tenant-0000/B2C_1A_Invented_Policy/oauth2/v2.0/authorize"
	b2cQuery     = "?client_id=invented&state=invented-state&nonce=invented-nonce#/orders-and-purchases"
)

// b2c is what the Costco reading adds about a B2C step, as a page answers it.
type b2c struct {
	step       string
	busy       bool
	empty      bool
	pageErrors []string
	itemErrors []string
	sent       []string
	heading    string
	buttons    []map[string]any
	radios     []map[string]any
}

func (b b2c) answer() map[string]any {
	return map[string]any{
		"step": b.step, "framed": b.step != "" || b.empty, "drawn": b.step != "" && !b.empty,
		"busy": b.busy, "pageErrors": strs(b.pageErrors), "itemErrors": strs(b.itemErrors), "sent": strs(b.sent),
		"heading": b.heading, "buttons": maps(b.buttons), "radios": maps(b.radios),
	}
}

func strs(values []string) []any {
	out := []any{}
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func maps(values []map[string]any) []any {
	out := []any{}
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

func button(id, words string) map[string]any {
	selector := ""
	if id != "" {
		selector = "#" + id
	}
	return map[string]any{"selector": selector, "id": id, "words": words, "disabled": false}
}

func radio(id, words string) map[string]any {
	return map[string]any{
		"words": words, "target": `label[for="` + id + `"]`, "radio": "#" + id, "checked": false,
	}
}

// costcoLook is one reading of a Costco page.
type costcoLook struct {
	visible []string
	texts   map[string]string
	body    string
	b2c     b2c
}

// costcoShowing is a page whose readings are these looks in turn, the last
// repeating. Any other script (the change a press waits for) answers "".
func costcoShowing(address string, looks ...costcoLook) (*browser.StubPage, *int) {
	page := &browser.StubPage{Location: address}
	read := 0
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != costcoReadScript {
			return "", nil
		}
		look := looks[min(read, len(looks)-1)]
		read++
		shown := map[string]any{}
		for _, group := range look.visible {
			shown[group] = true
		}
		texts := map[string]any{}
		for group, text := range look.texts {
			texts[group] = text
		}
		body := look.body
		if body == "" && !look.b2c.empty {
			body = "Something on the page"
		}
		return map[string]any{
			"url": page.Location, "title": "", "text": body, "visible": shown, "texts": texts,
			"b2c": look.b2c.answer(),
		}, nil
	}
	return page, &read
}

func TestTheCostcoClassifierReadsEachB2CStep(t *testing.T) {
	for _, one := range []struct {
		name   string
		look   costcoLook
		want   string
		prompt string
		method string
	}{
		{
			name: "the combined sign-in, which the password decides",
			look: costcoLook{visible: []string{"email", "password"}, b2c: b2c{step: "CombinedSigninAndSignup"}},
			want: StatePassword,
		},
		{
			name: "an email-only first step",
			look: costcoLook{visible: []string{"email"}, b2c: b2c{step: "Unified"}},
			want: StateEmail,
		},
		{
			name: "a wrong password, in B2C's page-level words",
			look: costcoLook{visible: []string{"email", "password"}, b2c: b2c{
				step: "CombinedSigninAndSignup", pageErrors: []string{"Your password is incorrect."},
			}},
			want: StateFailed, prompt: "Your password is incorrect.",
		},
		{
			name: "throttling, which carries no bad-login word",
			look: costcoLook{visible: []string{"email", "password"}, b2c: b2c{
				step:       "CombinedSigninAndSignup",
				pageErrors: []string{"You've made too many attempts. Please wait and try again later."},
			}},
			want: StateFailed, prompt: "You've made too many attempts. Please wait and try again later.",
		},
		{
			name: "a field's own error",
			look: costcoLook{visible: []string{"email", "password"}, b2c: b2c{
				step: "CombinedSigninAndSignup", itemErrors: []string{"Please enter a valid email address."},
			}},
			want: StateFailed, prompt: "Please enter a valid email address.",
		},
		{
			name: "the code box, in the step's own words",
			look: costcoLook{
				visible: []string{"code", "prompt"},
				texts:   map[string]string{"prompt": "Verification code has been sent to your email."},
				b2c:     b2c{step: "SelfAsserted", buttons: []map[string]any{button("verifyCode", "Verify code")}},
			},
			want: StateOTP, prompt: "Verification code has been sent to your email.", method: "email",
		},
		{
			name: "the sent-to message, below a heading the prompt group reaches first",
			look: costcoLook{
				visible: []string{"code", "prompt"},
				texts:   map[string]string{"prompt": "Verify Your Email"},
				b2c: b2c{
					step: "SelfAsserted", sent: []string{"Verification code has been sent to your email address."},
					buttons: []map[string]any{button("emailVerificationControl_but_verify_code", "Verify code")},
				},
			},
			want: StateOTP, prompt: "Verification code has been sent to your email address.", method: "email",
		},
		{
			name: "a wrong code, which the person can type again",
			look: costcoLook{
				visible: []string{"code", "prompt"},
				texts:   map[string]string{"prompt": "Enter the code we emailed you"},
				b2c:     b2c{step: "SelfAsserted", itemErrors: []string{"That code is incorrect. Please try again."}},
			},
			want: StateOTP, prompt: "That code is incorrect. Please try again.", method: "email",
		},
		{
			name: "the email verification step, with the address shown beside its send button",
			look: costcoLook{visible: []string{"email"}, b2c: b2c{
				step:    "SelfAsserted",
				buttons: []map[string]any{button("emailVerificationControl_but_send_code", "Send verification code")},
			}},
			want: StateFactor,
		},
		{
			name: "the phone factor's send button",
			look: costcoLook{b2c: b2c{
				step:    "Phonefactor",
				buttons: []map[string]any{button("sendCode", "Send Code"), button("callMe", "Call Me")},
			}},
			want: StateFactor,
		},
		{
			name: "a choice of ways to verify",
			look: costcoLook{b2c: b2c{
				step:    "SelfAsserted",
				radios:  []map[string]any{radio("phone", "Phone"), radio("email", "Email")},
				buttons: []map[string]any{button("continue", "Continue")},
			}},
			want: StateFactor,
		},
		{
			name: "signed in, on the purchases page",
			look: costcoLook{visible: []string{"card"}},
			want: StateSignedIn,
		},
	} {
		t.Run(one.name, func(t *testing.T) {
			address := b2cAuthorize + b2cQuery
			if one.want == StateSignedIn {
				address = costcoOrdersURL
			}
			page, _ := costcoShowing(address, one.look)
			where, err := Costco().Classify(page)
			require.NoError(t, err)
			require.Equal(t, one.want, where.State, where.Error)
			if one.prompt != "" {
				require.Equal(t, one.prompt, where.Prompt)
				if one.want == StateFailed {
					require.Equal(t, one.prompt, where.Error)
				}
			}
			require.Equal(t, one.method, where.Method)
			require.Zero(t, page.Slept, "a page it recognises is not waited on")
		})
	}
}

func TestAFactorStepOffersItsChoicesInItsOwnWordsWithTheAddressMasked(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{b2c: b2c{
		step: "SelfAsserted",
		radios: []map[string]any{
			radio("phone", "Text me at 555-0100"), radio("email", "Email someone@example.test"),
		},
	}})
	where, err := Costco().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFactor, where.State)
	require.Len(t, where.Choices, 2)
	require.Equal(t, "sms", where.Choices[0].Kind)
	require.Equal(t, "email", where.Choices[1].Kind)
	require.Equal(t, "Email …", where.Choices[1].Words)
	require.NotContains(t, where.Choices[0].Words, "0100")
}

func TestAB2CStepStillDrawingIsReadAgainRatherThanCalledUnrecognised(t *testing.T) {
	for _, one := range []struct {
		name  string
		first costcoLook
	}{
		{"the frame is there and empty", costcoLook{b2c: b2c{empty: true}}},
		{"a press is being posted", costcoLook{b2c: b2c{step: "Unified", busy: true}}},
		{"the page has no words yet", costcoLook{body: " "}},
	} {
		t.Run(one.name, func(t *testing.T) {
			drawn := costcoLook{visible: []string{"email", "password"}, b2c: b2c{step: "Unified"}}
			page, read := costcoShowing(b2cAuthorize+b2cQuery, one.first, one.first, drawn)
			where, err := Costco().Classify(page)
			require.NoError(t, err)
			require.Equal(t, StatePassword, where.State)
			require.Equal(t, 3, *read)
			require.Equal(t, 2*costcoRecheck, page.Slept)
		})
	}
}

func TestAStepThatNeverDrawsIsGivenUpOnInBoundedTime(t *testing.T) {
	page, read := costcoShowing(b2cAuthorize+b2cQuery, costcoLook{b2c: b2c{empty: true}})
	where, err := Costco().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFailed, where.State)
	require.Equal(t, costcoLooks+1, *read)
	require.Equal(t, costcoLooks*costcoRecheck, page.Slept)
}

func TestAnUnrecognisedPageSaysWhatWasOnItAndNotWhereItsQueryWent(t *testing.T) {
	page, read := costcoShowing(b2cAuthorize+b2cQuery, costcoLook{b2c: b2c{
		step:    "SelfAsserted-Invented",
		heading: "Keep someone@example.test signed in?",
		buttons: []map[string]any{button("yes", "Yes"), button("", "No, thanks")},
	}})
	where, err := Costco().Classify(page)
	require.NoError(t, err)
	require.Equal(t, StateFailed, where.State)
	require.Equal(t,
		`unrecognised page at signin.costco.com/tenant-0000/B2C_1A_Invented_Policy/oauth2/v2.0/authorize: `+
			`B2C step "SelfAsserted-Invented"; heading "Keep … signed in?"; buttons "Yes", "No, thanks"`,
		where.Error)
	require.NotContains(t, where.Error, "invented-state")
	require.Equal(t, 2, *read, "one more look after a settle, and no more for a page that is not busy")
}

func TestAnUnrecognisedPageOffB2CStillSaysWhereItWas(t *testing.T) {
	page, _ := costcoShowing("https://www.costco.com/somewhere?token=invented", costcoLook{})
	where, err := Costco().Classify(page)
	require.NoError(t, err)
	require.Equal(t, "unrecognised page at www.costco.com/somewhere", where.Error)
}

func TestTheFactorStepTakesTheEmailedCodeAndSendsIt(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{b2c: b2c{
		step: "SelfAsserted",
		radios: []map[string]any{
			radio("call", "Call me"), radio("phone", "Text me"), radio("email", "Email me"),
		},
	}})
	var chose []string
	page.OnChoose = func(target, radio string) (string, error) {
		chose = append(chose, target, radio)
		return browser.TookClick, nil
	}
	factor, err := Costco().ChooseFactor(page, "")
	require.NoError(t, err)
	require.Equal(t, "email", factor.Kind)
	require.True(t, factor.Confirmed)
	require.Equal(t, []string{`label[for="email"]`, "#email"}, chose)
	require.Equal(t, []string{`label[for="email"]`, costcoContinue}, page.Clicked)
	require.Empty(t, page.Filled, "choosing a way types nothing")
}

func TestAFactorStepOfferingOnlyACallIsNotTaken(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{b2c: b2c{
		step: "Phonefactor", radios: []map[string]any{radio("call", "Call me")},
	}})
	factor, err := Costco().ChooseFactor(page, "")
	require.NoError(t, err)
	require.Empty(t, factor.Kind)
	require.Empty(t, page.Clicked)
}

func TestTheSendButtonIsPressedByItsID(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{visible: []string{"email"}, b2c: b2c{
		step: "SelfAsserted",
		buttons: []map[string]any{
			button("emailVerificationControl_but_send_code", "Send verification code"),
			button("cancel", "Cancel"),
		},
	}})
	factor, err := Costco().ChooseFactor(page, "")
	require.NoError(t, err)
	require.Equal(t, costcoSentCode, factor.Kind, "the button names no channel")
	require.Equal(t, []string{"#emailVerificationControl_but_send_code"}, page.Clicked)
}

func TestACodeIsConfirmedWithTheStepsVerifyButton(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{visible: []string{"code"}})
	step, err := Costco().Answer(page, State{State: StateOTP}, "123456", "a-password")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, []browser.StubFill{{Selector: costcoCodeField, Value: "123456"}}, page.TypedInto)
	require.Empty(t, page.Filled)
	require.Equal(t, []string{costcoVerifyButton}, page.Clicked)
}

// A fixed draw: every pause is its lower bound plus the same share of its
// spread.
func fixedPace() browser.Pace {
	pace := browser.TypingPace
	pace.Draw = func(n int64) int64 { return n / 2 }
	return pace
}

func TestThePasswordIsTypedNotFilledAndPressedAfterAPause(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{visible: []string{"email", "password"}})
	module := &costcoModule{refused: &sync.Map{}, pace: fixedPace()}
	_, err := module.FillPassword(page, "an-invented-password", "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, []browser.StubFill{
		{Selector: costcoEmailField, Value: "someone@example.test"},
		{Selector: costcoPasswordField, Value: "an-invented-password"},
	}, page.TypedInto)
	require.Empty(t, page.Filled)
	require.Equal(t, []string{costcoSubmitButton}, page.Clicked)
	require.GreaterOrEqual(t, page.Slept, fixedPace().Pause())
}

func TestTheEmailIsTypedNotFilled(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{visible: []string{"email"}})
	module := &costcoModule{refused: &sync.Map{}, pace: fixedPace()}
	_, err := module.FillEmail(page, "someone@example.test")
	require.NoError(t, err)
	require.Equal(t, []browser.StubFill{{Selector: costcoEmailField, Value: "someone@example.test"}}, page.TypedInto)
	require.Empty(t, page.Filled)
	require.Equal(t, []string{costcoSubmitButton}, page.Clicked)
	require.GreaterOrEqual(t, page.Slept, fixedPace().Pause())
}

func TestACodeWithNoVerifyButtonIsSentWithTheFormsOwn(t *testing.T) {
	page, _ := costcoShowing(b2cAuthorize, costcoLook{visible: []string{"code"}})
	page.Missing = func(selector string) bool { return selector == costcoVerifyButton }
	step, err := Costco().Answer(page, State{State: StateOTP}, "123456", "")
	require.NoError(t, err)
	require.True(t, step.Acted)
	require.Equal(t, []string{costcoSubmitButton}, page.Clicked)
}
