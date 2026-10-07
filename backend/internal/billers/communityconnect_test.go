package billers

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Every slug, id, account number, address, figure and date here is invented.

const (
	communityConnectTestSite  = "exampletown"
	communityConnectOtherSite = "sampleton"
)

func TestCommunityConnectIsAimedAtOneDeploymentAtATime(t *testing.T) {
	module := NewCommunityConnect()

	one, ok := module.WithSite(communityConnectTestSite).(*CommunityConnect)
	require.True(t, ok)
	other, ok := module.WithSite(communityConnectOtherSite).(*CommunityConnect)
	require.True(t, ok)

	require.Equal(t, communityConnectTestSite, one.Site())
	require.Equal(t, communityConnectOtherSite, other.Site())
	// Aiming is a copy, not a mutation.
	require.Equal(t, "", module.Site(), "the registry's own module stays unaimed")
	require.NotEqual(t, one.SignInURL(), other.SignInURL())

	// A slug is a hostname label, and a person types it into a form.
	require.Equal(t, one.SignInURL(),
		module.WithSite("  EXAMPLETOWN  ").(*CommunityConnect).SignInURL())
}

func TestCommunityConnectAddressesAreTheDeploymentsOwn(t *testing.T) {
	module := NewCommunityConnect().WithSite(communityConnectTestSite).(*CommunityConnect)

	root := "https://" + communityConnectTestSite + ".ourcommunityconnect.com/"
	require.Equal(t, root, module.SignInURL())
	require.Equal(t, root, module.LandingURL())
	require.Contains(t, module.SignInPrompt(), "Our Community Connect")
	require.Equal(t, "https://www.ourcommunityconnect.com", module.Home)
}

// The account area must not answer for the sign-in form: the app root is the
// sign-in to a signed-out visitor, so a pattern over the host alone would call
// a password box a signed-in session.
func TestTheCommunityConnectAccountAreaIsNotItsSignInForm(t *testing.T) {
	module := NewCommunityConnect().WithSite(communityConnectTestSite).(*CommunityConnect)
	host := "https://" + communityConnectTestSite + ".ourcommunityconnect.com"

	for _, inside := range []string{
		host + "/home",
		host + "/utility-billing",
		host + "/utility-billing/summary",
		host + "/accounts-receivable",
		host + "/home?selectedCustomerId=1",
	} {
		require.Truef(t, module.AccountArea(inside), "should be inside the account: %s", inside)
	}

	for _, outside := range []string{
		host + "/login",
		host + "/login?returnUrl=%2Fhome",
		// The app root, which is the sign-in to anybody not signed in.
		host + "/",
		host,
		// Another town's deployment of the same product, which this
		// connection's session has nothing to do with.
		"https://" + communityConnectOtherSite + ".ourcommunityconnect.com/home",
		// The vendor's own marketing site, which bills nobody.
		"https://www.ourcommunityconnect.com/",
		// A lookalike host that merely ends the same way.
		"https://" + communityConnectTestSite + ".ourcommunityconnect.com.example.test/home",
	} {
		require.Falsef(t, module.AccountArea(outside), "should be outside the account: %s", outside)
	}

	// The form is read before the address either way, so a page showing a
	// password box is a sign-in wherever it is served from.
	require.Equal(t, StatePassword, StateOf(
		Form{Username: true, Password: true}, host+"/home", module.AccountArea).State)
}

// A module nobody has told which deployment to use builds no addresses at all.
func TestACommunityConnectWithNoSiteBuildsNoAddress(t *testing.T) {
	module := NewCommunityConnect()

	require.Equal(t, "", module.Site())
	require.Equal(t, "", module.SignInURL())
	require.Equal(t, "", module.LandingURL())
	require.Nil(t, module.AccountArea)
	require.Equal(t, module, module.WithSite("   "), "nothing to aim at is still unaimed")

	// Nothing that looks like an address with the site left out of it.
	require.Equal(t, StateInteractive, StateOf(
		Form{}, "https://.ourcommunityconnect.com/", module.AccountArea).State)

	// A slug is a hostname label and also a path segment, so anything that
	// would reach a second host or a second path leaves the module unaimed
	// rather than being interpolated into an address.
	for _, refused := range []string{
		"exampletown.example.test",
		"exampletown/../sampleton",
		"example town",
		"",
	} {
		require.Equalf(t, "", module.WithSite(refused).(*CommunityConnect).Site(),
			"should aim at nothing: %q", refused)
	}
}

