package billers

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Every portal, account number, name and figure here is invented, written to
// the shape MyChart's billing pages are described as showing.

const myChartTestRoot = "https://mychart.examplehealth.example/MyChart"

func aimedMyChart(t *testing.T) *MyChart {
	t.Helper()
	aimed, ok := NewMyChart().WithSite(myChartTestRoot + "/Home/").(*MyChart)
	require.True(t, ok)
	return aimed
}

func TestMyChartIsAimedAtThePortalsBillingSummary(t *testing.T) {
	unaimed := NewMyChart()
	require.Empty(t, unaimed.SignInURL())
	require.Empty(t, unaimed.SiteHome())
	require.Same(t, Module(unaimed), unaimed.WithSite("https://192.0.2.7/MyChart"), "an IP address aims nothing")

	aimed := aimedMyChart(t)
	require.Equal(t, myChartTestRoot+"/Billing/Summary", aimed.SignInURL())
	require.Equal(t, myChartTestRoot+"/Billing/Summary", aimed.LandingURL())
	require.Equal(t, myChartTestRoot, aimed.SiteHome())
	require.Empty(t, unaimed.SiteHome(), "aiming answers a copy")
}

func TestMyChartsAccountAreaIsThePortalPastItsSignIn(t *testing.T) {
	for address, inside := range map[string]bool{
		myChartTestRoot + "/Billing/Summary":                     true,
		myChartTestRoot + "/billing/details?ID=abc":              true,
		myChartTestRoot + "/Home/":                               true,
		myChartTestRoot + "/Authentication/Login?postloginurl=x": false,
		myChartTestRoot + "/Authentication/SecondaryValidation":  false,
		myChartTestRoot + "/Signup":                              false,
		myChartTestRoot + "/":                                    false,
		"https://mychart.examplehealth.example/Other/Billing":    false,
		"http://mychart.examplehealth.example/MyChart/Home":      false,
		"https://mychart.elsewhere.example/MyChart/Home":         false,
	} {
		require.Equalf(t, inside, MyChartInside(myChartTestRoot, address), "%s", address)
	}
	require.True(t, MyChartInside("https://mychart.examplehealth.example", "https://mychart.examplehealth.example/Billing/Summary"))
	require.False(t, MyChartInside("https://mychart.examplehealth.example", "https://mychart.examplehealth.example/Authentication/Login"))
}

func TestMyChartBillingAccountsAreTheSummaryCardsThatPrintANumber(t *testing.T) {
	notes := &Notes{}
	found := MyChartSubaccounts([]MyChartCard{
		{Text: "Example Medical Group\nGuarantor #: 0000-1234\nPatients included: Sam\nAmount due $150.00\nPay now\nView account"},
		{Text: "Account 00005678\nAmount due $0.00\nView account"},
		{Text: "Example Medical Group\nGuarantor #: 00001234\nView account"},
		{Text: "Example Clinic\nYour balance $12.00\nView account", Account: "99009999"},
		{Text: "Something went wrong loading this account"},
	}, notes)
	require.Equal(t, []Subaccount{
		{ExternalID: "00001234", Label: "Billing account ****1234 · Example Medical Group", MaskedNumber: domain.MaskAccount("00001234")},
		{ExternalID: "00005678", Label: "Billing account ****5678", MaskedNumber: domain.MaskAccount("00005678")},
		{ExternalID: "99009999", Label: "Billing account ****9999 · Example Clinic", MaskedNumber: domain.MaskAccount("99009999")},
	}, found)
	require.Len(t, notes.List(), 1)
}

// A card read too widely carries the portal's frame above it; the name is the
// card's own, beside the number, and never a skip link or a menu.
func TestAMyChartAccountIsNamedByItsCardAndNeverByThePortalsFrame(t *testing.T) {
	for text, want := range map[string]string{
		"Skip navigation to main content\nYour Menu\nBilling Summary\nGuarantor #00009012\nPay now": "",
		"Skip navigation to main content\nMenu\nBilling Account Summary\nExample Health\n" +
			"Hospital Services\nGuarantor #00009012\nPatients included: You": "Example Health",
		"Need help? Call us 8 to 5\nGuarantor #00009012\nExample Physicians\nView account": "Example Physicians",
		"Example Health - Billing questions: line 2\nGuarantor #00009012\nPatients included: You\n" +
			"Your Balance\n$4.00": "",
	} {
		require.Equalf(t, want, myChartName(text), "%q", text)
	}
}

