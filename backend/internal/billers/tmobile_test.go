package billers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The shapes are the portal's (docs/connectors/providers.md); every figure,
// account number and name is invented.

const tmobileFakeBAN = "999999999"

// tmobileDataset is what `bill-dataset` answers: what is owed right now, the
// current cycle's due date, and the lines under the account.
func tmobileDataset() map[string]any {
	return map[string]any{
		"accountNumber": tmobileFakeBAN,
		"arBalance":     map[string]any{"balanceDue": "120.00"},
		"currentBillCharges": map[string]any{
			"currentBillDueAmount": "120.00",
		},
		"autoPay": map[string]any{
			"easyPayStatus": true,
			"dueDate":       "2026-09-26",
			"scheduledDate": "2026-09-24",
			"amount":        "120.00",
		},
		"accountLinesInfo": []any{
			map[string]any{"msisdn": "5550100001"},
			map[string]any{"msisdn": "5550100002"},
			map[string]any{"msisdn": "5550100003"},
		},
	}
}

// tmobileCycle is one row of `getBillList`'s yearly groups.
func tmobileCycle(statementID, start, end, charges string) map[string]any {
	return map[string]any{
		"startTime":         start + "T00:00:00",
		"endTime":           end + "T00:00:00",
		"statementId":       statementID,
		"documentId":        tmobileFakeBAN + "-1234567890_" + statementID,
		"currentCharges":    charges,
		"cycleValue":        "9",
		"billingSystemCode": "no data available",
		"format":            "no data available",
	}
}

// tmobileDocument is what `billdetails` answers for one statement.
func tmobileDocument(issued, due, from, to, amount string) map[string]any {
	return map[string]any{
		"issue_date":       issued + "T00:00:00.000Z",
		"due_date":         due + "T00:00:00.000Z",
		"bill_period_from": from + "T00:00:00.000Z",
		"bill_period_to":   to + "T00:00:00.000Z",
		"invoice_id":       "INV-0000001",
		"wamountDue":       map[string]any{"amount_ex_tax": amount},
	}
}

func tmobileAt(t *testing.T, newest bool, today string) TMobileCycleAt {
	t.Helper()
	return TMobileCycleAt{
		BAN: tmobileFakeBAN, Newest: newest,
		BalanceDue: "120.00", Today: day(t, today),
	}
}

func TestTheNewestTMobileCycleIsAnOpenBillWithTheDatasetsOwnDueDate(t *testing.T) {
	notes := &Notes{}

	bill, ok := TMobileBillFromCycle(
		tmobileCycle("7", "2026-09-02", "2026-10-01", "$120.00"),
		tmobileDataset(), tmobileAt(t, true, "2026-09-21"), notes)

	require.True(t, ok)
	require.Equal(t, tmobileFakeBAN, bill.Subaccount)
	require.Equal(t, "120.00", bill.AmountDue.String())
	require.Equal(t, "USD", bill.Currency)
	require.Equal(t, "2026-09-26", bill.DueOn)
	require.Equal(t, "2026-09-24", bill.AutopayOn)
	require.Equal(t, "2026-09-02", bill.PeriodStart)
	require.Equal(t, "2026-10-01", bill.PeriodEnd)
	// The cycle list states no issue date and the reader invents none.
	require.Empty(t, bill.IssuedOn)
	require.Equal(t, "Open", bill.Status)
	require.Equal(t, tmobileFakeBAN+":7", bill.ExternalID)

	var raw tmobileRaw
	require.NoError(t, json.Unmarshal(bill.Raw, &raw))
	require.Equal(t, tmobileFakeBAN+"-1234567890_7", raw.DocumentID)
	require.Equal(t, "7", raw.StatementID)
	require.Empty(t, notes.List())
}