// An unaimed module cannot read either, and answers a note rather than a
// sign-in: what is missing is a setting.
func TestAnUnaimedCommunityConnectReadsNothingAndSaysWhy(t *testing.T) {
	module := NewCommunityConnect()
	page := stubCommunityConnectPage(nil)
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.Empty(t, result.Bills)
	require.False(t, result.NeedsSignIn, "a sign-in would not fill in a setting")
	require.Contains(t, notes.List()[0], "has not been told which deployment to use")

	found, err := module.Subaccounts(Call{Page: page, Notes: &Notes{}})
	require.NoError(t, err)
	require.Empty(t, found)

	require.Empty(t, page.Args, "an unaimed module asks nothing of the page")
}

func TestTheCommunityConnectOpenBillIsThePortalHomesOwnBalanceAndDates(t *testing.T) {
	notes := &Notes{}

	bill, ok := CommunityConnectOpenBill("4101", communityConnectHomeOf(0),
		communityConnectDay(t, "2026-09-20"), notes)

	require.True(t, ok)
	require.Equal(t, "4101", bill.Subaccount)
	require.Equal(t, "4101:2026-09-05", bill.ExternalID)
	require.Equal(t, "90.00", bill.AmountDue.String())
	require.Equal(t, "2026-09-25", bill.DueOn)
	require.Equal(t, "2026-09-05", bill.IssuedOn)
	require.Equal(t, "USD", bill.Currency)
	require.Equal(t, "Open", bill.Status)
	require.Empty(t, notes.List())

	// The date the PDF endpoint takes is the timestamp the portal stated, not
	// the day this module renders for a household to read.
	var raw communityConnectRaw
	require.NoError(t, json.Unmarshal(bill.Raw, &raw))
	require.Equal(t, "4101", raw.Customer)
	require.Equal(t, "2026-09-05T00:00:00Z", raw.BillDate)
	require.Equal(t, "2026-09-05", raw.IssuedOn)
}

// This provider states the autopay flag and no separate autopay date, so the
// due date is the day the money moves — and only while the flag says it does.
func TestACommunityConnectAutopayDateIsTheDueDateAndOnlyWhileAutopayIsOn(t *testing.T) {
	on, ok := CommunityConnectOpenBill("4101", communityConnectHomeOf(0),
		communityConnectDay(t, "2026-09-20"), &Notes{})
	require.True(t, ok)
	require.Equal(t, "2026-09-25", on.AutopayOn)
	require.Equal(t, on.DueOn, on.AutopayOn)

	off := communityConnectHomeOf(0)
	off["AutoPay"] = false
	settled, ok := CommunityConnectOpenBill("4101", off,
		communityConnectDay(t, "2026-09-20"), &Notes{})
	require.True(t, ok)
	require.Equal(t, "", settled.AutopayOn)
}

func TestTheCommunityConnectStatusRuleIsTheBalanceAndTheDueDate(t *testing.T) {
	for _, one := range []struct {
		name    string
		balance any
		due     string
		today   string
		status  string
	}{
		{"owed and not yet due", 90.0, "2026-09-25T00:00:00Z", "2026-09-20", "Open"},
		{"owed and overdue", 90.0, "2026-09-10T00:00:00Z", "2026-09-20", "Open"},
		{"nothing owed and not yet due", 0, "2026-09-25T00:00:00Z", "2026-09-20", "Open"},
		{"nothing owed and the day has passed", 0, "2026-09-10T00:00:00Z", "2026-09-20", "Paid"},
		{"a credit on the account", -12.5, "2026-09-10T00:00:00Z", "2026-09-20", "Paid"},
	} {
		t.Run(one.name, func(t *testing.T) {
			home := communityConnectHomeOf(0)
			home["CurrentBalance"] = one.balance
			home["CurrentDueDate"] = one.due
			bill, ok := CommunityConnectOpenBill("4101", home,
				communityConnectDay(t, one.today), &Notes{})
			require.True(t, ok)
			require.Equal(t, one.status, bill.Status)
		})
	}
}

