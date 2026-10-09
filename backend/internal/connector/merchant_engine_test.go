package connector

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/merchants"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The engine's flows, in process and with no Chromium: the sign-in state
// machine, the pull and the two sessions it runs on.

// engineWith is an engine whose browser is a stub and whose clock is a test's.
func engineWith(t *testing.T, module merchants.Module, opened *openedBrowser) (Merchants, *time.Time) {
	t.Helper()
	now := time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC)
	engine := Merchants{&Engine{
		Merchants: merchants.NewWith(module),
		Open:      opened.opener(),
		Now:       func() time.Time { return now },
	}}
	return engine, &now
}

func stubBrowser() *openedBrowser {
	return &openedBrowser{
		page: &browser.StubPage{Location: "https://merchant.test/sign-in"},
		jar:  `{"cookies":[{"name":"session","value":"x"}],"origins":[]}`,
	}
}

func TestATypedSignInFillsTheFormAndStopsAtTheCode(t *testing.T) {
	module := &fakeModule{states: []merchants.State{
		{State: merchants.StateEmail}, {State: merchants.StatePassword},
		{State: merchants.StateOTP, Prompt: "Enter the code we texted you"},
	}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	state, err := engine.StartSignIn(context.Background(), module.ID(),
		"someone@example.test", "a-password")
	require.NoError(t, err)
	require.Equal(t, merchants.StateOTP, state.State)
	require.Equal(t, "Enter the code we texted you", state.Prompt)
	require.NotEmpty(t, state.SessionID)
	require.Equal(t, "someone@example.test", module.email)
	require.Equal(t, "a-password", module.password)
	require.Equal(t, 1, module.attached, "the wire is listened to from the first request")
	require.Equal(t, []string{module.SignInURL()}, opened.page.Visited)
	require.Equal(t, 1, engine.Sessions())
}

// A sign-in that is through keeps no password: it is of no further use, and
// the browser is the way back in from here.
func TestASignedInSessionKeepsNoPassword(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateEmail}, {State: merchants.StateSignedIn}}}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.Equal(t, merchants.StateSignedIn, state.State)

	s, err := engine.find(state.SessionID)
	require.NoError(t, err)
	require.Empty(t, s.login().Password)
}

func TestAPasswordNobodyGaveIsAFailureAndNotALoop(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StatePassword}}}
	engine, _ := engineWith(t, module, stubBrowser())
	_, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "")
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}

// A merchant that keeps asking for the same thing is a failed sign-in with a
// reason, not a browser held open for twenty minutes.
func TestASignInThatGoesRoundInCirclesFails(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateEmail}}}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.Equal(t, merchants.StateFailed, state.State)
	require.Equal(t, "sign-in loop", state.Error)
	require.NotEmpty(t, state.Image, "a failure carries a picture of the page that caused it")
}

func TestAnsweringTheCodeCarriesTheSignInOn(t *testing.T) {
	module := &fakeModule{acts: true, states: []merchants.State{
		{State: merchants.StateEmail}, {State: merchants.StatePassword}, {State: merchants.StateOTP},
		// The answer re-reads the page, then the loop reads it again.
		{State: merchants.StateOTP}, {State: merchants.StateSignedIn},
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.Equal(t, merchants.StateOTP, state.State)

	state, err = engine.AnswerSignIn(context.Background(), state.SessionID, "123456")
	require.NoError(t, err)
	require.Equal(t, merchants.StateSignedIn, state.State)
	require.Equal(t, "123456", module.answered)
}

// A code pressed at a page that is not asking for one changes nothing and says
// what the page is asking for instead.
func TestAnAnswerAtAPageThatWantsNoCodeReportsThePageBack(t *testing.T) {
	module := &fakeModule{acts: false, states: []merchants.State{
		{State: merchants.StateEmail}, {State: merchants.StatePassword},
		{State: merchants.StateCaptcha, Prompt: "Type the characters in the picture"},
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	state, err = engine.AnswerSignIn(context.Background(), state.SessionID, "123456")
	require.NoError(t, err)
	require.Equal(t, merchants.StateCaptcha, state.State)
	require.Equal(t, "Type the characters in the picture", state.Prompt)
}

// A CAPTCHA is shown as the module cut it out of the page, and as the whole
// screen when it could not.
func TestACaptchaCarriesThePictureThePersonHasToRead(t *testing.T) {
	module := &fakeModule{
		captcha: "a-cut-out-png",
		states:  []merchants.State{{State: merchants.StateCaptcha}},
	}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.Equal(t, "a-cut-out-png", state.Image)

	module.captcha = ""
	state, err = engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.NotEmpty(t, state.Image, "the whole screen is the fallback")
}

func TestCompleteHandsBackTheJarAndTheNameTheSiteGreetsSomebodyBy(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateSignedIn}}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)

	kept, hint, err := engine.CompleteSignIn(context.Background(), state.SessionID)
	require.NoError(t, err)
	require.JSONEq(t, opened.jar, string(kept))
	require.Equal(t, "Alex", hint)
	require.Equal(t, 0, engine.Sessions(), "the session is done with")
	require.Equal(t, 1, opened.closed, "and so is its browser")
	// The cookies the site sets after a sign-in are only on the landing page.
	require.Equal(t, module.LandingURL(), opened.page.Visited[len(opened.page.Visited)-1])
}

// A sign-in that is not finished is a 409 and not a session sealed onto the
// account.
func TestCompleteRefusesASignInThatIsNotFinished(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateOTP, Prompt: "Enter the code"}}}
	engine, _ := engineWith(t, module, stubBrowser())
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)

	_, _, err = engine.CompleteSignIn(context.Background(), state.SessionID)
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentConflict)
	require.Contains(t, provider.AgentMessage(err), "Enter the code")
}

