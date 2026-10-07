package billers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
)

// The field names and the "Bill due" wording are the portal's
// (docs/connectors/providers.md); every value is invented.

const weEnergiesTestAccount = "9876543210-01234"

func weEnergiesListRow() WeEnergiesListRow {
	return WeEnergiesListRow{
		SelectAcct:   weEnergiesTestAccount,
		CustomerName: "PAT EXAMPLE",
		Address: "PAT EXAMPLE\n100 EXAMPLE ST\nANYTOWN, ZZ 00000\nAccount #: " +
			weEnergiesTestAccount,
		DueBy:     "10/05/2026",
		DueAmount: "$90.00",
		Text: "PAT EXAMPLE 100 EXAMPLE ST ANYTOWN, ZZ 00000 Account #: " +
			weEnergiesTestAccount + " Amt due by 10/05/2026 $90.00",
	}
}

// Newest first, as the portal sorts it: a bill row carries a bill amount and a
// "Bill due" description, a payment row carries a payment amount instead.
func weEnergiesLedgerRows() []WeEnergiesLedgerRow {
	return []WeEnergiesLedgerRow{
		{},
		{
			Date: "09/10/2026", Description: "Bill due 10/05/2026", BillAmount: "$90.00",
			StatementHref: "https://www.we-energies.com/AccountSummary/View/BillPdf?billid=aaa&acct=zzz",
		},
		{Date: "09/04/2026", Description: "Payment - Automatic Payment", PaymentAmount: "$70.00"},
		{
			Date: "08/11/2026", Description: "Bill due 09/04/2026", BillAmount: "$70.00",
			StatementHref: "https://www.we-energies.com/AccountSummary/View/BillPdf?billid=bbb&acct=zzz",
		},
		{Date: "08/06/2026", Description: "Payment - Automatic Payment", PaymentAmount: "$55.00"},
		{Date: "07/13/2026", Description: "Bill due 08/06/2026", BillAmount: "$55.00"},
		{Date: "07/08/2026", Description: "Payment - Automatic Payment", PaymentAmount: "$45.00"},
		{Date: "06/12/2026", Description: "Bill due 07/08/2026", BillAmount: "$45.00"},
	}
}

func weEnergiesScheduledPayments() []WeEnergiesBooked {
	return []WeEnergiesBooked{{Date: "10/05/2026", Amount: "$90.00"}}
}

func TestAWeEnergiesListRowAnswersThePortalNumberTheAddressAndAMask(t *testing.T) {
	found := WeEnergiesSubaccountsFromRows([]WeEnergiesListRow{weEnergiesListRow()})

	require.Len(t, found, 1)
	require.Equal(t, weEnergiesTestAccount, found[0].ExternalID)
	require.Equal(t, "100 EXAMPLE ST, ANYTOWN, ZZ 00000", found[0].Label)
	require.Equal(t, "••••1234", found[0].MaskedNumber)
}

func TestAWeEnergiesRowWithNoSubmitFallsBackToItsAccountLine(t *testing.T) {
	row := weEnergiesListRow()
	row.SelectAcct = ""

	found := WeEnergiesSubaccountsFromRows([]WeEnergiesListRow{row})

	require.Len(t, found, 1)
	require.Equal(t, weEnergiesTestAccount, found[0].ExternalID)
}

func TestAWeEnergiesRowThatNamesNoAccountAtAllIsLeftOut(t *testing.T) {
	require.Empty(t, WeEnergiesSubaccountsFromRows([]WeEnergiesListRow{
		{Address: "ANYTOWN, ZZ", Text: "ANYTOWN, ZZ"},
	}))
}

func TestTheWeEnergiesLedgerAnswersItsThreeNewestBillsWithThePortalsDueDate(t *testing.T) {
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount,
		weEnergiesScheduledPayments(), &Notes{})

	require.Len(t, bills, 3)
	require.Equal(t, []string{"2026-10-05", "2026-09-04", "2026-08-06"},
		[]string{bills[0].DueOn, bills[1].DueOn, bills[2].DueOn})

	newest := bills[0]
	require.Equal(t, weEnergiesTestAccount, newest.Subaccount)
	require.Equal(t, weEnergiesTestAccount+":2026-09-10", newest.ExternalID)
	require.Equal(t, "2026-09-10", newest.IssuedOn)
	require.Equal(t, "90.00", newest.AmountDue.String())
	require.Equal(t, "USD", newest.Currency)

	var raw weEnergiesRaw
	require.NoError(t, json.Unmarshal(newest.Raw, &raw))
	require.Equal(t, weEnergiesLedgerRows()[1].StatementHref, raw.StatementPath)
}

