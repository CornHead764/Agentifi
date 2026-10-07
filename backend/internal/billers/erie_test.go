package billers

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// The shapes are the portal's (docs/connectors/providers.md); every value is
// invented.

const (
	erieTestAccount = "00000000-0000-4000-8000-000000000001"
	erieTestPolicy  = "Q000001234"
	// The documents call hyphenates the same number.
	erieTestDocumentPolicy = "Q00-0001234"
)

func TestTheErieAccountAreaIsThePortalAndNotTheWayIn(t *testing.T) {
	inside := NewErie().AccountArea
	for _, address := range []string{
		"https://www.erieinsurance.com/Customer/ManageAccount/account",
		"https://www.erieinsurance.com/Customer/ManageAccount/account?Mobile=true",
		"https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Index?transferKey=x&userRole=Inquiry",
		"https://www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account/" + erieTestAccount + "/Policy/0",
		"https://www.erieinsurance.com/account",
		"https://www.erieinsurance.com/account/?tab=overview",
	} {
		require.Truef(t, inside(address), "at %s", address)
	}
	for _, address := range []string{
		"https://www.erieinsurance.com/",
		"https://www.erieinsurance.com/Account/Login/Login",
		"https://www.erieinsurance.com/Account/Login/Login?ReturnUrl=%2Faccount",
		"https://www.erieinsurance.com/accounting",
		"https://authnprd.erieinsurance.com/saml20/idp/sso",
		"https://custsso.erieinsurance.com/my.logout.php3",
	} {
		require.Falsef(t, inside(address), "at %s", address)
	}
}

func TestTheAccountIdAndTheBillingLinksComeOffTheLandingPage(t *testing.T) {
	found := ErieAccountFromLinks([]string{
		"https://www.erieinsurance.com/Customer/ManageAccount/account",
		"https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Account/" + erieTestAccount +
			"/Policy/" + erieTestPolicy + "/VendorId/STG",
		"https://www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account/" + erieTestAccount + "/Policy/0",
	})

	require.Equal(t, erieTestAccount, found.ID)
	require.Contains(t, found.Billing, erieTestPolicy)

	// A household whose landing page shows the documents link alone still has
	// an account id; the billing link is rebuilt from it.
	only := ErieAccountFromLinks([]string{
		"https://www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account/" + erieTestAccount + "/Policy/0",
	})
	require.Equal(t, erieTestAccount, only.ID)
	require.Empty(t, only.Billing)
	require.Equal(t,
		"https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Account/"+erieTestAccount+
			"/Policy/"+erieTestPolicy+"/VendorId/STG",
		erieInquiryLink(erieTestAccount, erieTestPolicy))
}

func TestEveryInForcePolicyIsABilledAccountUnderItsOwnName(t *testing.T) {
	found := ErieSubaccountsFromPolicies([]map[string]any{
		{"policyNumber": erieTestPolicy, "productName": "Auto", "productGroupName": "Personal Vehicles"},
		{"policyNumber": "Q000005678", "productName": "Home", "productGroupName": "Personal Property"},
		{"policyNumber": "", "productName": "Umbrella"},
	})

	require.Len(t, found, 2)
	require.Equal(t, erieTestPolicy, found[0].ExternalID)
	require.Equal(t, "Auto policy", found[0].Label)
	require.Equal(t, "••••1234", found[0].MaskedNumber)
	require.Equal(t, "Home policy", found[1].Label)
}

func erieTestRead() ErieRead {
	return ErieRead{
		Policy:  erieTestPolicy,
		Account: erieTestAccount,
		Terms: []map[string]any{
			{
				"policy":                  map[string]any{"policyNumber": erieTestPolicy, "effectiveDate": "2026-03-01"},
				"expirationDate":          "2027-03-01",
				"policyStatusDescription": "In Force",
				"policyMinDue":            120.0,
				"paymentAmount":           240.0,
				"currentBalance":          360.0,
				"payPlanDescription":      "Annual (Plan A)",
			},
			{
				"policy":                  map[string]any{"policyNumber": erieTestPolicy, "effectiveDate": "2025-03-01"},
				"expirationDate":          "2026-03-01",
				"policyStatusDescription": "Expired",
				"policyMinDue":            0.0,
				"paymentAmount":           230.0,
			},
		},
		Activity: []map[string]any{
			{
				"transactionCategory":      "Payments",
				"transactionType":          "PAY",
				"transactionDescription":   "Payment",
				"transactionAmount":        -240.0,
				"transactionEffectiveDate": "2026-04-10",
			},
			{
				"transactionCategory":      "Policy Activity",
				"transactionType":          "REN",
				"transactionDescription":   "Renewal Premium",
				"transactionAmount":        480.0,
				"transactionEffectiveDate": "2026-03-01",
			},
		},
		Installments: map[string]any{"currentDate": "9/19/2026", "isAutoPay": false},
		Documents: []map[string]any{
			{
				"documentHandle": "100200300", "documentId": "abcdef01", "documentType": "Invoice",
				"printDate": "08/25/2026", "effectiveDate": "8/25/2026", "policyNumber": erieTestDocumentPolicy,
			},
			{
				"documentHandle": "100200200", "documentId": "abcdef02", "documentType": "Invoice",
				"printDate": "03/25/2026", "effectiveDate": "3/25/2026", "policyNumber": erieTestDocumentPolicy,
			},
			{
				"documentHandle": "100200100", "documentId": "abcdef03", "documentType": "Declarations",
				"printDate": "03/01/2026", "policyNumber": erieTestDocumentPolicy,
			},
		},
		InvoiceText: map[string]string{
			"100200300": "ERIE INSURANCE\nPolicy Auto (" + erieTestDocumentPolicy + ")\n" +
				"Due Date 09/15/2026\nMinimum Due $120.00\nTotal Due $360.00\n",
			"100200200": "ERIE INSURANCE\nPolicy Auto (" + erieTestDocumentPolicy + ")\n" +
				"Due Date 04/15/2026\nMinimum Due $240.00\nTotal Due $240.00\n",
		},
		TileDue: "Minimum due by 9/15/2026",
		Window:  [2]string{"2025-08-20", "2026-09-19"},
	}
}

// t0 is the day the fixtures are written around.
const t0 = "2026-09-19"

func erieDay(value string) time.Time {
	parsed, _ := time.Parse("2006-01-02", value)
	return parsed
}

func TestTheAmountOwedIsTheNewestInvoicesBillAndNotASecondOneBesideIt(t *testing.T) {
	notes := &Notes{}

	bills := ErieBillsFromPolicy(erieTestRead(), notes)

	require.Len(t, bills, 2, "one bill per invoice, and the amount owed folded into the one it belongs to")
	require.Equal(t, erieTestPolicy, bills[0].Subaccount)
	require.Equal(t, "2026-09-15", bills[0].DueOn, "the due date is the invoice's own; no call carries one")
	require.Equal(t, "2026-08-25", bills[0].IssuedOn)
	require.Equal(t, "120.00", bills[0].AmountDue.String(), "what the portal says is owed wins over the invoice's total")
	require.Equal(t, "Open", bills[0].Status)
	require.Equal(t, "USD", bills[0].Currency)
	require.Empty(t, bills[0].AutopayOn, "this policy is not enrolled in automatic payments")
	require.Equal(t, erieTestPolicy+":2026-08-25", bills[0].ExternalID)

	// The spring invoice, settled by the payment that followed it.
	require.Equal(t, "2026-04-15", bills[1].DueOn)
	require.Equal(t, "240.00", bills[1].AmountDue.String())
	require.Equal(t, "Paid", bills[1].Status)

	// And the statement each of them carries.
	var raw erieRaw
	require.NoError(t, json.Unmarshal(bills[0].Raw, &raw))
	require.Equal(t, "100200300", raw.DocumentHandle)
	require.Equal(t, erieTestDocumentPolicy, raw.DocumentPolicy)
	require.Equal(t, erieTestAccount, raw.OnlineAccount)
	require.Equal(t, "2025-08-20", raw.StartDate)
	require.Empty(t, notes.List())
}