func TestAStatementThatStatesItsDueDateIsFiledOpenWithItsFile(t *testing.T) {
	notes := &Notes{}
	bills := MyChartStatements("00001234", []MyChartRow{{
		Section: "Statements",
		Text: "Statement date: 03/03/2026\nService dates: 01/12/2026 - 02/02/2026\nTotal charges $500.00\n" +
			"Insurance paid ($300.00)\nAdjustments ($50.00)\nAmount due $150.00\nDue date: 03/28/2026\nView statement",
		Controls: []MyChartControl{{Words: "View statement", Href: myChartTestRoot + "/Billing/Statement?id=s1"}},
	}}, notes)
	require.Len(t, bills, 1)
	bill := bills[0]
	require.Equal(t, "00001234", bill.Subaccount)
	require.Equal(t, "00001234:2026-03-03", bill.ExternalID)
	require.Equal(t, "2026-03-03", bill.IssuedOn)
	require.Equal(t, "2026-03-28", bill.DueOn)
	require.Equal(t, "Open", bill.Status)
	require.True(t, bill.AmountDue.Equal(domain.MustFromString("150.00")))
	require.Equal(t, "2026-01-12", bill.PeriodStart)
	require.Equal(t, "2026-01-12", bill.PeriodEnd, "the second service day has no label of its own")
	raw := rawOf[myChartRaw](bill)
	require.Equal(t, myChartRaw{
		Account: "00001234", Statement: myChartTestRoot + "/Billing/Statement?id=s1",
		Charges: "$500.00", Insurance: "($300.00)", Adjustment: "($50.00)",
	}, raw)
	require.Empty(t, notes.List())
}

func TestAStatementWithNoDueDateIsDueOnTheDayItWasIssued(t *testing.T) {
	bills := MyChartStatements("00001234", []MyChartRow{
		{Text: "Jan 6, 2026\n$55.00\nStatement", Controls: []MyChartControl{{Words: "Statement"}}},
		{Text: "Statement 02/04/2026\nNew balance ($12.00)"},
	}, &Notes{})
	require.Len(t, bills, 2)
	require.Equal(t, "2026-01-06", bills[0].DueOn)
	require.Equal(t, "Open", bills[0].Status, "it is owed")
	require.True(t, bills[0].AmountDue.Equal(domain.MustFromString("55.00")))
	require.Empty(t, rawOf[myChartRaw](bills[0]).Statement, "a button is no link to fetch")
	require.Equal(t, "Paid", bills[1].Status, "a credit is nothing owed")
	require.True(t, bills[1].AmountDue.IsZero())
}

// A statement list draws each day as a calendar: month, day and year on lines
// of their own, and a "View" whose row only its heading names.
func TestAStatementDatedByACalendarIconIsRead(t *testing.T) {
	notes := &Notes{}
	viewer := myChartTestRoot + "/Billing/Details/StatementViewer?id=s5"
	bills := MyChartStatements("00001234", []MyChartRow{
		{Section: "Statements", Text: "Mar\n12\n2026\nView\nSent electronically\n$45.00",
			Controls: []MyChartControl{{Words: "View", Href: viewer}}},
		{Section: "Statements", Text: "Feb 12\n2026\n$30.00", Controls: []MyChartControl{{Words: ""}}},
		{Section: "Statements", Text: "Jan122026$25.00\nView", Controls: []MyChartControl{{Words: "View"}}},
	}, notes)
	require.Len(t, bills, 3)
	require.Equal(t, "2026-01-12", bills[0].IssuedOn, "inline spans run the month, day and year together")
	require.True(t, bills[0].AmountDue.Equal(domain.MustFromString("25.00")))
	require.Equal(t, "2026-02-12", bills[1].IssuedOn)
	require.Equal(t, "2026-03-12", bills[2].IssuedOn)
	require.True(t, bills[2].AmountDue.Equal(domain.MustFromString("45.00")))
	require.Equal(t, viewer, rawOf[myChartRaw](bills[2]).Statement)
	require.Empty(t, notes.List())
}

