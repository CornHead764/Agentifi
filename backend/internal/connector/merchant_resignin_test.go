package connector

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/merchants"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/totp"
)

// A pull whose session has lapsed, with a kept password, against a merchant
// that is not a merchant. Every login, key and session here is invented.

const lapsedJar = `{"cookies":[{"name":"session","value":"lapsed"}],"origins":[]}`

// An invented setup key, in the base32 an authenticator app is given.
const keptKey = "JBSWY3DPEHPK3PXP"

func keptMerchantLogin() *provider.MerchantCredential {
	return &provider.MerchantCredential{Email: "someone@example.test", Password: "a-kept-password"}
}

// lapsedThenPulls is a pull that meets a sign-in screen on the lapsed session
// and reads orders on any other.
func lapsedThenPulls(sessions *[]string) func(call merchants.Call) (merchants.Result, error) {
	return func(call merchants.Call) (merchants.Result, error) {
		*sessions = append(*sessions, string(call.Session))
		if string(call.Session) == lapsedJar {
			return merchants.Result{NeedsSignIn: true, Reason: "Amazon asked to sign in again"}, nil
		}
		return merchants.Result{Parsed: &merchantimport.Parsed{}, Orders: 2}, nil
	}
}

func TestALapsedSessionSignsInWithTheKeptPasswordOnceAndCarriesOn(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StateEmail}, {State: merchants.StatePassword}, {State: merchants.StateSignedIn}},
		fetch:  lapsedThenPulls(&sessions),
	}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Empty(t, result.Paused)
	require.Equal(t, 2, result.Orders)
	require.Equal(t, 1, module.fills, "the kept password is typed once")
	require.Equal(t, "someone@example.test", module.email)
	require.Equal(t, "a-kept-password", module.password)
	require.Equal(t, []string{lapsedJar, opened.jar}, sessions,
		"the pull ran again on the session the sign-in opened")
	require.JSONEq(t, opened.jar, string(result.StorageState))
	require.Len(t, opened.seeds, 3, "the pull, the sign-in, and the pull again")
	require.JSONEq(t, lapsedJar, string(opened.seeds[1]),
		"the sign-in browser starts from the lapsed cookies, so the device is one Amazon knows")
	require.Contains(t, result.Notes, "Amazon signed in again with the kept password")
	require.Equal(t, 3, opened.closed, "every browser the pull opened is closed")
}

func TestAPasswordTheMerchantRefusesIsKeptButPaused(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StatePassword}, {
			State: merchants.StateFailed, Prompt: "Your password is incorrect", Error: "Your password is incorrect",
		}},
		fetch: lapsedThenPulls(&sessions),
	}
	opened := stubBrowser()
	opened.page.OnGoto = func(url string) error {
		if url == module.SignInURL() {
			opened.page.Location = "https://merchant.test/ap/signin?openid.return_to=a-token"
		}
		return nil
	}
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, provider.SignInPausedPasswordRefused, result.Paused)
	require.Equal(t, "Amazon did not accept the kept password "+
		"(Your password is incorrect, at merchant.test/ap/signin)", result.Reason)
	require.NotContains(t, result.Reason, "a-token", "the page is named without its query")
	require.NotEmpty(t, result.Image)
	require.Equal(t, 1, module.fills)
	require.Len(t, sessions, 1, "a refused password pulls nothing more")
	require.Empty(t, result.StorageState)
}

