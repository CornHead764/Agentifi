package billers

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// Every figure below is invented; only the shapes are the service's.

const (
	alliantAuthAnswer = `{
  "status": { "type": "success", "message": "" },
  "data": {
    "accessToken": "invented-access-token",
    "refreshToken": "invented-refresh-token",
    "expiresIn": 20,
    "user": { "uuid": "11111111-2222-3333-4444-555555555555", "firstName": "Alex", "lastName": "Example" }
  }
}`

	alliantAddressesAnswer = `{
  "status": { "type": "success", "message": "" },
  "data": [
    {
      "accountNumber": "0000001234",
      "premiseNumber": "0000009876",
      "serviceAddress": { "address1": "123 Main St", "address2": "", "city": "Springfield", "state": "IA" },
      "nickName": null
    },
    {
      "accountNumber": "0000105678",
      "premiseNumber": "0000009877",
      "serviceAddress": { "address1": "123 Main St Unit 2", "address2": "", "city": "Springfield", "state": "IA" },
      "nickName": "The workshop"
    }
  ]
}`

	alliantCurrentAnswer = `{
  "status": { "type": "success", "code": 200, "message": "success", "error": false },
  "data": {
    "accountNumber": "0000001234",
    "invoiceId": "900000000001",
    "previousBalance": 150.00,
    "paymentReceived": 150.00,
    "remainingBalance": 120.00,
    "currentCharges": 120.00,
    "totalAmountDue": 120.00,
    "billPeriodStartDate": null,
    "billPeriodEndDate": null,
    "netDueDate": "09-24-2026",
    "invoiceDate": "09-02-2026",
    "upcomingAutoPayDate": "09-22-2026"
  }
}`

	alliantUnreadableAnswer = `{
  "status": { "type": "success", "code": 200, "message": "success", "error": false },
  "data": {
    "accountNumber": "0000001234",
    "invoiceId": "900000000002",
    "remainingBalance": 0,
    "totalAmountDue": "see statement",
    "netDueDate": "08-25-2026",
    "invoiceDate": "08-03-2026"
  }
}`

	alliantHistoryAnswer = `{
  "status": { "type": "success", "code": 200, "message": "success", "error": false },
  "data": [
    { "accountNumber": "0000001234", "billDate": "09-02-2026", "dueDate": null, "invoiceId": "900000000001", "amountDue": 120.00 },
    { "accountNumber": "0000001234", "billDate": "08-03-2026", "dueDate": null, "invoiceId": "900000000002", "amountDue": 150.00 }
  ]
}`

	alliantStatementLinkAnswer = `{
  "status": { "type": "success", "code": 200, "message": "success", "error": false },
  "data": {
    "billPdf": "https://statements.invalid/ALLIANTENERGY/BillPopLogin.aspx?id=invented-signed-id&docid=900000000001&format=pdf",
    "insertDoc": []
  }
}`
)

func alliantModule() *Alliant { return &Alliant{Base: "https://service.invalid"} }

func alliantCall(fetch *scriptedFetch, session Session) (Call, *Notes) {
	notes := &Notes{}
	return Call{
		Ctx: context.Background(), Fetch: fetch, Session: session, Notes: notes,
		Now: func() time.Time { return time.Date(2026, 9, 19, 4, 0, 0, 0, time.UTC) },
	}, notes
}

func keptAlliantSession(t *testing.T) Session {
	t.Helper()
	return mustSession(alliantSession{
		Kind:         alliantSessionKind,
		AccessToken:  "invented-access-token",
		RefreshToken: "invented-refresh-token",
		UUID:         "11111111-2222-3333-4444-555555555555",
		ExpiresAt:    time.Now().Add(15 * time.Minute).UTC().Format(time.RFC3339),
		AccountHint:  "Alex",
	})
}