// The account's overview names its last statement beside the account's
// balance; the statement list's own row for that day is the one filed, with
// whichever link either offers.
func TestAStatementListsRowIsFiledOverTheOverviewsLastStatement(t *testing.T) {
	overview := myChartTestRoot + "/Billing/Details/StatementViewer?id=s5"
	bills := MyChartStatements("00001234", []MyChartRow{
		{Text: "Your Balance\n$520.00\nLast paid: $33.00 on 12/27/2025\nView last statement (03/12/2026)",
			Controls: []MyChartControl{{Words: "View last statement (03/12/2026)", Href: overview}}},
		{Section: "Statements", Text: "Mar\n12\n2026\nView\n$45.00", Opens: "3-1",
			Controls: []MyChartControl{{Words: "View"}}},
	}, &Notes{})
	require.Len(t, bills, 1)
	require.True(t, bills[0].AmountDue.Equal(domain.MustFromString("45.00")))
	require.Equal(t, "Open", bills[0].Status)
	require.Equal(t, overview, rawOf[myChartRaw](bills[0]).Statement)
}

// A row read again, under a tab or after "Load more", is one row; the tab
// gives it a section and a later reading the link it lacked.
func TestARowReadTwiceIsOneRow(t *testing.T) {
	read := newMyChartRead()
	require.Equal(t, 2, read.add(MyChartAccountPage{
		Statements: []MyChartRow{{Text: "02/12/2026 $30.00 View", Opens: "1-1"}},
		Dated:      []MyChartRow{{Text: "02/20/2026 Payment $30.00"}},
		Account:    "00001234",
	}))
	require.Equal(t, 1, read.add(MyChartAccountPage{
		Statements: []MyChartRow{
			{Section: "Statements", Text: "02/12/2026 $30.00 View",
				Controls: []MyChartControl{{Words: "View", Href: myChartTestRoot + "/Billing/Details/StatementViewer?id=s4"}}},
			{Section: "Statements", Text: "01/12/2026 $25.00 View"},
		},
		Dated: []MyChartRow{{Section: "Patient Payments", Text: "02/20/2026 Payment $30.00"}},
	}))
	require.Len(t, read.page.Statements, 2)
	require.Equal(t, "Statements", read.page.Statements[0].Section)
	require.Empty(t, read.page.Statements[0].Opens)
	require.NotEmpty(t, myChartStatementLink(read.page.Statements[0].Controls))
	require.Equal(t, "Patient Payments", read.page.Dated[0].Section)
	require.Equal(t, "00001234", read.page.Account)

	read.opened("9-9", "https://elsewhere.example/nothing")
	require.Empty(t, myChartStatementLink(read.page.Statements[1].Controls), "a mark no row carries gives no row a link")
}

func TestAStatementWhoseAmountCannotBeReadIsANoteNotAZero(t *testing.T) {
	notes := &Notes{}
	bills := MyChartStatements("00001234", []MyChartRow{
		{Text: "Statement date 03/03/2026\nCharges $90.00\nInsurance $40.00"},
		{Text: "View statement"},
	}, notes)
	require.Empty(t, bills)
	require.Len(t, notes.List(), 2)
}

func TestPaymentsAreTheHouseholdsAndNotTheInsurers(t *testing.T) {
	notes := &Notes{}
	payments := MyChartPayments("00001234", []MyChartRow{
		{Section: "Payments", Text: "03/10/2026\nPayment - Thank you\nVisa ending in 0000\n$150.00"},
		{Section: "Payments", Text: "02/14/2026\n$25.00\nMyChart payment"},
		{Section: "Payments", Text: "02/14/2026\n$25.00\nMyChart payment"},
		{Section: "Payments", Text: "04/01/2026\nScheduled payment\n$50.00"},
		{Section: "Activity", Text: "03/05/2026\nInsurance payment\n($300.00)"},
		{Section: "Activity", Text: "Jan 12, 2026\nOffice visit\n$500.00"},
		{Section: "Activity", Text: "Feb 20, 2026\nPatient payment\n($30.00)"},
		{Section: "Statements", Text: "03/03/2026 payment due $150.00", Controls: []MyChartControl{{Words: "View statement"}}},
	}, notes)
	require.Equal(t, []Payment{
		{Subaccount: "00001234", ExternalID: "00001234:2026-03-10:150.00", PaidOn: "2026-03-10",
			Amount: domain.MustFromString("150.00"), Method: "Visa ending in 0000"},
		{Subaccount: "00001234", ExternalID: "00001234:2026-02-14:25.00", PaidOn: "2026-02-14",
			Amount: domain.MustFromString("25.00")},
		{Subaccount: "00001234", ExternalID: "00001234:2026-02-14:25.00:+", PaidOn: "2026-02-14",
			Amount: domain.MustFromString("25.00")},
		{Subaccount: "00001234", ExternalID: "00001234:2026-02-20:30.00", PaidOn: "2026-02-20",
			Amount: domain.MustFromString("30.00")},
	}, payments)
	require.Len(t, notes.List(), 1, "the scheduled payment is a note")
}

