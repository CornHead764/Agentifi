package billers

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
)

// The customer block's field names are fetchDigitalBill's own; every value is
// invented.

func spectrumCustomer() map[string]any {
	return map[string]any{
		"accountNumber":   "82000000001234",
		"autoPayDate":     "2026-09-12",
		"paymentDueDate":  "2026-09-15",
		"paymentDueText":  "",
		"statementDate":   "2026-08-25",
		"startDate":       "2026-08-25",
		"endDate":         "2026-09-24",
		"totalAmountDue":  75,
		"unpaidBalance":   75,
		"previousBalance": 0,
	}
}

func day(t *testing.T, value string) time.Time {
	t.Helper()
	parsed, err := time.Parse("2006-01-02", value)
	require.NoError(t, err)
	return parsed
}

func TestTheNewestSpectrumStatementIsAnOpenBillWithThePortalsOwnDatesAndTotal(t *testing.T) {
	notes := &Notes{}
	bill, ok := SpectrumBillFromStatement(spectrumCustomer(), SpectrumStatementAt{
		PDFID: "pdf-1", Newest: true, Today: day(t, "2026-09-01"),
	}, notes)

	require.True(t, ok)
	require.Equal(t, "82000000001234", bill.Subaccount)
	require.Equal(t, "75.00", bill.AmountDue.String())
	require.Equal(t, "2026-09-15", bill.DueOn)
	require.Equal(t, "2026-09-12", bill.AutopayOn)
	require.Equal(t, "2026-08-25", bill.IssuedOn)
	require.Equal(t, "2026-09-24", bill.PeriodEnd)
	require.Equal(t, "Open", bill.Status)
	require.Equal(t, "82000000001234:2026-08-25", bill.ExternalID)

	var raw spectrumRaw
	require.NoError(t, json.Unmarshal(bill.Raw, &raw))
	require.Equal(t, "pdf-1", raw.PDFID)
	require.Empty(t, notes.List())
}

func TestASpectrumAutopayStatementTakesTheDateInTheDueTextElseTheAutopayDate(t *testing.T) {
	customer := spectrumCustomer()
	customer["paymentDueDate"] = ""
	customer["paymentDueText"] = "Auto Pay scheduled for 09/13/2026"
	inText, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{Newest: true}, &Notes{})
	require.True(t, ok)
	require.Equal(t, "2026-09-13", inText.DueOn)

	customer["paymentDueText"] = ""
	fromAutopay, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{Newest: true}, &Notes{})
	require.True(t, ok)
	require.Equal(t, "2026-09-12", fromAutopay.DueOn)
}

func TestAnOlderSpectrumStatementIsPaidAndSoIsASettledNewestOne(t *testing.T) {
	older, ok := SpectrumBillFromStatement(spectrumCustomer(), SpectrumStatementAt{}, &Notes{})
	require.True(t, ok)
	require.Equal(t, "Paid", older.Status)

	// The newest, past its due date with nothing left unpaid.
	customer := spectrumCustomer()
	customer["unpaidBalance"] = 0
	settled, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{
		Newest: true, Today: day(t, "2026-10-01"),
	}, &Notes{})
	require.True(t, ok)
	require.Equal(t, "Paid", settled.Status)
}

func TestASpectrumStatementWithoutAReadableTotalIsLeftOutWithANote(t *testing.T) {
	customer := spectrumCustomer()
	customer["totalAmountDue"] = nil
	notes := &Notes{}

	_, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{Newest: true}, notes)

	require.False(t, ok)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no readable total")
}

func TestASpectrumAccountShowsUnderItsServiceAddressAndHasOneWhenItDoesNot(t *testing.T) {
	require.Equal(t, "Example Service Address, Anytown", spectrumAddressLabel(map[string]any{
		"line1": "Example Service Address", "city": "Anytown", "state": "OH",
	}))
	require.Equal(t, "Internet", spectrumAddressLabel(nil))
	require.Equal(t, "Internet", spectrumAddressLabel(map[string]any{}))
}

// The module reads its own bearer, its own statements and its own PDF over a
// stub page: what is asserted is the decisions, not Chromium.
func TestSpectrumSaysASignInIsOwedWhenThePageSendsNoBearer(t *testing.T) {
	module := NewSpectrum()
	page := stubSpectrumPage(nil)
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	// The portal's own redirect: back to the sign-in, carrying where it came
	// from in the query. The note must name the page and not the query.
	page.OnGoto = func(string) error {
		page.Location = "https://www.spectrum.net/login?return=%2Fbilling"
		return nil
	}
	notes := &Notes{}

	result, err := module.FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Spectrum asked to sign in again", result.Reason)
	require.Contains(t, notes.Traces()[0], "the profile is not signed in")
	// The trace names the page and never its query string.
	require.NotContains(t, notes.Traces()[0], "?")
}

