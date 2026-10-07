package billers

import (
	"encoding/base64"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// The field names are GetCustomerDetail's own; every value is invented.
const truGreenDetailJSON = `{
  "CustomerNumber": 70001234,
  "BadDebtAmount": 0,
  "InvoiceYears": [2025, 2026],
  "Payments": [{"amount": 60.00, "cashType": "CC", "date": "2026-05-05", "referenceNumber": "R-1", "subType": "EP"}],
  "SalesAgreements": [
    {
      "salesAgrNum": "SA-100", "saTemplateDescription": "Healthy Lawn Plan", "customerNumber": 70001234,
      "arBalance": 60.00, "autoChgAcctTyp": "CCD", "autoChgOptTyp": "ACC",
      "Invoices": [
        {"invoiceNum": "I-1", "invoiceAmount": 60.00, "invoiceBalance": 0, "invoiceDate": "2026-05-04",
         "invoiceOpen": "N", "invoiceStatus": "SKP", "workOrderRef": "WO-1001", "svcDescription": "Round 2"},
        {"invoiceNum": "I-2", "invoiceAmount": 60.00, "invoiceBalance": 0, "invoiceDate": "2025-06-16",
         "invoiceOpen": "N", "invoiceStatus": "SKP", "workOrderRef": "WO-0901"},
        {"invoiceNum": "I-3", "invoiceAmount": 60.00, "invoiceBalance": 60.00, "invoiceDate": "2026-09-14",
         "invoiceOpen": "Y", "workOrderRef": "WO-1003"},
        {"invoiceNum": "I-4", "invoiceAmount": 0, "invoiceBalance": 0, "invoiceDate": "2026-07-20",
         "invoiceOpen": "N", "workOrderRef": "WO-1002"}
      ]
    },
    {
      "salesAgrNum": "SA-200", "saTemplateDescription": "Tree and Shrub Care", "customerNumber": "0070001234",
      "arBalance": 0,
      "Invoices": [
        {"invoiceNum": "I-5", "invoiceAmount": 35, "invoiceBalance": 0, "invoiceDate": "2026-05-04",
         "invoiceOpen": "N", "workOrderRef": "WO-2001"},
        {"invoiceNum": "I-6", "invoiceAmount": "n/a", "invoiceDate": "2026-06-01", "invoiceOpen": "N"},
        {"invoiceNum": "I-7", "invoiceAmount": 40, "invoiceDate": "2026-08-03", "workOrderRef": "WO-2002"},
        {"invoiceAmount": 50, "invoiceBalance": 0, "invoiceDate": "2026-07-06", "invoiceOpen": "N",
         "workOrderRef": "WO-2003"},
        {"invoiceAmount": 20, "invoiceBalance": 0, "invoiceDate": "2026-07-06", "invoiceOpen": "N"}
      ]
    }
  ]
}`

func truGreenDetail(t *testing.T) TruGreenDetail {
	t.Helper()
	var detail TruGreenDetail
	require.NoError(t, httpx.DecodeJSON([]byte(truGreenDetailJSON), &detail))
	return detail
}

func TestTruGreenFilesEachSettledInvoiceAsAPaidBillDatedByItsInvoice(t *testing.T) {
	notes := &Notes{}
	bills := TruGreenBillsFromDetail(truGreenDetail(t), notes)

	require.Len(t, bills, 4)

	older := bills[0]
	require.Equal(t, "70001234", older.Subaccount)
	require.Equal(t, "70001234:I-2", older.ExternalID)
	require.Equal(t, "I-2", older.Invoice)
	require.Equal(t, "2025-06-16", older.DueOn)
	require.Equal(t, "60.00", older.AmountDue.String())
	require.Equal(t, "Paid", older.Status)

	// Two plans' visits invoiced on one day are charged separately, so they
	// are two bills, each with its own invoice and work order, and its plan's
	// customer number as that plan writes it, for the statement's address.
	for i, want := range []struct {
		invoice, amount, customer, workOrder string
	}{
		{"I-1", "60.00", "70001234", "WO-1001"},
		{"I-5", "35.00", "0070001234", "WO-2001"},
	} {
		one := bills[1+i]
		require.Equal(t, "70001234:"+want.invoice, one.ExternalID)
		require.Equal(t, want.invoice, one.Invoice)
		require.Equal(t, "2026-05-04", one.IssuedOn)
		require.Equal(t, "2026-05-04", one.DueOn)
		require.Equal(t, want.amount, one.AmountDue.String())
		require.Equal(t, "Paid", one.Status)
		require.Empty(t, one.AutopayOn, "TruGreen states no payment day")
		var raw truGreenRaw
		require.NoError(t, json.Unmarshal(one.Raw, &raw))
		require.Equal(t, truGreenRaw{Customer: want.customer, WorkOrder: want.workOrder}, raw)
	}

	// An invoice with no number is known by its work order.
	unnumbered := bills[3]
	require.Equal(t, "70001234:WO-2003", unnumbered.ExternalID)
	require.Equal(t, "WO-2003", unnumbered.Invoice)
	require.Equal(t, "2026-07-06", unnumbered.DueOn)
	require.Equal(t, "50.00", unnumbered.AmountDue.String())

	require.Equal(t, []string{
		"TruGreen shows an open invoice of 60.00 dated 2026-09-14 and states no due date for it, " +
			"so it is not filed as a bill",
		`a TruGreen invoice carried no readable date or amount ("2026-06-01", "n/a"); it is left out`,
		// No open flag and no balance: nothing says it is settled.
		"TruGreen shows an open invoice of 40.00 dated 2026-08-03 and states no due date for it, " +
			"so it is not filed as a bill",
		"a settled TruGreen invoice of 20.00 dated 2026-07-06 carried no invoice number or work order; " +
			"it is left out",
	}, notes.List())
}

func TestTruGreenFilesAnInvoiceListedTwiceOnce(t *testing.T) {
	detail := TruGreenDetail{CustomerNumber: "70001234", SalesAgreements: []TruGreenAgreement{{
		Invoices: []TruGreenInvoice{
			{Number: "I-9", Amount: json.Number("25.00"), Balance: json.Number("0"),
				Date: "2026-04-14", Open: "N", WorkOrderRef: "WO-3001"},
			{Number: "I-9", Amount: json.Number("25.00"), Balance: json.Number("0"),
				Date: "2026-04-14", Open: "N", WorkOrderRef: "WO-3001"},
		},
	}}}
	notes := &Notes{}
	bills := TruGreenBillsFromDetail(detail, notes)
	require.Len(t, bills, 1)
	require.Equal(t, []string{"TruGreen listed the invoice I-9 twice; the first is kept"}, notes.List())
}

func TestATruGreenInvoiceIsOpenUnlessThePortalReadablySaysItIsSettled(t *testing.T) {
	for _, tc := range []struct {
		name    string
		invoice TruGreenInvoice
		open    bool
	}{
		{"settled", TruGreenInvoice{Open: "N", Balance: json.Number("0")}, false},
		{"settled with no balance written", TruGreenInvoice{Open: "N"}, false},
		{"flagged open", TruGreenInvoice{Open: "Y", Balance: json.Number("0")}, true},
		{"flagged settled with a balance owed", TruGreenInvoice{Open: "N", Balance: json.Number("10.00")}, true},
		{"no flag, nothing owed", TruGreenInvoice{Balance: json.Number("0")}, false},
		{"no flag, no balance", TruGreenInvoice{}, true},
		{"a credit balance", TruGreenInvoice{Open: "N", Balance: json.Number("-5.00")}, false},
	} {
		require.Equal(t, tc.open, truGreenInvoiceOpen(tc.invoice), tc.name)
	}
}

func TestTruGreenBillsOneAccountPerCustomerNamedByItsPlans(t *testing.T) {
	detail := truGreenDetail(t)

	found := TruGreenSubaccounts([]string{"0070001234", "0070005678"}, detail)
	require.Equal(t, []Subaccount{
		{ExternalID: "70001234", Label: "Healthy Lawn Plan, Tree and Shrub Care", MaskedNumber: "••••1234"},
		{ExternalID: "70005678", Label: "TruGreen lawn service", MaskedNumber: "••••5678"},
	}, found)

	// With nothing kept in storage, the detail's own customer stands.
	require.Equal(t, []Subaccount{
		{ExternalID: "70001234", Label: "Healthy Lawn Plan, Tree and Shrub Care", MaskedNumber: "••••1234"},
	}, TruGreenSubaccounts(nil, detail))
}

func TestTruGreenRecordsOnlyTheAppsCustomerDetailCall(t *testing.T) {
	for _, address := range []string{
		"https://api.trugreen.com/account/GetCustomerDetail_ver7",
		"https://api.trugreen.com/account/GetCustomerDetail_ver8",
		"https://API.trugreen.com/account/getcustomerdetail_ver7?x=1",
	} {
		require.True(t, truGreenDetailCall.MatchString(address), address)
	}
	for _, address := range []string{
		"https://api.trugreen.com/account/GetCustomerDetail",
		"https://api.trugreen.com/account/GetCustomerDetail_ver7Extra",
		"https://api.trugreen.com/account/GetCustomerDetailByParty_ver7",
		"https://api.trugreen.com.example.test/account/GetCustomerDetail_ver7",
		"http://api.trugreen.com/account/GetCustomerDetail_ver7",
		"https://www.trugreen.com/my-account/my-services",
	} {
		require.False(t, truGreenDetailCall.MatchString(address), address)
	}
}

// stubTruGreenPage answers the services page with the detail the app receives
// and the storage entry it keeps.
func stubTruGreenPage(t *testing.T, status int, detail string) *browser.StubPage {
	t.Helper()
	page := &browser.StubPage{}
	page.OnGoto = func(address string) error {
		if address == truGreenServices {
			page.Respond(browser.StubResponse{
				Address: "https://api.trugreen.com/account/GetCustomerDetail_ver7", Code: status,
				Payload: []byte(detail),
			})
		}
		return nil
	}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		asked, _ := arg.(map[string]any)
		if asked["pattern"] == "^tg_cust$" {
			return map[string]any{"found": []any{map[string]any{
				"store": "localStorage", "key": "tg_cust",
				"value": `{"customers":[{"customerNumber":"0070001234","first_name":"Pat"}]}`,
			}}}, nil
		}
		for statement, body := range map[string]string{
			"70001234/workorder/WO-1001":   "%PDF-1.7 invented lawn round",
			"0070001234/workorder/WO-2001": "%PDF-1.7 invented shrub visit",
		} {
			if asked["url"] == "https://www.trugreen.com/document/"+statement {
				return map[string]any{
					"status": 200, "type": "application/pdf",
					"base64": base64.StdEncoding.EncodeToString([]byte(body)),
				}, nil
			}
		}
		return map[string]any{"status": 404}, nil
	}
	return page
}