// A Payments tab prints each payment's day twice, a calendar for a screen
// reader and the day as the eye sees it, and its row holds the period filter
// ("Currently viewing: Payments since last statement") and a receipt.
func TestAPaymentThatPrintsItsDayTwiceBesideThePeriodFilterIsRead(t *testing.T) {
	notes := &Notes{}
	payments := MyChartPayments("00001234", []MyChartRow{
		{Section: "Payments", Text: "Showing 1 out of 1 payments since 03/12/2026"},
		{Section: "Payments",
			Text: "Currently viewing: Payments since last statement\nMarch\n19\n2026\nMar 19 2026\nPatient Payment\n" +
				"MasterCard x0000\n$45.00\nView receipt",
			Controls: []MyChartControl{
				{Words: "Currently viewing: Payments since last statement"}, {Words: "View receipt"},
			}},
		{Section: "Payments", Text: "Feb 20 2026\nFebruary 20, 2026\nPatient Payment\nVisa x0000\n$30.00"},
		{Section: "Payments", Text: "Jan 20 2026\nFeb 2 2026\nPatient Payment\n$20.00"},
	}, notes)
	require.Equal(t, []Payment{
		{Subaccount: "00001234", ExternalID: "00001234:2026-03-19:45.00", PaidOn: "2026-03-19",
			Amount: domain.MustFromString("45.00"), Method: "MasterCard x0000"},
		{Subaccount: "00001234", ExternalID: "00001234:2026-02-20:30.00", PaidOn: "2026-02-20",
			Amount: domain.MustFromString("30.00"), Method: "Visa x0000"},
	}, payments, "a row printing two different days is no payment")
	require.Empty(t, notes.List())
	require.False(t, myChartIsStatementRow(MyChartRow{Controls: []MyChartControl{
		{Words: "Currently viewing: Payments since last statement"}, {Words: "Viewing options"}, {Words: "View receipt"},
	}}))
	require.True(t, myChartIsStatementRow(MyChartRow{Controls: []MyChartControl{{Words: "View last statement (03/12/2026)"}}}))
}

// Each period a Payments tab is shown for lists the payments again; a payment
// is read once however many periods list it, and two alike in one period are
// two.
func TestAPaymentListedByMorePeriodsThanOneIsReadOnce(t *testing.T) {
	payments := MyChartPayments("00001234", []MyChartRow{
		{View: "Payments", Section: "Payments", Text: "Mar 19 2026\nPatient Payment\nMasterCard x0000\n$45.00"},
		{View: "Payments · Year to date", Section: "Payments",
			Text: "March 19 2026\nMar 19 2026\nPatient Payment\nMasterCard x0000\n$45.00\nView receipt"},
		{View: "Payments · Year to date", Section: "Payments", Text: "Jan 20 2026\nCopay\nVisa x0000\n$20.00\nReceipt 1"},
		{View: "Payments · Year to date", Section: "Payments", Text: "Jan 20 2026\nCopay\nVisa x0000\n$20.00\nReceipt 2"},
		{View: "Payments · Year to date", Section: "Payments", Text: "Jan 20 2026\nCopay\nMasterCard x0000\n$20.00"},
		{View: "Activity", Section: "Activity", Text: "01/20/2026 Patient payment Visa x0000 ($20.00)"},
		{View: "Payments · Last year", Section: "Payments", Text: "Dec 18 2025\nPatient Payment\nVisa x0000\n$65.00"},
	}, &Notes{})
	var read []string
	for _, payment := range payments {
		read = append(read, payment.ExternalID+" "+payment.Method)
	}
	require.Equal(t, []string{
		"00001234:2026-03-19:45.00 MasterCard x0000",
		"00001234:2026-01-20:20.00 Visa x0000",
		"00001234:2026-01-20:20.00:+ Visa x0000",
		"00001234:2026-01-20:20.00:++ MasterCard x0000",
		"00001234:2025-12-18:65.00 Visa x0000",
	}, read)
}

