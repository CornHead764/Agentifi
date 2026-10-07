package billers

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// The shapes are the portal's (docs/connectors/providers.md); every value is
// invented.

const (
	rsyncNetTestAccount = "100000"
	// The Services block, the Balance block and the table's text, as the page
	// reading flattens the billing page into one string.
	rsyncNetTestText = "Account Manager\n" +
		"Services\n" +
		"100 GB Offsite Filesystem\n" +
		"$50.00 per year\n" +
		"Next billing: June 1, 2027\n" +
		"Balance\n" +
		"Amount Due: $0.00\n" +
		"Date Description Amount\n"
	rsyncNetTestBalance = "Amount Due: $0.00"
)

// Newest first, as the portal sorts it, with its own header row at the top: a
// payment is the record of the year's charge, and the refund is money coming
// back.
func rsyncNetTestRows() []RsyncNetRow {
	return []RsyncNetRow{
		{Date: "Date", Description: "Description", Amount: "Amount"},
		{Date: "June 1, 2026", Description: "Payment- Thank you!", Amount: "50.00"},
		{Date: "June 1, 2025", Description: "Payment- Thank you!", Amount: "45.00"},
		{Date: "August 2, 2024", Description: "Refund- overlapping term", Amount: "45.00"},
		{Date: "June 1, 2024", Description: "Payment- Thank you!", Amount: "40.00"},
		{Date: "June 1, 2023", Description: "Payment- Thank you!", Amount: "35.00"},
	}
}

func rsyncNetTestRead() RsyncNetPage {
	return RsyncNetPage{
		Text:    rsyncNetTestText,
		Balance: rsyncNetTestBalance,
		Rows:    rsyncNetTestRows(),
	}
}

func rsyncNetNotes(notes *Notes) string { return strings.Join(notes.List(), "\n") }

func TestTheRsyncNetServicesBlockAnswersTheChargeItsPeriodAndTheOneDateItStates(t *testing.T) {
	service := RsyncNetServiceFrom(rsyncNetTestRead())

	require.Equal(t, "Offsite Filesystem", service.Description)
	require.Equal(t, "50.00", service.Amount.String())
	require.Equal(t, "year", service.Every)
	require.Equal(t, "2027-06-01", service.DueOn)
}

func TestTheRsyncNetOpenBillIsTheRecurringChargeOnItsNextBillingDay(t *testing.T) {
	bills := RsyncNetBillsFromPage(rsyncNetTestRead(), rsyncNetTestAccount, &Notes{})

	require.NotEmpty(t, bills)
	open := bills[0]
	require.Equal(t, "Open", open.Status)
	require.Equal(t, "2027-06-01", open.DueOn)
	require.Equal(t, "50.00", open.AmountDue.String())
	require.Equal(t, "USD", open.Currency)
	require.Equal(t, rsyncNetTestAccount, open.Subaccount)
	require.Equal(t, rsyncNetTestAccount+":2027-06-01", open.ExternalID)
	// The charge has not been taken, so there is no receipt to ask for and no
	// issue date the portal ever wrote.
	require.Equal(t, "", open.IssuedOn)
	require.Empty(t, open.Raw)
}

// "Amount Due: $0.00" beside a charge eleven months away means nothing is
// overdue. It must never become a bill for nothing.
func TestARsyncNetZeroBalanceIsNeverFiledAsAnAmount(t *testing.T) {
	read := rsyncNetTestRead()
	read.Text = strings.Replace(read.Text, "$50.00 per year\n", "", 1)
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	for _, one := range bills {
		require.NotEqual(t, "Open", one.Status)
		require.NotEqual(t, "0.00", one.AmountDue.String())
	}
	require.Contains(t, rsyncNetNotes(notes), "owes nothing today")
}