func TestTruGreenReadsTheResponseTheServicesPageReceived(t *testing.T) {
	page := stubTruGreenPage(t, 200, truGreenDetailJSON)
	module := NewTruGreen()

	notes := &Notes{}
	accounts, err := module.Subaccounts(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, "70001234", accounts[0].ExternalID)

	notes = &Notes{}
	pulled, err := module.FetchBills(Call{Page: page, Notes: notes, Subaccounts: []string{"70001234"}})
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Len(t, pulled.Bills, 4)
	require.Contains(t, notes.List(), "TruGreen answered 4 paid bills")
	require.Equal(t, []string{truGreenServices, truGreenServices}, page.Visited)

	// Each of the day's invoices fetches its own work order's statement.
	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, pulled.Bills[1])
	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "trugreen-1234-2026-05-04-I-1.pdf", document.Filename)
	require.Equal(t, "%PDF-1.7 invented lawn round", string(document.Bytes))

	document, err = module.FetchDocument(Call{Page: page, Notes: notes}, pulled.Bills[2])
	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "trugreen-1234-2026-05-04-I-5.pdf", document.Filename)
	require.Equal(t, "%PDF-1.7 invented shrub visit", string(document.Bytes))

	// An invoice whose work order answers no PDF has no statement, and says so.
	notes = &Notes{}
	document, err = module.FetchDocument(Call{Page: page, Notes: notes}, pulled.Bills[0])
	require.NoError(t, err)
	require.Nil(t, document)
	require.Len(t, notes.List(), 1)
}

