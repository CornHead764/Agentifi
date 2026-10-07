package billers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// The field names are the plan site's GraphQL answers' own; every value is
// invented.
const northwesternMutualBillingJSON = `{"data":{"payments":{"billingAccounts":[
  {"isaId":"I-1","isaNumber":"40001234","isaPrefix":"","isaType":"IsaPlus","isaClass":"STANDARD",
   "frequency":"Monthly","paymentDetails":{"dueAmount":"$50.00","dueAmountLabel":"Amount Due",
   "dueDate":"November 2, 2026","dueDateLabel":"Due Date"},"tag":{"status":"NEUTRAL","text":"Autopay Off"}},
  {"isaId":"I-2","isaNumber":"40005678","isaType":"IsaPlus","isaClass":"STANDARD","frequency":"Annual",
   "paymentDetails":{"dueAmount":"$1,000.00","dueAmountLabel":"Amount Due","dueDate":"Mar 3, 2027",
   "dueDateLabel":"Scheduled For","dueExtra":{"prefix":"From","postfix":"Checking ****0000",
   "value":"INVENTED BANK","status":"NEUTRAL"}},"premiumPayment":"$1,000.00",
   "tag":{"status":"SUCCESS","text":"Autopay On"}},
  {"isaId":"I-3","isaNumber":"40009999","frequency":"Monthly",
   "paymentDetails":{"dueAmount":"$80.00","dueDate":"Oct 15, 2026","dueDateLabel":"Scheduled For"},
   "tag":{"status":"SUCCESS","text":"Autopay On"}}
]}}}`

const northwesternMutualActivityJSON = `{"data":{"wallet":{"paymentActivity":{"disclaimerMessage":"",
 "transactionHistory":{"productMetrics":[],"paymentMethodMetrics":[],"transactions":[
  {"accountId":"A900000","amount":1000.00,"amountFormatted":"$1,000.00","date":"2026-03-03",
   "dateFormatted":"Mar 3, 2026","description":"Autopay","paymentMethod":"Checking","status":"PROCESSED",
   "statusFormatted":"Processed","action":{"name":"BILLING_LINK","displayName":"Billing Account ****5678"},
   "policyDetails":[]},
  {"accountId":"A900002","amount":80.00,"date":"2026-10-16","description":"Autopay","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****9999"}},
  {"accountId":"A900002","amount":80.00,"date":"2026-09-15","description":"Autopay","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****9999"}},
  {"accountId":"A900002","amount":40.00,"date":"2026-08-15","description":"One-time payment",
   "status":"PROCESSED","action":{"name":"BILLING_LINK","displayName":"Billing Account ****9999"}},
  {"accountId":"A900002","amount":40.00,"date":"2026-08-15","description":"Autopay","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****9999"}},
  {"accountId":"A900000","amount":1000.00,"date":"2026-09-20","description":"Payment","status":"PENDING",
   "statusFormatted":"Pending","action":{"name":"BILLING_LINK","displayName":"Billing Account ****5678"}},
  {"accountId":"A900003","amount":90.00,"date":"2026-09-01","description":"Loan repayment",
   "status":"PROCESSED","action":{"name":"POLICY_LINK","displayName":"Policy ****0000"},
   "policyDetails":[{"policyId":"P-1","policyName":"Invented Universal Life"}]},
  {"accountId":"A900004","amount":70.00,"date":"2026-09-02","description":"Autopay","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****0000"}},
  {"accountId":"A900002","amount":"n/a","date":"2026-07-15","description":"Autopay","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****9999"}},
  {"accountId":"A900000","amount":-25.00,"date":"2026-04-01","description":"Refund","status":"PROCESSED",
   "action":{"name":"BILLING_LINK","displayName":"Billing Account ****5678"}}
]}}}}}`