func TestAWeEnergiesBillIsOpenUntilAPaymentPostsOnOrAfterItsOwnDate(t *testing.T) {
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount,
		weEnergiesScheduledPayments(), &Notes{})

	require.Equal(t, []string{"Open", "Paid", "Paid"},
		[]string{bills[0].Status, bills[1].Status, bills[2].Status})
}

func TestTheBookedWeEnergiesPaymentIsReportedAgainstTheOpenBillAndNoOther(t *testing.T) {
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount,
		weEnergiesScheduledPayments(), &Notes{})

	require.Equal(t, "2026-10-05", bills[0].AutopayOn)
	require.Equal(t, "", bills[1].AutopayOn)
}

func TestTheWeEnergiesPortalShowsNoServicePeriodSoNoneIsInvented(t *testing.T) {
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount,
		weEnergiesScheduledPayments(), &Notes{})

	require.Equal(t, "", bills[0].PeriodStart)
	require.Equal(t, "", bills[0].PeriodEnd)
}

func TestAWeEnergiesPaymentRowIsNeverABillAndAHeaderRowIsNeverAnything(t *testing.T) {
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount, nil, &Notes{})

	require.Len(t, bills, 3)
	for _, one := range bills {
		require.NotEmpty(t, one.DueOn)
		require.NotEmpty(t, one.AmountDue)
	}
}

func TestAWeEnergiesBillRowWithNoReadableAmountIsLeftOutWithANote(t *testing.T) {
	row := weEnergiesLedgerRows()[1]
	row.BillAmount = ""
	notes := &Notes{}

	bills := WeEnergiesBillsFromLedger([]WeEnergiesLedgerRow{row}, weEnergiesTestAccount, nil, notes)

	require.Empty(t, bills)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no readable amount")
}

// The account list states the open balance outright, and it outranks what the
// ledger's payment dates suggest.

func TestTheWeEnergiesListRowStatesTheOpenBalanceAndItsDueDate(t *testing.T) {
	balance := WeEnergiesBalanceFromRow(weEnergiesListRow(), weEnergiesTestAccount)

	require.True(t, balance.Read)
	require.Equal(t, "2026-10-05", balance.DueOn)
	require.Equal(t, "90.00", balance.Amount.String())
}

func TestAWeEnergiesRowWhoseSpansAreNamedDifferentlyIsReadFromItsText(t *testing.T) {
	row := weEnergiesListRow()
	row.DueBy, row.DueAmount = "", ""

	balance := WeEnergiesBalanceFromRow(row, weEnergiesTestAccount)

	require.True(t, balance.Read)
	require.Equal(t, "2026-10-05", balance.DueOn)
	require.Equal(t, "90.00", balance.Amount.String())
}

func TestAWeEnergiesCreditBalanceIsNothingOwed(t *testing.T) {
	row := weEnergiesListRow()
	row.DueAmount = "$12.50 CR"

	balance := WeEnergiesBalanceFromRow(row, weEnergiesTestAccount)

	require.True(t, balance.Read)
	require.False(t, Owes(balance.Amount))
}

func TestAWeEnergiesRowThatStatesNoFigureSaysNothingAboutWhatIsOwed(t *testing.T) {
	row := WeEnergiesListRow{SelectAcct: weEnergiesTestAccount, Text: "100 EXAMPLE ST"}
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount, nil, &Notes{})

	merged := WeEnergiesWithBalance(bills, WeEnergiesBalanceFromRow(row, weEnergiesTestAccount))

	require.Equal(t, []string{"Open", "Paid", "Paid"},
		[]string{merged[0].Status, merged[1].Status, merged[2].Status})
}

// The ledger has not caught up with the newest bill, or did not open at all:
// the outstanding bill is still reported, from the list, with the older ones.
func TestAnOutstandingWeEnergiesBillTheLedgerNeverShowedIsReportedFromTheList(t *testing.T) {
	older := weEnergiesLedgerRows()[2:]
	bills := WeEnergiesBillsFromLedger(older, weEnergiesTestAccount, nil, &Notes{})

	merged := WeEnergiesWithBalance(bills, WeEnergiesBalanceFromRow(weEnergiesListRow(), weEnergiesTestAccount))

	require.Len(t, merged, 3)
	require.Equal(t, []string{"2026-10-05", "2026-09-04", "2026-08-06"},
		[]string{merged[0].DueOn, merged[1].DueOn, merged[2].DueOn})
	require.Equal(t, "Open", merged[0].Status)
	require.Equal(t, "90.00", merged[0].AmountDue.String())
	require.Equal(t, weEnergiesTestAccount, merged[0].Subaccount)
	require.Equal(t, "USD", merged[0].Currency)
	require.Equal(t, "Paid", merged[1].Status)
}