func TestTheRsyncNetBalanceStandsInOnlyForAChargeThatCouldNotBeReadAndIsOwed(t *testing.T) {
	read := rsyncNetTestRead()
	read.Text = strings.Replace(read.Text, "$50.00 per year\n", "", 1)
	read.Balance = "Amount Due: $90.00"
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	require.NotEmpty(t, bills)
	require.Equal(t, "Open", bills[0].Status)
	require.Equal(t, "90.00", bills[0].AmountDue.String())
	require.Equal(t, "2027-06-01", bills[0].DueOn)
	require.Contains(t, rsyncNetNotes(notes), "the balance due is reported")
}

func TestARsyncNetChargeWithNoNextBillingDayIsNoOpenBillAtAll(t *testing.T) {
	read := rsyncNetTestRead()
	read.Text = strings.Replace(read.Text, "Next billing: June 1, 2027\n", "", 1)
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	for _, one := range bills {
		require.Equal(t, "Paid", one.Status)
	}
	require.Contains(t, rsyncNetNotes(notes), "no next billing date")
}

// Each wording of the Services block's date the portal may use. In "Next
// Billing Date:" the word "Date" must not be read as the month.
func TestEveryWordingOfTheRsyncNetNextBillingDayIsTheOpenBillsDueDate(t *testing.T) {
	for said, expected := range map[string]string{
		"Next billing: June 1, 2027":          "2027-06-01",
		"Next Billing Date: October 19, 2026": "2026-10-19",
		"Next Billing Date: 10/19/2026":       "2026-10-19",
		"Next billing date 2026-10-19":        "2026-10-19",
		"Next billing: Sept 4, 2026":          "2026-09-04",
		"Next Billing Date: Sept. 4, 2026":    "2026-09-04",
		"Next billing: Oct. 19 2026":          "2026-10-19",
	} {
		read := rsyncNetTestRead()
		read.Text = strings.Replace(read.Text, "Next billing: June 1, 2027", said, 1)

		bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, &Notes{})

		require.NotEmptyf(t, bills, "reading %q", said)
		require.Equalf(t, "Open", bills[0].Status, "reading %q", said)
		require.Equalf(t, expected, bills[0].DueOn, "reading %q", said)
		require.Equalf(t, "50.00", bills[0].AmountDue.String(), "reading %q", said)
	}
}

func TestARsyncNetServicesBlockWhoseDateCannotBeReadSaysSoInItsNote(t *testing.T) {
	read := rsyncNetTestRead()
	read.Text = strings.Replace(read.Text, "Next billing: June 1, 2027", "Next billing: on renewal", 1)
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	for _, one := range bills {
		require.Equal(t, "Paid", one.Status)
	}
	require.Contains(t, rsyncNetNotes(notes), "states no next billing date")
	require.Contains(t, rsyncNetNotes(notes), "no upcoming charge is reported")
}

// An overdue balance beside a readable next charge is owed now, not at the
// next charge: it is a bill of its own on the day the Balance block states.
func TestAnOwedRsyncNetBalanceBesideTheNextChargeIsItsOwnOpenBillOnItsStatedDay(t *testing.T) {
	for _, balance := range []string{
		"Amount Due: $40.00\nDue Date: August 30, 2026",
		"Amount Due: $40.00\nDue by 8/30/2026",
		"Amount Due: $40.00 Payable by 2026-08-30",
	} {
		read := rsyncNetTestRead()
		read.Balance = balance
		notes := &Notes{}

		bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

		require.GreaterOrEqualf(t, len(bills), 2, "reading %q", balance)
		require.Equal(t, "Open", bills[0].Status)
		require.Equal(t, "2026-08-30", bills[0].DueOn)
		require.Equal(t, "40.00", bills[0].AmountDue.String())
		require.Equal(t, rsyncNetTestAccount+":2026-08-30", bills[0].ExternalID)
		require.Equal(t, "Open", bills[1].Status)
		require.Equal(t, "2027-06-01", bills[1].DueOn)
		require.Equal(t, "50.00", bills[1].AmountDue.String())
		require.Contains(t, rsyncNetNotes(notes), "owed, due 2026-08-30")
	}
}