func TestAPolicyOnAutomaticPaymentsPaysOnTheDayItIsDue(t *testing.T) {
	// The token id is the portal's way of saying a payment method is on file
	// for the draft; the installments call says the same thing in a flag.
	read := erieTestRead()
	read.Terms[0]["recurringEFTTokenId"] = "token-nobody-should-see"

	bills := ErieBillsFromPolicy(read, &Notes{})

	require.Equal(t, "2026-09-15", bills[0].AutopayOn)
	require.Empty(t, bills[1].AutopayOn, "a bill that is already paid pays again on no day at all")

	read.Terms[0]["recurringEFTTokenId"] = ""
	read.Installments = map[string]any{"isAutoPay": true}
	require.Equal(t, "2026-09-15", ErieBillsFromPolicy(read, &Notes{})[0].AutopayOn)
}

func TestAnInvoiceWithNoTextFallsBackToThePayPlansOwnInstalment(t *testing.T) {
	// A scanned invoice, or one whose PDF would not open: the figure comes
	// from the term instead, and the due date from the tile.
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 1, "the spring invoice has no due date anywhere and is not filed")
	require.Equal(t, "120.00", bills[0].AmountDue.String(), "what is owed is still what is owed")
	require.Equal(t, "2026-09-15", bills[0].DueOn, "read off the summary tile")
	require.Len(t, notes.List(), 2)
	require.Contains(t, notes.List()[0], "2026-03-25")
	require.Contains(t, notes.List()[0], "no due date")
	require.Contains(t, notes.List()[0], "not filed")
	require.Contains(t, notes.List()[1], "keys and types only")
}

func TestNoErieBillIsEverHandedOnWithoutADueDate(t *testing.T) {
	// Every way a date can be missing at once: no PDF text, no tile, no
	// installments, something owed and invoices to fold it into.
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	read.TileDue = ""

	for _, bill := range ErieBillsFromPolicy(read, &Notes{}) {
		require.NotEmpty(t, bill.DueOn)
	}
}

func TestAnInvoiceWithNoTextIsDueOnTheInstallmentThatFallsInItsCycle(t *testing.T) {
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	read.TileDue = "Minimum due"
	read.Installments = map[string]any{
		"currentDate": "9/19/2026",
		"installments": []any{
			map[string]any{"dueDate": "4/15/2026", "status": "Paid"},
			map[string]any{"dueDate": "9/15/2026", "status": "Billed"},
		},
		"futureInstallments": []any{
			map[string]any{"dueDate": "10/15/2026"},
			map[string]any{"dueDate": "11/15/2026"},
		},
		"isAutoPay": false,
	}
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 2)
	require.Equal(t, "2026-09-15", bills[0].DueOn, "the first installment not yet paid, with the tile silent")
	require.Equal(t, "120.00", bills[0].AmountDue.String())
	require.Equal(t, "2026-04-15", bills[1].DueOn, "the installment that fell due after the spring invoice")
	require.Empty(t, notes.List())
}

func TestAnInstallmentAfterTheNextInvoiceIsNotAnOlderInvoicesDueDate(t *testing.T) {
	// Nothing owed, and the only installment left is a later cycle's: neither
	// invoice may borrow it.
	read := erieTestRead()
	read.Terms[0]["policyMinDue"] = 0.0
	read.InvoiceText = map[string]string{}
	read.TileDue = "Minimum due"
	read.Installments = map[string]any{
		"futureInstallments": []any{map[string]any{"installmentDueDate": "12/15/2026"}},
	}
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Empty(t, bills)
	require.Len(t, notes.List(), 3, "one per invoice and one saying what was read")
}

func TestTheAmountOwedIsNeverDueOnAnOlderInvoicesDate(t *testing.T) {
	// The newest invoice's PDF has no text; the spring one's does. Its due
	// date is the spring bill's, not the one owed now.
	read := erieTestRead()
	delete(read.InvoiceText, "100200300")
	read.TileDue = "Minimum due"
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 1)
	require.Equal(t, "2026-04-15", bills[0].DueOn)
	require.Equal(t, "240.00", bills[0].AmountDue.String())
	require.Len(t, notes.List(), 2)
	require.Contains(t, notes.List()[0], "amount owed")
	require.Contains(t, notes.List()[0], "not filed")
}

func TestAnAmountOwedWithNoDueDateAnywhereCarriesNoneAndSaysSo(t *testing.T) {
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	read.TileDue = "Minimum due"
	read.Documents = nil
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Empty(t, bills)
	require.Len(t, notes.List(), 2)
	require.Contains(t, notes.List()[0], "no due date")
	require.Contains(t, notes.List()[0], "ending 1234")
	for _, note := range notes.List() {
		require.NotContains(t, note, "120", "a note is read by a person and carries no figures")
	}
}

func TestTheNoteForAnUndatedBillNamesEverySourcesShapeAndNoneOfItsValues(t *testing.T) {
	read := erieTestRead()
	read.InvoiceText = map[string]string{"100200300": "ERIE INSURANCE\nMinimum Due $120.00\n"}
	read.TileDue = "Minimum due"
	read.Installments = map[string]any{"currentDate": "9/19/2026", "isAutoPay": false, "planCode": "Z9"}
	notes := &Notes{}

	ErieBillsFromPolicy(read, notes)

	what := notes.List()[len(notes.List())-1]
	require.Contains(t, what, "the policy ending 1234")
	require.Contains(t, what, "invoice PDFs 1 of 2 read, 1 with text, 0 with a due-date label")
	require.Contains(t, what, "installments call {currentDate: date, isAutoPay: bool, planCode: string}")
	require.Contains(t, what, "expirationDate: date")
	require.Contains(t, what, "policyMinDue: number")
	require.Contains(t, what, "activity [2 × {")
	require.Contains(t, what, "documentHandle: string")
	require.Contains(t, what, "summary tile carries no date")
	for _, value := range []string{"9/19", "Z9", "120", "240", "100200300", erieTestPolicy, erieTestDocumentPolicy, "Plan A"} {
		require.NotContains(t, what, value)
	}

	read.Installments, read.InstallmentsMissed = nil, "answered HTTP 500"
	notes = &Notes{}
	ErieBillsFromPolicy(read, notes)
	require.Contains(t, notes.List()[len(notes.List())-1], "installments call answered HTTP 500")
}

func TestAnInstallmentListInAnyShapeAndAspNetDatesStillDatesAnInvoice(t *testing.T) {
	// The answer is a bare list, its rows name the date their own way and
	// write it as ASP.NET does.
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	read.TileDue = ""
	read.Installments = []any{
		map[string]any{"instDueDt": "/Date(1776211200000)/", "statusDescription": "Paid in full"},
		map[string]any{"instDueDt": "/Date(1789444800000-0400)/", "statusDescription": "Billed"},
		map[string]any{"instDueDt": "/Date(1792036800000)/", "statusDescription": "Future"},
	}
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 2)
	require.Equal(t, "2026-09-15", bills[0].DueOn)
	require.Equal(t, "2026-04-15", bills[1].DueOn)
	require.Empty(t, notes.List())
}

func TestTheTermsOwnDueDateDatesTheAmountOwed(t *testing.T) {
	read := erieTestRead()
	read.InvoiceText = map[string]string{}
	read.TileDue = ""
	read.Documents = read.Documents[:1]
	read.Terms[0]["policyMinDueDate"] = "2026-09-15T00:00:00"
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 1)
	require.Equal(t, "2026-09-15", bills[0].DueOn)
	require.Equal(t, "120.00", bills[0].AmountDue.String())
	require.Empty(t, notes.List())
}