func TestAnOlderTMobileCycleIsAPaidBillWithTheStatementsOwnDates(t *testing.T) {
	notes := &Notes{}

	bill, ok := TMobileBillFromDocument(
		tmobileCycle("6", "2026-08-02", "2026-09-01", "$110.00"),
		tmobileDocument("2026-08-03", "2026-08-27", "2026-08-02", "2026-09-01", "110.00"),
		tmobileAt(t, false, "2026-09-21"), notes)

	require.True(t, ok)
	require.Equal(t, "110.00", bill.AmountDue.String())
	require.Equal(t, "2026-08-27", bill.DueOn)
	require.Equal(t, "2026-08-03", bill.IssuedOn)
	require.Equal(t, "2026-08-02", bill.PeriodStart)
	require.Equal(t, "2026-09-01", bill.PeriodEnd)
	// The statement states no autopay date, and the dataset's belongs to the
	// current cycle rather than to this one.
	require.Empty(t, bill.AutopayOn)
	require.Equal(t, "Paid", bill.Status)
	require.Equal(t, tmobileFakeBAN+":6", bill.ExternalID)

	var raw tmobileRaw
	require.NoError(t, json.Unmarshal(bill.Raw, &raw))
	require.Equal(t, "2026-08-03", raw.IssuedOn)
	require.Empty(t, notes.List())
}

func TestATMobileCycleWhoseAmountWillNotReadIsLeftOutWithANoteAndNeverAsZero(t *testing.T) {
	dataset := tmobileDataset()
	dataset["currentBillCharges"] = map[string]any{"currentBillDueAmount": "see statement"}
	notes := &Notes{}

	bill, ok := TMobileBillFromCycle(
		tmobileCycle("7", "2026-09-02", "2026-10-01", ""),
		dataset, tmobileAt(t, true, "2026-09-21"), notes)

	require.False(t, ok)
	require.Empty(t, bill.AmountDue, "a figure that will not read is never answered as 0")
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no readable amount")
}

func TestATMobileCycleFallsBackToItsOwnDollarPrefixedCharges(t *testing.T) {
	dataset := tmobileDataset()
	// The dataset's own figure missing is the case this falls back for: the
	// cycle's `currentCharges` is the one figure this portal writes with a
	// dollar sign in front of it.
	delete(dataset, "currentBillCharges")
	notes := &Notes{}

	bill, ok := TMobileBillFromCycle(
		tmobileCycle("7", "2026-09-02", "2026-10-01", "$115.00"),
		dataset, tmobileAt(t, true, "2026-09-21"), notes)

	require.True(t, ok)
	require.Equal(t, "115.00", bill.AmountDue.String())
	require.Empty(t, notes.List())
}

func TestATMobileStatementWithNoDueDateIsLeftOutRatherThanBilledOnAGuess(t *testing.T) {
	notes := &Notes{}

	// The cycle ends on a day the reader could have called a due date, and
	// does not: a reminder fires on the due date, and a guessed one fires on
	// the wrong day.
	_, ok := TMobileBillFromDocument(
		tmobileCycle("6", "2026-08-02", "2026-09-01", "$110.00"),
		map[string]any{"wamountDue": map[string]any{"amount_ex_tax": "110.00"}},
		tmobileAt(t, false, "2026-09-21"), notes)

	require.False(t, ok)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no due date")
	require.Contains(t, notes.List()[0], "rather than billed on a guess")
}

func TestATMobileAutopayDateIsCarriedOnlyWhileAutopayIsOn(t *testing.T) {
	off := tmobileDataset()
	off["autoPay"] = map[string]any{
		"easyPayStatus": false,
		"dueDate":       "2026-09-26",
		"scheduledDate": "2026-09-24",
	}

	bill, ok := TMobileBillFromCycle(
		tmobileCycle("7", "2026-09-02", "2026-10-01", "$120.00"),
		off, tmobileAt(t, true, "2026-09-21"), &Notes{})

	require.True(t, ok)
	require.Equal(t, "2026-09-26", bill.DueOn, "the due date stands whether or not autopay is on")
	require.Empty(t, bill.AutopayOn, "a date beside a switch that is off is no payment anybody booked")

	// The same flag as the string a service that serializes its booleans
	// sends.
	on := tmobileDataset()
	on["autoPay"] = map[string]any{
		"easyPayStatus": "true",
		"dueDate":       "2026-09-26",
		"scheduledDate": "2026-09-24",
	}
	fromString, ok := TMobileBillFromCycle(
		tmobileCycle("7", "2026-09-02", "2026-10-01", "$120.00"),
		on, tmobileAt(t, true, "2026-09-21"), &Notes{})
	require.True(t, ok)
	require.Equal(t, "2026-09-24", fromString.AutopayOn)
}