// A form that asks for the password a second time has turned it down; typing
// it again is how an account gets locked.
// A page nothing recognises after the password says nothing about the
// password, so the kept one is not paused and the next sync tries it again.
func TestAPageNothingRecognisesAfterTheKeptPasswordDoesNotPauseIt(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateInteractive}},
		fetch:  lapsedThenPulls(&sessions),
	}
	opened := stubBrowser()
	opened.page.OnGoto = func(url string) error {
		if url == module.SignInURL() {
			opened.page.Location = "https://merchant.test/gateway?session=a-token"
		}
		return nil
	}
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Empty(t, result.Paused)
	require.Contains(t, result.Reason, "Amazon did not finish signing in with the kept password")
	require.Contains(t, result.Reason, "merchant.test/gateway")
	require.NotContains(t, result.Reason, "a-token")
	require.Contains(t, result.Notes, "Amazon's sign-in ended at a page it did not recognise, at merchant.test/gateway")
	require.GreaterOrEqual(t, opened.page.Slept, arrivalWait)
	require.Equal(t, 1, module.fills)
}

func TestAPasswordAskedForAgainIsNotTypedAgain(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StatePassword}},
		fetch:  lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedPasswordRefused, result.Paused)
	require.Contains(t, result.Reason, "asked for the password again")
	require.Equal(t, 1, module.fills)
}

func TestACodeWithNoKeyToAnswerItPausesForAPersonAndIsNotARefusal(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the code we texted to the phone ending 00"}},
		fetch: lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
	require.Equal(t, "Amazon asked for a code; sign in to answer it", result.Reason)
	require.Empty(t, module.answered, "nothing was typed into the code box")
	require.Equal(t, 1, module.fills)
}

// A texted code is not one a kept authenticator key can answer, whatever key
// is kept.
func TestAKeptKeyDoesNotAnswerATextedCode(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts:   true,
		states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateOTP, Prompt: "Enter the code we texted you"}},
		fetch:  lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
	require.Empty(t, module.answered)
}

func TestAKeptKeyAnswersTheAuthenticatorCodeAndThePullCarriesOn(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{
			{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the code from your authenticator app", Method: agent.MethodAuthenticator},
			{State: merchants.StateSignedIn},
		},
		fetch: lapsedThenPulls(&sessions),
	}
	opened := stubBrowser()
	engine, now := engineWith(t, module, opened)
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	want, err := totp.Code(keptKey, *now)
	require.NoError(t, err)
	require.Equal(t, want, module.answered, "the code is made from the kept key at the moment it is asked for")
	require.Equal(t, 1, module.fills)
	require.Len(t, sessions, 2)
	require.Contains(t, result.Notes, "Amazon asked for an authenticator code; the kept key answered it")
}

func TestACodeTheMerchantTurnsDownPausesForAPerson(t *testing.T) {
	var sessions []string
	asked := merchants.State{State: merchants.StateOTP, Prompt: "Enter the code from your authenticator app", Method: agent.MethodAuthenticator}
	module := &fakeModule{
		acts:   true,
		states: []merchants.State{{State: merchants.StatePassword}, asked, asked},
		fetch:  lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
	require.Contains(t, result.Reason, "did not accept the code made from the kept authenticator key")
}

func TestAnApprovalOnAPhonePausesForAPerson(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateApproval}},
		fetch:  lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
	require.Contains(t, result.Reason, "approval")
}

// A merchant that could not be reached has not refused anything: the pull
// fails, and tomorrow's tries again.
func TestASignInPageThatWillNotLoadIsAFailedPullAndNotARefusal(t *testing.T) {
	var sessions []string
	module := &fakeModule{states: []merchants.State{{State: merchants.StatePassword}}, fetch: lapsedThenPulls(&sessions)}
	opened := stubBrowser()
	opened.page.OnGoto = func(url string) error {
		if url == module.SignInURL() {
			return errors.New("net::ERR_CONNECTION_RESET")
		}
		return nil
	}
	engine, _ := engineWith(t, module, opened)

	_, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.Error(t, err)
	require.Contains(t, err.Error(), "ERR_CONNECTION_RESET")
	require.Equal(t, 0, module.fills)
}

func TestWithoutAKeptPasswordASignInScreenStopsThePull(t *testing.T) {
	var sessions []string
	module := &fakeModule{states: []merchants.State{{State: merchants.StatePassword}}, fetch: lapsedThenPulls(&sessions)}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, nil)
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Empty(t, result.Paused)
	require.Equal(t, 1, opened.opens, "no sign-in browser is opened")
	require.Equal(t, 0, module.fills)
}