// The next billing date is the charge's day, not the balance's; a balance with
// no day of its own is said and not filed under a day made up for it.
func TestAnOwedRsyncNetBalanceWithNoStatedDayIsANoteAndNotABill(t *testing.T) {
	read := rsyncNetTestRead()
	read.Balance = "Amount Due: $40.00"
	read.Text = strings.Replace(read.Text, "Amount Due: $0.00", "Amount Due: $40.00", 1)
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	var open []Bill
	for _, one := range bills {
		if one.Status == "Open" {
			open = append(open, one)
		}
	}
	require.Len(t, open, 1)
	require.Equal(t, "2027-06-01", open[0].DueOn)
	require.Equal(t, "50.00", open[0].AmountDue.String())
	require.Contains(t, rsyncNetNotes(notes), "$40.00 owed today and states no day it is due")
}

func TestAnOwedRsyncNetBalanceDueOnTheNextBillingDayIsOneBillNotTwo(t *testing.T) {
	read := rsyncNetTestRead()
	read.Balance = "Amount Due: $40.00\nDue Date: June 1, 2027"
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	require.Equal(t, "Open", bills[0].Status)
	require.Equal(t, "2027-06-01", bills[0].DueOn)
	require.Equal(t, "90.00", bills[0].AmountDue.String())
	require.Equal(t, "Paid", bills[1].Status)
	require.Contains(t, rsyncNetNotes(notes), "reported as one bill")
}

// A balance that is the upcoming charge itself, stated for the same day, is
// that charge once and not twice its amount.
func TestARsyncNetBalanceThatIsTheNextChargeIsNotCountedTwice(t *testing.T) {
	read := rsyncNetTestRead()
	read.Balance = "Amount Due: $50.00\nDue Date: 06/01/2027"
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	require.Equal(t, "2027-06-01", bills[0].DueOn)
	require.Equal(t, "50.00", bills[0].AmountDue.String())
	require.Equal(t, "Paid", bills[1].Status)
	require.Contains(t, rsyncNetNotes(notes), "reported once")
}

func TestAnOwedRsyncNetBalanceIsReportedEvenWhenNoNextBillingDayIsStated(t *testing.T) {
	read := rsyncNetTestRead()
	read.Text = strings.Replace(read.Text, "Next billing: June 1, 2027\n", "", 1)
	read.Balance = "Amount Due: $40.00\nDue Date: August 30, 2026"
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	require.Equal(t, "Open", bills[0].Status)
	require.Equal(t, "2026-08-30", bills[0].DueOn)
	require.Equal(t, "40.00", bills[0].AmountDue.String())
	require.Equal(t, "Paid", bills[1].Status)
	require.Contains(t, rsyncNetNotes(notes), "states no next billing date")
}

// The paid history is capped at the constant, and the header row is not a
// transaction however it is counted.
func TestTheRsyncNetPaidHistoryIsCappedAtItsConstantNewestFirst(t *testing.T) {
	bills := RsyncNetBillsFromPage(rsyncNetTestRead(), rsyncNetTestAccount, &Notes{})

	paid := bills[1:]
	require.Len(t, paid, rsyncNetBillsToRead)
	require.Equal(t, []string{"2026-06-01", "2025-06-01", "2024-06-01"},
		[]string{paid[0].DueOn, paid[1].DueOn, paid[2].DueOn})
	for _, one := range paid {
		require.Equal(t, "Paid", one.Status)
		require.NotEmpty(t, one.AmountDue)
	}
}

// This portal writes no invoice row, so the payment is the charge's only record and is filed as the paid bill; money
// coming back settles nothing and is left out.
func TestARsyncNetPaymentRowIsThePaidBillAndARefundRowIsNot(t *testing.T) {
	bills := RsyncNetBillsFromPage(rsyncNetTestRead(), rsyncNetTestAccount, &Notes{})

	var days []string
	for _, one := range bills {
		days = append(days, one.DueOn)
	}
	require.Contains(t, days, "2026-06-01")
	require.NotContains(t, days, "2024-08-02")
	require.False(t, rsyncNetIsMoneyBack("Payment- Thank you!"))
	require.True(t, rsyncNetIsMoneyBack("Refund- overlapping term"))
	// A card is not a credit: the word the portal writes for the instrument
	// must not read as money coming back.
	require.False(t, rsyncNetIsMoneyBack("Payment- credit card ending 0000"))
	require.True(t, rsyncNetIsMoneyBack("Credit applied to account"))
}