func TestTheTMobileStatusRuleIsTheBalanceTheDueDateAndBeingTheNewestCycle(t *testing.T) {
	cycle := tmobileCycle("7", "2026-09-02", "2026-10-01", "$120.00")

	for _, one := range []struct {
		name    string
		newest  bool
		balance any
		today   string
		want    string
	}{
		{"a balance above zero is open", true, "120.00", "2026-09-21", "Open"},
		{"settled, and the due date still ahead, is open", true, "0.00", "2026-09-21", "Open"},
		{"settled, and the due date today, is open", true, "0.00", "2026-09-26", "Open"},
		{"settled, and the due date passed, is paid", true, "0.00", "2026-09-27", "Paid"},
		{"a balance that will not read keeps the newest cycle open", true, "--", "2026-09-27", "Open"},
		{"a balance that is missing keeps the newest cycle open", true, nil, "2026-09-27", "Open"},
		{"a superseded cycle is paid whatever is owed", false, "120.00", "2026-09-21", "Paid"},
	} {
		t.Run(one.name, func(t *testing.T) {
			bill, ok := TMobileBillFromCycle(cycle, tmobileDataset(), TMobileCycleAt{
				BAN: tmobileFakeBAN, Newest: one.newest,
				BalanceDue: one.balance, Today: day(t, one.today),
			}, &Notes{})
			require.True(t, ok)
			require.Equal(t, one.want, bill.Status)
		})
	}
}

func TestATMobileAccountShowsUnderItsLineCountAndHasAWordWhenItHasNone(t *testing.T) {
	found := TMobileSubaccountsFromDataset(tmobileDataset())
	require.Len(t, found, 1)
	require.Equal(t, tmobileFakeBAN, found[0].ExternalID)
	require.Equal(t, "3 lines", found[0].Label)
	require.Equal(t, "••••9999", found[0].MaskedNumber)

	one := tmobileDataset()
	one["accountLinesInfo"] = []any{map[string]any{"msisdn": "5550100001"}}
	require.Equal(t, "1 line", TMobileSubaccountsFromDataset(one)[0].Label)

	none := tmobileDataset()
	none["accountLinesInfo"] = []any{}
	require.Equal(t, "Mobile", TMobileSubaccountsFromDataset(none)[0].Label)

	require.Empty(t, TMobileSubaccountsFromDataset(map[string]any{}))
}

// The module reads its own session, its own cycles and its own statement over
// a stub page: what is asserted is the decisions, not Chromium.

func TestTMobileSignsInThroughTheSharedClassifier(t *testing.T) {
	module := NewTMobile()

	browserModule, ok := Module(module).(BrowserModule)
	require.True(t, ok, "T-Mobile is signed in to in a page")

	// The protected page, not the bare form: signed out it carries the OAuth
	// parameters through to the form.
	require.Equal(t, "https://www.t-mobile.com/account", browserModule.SignInURL())
	require.Equal(t, browserModule.SignInURL(), browserModule.LandingURL())
	require.Equal(t, "https://www.t-mobile.com/bill/historical", module.AccountPage)
	require.True(t, module.AccountArea(module.AccountPage), "the account page is inside the account area")

	// The name comes from the catalogue and is spelled nowhere else.
	biller, known := domain.BillerByID(module.ID())
	require.True(t, known)
	require.Contains(t, browserModule.SignInPrompt(), biller.Name)
}