// A statement list that prints each day twice is still a list, read over the
// overview's "last statement".
func TestAStatementPrintingItsDayTwiceIsTheListsRow(t *testing.T) {
	bills := MyChartStatements("00001234", []MyChartRow{
		{Text: "Your Balance\n$520.00\nPaid this month $10.00\nView last statement (03/12/2026)",
			Controls: []MyChartControl{{Words: "View last statement (03/12/2026)"}}},
		{Section: "Statements", Text: "March\n12\n2026\nMar 12 2026\n$45.00\nView", Opens: "2-1",
			Controls: []MyChartControl{{Words: "View"}}},
	}, &Notes{})
	require.Len(t, bills, 1)
	require.Equal(t, "2026-03-12", bills[0].IssuedOn)
	require.True(t, bills[0].AmountDue.Equal(domain.MustFromString("45.00")))
}

// The rows below are what the account script read off mychart_live_test.go's
// invented account page in a headless Chromium, so the readers are held to
// the script's real output without a browser.
func TestTheRowsTheScriptReadsAreTwoStatementsAndTwoPayments(t *testing.T) {
	notes := &Notes{}
	statement := myChartTestRoot + "/Billing/Statement?id=s1"
	read := MyChartAccountPage{
		Statements: []MyChartRow{
			{Section: "Statements", Text: "Statement date: 03/03/2026\nAmount due $150.00\nDue date: 03/28/2026\nView statement",
				Controls: []MyChartControl{{Words: "View statement", Href: statement}}},
			{Section: "Statements", Text: "Statement date: 02/03/2026\nAmount due $0.00\nView statement",
				Controls: []MyChartControl{{Words: "View statement"}}},
		},
		Dated: []MyChartRow{
			{Section: "Account details", Text: "Jan 12, 2026Office visit$500.00"},
			{Section: "Statements", Text: "Statement date: 03/03/2026"},
			{Section: "Statements", Text: "Due date: 03/28/2026"},
			{Section: "Statements", Text: "Statement date: 02/03/2026\nAmount due $0.00\nView statement",
				Controls: []MyChartControl{{Words: "View statement"}}},
			{Section: "Payments", Text: "03/10/2026\tPayment - Thank you\nVisa ending in 0000\t$150.00"},
			{Section: "Payments", Text: "02/14/2026\tMyChart payment\t$25.00"},
		},
	}
	bills := MyChartStatements("00001234", read.Statements, notes)
	require.Len(t, bills, 2)
	require.Equal(t, []string{"2026-02-03", "2026-03-03"}, []string{bills[0].IssuedOn, bills[1].IssuedOn})
	require.Equal(t, []string{"Paid", "Open"}, []string{bills[0].Status, bills[1].Status})
	require.Equal(t, statement, rawOf[myChartRaw](bills[1]).Statement)

	payments := MyChartPayments("00001234", read.Dated, notes)
	require.Len(t, payments, 2)
	require.Equal(t, "2026-03-10", payments[0].PaidOn)
	require.Equal(t, "Visa ending in 0000", payments[0].Method)
	require.True(t, payments[1].Amount.Equal(domain.MustFromString("25.00")))
	require.Empty(t, notes.List())
}

// myChartPortal is a scripted portal: the summary's cards and one account's
// page, by which script asked. The page has no tabs and no "more", and
// shows no dialog.
func myChartPortal(cards []MyChartCard, account MyChartAccountPage) *browser.StubPage {
	page := &browser.StubPage{}
	page.OnEvaluate = func(script string, _ any) (any, error) {
		switch script {
		case myChartSummaryScript:
			return cards, nil
		case myChartRowsScript:
			return account, nil
		case myChartTabsScript:
			return []string{}, nil
		case myChartDialogScript:
			return false, nil
		}
		return nil, nil
	}
	return page
}

// trailOf is a Call's trail kept in a list.
func trailOf(call *Call) *[]string {
	var lines []string
	call.Trail = func(note string, look bool) {
		if look {
			note = "page: " + note
		}
		lines = append(lines, note)
	}
	return &lines
}