// A session the kept password had just opened and the merchant refuses as
// well stops the pull; the password is not typed a second time.
func TestASessionRefusedRightAfterTheKeptPasswordOpenedItStopsThere(t *testing.T) {
	module := &fakeModule{
		states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateSignedIn}},
		fetch: func(call merchants.Call) (merchants.Result, error) {
			return merchants.Result{NeedsSignIn: true}, nil
		},
	}
	engine, _ := engineWith(t, module, stubBrowser())

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, keptMerchantLogin())
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Empty(t, result.Paused, "the password got in; it is the session that was refused")
	require.Equal(t, "Amazon refused the session the kept password had just opened", result.Reason)
	require.Equal(t, 1, module.fills)
}

// Costco's shape: the kept session is a refresh token rather than cookies, and
// the sign-in hands back a new one out of the signed-in page.
type handingModule struct {
	*fakeModule
	handed json.RawMessage
}

func (m handingModule) SessionFromPage(page browser.Page, at time.Time) (json.RawMessage, bool, error) {
	return m.handed, true, nil
}

func TestAnExpiredRefreshTokenSignsInForANewOneAndPullsWithIt(t *testing.T) {
	const expired = `{"kind":"costco-b2c","refresh_token":"expired"}`
	const fresh = `{"kind":"costco-b2c","refresh_token":"fresh"}`
	const rotated = `{"kind":"costco-b2c","refresh_token":"rotated"}`
	var sessions []string
	module := handingModule{
		fakeModule: &fakeModule{
			id: domain.MerchantCostco, kinds: []string{"costco-b2c"},
			states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateSignedIn}},
			fetch: func(call merchants.Call) (merchants.Result, error) {
				sessions = append(sessions, string(call.Session))
				if string(call.Session) == expired {
					return merchants.Result{NeedsSignIn: true, Reason: "Costco's refresh token has expired"}, nil
				}
				return merchants.Result{Parsed: &merchantimport.Parsed{}, StorageState: json.RawMessage(rotated)}, nil
			},
		},
		handed: json.RawMessage(fresh),
	}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), domain.MerchantCostco, json.RawMessage(expired), 30, nil, nil, nil,
		keptMerchantLogin())
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Equal(t, []string{expired, fresh}, sessions)
	require.JSONEq(t, rotated, string(result.StorageState), "the token the pull rotated is the one kept")
	require.Equal(t, 1, opened.opens, "only the sign-in opens a browser; both pulls call over HTTP")
	require.Nil(t, opened.seeds[0], "a refresh token is not cookies to seed a browser with")
	require.Equal(t, 1, module.fills)
}

func TestACredentialPrintsWhichFieldsAreSetAndNoSecret(t *testing.T) {
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey
	for _, printed := range []string{
		fmt.Sprintf("%v", *login), fmt.Sprintf("%+v", *login), fmt.Sprintf("%#v", *login), login.String(),
	} {
		require.NotContains(t, printed, "a-kept-password")
		require.NotContains(t, printed, keptKey)
		require.NotContains(t, printed, "someone@example.test")
	}
}