// A form wins over the account area, so the signed-out entry the redirect chain
// passes through is not read as a finished sign-in.
func TestTMobileNamesTheAccountAreaSomebodyHasSeen(t *testing.T) {
	module := NewTMobile()
	require.NotNil(t, module.AccountArea)

	for _, inside := range []string{
		"https://www.t-mobile.com/bill/historical",
		"https://www.t-mobile.com/bill/summary/" + tmobileFakeBAN,
		"https://www.t-mobile.com/my-account/dashboard",
		"https://www.t-mobile.com/account",
	} {
		require.Equal(t, StateSignedIn, StateOf(Form{}, inside, module.AccountArea).State, inside)
	}
	for _, outside := range []string{
		"https://www.t-mobile.com/",
		"https://www.t-mobile.com/signin?state=abc",
		"https://account.t-mobile.com/signin/v2/?client_id=MYTMO",
		"https://account.t-mobile.com/sap/v2/actions/choosemethod",
	} {
		require.Equal(t, StateInteractive, StateOf(Form{}, outside, module.AccountArea).State, outside)
	}

	// The form is read before the address, so the entry page showing a
	// username box is a sign-in and not an account.
	require.Equal(t, StateEmail,
		StateOf(Form{Username: true}, "https://www.t-mobile.com/account", module.AccountArea).State)
}

func TestTMobileSaysASignInIsOwedWhenTheBillingPageNeverAnswers(t *testing.T) {
	// The provider's way of saying no is to hang, so the in-page call answers
	// `timed_out` rather than a status. That — and not a missing token — is
	// what a sign-in owed is made of.
	module := NewTMobile()
	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "getBillList" {
			return map[string]any{"status": 0, "json": nil,
				"excerpt": "the call did not answer in time", "base64": "", "timed_out": true}
		}
		return answer
	})
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	// The portal's own redirect: back to the sign-in, carrying where it came
	// from in the query. The note must name the page and not the query.
	page.OnGoto = func(string) error {
		page.Location = "https://www.t-mobile.com/signin?state=abc123"
		return nil
	}
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "T-Mobile asked to sign in again", result.Reason)
	require.Contains(t, notes.List()[0], "never answered its statement list")
	require.NotContains(t, notes.List()[0], "?", "the note names the page, not the query")
}

func TestTMobileStillReadsThisMonthWhenTheHookCaughtNoToken(t *testing.T) {
	// A missed token is not a lapsed session: only one of the three endpoints
	// needs a token, so the current cycle is still readable.
	module := NewTMobile()
	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "billdetails" {
			return map[string]any{"status": 0, "json": nil,
				"excerpt": "no session token", "base64": "", "timed_out": false, "no_token": true}
		}
		return answer
	})
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page: page, Notes: notes,
		Now: func() time.Time { return day(t, "2026-09-21") },
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn, "a missed token is not a lapsed session")
	require.Len(t, result.Bills, 1)
	require.Equal(t, "2026-09-26", result.Bills[0].DueOn)
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "handed no session token")
	require.NotContains(t, strings.Join(notes.List(), "\n"), "token")
}

func TestTMobileAnswersItsThreeNewestCyclesAcrossTheYearGroups(t *testing.T) {
	module := NewTMobile()
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page:  stubTMobilePage(nil),
		Notes: notes,
		Now:   func() time.Time { return day(t, "2026-09-21") },
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, result.Bills, 3)
	require.Equal(t, []string{"2026-09-26", "2026-08-27", "2026-07-28"},
		[]string{result.Bills[0].DueOn, result.Bills[1].DueOn, result.Bills[2].DueOn})
	require.Equal(t, "Open", result.Bills[0].Status)
	require.Equal(t, "Paid", result.Bills[1].Status)
	require.Equal(t, tmobileFakeBAN, result.Bills[0].Subaccount)
	require.Contains(t, notes.List(), "T-Mobile answered 3 statements of the 4 it lists")
}