func TestASessionTheReaperHasTakenIsNotFound(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateOTP}}}
	opened := stubBrowser()
	engine, now := engineWith(t, module, opened)
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)

	*now = now.Add(SessionTTL + time.Minute)
	_, err = engine.SignInStatus(context.Background(), state.SessionID)
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentNotFound)
	require.Equal(t, 0, engine.Sessions())
	require.Equal(t, 1, opened.closed, "the browser nobody came back for is closed")
}

func TestCancellingASignInLetsItsBrowserGoAtOnce(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateOTP}}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
	require.Equal(t, 0, engine.Sessions())
	require.Equal(t, 1, opened.closed)
}

func TestAnUnknownMerchantIsABadRequestAndOpensNothing(t *testing.T) {
	opened := stubBrowser()
	engine, _ := engineWith(t, &fakeModule{}, opened)
	_, err := engine.StartSignIn(context.Background(), "walmart", "a@example.test", "p")
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.Equal(t, 0, opened.opens)
}

// --- The pull --------------------------------------------------------------------

func TestAPullHandsBackTheJarItEndedWith(t *testing.T) {
	var seen merchants.Call
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		seen = call
		call.Notes.Addf("read 2 orders")
		return merchants.Result{
			AccountHint: "Alex", Parsed: &merchantimport.Parsed{},
			Orders: 2, Charges: 3,
		}, nil
	}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"cookies":[],"origins":[]}`), 45, []string{"111-2222222-3333333"},
		[]string{"111-4444444-5555555"}, nil, nil)
	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Equal(t, "Alex", result.AccountHint)
	require.Equal(t, 2, result.Orders)
	require.Equal(t, 3, result.Charges)
	require.Equal(t, []string{"read 2 orders"}, result.Notes)
	// Amazon rolls its cookies: what is kept is the jar after the pull, never
	// the one that was sent.
	require.JSONEq(t, opened.jar, string(result.StorageState))
	require.Equal(t, 1, opened.closed, "the browser is closed whether or not the pull worked")

	require.Equal(t, 45, seen.SinceDays)
	require.True(t, seen.SkipDetails["111-2222222-3333333"])
	require.True(t, seen.Invoiced["111-4444444-5555555"])
	require.False(t, seen.Invoiced["111-2222222-3333333"])
	require.Same(t, opened.page, seen.Page)
	require.Nil(t, seen.HTTP, "a browser pull is given no caller of its own")
}

func TestAPullCarriesTheListenerToTheModuleAndSaysItIsOpeningTheSite(t *testing.T) {
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		call.Report("Reading invoices: %d of %d", 1, 4)
		return merchants.Result{Parsed: &merchantimport.Parsed{}}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	var lines []string
	ctx := provider.WithPullProgress(context.Background(), func(line string) { lines = append(lines, line) })

	_, err := engine.Fetch(ctx, module.ID(), json.RawMessage(`{"cookies":[],"origins":[]}`), 45, nil, nil, nil, nil)

	require.NoError(t, err)
	require.Equal(t, []string{"Opening " + merchants.Name(module), "Reading invoices: 1 of 4"}, lines)
}

func TestAPullPrintsTheInvoicesAModuleLaidOut(t *testing.T) {
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		return merchants.Result{Parsed: &merchantimport.Parsed{}, Invoices: []merchants.Invoice{
			{OrderID: "A-1", Filename: "a-1.pdf", PDF: []byte("%PDF-1.4 printed by the page")},
			{OrderID: "B-2", Filename: "b-2.pdf", HTML: "<p>B-2</p>"},
			{OrderID: "C-3", Filename: "c-3.pdf"},
		}}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	var printed []string
	engine.Print = func(page string) ([]byte, error) {
		printed = append(printed, page)
		return []byte("%PDF-1.4 " + page), nil
	}

	result, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"cookies":[]}`), 30, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, []string{"<p>B-2</p>"}, printed, "only the HTML is printed")
	require.Len(t, result.Invoices, 2, "an invoice with nothing to print is dropped")
	require.Equal(t, "A-1", result.Invoices[0].OrderNumber)
	require.Equal(t, "a-1.pdf", result.Invoices[0].Filename)
	require.Equal(t, "%PDF-1.4 printed by the page", string(result.Invoices[0].PDF))
	require.Equal(t, "B-2", result.Invoices[1].OrderNumber)
	require.Equal(t, "%PDF-1.4 <p>B-2</p>", string(result.Invoices[1].PDF))
}