func TestADueDateOnALedgerRowDatesTheInvoiceInItsCycle(t *testing.T) {
	read := erieTestRead()
	read.Terms[0]["policyMinDue"] = 0.0
	read.InvoiceText = map[string]string{}
	read.TileDue = ""
	read.Activity = append(read.Activity, map[string]any{
		"transactionCategory": "Billing", "transactionType": "INS",
		"transactionAmount": 240.0, "transactionEffectiveDate": "2026-03-25", "installmentDueDate": "4/15/2026",
	})
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 1, "the summer invoice has nothing due in its cycle")
	require.Equal(t, "2026-04-15", bills[0].DueOn)
	require.Equal(t, "Paid", bills[0].Status)
}

func TestOnAutomaticPaymentsTheDraftInAnInvoicesCycleIsItsDueDate(t *testing.T) {
	// Monthly invoices printed on the 2nd and drawn on the 20th, with nothing
	// else anywhere that names a due date.
	read := ErieRead{
		Policy: erieTestPolicy, Account: erieTestAccount,
		Terms: []map[string]any{{
			"expirationDate": "2027-03-01", "policyStatusDescription": "In Force",
			"policyMinDue": 0.0, "paymentAmount": 75.0, "recurringEFTTokenId": "token-nobody-should-see",
		}},
		Activity: []map[string]any{
			{"transactionType": "PAY", "transactionAmount": -75.0, "transactionEffectiveDate": "2026-07-20"},
			{"transactionType": "PAY", "transactionAmount": -75.0, "transactionEffectiveDate": "2026-08-20"},
			{"transactionType": "PAY", "transactionAmount": -75.0, "transactionEffectiveDate": "2026-09-21"},
		},
		Documents: []map[string]any{
			{"documentHandle": "3", "documentType": "Invoice", "printDate": "09/02/2026", "policyNumber": erieTestDocumentPolicy},
			{"documentHandle": "2", "documentType": "Invoice", "printDate": "08/03/2026", "policyNumber": erieTestDocumentPolicy},
			{"documentHandle": "1", "documentType": "Invoice", "printDate": "07/02/2026", "policyNumber": erieTestDocumentPolicy},
		},
	}
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 3)
	require.Equal(t, "2026-09-21", bills[0].DueOn)
	require.Equal(t, "2026-08-20", bills[1].DueOn)
	require.Equal(t, "2026-07-20", bills[2].DueOn)
	require.Empty(t, notes.List())

	// Off automatic payments a payment's day is only the day somebody paid.
	read.Terms[0]["recurringEFTTokenId"] = ""
	require.Empty(t, ErieBillsFromPolicy(read, &Notes{}))
}

func TestAPolicyWithNothingOwedIsItsInvoicesAndNoOpenBill(t *testing.T) {
	read := erieTestRead()
	read.Terms[0]["policyMinDue"] = 0.0
	read.Activity = append(read.Activity, map[string]any{
		"transactionCategory": "Payments", "transactionType": "PAY",
		"transactionAmount": -120.0, "transactionEffectiveDate": "2026-09-01",
	})

	bills := ErieBillsFromPolicy(read, &Notes{})

	require.Len(t, bills, 2)
	for _, bill := range bills {
		require.Equal(t, "Paid", bill.Status)
	}
}

func TestAPolicyWithNoTermIsStillItsInvoicesWithANote(t *testing.T) {
	read := erieTestRead()
	read.Terms = nil
	notes := &Notes{}

	bills := ErieBillsFromPolicy(read, notes)

	require.Len(t, bills, 2)
	require.Equal(t, "2026-09-15", bills[0].DueOn)
	require.Equal(t, "120.00", bills[0].AmountDue.String(), "the invoice's own figure, with no term to say what is owed")
	require.Equal(t, "2026-04-15", bills[1].DueOn)
	require.Equal(t, "Paid", bills[1].Status)
	require.Contains(t, notes.List()[0], "no term")
}

func TestAnInvoiceIsMatchedToItsPolicyHoweverThePortalSpellsTheNumber(t *testing.T) {
	documents := []map[string]any{
		{"documentHandle": "1", "documentType": "Invoice", "printDate": "08/25/2026", "policyNumber": erieTestDocumentPolicy},
		{"documentHandle": "2", "documentType": "Invoice", "printDate": "07/25/2026", "policyNumber": "Q00-0005678"},
		{"documentHandle": "3", "documentType": "ID cards", "printDate": "06/25/2026", "policyNumber": erieTestDocumentPolicy},
	}

	found := ErieInvoices(documents, erieTestPolicy)

	require.Len(t, found, 1)
	require.Equal(t, "1", Text(found[0]["documentHandle"]))
}

func stubEriePortal(t *testing.T, rewrite func(address string) (int, string, bool)) *browser.StubPage {
	t.Helper()
	page := &browser.StubPage{Location: erieLanding}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		switch {
		case script == erieFormTokenScript:
			if strings.Contains(page.Location, erieDocumentsPath) {
				return map[string]any{"name": "__RequestVerificationToken", "value": "form-token-invented"}, nil
			}
			return map[string]any{"name": "", "value": ""}, nil
		case script == erieLinksScript:
			return []any{
				"https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Account/" + erieTestAccount +
					"/Policy/" + erieTestPolicy + "/VendorId/STG",
				"https://www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account/" +
					erieTestAccount + "/Policy/0",
			}, nil
		case script == erieTileScript:
			return "Minimum due by 9/15/2026", nil
		case strings.Contains(script, "await fetch(arg.url"):
			asked, _ := arg.(map[string]any)
			address, _ := asked["url"].(string)
			status, body := erieStubAnswer(address)
			if rewrite != nil {
				if newStatus, newBody, held := rewrite(address); held {
					status, body = newStatus, newBody
				}
			}
			return map[string]any{
				"status":  status,
				"headers": map[string]any{"content-type": "application/json"},
				"base64":  base64.StdEncoding.EncodeToString([]byte(body)),
			}, nil
		}
		return nil, nil
	}
	return page
}

func erieStubAnswer(address string) (int, string) {
	switch {
	case strings.Contains(address, "/Documents/Policies/Account/"):
		return 200, `{"inForceProducts":[{"policyNumber":"` + erieTestPolicy + `","policySourceSystem":"PMS",` +
			`"product":"APV","productGroupName":"Personal Vehicles","productName":"Auto"}],` +
			`"pastProducts":[],"partyName":"A Household"}`
	case strings.Contains(address, "GetTermList"):
		return 200, `[{"policy":{"policyNumber":"` + erieTestPolicy + `","effectiveDate":"2026-03-01"},` +
			`"expirationDate":"2027-03-01","policyStatusDescription":"In Force","policyMinDue":120,` +
			`"paymentAmount":240,"currentBalanceAmount":360,"payPlanDescription":"Annual (Plan A)",` +
			`"recurringEFTTokenId":"token-nobody-should-see"}]`
	case strings.Contains(address, "GetActivity"):
		return 200, `[{"transactionCategory":"Payments","transactionType":"PAY","transactionAmount":-240,` +
			`"transactionEffectiveDate":"2026-04-10"}]`
	case strings.Contains(address, "GetFutureInstallments"):
		return 200, `{"currentDate":"9/19/2026","installments":[],"isAutoPay":true}`
	case strings.Contains(address, "GetDocuments"):
		return 200, `{"documents":[{"documentHandle":"100200300","documentId":"abcdef01",` +
			`"documentType":"Invoice","documentSubType":"","printDate":"08/25/2026","effectiveDate":"8/25/2026",` +
			`"policyNumber":"` + erieTestDocumentPolicy + `","product":"APV","sequenceId":1}],` +
			`"documentTypes":{"billingDocuments":["Invoice"],"policyDocuments":["Declarations"]},` +
			`"hasMultiplePolicies":true,"policyTypeText":"Auto"}`
	case strings.Contains(address, "/api/pdf/download"):
		return 200, "%PDF-1.4 invented"
	}
	return 404, "no such page"
}