func TestTheSignInPostsThePlatformHeadersAndKeepsTheTokenAndUUID(t *testing.T) {
	fetch := scripted(t, jsonStep(200, alliantAuthAnswer))
	call, notes := alliantCall(fetch, nil)

	out := alliantModule().Authenticate(context.Background(),
		Credentials{Username: "someone@example.test", Password: "invented"}, call)
	require.Empty(t, out.Failed)
	require.Nil(t, out.Challenge)

	asked := fetch.last()
	require.Equal(t, http.MethodPost, asked.Method)
	require.Equal(t, "https://service.invalid"+alliantAuthPath, asked.URL)
	require.Equal(t, "PL", asked.Headers.Get("st"))
	require.Equal(t, "1", asked.Headers.Get("uid"))
	require.Equal(t, "1", asked.Headers.Get("pt"))
	require.Equal(t, alliantOrigin, asked.Headers.Get("origin"))
	require.Contains(t, asked.Body, `"customattributes"`)

	kept, err := readAlliantSession(out.Session)
	require.NoError(t, err)
	require.Equal(t, alliantSessionKind, kept.Kind)
	require.Equal(t, "invented-access-token", kept.AccessToken)
	require.Equal(t, "invented-refresh-token", kept.RefreshToken)
	require.Equal(t, "11111111-2222-3333-4444-555555555555", kept.UUID)
	require.Equal(t, "someone@example.test", kept.Username)
	require.Equal(t, "Alex", kept.AccountHint)
	// expiresIn is minutes on this platform, and twenty of them from the call.
	require.Equal(t, "2026-09-19T04:20:00Z", kept.ExpiresAt)
	require.Equal(t, "2026-09-19T04:20:00Z", string(out.Session.ExpiresAt().UTC().Format(time.RFC3339)))
	require.Empty(t, notes.List(), "how long the token lasts is for the log")
	require.Len(t, notes.Traces(), 1)
	require.Contains(t, notes.Traces()[0], "signed in to Alliant Energy")
	// Nothing anybody typed is ever in a note or a trace.
	require.NotContains(t, notes.Traces()[0], "invented")
}

func TestARefusedSignInIsFailedInTheServicesOwnWords(t *testing.T) {
	fetch := scripted(t, jsonStep(401,
		`{"status":{"type":"error","message":"Invalid username or password"},"data":null}`))
	call, _ := alliantCall(fetch, nil)

	out := alliantModule().Authenticate(context.Background(),
		Credentials{Username: "someone@example.test", Password: "wrong"}, call)
	require.Equal(t, "Invalid username or password", out.Failed)
	require.Empty(t, out.Session)
}

func TestASignInWithNoLoginIsRefusedBeforeAnythingIsAsked(t *testing.T) {
	fetch := scripted(t)
	call, _ := alliantCall(fetch, nil)
	out := alliantModule().Authenticate(context.Background(), Credentials{Username: "someone"}, call)
	require.Contains(t, out.Failed, "needs a username and a password")
	require.Empty(t, fetch.calls)
}

func TestTheAccountWalkIsTheSubaccountListMaskedByItsLastFour(t *testing.T) {
	fetch := scripted(t, jsonStep(200, alliantAddressesAnswer))
	call, notes := alliantCall(fetch, keptAlliantSession(t))

	found, err := alliantModule().Subaccounts(call)
	require.NoError(t, err)
	require.Len(t, found, 2)
	require.Equal(t, "0000009876-0000001234", found[0].ExternalID)
	require.Equal(t, "123 Main St, Springfield", found[0].Label)
	require.Equal(t, "••••1234", found[0].MaskedNumber)
	// A nickname wins over the address.
	require.Equal(t, "The workshop", found[1].Label)
	require.Equal(t, "••••5678", found[1].MaskedNumber)

	asked := fetch.last()
	require.Equal(t, http.MethodGet, asked.Method)
	require.Contains(t, asked.URL, alliantAddressesPath+"/11111111-2222-3333-4444-555555555555")
	require.Equal(t, "Bearer invented-access-token", asked.Headers.Get("authorization"))
	require.Equal(t, "2", asked.Headers.Get("uid"))
	require.Contains(t, notes.List()[0], "lists 2 billed accounts")
}