func TestACommunityConnectAccountWithNoReadableBalanceOrDueDateIsLeftOutWithANote(t *testing.T) {
	for _, one := range []struct {
		name  string
		field string
		value any
	}{
		{"a balance that will not read", "CurrentBalance", "not a number"},
		{"no balance at all", "CurrentBalance", nil},
		{"a due date nobody stated", "CurrentDueDate", ""},
	} {
		t.Run(one.name, func(t *testing.T) {
			home := communityConnectHomeOf(0)
			home[one.field] = one.value
			notes := &Notes{}

			bill, ok := CommunityConnectOpenBill("4101", home,
				communityConnectDay(t, "2026-09-20"), notes)

			require.False(t, ok)
			require.Empty(t, bill, "no bill at all, never a zero one")
			require.Len(t, notes.List(), 1)
			require.Contains(t, notes.List()[0], "is left out")
		})
	}
}

// A `Payments` row is money going the other way; taking it would double every
// bill.
func TestOnlyTheBillingsRowsOfACommunityConnectLedgerAreFiled(t *testing.T) {
	notes := &Notes{}

	bills := CommunityConnectHistory("4101", communityConnectStubLedger(0), "", notes)

	require.NotEmpty(t, bills)
	for _, bill := range bills {
		require.NotEqual(t, "30.00", bill.AmountDue.String(),
			"a payment must never be filed as a statement")
	}
	// The newest row in the ledger is a payment, so a reader that took both
	// would have answered it first.
	require.Equal(t, "4101:2026-09-05", bills[0].ExternalID)
}

func TestCommunityConnectChargesAreSortedNewestFirstAndCapped(t *testing.T) {
	bills := CommunityConnectHistory("4101", communityConnectStubLedger(0), "", &Notes{})

	require.Len(t, bills, communityConnectStatementsToRead)
	require.Equal(t, []string{"2026-09-05", "2026-08-05", "2026-07-05"},
		[]string{bills[0].IssuedOn, bills[1].IssuedOn, bills[2].IssuedOn})
	// A closed cycle is paid, and dated by the cycle it belongs to: this
	// portal states no due date for one it has already closed.
	require.Equal(t, "Paid", bills[1].Status)
	require.Equal(t, bills[1].IssuedOn, bills[1].DueOn)
}

func TestTheOpenCommunityConnectCycleIsNotFiledTwice(t *testing.T) {
	bills := CommunityConnectHistory("4101", communityConnectStubLedger(0), "4101:2026-09-05", &Notes{})

	require.Len(t, bills, 2)
	for _, bill := range bills {
		require.NotEqual(t, "4101:2026-09-05", bill.ExternalID)
	}
}

func TestACommunityConnectChargeThatWillNotReadIsLeftOutWithANoteAndNeverAsZero(t *testing.T) {
	ledger := communityConnectStubLedger(0)
	for _, row := range ledger {
		if row["Description"] == "Billings" && row["PeriodEnd"] == "2026-09-05T00:00:00Z" {
			row["Amount"] = "n/a"
		}
	}
	notes := &Notes{}

	bills := CommunityConnectHistory("4101", ledger, "", notes)

	require.Len(t, bills, 2)
	for _, bill := range bills {
		require.NotEqual(t, "0.00", bill.AmountDue.String())
		require.NotEqual(t, "4101:2026-09-05", bill.ExternalID)
	}
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no readable amount")
}