func TestWithNoLedgerAtAllTheWeEnergiesBalanceIsStillTheOpenBill(t *testing.T) {
	merged := WeEnergiesWithBalance(nil, WeEnergiesBalanceFromRow(weEnergiesListRow(), weEnergiesTestAccount))

	require.Len(t, merged, 1)
	require.Equal(t, "Open", merged[0].Status)
	require.Equal(t, "2026-10-05", merged[0].DueOn)
}

// A household that pays by hand paid the August bill late, after September's
// was issued. The ledger's rule reads that payment as settling September's
// bill; the list still says it is owed, and the list is right.
func TestAWeEnergiesBillStillOwedIsOpenThoughALatePaymentPostedAfterItsDate(t *testing.T) {
	rows := []WeEnergiesLedgerRow{
		{Date: "09/15/2026", Description: "Payment - Thank You", PaymentAmount: "$70.00"},
		{Date: "09/10/2026", Description: "Bill due 10/05/2026", BillAmount: "$90.00"},
		{Date: "08/11/2026", Description: "Bill due 09/04/2026", BillAmount: "$70.00"},
	}
	bills := WeEnergiesBillsFromLedger(rows, weEnergiesTestAccount, nil, &Notes{})
	require.Equal(t, "Paid", bills[0].Status, "the ledger's own inference, which the list corrects")

	merged := WeEnergiesWithBalance(bills, WeEnergiesBalanceFromRow(weEnergiesListRow(), weEnergiesTestAccount))

	require.Len(t, merged, 2)
	require.Equal(t, "2026-10-05", merged[0].DueOn)
	require.Equal(t, "Open", merged[0].Status)
}

func TestAWeEnergiesBalanceOfNothingSettlesEveryBill(t *testing.T) {
	row := weEnergiesListRow()
	row.DueAmount = "$0.00"
	bills := WeEnergiesBillsFromLedger(weEnergiesLedgerRows(), weEnergiesTestAccount,
		weEnergiesScheduledPayments(), &Notes{})

	merged := WeEnergiesWithBalance(bills, WeEnergiesBalanceFromRow(row, weEnergiesTestAccount))

	for _, one := range merged {
		require.Equal(t, "Paid", one.Status)
		require.Empty(t, one.AutopayOn)
	}
}

// And the module over a page that is not a browser: what is asserted is the
// decisions and the steps, not Chromium.

// Choosing the account and showing the bills are post-backs, and each is
// waited for by what it puts on the page: a Settle straight after the click
// reads the page being left.
func TestWeEnergiesWaitsForEachPostBackToLandBeforeReading(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	var waited []string
	page.OnWaitFor = func(script string, _ time.Duration) error {
		waited = append(waited, script)
		return nil
	}

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.Len(t, result.Bills, 3)
	require.Equal(t, []string{weEnergiesHasBillsBox, weEnergiesShowsBills}, waited)
}

// A choice that lands on the account overview rather than the payment history
// is followed by the payment history itself.
func TestAWeEnergiesChoiceThatLandsElsewhereOpensThePaymentHistoryDirectly(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	page.OnWaitFor = func(script string, _ time.Duration) error {
		if script == weEnergiesHasBillsBox && page.Location != weEnergiesPaymentHistory {
			return errors.New("timeout")
		}
		return nil
	}

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.Contains(t, page.Visited, weEnergiesPaymentHistory)
	require.Len(t, result.Bills, 3)
}

// With the ledger unreachable the balance the list shows is still a
// bill, and the note says which step failed.
func TestWhenTheWeEnergiesLedgerNeverOpensTheOutstandingBillIsStillReported(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	page.OnWaitFor = func(string, time.Duration) error { return errors.New("timeout") }
	page.OnCount = func(string) (int, error) { return 0, nil }
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, result.Bills, 1)
	require.Equal(t, "Open", result.Bills[0].Status)
	require.Equal(t, "2026-10-05", result.Bills[0].DueOn)
	require.Equal(t, "90.00", result.Bills[0].AmountDue.String())
	require.Contains(t, notes.List()[0], "offered no Bills box")
}