func northwesternMutualFixtures(t *testing.T) ([]NorthwesternMutualBillingAccount, []NorthwesternMutualPayment) {
	t.Helper()
	var billing northwesternMutualBillingAnswer
	require.NoError(t, httpx.DecodeJSON([]byte(northwesternMutualBillingJSON), &billing))
	var activity northwesternMutualActivityAnswer
	require.NoError(t, httpx.DecodeJSON([]byte(northwesternMutualActivityJSON), &activity))
	return billing.Data.Payments.BillingAccounts,
		activity.Data.Wallet.PaymentActivity.TransactionHistory.Transactions
}

func TestNorthwesternMutualFilesProcessedPaymentsPaidAndWhatIsDueOpen(t *testing.T) {
	accounts, payments := northwesternMutualFixtures(t)
	notes := &Notes{}
	bills := NorthwesternMutualBills(accounts, payments, notes)

	type filed struct {
		external, due, issued, amount, autopay, status string
	}
	var got []filed
	for _, bill := range bills {
		require.Equal(t, "USD", bill.Currency)
		got = append(got, filed{bill.ExternalID, bill.DueOn, bill.IssuedOn, bill.AmountDue.String(),
			bill.AutopayOn, bill.Status})
	}
	require.Equal(t, []filed{
		// Due on a stated date with autopay off: open, and no draft day.
		{"40001234:2026-11-02", "2026-11-02", "", "50.00", "", "Open"},
		{"40005678:2026-03-03", "2026-03-03", "2026-03-03", "1000.00", "", "Paid"},
		// "Scheduled For" is the day autopay drafts it.
		{"40005678:2027-03-03", "2027-03-03", "", "1000.00", "2027-03-03", "Open"},
		// Two payments processed on one day are one bill of their sum.
		{"40009999:2026-08-15", "2026-08-15", "2026-08-15", "80.00", "", "Paid"},
		{"40009999:2026-09-15", "2026-09-15", "2026-09-15", "80.00", "", "Paid"},
		// The scheduled draft processed a day late: it is this paid bill, and
		// no open bill is filed beside it.
		{"40009999:2026-10-16", "2026-10-16", "2026-10-16", "80.00", "", "Paid"},
	}, got)

	for _, bill := range bills {
		require.Equal(t, bill.ExternalID[:len("40000000")], bill.Subaccount)
	}
	var raw northwesternMutualRaw
	require.NoError(t, json.Unmarshal(bills[3].Raw, &raw))
	require.Equal(t, northwesternMutualRaw{Account: "Billing Account ****9999", Frequency: "Monthly",
		Description: "One-time payment, Autopay"}, raw)
	var due northwesternMutualRaw
	require.NoError(t, json.Unmarshal(bills[2].Raw, &due))
	require.Equal(t, northwesternMutualRaw{Account: "Billing Account ****5678", Frequency: "Annual",
		Shown: "Scheduled For Autopay On"}, due)

	require.Equal(t, []string{
		`Northwestern Mutual shows a payment of 1000.00 from Billing Account ****5678 on 2026-09-20 as "Pending"; ` +
			"only a processed payment is filed",
		`Northwestern Mutual shows a payment from "Billing Account ****0000", which is not one billing account ` +
			"the billing page lists; it is left out",
		`a Northwestern Mutual payment from Billing Account ****9999 carried no readable date or amount ` +
			`("2026-07-15", "n/a"); it is left out`,
	}, notes.List())
}