func TestTMobileLeavesTheOlderCyclesOutWhenThePageHandedOverNoToken(t *testing.T) {
	module := NewTMobile()
	notes := &Notes{}

	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "billdetails" {
			// What the in-page script answers when the app never handed this
			// page a token.
			return map[string]any{"status": 0, "json": nil, "excerpt": "no session token", "base64": "", "no_token": true}
		}
		return answer
	})
	result, err := module.FetchBills(Call{
		Page: page, Notes: notes,
		Now: func() time.Time { return day(t, "2026-09-21") },
	})

	require.NoError(t, err)
	require.Len(t, result.Bills, 1, "the current cycle is the one whose due date the dataset states")
	require.Equal(t, "2026-09-26", result.Bills[0].DueOn)
	require.Empty(t, result.Bills[0].IssuedOn)
	require.Contains(t, notes.List()[0], "could not be asked for its issue date")
	require.Contains(t, notes.List()[1], "lists 4 statements and only the current cycle states a due date")
	require.Contains(t, notes.List()[1], "the page would not let them be read")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "handed no session token")
	require.Contains(t, notes.List()[1], "2 older statements")
}

func TestARefusedTMobileCallIsASignInOwedRatherThanAFailedPull(t *testing.T) {
	module := NewTMobile()
	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "getBillList" {
			return map[string]any{"status": 401, "json": nil, "excerpt": "Unauthorized", "base64": ""}
		}
		return answer
	})

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "T-Mobile refused the kept session", result.Reason)
}

func TestTMobileListsTheBilledAccountTheDatasetNames(t *testing.T) {
	module := NewTMobile()
	notes := &Notes{}

	found, err := module.Subaccounts(Call{Page: stubTMobilePage(nil), Notes: notes})

	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, tmobileFakeBAN, found[0].ExternalID)
	require.Equal(t, "3 lines", found[0].Label)
	require.Contains(t, notes.List(), "T-Mobile lists 1 billed account")
}

func TestATMobileStatementThatIsNotAPDFIsNoDocumentAndANote(t *testing.T) {
	module := NewTMobile()
	bill := Bill{Subaccount: tmobileFakeBAN, ExternalID: tmobileFakeBAN + ":7"}
	bill.Raw, _ = json.Marshal(tmobileRaw{
		DocumentID: tmobileFakeBAN + "-1234567890_7", StatementID: "7", IssuedOn: "2026-09-02",
	})
	notes := &Notes{}

	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "billdetails-pdf" {
			// A portal whose session has lapsed answering its own sign-in page
			// under HTTP 200.
			return map[string]any{
				"status": 200, "json": nil, "excerpt": "",
				"base64": base64.StdEncoding.EncodeToString([]byte("<html>Sign in</html>")),
			}
		}
		return answer
	})

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestATMobileStatementIsAPDFNamedForItsAccountsLastFourAndItsIssueDate(t *testing.T) {
	module := NewTMobile()
	bill := Bill{Subaccount: tmobileFakeBAN, ExternalID: tmobileFakeBAN + ":7", DueOn: "2026-09-26"}
	bill.Raw, _ = json.Marshal(tmobileRaw{
		DocumentID: tmobileFakeBAN + "-1234567890_7", StatementID: "7", IssuedOn: "2026-09-02",
	})

	document, err := module.FetchDocument(Call{Page: stubTMobilePage(nil), Notes: &Notes{}}, bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "tmobile-9999-2026-09-02.pdf", document.Filename)
	require.True(t, len(document.Bytes) > 0)
}

func TestATMobileStatementIsNotAskedForWhenThePageWasHandedNoToken(t *testing.T) {
	module := NewTMobile()
	bill := Bill{Subaccount: tmobileFakeBAN, ExternalID: tmobileFakeBAN + ":7"}
	bill.Raw, _ = json.Marshal(tmobileRaw{DocumentID: tmobileFakeBAN + "-1234567890_7"})
	notes := &Notes{}

	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "billdetails-pdf" {
			return map[string]any{"status": 0, "json": nil, "excerpt": "no session token", "base64": "", "no_token": true}
		}
		return answer
	})

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "could not be fetched")
	require.Contains(t, notes.Traces()[0], "handed no session token")
}