func TestWeEnergiesSaysASignInIsOwedWhenTheListIsNotTheListAtAll(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	// The portal's own redirect to the B2C tenant, carrying the return path.
	page.OnGoto = func(string) error {
		page.Location = "https://login.we-energies.com/b2c/authorize?redirect=%2Fsecure"
		return nil
	}
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "We Energies asked to sign in again", result.Reason)
	require.Contains(t, notes.List()[0], "the profile is not signed in")
	require.NotContains(t, notes.List()[0], "?")
}

func TestWeEnergiesTicksTheBillsBoxAndPostsBackBeforeReadingTheLedger(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, result.Bills, 3)
	require.Contains(t, page.Clicked, weEnergiesRefresh)
	require.Equal(t, []string{weEnergiesShowBills}, page.Checked)
}

func TestWeEnergiesReadsOnlyTheAccountsThePullAskedFor(t *testing.T) {
	module := NewWeEnergies()
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page: stubWeEnergiesPage(), Notes: notes, Subaccounts: []string{"0000000000-00000"},
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Empty(t, result.Bills)
	require.Contains(t, notes.List()[0], "none of them the 1 this pull asked for")
}

func TestAWeEnergiesStatementIsAPDFThePageFetchedWithItsOwnCookies(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	bill := Bill{Subaccount: weEnergiesTestAccount, IssuedOn: "2026-09-10",
		ExternalID: weEnergiesTestAccount + ":2026-09-10"}
	bill.Raw, _ = json.Marshal(weEnergiesRaw{
		StatementPath: "https://www.we-energies.com/AccountSummary/View/BillPdf?billid=aaa&acct=zzz",
	})

	document, err := module.FetchDocument(Call{Page: page, Notes: &Notes{}}, bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "we-energies-1234-2026-09-10.pdf", document.Filename)
}

func TestAWeEnergiesStatementThatIsNotAPDFIsNoDocumentAndANote(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()
	page.OnEvaluate = weEnergiesEvaluate(page, func(script string, answer any) any {
		if script == plainPageCall {
			return map[string]any{"status": 302, "type": "text/html", "base64": ""}
		}
		return answer
	})
	bill := Bill{Subaccount: weEnergiesTestAccount, ExternalID: weEnergiesTestAccount + ":2026-09-10"}
	bill.Raw, _ = json.Marshal(weEnergiesRaw{StatementPath: "https://www.we-energies.com/x"})
	notes := &Notes{}

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestEveryWeEnergiesCallIsHandedShapesAPageCanTake(t *testing.T) {
	module := NewWeEnergies()
	page := stubWeEnergiesPage()

	_, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.NotEmpty(t, page.Args, "the account list and the ledger are both read in the page")
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
}

func stubWeEnergiesPage() *browser.StubPage {
	page := &browser.StubPage{Location: weEnergiesHome + "/AccountSummary/View/PaymentHistory"}
	page.OnEvaluate = weEnergiesEvaluate(page, nil)
	// The two controls the post-back needs, and nothing the module does not ask
	// for: a count of zero is how a page says a control is not there.
	page.OnCount = func(selector string) (int, error) {
		switch selector {
		case weEnergiesShowBills, weEnergiesRefresh, weEnergiesLedger + " tr":
			return 1, nil
		}
		return 0, nil
	}
	page.OnCheck = func(string) (bool, error) { return true, nil }
	return page
}

// weEnergiesEvaluate answers each of the module's three scripts. `rewrite` is
// how one test makes a single script answer differently.
func weEnergiesEvaluate(
	page *browser.StubPage, rewrite func(script string, answer any) any,
) func(string, any) (any, error) {
	return func(script string, arg any) (any, error) {
		var answer any
		switch script {
		case weEnergiesListScript:
			answer = []any{weEnergiesStubListRow()}
		case weEnergiesChooseScript:
			answer = arg == weEnergiesTestAccount
		case weEnergiesLedgerScript:
			answer = map[string]any{
				"rows":   weEnergiesStubLedger(),
				"booked": []any{map[string]any{"date": "10/05/2026", "amount": "$90.00"}},
			}
		case weEnergiesShowsBills:
			// The box has just been ticked; the rows come with the refresh.
			answer = false
		case plainPageCall:
			answer = map[string]any{
				"status": 200, "type": "application/pdf",
				"base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invented")),
			}
		}
		if rewrite != nil {
			answer = rewrite(script, answer)
		}
		return answer, nil
	}
}

func weEnergiesStubListRow() map[string]any {
	row := weEnergiesListRow()
	return map[string]any{
		"selectAcct": row.SelectAcct, "customerName": row.CustomerName,
		"address": row.Address, "dueBy": row.DueBy, "dueAmount": row.DueAmount, "text": row.Text,
	}
}