func TestNorthwesternMutualFilesNoDueThatIsNothingOrCannotBeRead(t *testing.T) {
	due := func(amount, date string) NorthwesternMutualBillingAccount {
		account := NorthwesternMutualBillingAccount{Number: "40001234", Frequency: "Quarterly"}
		account.Payment.DueAmount, account.Payment.DueDate = amount, date
		return account
	}
	for _, tc := range []struct {
		name    string
		account NorthwesternMutualBillingAccount
		notes   []string
	}{
		{"nothing shown", due("", ""), nil},
		{"nothing due", due("$0.00", "Dec 1, 2026"), nil},
		{"an unreadable date", due("$310.00", "sometime"), []string{
			`Northwestern Mutual's billing page shows Billing Account ****1234 due as "$310.00" on "sometime", ` +
				"which could not be read; no bill is filed for it"}},
		{"an unreadable amount", due("pending", "Dec 1, 2026"), []string{
			`Northwestern Mutual's billing page shows Billing Account ****1234 due as "pending" on "Dec 1, 2026", ` +
				"which could not be read; no bill is filed for it"}},
	} {
		notes := &Notes{}
		require.Empty(t, NorthwesternMutualBills([]NorthwesternMutualBillingAccount{tc.account}, nil, notes), tc.name)
		require.Equal(t, tc.notes, notes.List(), tc.name)
	}
}

// An open bill is that cycle until a processed payment of its amount lands
// within five days of its date, on either side.
func TestANorthwesternMutualDueIsPaidOnlyByItsOwnAccountsPaymentOfItsAmountNearItsDate(t *testing.T) {
	account := NorthwesternMutualBillingAccount{Number: "40009999", Frequency: "Monthly"}
	account.Payment.DueAmount = "$80.00"
	account.Payment.DueDate = "Oct 15, 2026"
	paid := func(amount json.Number, date, shown string) NorthwesternMutualPayment {
		payment := NorthwesternMutualPayment{Amount: amount, Date: date, Status: "PROCESSED"}
		payment.Action.Name, payment.Action.Shown = "BILLING_LINK", shown
		return payment
	}
	for _, tc := range []struct {
		name    string
		payment NorthwesternMutualPayment
		open    bool
	}{
		{"on the day", paid("80.00", "2026-10-15", "Billing Account ****9999"), false},
		{"five days late", paid("80.00", "2026-10-20", "Billing Account ****9999"), false},
		{"five days early", paid("80.00", "2026-10-10", "Billing Account ****9999"), false},
		{"six days late", paid("80.00", "2026-10-21", "Billing Account ****9999"), true},
		{"another amount", paid("79.99", "2026-10-15", "Billing Account ****9999"), true},
		{"another account", paid("80.00", "2026-10-15", "Billing Account ****1234"), true},
	} {
		notes := &Notes{}
		bills := NorthwesternMutualBills([]NorthwesternMutualBillingAccount{account},
			[]NorthwesternMutualPayment{tc.payment}, notes)
		open := false
		for _, bill := range bills {
			open = open || bill.Status == "Open"
		}
		require.Equal(t, tc.open, open, tc.name)
	}
}

func TestNorthwesternMutualReadsOnlyTheBillingPagesOwnDateSpellings(t *testing.T) {
	for text, want := range map[string]string{
		"Mar 3, 2027":       "2027-03-03",
		"March 3, 2027":     "2027-03-03",
		" Oct  15,  2026 ":  "2026-10-15",
		"Dec 31, 2026":      "2026-12-31",
		"Mar 3rd, 2027":     "2027-03-03",
		"Sept. 3, 2027":     "2027-09-03",
		"2026-10-15":        "",
		"10/15/2026":        "",
		"Feb 30, 2027":      "",
		"Due Mar 3, 2027":   "",
		"Mar 3, 2027 (est)": "",
		"":                  "",
	} {
		require.Equal(t, want, NorthwesternMutualShownDay(text), text)
	}
}

func TestNorthwesternMutualBillsOneAccountPerBillingAccount(t *testing.T) {
	accounts, _ := northwesternMutualFixtures(t)
	accounts = append(accounts, NorthwesternMutualBillingAccount{Number: "", Frequency: "Monthly"})
	notes := &Notes{}
	require.Equal(t, []Subaccount{
		{ExternalID: "40001234", Label: "Billing Account ****1234, Monthly", MaskedNumber: "••••1234"},
		{ExternalID: "40005678", Label: "Billing Account ****5678, Annual", MaskedNumber: "••••5678"},
		{ExternalID: "40009999", Label: "Billing Account ****9999, Monthly", MaskedNumber: "••••9999"},
	}, NorthwesternMutualSubaccounts(accounts, notes))
	require.Equal(t, []string{"a Northwestern Mutual billing account carried no readable number; it is left out"},
		notes.List())
}