func TestEachPaidRsyncNetBillCarriesItsOwnDayAsBothDatesAndItsReceipt(t *testing.T) {
	bills := RsyncNetBillsFromPage(rsyncNetTestRead(), rsyncNetTestAccount, &Notes{})

	newest := bills[1]
	require.Equal(t, "2026-06-01", newest.IssuedOn)
	require.Equal(t, "2026-06-01", newest.DueOn)
	require.Equal(t, rsyncNetTestAccount+":2026-06-01", newest.ExternalID)

	var raw rsyncNetRaw
	require.NoError(t, json.Unmarshal(newest.Raw, &raw))
	require.Equal(t, "2026-06-01", raw.ReceiptOn)
}

func TestARsyncNetRowWithNoReadableAmountIsLeftOutWithANote(t *testing.T) {
	read := rsyncNetTestRead()
	read.Rows = []RsyncNetRow{{Date: "June 1, 2026", Description: "Payment- Thank you!", Amount: "--"}}
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	for _, one := range bills {
		require.NotEqual(t, "Paid", one.Status)
	}
	require.Contains(t, rsyncNetNotes(notes), "no readable amount")
	require.Contains(t, rsyncNetNotes(notes), "2026-06-01")
}

func TestTheRsyncNetTableHeaderIsNeverABillAndSaysNothingAboutIt(t *testing.T) {
	read := rsyncNetTestRead()
	read.Rows = []RsyncNetRow{{Date: "Date", Description: "Description", Amount: "Amount"}}
	notes := &Notes{}

	bills := RsyncNetBillsFromPage(read, rsyncNetTestAccount, notes)

	require.Len(t, bills, 1, "the open bill and nothing from the table")
	require.NotContains(t, rsyncNetNotes(notes), "no readable amount")
}

func TestARsyncNetLongFormDayIsReadAndAnythingElseIsNotGuessedAt(t *testing.T) {
	for written, expected := range map[string]string{
		"March 14, 2027":    "2027-03-14",
		"Mar 14, 2027":      "2027-03-14",
		"Sept 4, 2026":      "2026-09-04",
		"Sept. 4, 2026":     "2026-09-04",
		"January 2 2027":    "2027-01-02",
		"2027-03-14":        "2027-03-14",
		"3/14/2027":         "2027-03-14",
		"Smarch 14, 2027":   "",
		"next Tuesday":      "",
		"14 March 2027":     "",
		"March the 14th":    "",
		"Next billing soon": "",
	} {
		require.Equalf(t, expected, DayIn(written), "reading %q", written)
	}
}

// The portal's own reading of a signed-out page: it serves the form at the
// billing page's own address, so what says so is the reading and not the URL.
func TestARsyncNetSignInFormAtTheBillingAddressIsNotSignedIn(t *testing.T) {
	require.False(t, RsyncNetPage{Text: "Login\nUsername\nPassword\n"}.SignedIn())
	require.True(t, rsyncNetTestRead().SignedIn())
	require.True(t, RsyncNetPage{Balance: rsyncNetTestBalance}.SignedIn())
	// A header row is not evidence: signed out, this portal serves its form at
	// the billing address as a one-row table.
	require.False(t, RsyncNetPage{Rows: []RsyncNetRow{{Date: "Date"}}}.SignedIn())
	require.True(t, RsyncNetPage{Rows: []RsyncNetRow{{Date: "March 14, 2027"}}}.SignedIn(),
		"a row carrying a day this portal writes could only be the transactions table")
}