func TestAPullReadsEachWantedAccountsStatementsAndPayments(t *testing.T) {
	details := myChartTestRoot + "/Billing/Details?ID=a1"
	page := myChartPortal(
		[]MyChartCard{
			{Text: "Guarantor #: 00001234\nAmount due $150.00", Href: details},
			{Text: "Guarantor #: 00005678\nAmount due $0.00", Href: myChartTestRoot + "/Billing/Details?ID=a2"},
		},
		MyChartAccountPage{
			Statements: []MyChartRow{{
				Section: "Statements", Text: "Statement date 03/03/2026\nAmount due $150.00\nDue 03/28/2026",
				Controls: []MyChartControl{{Words: "View statement", Href: myChartTestRoot + "/Billing/Statement?id=s1"}},
			}},
			Dated: []MyChartRow{{Section: "Payments", Text: "03/10/2026\nPayment received\n$150.00"}},
		})
	page.OnBytes = func(address string) (int, string, []byte, error) {
		return 200, "application/pdf", []byte("%PDF-1.4 invented"), nil
	}
	notes := &Notes{}
	module := aimedMyChart(t)

	call := Call{Ctx: t.Context(), Page: page, Notes: notes, Subaccounts: []string{"00001234"}}
	trail := trailOf(&call)
	pulled, err := module.FetchBills(call)
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Equal(t, []string{myChartTestRoot + "/Billing/Summary", details}, page.Visited)
	require.Len(t, pulled.Bills, 1)
	require.Len(t, pulled.Payments, 1)
	require.Equal(t, "2026-03-10", pulled.Payments[0].PaidOn)
	require.Equal(t, []string{"00001234", "00005678"},
		[]string{pulled.Subaccounts[0].ExternalID, pulled.Subaccounts[1].ExternalID}, "every card's label is carried")
	require.Contains(t, *trail, "page: MyChart's billing summary")
	require.Contains(t, *trail, "page: MyChart billing account ••••1234: its page")

	document, err := module.FetchDocument(Call{Ctx: t.Context(), Page: page, Notes: notes}, pulled.Bills[0])
	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "mychart-1234-2026-03-03.pdf", document.Filename)
	require.Equal(t, []string{myChartTestRoot + "/Billing/Statement?id=s1"}, page.Opened)
}

// The account page lists the payments since the last statement and offers
// "Year to date" and "Last year"; each is chosen and applied, and its list
// read, in the page's order.
func TestAPullReadsThePaymentsOfEveryPeriodThePageOffers(t *testing.T) {
	shown := ""
	payment := func(day, amount string) MyChartRow {
		return MyChartRow{Section: "Payments", Text: day + "\nPatient Payment\nVisa x0000\n" + amount + "\nView receipt"}
	}
	lists := map[string][]MyChartRow{
		"":             {payment("Mar 19 2026", "$45.00")},
		"Year to date": {{Section: "Payments", Text: "Showing 2 payments since 01/01/2026"}, payment("Mar 19 2026", "$45.00"), payment("Jan 20 2026", "$20.00")},
		"Last year":    {payment("Dec 18 2025", "$65.00")},
	}
	var chosen []string
	page := &browser.StubPage{}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch script {
		case myChartSummaryScript:
			return []MyChartCard{{Text: "Guarantor #: 00001234", Href: myChartTestRoot + "/Billing/Details?ID=a1"}}, nil
		case myChartRowsScript:
			return MyChartAccountPage{Dated: lists[shown]}, nil
		case myChartTabsScript:
			return []string{}, nil
		case myChartDialogScript:
			return false, nil
		case myChartViewingsScript:
			choose, _ := arg.(map[string]any)["choose"].(string)
			if choose == "" {
				if shown != "" {
					return []string{}, nil
				}
				return []string{"Year to date", "Last year"}, nil
			}
			chosen = append(chosen, choose)
			shown = choose
			return "Apply", nil
		}
		return nil, nil
	}
	call := Call{Ctx: t.Context(), Page: page, Notes: &Notes{}}
	trail := trailOf(&call)
	pulled, err := aimedMyChart(t).FetchBills(call)
	require.NoError(t, err)
	require.Equal(t, []string{"Year to date", "Last year"}, chosen)
	var paid []string
	for _, one := range pulled.Payments {
		paid = append(paid, one.ExternalID)
	}
	require.Equal(t, []string{"00001234:2026-03-19:45.00", "00001234:2026-01-20:20.00", "00001234:2025-12-18:65.00"}, paid)
	require.Contains(t, *trail, "billing account ••••1234: the page can be shown for Year to date · Last year")
	require.Contains(t, *trail, "billing account ••••1234: chose “Year to date” (applied by “Apply”): 2 new rows")
	require.Contains(t, *trail, "page: MyChart billing account ••••1234: the page shown for “Last year”")
}

