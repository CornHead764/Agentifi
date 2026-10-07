package api

import (
	"bytes"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// seedRate stores one day's conversion factor for a space.
func seedRate(l *ledger, quote, factor string, on domain.Date) {
	l.t.Helper()
	rate, err := decimal.NewFromString(factor)
	require.NoError(l.t, err)
	require.NoError(l.t, store.NewFxRates(l.env.DB).UpsertRates(l.t.Context(),
		[]provider.FxRate{{
			SpaceID: domain.ID(l.id("space").String()), BaseCurrency: "USD",
			QuoteCurrency: quote, On: on, Rate: rate, Source: "test",
		}}))
}

// Importing a statement file from the app.
//
// The property under test throughout is that this endpoint is the CLI: the
// same readers, the same mapper, the same idempotent writer. Anything it does
// differently would be a second way to write somebody's ledger.

// upload posts one file to /imports the way a browser would.
func upload(c *client, name, body string, fields map[string]string) *response {
	c.t.Helper()
	return uploadTo(c, "/imports", name, body, fields)
}

// uploadTo posts one file to any endpoint that takes a multipart form.
func uploadTo(c *client, path, name, body string, fields map[string]string) *response {
	c.t.Helper()
	var buffer bytes.Buffer
	form := multipart.NewWriter(&buffer)
	part, err := form.CreateFormFile("file", name)
	require.NoError(c.t, err)
	_, err = part.Write([]byte(body))
	require.NoError(c.t, err)
	for key, value := range fields {
		require.NoError(c.t, form.WriteField(key, value))
	}
	require.NoError(c.t, form.Close())

	r := httptest.NewRequest(http.MethodPost, path, &buffer)
	r.Header.Set("Content-Type", form.FormDataContentType())
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.spaceID != "" {
		r.Header.Set("X-Space-Id", c.spaceID)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

const uploadOFX = `OFXHEADER:100
DATA:OFXSGML
VERSION:102

<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>USD
<BANKACCTFROM><ACCTID>000123456789<ACCTTYPE>CHECKING</BANKACCTFROM>
<BANKTRANLIST>
<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260816<TRNAMT>-12.34<FITID>ofx-1<NAME>SAFEWAY</STMTTRN>
<STMTTRN><TRNTYPE>CREDIT<DTPOSTED>20260815<TRNAMT>2400.00<FITID>ofx-2<NAME>ACME PAYROLL</STMTTRN>
</BANKTRANLIST>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`

func TestADryRunReportsTheFileAndWritesNothing(t *testing.T) {
	l := buildLedger(t)
	before := len(l.alex.get("/accounts").requireStatus(http.StatusOK).list())

	body := upload(l.alex, "statement.qfx", uploadOFX,
		map[string]string{"dry_run": "true", "account": "Everyday Checking"}).
		requireStatus(http.StatusOK).json()

	require.Equal(t, "ofx", body["format"])
	require.Equal(t, true, body["dry_run"])
	require.EqualValues(t, 2, body["transactions"])
	require.Nil(t, body["written"])
	require.Contains(t, body["summary"], "OFX import")

	after := len(l.alex.get("/accounts").requireStatus(http.StatusOK).list())
	require.Equal(t, before, after, "a dry run creates no account")
}

func TestAnUploadWritesTheRowsAndSaysWhatItWrote(t *testing.T) {
	l := buildLedger(t)
	body := upload(l.alex, "statement.qfx", uploadOFX,
		map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusOK).json()

	written, ok := body["written"].(map[string]any)
	require.True(t, ok, "a real import reports what it wrote")
	require.EqualValues(t, 2, written["transactions"])
	require.EqualValues(t, 0, written["accounts"], "it paired with the account already there")
}

func TestImportingTheSameFileTwiceAddsNothingTheSecondTime(t *testing.T) {
	// The property the whole external-id scheme exists for. A household that
	// downloads the same statement twice does not end up owing itself twice.
	l := buildLedger(t)
	first := upload(l.alex, "statement.qfx", uploadOFX,
		map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 2, first["written"].(map[string]any)["transactions"])

	second := upload(l.alex, "statement.qfx", uploadOFX,
		map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusOK).json()
	written := second["written"].(map[string]any)
	require.EqualValues(t, 0, written["transactions"])
	require.EqualValues(t, 2, written["transactions_skipped"])
}

func TestTheFormatIsReadFromTheContentNotTheName(t *testing.T) {
	// A bank that serves a .qfx as text/plain and a user who renamed the
	// download both still have an OFX file. Reading one as a CSV produces a
	// confident report about nothing.
	l := buildLedger(t)
	body := upload(l.alex, "mystery.txt", uploadOFX,
		map[string]string{"dry_run": "true", "account": "Everyday Checking"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "ofx", body["format"])
}

func TestARepeatedProblemCarriesItsCountOnce(t *testing.T) {
	l := buildLedger(t)
	csv := "Date,Account,Reviewed,Payee,Category,Amount\n" +
		"2026-08-25,Everyday Checking,maybe,Corner Store,Groceries,-3.00\n" +
		"2026-08-26,Everyday Checking,maybe,Corner Store,Groceries,-4.00\n" +
		"2026-08-27,Everyday Checking,maybe,Corner Store,Groceries,-5.00\n"
	body := upload(l.alex, "statement.csv", csv, map[string]string{"dry_run": "true"}).
		requireStatus(http.StatusUnprocessableEntity).json()

	problems := body["errors"].([]any)
	require.Len(t, problems, 1)
	line := problems[0].(string)
	require.Equal(t, 1, strings.Count(line, "3"), line)
	require.Contains(t, line, "(x3)")
}

func TestSomethingThatIsNeitherFormatIsRefused(t *testing.T) {
	l := buildLedger(t)
	upload(l.alex, "notes.txt", "just some words\nand more words\n",
		map[string]string{"dry_run": "true"}).
		requireStatus(http.StatusBadRequest)
}

func TestAnEmptyFileIsRefusedByName(t *testing.T) {
	l := buildLedger(t)
	upload(l.alex, "empty.ofx", "", map[string]string{"dry_run": "true"}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestNoFileAtAllIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/imports", map[string]any{"dry_run": true}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAViewerCannotImportAFile(t *testing.T) {
	// It writes a ledger, so it is a write.
	l := buildLedger(t)
	upload(l.as("vera"), "statement.qfx", uploadOFX,
		map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusForbidden)
}

const uploadEUR = `OFXHEADER:100
DATA:OFXSGML

<OFX><BANKMSGSRSV1><STMTTRNRS><STMTRS>
<CURDEF>EUR
<BANKACCTFROM><ACCTID>555<ACCTTYPE>CHECKING</BANKACCTFROM>
<STMTTRN><TRNTYPE>DEBIT<DTPOSTED>20260812<TRNAMT>-40.00<FITID>eur-1<NAME>CAFE PARIS</STMTTRN>
</STMTRS></STMTTRNRS></BANKMSGSRSV1></OFX>`

func TestAForeignStatementIsConvertedOnTheWayIn(t *testing.T) {
	// Without this a euro charge sits in a dollar ledger at face value until
	// the next sync happens to run — and an install with no connection has no
	// next sync at all.
	l := buildLedger(t)
	seedRate(l, "EUR", "0.9", domain.Date{Year: 2026, Month: 8, Day: 12})

	upload(l.alex, "euro.qfx", uploadEUR, map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusOK)

	rows := l.alex.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	var found map[string]any
	for _, raw := range rows["items"].([]any) {
		one := raw.(map[string]any)
		if one["statement_name"] == "CAFE PARIS" {
			found = one
		}
	}
	require.NotNil(t, found, "the row was imported")
	require.Equal(t, "EUR", found["currency"])
	require.Equal(t, "-40.00", found["amount"], "the bank's own number is kept")
	require.Equal(t, "-44.44", found["amount_primary"], "and the household's money is stamped")
}

func TestAForeignStatementWithNoRateKeepsItsOwnNumberOnly(t *testing.T) {
	// A converted-at-a-guess figure is indistinguishable from a real one once
	// stored, so a missing rate leaves the row unconverted rather than wrong.
	l := buildLedger(t)
	upload(l.alex, "euro.qfx", uploadEUR, map[string]string{"account": "Everyday Checking"}).
		requireStatus(http.StatusOK)

	rows := l.alex.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	for _, raw := range rows["items"].([]any) {
		one := raw.(map[string]any)
		if one["statement_name"] == "CAFE PARIS" {
			require.Nil(t, one["amount_primary"])
			return
		}
	}
	t.Fatal("the row was not imported")
}

// uploadCSV files three rows: a card charge whose statement lands next month,
// and both legs of a payment of that card from checking.
const uploadCSV = `Date,Account,Payee,Category,Amount
2026-08-25,Rewards Card,Hardware Store,Home Improvement,-90.00
2026-08-10,Rewards Card,Card Payment,Transfers,250.00
2026-08-10,Everyday Checking,Card Payment,Transfers,-250.00
`

// setCardCycle puts a statement cycle on the seeded card, which the fixture leaves
// unset. Written through the store rather than the accounts endpoint because
// that one restamps the rows already there, and the point here is what the
// import does with the rows it writes.
func setCardCycle(l *ledger, closeDay int16, due domain.Date) {
	l.t.Helper()
	space := store.SpaceIDOf(l.id("space"))
	card, err := l.env.DB.GetAccount(l.t.Context(), space, l.id("card"))
	require.NoError(l.t, err)
	card.StatementCloseDay = &closeDay
	card.DueDate = due
	require.NoError(l.t, l.env.DB.UpdateAccount(l.t.Context(), space, &card))
}

// imported returns the uploaded rows by payee.
func imported(l *ledger) map[string]map[string]any {
	l.t.Helper()
	rows := l.alex.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	out := map[string]map[string]any{}
	for _, raw := range rows["items"].([]any) {
		one := raw.(map[string]any)
		out[one["payee"].(string)] = one
	}
	return out
}

func TestAnUploadedCardChargeIsStampedWithItsStatementEffectiveDate(t *testing.T) {
	// A card charge moves money on the day its bill is due, not the day it was
	// swiped. An import that skipped this would file a whole statement of
	// charges under the swipe month, which is the month the spending plan
	// would then get wrong.
	l := buildLedger(t)
	setCardCycle(l, 20, domain.NewDate(2026, time.September, 15))

	upload(l.alex, "statement.csv", uploadCSV, nil).requireStatus(http.StatusOK)

	rows := imported(l)
	charge := rows["Hardware Store"]
	require.NotNil(t, charge, "the charge was imported")
	require.Equal(t, "2026-08-25", charge["date"])
	// Posted after the 20th, so it closes on 20 September and falls due on the
	// 15th after that.
	require.Equal(t, "2026-10-15", charge["effective_date"])
}

func TestAPaymentInsideAnUploadedFileIsPaired(t *testing.T) {
	// Both legs in one file. Unpaired, the same money counts once as income
	// and once as expense in every report.
	l := buildLedger(t)
	upload(l.alex, "statement.csv", uploadCSV, nil).requireStatus(http.StatusOK)

	rows := imported(l)
	out := rows["Card Payment"]
	require.NotNil(t, out)

	// Both legs carry the same payee, so the pair is read from the ledger
	// rather than from the two rows the map above kept.
	all := l.alex.get("/transactions?limit=200").requireStatus(http.StatusOK).json()
	var pairs []string
	for _, raw := range all["items"].([]any) {
		one := raw.(map[string]any)
		if one["payee"] == "Card Payment" && one["transfer_pair_id"] != nil {
			pairs = append(pairs, one["transfer_pair_id"].(string))
		}
	}
	require.Len(t, pairs, 2, "both legs of the payment were paired")
	require.Equal(t, pairs[0], pairs[1], "and paired with each other")
}

func TestEveryUploadedRowLandsWithARunningBalance(t *testing.T) {
	// The register's balance column is materialized. Without the import
	// settling its rows, the column would sit empty on every one of them
	// until some later edit happened to touch the account.
	l := buildLedger(t)
	upload(l.alex, "statement.csv", uploadCSV, nil).requireStatus(http.StatusOK)

	for payee, row := range imported(l) {
		require.NotNil(t, row["balance"], "%s has no running balance", payee)
	}
}