func TestSpectrumAnswersItsThreeNewestStatementsAndPinsEachPDFByDate(t *testing.T) {
	module := NewSpectrum()
	notes := &Notes{}

	result, err := module.FetchBills(Call{
		Page:  stubSpectrumPage(nil),
		Notes: notes,
		Now:   func() time.Time { return day(t, "2026-09-01") },
	})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn)
	require.Len(t, result.Bills, 3)
	require.Equal(t, []string{"2026-09-15", "2026-08-15", "2026-07-15"},
		[]string{result.Bills[0].DueOn, result.Bills[1].DueOn, result.Bills[2].DueOn})
	require.Equal(t, "Open", result.Bills[0].Status)
	require.Equal(t, "Paid", result.Bills[1].Status)

	var raw spectrumRaw
	require.NoError(t, json.Unmarshal(result.Bills[0].Raw, &raw))
	require.Equal(t, "pdf-2026-08-25", raw.PDFID)
}

func TestARefusedSpectrumGraphCallIsASignInOwedRatherThanAFailedPull(t *testing.T) {
	module := NewSpectrum()
	page := stubSpectrumPage(func(operation string, answer map[string]any) map[string]any {
		if operation == "fetchDigitalBillStatementsList" {
			return map[string]any{"status": 401, "json": nil, "excerpt": "Unauthorized"}
		}
		return answer
	})

	result, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.True(t, result.NeedsSignIn)
	require.Equal(t, "Spectrum refused the kept session", result.Reason)
}

func TestASpectrumStatementThatDecodesToSomethingElseIsNoDocumentAndANote(t *testing.T) {
	module := NewSpectrum()
	bill := Bill{Subaccount: "82000000001234", ExternalID: "82000000001234:2026-08-25"}
	bill.Raw, _ = json.Marshal(spectrumRaw{PDFID: "pdf-1", StatementDate: "2026-08-25"})
	notes := &Notes{}

	page := stubSpectrumPage(func(operation string, answer map[string]any) map[string]any {
		if operation == "FetchStatementPDFEncodedString" {
			return spectrumData(map[string]any{
				"viewer": map[string]any{"account": map[string]any{
					// Base64 of "<html>Sign in</html>" — a portal answering its
					// own sign-in page with a 200.
					"statementPdf": "PGh0bWw+U2lnbiBpbjwvaHRtbD4=",
				}},
			})
		}
		return answer
	})

	document, err := module.FetchDocument(Call{Page: page, Notes: notes}, bill)

	require.NoError(t, err)
	require.Nil(t, document)
	require.Contains(t, notes.List()[0], "not a PDF")
}

func TestASpectrumStatementIsAPDFNamedForItsAccountsLastFourAndItsDate(t *testing.T) {
	module := NewSpectrum()
	bill := Bill{Subaccount: "82000000001234", ExternalID: "82000000001234:2026-08-25"}
	bill.Raw, _ = json.Marshal(spectrumRaw{PDFID: "pdf-1", StatementDate: "2026-08-25"})

	document, err := module.FetchDocument(Call{Page: stubSpectrumPage(nil), Notes: &Notes{}}, bill)

	require.NoError(t, err)
	require.NotNil(t, document)
	require.Equal(t, "application/pdf", document.ContentType)
	require.Equal(t, "spectrum-1234-2026-08-25.pdf", document.Filename)
	require.True(t, len(document.Bytes) > 0)
}

func TestANewestSpectrumStatementWhoseUnpaidBalanceWillNotReadStaysOpenWithANote(t *testing.T) {
	customer := spectrumCustomer()
	customer["unpaidBalance"] = "--"
	notes := &Notes{}

	// Past its due date: only the unreadable balance can keep it open.
	bill, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{
		Newest: true, Today: day(t, "2026-10-01"),
	}, notes)

	require.True(t, ok)
	require.Equal(t, "Open", bill.Status)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no readable unpaid balance")

	// An older statement is paid whatever its figures say, and says nothing.
	olderNotes := &Notes{}
	older, ok := SpectrumBillFromStatement(customer, SpectrumStatementAt{
		Today: day(t, "2026-10-01"),
	}, olderNotes)
	require.True(t, ok)
	require.Equal(t, "Paid", older.Status)
	require.Empty(t, olderNotes.List())
}