// A code the merchant mailed or texted is waited for in the household's
// mailbox before the pull gives up on it; the mailbox itself is the backend's,
// so here it is a function that answers or does not.
func TestACodeThatReachesTheMailboxIsAnsweredAndThePullCarriesOn(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{
			{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the code we emailed you"},
			{State: merchants.StateSignedIn},
		},
		fetch: lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	waited := 0
	login.MailedCode = func(context.Context, time.Time) (string, bool) {
		waited++
		return "735102", true
	}

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Empty(t, result.Paused, "a code the mailbox answered pauses nothing")
	require.Equal(t, 1, waited)
	require.Equal(t, "735102", module.answered)
	require.Len(t, sessions, 2, "the pull ran again on the new session")
	require.Contains(t, result.Notes, "Amazon sent a code; the mailbox answered it")
	for _, note := range result.Notes {
		require.NotContains(t, note, "735102", "a code is never a note")
	}
}

func TestACodeThatNeverReachesTheMailboxPausesForAPerson(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts:   true,
		states: []merchants.State{{State: merchants.StatePassword}, {State: merchants.StateOTP, Prompt: "Enter the code we emailed you"}},
		fetch:  lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	login.MailedCode = func(context.Context, time.Time) (string, bool) { return "", false }

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
	require.Contains(t, result.Reason, "none reached the mailbox in time")
	require.Empty(t, module.answered)
}

func TestAnAuthenticatorCodeIsNeverWaitedForInTheMailbox(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the code from your authenticator app", Method: agent.MethodAuthenticator}},
		fetch: lapsedThenPulls(&sessions),
	}
	engine, _ := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	login.MailedCode = func(context.Context, time.Time) (string, bool) {
		t.Fatal("an authenticator code was waited for in the mailbox")
		return "", false
	}

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedCodeNeeded, result.Paused)
}

// A login whose household said its second factor is the authenticator: a code
// box that names no channel is answered from the kept key, and one that says
// the code was sent somewhere never is.
func TestALoginsAuthenticatorChoiceAnswersACodeBoxThatNamesNoChannel(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{
			{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the verification code"},
			{State: merchants.StateSignedIn},
		},
		fetch: lapsedThenPulls(&sessions),
	}
	engine, now := engineWith(t, module, stubBrowser())
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey
	login.SecondFactor = domain.SecondFactorTOTP

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	want, err := totp.Code(keptKey, *now)
	require.NoError(t, err)
	require.Equal(t, want, module.answered)

	for _, prompt := range []string{"Enter the code we texted you", "Enter the code we emailed you"} {
		require.False(t, keyAnswers(merchants.State{State: merchants.StateOTP, Prompt: prompt},
			provider.MerchantCredential{TOTPSecret: keptKey, SecondFactor: domain.SecondFactorTOTP}), prompt)
	}
}

// The rule a bill sign-in answers a code box by, which a merchant's is: a box
// naming no channel is the key's unless the household said the codes come by
// e-mail, and a box naming a mailbox or a phone never is.
func TestAKeptKeyAnswersACodeBoxByTheBillSignInsRule(t *testing.T) {
	for _, one := range []struct {
		where  merchants.State
		chose  domain.SecondFactor
		answer bool
	}{
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the verification code"}, "", true},
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the verification code"}, domain.SecondFactorTOTP, true},
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the verification code"}, domain.SecondFactorEmail, false},
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the code from Okta Verify", Method: agent.MethodAuthenticator},
			domain.SecondFactorEmail, true},
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the code we texted you"}, domain.SecondFactorTOTP, false},
		{merchants.State{State: merchants.StateOTP, Prompt: "Enter the code we emailed you"}, "", false},
		{merchants.State{State: merchants.StateOTP,
			Prompt: "Enter the one-time code from your authenticator app or text message"}, domain.SecondFactorEmail, false},
	} {
		login := provider.MerchantCredential{TOTPSecret: keptKey, SecondFactor: one.chose}
		require.Equal(t, one.answer, keyAnswers(one.where, login), "%q, chose %q", one.where.Prompt, one.chose)
	}
	require.False(t, keyAnswers(merchants.State{State: merchants.StateOTP, Prompt: "Enter the verification code"},
		provider.MerchantCredential{}), "no kept key answers nothing")
}