func TestCommunityConnectListsEveryAccountOnTheLoginAndFallsBackToTheSelectedOne(t *testing.T) {
	found := CommunityConnectSubaccounts(communityConnectHomeOf(0))

	require.Len(t, found, 2)
	require.Equal(t, "4101", found[0].ExternalID)
	require.Equal(t, "Example Service Address", found[0].Label)
	require.Equal(t, "••••1234", found[0].MaskedNumber)
	require.Equal(t, "4102", found[1].ExternalID)
	require.Equal(t, "••••5678", found[1].MaskedNumber)

	// A login that lists none still bills the account the portal home is about.
	alone := communityConnectHomeOf(0)
	delete(alone, "OtherUtilityCustomers")
	only := CommunityConnectSubaccounts(alone)
	require.Len(t, only, 1)
	require.Equal(t, "4101", only[0].ExternalID)
	require.Equal(t, "Example Service Address", only[0].Label)

	require.Empty(t, CommunityConnectSubaccounts(nil))
}

func TestCommunityConnectAnswersEachAccountsOpenBillAndItsRecentCharges(t *testing.T) {
	module := communityConnectModule()
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page:  stubCommunityConnectPage(nil),
		Notes: notes,
		Now:   func() time.Time { return communityConnectDay(t, "2026-09-20") },
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	// Two accounts, each answering its open cycle and the two closed ones
	// behind it — the open cycle's own ledger row is not filed a second time.
	require.Len(t, result.Bills, 6)
	require.Equal(t, []string{"4101:2026-09-05", "4101:2026-08-05", "4101:2026-07-05"},
		[]string{result.Bills[0].ExternalID, result.Bills[1].ExternalID, result.Bills[2].ExternalID})
	require.Equal(t, "Open", result.Bills[0].Status)
	require.Equal(t, "90.00", result.Bills[0].AmountDue.String())
	require.Equal(t, "2026-09-25", result.Bills[0].AutopayOn)
	require.Equal(t, "Paid", result.Bills[1].Status)

	// The second account is its own balance and its own autopay answer.
	require.Equal(t, "4102:2026-09-05", result.Bills[3].ExternalID)
	require.Equal(t, "Paid", result.Bills[3].Status)
	require.Equal(t, "", result.Bills[3].AutopayOn)

	require.Contains(t, notes.List()[len(notes.List())-1],
		"Our Community Connect answered 6 statements across 2 accounts")
}

func TestACommunityConnectPullReadsOnlyTheAccountsItWasAskedFor(t *testing.T) {
	module := communityConnectModule()

	result, err := module.FetchBills(Call{
		Page:        stubCommunityConnectPage(nil),
		Notes:       &Notes{},
		Subaccounts: []string{"4102"},
		Now:         func() time.Time { return communityConnectDay(t, "2026-09-20") },
	})

	require.NoError(t, err)
	require.Len(t, result.Bills, 3)
	for _, bill := range result.Bills {
		require.Equal(t, "4102", bill.Subaccount)
	}
}

func TestCommunityConnectListsTheAccountsOnTheLoginOverThePage(t *testing.T) {
	module := communityConnectModule()
	notes := &Notes{}

	found, err := module.Subaccounts(Call{Page: stubCommunityConnectPage(nil), Notes: notes})

	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, "4101", found[0].ExternalID)
	require.Contains(t, notes.List()[0], "lists 2 billed accounts")
}

// Every call here carries the bearer, so a page the app never handed one to has
// nothing to ask with (unlike T-Mobile, where most endpoints need no token).
func TestCommunityConnectSaysASignInIsOwedWhenTheAppSendsNoBearer(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(nil)
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	// The portal's own redirect: back to the sign-in, carrying where it came
	// from in the query. The note must name the page and not the query.
	page.OnGoto = func(string) error {
		page.Location = communityConnectTestRoot + "login?returnUrl=%2Fhome"
		return nil
	}
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Our Community Connect asked to sign in again", result.Reason)
	require.Contains(t, notes.List()[0], "the browser is not signed in")
	require.NotContains(t, notes.List()[0], "?", "the note names the page, not the query")
	require.NotContains(t, strings.Join(notes.List(), " "), "Bearer",
		"the token is never noted")
}