func erieTestCall(page *browser.StubPage, notes *Notes) Call {
	return Call{
		Ctx: context.Background(), Page: page, Notes: notes,
		Now: func() time.Time { return erieDay(t0) },
	}
}

func TestErieReadsItsPoliciesAndTheBillsBehindThemOverAStubPortal(t *testing.T) {
	module := NewErie()
	page := stubEriePortal(t, nil)
	notes := &Notes{}

	found, err := module.Subaccounts(erieTestCall(page, notes))
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "Auto policy", found[0].Label)

	page = stubEriePortal(t, nil)
	pull, err := module.FetchBills(erieTestCall(page, notes))

	require.NoError(t, err)
	require.False(t, pull.NeedsSignIn)
	require.Len(t, pull.Bills, 1)
	require.Equal(t, erieTestPolicy, pull.Bills[0].Subaccount)
	require.Equal(t, "120.00", pull.Bills[0].AmountDue.String())
	// The invoice PDF the stub answers carries no text layer, so the due date
	// is the tile's and the bill is still a bill.
	require.Equal(t, "2026-09-15", pull.Bills[0].DueOn)
	require.Equal(t, "2026-09-15", pull.Bills[0].AutopayOn, "the term carries a recurring payment token")
	require.Equal(t, "Open", pull.Bills[0].Status)
	for _, bill := range pull.Bills {
		require.NotEmpty(t, bill.DueOn)
	}

	// The billing application's session is the redirect its own link starts:
	// asked before that, its JSON answers nothing.
	require.Contains(t, page.Visited,
		"https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Account/"+erieTestAccount+
			"/Policy/"+erieTestPolicy+"/VendorId/STG")
	require.Contains(t, page.Visited, erieDocumentsPage(erieTestAccount))

	// Nothing anybody typed, and no identifier, in any note.
	for _, note := range notes.List() {
		require.NotContains(t, note, erieTestAccount)
		require.NotContains(t, note, "token")
	}
}

func TestErieSaysASignInIsOwedWhenTheLandingPageIsTheSignInPage(t *testing.T) {
	module := NewErie()
	page := stubEriePortal(t, nil)
	page.OnGoto = func(string) error {
		page.Location = "https://www.erieinsurance.com/Account/Login/Login?ReturnUrl=%2FCustomer"
		return nil
	}
	notes := &Notes{}

	pull, err := module.FetchBills(erieTestCall(page, notes))

	require.NoError(t, err)
	require.True(t, pull.NeedsSignIn)
	require.Equal(t, "Erie Insurance asked to sign in again", pull.Reason)
	require.Contains(t, notes.List()[0], "the profile is not signed in")
	require.NotContains(t, notes.List()[0], "?", "a note names the page and never its query string")
}

func TestErieSaysASignInIsOwedWhenTheBillingSessionHasLapsed(t *testing.T) {
	for name, answer := range map[string]struct {
		status int
		body   string
	}{
		"a 401":                 {401, `{"message":"Unauthorized"}`},
		"a 403":                 {403, ""},
		"a sign-in page at 200": {200, "<!DOCTYPE html><html><title>Sign On</title></html>"},
	} {
		t.Run(name, func(t *testing.T) {
			page := stubEriePortal(t, func(address string) (int, string, bool) {
				if strings.Contains(address, "GetTermList") {
					return answer.status, answer.body, true
				}
				return 0, "", false
			})
			notes := &Notes{}

			pull, err := NewErie().FetchBills(erieTestCall(page, notes))

			require.NoError(t, err)
			require.True(t, pull.NeedsSignIn)
			require.Contains(t, strings.Join(notes.List(), "\n"), "the billing session has lapsed")
			for _, arg := range page.Args {
				asked, _ := arg.(map[string]any)
				address, _ := asked["url"].(string)
				require.NotContains(t, address, "GetActivity", "nothing more is asked of a session that is gone")
			}
		})
	}
}

func TestATermListThatFailsOtherwiseIsANoteAndNotASignIn(t *testing.T) {
	page := stubEriePortal(t, func(address string) (int, string, bool) {
		if strings.Contains(address, "GetTermList") {
			return 500, `{"message":"try again"}`, true
		}
		return 0, "", false
	})
	notes := &Notes{}

	pull, err := NewErie().FetchBills(erieTestCall(page, notes))

	require.NoError(t, err)
	require.False(t, pull.NeedsSignIn)
	joined := strings.Join(notes.List(), "\n")
	require.Contains(t, joined, "answered HTTP 500")
	require.Contains(t, joined, "no term")
}

func TestAnErieStatementIsAPDFPostedByThePageAndNamedForItsPolicy(t *testing.T) {
	module := NewErie()
	bill := Bill{Subaccount: erieTestPolicy, IssuedOn: "2026-08-25"}
	bill.Raw, _ = json.Marshal(erieRaw{
		DocumentHandle: "100200300", DocumentPolicy: erieTestDocumentPolicy,
		OnlineAccount: erieTestAccount, StartDate: "2025-08-20", EndDate: "2026-09-19",
	})
	page := stubEriePortal(t, nil)
	// Left wherever the pull last was: the billing application.
	page.Location = "https://www.erieinsurance.com/BillingCenterWeb/Inquiry/Index"
	var posted url.Values
	var postedFrom string
	var headers map[string]any
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if asked, ok := arg.(map[string]any); ok && strings.Contains(script, "await fetch(arg.url") {
			if body, held := asked["body"].(string); held {
				posted, _ = url.ParseQuery(body)
				postedFrom = page.Location
				headers, _ = asked["headers"].(map[string]any)
			}
		}
		return inner(script, arg)
	}

	document, err := module.FetchDocument(erieTestCall(page, &Notes{}), bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "erie-1234-2026-08-25.pdf", document.Filename)
	require.True(t, strings.HasPrefix(string(document.Bytes), "%PDF-"))
	require.Equal(t, "100200300", posted.Get("documentHandle"))
	require.Equal(t, "Views Document", posted.Get("transactionName"))
	require.Equal(t, "Invoice", posted.Get("documentType"))
	require.Equal(t, "DocumentsPage", posted.Get("origin"))
	require.Equal(t, erieTestDocumentPolicy, posted.Get("policyNumber"))
	require.Equal(t, erieTestAccount, posted.Get("onlineAccountId"))

	// From the documents page, with the token that page's own form carries.
	require.Equal(t, erieDocumentsPage(erieTestAccount), postedFrom)
	require.Equal(t, "form-token-invented", posted.Get("__RequestVerificationToken"))
	sent := ""
	for name, value := range headers {
		if strings.EqualFold(name, "RequestVerificationToken") {
			sent, _ = value.(string)
		}
	}
	require.Equal(t, "form-token-invented", sent, "header names are the browser's to case")
}

// refusingErieStatements is a portal whose page fetch of a statement throws
// as Chrome's does, and whose answer to a call that follows no redirects is
// a redirect. form answers the submitted form, or nothing when nil; probe is
// what the browser sees of the call that follows no redirects.
func refusingErieStatements(
	t *testing.T, form func(page *browser.StubPage), probe ...func(page *browser.StubPage),
) (*browser.StubPage, *int) {
	page := stubEriePortal(t, nil)
	fetches := 0
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		asked, _ := arg.(map[string]any)
		address, _ := asked["url"].(string)
		switch {
		case script == erieSubmitScript:
			page.Location = erieHome + erieStatementPath
			if form != nil {
				form(page)
			}
			return nil, nil
		case strings.Contains(script, "await fetch(arg.url") && strings.Contains(address, erieStatementPath):
			fetches++
			if asked["redirect"] == "manual" {
				for _, seen := range probe {
					seen(page)
				}
				return map[string]any{"status": 0, "redirected": true, "base64": ""}, nil
			}
			return map[string]any{"status": 0, "error": "Failed to fetch", "excerpt": "Failed to fetch"}, nil
		}
		return inner(script, arg)
	}
	return page, &fetches
}