func TestNorthwesternMutualRecordsOnlyThePlanSitesGraphQL(t *testing.T) {
	for _, address := range []string{
		"https://api.plan.northwesternmutual.com/graphql",
		"https://API.plan.northwesternmutual.com/graphql?op=Accounts",
	} {
		require.True(t, northwesternMutualGraphQL.MatchString(address), address)
	}
	for _, address := range []string{
		"http://api.plan.northwesternmutual.com/graphql",
		"https://api.plan.northwesternmutual.com/graphql-ws",
		"https://api.plan.northwesternmutual.com.example.test/graphql",
		"https://plan.northwesternmutual.com/graphql",
	} {
		require.False(t, northwesternMutualGraphQL.MatchString(address), address)
	}
}

// stubNorthwesternMutualPage answers each plan page with the app's GraphQL
// traffic: a question about something else first, then the one read.
func stubNorthwesternMutualPage(status int, billing, activity string) *browser.StubPage {
	page := &browser.StubPage{}
	const graphql = "https://api.plan.northwesternmutual.com/graphql"
	page.OnGoto = func(address string) error {
		switch address {
		case northwesternMutualBilling:
			page.Respond(browser.StubResponse{Address: graphql, Code: 200,
				Payload: []byte(`{"data":{"profile":{"firstName":"Pat"}}}`)})
			page.Respond(browser.StubResponse{Address: graphql, Code: status, Payload: []byte(billing)})
		case northwesternMutualActivity:
			page.Respond(browser.StubResponse{Address: graphql, Code: 200, Payload: []byte(billing)})
			if activity != "" {
				page.Respond(browser.StubResponse{Address: graphql, Code: status, Payload: []byte(activity)})
			}
		}
		return nil
	}
	return page
}