func TestARefusedCommunityConnectCallIsASignInOwedRatherThanAFailedPull(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(
		func(name, customer string, answer map[string]any) map[string]any {
			if name == "history" {
				return map[string]any{"status": 401, "json": nil, "excerpt": "Unauthorized",
					"base64": "", "timed_out": false, "no_token": false}
			}
			return answer
		})

	result, err := module.FetchBills(Call{
		Page: page, Notes: &Notes{},
		Now: func() time.Time { return communityConnectDay(t, "2026-09-20") },
	})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Our Community Connect refused the kept session", result.Reason)
	// Half a pull is worth filing: the open bill was read before the refusal.
	require.Len(t, result.Bills, 1)
}

// A call that runs out of its own deadline is not an account with no bills.
func TestACommunityConnectCallThatNeverAnswersIsASignInOwedAndNotAnEmptyPull(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(
		func(name, customer string, answer map[string]any) map[string]any {
			if name == "home" && customer == "" {
				return map[string]any{"status": 0, "json": nil,
					"excerpt": "the call did not answer in time",
					"base64":  "", "timed_out": true, "no_token": false}
			}
			return answer
		})
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.Empty(t, result.Bills)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Our Community Connect asked to sign in again", result.Reason)
	require.Contains(t, notes.List()[0], "never answered the accounts on this login")
}

func TestACommunityConnectStatementThatIsNotAPDFIsNoDocumentAndANote(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(
		func(name, customer string, answer map[string]any) map[string]any {
			if name == "statement" {
				// A portal whose session has lapsed answers a statement
				// request with its own sign-in page, under HTTP 200.
				answer["base64"] = base64.StdEncoding.EncodeToString(
					[]byte("<html>Sign in</html>"))
			}
			return answer
		})
	notes := &Notes{}

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, communityConnectStubBill())

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestACommunityConnectStatementIsAPDFNamedForItsAccountsLastFourAndItsDate(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(nil)

	document, err := module.FetchDocument(Call{Page: page, Notes: &Notes{}}, communityConnectStubBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "community-connect-4101-2026-09-05.pdf", document.Filename)
	require.True(t, len(document.Bytes) > 0)

	// The bill date goes to the endpoint as the portal stated it.
	asked, _ := page.Args[0].(map[string]any)
	address, _ := asked["url"].(string)
	require.Contains(t, address, "billDate="+url.QueryEscape("2026-09-05T00:00:00Z"))
}

func TestACommunityConnectStatementIsNotAskedForByAnUnaimedModule(t *testing.T) {
	page := stubCommunityConnectPage(nil)

	document, err := NewCommunityConnect().FetchDocument(
		Call{Page: page, Notes: &Notes{}}, communityConnectStubBill())

	require.NoError(t, err)
	require.Nil(t, document)
	require.Empty(t, page.Args)
}

// Every call goes to the API's own host with the slug as the first segment of
// the path. The app's host answers those paths with its own index.html, a 200
// that reads as an empty portal.
func TestEveryCommunityConnectCallGoesToTheAPIAtItsOwnDeployment(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(nil)

	_, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.NotEmpty(t, page.Args)
	for index, one := range page.Args {
		asked, isCall := one.(map[string]any)
		if !isCall {
			continue
		}
		address, _ := asked["url"].(string)
		require.Truef(t, strings.HasPrefix(address, "https://api.miviewpoint.net/"+communityConnectTestSite+"/"),
			"call %d should be the API at this deployment: %s", index+1, address)
		require.NotContainsf(t, address, communityConnectOtherSite,
			"call %d should be nobody else's deployment", index+1)
	}
	require.Equal(t, communityConnectTestRoot, page.Visited[0],
		"the pull lands on the aimed app root and never on a literal")
}

func TestEveryCommunityConnectCallIsHandedShapesAPageCanTake(t *testing.T) {
	module := communityConnectModule()
	page := stubCommunityConnectPage(nil)

	_, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.NotEmpty(t, page.Args, "the portal's calls all go through Evaluate")
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
	require.Empty(t, page.Scripts,
		"no init script: in Camoufox it would run in a world the app never calls")
}