func erieTestBill() Bill {
	bill := Bill{Subaccount: erieTestPolicy, IssuedOn: "2026-08-25"}
	bill.Raw, _ = json.Marshal(erieRaw{
		DocumentHandle: "100200300", DocumentPolicy: erieTestDocumentPolicy,
		OnlineAccount: erieTestAccount, StartDate: "2025-08-20", EndDate: "2026-09-19",
	})
	return bill
}

func TestAStatementThePagesFetchIsRefusedIsPostedAsThePagesOwnForm(t *testing.T) {
	module := NewErie()
	page, fetches := refusingErieStatements(t, func(page *browser.StubPage) {
		page.Respond(browser.StubResponse{
			Address: erieHome + erieStatementPath, Code: 302,
		})
		page.Respond(browser.StubResponse{
			Address: "https://documents.example.invalid/render/" + erieTestAccount, Code: 200,
			Payload: []byte("%PDF-1.4 invented"),
		})
	})
	notes := &Notes{}

	document, err := module.FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.True(t, strings.HasPrefix(string(document.Bytes), "%PDF-"))
	require.Equal(t, 2, *fetches, "the call, then the call that follows no redirects")
	require.Empty(t, notes.List())
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "answered a redirect")

	// The page that has learned its own call is refused goes round it.
	page.Location = erieDocumentsPage(erieTestAccount)
	document, err = module.FetchDocument(erieTestCall(page, notes), erieTestBill())
	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, 3, *fetches, "only the call that follows no redirects")
}

func TestAStatementRefusedBothWaysSaysWhatTheBrowserSawOnce(t *testing.T) {
	module := NewErie()
	page, fetches := refusingErieStatements(t, func(page *browser.StubPage) {
		page.Respond(browser.StubResponse{Address: erieHome + erieStatementPath + "?x=1", Code: 302})
		page.Respond(browser.StubResponse{
			Address: "https://custsso.erieinsurance.com/my.policy/" + erieTestAccount, Code: 200,
			Payload: []byte("<html>Sign in</html>"),
		})
	})
	notes := &Notes{}

	document, err := module.FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.Nil(t, document)
	require.Len(t, notes.List(), 1)
	note := notes.List()[0]
	require.Contains(t, note, "from www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account:")
	require.Contains(t, note, "Failed to fetch")
	require.Contains(t, note, "without following redirects it answered a redirect")
	require.Contains(t, note, "HTTP 302 at www.erieinsurance.com/DocumentListWeb/api/pdf/download (not a PDF)")
	require.Contains(t, note, "HTTP 200 at custsso.erieinsurance.com/my.policy (not a PDF)")
	require.NotContains(t, note, erieTestAccount)
	require.NotContains(t, note, "form-token-invented")
	require.NotContains(t, note, "x=1")

	// Refused both ways, the page is not asked again in this pull.
	document, err = module.FetchDocument(erieTestCall(page, notes), erieTestBill())
	require.NoError(t, err)
	require.Nil(t, document)
	require.Len(t, notes.List(), 1)
	require.Equal(t, 2, *fetches)

	// A page the engine is done with starts afresh.
	module.Forget(page)
	_, _ = module.FetchDocument(erieTestCall(page, notes), erieTestBill())
	require.Equal(t, 4, *fetches)
}

func TestAFormThatAnswersNothingSaysSo(t *testing.T) {
	page, _ := refusingErieStatements(t, nil)
	notes := &Notes{}

	document, _ := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "no response and no download within 20s")
	require.GreaterOrEqual(t, page.Slept, erieFormWait)
}

func TestAnErieStatementThatIsNotAPDFIsNoDocumentAndANote(t *testing.T) {
	module := NewErie()
	bill := Bill{Subaccount: erieTestPolicy}
	bill.Raw, _ = json.Marshal(erieRaw{DocumentHandle: "100200300", OnlineAccount: erieTestAccount})
	page := stubEriePortal(t, func(address string) (int, string, bool) {
		if strings.Contains(address, "/api/pdf/download") {
			return 200, "<html>Sign in</html>", true
		}
		return 0, "", false
	})
	notes := &Notes{}

	document, err := module.FetchDocument(erieTestCall(page, notes), bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestEveryErieCallIsHandedShapesAPageCanTake(t *testing.T) {
	page, _ := refusingErieStatements(t, nil)

	_, err := NewErie().FetchBills(erieTestCall(page, &Notes{}))

	require.NoError(t, err)
	require.NotEmpty(t, page.Args)
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
}

const erieTestFile = "https://documents.example.invalid/render/" + erieTestAccount + "/invoice?sig=invented"

func TestAStatementThePagesFormHandsOverAsADownloadIsRead(t *testing.T) {
	page, _ := refusingErieStatements(t, func(page *browser.StubPage) {
		page.Respond(browser.StubResponse{
			Address: erieHome + erieStatementPath, Code: 302, Headers: map[string]string{"Location": erieTestFile},
		})
		page.Download(browser.StubDownload{Address: erieTestFile, Payload: []byte("%PDF-1.4 invented")})
	})
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "%PDF-1.4 invented", string(document.Bytes))
	require.Empty(t, notes.List())
	trace := strings.Join(notes.Traces(), "\n")
	require.Contains(t, trace, "the statement was read by submitting the documents page's form, as a download from documents.example.invalid/render")
	require.NotContains(t, trace, erieTestAccount)
	require.NotContains(t, trace, "sig=")
}

func TestTheRedirectTheBrowserShowsIsOpenedInAPageOfItsOwn(t *testing.T) {
	submitted := false
	page, _ := refusingErieStatements(t, func(*browser.StubPage) { submitted = true },
		func(page *browser.StubPage) {
			page.Respond(browser.StubResponse{
				Address: erieHome + erieStatementPath, Code: 302, Headers: map[string]string{"location": erieTestFile},
			})
		})
	page.OnBytes = func(address string) (int, string, []byte, error) {
		if address == erieTestFile {
			return 200, "application/octet-stream", []byte("%PDF-1.4 invented"), nil
		}
		return 404, "text/html", nil, nil
	}
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, []string{erieTestFile}, page.Opened, "the whole address, query and all")
	require.False(t, submitted, "the form is not posted for a statement already read")
	require.Contains(t, strings.Join(notes.Traces(), "\n"),
		"answered a redirect to documents.example.invalid/render; "+
			"the statement was read by opening the redirect it answered, at documents.example.invalid/render")
}