func TestSpectrumSortsItsStatementsByDayRatherThanByTheTextTheyArriveAs(t *testing.T) {
	// As text, "9/25/2026" sorts above "10/25/2026" and "12/25/2025": the
	// September statement would be taken for the newest and October's dropped.
	listed := []struct{ id, date, issued, due, unpaid string }{
		{"digital-sep", "9/25/2026", "2026-09-25", "2026-10-15", "0"},
		{"digital-oct", "10/25/2026", "2026-10-25", "2026-11-15", "60.00"},
		{"digital-aug", "8/25/2026", "2026-08-25", "2026-09-15", "0"},
		{"digital-dec", "12/25/2025", "2025-12-25", "2026-01-15", "0"},
	}
	page := stubSpectrumPage(func(operation string, answer map[string]any) map[string]any {
		if operation != "fetchDigitalBillStatementsList" {
			return answer
		}
		var statements []any
		for _, one := range listed {
			statements = append(statements, map[string]any{"id": one.id, "date": one.date, "version": "1"})
		}
		return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
			"digitalStatementList": map[string]any{"statements": statements},
		}}})
	})
	// Each statement answers its own customer block; the stub's four do not
	// carry these ids.
	var asked []string
	evaluate := page.OnEvaluate
	page.OnEvaluate = func(script string, arg any) (any, error) {
		call, _ := arg.(map[string]any)
		body, _ := call["body"].(map[string]any)
		if body["operationName"] != "fetchDigitalBill" {
			return evaluate(script, arg)
		}
		variables, _ := body["variables"].(map[string]any)
		id, _ := variables["statementId"].(string)
		asked = append(asked, id)
		for _, one := range listed {
			if one.id != id {
				continue
			}
			return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
				"digitalStatement": map[string]any{"customer": map[string]any{
					"accountNumber":  "82000000001234",
					"statementDate":  one.issued,
					"paymentDueDate": one.due,
					"totalAmountDue": "60.00",
					"unpaidBalance":  one.unpaid,
				}},
			}}}), nil
		}
		return evaluate(script, arg)
	}

	result, err := NewSpectrum().FetchBills(Call{
		Page: page, Notes: &Notes{},
		Now: func() time.Time { return day(t, "2026-12-01") },
	})

	require.NoError(t, err)
	require.Equal(t, []string{"digital-oct", "digital-sep", "digital-aug"}, asked)
	require.Len(t, result.Bills, 3)
	require.Equal(t, "2026-10-25", result.Bills[0].IssuedOn)
	require.Equal(t, "Open", result.Bills[0].Status)
	require.Equal(t, "Paid", result.Bills[1].Status)
}

func TestASpectrumStatementListThatWillNotReadIsNoBillsAndSaysSo(t *testing.T) {
	page := stubSpectrumPage(func(operation string, answer map[string]any) map[string]any {
		if operation == "fetchDigitalBillStatementsList" {
			return spectrumData(map[string]any{"viewer": nil})
		}
		return answer
	})
	notes := &Notes{}

	result, err := NewSpectrum().FetchBills(Call{Page: page, Notes: notes})

	require.NoError(t, err)
	require.False(t, result.NeedsSignIn, "an answer with no list in it is not a refusal")
	require.Empty(t, result.Bills)
	require.Len(t, notes.List(), 1)
	require.Contains(t, notes.List()[0], "no statements were read")
}

func TestSpectrumsAccountWalkSaysASignInIsOwedWhenThePageSendsNoBearer(t *testing.T) {
	page := stubSpectrumPage(nil)
	page.OnWaitFor = func(string, time.Duration) error { return errTimedOut }
	notes := &Notes{}

	found, err := NewSpectrum().Subaccounts(Call{Page: page, Notes: notes})

	require.ErrorIs(t, err, ErrNeedsSignIn)
	require.Empty(t, found)
	require.Contains(t, notes.Traces()[0], "the profile is not signed in")
}

func TestSpectrumsAccountWalkSaysASignInIsOwedWhenTheGraphRefusesIt(t *testing.T) {
	page := stubSpectrumPage(func(operation string, answer map[string]any) map[string]any {
		if operation == "fetchAccountGlobal" {
			return map[string]any{"status": 401, "json": nil, "excerpt": "Unauthorized"}
		}
		return answer
	})

	_, err := NewSpectrum().Subaccounts(Call{Page: page, Notes: &Notes{}})

	require.ErrorIs(t, err, ErrNeedsSignIn)
}