func TestNorthwesternMutualReadsWhatTheBillingAndActivityPagesReceived(t *testing.T) {
	page := stubNorthwesternMutualPage(200, northwesternMutualBillingJSON, northwesternMutualActivityJSON)
	module := NewNorthwesternMutual()

	notes := &Notes{}
	accounts, err := module.Subaccounts(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.Len(t, accounts, 3)
	require.Contains(t, notes.List(), "Northwestern Mutual lists 3 billing accounts")
	require.Equal(t, []string{northwesternMutualBilling}, page.Visited)

	notes = &Notes{}
	pulled, err := module.FetchBills(Call{Page: page, Notes: notes, Subaccounts: []string{"40009999", "40005678"}})
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Len(t, pulled.Bills, 5)
	require.Contains(t, notes.List(), "Northwestern Mutual answered 4 paid bills and 1 due")
	require.Equal(t,
		[]string{northwesternMutualBilling, northwesternMutualBilling, northwesternMutualActivity}, page.Visited)
	require.Zero(t, page.Slept)
}

// With no payment activity, what the billing page says is due is still filed.
func TestNorthwesternMutualFilesWhatIsDueWhenTheActivityPageNeverAnswers(t *testing.T) {
	page := stubNorthwesternMutualPage(200, northwesternMutualBillingJSON, "")
	notes := &Notes{}
	pulled, err := NewNorthwesternMutual().FetchBills(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Len(t, pulled.Bills, 3)
	for _, bill := range pulled.Bills {
		require.Equal(t, "Open", bill.Status)
	}
	require.Equal(t, northwesternMutualWait, page.Slept)
	require.Contains(t, notes.List(), "Northwestern Mutual's payment activity did not load within 45s; it was not read")
}

func TestNorthwesternMutualSaysSoWhenTheBillingPageNeverAsks(t *testing.T) {
	page := &browser.StubPage{}
	notes := &Notes{}
	pulled, err := NewNorthwesternMutual().FetchBills(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Empty(t, pulled.Bills)
	require.Equal(t, northwesternMutualWait, page.Slept)
	require.Equal(t, []string{northwesternMutualBilling}, page.Visited, "no activity is read without accounts")
	require.Equal(t, []string{"Northwestern Mutual's billing page did not load within 45s; it was not read"},
		notes.List())
}

func TestNorthwesternMutualAsksForASignInWhenTheAppIsRefusedOrBouncedToTheForm(t *testing.T) {
	module := NewNorthwesternMutual()

	refused := stubNorthwesternMutualPage(401, `{"errors":[{"message":"Unauthorized"}]}`, "")
	pulled, err := module.FetchBills(Call{Page: refused, Notes: &Notes{}})
	require.NoError(t, err)
	require.True(t, pulled.NeedsSignIn)

	bounced := &browser.StubPage{}
	bounced.OnGoto = func(string) error {
		bounced.Location = "https://login.northwesternmutual.com/login?fromURI=%2Fbilling"
		return nil
	}
	pulled, err = module.FetchBills(Call{Page: bounced, Notes: &Notes{}})
	require.NoError(t, err)
	require.True(t, pulled.NeedsSignIn)
	require.Zero(t, bounced.Slept, "a page bounced to the form is not waited on")

	_, err = module.Subaccounts(Call{Page: bounced, Notes: &Notes{}})
	require.ErrorIs(t, err, ErrNeedsSignIn)
}

func TestNorthwesternMutualEntersAtTheProbedFormAndLandsOnThePlanSummary(t *testing.T) {
	module := NewNorthwesternMutual()
	require.Equal(t, "https://www.northwesternmutual.com/login/", module.SignInURL())
	require.Equal(t, "https://plan.northwesternmutual.com/summary", module.LandingURL())
	require.Equal(t, module.LandingURL(), module.AccountPage)
	require.False(t, module.AccountArea(module.SignInURL()), "the sign-in entry is not inside")
	require.True(t, module.AccountArea(module.LandingURL()))

	biller, known := domain.BillerByID(domain.BillerNorthwesternMutual)
	require.True(t, known)
	for _, address := range []string{module.SignInURL(), module.LandingURL()} {
		require.NotEmpty(t, browser.WithinSite(address, biller.Home, ""), address)
	}
	require.False(t, biller.HasDocuments)
	require.True(t, biller.ReportsAutopay)
}

// The sign-in's form, factor and code pages all sit on the login host, so only
// the plan site is inside, and not its SAML hand-over or sign-out.
func TestNorthwesternMutualIsInsideOnThePlanSiteAndNotOnTheLoginHost(t *testing.T) {
	area := NewNorthwesternMutual().AccountArea
	for _, inside := range []string{
		"https://plan.northwesternmutual.com/summary",
		"https://plan.northwesternmutual.com/billing",
		"https://plan.northwesternmutual.com/wallet/payment-activity",
		"https://PLAN.northwesternmutual.com/summary?tab=1",
	} {
		require.Equal(t, StateSignedIn, StateOf(Form{}, inside, area).State, inside)
	}
	for _, outside := range []string{
		"https://plan.northwesternmutual.com/saml/logout",
		"https://plan.northwesternmutual.com/SAML/acs",
		"https://plan.northwesternmutual.com/logout",
		"https://login.northwesternmutual.com/login?fromURI=%2Fsummary",
		"https://login.northwesternmutual.com/mfaverify",
		"https://www.northwesternmutual.com/login/",
		"http://plan.northwesternmutual.com/summary",
		"https://plan.northwesternmutual.com.example.test/summary",
	} {
		require.Equal(t, StateInteractive, StateOf(Form{}, outside, area).State, outside)
	}
	require.Equal(t, StatePassword,
		StateOf(Form{Password: true, Username: true}, "https://plan.northwesternmutual.com/summary", area).State,
		"the form is read before the address")
}