func TestAStatementNoteNamesWhereEachRedirectLedAndLeavesOutTheTrackers(t *testing.T) {
	formFile := "https://files.example.invalid/get/98765?token=invented"
	page, _ := refusingErieStatements(t, func(page *browser.StubPage) {
		for _, tracker := range []string{
			"https://www.google.com/rmkt/collect/1234/", "https://www.google.com/rmkt/collect/1234/",
			"https://googleads.g.doubleclick.net/pagead/viewthroughconversion/1234/",
		} {
			page.Respond(browser.StubResponse{Address: tracker, Code: 200})
		}
		page.Respond(browser.StubResponse{
			Address: erieHome + erieStatementPath, Code: 302, Headers: map[string]string{"Location": formFile},
		})
		page.Download(browser.StubDownload{Address: formFile, Err: errors.New("saving failed")})
	}, func(page *browser.StubPage) {
		page.Respond(browser.StubResponse{
			Address: erieHome + erieStatementPath, Code: 302,
			Headers: map[string]string{"Location": "/DocumentViewer/render/12345?token=invented"},
		})
	})
	page.OnBytes = func(address string) (int, string, []byte, error) {
		if address == formFile {
			return 0, "", nil, errors.New("browser: nothing came back from " + browser.WithoutQuery(address) +
				": no response body and no download")
		}
		return 200, "text/html", []byte("<html>Sign in</html>"), nil
	}
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.Nil(t, document)
	require.Equal(t, []string{erieHome + "/DocumentViewer/render/12345?token=invented", formFile}, page.Opened)
	require.Len(t, notes.List(), 1)
	note := notes.List()[0]
	for _, said := range []string{
		"asked again without following redirects it answered a redirect to www.erieinsurance.com/DocumentViewer/render; ",
		"opened in a page of its own, www.erieinsurance.com/DocumentViewer/render answered HTTP 200 as text/html (not a PDF); ",
		"submitted as the page's form, the browser saw a download from files.example.invalid/get (could not be saved), " +
			"HTTP 302 at www.erieinsurance.com/DocumentListWeb/api/pdf/download redirecting to files.example.invalid/get, " +
			"3 analytics responses left out; ",
		"opened in a page of its own, files.example.invalid/get answered nothing readable " +
			"(browser: nothing came back from files.example.invalid/get: no response body and no download); ",
		"no further statement is asked for from this page",
	} {
		require.Contains(t, note, said)
	}
	for _, private := range []string{"12345", "98765", "token=", "google", "doubleclick", erieTestAccount} {
		require.NotContains(t, note, private)
	}
}

func TestEveryKeyOfAnActivityRowIsNamedInTheUndatedNote(t *testing.T) {
	row := map[string]any{}
	keys := make([]string, 0, 56)
	for index := range 56 {
		key := fmt.Sprintf("field%c%c", 'a'+index/26, 'a'+index%26)
		row[key] = "x"
		keys = append(keys, key)
	}
	read := ErieRead{Policy: erieTestPolicy, Activity: []map[string]any{row}}

	note := erieWhatWasRead(read, nil, nil)

	for _, key := range keys {
		require.Contains(t, note, key+": string")
	}
	require.NotContains(t, note, "more")
}

// erieInvoiceLink is the documents page's own link for the test invoice, as
// erieFindScript answers it.
func erieInvoiceLink(how string) map[string]any {
	return map[string]any{
		"found": true, "how": how,
		"element": map[string]any{
			"tag": "a", "attributes": []any{"class", "href", "data-handle"}, "href": "#",
			"onclick": false, "jquery": []any{"click"}, "target": "", "form": "", "visible": true,
		},
		"page": erieDocumentsMachinery(),
	}
}

func erieDocumentsMachinery() map[string]any {
	return map[string]any{
		"clickable": 12,
		"naming": []any{map[string]any{
			"tag": "a", "attributes": []any{"class", "href", "data-doc-12345"},
			"href":    erieHome + "/DocumentListWeb/Documents/View/" + erieTestAccount + "?handle=100200300",
			"onclick": true, "jquery": []any{}, "target": "_blank", "form": "", "visible": true,
		}},
		"forms": []any{map[string]any{
			"action": erieHome + erieStatementPath, "method": "post",
			"inputs": []any{"documentHandle", "policyNumber", "__RequestVerificationToken"},
		}},
	}
}

// pressableErieStatements is a portal whose page fetch of a statement is
// refused as Chrome's is, whose documents page answers find for the invoice's
// own link, and where pressing it does what press says.
func pressableErieStatements(
	t *testing.T, find map[string]any, press func(page *browser.StubPage),
) (*browser.StubPage, *[]any) {
	page, _ := refusingErieStatements(t, nil)
	var asked []any
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script == erieFindScript {
			asked = append(asked, arg)
			return find, nil
		}
		return inner(script, arg)
	}
	page.OnClick = func(selector string) error {
		if selector == erieMark && press != nil {
			press(page)
		}
		return nil
	}
	return page, &asked
}

func TestAStatementIsReadByPressingTheInvoicesOwnLink(t *testing.T) {
	page, asked := pressableErieStatements(t, erieInvoiceLink("its document handle"), func(page *browser.StubPage) {
		page.Download(browser.StubDownload{Address: erieTestFile, Payload: []byte("%PDF-1.4 invented")})
	})
	notes := &Notes{}
	bill := erieTestBill()

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "%PDF-1.4 invented", string(document.Bytes))
	require.Empty(t, notes.List())
	for _, arg := range page.Args {
		call, _ := arg.(map[string]any)
		require.NotEqual(t, erieHome+erieStatementPath, call["url"], "nothing is posted for a statement already read")
	}
	require.Len(t, *asked, 1)
	find, _ := (*asked)[0].(map[string]any)
	require.Equal(t, "100200300", find["handle"])
	require.Contains(t, find["dates"], "08/25/2026")
	require.Contains(t, find["dates"], "8/25/2026")
	trace := strings.Join(notes.Traces(), "\n")
	require.Contains(t, trace, "an Erie statement was read asking the page itself: the page carries no Angular; "+
		"pressed the invoice's own <a>, attributes class "+
		"href data-handle, href #, jQuery handlers click (matched by its document handle), "+
		"as a download from documents.example.invalid/render")
	require.NotContains(t, trace, erieTestAccount)
}

func TestAPressThatOpensAPopupIsReadFromThePopup(t *testing.T) {
	viewer := "https://documents.example.invalid/view/98765?sig=invented"
	popup := &browser.StubPage{Location: viewer}
	page, _ := pressableErieStatements(t, erieInvoiceLink("the print date in its row"), func(page *browser.StubPage) {
		page.Popup(popup)
	})
	page.OnBytes = func(address string) (int, string, []byte, error) {
		if address == viewer {
			return 200, "application/pdf", []byte("%PDF-1.4 invented"), nil
		}
		return 404, "text/html", nil, nil
	}
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.True(t, popup.Closed, "a popup is closed once read")
	trace := strings.Join(notes.Traces(), "\n")
	require.Contains(t, trace, "as the file the popup it opened shows, at documents.example.invalid/view")
	require.NotContains(t, trace, "98765")
}

func TestANoteSaysHowTheDocumentsPageAsksForAFileWhenNoLinkIsFound(t *testing.T) {
	page, _ := pressableErieStatements(t, map[string]any{"found": false, "page": erieDocumentsMachinery()}, nil)
	notes := &Notes{}

	document, _ := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Nil(t, document)
	note := notes.List()[0]
	require.Contains(t, note, "asking the page itself: the page carries no Angular; the documents page shows no "+
		"link or button for the invoice; the page has no form of its own to the statement path "+
		"(the documents page has 12 links and buttons; those naming a document: <a>, attributes "+
		"class href <id>, href www.erieinsurance.com/DocumentListWeb/Documents/View, an onclick handler, "+
		"target _blank; a post form to www.erieinsurance.com/DocumentListWeb/api/pdf/download with inputs "+
		"documentHandle policyNumber __RequestVerificationToken); the page's own call was refused")
	for _, private := range []string{erieTestAccount, "100200300", "12345", "handle="} {
		require.NotContains(t, note, private)
	}
}