const communityConnectTestRoot = "https://" + communityConnectTestSite + ".ourcommunityconnect.com/"

func communityConnectModule() *CommunityConnect {
	return NewCommunityConnect().WithSite(communityConnectTestSite).(*CommunityConnect)
}

func communityConnectDay(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	require.NoError(t, err)
	return parsed
}

// Invented throughout: the service addresses are labels rather than places.
var communityConnectStubAccounts = []struct {
	id, number, address, due string
	balance                  float64
	autopay                  bool
	charge                   float64
}{
	{id: "4101", number: "1000-1234", address: "Example Service Address",
		due: "2026-09-25T00:00:00Z", balance: 90.0, autopay: true, charge: 90.0},
	{id: "4102", number: "1000-5678", address: "Second Service Address",
		due: "2026-09-10T00:00:00Z", balance: 0, autopay: false, charge: 40.0},
}

// communityConnectHomeOf is one account's portal home, in the shape the real
// one answers: full ISO timestamps, and figures as JSON numbers.
func communityConnectHomeOf(which int) map[string]any {
	account := communityConnectStubAccounts[which]
	var others []any
	for _, one := range communityConnectStubAccounts {
		others = append(others, map[string]any{
			"ID": one.id, "CustomerNumber": one.number,
			"Name": "Invented Household", "ServiceAddress": one.address,
			"Balance": one.balance,
		})
	}
	return map[string]any{
		"CustomerID":             account.id,
		"CustomerNumber":         account.number,
		"Name":                   "Invented Household",
		"CurrentBalance":         account.balance,
		"AccountBalance":         account.balance,
		"CurrentDueDate":         account.due,
		"CurrentBillDate":        "2026-09-05T00:00:00Z",
		"LastPaymentAmount":      30.0,
		"LastPaymentDate":        "2026-08-20T00:00:00Z",
		"ServiceAddress":         account.address,
		"ServiceCity":            "Anytown",
		"AutoPay":                account.autopay,
		"OtherUtilityCustomers":  others,
		"EnrolledInPaperless":    true,
		"AccountsReceivableFlag": false,
	}
}

// communityConnectStubLedger is one account's summarised transactions as the
// portal lists them: charges and payments interleaved, oldest first, and the
// newest row of all a payment — so a reader that filed both would answer that
// one first and lose a charge to the cap.
func communityConnectStubLedger(which int) []map[string]any {
	charge := communityConnectStubAccounts[which].charge
	var rows []map[string]any
	for index, month := range []string{"06", "07", "08", "09"} {
		rows = append(rows, map[string]any{
			"Description": "Billings",
			"PeriodEnd":   "2026-" + month + "-05T00:00:00Z",
			"Amount":      charge - float64(3-index),
			"Balance":     charge,
		})
		rows = append(rows, map[string]any{
			"Description": "Payments",
			"PeriodEnd":   "2026-" + month + "-20T00:00:00Z",
			"Amount":      30.0,
			"Balance":     0.0,
		})
	}
	return rows
}

// communityConnectStubBill is a filed bill as FetchDocument is handed one.
func communityConnectStubBill() Bill {
	bill := Bill{Subaccount: "4101", ExternalID: "4101:2026-09-05", IssuedOn: "2026-09-05"}
	bill.Raw, _ = json.Marshal(communityConnectRaw{
		Customer: "4101", BillDate: "2026-09-05T00:00:00Z", IssuedOn: "2026-09-05",
	})
	return bill
}

// `rewrite` is how a test makes one call answer differently — a 401, a call
// that never came back, a document that is not a PDF — without restating the
// others.
func stubCommunityConnectPage(
	rewrite func(name, customer string, answer map[string]any) map[string]any,
) *browser.StubPage {
	page := &browser.StubPage{Location: communityConnectTestRoot + "home"}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		// Signed in wherever the portal has not sent the tab to its sign-in.
		if script == communityConnectHasToken {
			return !strings.Contains(page.Location, "/login"), nil
		}
		asked, _ := arg.(map[string]any)
		address, _ := asked["url"].(string)
		name, customer := communityConnectStubCall(script, asked, address)
		if name == "" {
			return nil, nil
		}
		answer := communityConnectStubAnswer(name, customer)
		if rewrite != nil {
			answer = rewrite(name, customer, answer)
		}
		return answer, nil
	}
	return page
}