func TestTruGreenAsksForASignInWhenTheAppIsRefusedOrBouncedToTheForm(t *testing.T) {
	module := NewTruGreen()

	refused := stubTruGreenPage(t, 403, `{"error":{"code":"XX30002","message":"Anonymous user."}}`)
	pulled, err := module.FetchBills(Call{Page: refused, Notes: &Notes{}})
	require.NoError(t, err)
	require.True(t, pulled.NeedsSignIn)

	bounced := &browser.StubPage{}
	bounced.OnGoto = func(string) error {
		bounced.Location = "https://www.trugreen.com/my-account/login"
		return nil
	}
	pulled, err = module.FetchBills(Call{Page: bounced, Notes: &Notes{}})
	require.NoError(t, err)
	require.True(t, pulled.NeedsSignIn)
	require.Zero(t, bounced.Slept, "a page bounced to the form is not waited on")

	_, err = module.Subaccounts(Call{Page: bounced, Notes: &Notes{}})
	require.ErrorIs(t, err, ErrNeedsSignIn)
}

func TestTruGreenSaysSoWhenTheServicesPageNeverAsksForTheDetail(t *testing.T) {
	page := &browser.StubPage{}
	notes := &Notes{}
	pulled, err := NewTruGreen().FetchBills(Call{Page: page, Notes: notes})
	require.NoError(t, err)
	require.False(t, pulled.NeedsSignIn)
	require.Empty(t, pulled.Bills)
	require.Equal(t, truGreenDetailWait, page.Slept)
	require.Contains(t, notes.List()[0], "did not load the account's invoices")
}

