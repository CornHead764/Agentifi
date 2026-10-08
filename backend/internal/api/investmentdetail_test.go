package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The three reads behind the Investments section: a security's own screen, the
// price series behind its chart, and the named activity on the accounts.
//
// Every figure here is checkable by hand from an invented portfolio. The
// arithmetic is written out beside each assertion, because a test that only
// says "the number the code produced" tests nothing.

// seedFundSecurity writes a security of a kind other than equity, so the
// asset-class allocation has more than one slice to draw.
func seedFundSecurity(t *testing.T, space store.SpaceID, symbol, kind, price string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO securities (id, space_id, symbol, name, kind, currency,
			last_price, prior_close, last_price_at)
		VALUES ($1, $2, $3, $4, $5, 'USD', $6, $6, now())`,
		id, space.UUID(), symbol, symbol+" Fund", kind, price)
	require.NoError(t, err)
	return id
}

// buildAllocationLedger is a portfolio worth $1,500 across two accounts and two
// asset classes:
//
//	Brokerage    AAA  10 shares @ 100.00 = 1,000.00, an equity
//	Rollover IRA FFF 100 shares @   5.00 =   500.00, a mutual fund
func buildAllocationLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	l.ids["brokerage"] = seedInvestmentAccount(t, space, "Brokerage", stringPtrOf("1000.00"))
	l.ids["ira"] = seedInvestmentAccount(t, space, "Rollover IRA", stringPtrOf("500.00"))
	l.ids["aaa"] = seedSecurity(t, space, "AAA", stringPtrOf("100.00"), stringPtrOf("90.00"))
	l.ids["fff"] = seedFundSecurity(t, space, "FFF", "mutualfund", "5.00")

	l.ids["holding_aaa"] = seedHolding(t, space, l.id("brokerage"), l.id("aaa"),
		"10", stringPtrOf("60.00"), true)
	l.ids["holding_fff"] = seedHolding(t, space, l.id("ira"), l.id("fff"),
		"100", stringPtrOf("4.00"), true)
	return l
}

func groupByLabel(t *testing.T, rows []any, label string) map[string]any {
	t.Helper()
	for _, raw := range rows {
		row := raw.(map[string]any)
		if row["label"] == label {
			return row
		}
	}
	t.Fatalf("no allocation group labelled %s", label)
	return nil
}

func TestEachHoldingCarriesItsShareOfThePortfolio(t *testing.T) {
	// $1,000 and $500 of a $1,500 portfolio: two thirds and one third.
	l := buildAllocationLedger(t)
	page := holdingsPage(l, "")

	require.Equal(t, "0.666667", holdingBySymbol(t, page, "AAA")["share"])
	require.Equal(t, "0.333333", holdingBySymbol(t, page, "FFF")["share"])
}

func TestTheAllocationIsCutByAssetClassAndByAccountAsWellAsBySecurity(t *testing.T) {
	l := buildAllocationLedger(t)
	page := holdingsPage(l, "")

	byClass := page["allocation_by_class"].([]any)
	require.Len(t, byClass, 2)
	stocks := groupByLabel(t, byClass, "Stocks")
	require.Equal(t, "1000.00", stocks["value"])
	require.Equal(t, "0.666667", stocks["share"])
	require.Equal(t, "500.00", groupByLabel(t, byClass, "Mutual funds")["value"])

	byAccount := page["allocation_by_account"].([]any)
	require.Len(t, byAccount, 2)
	require.Equal(t, "1000.00", groupByLabel(t, byAccount, "Brokerage")["value"])
	require.Equal(t, "500.00", groupByLabel(t, byAccount, "Rollover IRA")["value"])
	// Largest first, the way the card renders them.
	require.Equal(t, "Brokerage", byAccount[0].(map[string]any)["label"])
}

func TestASecuritySumsItsPositionsAcrossEveryAccountHoldingIt(t *testing.T) {
	// The same ticker in two accounts: 10 shares at 100.00 in the brokerage
	// and 5 in the IRA, so 15 shares worth 1,500.00 against a basis of
	// 10 × 60.00 + 5 × 80.00 = 1,000.00, a gain of 500.00.
	l := buildAllocationLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	seedHolding(t, space, l.id("ira"), l.id("aaa"), "5", stringPtrOf("80.00"), true)

	body := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()
	require.Equal(t, "AAA", body["security"].(map[string]any)["symbol"])
	require.Len(t, body["positions"].([]any), 2)
	require.Equal(t, "15", body["shares"])
	require.Equal(t, "1500.00", body["market_value"])
	require.Equal(t, "1000.00", body["cost_basis"])
	require.Equal(t, "500.00", body["total_gain"])
	require.Equal(t, false, body["is_cost_basis_incomplete"])
	// 15 shares moving 100.00 − 90.00.
	require.Equal(t, "150.00", body["day_change"])
}

func TestASecurityWithNoBasisInOneAccountIsIncompleteForTheWhole(t *testing.T) {
	l := buildAllocationLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	seedHolding(t, space, l.id("ira"), l.id("aaa"), "5", nil, false)

	body := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["is_cost_basis_incomplete"])
	// The known half only: 600.00 of basis, not 600.00 against a 1,500.00
	// market value.
	require.Equal(t, "600.00", body["cost_basis"])
	require.Equal(t, "400.00", body["total_gain"])
}

func TestASecurityWithNoStoredClosesHasAnEmptySeriesRatherThanAFlatLine(t *testing.T) {
	l := buildAllocationLedger(t)
	body := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()

	require.Empty(t, body["prices"])
	require.Nil(t, body["price_change"])
	require.Nil(t, body["price_change_pct"])
}

func TestSomebodyOutsideTheHouseholdReachesNeitherScreen(t *testing.T) {
	// Both reads resolve their space the way every other route does, so a
	// user with no membership is refused before either query runs.
	l := buildAllocationLedger(t)
	l.as("bob").get("/securities/" + l.str("aaa")).requireStatus(http.StatusNotFound)
	l.as("bob").get("/investment-activity").requireStatus(http.StatusNotFound)
}

// --- Price history -----------------------------------------------------------

func TestBackfillingHistoryStoresWhatTheSourceAnsweredAndTheChartReadsIt(t *testing.T) {
	l := buildAllocationLedger(t)
	source := &fakePrices{history: map[string][]domain.PricePoint{
		"AAA": {
			{On: domain.NewDate(2026, time.March, 2), Close: decimal.RequireFromString("80.00")},
			{On: domain.NewDate(2026, time.March, 3), Close: decimal.RequireFromString("92.00")},
			{On: domain.NewDate(2026, time.March, 4), Close: decimal.RequireFromString("100.00")},
		},
	}}
	client := pricesClient(l, source)

	body := client.post(
		"/securities/"+l.str("aaa")+"/history?from=2026-03-01&to=2026-03-31", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(3), body["stored"])

	detail := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()
	require.Len(t, detail["prices"].([]any), 3)
	// 80.00 to 100.00 is +20.00, which is a quarter of the open.
	require.Equal(t, "20", detail["price_change"])
	require.Equal(t, "0.25", detail["price_change_pct"])
}

func TestBackfillingTwiceIsOneSeriesNotTwo(t *testing.T) {
	l := buildAllocationLedger(t)
	source := &fakePrices{history: map[string][]domain.PricePoint{
		"AAA": {
			{On: domain.NewDate(2026, time.March, 2), Close: decimal.RequireFromString("80.00")},
			{On: domain.NewDate(2026, time.March, 3), Close: decimal.RequireFromString("92.00")},
		},
	}}
	client := pricesClient(l, source)
	path := "/securities/" + l.str("aaa") + "/history?from=2026-03-01&to=2026-03-31"
	client.post(path, nil).requireStatus(http.StatusOK)
	client.post(path, nil).requireStatus(http.StatusOK)

	detail := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()
	require.Len(t, detail["prices"].([]any), 2)
}

func TestASymbolTheSourceCannotPriceStoresNothingRatherThanFailing(t *testing.T) {
	// An employer plan's own fund has no public history and never will.
	l := buildAllocationLedger(t)
	client := pricesClient(l, &fakePrices{})

	body := client.post("/securities/"+l.str("fff")+"/history", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), body["stored"])
}

func TestARefreshKeepsTheQuoteItStoredAsThatDaysClose(t *testing.T) {
	l := buildAllocationLedger(t)
	source := &fakePrices{sheet: map[string]domain.Rate{
		"AAA": decimal.RequireFromString("123.45"),
	}}
	pricesClient(l, source).post("/securities/refresh", nil).requireStatus(http.StatusOK)

	detail := l.alex.get("/securities/" + l.str("aaa")).requireStatus(http.StatusOK).json()
	points := detail["prices"].([]any)
	require.Len(t, points, 1)
	require.Equal(t, "123.45", points[0].(map[string]any)["close"])
}

func TestAWindowNarrowsThePriceSeriesTheDetailReturns(t *testing.T) {
	l := buildAllocationLedger(t)
	source := &fakePrices{history: map[string][]domain.PricePoint{
		"AAA": {
			{On: domain.NewDate(2026, time.March, 2), Close: decimal.RequireFromString("80.00")},
			{On: domain.NewDate(2026, time.April, 2), Close: decimal.RequireFromString("92.00")},
		},
	}}
	pricesClient(l, source).post(
		"/securities/"+l.str("aaa")+"/history?from=2026-03-01&to=2026-04-30", nil).
		requireStatus(http.StatusOK)

	detail := l.alex.get("/securities/" + l.str("aaa") + "?from=2026-04-01&to=2026-04-30").
		requireStatus(http.StatusOK).json()
	require.Len(t, detail["prices"].([]any), 1)
}

// --- Named activity ----------------------------------------------------------

// buildActivityLedger puts a year of brokerage rows on one investment account,
// in the wordings a statement uses. The grocery rows buildLedger already wrote
// sit on a checking account and must not appear on this tab.
func buildActivityLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := seedInvestmentAccount(t, space, "Brokerage", stringPtrOf("10000.00"))
	l.ids["brokerage"] = account

	rows := []struct {
		statement string
		amount    string
		category  uuid.UUID
	}{
		{"YOU BOUGHT ACME 10 @ 100", "-1000.00", uuid.Nil},
		{"DIVIDEND RECEIVED ACME", "31.00", l.id("groceries")},
		{"INTEREST EARNED", "0.50", uuid.Nil},
		{"MANAGEMENT FEE Q1", "-14.00", uuid.Nil},
		{"WIRE TRANSFER 8841", "-500.00", uuid.Nil},
	}
	for _, row := range rows {
		txn := &store.Transaction{
			AccountID: account, Date: domain.NewDate(2026, time.March, 4),
			Amount: domain.MustFromString(row.amount), Currency: "USD",
			StatementName: row.statement, Payee: "Brokerage",
			CategoryID: row.category, Source: domain.SourceSync,
		}
		require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))
	}
	return l
}

func activityKinds(t *testing.T, body map[string]any) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, raw := range body["items"].([]any) {
		row := raw.(map[string]any)
		out[row["statement_name"].(string)] = row["kind"].(string)
	}
	return out
}

func TestTheActivityTabNamesEachRowFromTheStatement(t *testing.T) {
	l := buildActivityLedger(t)
	body := l.alex.get("/investment-activity?from=2026-01-01&to=2026-12-31").
		requireStatus(http.StatusOK).json()

	kinds := activityKinds(t, body)
	require.Equal(t, "buy", kinds["YOU BOUGHT ACME 10 @ 100"])
	require.Equal(t, "dividend", kinds["DIVIDEND RECEIVED ACME"],
		"a dividend filed under Groceries is still a dividend")
	require.Equal(t, "interest", kinds["INTEREST EARNED"])
	require.Equal(t, "fee", kinds["MANAGEMENT FEE Q1"])
	require.Equal(t, "unknown", kinds["WIRE TRANSFER 8841"],
		"money out of a brokerage is four different things; naming one invents a fact")
}

func TestTheActivityTabTotalsIncomeAndFees(t *testing.T) {
	l := buildActivityLedger(t)
	body := l.alex.get("/investment-activity?from=2026-01-01&to=2026-12-31").
		requireStatus(http.StatusOK).json()

	// 31.00 of dividends and 0.50 of interest; 14.00 of fees, reported as a
	// magnitude rather than at the ledger's sign.
	require.Equal(t, "31.50", body["income"])
	require.Equal(t, "14.00", body["fees"])
}

func TestTheActivityTabCoversInvestmentAccountsOnly(t *testing.T) {
	// buildLedger's groceries, transfers and coffee are on a checking account
	// and a card. None of them is investment activity.
	l := buildActivityLedger(t)
	body := l.alex.get("/investment-activity?from=2026-01-01&to=2026-12-31").
		requireStatus(http.StatusOK).json()

	require.Len(t, body["items"].([]any), 5)
	for _, raw := range body["items"].([]any) {
		require.Equal(t, l.str("brokerage"), raw.(map[string]any)["account_id"])
	}
}

func TestASpaceWithNoInvestmentAccountsSeesNoActivityRatherThanTheWholeLedger(t *testing.T) {
	// The trap: an empty account list read as "every account" would fill this
	// tab with groceries.
	l := buildLedger(t)
	body := l.alex.get("/investment-activity?from=2026-01-01&to=2026-12-31").
		requireStatus(http.StatusOK).json()
	require.Empty(t, body["items"])
	require.Empty(t, body["account_ids"])
}