func TestARefusedTokenOnTheAccountWalkIsASignInTheHouseholdOwes(t *testing.T) {
	fetch := scripted(t, jsonStep(401, `{"status":{"type":"error","message":"Unauthorized Access"}}`))
	call, _ := alliantCall(fetch, keptAlliantSession(t))
	_, err := alliantModule().Subaccounts(call)
	require.ErrorIs(t, err, ErrNeedsSignIn)
}

func TestTheCurrentBillIsMappedWithMoneyAsAStringAndDatesFromMMDDYYYY(t *testing.T) {
	fetch := scripted(t, jsonStep(200, alliantCurrentAnswer), jsonStep(200, alliantHistoryAnswer))
	call, notes := alliantCall(fetch, keptAlliantSession(t))
	call.Subaccounts = []string{"0000009876-0000001234"}

	out, err := alliantModule().FetchBills(call)
	require.NoError(t, err)
	require.False(t, out.NeedsSignIn)
	require.Len(t, out.Bills, 2)

	bill := out.Bills[0]
	require.Equal(t, "0000009876-0000001234", bill.Subaccount)
	require.Equal(t, "900000000001", bill.ExternalID)
	require.Equal(t, "120.00", bill.AmountDue.String(), "money crosses as a string, never a float")
	require.Equal(t, "2026-09-24", bill.DueOn)
	require.Equal(t, "2026-09-02", bill.IssuedOn)
	require.Equal(t, "2026-09-22", bill.AutopayOn)
	require.Equal(t, "USD", bill.Currency)
	require.Equal(t, "Open", bill.Status)
	require.Empty(t, bill.PeriodStart, "the service states no period, so none is invented")

	// The history's copy of the current invoice is not sent twice; the earlier
	// one is sent settled, due as many days after its bill date as the current
	// bill is after its own: 2026-09-02 to 2026-09-24 is 22 days.
	earlier := out.Bills[1]
	require.Equal(t, "900000000002", earlier.ExternalID)
	require.Equal(t, "2026-08-03", earlier.IssuedOn)
	require.Equal(t, "2026-08-25", earlier.DueOn)
	require.Equal(t, "150.00", earlier.AmountDue.String())
	require.Equal(t, "Paid", earlier.Status)
	require.Contains(t, strings.Join(notes.List(), "\n"),
		"1 earlier bill on 0000009876-0000001234 filed as paid, each due 22 days after its bill date")

	// The account number behind the id is what the billing call asks for.
	require.Contains(t, fetch.calls[0].Body, `"accountNumbers":["0000001234"]`)
	// yyyyMMdd, which is how bill/History wants its window: two years back.
	require.Contains(t, fetch.calls[1].Body, `"startDate":"20240919"`)
	require.Contains(t, fetch.calls[1].Body, `"endDate":"20260919"`)
}

func TestABillWhoseAmountCannotBeReadIsANoteInsteadOfABillAndAPaidOneReadsPaid(t *testing.T) {
	fetch := scripted(t, jsonStep(200, alliantUnreadableAnswer), jsonStep(200, alliantHistoryAnswer))
	call, notes := alliantCall(fetch, keptAlliantSession(t))
	call.Subaccounts = []string{"0000009876-0000001234"}

	out, err := alliantModule().FetchBills(call)
	require.NoError(t, err)
	for _, bill := range out.Bills {
		require.NotEqual(t, "900000000002", bill.ExternalID,
			"a bill sent as zero, or as settled from the history, would tell the household nothing is owed")
	}
	require.Contains(t, strings.Join(notes.List(), "\n"), "could not read")
	// With no current bill to take the term from, the earlier one is dated on
	// its bill date, and the note says so.
	require.Len(t, out.Bills, 1)
	require.Equal(t, "2026-09-02", out.Bills[0].DueOn)
	require.Equal(t, "Paid", out.Bills[0].Status)
	require.Contains(t, strings.Join(notes.List(), "\n"), "dated on the bill date")

	// The same row with a readable amount and nothing left owing reads Paid.
	var row map[string]any
	require.NoError(t, json.Unmarshal([]byte(alliantUnreadableAnswer), &row))
	data := row["data"].(map[string]any)
	data["totalAmountDue"] = 150.00
	bill, ok := AlliantBillFromRow(data, "0000009876-0000001234", &Notes{})
	require.True(t, ok)
	require.Equal(t, "Paid", bill.Status)
	require.Equal(t, "150.00", bill.AmountDue.String())
}