func TestSpectrumListsTheBilledAccountsUnderTheLogin(t *testing.T) {
	found, err := NewSpectrum().Subaccounts(Call{Page: stubSpectrumPage(nil), Notes: &Notes{}})

	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "82000000001234", found[0].ExternalID)
	require.Equal(t, "Example Service Address, Anytown", found[0].Label)
}

func TestEverySpectrumCallIsHandedShapesAPageCanTake(t *testing.T) {
	module := NewSpectrum()
	page := stubSpectrumPage(nil)

	_, err := module.FetchBills(Call{Page: page, Notes: &Notes{}})

	require.NoError(t, err)
	require.NotEmpty(t, page.Args, "the GraphQL calls all go through Evaluate")
	for index, arg := range page.Args {
		require.NoErrorf(t, browser.Serializable(arg), "the argument of call %d", index+1)
	}
}

// errTimedOut is a wait the page never satisfied.
var errTimedOut = errors.New("timed out")

// `rewrite` is how a test makes one operation answer differently — a 401, a
// document that is not a PDF — without restating the other four.
func stubSpectrumPage(rewrite func(operation string, answer map[string]any) map[string]any) *browser.StubPage {
	page := &browser.StubPage{Location: spectrumBilling}
	page.OnEvaluate = func(script string, arg any) (any, error) {
		if script != spectrumCall {
			return nil, nil
		}
		asked, _ := arg.(map[string]any)
		body, _ := asked["body"].(map[string]any)
		operation, _ := body["operationName"].(string)
		variables, _ := body["variables"].(map[string]any)
		answer := spectrumStubAnswer(operation, variables)
		if rewrite != nil {
			answer = rewrite(operation, answer)
		}
		return answer, nil
	}
	return page
}

// The four statements the stub portal lists, newest first, with the due date
// each one's customer block carries. Invented throughout.
var spectrumStubStatements = []struct{ id, date, due, unpaid string }{
	{"digital-1", "2026-08-25", "2026-09-15", "75.00"},
	{"digital-2", "2026-07-25", "2026-08-15", "0"},
	{"digital-3", "2026-06-25", "2026-07-15", "0"},
	{"digital-4", "2026-05-25", "2026-06-15", "0"},
}

func spectrumStubAnswer(operation string, variables map[string]any) map[string]any {
	switch operation {
	case "fetchAccountGlobal":
		return spectrumData(map[string]any{"accounts": map[string]any{"core": []any{
			map[string]any{
				"accountNumber": "82000000001234",
				"serviceAddress": map[string]any{
					"line1": "Example Service Address", "city": "Anytown", "state": "OH",
				},
			},
		}}})
	case "fetchDigitalBillStatementsList":
		// Listed out of order on purpose: the module sorts by date rather than
		// trusting the portal's own.
		var statements []any
		for _, one := range []int{2, 0, 3, 1} {
			statements = append(statements, map[string]any{
				"id":      spectrumStubStatements[one].id,
				"date":    spectrumStubStatements[one].date,
				"version": "1",
			})
		}
		return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
			"digitalStatementList": map[string]any{"statements": statements},
		}}})
	case "fetchAccountStatementList":
		var statements []any
		for _, one := range spectrumStubStatements {
			statements = append(statements, map[string]any{"id": "pdf-" + one.date, "date": one.date})
		}
		return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
			"statementList": map[string]any{"statements": statements},
		}}})
	case "fetchDigitalBill":
		wanted, _ := variables["statementId"].(string)
		for _, one := range spectrumStubStatements {
			if one.id != wanted {
				continue
			}
			return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
				"digitalStatement": map[string]any{"customer": map[string]any{
					"accountNumber":  "82000000001234",
					"statementDate":  one.date,
					"paymentDueDate": one.due,
					"autoPayDate":    "",
					"paymentDueText": "",
					"startDate":      one.date,
					"endDate":        one.due,
					"totalAmountDue": "75.00",
					"unpaidBalance":  one.unpaid,
				}},
			}}})
		}
		return map[string]any{"status": 404, "json": nil, "excerpt": "no such statement"}
	case "FetchStatementPDFEncodedString":
		return spectrumData(map[string]any{"viewer": map[string]any{"account": map[string]any{
			"statementPdf": base64.StdEncoding.EncodeToString([]byte("%PDF-1.4 invented")),
		}}})
	}
	return map[string]any{"status": 400, "json": nil, "excerpt": "unknown operation"}
}

func spectrumData(data map[string]any) map[string]any {
	return map[string]any{"status": 200, "json": map[string]any{"data": data}, "excerpt": ""}
}