func TestAPrinterThatFailsCostsTheInvoicesNotThePull(t *testing.T) {
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		return merchants.Result{Parsed: &merchantimport.Parsed{}, Orders: 1, Invoices: []merchants.Invoice{
			{OrderID: "B-2", Filename: "b-2.pdf", HTML: "<p>B-2</p>"},
		}}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	engine.Print = func(string) ([]byte, error) { return nil, errors.New("no browser to print in") }

	result, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"cookies":[]}`), 30, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 1, result.Orders)
	require.Empty(t, result.Invoices)
	require.Len(t, result.Notes, 1)
	require.Contains(t, result.Notes[0], "no browser to print in")
}

// A window nobody set is a month; one past ten years is ten years.
func TestThePullsWindowIsClamped(t *testing.T) {
	var seen []int
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		seen = append(seen, call.SinceDays)
		return merchants.Result{}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	for _, days := range []int{0, -5, 99999} {
		_, err := engine.Fetch(context.Background(), module.ID(),
			json.RawMessage(`{"cookies":[]}`), days, nil, nil, nil, nil)
		require.NoError(t, err)
	}
	require.Equal(t, []int{30, 30, 3650}, seen)
}

func TestAPullThatMetASignInScreenSaysSoWithAPicture(t *testing.T) {
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		return merchants.Result{NeedsSignIn: true}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	result, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"cookies":[]}`), 30, nil, nil, nil, nil)
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Amazon asked to sign in again", result.Reason)
	require.NotEmpty(t, result.Image)
	require.Empty(t, result.StorageState, "a challenged session is not written over")
}

func TestAPullThatFailsOnAPageCarriesThePage(t *testing.T) {
	broken := errors.New("the orders page never finished loading")
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		return merchants.Result{}, broken
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	_, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"cookies":[]}`), 30, nil, nil, nil, nil)
	require.ErrorIs(t, err, broken)
	require.NotEmpty(t, provider.ScreenshotOf(err))
}

func TestAPullOverHTTPThatFailsHasNoPageToShow(t *testing.T) {
	broken := errors.New("the orders call answered 500")
	module := &fakeModule{
		id: domain.MerchantCostco, kinds: []string{"costco-b2c"},
		fetch: func(call merchants.Call) (merchants.Result, error) {
			return merchants.Result{}, broken
		},
	}
	engine, _ := engineWith(t, module, stubBrowser())
	_, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"costco-b2c","refresh_token":"old"}`), 30, nil, nil, nil, nil)
	require.ErrorIs(t, err, broken)
	require.Nil(t, provider.ScreenshotOf(err))
}