func TestANewestTMobileCycleWhoseBalanceWillNotReadStaysOpenWithANote(t *testing.T) {
	cycle := tmobileCycle("7", "2026-09-02", "2026-10-01", "$120.00")
	notes := &Notes{}

	// Past its due date: only the unreadable balance can keep it open.
	bill, ok := TMobileBillFromCycle(cycle, tmobileDataset(), TMobileCycleAt{
		BAN: tmobileFakeBAN, Newest: true, BalanceDue: "--", Today: day(t, "2026-09-27"),
	}, notes)

	require.True(t, ok)
	require.Equal(t, "Open", bill.Status)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "balance due did not read")
}

func TestTheNewestTMobileCycleCarriesTheIssueDateItsOwnStatementStates(t *testing.T) {
	var asked []string
	page := stubTMobilePage(nil)
	evaluate := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == tmobileDetailsCall {
			documentID, _ := arg.(map[string]any)["extra"].(map[string]any)["documentId"].(string)
			asked = append(asked, documentID)
		}
		return evaluate(script, arg)
	}

	result, err := NewTMobile().FetchBills(Call{
		Page: page, Notes: &Notes{},
		Now: func() time.Time { return day(t, "2026-09-21") },
	})

	require.NoError(t, err)
	require.Equal(t, tmobileFakeBAN+"-1234567890_7", asked[0], "the newest cycle's statement is asked for too")
	require.Equal(t, "2026-09-03", result.Bills[0].IssuedOn)
	require.Equal(t, "2026-09-26", result.Bills[0].DueOn, "the due date is still the dataset's")
	var raw tmobileRaw
	require.NoError(t, json.Unmarshal(result.Bills[0].Raw, &raw))
	require.Equal(t, "2026-09-03", raw.IssuedOn)
}

func TestTheNewestTMobileCycleIsStillABillWhenItsStatementWillNotAnswer(t *testing.T) {
	page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
		if call == "billdetails" {
			return map[string]any{"status": 500, "json": nil, "excerpt": "Internal error", "base64": ""}
		}
		return answer
	})
	notes := &Notes{}

	result, err := NewTMobile().FetchBills(Call{
		Page: page, Notes: notes,
		Now: func() time.Time { return day(t, "2026-09-21") },
	})

	require.NoError(t, err)
	require.Len(t, result.Bills, 1)
	require.Equal(t, "2026-09-26", result.Bills[0].DueOn)
	require.Empty(t, result.Bills[0].IssuedOn)
	require.Contains(t, notes.List()[0], "answered HTTP 500 with no issue date")
}

func TestATMobilePullThatReadsNoStatementsSaysWhy(t *testing.T) {
	for _, one := range []struct {
		name    string
		call    string
		answer  map[string]any
		wantHas string
	}{
		{"a statement list that will not read", "getBillList",
			tmobileBody(map[string]any{"responseCode": "500"}), "no statements were read"},
		{"no word of what is owed", "bill-dataset",
			tmobileBody(map[string]any{"errors": []any{}}), "no statements were read"},
		{"no account number on either answer", "both",
			nil, "named no account number; its statements are left out"},
	} {
		t.Run(one.name, func(t *testing.T) {
			page := stubTMobilePage(func(call string, answer map[string]any) map[string]any {
				if one.call == "both" {
					if body, ok := answer["json"].(map[string]any); ok {
						delete(body, "accountNumber")
						if set, ok := body["briteBillDataSet"].(map[string]any); ok {
							delete(set, "accountNumber")
						}
					}
					return answer
				}
				if call == one.call {
					return one.answer
				}
				return answer
			})
			notes := &Notes{}

			result, err := NewTMobile().FetchBills(Call{Page: page, Notes: notes})

			require.NoError(t, err)
			require.False(t, result.NeedsSignIn)
			require.Empty(t, result.Bills)
			require.Len(t, notes.List(), 1)
			require.Contains(t, notes.List()[0], one.wantHas)
		})
	}
}