func TestTruGreenEntersAtItsAccountPageAndLandsThere(t *testing.T) {
	module := NewTruGreen()
	require.Equal(t, "https://www.trugreen.com/myaccount", module.SignInURL())
	require.Equal(t, module.SignInURL(), module.LandingURL())
	require.Equal(t, "https://www.trugreen.com/my-account/account-summary", module.AccountPage)
	require.False(t, module.AccountArea(module.SignInURL()), "the sign-in entry is not inside")
	require.True(t, module.AccountArea(truGreenServices))
}

// Without an account area, a signed-in landing on the account summary reads
// as an unrecognised page.
func TestTruGreenIsInsideUnderMyAccountAndNotAtItsLoginPage(t *testing.T) {
	module := NewTruGreen()
	require.NotNil(t, module.AccountArea)

	for _, inside := range []string{
		"https://www.trugreen.com/my-account/account-summary",
		"https://www.trugreen.com/my-account/account-summary?tab=billing",
		"https://www.trugreen.com/my-account/billing",
	} {
		require.Equal(t, StateSignedIn, StateOf(Form{}, inside, module.AccountArea).State, inside)
	}
	for _, outside := range []string{
		"https://www.trugreen.com/my-account/login",
		"https://www.trugreen.com/my-account/login?returnUrl=%2Fmy-account%2Faccount-summary",
		"https://www.trugreen.com/my-account/Login",
		"https://www.trugreen.com/my-account/logout",
		"https://www.trugreen.com/my-account/register",
		"https://www.trugreen.com/my-account/forgot-password",
		"https://www.trugreen.com/my-account/",
		"https://www.trugreen.com/myaccount",
		"https://www.trugreen.com/",
		"https://www.trugreen.com.example.test/my-account/account-summary",
		"http://www.trugreen.com/my-account/account-summary",
	} {
		require.Equal(t, StateInteractive, StateOf(Form{}, outside, module.AccountArea).State, outside)
	}

	// The form is read before the address, whatever the address.
	require.Equal(t, StatePassword,
		StateOf(Form{Password: true, Username: true}, "https://www.trugreen.com/my-account/account-summary",
			module.AccountArea).State)
	require.True(t, module.AccountArea(module.AccountPage), "the account page is inside the account area")
}