func TestAStatementLinkOffThePortalIsNotFetched(t *testing.T) {
	raw, _ := json.Marshal(myChartRaw{Account: "00001234", Statement: "https://files.elsewhere.example/s1.pdf"})
	page := &browser.StubPage{}
	notes := &Notes{}
	document, err := aimedMyChart(t).FetchDocument(Call{Ctx: t.Context(), Page: page, Notes: notes},
		Bill{Subaccount: "00001234", IssuedOn: "2026-03-03", Raw: raw})
	require.NoError(t, err)
	require.Nil(t, document)
	require.Empty(t, page.Opened)
	require.Len(t, notes.List(), 1)
}

func TestAPullThatLandsOnTheSignInAsksForOne(t *testing.T) {
	page := myChartPortal(nil, MyChartAccountPage{})
	page.OnGoto = func(string) error {
		page.Location = myChartTestRoot + "/Authentication/Login?postloginurl=Billing"
		return nil
	}
	pulled, err := aimedMyChart(t).FetchBills(Call{Ctx: t.Context(), Page: page, Notes: &Notes{}})
	require.NoError(t, err)
	require.True(t, pulled.NeedsSignIn)

	_, err = aimedMyChart(t).Subaccounts(Call{Ctx: t.Context(), Page: page, Notes: &Notes{}})
	require.ErrorIs(t, err, ErrNeedsSignIn)
}

func TestAnAccountLinkOffThePortalIsNotFollowed(t *testing.T) {
	page := myChartPortal([]MyChartCard{
		{Text: "Guarantor #: 00001234", Href: "https://elsewhere.example/MyChart/Billing/Details"},
	}, MyChartAccountPage{})
	notes := &Notes{}
	pulled, err := aimedMyChart(t).FetchBills(Call{Ctx: t.Context(), Page: page, Notes: notes})
	require.NoError(t, err)
	require.Empty(t, pulled.Bills)
	require.Equal(t, []string{myChartTestRoot + "/Billing/Summary"}, page.Visited)
	require.Len(t, notes.List(), 1)
}

func TestAHostOnlyPortalAddressIsAimedAtTheVendorsDefaultRoot(t *testing.T) {
	aimed, ok := NewMyChart().WithSite("https://mychart.examplehealth.example/").(*MyChart)
	require.True(t, ok)
	require.Equal(t, myChartTestRoot, aimed.SiteHome())
	require.Equal(t, myChartTestRoot+"/Billing/Summary", aimed.SignInURL())

	other, ok := NewMyChart().WithSite("https://mychart.examplehealth.example/Portal/Home").(*MyChart)
	require.True(t, ok)
	require.Equal(t, "https://mychart.examplehealth.example/Portal", other.SiteHome())
}

func TestASummaryThatIsThePortalsNotFoundPageSaysWhichAddressAnsweredIt(t *testing.T) {
	page := myChartPortal(nil, MyChartAccountPage{})
	page.Heading = "404 - Page not found"
	notes := &Notes{}
	_, err := aimedMyChart(t).Subaccounts(Call{Ctx: t.Context(), Page: page, Notes: notes})
	require.NoError(t, err)
	require.Contains(t, strings.Join(notes.List(), "\n"),
		`MyChart answered "404 - Page not found" for the billing summary (/MyChart/Billing/Summary)`)
}

func TestAnEmptySummaryThatAnswersHTTP404SaysSo(t *testing.T) {
	page := myChartPortal(nil, MyChartAccountPage{})
	page.OnBytes = func(string) (int, string, []byte, error) { return 404, "text/html", nil, nil }
	notes := &Notes{}
	_, err := aimedMyChart(t).Subaccounts(Call{Ctx: t.Context(), Page: page, Notes: notes})
	require.NoError(t, err)
	require.Contains(t, strings.Join(notes.List(), "\n"),
		"MyChart answered HTTP 404 for the billing summary at "+myChartTestRoot+"/Billing/Summary")
}