func TestAPressTheBrowserAnswersWithASignInPageSaysWhatItSaw(t *testing.T) {
	page, _ := pressableErieStatements(t, erieInvoiceLink("its document id"), func(page *browser.StubPage) {
		page.Respond(browser.StubResponse{Address: "https://www.google.com/rmkt/collect/1234/", Code: 200})
		page.Respond(browser.StubResponse{
			Address: erieHome + erieStatementPath, Code: 302,
			Headers: map[string]string{"Location": "/DocumentListWeb/Login/Login?ReturnUrl=invented"},
		})
		page.Respond(browser.StubResponse{
			Address: erieHome + "/DocumentListWeb/Login/Login?ReturnUrl=invented", Code: 200,
			Payload: []byte("<html>Sign in</html>"),
		})
	})
	notes := &Notes{}

	document, _ := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Nil(t, document)
	note := notes.List()[0]
	require.Contains(t, note, "asking the page itself: the page carries no Angular; pressed the invoice's own <a>, attributes class "+
		"href data-handle, href #, jQuery handlers click (matched by its document id): the browser saw "+
		"HTTP 302 at www.erieinsurance.com/DocumentListWeb/api/pdf/download redirecting to "+
		"www.erieinsurance.com/DocumentListWeb/Login/Login, "+
		"HTTP 200 at www.erieinsurance.com/DocumentListWeb/Login/Login (not a PDF), "+
		"1 analytics response left out; no popup opened; the page has no form of its own to the statement path (the documents page has 12 links and buttons")
	require.NotContains(t, note, "ReturnUrl")
	require.NotContains(t, note, "google")
}

// askableErieStatements is a portal whose page fetch of a statement is
// refused as Chrome's is, whose documents page answers each named script
// itself, and where pressing a marked element does what pressed says.
func askableErieStatements(
	t *testing.T,
	scripts map[string]func(page *browser.StubPage, arg map[string]any) any,
	pressed map[string]func(page *browser.StubPage),
) *browser.StubPage {
	page, _ := refusingErieStatements(t, nil)
	inner := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if answer, held := scripts[script]; held {
			asked, _ := arg.(map[string]any)
			return answer(page, asked), nil
		}
		return inner(script, arg)
	}
	page.OnClick = func(selector string) error {
		if press, held := pressed[selector]; held {
			press(page)
		}
		return nil
	}
	return page
}

func erieDownloadsTheStatement(page *browser.StubPage) {
	page.Download(browser.StubDownload{Address: erieTestFile, Payload: []byte("%PDF-1.4 invented")})
}

func TestTheControllersOwnDownloadFunctionIsCalledOnTheDocument(t *testing.T) {
	angular := func(page *browser.StubPage, arg map[string]any) any {
		answer := map[string]any{
			"state": "scope", "doc": []any{"documentHandle", "documentId", "policySourceSystem"},
			"functions": []any{"vm.downloadDocument", "vm.getPolicyForms"}, "chosen": "vm.downloadDocument",
		}
		if arg["call"] == true {
			answer["called"] = true
			erieDownloadsTheStatement(page)
		}
		return answer
	}
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieAngularScript: angular,
	}, nil)
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Empty(t, page.Clicked, "nothing is pressed for a statement the controller handed over")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "the page's Angular scope holds the invoice's "+
		"document object (keys documentHandle documentId policySourceSystem) and functions vm.downloadDocument "+
		"vm.getPolicyForms; the header's ng-click calls nothing; the row's ng-click calls nothing; called "+
		"vm.downloadDocument on the document object, as a download from documents.example.invalid/render")
}

func TestAPageWhoseAngularHoldsNothingThatDownloadsIsCalledNothing(t *testing.T) {
	var calls []any
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieAngularScript: func(_ *browser.StubPage, arg map[string]any) any {
			calls = append(calls, arg["call"])
			return map[string]any{
				"state": "scope", "doc": []any{"documentHandle", "referenceId"},
				"functions": []any{"resetAndGetDocuments", "getPolicyDocuments", "openFilters"},
				"header":    []any{"getPolicyFormsForPolicy"}, "row": []any{"ShowHideAdditionalForms"}, "chosen": "",
			}
		},
	}, nil)
	notes := &Notes{}

	_, _ = NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Equal(t, []any{false}, calls, "nothing is called")
	require.Contains(t, notes.List()[0], "functions resetAndGetDocuments getPolicyDocuments openFilters; the "+
		"header's ng-click calls getPolicyFormsForPolicy; the row's ng-click calls ShowHideAdditionalForms; "+
		"none of them downloads a file, so none was called")
}

// erieGateway is a popup resting on the identity provider's access policy
// page, which passes itself on when its own hidden form is submitted and
// passed is set.
func erieGateway(passed bool) *browser.StubPage {
	popup := &browser.StubPage{
		Location: erieIdentityHome + "/vdesk/webtop.eui?webtop=invented",
		Cookies:  []string{"MRHSession", "LastMRH_Session", "someoneElses"},
	}
	popup.OnEvaluate = func(script string, arg any) (any, error) {
		if script != erieAPMScript {
			return nil, nil
		}
		asked, _ := arg.(map[string]any)
		form := map[string]any{"action": erieIdentityHome + "/my.policy", "method": "post",
			"inputs": []any{"username", "password"}, "passing": false}
		if passed {
			form = map[string]any{"action": erieIdentityHome + "/my.policy", "method": "post",
				"inputs": []any{"vhost", "state"}, "passing": true}
		}
		answer := map[string]any{"title": "Logon", "headings": []any{"Please wait"}, "forms": []any{form}}
		if passed && asked["act"] == true {
			answer["acted"] = "form"
			popup.Location = erieHome + erieStatementPath
			popup.Download(browser.StubDownload{Address: erieTestFile, Payload: []byte("%PDF-1.4 invented")})
		}
		return answer, nil
	}
	return popup
}

func ownFormOpening(popup *browser.StubPage, selves *[]any) func(*browser.StubPage, map[string]any) any {
	return func(page *browser.StubPage, arg map[string]any) any {
		*selves = append(*selves, arg["self"])
		if arg["self"] != true {
			page.Popup(popup)
		}
		return map[string]any{
			"found": true, "forms": 15, "action": erieHome + erieStatementPath, "target": "_blank",
			"filled": []any{map[string]any{"name": "documentHandle", "from": "the page's document object"}},
		}
	}
}

func TestAPopupHeldAtTheAccessPolicyGatewayIsPassedOnAndBringsTheFile(t *testing.T) {
	popup := erieGateway(true)
	var selves []any
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieOwnFormScript: ownFormOpening(popup, &selves),
	}, nil)
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.True(t, popup.Closed)
	require.Equal(t, []any{false}, selves, "a form that brought the file is not submitted again")
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "(one of 15, target _blank) with documentHandle from "+
		"the page's document object, as the file the popup it opened brought, at custsso.erieinsurance.com/vdesk/webtop.eui "+
		"it submitted the page's own form; as a download from documents.example.invalid/render")
}

func TestAPopupTheGatewayHoldsIsDescribedAndTheFormSentAgainInThisWindow(t *testing.T) {
	popup := erieGateway(false)
	var selves []any
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieOwnFormScript: ownFormOpening(popup, &selves),
	}, nil)
	notes := &Notes{}

	document, _ := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Nil(t, document)
	require.Equal(t, []any{false, true}, selves)
	note := notes.List()[0]
	require.Contains(t, note, "a popup opened at custsso.erieinsurance.com/vdesk/webtop.eui and brought no PDF: it rests at "+
		"custsso.erieinsurance.com/vdesk/webtop.eui, titled \"Logon\", headings \"Please wait\", a post form to "+
		"custsso.erieinsurance.com/my.policy with inputs username password, the gateway's session cookies held: "+
		"MRHSession LastMRH_Session, beside 1 other cookie; submitted again in this window: the browser saw no "+
		"response and no download within 20s; no popup opened")
	require.NotContains(t, note, "someoneElses")
	require.NotContains(t, note, "webtop=")
}