func TestAnHTTPFailureOnTheCurrentBillIsANoteNamingTheRouteAndNoBills(t *testing.T) {
	fetch := scripted(t, jsonStep(500, `{"error":"upstream"}`))
	call, notes := alliantCall(fetch, keptAlliantSession(t))
	call.Subaccounts = []string{"0000009876-0000001234"}

	out, err := alliantModule().FetchBills(call)
	require.NoError(t, err)
	require.Empty(t, out.Bills)
	require.Contains(t, notes.List()[0], alliantCurrentBillPath)
	require.Contains(t, notes.List()[0], "HTTP 500")
}

func TestARefusedTokenOnTheBillingRouteIsASignInTheHouseholdOwes(t *testing.T) {
	fetch := scripted(t, jsonStep(403, `{"status":{"type":"error","message":"Unauthorized Access"}}`))
	call, _ := alliantCall(fetch, keptAlliantSession(t))
	call.Subaccounts = []string{"0000009876-0000001234"}

	out, err := alliantModule().FetchBills(call)
	require.NoError(t, err)
	require.True(t, out.NeedsSignIn)
	require.Contains(t, out.Reason, "refused the kept token")
}

func TestTheRefreshIsTriedHonestlyAndARefusalAsksForASignIn(t *testing.T) {
	fetch := scripted(t, jsonStep(200, `{
      "status": { "type": "success" },
      "data": { "accessToken": "invented-second-token", "refreshToken": "invented-second-refresh", "expiresIn": 20 }
    }`))
	call, notes := alliantCall(fetch, keptAlliantSession(t))

	session, needsSignIn, _ := alliantModule().Refresh(call)
	require.False(t, needsSignIn)
	kept, err := readAlliantSession(session)
	require.NoError(t, err)
	require.Equal(t, "invented-second-token", kept.AccessToken)
	require.Equal(t, "invented-second-refresh", kept.RefreshToken)
	// The uuid is not restated by the refresh and is carried over, or the next
	// account walk would have nobody to walk.
	require.Equal(t, "11111111-2222-3333-4444-555555555555", kept.UUID)
	require.Contains(t, notes.Traces()[0], "refreshed")
	require.Contains(t, fetch.last().URL, alliantRefreshPath)

	// An expired session's refusal.
	refused := scripted(t, jsonStep(401, `{"status":{"type":"error","message":"Unauthorized Access"}}`))
	call, notes = alliantCall(refused, keptAlliantSession(t))
	_, needsSignIn, reason := alliantModule().Refresh(call)
	require.True(t, needsSignIn)
	require.Contains(t, reason, "401")
	require.Contains(t, notes.Traces()[0], alliantRefreshPath)
	require.Contains(t, notes.Traces()[0], "answered HTTP 401: Unauthorized Access;")
}

func TestARefreshWithNoRefreshTokenAsksForASignInWithoutACall(t *testing.T) {
	fetch := scripted(t)
	call, _ := alliantCall(fetch, mustSession(alliantSession{Kind: alliantSessionKind, AccessToken: "x"}))
	_, needsSignIn, reason := alliantModule().Refresh(call)
	require.True(t, needsSignIn)
	require.Contains(t, reason, "no refresh token")
	require.Empty(t, fetch.calls)
}