// communityConnectStubCall is which of the portal's calls this address is, and
// whose account it is about.
func communityConnectStubCall(script string, asked map[string]any, address string) (string, string) {
	parsed, err := url.Parse(address)
	if err != nil {
		return "", ""
	}
	query := parsed.Query()
	switch {
	case script != plainPageCall || asked["token"] == nil:
		return "", ""
	case asked["read"] == "bytes":
		return "statement", query.Get("customerId")
	case strings.Contains(parsed.Path, "UtilityPortalHome"):
		return "home", query.Get("selectedCustomerId")
	case strings.Contains(parsed.Path, "GetCustomerSummarizedTransactions"):
		inside := parsed.Path[strings.Index(parsed.Path, "customerID=")+len("customerID="):]
		return "history", strings.TrimSuffix(inside, ")")
	}
	return "", ""
}

func communityConnectStubAnswer(name, customer string) map[string]any {
	which := 0
	for index, one := range communityConnectStubAccounts {
		if one.id == customer {
			which = index
		}
	}
	switch name {
	case "home":
		return communityConnectBody(communityConnectHomeOf(which), "")
	case "history":
		var value []any
		for _, row := range communityConnectStubLedger(which) {
			value = append(value, row)
		}
		return communityConnectBody(map[string]any{"value": value}, "")
	case "statement":
		return communityConnectBody(nil,
			base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invented")))
	}
	return map[string]any{"status": 404, "json": nil, "excerpt": "no such call",
		"base64": "", "timed_out": false, "no_token": false}
}

func communityConnectBody(body map[string]any, bytes string) map[string]any {
	return map[string]any{
		"status": 200, "json": body, "excerpt": "", "base64": bytes,
		"timed_out": false, "no_token": false,
	}
}

// Autopay has run: the portal home says the balance is 0 and nothing about
// what was billed. The cycle is filed at what its Billings row charged, once,
// with the due date and autopay only the home states.
func TestACommunityConnectCyclePaidByAutopayIsFiledAtWhatItCharged(t *testing.T) {
	open := Bill{ExternalID: "7:2026-08-31", DueOn: "2026-09-20", AutopayOn: "2026-09-20",
		AmountDue: domain.MustFromString("0.00"), Status: "Paid"}
	history := []Bill{
		{ExternalID: "7:2026-08-31", DueOn: "2026-08-31", AmountDue: domain.MustFromString("111.11"), Status: "Paid"},
		{ExternalID: "7:2026-07-31", DueOn: "2026-07-31", AmountDue: domain.MustFromString("122.22"), Status: "Paid"},
	}

	cycles := CommunityConnectCycles(open, true, history)

	require.Len(t, cycles, 2)
	require.Equal(t, "7:2026-08-31", cycles[0].ExternalID)
	require.Equal(t, "111.11", cycles[0].AmountDue.String())
	require.Equal(t, "2026-09-20", cycles[0].DueOn)
	require.Equal(t, "2026-09-20", cycles[0].AutopayOn)
	require.Equal(t, "7:2026-07-31", cycles[1].ExternalID)
}

// Money still owed is what is due, whatever the cycle charged.
func TestACommunityConnectCycleStillOwedKeepsItsBalance(t *testing.T) {
	open := Bill{ExternalID: "7:2026-08-31", AmountDue: domain.MustFromString("40.00"), Status: "Open"}
	history := []Bill{{ExternalID: "7:2026-08-31", AmountDue: domain.MustFromString("111.11"), Status: "Paid"}}

	cycles := CommunityConnectCycles(open, true, history)

	require.Equal(t, []Bill{open}, cycles)
}