// A handed-over session opens no browser at all: the module pulls over plain
// HTTP and says what to keep for tomorrow.
func TestAHandedOverSessionOpensNoBrowser(t *testing.T) {
	var seen merchants.Call
	module := &fakeModule{
		id: domain.MerchantCostco, kinds: []string{"costco-b2c"},
		fetch: func(call merchants.Call) (merchants.Result, error) {
			seen = call
			return merchants.Result{
				Parsed:       &merchantimport.Parsed{},
				StorageState: json.RawMessage(`{"kind":"costco-b2c","refresh_token":"new"}`),
			}, nil
		},
	}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	result, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"costco-b2c","refresh_token":"old"}`), 30, nil, nil, nil, nil)
	require.NoError(t, err)
	require.Equal(t, 0, opened.opens, "no browser is opened for a handed-over session")
	require.Nil(t, seen.Page)
	require.NotNil(t, seen.HTTP, "it is given a caller of its own instead")
	require.JSONEq(t, `{"kind":"costco-b2c","refresh_token":"new"}`, string(result.StorageState))
}

func TestASessionOfAKindTheMerchantDoesNotTakeIsRefused(t *testing.T) {
	module := &fakeModule{kinds: []string{"costco-b2c"}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)
	_, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"something-else"}`), 30, nil, nil, nil, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.Contains(t, provider.AgentMessage(err), `does not take a "something-else" session`)
	require.Equal(t, 0, opened.opens)
}

// The engine runs in the API process, so a module that panics must be a
// failed pull with a reason rather than a stack trace where a number belongs.
func TestAPanicInAMerchantModuleIsAFailedPullAndNotACrash(t *testing.T) {
	module := &fakeModule{fetch: func(call merchants.Call) (merchants.Result, error) {
		var boom []int
		_ = boom[3]
		return merchants.Result{}, nil
	}}
	engine, _ := engineWith(t, module, stubBrowser())
	engine.Log = quietLog()
	_, err := engine.Fetch(context.Background(), module.ID(), json.RawMessage(`{"cookies":[]}`), 30, nil, nil, nil, nil)
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentFailed)
	require.Contains(t, provider.AgentMessage(err), "Amazon failed inside the built-in browser engine")
}

func TestHealthAnswersWhileThereAreModulesToReach(t *testing.T) {
	module := &fakeModule{}
	engine, _ := engineWith(t, module, stubBrowser())
	require.True(t, engine.Available())
	require.NoError(t, engine.Health(context.Background(), module.ID()))

	require.False(t, Merchants{}.Available())
}

// Costco runs only in Camoufox, so without a Camoufox server it has no browser
// even though Amazon's Chrome is there.
func TestCostcoIsUnavailableWithoutCamoufox(t *testing.T) {
	engine := Merchants{&Engine{Merchants: merchants.NewRegistry(), Open: stubBrowser().opener()}}
	require.NoError(t, engine.Health(context.Background(), domain.MerchantAmazon))
	require.ErrorIs(t, engine.Health(context.Background(), domain.MerchantCostco), browser.ErrNoFirefox)

	engine.OpenFirefox = stubBrowser().opener()
	require.NoError(t, engine.Health(context.Background(), domain.MerchantCostco))
}

// A call that names no merchant means Amazon.
func TestTheRegistryCarriesBothMerchantsAndDefaultsToAmazon(t *testing.T) {
	registry := merchants.NewRegistry()
	amazon, err := registry.Pick("")
	require.NoError(t, err)
	require.Equal(t, domain.MerchantAmazon, amazon.ID())

	costco, err := registry.Pick(domain.MerchantCostco)
	require.NoError(t, err)
	require.Equal(t, domain.MerchantCostco, costco.ID())

	_, err = registry.Pick("walmart")
	require.ErrorContains(t, err, "unknown merchant")
}

// backfillingModule is a fake whose invoice pages can be reopened.
type backfillingModule struct {
	*fakeModule
	backfill func(call merchants.Call, orders []string, each func(string, *merchants.Invoice)) (string, bool)
}

func (m backfillingModule) BackfillInvoices(call merchants.Call, orders []string, each func(string, *merchants.Invoice)) (string, bool) {
	return m.backfill(call, orders, each)
}