func TestAnAccordionHeaderIsOpenedAndTheControlInsideItPressed(t *testing.T) {
	header := map[string]any{
		"found": true, "how": "its document handle",
		"element": map[string]any{
			"tag": "div", "attributes": []any{"class", "ng-click", "data-toggle", "aria-expanded", "aria-controls"},
			"href": "#", "onclick": false, "jquery": []any{"click"}, "visible": false,
		},
		"toggle": map[string]any{"is": true, "expanded": "false", "panel": "#document-panel"},
		"page":   erieDocumentsMachinery(),
	}
	var panelAsked any
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieAngularScript: func(*browser.StubPage, map[string]any) any { return map[string]any{"state": "unreadable"} },
		erieFindScript:    func(*browser.StubPage, map[string]any) any { return header },
		erieInnerScript: func(_ *browser.StubPage, arg map[string]any) any {
			panelAsked = arg["panel"]
			return map[string]any{
				"shown": true, "expanded": "true", "found": true,
				"controls": []any{map[string]any{"tag": "a", "attributes": []any{"ng-click", "class", "title"},
					"onclick": false, "jquery": []any{}, "visible": true}},
				"element": map[string]any{"tag": "a", "attributes": []any{"ng-click", "class", "title"},
					"onclick": false, "jquery": []any{}, "visible": true},
			}
		},
	}, map[string]func(*browser.StubPage){erieInnerMark: erieDownloadsTheStatement})
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, []string{erieMark, erieInnerMark}, page.Clicked, "the header, then the control inside")
	require.Equal(t, "#document-panel", panelAsked)
	require.Contains(t, strings.Join(notes.Traces(), "\n"), "the page's Angular answers no scope (its debug "+
		"information is off); opened the invoice's own <div>, attributes class ng-click data-toggle aria-expanded "+
		"aria-controls, href #, jQuery handlers click, not visible (matched by its document handle), an accordion "+
		"header (aria-expanded false, then true), its panel shown, with controls <a>, attributes ng-click class "+
		"title, no onclick or jQuery handler; pressed the control inside it, <a>, attributes ng-click class title, "+
		"no onclick or jQuery handler, as a download from documents.example.invalid/render")
}

// erieSignInRoundTrip is the redirects a statement post can go through: the
// portal's own sign-in, the identity provider and back.
func erieSignInRoundTrip(page *browser.StubPage, ending browser.StubResponse) {
	for _, hop := range []browser.StubResponse{
		{Address: erieHome + erieStatementPath, Code: 302,
			Headers: map[string]string{"Location": "/DocumentListWeb/Login/Login?ReturnUrl=invented"}},
		{Address: erieHome + "/DocumentListWeb/Login/Login?ReturnUrl=invented", Code: 307,
			Headers: map[string]string{"Location": "/DocumentListWeb/Login/Login"}},
		{Address: erieHome + "/DocumentListWeb/Login/Login", Code: 303,
			Headers: map[string]string{"Location": "https://custsso.erieinsurance.com/saml/idp/profile/redirectorpost/sso?SAMLRequest=invented"}},
		{Address: "https://custsso.erieinsurance.com/saml/idp/profile/redirectorpost/sso?SAMLRequest=invented", Code: 302,
			Headers: map[string]string{"Location": "https://custsso.erieinsurance.com/saml/idp/resume/" + erieTestAccount}},
		{Address: "https://custsso.erieinsurance.com/saml/idp/resume/" + erieTestAccount, Code: 302,
			Headers: map[string]string{"Location": erieHome + "/DocumentListWeb/Saml/Acs"}},
		{Address: erieHome + "/DocumentListWeb/Saml/Acs", Code: 302,
			Headers: map[string]string{"Location": erieHome + erieStatementPath}},
		ending,
	} {
		page.Respond(hop)
	}
}

func answerErieOwnForm(page *browser.StubPage, arg map[string]any, ending browser.StubResponse) any {
	if arg["submit"] == true {
		erieSignInRoundTrip(page, ending)
	}
	return map[string]any{
		"found": true, "forms": 3, "action": erieHome + erieStatementPath, "target": "", "angular": "absent",
		"doc": false, "filled": []any{
			map[string]any{"name": "documentHandle", "from": "the document list row"},
			map[string]any{"name": "origin", "from": "the page's own value"},
			map[string]any{"name": "startDate", "from": "the document window"},
			map[string]any{"name": "endDate", "from": "the document window"},
			map[string]any{"name": "policySourceSystem", "from": "nothing"},
		},
	}
}

func TestThePagesOwnFormIsFilledAndFollowedThroughASignInRoundTrip(t *testing.T) {
	var asked map[string]any
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieOwnFormScript: func(page *browser.StubPage, arg map[string]any) any {
			asked = arg
			return answerErieOwnForm(page, arg, browser.StubResponse{
				Address: erieHome + erieStatementPath, Code: 200, Payload: []byte("%PDF-1.4 invented"),
			})
		},
	}, nil)
	notes := &Notes{}

	document, err := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.NoError(t, err)
	require.NotNil(t, document)
	known, _ := asked["known"].(map[string]any)
	start, _ := known["startDate"].(map[string]any)
	require.Equal(t, "2025-08-20", start["value"])
	account, _ := known["onlineAccountId"].(map[string]any)
	require.Equal(t, erieTestAccount, account["value"])
	trace := strings.Join(notes.Traces(), "\n")
	require.Contains(t, trace, "submitted the page's own form to www.erieinsurance.com/DocumentListWeb/api/pdf/download "+
		"(one of 3, target none) with documentHandle from the document list row, origin from the page's own value, "+
		"startDate endDate from the document window, policySourceSystem from nothing, "+
		"as the response from www.erieinsurance.com/DocumentListWeb/api/pdf/download")
}

func TestASignInRoundTripThatNeverBringsTheFileIsNamedHopByHop(t *testing.T) {
	page := askableErieStatements(t, map[string]func(*browser.StubPage, map[string]any) any{
		erieOwnFormScript: func(page *browser.StubPage, arg map[string]any) any {
			return answerErieOwnForm(page, arg, browser.StubResponse{
				Address: erieHome + "/DocumentListWeb/Documents/MyDocuments/account/" + erieTestAccount, Code: 200,
				Payload: []byte("<html>My Documents</html>"),
			})
		},
	}, nil)
	notes := &Notes{}

	document, _ := NewErie().FetchDocument(erieTestCall(page, notes), erieTestBill())

	require.Nil(t, document)
	note := notes.List()[0]
	require.Contains(t, note, ": the browser saw "+
		"HTTP 302 at www.erieinsurance.com/DocumentListWeb/api/pdf/download redirecting to www.erieinsurance.com/DocumentListWeb/Login/Login, "+
		"HTTP 307 at www.erieinsurance.com/DocumentListWeb/Login/Login redirecting to www.erieinsurance.com/DocumentListWeb/Login/Login, "+
		"HTTP 303 at www.erieinsurance.com/DocumentListWeb/Login/Login redirecting to custsso.erieinsurance.com/saml/idp/profile/redirectorpost/sso, "+
		"HTTP 302 at custsso.erieinsurance.com/saml/idp/profile/redirectorpost/sso redirecting to custsso.erieinsurance.com/saml/idp/resume, "+
		"HTTP 302 at custsso.erieinsurance.com/saml/idp/resume redirecting to www.erieinsurance.com/DocumentListWeb/Saml/Acs, "+
		"HTTP 302 at www.erieinsurance.com/DocumentListWeb/Saml/Acs redirecting to www.erieinsurance.com/DocumentListWeb/api/pdf/download, "+
		"HTTP 200 at www.erieinsurance.com/DocumentListWeb/Documents/MyDocuments/account (not a PDF); no popup opened")
	for _, private := range []string{erieTestAccount, "SAMLRequest", "ReturnUrl", "invented"} {
		require.NotContains(t, note, private)
	}
	require.GreaterOrEqual(t, page.Slept, erieFormQuiet, "a chain is given its quiet time to come back")
}