// A code minted with under five seconds of its step left is refused by the
// time it arrives, so the kept key waits for the next step, as a bill sign-in's
// does.
func TestAKeptKeyMintedLateInItsStepWaitsForTheNextOne(t *testing.T) {
	var sessions []string
	module := &fakeModule{
		acts: true,
		states: []merchants.State{
			{State: merchants.StatePassword},
			{State: merchants.StateOTP, Prompt: "Enter the code from your authenticator app", Method: agent.MethodAuthenticator},
			{State: merchants.StateSignedIn},
		},
		fetch: lapsedThenPulls(&sessions),
	}
	engine, now := engineWith(t, module, stubBrowser())
	*now = time.Date(2026, 9, 19, 4, 0, 27, 0, time.UTC)
	var slept []time.Duration
	engine.Sleep = func(d time.Duration) {
		slept = append(slept, d)
		*now = now.Add(d)
	}
	login := keptMerchantLogin()
	login.TOTPSecret = keptKey

	result, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(lapsedJar), 30, nil, nil, nil, login)
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Equal(t, []time.Duration{4 * time.Second}, slept, "three seconds to the step, and one into the next")
	want, err := totp.Code(keptKey, time.Date(2026, 9, 19, 4, 0, 31, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, want, module.answered)
}

// A form still up for a few readings after the press is the site posting the
// password and redirecting, not a refusal.
func TestAPasswordFormThatClearsWithinTheWaitIsNotARefusal(t *testing.T) {
	module := &fakeModule{states: []merchants.State{
		{State: merchants.StatePassword}, {State: merchants.StatePassword}, {State: merchants.StatePassword},
		{State: merchants.StatePassword}, {State: merchants.StatePassword}, {State: merchants.StateSignedIn},
	}}
	page := &browser.StubPage{Location: "https://merchant.test/sign-in"}

	state, err := signInOn(module, page, "someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateSignedIn, state.State)
	require.Equal(t, 1, module.fills)
	require.Equal(t, 4*passwordRecheck, page.Slept)
}

func TestAPasswordFormStillUpAfterTheWaitIsARefusal(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StatePassword}}}
	page := &browser.StubPage{Location: "https://merchant.test/sign-in"}

	state, err := signInOn(module, page, "someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateFailed, state.State)
	require.Equal(t, "the password was not accepted", state.Error)
	require.Equal(t, 1, module.fills)
	require.Equal(t, passwordAnswerWait, page.Slept)
}

// The loop presses through the way to verify and the send button, types the
// password once, and stops at the code.
func TestTheLoopPressesThroughAFactorStepAndTypesThePasswordOnce(t *testing.T) {
	module := &fakeModule{acts: true, states: []merchants.State{
		{State: merchants.StatePassword},
		{State: merchants.StateFactor, Choices: []merchants.FactorChoice{{Words: "Email me"}}},
		{State: merchants.StateFactor, Choices: []merchants.FactorChoice{{Words: "Send code"}}},
		{State: merchants.StateOTP, Prompt: "Enter the code we emailed you"},
	}}
	where, err := signInOn(module, &browser.StubPage{}, "someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateOTP, where.State)
	require.Equal(t, 1, module.fills)
}

func TestAFactorStepThatKeepsComingBackStopsAndSaysWhatItOffered(t *testing.T) {
	module := &fakeModule{acts: true, states: []merchants.State{
		{State: merchants.StatePassword},
		{State: merchants.StateFactor, Choices: []merchants.FactorChoice{{Words: "Send code"}}},
	}}
	where, err := signInOn(module, &browser.StubPage{}, "someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateFailed, where.State)
	require.Equal(t, `unrecognised factor page; offered "Send code"`, where.Error)
	require.Equal(t, 1, module.fills)
}

func TestAFactorStepTheModuleCannotTakeFailsNamingItsChoices(t *testing.T) {
	module := &fakeModule{states: []merchants.State{
		{State: merchants.StateFactor, Choices: []merchants.FactorChoice{{Words: "Call me"}}},
	}}
	where, err := signInOn(module, &browser.StubPage{}, "someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateFailed, where.State)
	require.Equal(t, `unrecognised factor page; offered "Call me"`, where.Error)
	require.Zero(t, module.fills)
}