func TestRsyncNetPrefersTheDashboardIdAndLabelsItAfterTheService(t *testing.T) {
	notes := &Notes{}

	found := RsyncNetSubaccountsFrom([]string{rsyncNetTestAccount}, rsyncNetTestRead(), notes)

	require.Len(t, found, 1)
	require.Equal(t, rsyncNetTestAccount, found[0].ExternalID)
	require.Equal(t, "Offsite Filesystem", found[0].Label)
	require.Equal(t, "••••0000", found[0].MaskedNumber)
	require.Contains(t, rsyncNetNotes(notes), "••••0000")
}

func TestRsyncNetKeepsOneBilledAccountWhenTheDashboardNamesNoIdAndSaysSo(t *testing.T) {
	notes := &Notes{}

	found := RsyncNetSubaccountsFrom(nil, rsyncNetTestRead(), notes)

	require.Len(t, found, 1)
	require.Equal(t, rsyncNetOneAccount, found[0].ExternalID)
	require.Equal(t, "Offsite Filesystem", found[0].Label)
	require.Equal(t, "", found[0].MaskedNumber)
	require.Contains(t, rsyncNetNotes(notes), "named no account id")
}

func TestARsyncNetBilledAccountWithNoServicesBlockIsStillNamedHonestly(t *testing.T) {
	found := RsyncNetSubaccountsFrom([]string{rsyncNetTestAccount}, RsyncNetPage{}, &Notes{})

	require.Len(t, found, 1)
	require.Equal(t, "rsync.net", found[0].Label)
}

func TestRsyncNetReadsItsBillsFromTheBillingPageItAsksForByName(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page: page, Notes: notes, Subaccounts: []string{rsyncNetTestAccount},
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, result.Bills, rsyncNetBillsToRead+1)
	require.Equal(t, []string{rsyncNetBilling}, page.Visited)
	require.Contains(t, rsyncNetNotes(notes), "rsync.net answered 4 bills")
}

// A pull that named no billed account reads the dashboard for the id rather
// than inventing one, and lands on the billing page afterwards so the receipts
// are fetched from it.
func TestRsyncNetReadsTheDashboardForTheIdOnlyWhenThePullNamedNone(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.Equal(t, []string{rsyncNetDashboard, rsyncNetBilling}, page.Visited)
	require.Equal(t, rsyncNetTestAccount, result.Bills[0].Subaccount)
}

func TestRsyncNetSaysASignInIsOwedWhenTheBillingAddressServesTheForm(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	page.OnEvaluate = rsyncNetEvaluate(func(script string, answer any) any {
		if script == rsyncNetBillingScript {
			return map[string]any{
				"text": "Login\nUsername\nPassword\nLogin", "balance": "", "rows": []any{},
			}
		}
		return answer
	})
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page: page, Notes: notes, Subaccounts: []string{rsyncNetTestAccount},
	})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "rsync.net asked to sign in again", result.Reason)
	require.Contains(t, rsyncNetNotes(notes), "served its sign-in form")
}

func TestRsyncNetSaysASignInIsOwedWhenTheProfileLandsSomewhereElse(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	page.OnGoto = func(string) error {
		page.Location = rsyncNetHome + "/index.html?from=am"
		return nil
	}
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page: page, Notes: notes, Subaccounts: []string{rsyncNetTestAccount},
	})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Contains(t, rsyncNetNotes(notes), "the profile is not signed in")
	require.NotContains(t, rsyncNetNotes(notes), "?")
}

func TestRsyncNetAnswersNoBrowserAsTheSignInItActuallyNeeds(t *testing.T) {
	module := NewRsyncNet()

	result, err := module.FetchBills(Call{Notes: &Notes{}})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Contains(t, result.Reason, "this pull opened none")
}

func TestARsyncNetReceiptIsAskedForByTheTransactionsOwnISODay(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	bill := Bill{Subaccount: rsyncNetTestAccount, ExternalID: rsyncNetTestAccount + ":2026-06-01"}
	bill.Raw, _ = json.Marshal(rsyncNetRaw{ReceiptOn: "2026-06-01"})

	document, err := module.FetchDocument(Call{Page: page, Notes: &Notes{}}, bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "rsync-net-0000-2026-06-01.pdf", document.Filename)
	require.Len(t, page.Args, 1)
	require.Equal(t, rsyncNetReceipt+"?sd=2026-06-01", page.Args[0].(map[string]any)["url"])
}