func TestABackfillHandsEachInvoiceOverAndKeepsTheJar(t *testing.T) {
	module := backfillingModule{fakeModule: &fakeModule{}, backfill: func(call merchants.Call, orders []string, each func(string, *merchants.Invoice)) (string, bool) {
		each(orders[0], &merchants.Invoice{OrderID: orders[0], Filename: "amazon-invoice-" + orders[0] + ".pdf", PDF: []byte("%PDF-1.4 x")})
		each(orders[1], nil)
		each(orders[2], &merchants.Invoice{OrderID: orders[2]})
		return "", false
	}}
	opened := stubBrowser()
	engine, _ := engineWith(t, module, opened)

	var filed, missed []string
	result, err := engine.BackfillInvoices(context.Background(), module.ID(), json.RawMessage(`{"cookies":[]}`),
		[]string{"111-0000003-0000003", "111-0000002-0000002", "111-0000001-0000001"},
		func(order string, invoice *provider.MerchantInvoice) {
			if invoice == nil {
				missed = append(missed, order)
				return
			}
			filed = append(filed, invoice.OrderNumber)
		})
	require.NoError(t, err)
	require.Equal(t, []string{"111-0000003-0000003"}, filed)
	require.Equal(t, []string{"111-0000002-0000002", "111-0000001-0000001"}, missed,
		"a page printed empty is a miss")
	require.JSONEq(t, opened.jar, string(result.StorageState))
	require.False(t, result.NeedsSignIn)
	require.Equal(t, 1, opened.closed)
}

func TestABackfillThatMetASignInKeepsNoJar(t *testing.T) {
	module := backfillingModule{fakeModule: &fakeModule{}, backfill: func(merchants.Call, []string, func(string, *merchants.Invoice)) (string, bool) {
		return "Sign in", true
	}}
	engine, _ := engineWith(t, module, stubBrowser())

	result, err := engine.BackfillInvoices(context.Background(), module.ID(), json.RawMessage(`{"cookies":[]}`),
		[]string{"111-0000001-0000001"}, func(string, *provider.MerchantInvoice) {})
	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Sign in", result.Stopped)
	require.Nil(t, result.StorageState)
}

func TestABackfillNeedsABrowserSessionAndAModuleThatReopens(t *testing.T) {
	opened := stubBrowser()
	engine, _ := engineWith(t, &fakeModule{}, opened)
	_, err := engine.BackfillInvoices(context.Background(), domain.MerchantAmazon, json.RawMessage(`{"cookies":[]}`),
		[]string{"111-0000001-0000001"}, func(string, *provider.MerchantInvoice) {})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)

	module := backfillingModule{fakeModule: &fakeModule{}, backfill: func(merchants.Call, []string, func(string, *merchants.Invoice)) (string, bool) {
		t.Fatal("a handed-over session opens no page")
		return "", false
	}}
	engine, _ = engineWith(t, module, opened)
	_, err = engine.BackfillInvoices(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"costco-b2c","refresh_token":"old"}`), []string{"1"}, func(string, *provider.MerchantInvoice) {})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.Equal(t, 0, opened.opens)
}

func TestAStoredCostcoReceiptIsLaidOutAndPrintedWithNoRequest(t *testing.T) {
	engine, _ := engineWith(t, &fakeModule{}, stubBrowser())
	var printed []string
	engine.Print = func(page string) ([]byte, error) {
		printed = append(printed, page)
		return []byte("%PDF-1.4 laid out"), nil
	}
	receipt := provider.MerchantReceipt{
		Merchant: domain.MerchantCostco, Number: "21100000000000000101", Location: "Invented Warehouse",
		Date: "2026-06-01", Tax: "1.50", Total: "25.50",
		Items:   []provider.MerchantReceiptLine{{Number: "1000001", Title: "INVENTED BREAD", Quantity: 2, Amount: "24.00"}},
		Tenders: []provider.MerchantReceiptLine{{Title: "VISA ••••5678", Amount: "25.50"}},
	}

	filename, pdf, err := engine.LayOutReceipt(receipt)
	require.NoError(t, err)
	require.Equal(t, "costco-receipt-21100000000000000101.pdf", filename)
	require.Equal(t, "%PDF-1.4 laid out", string(pdf))
	require.Len(t, printed, 1)
	require.Contains(t, printed[0], "INVENTED BREAD")
	require.Contains(t, printed[0], "Invented Warehouse")
	require.Contains(t, printed[0], "VISA ••••5678")
	require.NotContains(t, printed[0], "http", "the page reaches for nothing")

	receipt.Merchant = domain.MerchantAmazon
	_, _, err = engine.LayOutReceipt(receipt)
	require.Error(t, err, "an Amazon invoice is its own page, never a layout")
	engine.Print = nil
	receipt.Merchant = domain.MerchantCostco
	_, _, err = engine.LayOutReceipt(receipt)
	require.Error(t, err)
}