func weEnergiesStubLedger() []any {
	out := make([]any, 0, 8)
	for _, row := range weEnergiesLedgerRows() {
		out = append(out, map[string]any{
			"date": row.Date, "description": row.Description,
			"paymentAmount": row.PaymentAmount, "billAmount": row.BillAmount,
			"statementHref": row.StatementHref,
		})
	}
	return out
}

// The sign-in is an Azure AD B2C tenant: one form with both boxes, one submit
// button, and an error heading kept in the document from the first paint and
// shown only on a refusal. The ids are the tenant's; every value is invented.
func weEnergiesSignInReading() Reading {
	return Reading{
		Fields: []Field{
			{Type: "email", ID: "signInName", Name: "Sign in name", Label: "Email"},
			{Type: "password", ID: "password", Name: "Password", Label: "Password"},
		},
		Submits: 1,
		Heading: "Sign in",
		Text:    "Sign in\nEmail\nPassword\nSign in\nForgot password?\nSign Up",
	}
}

func weEnergiesAccountReading() Reading {
	return Reading{
		SignOutLink: true,
		Heading:     "Account Summary",
		Text:        "Account Summary\nSign Out\nBill History\nPayment History",
	}
}

// weEnergiesSignInPage answers each reading in turn, so a page that is still
// painting can be handed over as the first one.
func weEnergiesSignInPage(location string, readings ...Reading) *browser.StubPage {
	page := &browser.StubPage{Location: location}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != readFormScript {
			return nil, nil
		}
		next := readings[0]
		if len(readings) > 1 {
			readings = readings[1:]
		}
		encoded, err := json.Marshal(next)
		if err != nil {
			return nil, err
		}
		var out any
		return out, json.Unmarshal(encoded, &out)
	}
	return page
}

// The widget renders after the document has loaded and after the network has
// gone quiet, so the first reading of it is a page with no boxes.
func TestAWeEnergiesSignInPageStillPaintingIsReadAgainAndNotCalledUnrecognised(t *testing.T) {
	module := NewWeEnergies()
	page := weEnergiesSignInPage(
		"https://login.example.test/tenant/oauth2/v2.0/authorize?p=b2c_1a_ya_signup_signin",
		Reading{Text: ""}, weEnergiesSignInReading())

	where, err := module.Classify(page)

	require.NoError(t, err)
	require.Equal(t, StatePassword, where.State)
}

// Both boxes are on one form, so the password step fills both and presses once.
func TestTheWeEnergiesFormIsFilledWholeAndSubmittedOnce(t *testing.T) {
	module := NewWeEnergies()
	page := weEnergiesSignInPage(
		"https://login.example.test/tenant/oauth2/v2.0/authorize", weEnergiesSignInReading())

	offersControls(page, SubmitControl{Kind: agent.PressedButton, Words: "Sign in", Typed: true})
	_, err := module.FillPassword(page, "invented", "someone@example.test")

	require.NoError(t, err)
	require.Len(t, page.Filled, 2)
	require.Equal(t, "someone@example.test", page.Filled[0].Value)
	require.Equal(t, passwordSelector, page.Filled[1].Selector)
	require.Equal(t, []string{submitMark}, page.Clicked)
}

// The public My Account page carries no form; the summary page is both where
// the sign-in starts and where a kept session shows itself.
func TestTheWeEnergiesSignInStartsAtThePageThatShowsTheForm(t *testing.T) {
	module := NewWeEnergies()
	require.Equal(t, "https://www.we-energies.com/secure/auth/l/acct/summary_accounts",
		module.SignInURL())
	require.Equal(t, module.SignInURL(), module.LandingURL())
}

// The portal it lands on, whatever case it writes its own path in.
func TestTheWeEnergiesAccountPageIsSignedInWhateverCaseItsPathIsWrittenIn(t *testing.T) {
	module := NewWeEnergies()
	for _, landing := range []string{
		"https://www.we-energies.com/accountsummary/View/AccountOverview?&acct=opaque",
		"https://www.we-energies.com/AccountSummary/View/PaymentHistory",
		"https://www.we-energies.com/secure/auth/l/acct/summary_accounts",
	} {
		page := weEnergiesSignInPage(landing, weEnergiesAccountReading())
		where, err := module.Classify(page)
		require.NoError(t, err)
		require.Equalf(t, StateSignedIn, where.State, "at %s", landing)
	}
}