func TestARsyncNetReceiptThatIsNotAPDFIsNoDocumentAndANote(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	page.OnEvaluate = rsyncNetEvaluate(func(script string, answer any) any {
		if script == plainPageCall {
			// What a lapsed session answers: the sign-in page, under HTTP 200.
			return map[string]any{
				"status": 200, "type": "text/html",
				"base64": base64.StdEncoding.EncodeToString([]byte("<html>Login</html>")),
			}
		}
		return answer
	})
	bill := Bill{Subaccount: rsyncNetTestAccount, ExternalID: rsyncNetTestAccount + ":2026-06-01"}
	bill.Raw, _ = json.Marshal(rsyncNetRaw{ReceiptOn: "2026-06-01"})
	notes := &Notes{}

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, rsyncNetNotes(notes), "not a PDF")
}

// The open bill has no transaction behind it, so it has no receipt to ask for.
func TestTheOpenRsyncNetBillAsksForNoReceiptAtAll(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()
	notes := &Notes{}

	document, err := module.FetchDocument(Call{Page: page, Notes: notes},
		Bill{Subaccount: rsyncNetTestAccount, DueOn: "2027-06-01", Status: "Open"})

	require.NoError(t, err)
	require.Nil(t, document)
	require.Empty(t, page.Args)
	require.Empty(t, notes.List())
}

func TestTheRsyncNetSignInAndTheLandingAreBothTheBillingPage(t *testing.T) {
	module := NewRsyncNet()

	require.Equal(t, "https://www.rsync.net/am/billing_info.html", module.SignInURL())
	require.Equal(t, module.SignInURL(), module.LandingURL())
}

func TestEveryRsyncNetCallIsHandedShapesAPageCanTake(t *testing.T) {
	module := NewRsyncNet()
	page := stubRsyncNetPage()

	found, err := module.Subaccounts(Call{Page: page, Notes: &Notes{}})
	require.NoError(t, err)
	require.Len(t, found, 1)

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})
	require.NoError(t, err)
	for _, bill := range result.Bills {
		_, err := module.FetchDocument(Call{Page: page, Notes: &Notes{}}, bill)
		require.NoError(t, err)
	}

	require.NotEmpty(t, page.Args)
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
}

func stubRsyncNetPage() *browser.StubPage {
	page := &browser.StubPage{Location: rsyncNetBilling}
	page.OnEvaluate = rsyncNetEvaluate(nil)
	return page
}

// rsyncNetEvaluate answers each of the module's three scripts. `rewrite` is how
// one test makes a single script answer differently.
func rsyncNetEvaluate(rewrite func(script string, answer any) any) func(string, any) (any, error) {
	return func(script string, arg any) (any, error) {
		var answer any
		switch script {
		case rsyncNetBillingScript:
			answer = map[string]any{
				"text": rsyncNetTestText, "balance": rsyncNetTestBalance,
				"rows": rsyncNetStubRows(),
			}
		case rsyncNetDashboardScript:
			// The heading cell is dropped in the page, so what comes back is
			// the value alone.
			answer = []any{rsyncNetTestAccount}
		case plainPageCall:
			answer = map[string]any{
				"status": 200, "type": "application/pdf",
				"base64": base64.StdEncoding.EncodeToString([]byte("%PDF-1.7 invented")),
			}
		}
		if rewrite != nil {
			answer = rewrite(script, answer)
		}
		return answer, nil
	}
}

func rsyncNetStubRows() []any {
	out := make([]any, 0, len(rsyncNetTestRows()))
	for _, row := range rsyncNetTestRows() {
		out = append(out, map[string]any{
			"date": row.Date, "description": row.Description, "amount": row.Amount,
		})
	}
	return out
}