func TestEveryTMobileCallIsHandedShapesAPageCanTake(t *testing.T) {
	module := NewTMobile()
	page := stubTMobilePage(nil)

	_, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.NotEmpty(t, page.Args, "the portal's calls all go through Evaluate")
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
	require.Equal(t, []string{tmobileSessionHook}, page.Scripts,
		"the hook is an init script, because the app calls from a src-less iframe")
}

// The four cycles the stub portal lists, newest first, with the due date each
// one's statement carries. Invented throughout.
var tmobileStubCycles = []struct{ id, start, end, issued, due, charges string }{
	{"7", "2026-09-02", "2026-10-01", "2026-09-03", "2026-09-26", "$120.00"},
	{"6", "2026-08-02", "2026-09-01", "2026-08-03", "2026-08-27", "$110.00"},
	{"5", "2026-07-02", "2026-08-01", "2026-07-03", "2026-07-28", "$110.00"},
	{"4", "2026-06-02", "2026-07-01", "2026-06-03", "2026-06-27", "$100.00"},
}

// `rewrite` is how a test makes one call answer differently — a 401, a page
// that was handed no token, a document that is not a PDF — without restating
// the others.
func stubTMobilePage(rewrite func(call string, answer map[string]any) map[string]any) *browser.StubPage {
	page := &browser.StubPage{Location: tmobileBilling}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		asked, _ := arg.(map[string]any)
		call := ""
		switch script {
		case plainPageCall:
			call = "getBillList"
			if asked["url"] == tmobileDatasetURL {
				call = "bill-dataset"
			}
		case tmobileDetailsCall:
			call = "billdetails"
			if asked["read"] == "bytes" {
				call = "billdetails-pdf"
			}
		default:
			return nil, nil
		}
		answer := tmobileStubAnswer(call, asked)
		if rewrite != nil {
			answer = rewrite(call, answer)
		}
		return answer, nil
	}
	return page
}

func tmobileStubAnswer(call string, asked map[string]any) map[string]any {
	switch call {
	case "getBillList":
		// Grouped by year and listed out of order on purpose: the module
		// flattens the groups and sorts them rather than trusting the portal's
		// own order.
		return tmobileBody(map[string]any{
			"accountNumber": tmobileFakeBAN,
			"responseCode":  "100",
			"getBillList": []any{
				map[string]any{"year": "2026", "bills": []any{
					tmobileStubCycle(1), tmobileStubCycle(3),
				}},
				map[string]any{"year": "2026", "bills": []any{
					tmobileStubCycle(0), tmobileStubCycle(2),
				}},
			},
		})
	case "bill-dataset":
		return tmobileBody(map[string]any{"briteBillDataSet": tmobileDataset()})
	case "billdetails":
		wanted, _ := asked["extra"].(map[string]any)["documentId"].(string)
		for _, one := range tmobileStubCycles {
			if tmobileFakeBAN+"-1234567890_"+one.id != wanted {
				continue
			}
			return tmobileBody(tmobileDocument(
				one.issued, one.due, one.start, one.end, "110.00"))
		}
		return map[string]any{"status": 404, "json": nil, "excerpt": "no such statement", "base64": ""}
	case "billdetails-pdf":
		return map[string]any{
			"status": 200, "json": nil, "excerpt": "",
			"base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invented")),
		}
	}
	return map[string]any{"status": 400, "json": nil, "excerpt": "unknown call", "base64": ""}
}

func tmobileStubCycle(at int) map[string]any {
	one := tmobileStubCycles[at]
	return tmobileCycle(one.id, one.start, one.end, one.charges)
}

// tmobileBody is one answer in the shape the in-page call returns. These
// endpoints answer their shapes at the top level, with no envelope.
func tmobileBody(body map[string]any) map[string]any {
	return map[string]any{"status": 200, "json": body, "excerpt": "", "base64": ""}
}