func TestAStatementIsAskedForByInvoiceAndFetchedFromTheSignedLink(t *testing.T) {
	fetch := scripted(t, jsonStep(200, alliantStatementLinkAnswer), pdfStep())
	call, _ := alliantCall(fetch, keptAlliantSession(t))
	bill := Bill{
		Subaccount: "0000009876-0000001234", ExternalID: "900000000001",
		DueOn: "2026-09-24", Raw: json.RawMessage(`{"invoiceId":"900000000001"}`),
	}

	document, err := alliantModule().FetchDocument(call, bill)
	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "alliant-0000009876-0000001234-2026-09-24.pdf", document.Filename)
	require.True(t, strings.HasPrefix(string(document.Bytes), "%PDF-"))

	require.Contains(t, fetch.calls[0].Body, `"documentType":"Bill"`)
	require.Contains(t, fetch.calls[0].Body, `"invoiceId":"900000000001"`)
	require.Contains(t, fetch.calls[0].Body, `"accountNumber":"0000001234"`)
	require.Equal(t, "https://statements.invalid/ALLIANTENERGY/BillPopLogin.aspx?id=invented-signed-id&docid=900000000001&format=pdf",
		fetch.calls[1].URL)
}

func TestAStatementRefusalIsANoteAndNoDocumentRatherThanAFailedPull(t *testing.T) {
	// The link is answered and the host refuses it.
	fetch := scripted(t, jsonStep(200, alliantStatementLinkAnswer), jsonStep(404, "not found"))
	call, notes := alliantCall(fetch, keptAlliantSession(t))
	bill := Bill{Subaccount: "0000009876-0000001234", ExternalID: "900000000001", DueOn: "2026-09-24"}

	document, err := alliantModule().FetchDocument(call, bill)
	require.NoError(t, err, "a statement that cannot be had never fails the pull")
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "HTTP 404")

	// And a host that answers something that is not a PDF.
	notPDF := scripted(t, jsonStep(200, alliantStatementLinkAnswer),
		scriptedStep{status: 200, body: "<html>sign in</html>", contentType: "text/html"})
	call, notes = alliantCall(notPDF, keptAlliantSession(t))
	document, err = alliantModule().FetchDocument(call, bill)
	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestAnEarlierBillKeepsADueDateTheHistoryStatesAndOneItCannotKeyIsCounted(t *testing.T) {
	current := Bill{ExternalID: "900000000001", IssuedOn: "2026-09-02", DueOn: "2026-09-24"}
	rows := []map[string]any{
		{"invoiceId": "900000000003", "billDate": "07-06-2026", "dueDate": "07-27-2026", "amountDue": 90.00},
		{"invoiceId": "900000000004", "billDate": "06-04-2026", "dueDate": nil, "amountDue": 80.00},
		{"invoiceId": nil, "billDate": "05-05-2026", "amountDue": 70.00},
		{"invoiceId": "900000000005", "billDate": nil, "amountDue": 60.00},
	}
	notes := &Notes{}

	bills := AlliantEarlierBills(rows, current, "0000009876-0000001234", notes)
	require.Len(t, bills, 2)
	require.Equal(t, "2026-07-27", bills[0].DueOn, "a due date the service states is never replaced")
	require.Equal(t, "2026-06-26", bills[1].DueOn)
	for _, bill := range bills {
		require.Equal(t, "Paid", bill.Status)
		require.Equal(t, "0000009876-0000001234", bill.Subaccount)
	}
	joined := strings.Join(notes.List(), "\n")
	require.Contains(t, joined,
		"2 earlier bills on 0000009876-0000001234 carried no invoice or no bill date and were left out")
	require.Contains(t, joined, "2 earlier bills on 0000009876-0000001234 filed as paid")
}

func TestAnAnswerThatIsNotAPDFIsNamedByHowItBegan(t *testing.T) {
	notPDF := scripted(t, jsonStep(200, alliantStatementLinkAnswer),
		scriptedStep{status: 200, body: "<!doctype html>\n<html>  <body>viewer</body></html>", contentType: "application/pdf"})
	call, notes := alliantCall(notPDF, keptAlliantSession(t))
	bill := Bill{Subaccount: "0000009876-0000001234", ExternalID: "900000000002", IssuedOn: "2026-08-03",
		DueOn: "2026-08-25", Status: "Paid", Raw: json.RawMessage(`{"invoiceId":"900000000002"}`)}

	document, err := alliantModule().FetchDocument(call, bill)
	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], `it began "<!doctype html> <html> <body>viewer</body></html>"`)
	require.Contains(t, notPDF.calls[0].Body, `"invoiceId":"900000000002"`)
}
