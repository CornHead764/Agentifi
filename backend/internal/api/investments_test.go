package api

import (
	"context"
	"errors"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Investments, end to end. calculations.md §10.
//
// The two facts this file is about are the ones a number cannot carry: an
// unknown cost basis and an unknown prior close. Both have to reach the client
// as null beside a flag, because `market_value - 0` is a gain equal to the
// whole position and no client can tell that from a real one.
//
// The holdings and securities tables have no store module, so the fixture
// writes them with SQL the way investments.go reads them.

// seedSecurity writes one instrument. A nil price means no quote has arrived;
// a nil prior close means the day change is unknown rather than zero.
func seedSecurity(t *testing.T, space store.SpaceID, symbol string, price, priorClose *string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO securities (id, space_id, symbol, name, kind, currency,
			last_price, prior_close, last_price_at)
		VALUES ($1, $2, $3, $4, 'equity', 'USD', $5, $6, now())`,
		id, space.UUID(), symbol, symbol+" Inc", price, priorClose)
	require.NoError(t, err)
	return id
}

// seedHolding writes one position. averageCost nil with complete=false is the
// import that never carried its lots.
func seedHolding(
	t *testing.T, space store.SpaceID, accountID, securityID uuid.UUID,
	shares string, averageCost *string, complete bool,
) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(), `
		INSERT INTO holdings (id, space_id, account_id, security_id, shares,
			average_cost, is_cost_basis_complete)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		id, space.UUID(), accountID, securityID, shares, averageCost, complete)
	require.NoError(t, err)
	return id
}

func seedInvestmentAccount(t *testing.T, space store.SpaceID, name string, providerBalance *string) uuid.UUID {
	t.Helper()
	account := &store.Account{
		Name: name, Kind: domain.KindInvestment, Type: "brokerage",
		Currency: "USD", IncludeInNetWorth: true,
		// Held since January: the fixture is created today with no rows, and
		// the tests read it over windows months back.
		HistoryStartsOn: domain.NewDate(2026, time.January, 1),
	}
	if providerBalance != nil {
		account.ProviderBalance = domain.MustFromString(*providerBalance)
		account.HasProviderBalance = true
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	return account.ID
}

func stringPtrOf(s string) *string { return &s }

// buildPortfolioLedger seeds a portfolio whose arithmetic is checkable by hand.
//
//	AAA  10 shares @ 100.00, basis 60.00/share, prior close 90.00
//	     market 1,000.00  basis 600.00  gain 400.00  day change 100.00
//	BBB  20 shares @  50.00, no lots at all, no prior close
//	     market 1,000.00  basis unknown  day change unknown
//
// Both sit in "Brokerage", whose provider balance is 10,000.00. "Rollover IRA"
// holds 4,000.00 and no positions, which is the account a holdings-only total
// would silently drop.
func buildPortfolioLedger(t *testing.T) *ledger {
	t.Helper()
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	l.ids["brokerage"] = seedInvestmentAccount(t, space, "Brokerage", stringPtrOf("10000.00"))
	l.ids["ira"] = seedInvestmentAccount(t, space, "Rollover IRA", stringPtrOf("4000.00"))

	l.ids["aaa"] = seedSecurity(t, space, "AAA", stringPtrOf("100.00"), stringPtrOf("90.00"))
	l.ids["bbb"] = seedSecurity(t, space, "BBB", stringPtrOf("50.00"), nil)

	l.ids["holding_aaa"] = seedHolding(t, space, l.id("brokerage"), l.id("aaa"),
		"10", stringPtrOf("60.00"), true)
	l.ids["holding_bbb"] = seedHolding(t, space, l.id("brokerage"), l.id("bbb"),
		"20", nil, false)
	return l
}

func holdingsPage(l *ledger, query string) map[string]any {
	l.t.Helper()
	path := "/holdings"
	if query != "" {
		path += "?" + query
	}
	return l.alex.get(path).requireStatus(http.StatusOK).json()
}

func holdingBySymbol(t *testing.T, page map[string]any, symbol string) map[string]any {
	t.Helper()
	for _, raw := range page["items"].([]any) {
		row := raw.(map[string]any)
		if row["symbol"] == symbol {
			return row
		}
	}
	t.Fatalf("no holding for %s", symbol)
	return nil
}

// --- Incomplete cost basis ---------------------------------------------------

func TestAHoldingWithNoLotsReportsANullBasisAndNotAGainOfEverything(t *testing.T) {
	// §10: rendering market_value − 0 as the gain is a lie — and a flattering
	// one, which is the worst kind.
	l := buildPortfolioLedger(t)
	page := holdingsPage(l, "")

	bbb := holdingBySymbol(t, page, "BBB")
	require.Equal(t, "1000.00", bbb["market_value"])
	require.Nil(t, bbb["cost_basis"])
	require.Nil(t, bbb["total_gain"])
	require.Nil(t, bbb["total_gain_pct"])
	require.Equal(t, false, bbb["is_cost_basis_complete"])

	known := holdingBySymbol(t, page, "AAA")
	require.Equal(t, "600.00", known["cost_basis"])
	require.Equal(t, "400.00", known["total_gain"])
	require.Equal(t, true, known["is_cost_basis_complete"])
}

func TestThePortfolioGainCoversOnlyTheHoldingsWithAKnownBasis(t *testing.T) {
	l := buildPortfolioLedger(t)
	totals := holdingsPage(l, "")["totals"].(map[string]any)

	require.Equal(t, "2000.00", totals["market_value"])
	require.Equal(t, "600.00", totals["cost_basis"])
	require.Equal(t, "400.00", totals["total_gain"])
	require.Equal(t, true, totals["is_cost_basis_incomplete"])

	// The two numbers a partial basis invites: the whole market value as gain,
	// and market value less the partial basis. Neither may appear.
	require.NotEqual(t, "2000.00", totals["total_gain"])
	require.NotEqual(t, "1400.00", totals["total_gain"])
}

func TestAPortfolioWithNoBasisAtAllHasNoGainRatherThanZero(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := seedInvestmentAccount(t, space, "Inherited Shares", stringPtrOf("500.00"))
	security := seedSecurity(t, space, "CCC", stringPtrOf("25.00"), stringPtrOf("25.00"))
	seedHolding(t, space, account, security, "20", nil, false)

	totals := holdingsPage(l, "")["totals"].(map[string]any)
	require.Equal(t, "500.00", totals["market_value"])
	require.Nil(t, totals["cost_basis"])
	require.Nil(t, totals["total_gain"])
	require.Equal(t, true, totals["is_cost_basis_incomplete"])
}

// --- Missing prior close -----------------------------------------------------

func TestAMissingPriorCloseLeavesTheDayChangeUnknownRatherThanFlat(t *testing.T) {
	// A position with no session on file did not fail to move. It is the same
	// null-with-a-flag shape as the cost basis.
	l := buildPortfolioLedger(t)
	page := holdingsPage(l, "")

	bbb := holdingBySymbol(t, page, "BBB")
	require.Nil(t, bbb["day_change"])
	require.Nil(t, bbb["day_change_pct"])

	aaa := holdingBySymbol(t, page, "AAA")
	require.Equal(t, "100.00", aaa["day_change"])
	require.NotNil(t, aaa["day_change_pct"])

	totals := page["totals"].(map[string]any)
	require.Equal(t, "100.00", totals["day_change"])
	require.Equal(t, true, totals["is_day_change_incomplete"])
	// The denominator is yesterday's value, 2,000.00 − 100.00.
	require.Equal(t, "5.26", totals["day_change_pct"])

	securities := l.alex.get("/securities").requireStatus(http.StatusOK).list()
	require.Nil(t, findBySymbol(t, securities, "BBB")["prior_close"])
	require.NotNil(t, findBySymbol(t, securities, "AAA")["prior_close"])
}

func findBySymbol(t *testing.T, rows []map[string]any, symbol string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["symbol"] == symbol {
			return row
		}
	}
	t.Fatalf("no security %s", symbol)
	return nil
}

// --- The double-counting trap ------------------------------------------------

func TestTheTotalAddsBackTheCashInEveryInvestmentAccount(t *testing.T) {
	// §4: a holding inside a connected brokerage is already inside that
	// account's balance, so the two are never added for the same account.
	// What is left after subtracting the positions is that account's cash, and
	// it belongs in the total.
	//
	// Adding back only the accounts holding *no* positions would drop the
	// brokerage's 8,000.00 of uncommitted cash from every figure on the page —
	// while net worth, which just sums balances, still reports it. Two
	// screens, one set of accounts, an 8,000.00 gap.
	l := buildPortfolioLedger(t)
	totals := holdingsPage(l, "")["totals"].(map[string]any)

	// 10,000.00 brokerage − 2,000.00 of positions, plus the 4,000.00 IRA.
	require.Equal(t, "12000.00", totals["account_balance_not_held"])
	require.Equal(t, "14000.00", totals["total_value"])

	// The inflated figure this rule exists to prevent: the positions counted
	// once inside their account's balance and again on top of it.
	require.NotEqual(t, "16000.00", totals["total_value"])
}

func TestAnAccountWithNoPositionsStillHasAPortfolioValue(t *testing.T) {
	l := buildPortfolioLedger(t)
	totals := holdingsPage(l, "account_id="+l.str("ira"))["totals"].(map[string]any)

	require.Equal(t, "0.00", totals["market_value"])
	require.Equal(t, "4000.00", totals["account_balance_not_held"])
	require.Equal(t, "4000.00", totals["total_value"])
}

func TestAskingForOneAccountFiltersTheHoldingsAndTheTotalTogether(t *testing.T) {
	l := buildPortfolioLedger(t)
	page := holdingsPage(l, "account_id="+l.str("brokerage"))

	require.Len(t, page["items"], 2)
	totals := page["totals"].(map[string]any)
	require.Equal(t, "2000.00", totals["market_value"])
	// The brokerage's own cash: 10,000.00 balance less 2,000.00 of positions.
	require.Equal(t, "8000.00", totals["account_balance_not_held"])
	// Which is to say the account's whole balance, reached from the other side.
	require.Equal(t, "10000.00", totals["total_value"])
}

func TestAHoldingWithNoQuoteIsReportedRatherThanQuietlyDropped(t *testing.T) {
	// A missing quote is a fetch that failed. Skipping the row would shrink
	// the portfolio by an amount nobody would notice.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := seedInvestmentAccount(t, space, "Brokerage", stringPtrOf("100.00"))
	unquoted := seedSecurity(t, space, "ZZZ", nil, nil)
	seedHolding(t, space, account, unquoted, "5", stringPtrOf("10.00"), true)

	l.alex.get("/holdings").requireStatus(http.StatusConflict)
}

// --- Allocation and the wire -------------------------------------------------

func TestAllocationSharesTheMarketValueBetweenTheSecurities(t *testing.T) {
	l := buildPortfolioLedger(t)
	slices := holdingsPage(l, "")["allocation"].([]any)
	require.Len(t, slices, 2)

	total := domain.Zero
	for _, raw := range slices {
		slice := raw.(map[string]any)
		require.Equal(t, "0.5", slice["share"])
		total = total.Add(domain.MustFromString(slice["value"].(string)))
	}
	require.Equal(t, "2000.00", total.String())
}

func TestEveryInvestmentFigureCrossesTheWireAsAString(t *testing.T) {
	l := buildPortfolioLedger(t)
	page := holdingsPage(l, "")
	aaa := holdingBySymbol(t, page, "AAA")
	for _, key := range []string{"shares", "price", "market_value", "cost_basis", "total_gain"} {
		require.IsType(t, "", aaa[key], "%s is not a string", key)
	}
	require.IsType(t, "", page["totals"].(map[string]any)["total_value"])
}

// --- Tenancy -----------------------------------------------------------------

func TestAnotherSpacesPositionsAreInvisible(t *testing.T) {
	l := buildPortfolioLedger(t)
	other := store.SpaceIDOf(l.id("other_space"))
	theirAccount := seedInvestmentAccount(t, other, "Their Brokerage", stringPtrOf("999.00"))
	theirSecurity := seedSecurity(t, other, "NOPE", stringPtrOf("1.00"), stringPtrOf("1.00"))
	seedHolding(t, other, theirAccount, theirSecurity, "100", stringPtrOf("1.00"), true)

	page := holdingsPage(l, "")
	for _, raw := range page["items"].([]any) {
		require.NotEqual(t, "NOPE", raw.(map[string]any)["symbol"])
	}
	require.Equal(t, "2000.00", page["totals"].(map[string]any)["market_value"])

	for _, row := range l.alex.get("/securities").requireStatus(http.StatusOK).list() {
		require.NotEqual(t, "NOPE", row["symbol"])
	}
}

func TestAViewerReadsThePortfolio(t *testing.T) {
	// Every investments route is a read; a viewer has every right to look.
	l := buildPortfolioLedger(t)
	vera := l.as("vera")
	vera.get("/holdings").requireStatus(http.StatusOK)
	vera.get("/securities").requireStatus(http.StatusOK)
	vera.get("/performance").requireStatus(http.StatusOK)
}

// --- Performance -------------------------------------------------------------

func TestIRRReportsNoAnswerRatherThanAWrongRoot(t *testing.T) {
	// §10: a rate that quietly comes back wrong is indistinguishable from a
	// real one on a chart. A series that never changes sign has no rate of
	// return to find, and null is the honest answer.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := &store.Account{
		Name: "New Brokerage", Kind: domain.KindInvestment, Type: "brokerage",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance: domain.Zero, OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.March, 1),
		Amount: domain.MustFromString("5000.00"), Currency: "USD",
		StatementName: "DEPOSIT", Payee: "Deposit", Source: domain.SourceManual,
	}))

	body := l.alex.get("/performance?from=2026-01-01&to=2026-06-30&account_id=" +
		account.ID.String()).requireStatus(http.StatusOK).json()

	require.Equal(t, "0.00", body["start_value"])
	require.Equal(t, "5000.00", body["end_value"])
	require.Nil(t, body["irr"], "a portfolio with no sign change was given a rate")
	require.Nil(t, body["irr_pct"])
}

func TestTheReturnRatesAreComputedWhenTheySolve(t *testing.T) {
	// The other half: null has to mean "no root", not "never computed".
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := &store.Account{
		Name: "Steady Fund", Kind: domain.KindInvestment, Type: "brokerage",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance:   domain.MustFromString("1000.00"),
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.June, 30),
		Amount: domain.MustFromString("100.00"), Currency: "USD",
		StatementName: "DIVIDEND", Payee: "Dividend", Source: domain.SourceManual,
	}))

	body := l.alex.get("/performance?from=2026-01-01&to=2026-06-30&account_id=" +
		account.ID.String()).requireStatus(http.StatusOK).json()

	require.Equal(t, "1000.00", body["start_value"])
	require.Equal(t, "1100.00", body["end_value"])
	require.NotNil(t, body["irr"])
	require.NotNil(t, body["twr"])
	// Both rates are offered together; the chart toggles between them.
	require.NotNil(t, body["twr_pct"])
	require.NotNil(t, body["irr_pct"])
	require.Equal(t, "0.1", body["twr"])
}

func TestPerformanceRebasesEveryPointAgainstTheWindowStart(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := l.alex.get("/performance?from=2026-08-01&to=2026-08-31&account_id=" +
		l.str("brokerage")).requireStatus(http.StatusOK).json()

	require.Equal(t, "day", body["granularity"])
	points := body["points"].([]any)
	require.NotEmpty(t, points)
	first := points[0].(map[string]any)
	require.Equal(t, "2026-08-01", first["on"])
	require.Equal(t, "0", first["return_pct"])
	require.Equal(t, body["start_value"], first["value"])
	require.Equal(t, body["end_value"], points[len(points)-1].(map[string]any)["value"])
}

func TestPerformanceStartingFromNothingHasNoBaselineToRebaseAgainst(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := seedInvestmentAccount(t, space, "Empty Brokerage", nil)

	body := l.alex.get("/performance?from=2026-08-01&to=2026-08-31&account_id=" +
		account.String()).requireStatus(http.StatusOK).json()
	for _, raw := range body["points"].([]any) {
		require.Nil(t, raw.(map[string]any)["return_pct"])
	}
}

func TestAnEmptyAccountSelectionHoldsNothing(t *testing.T) {
	// The same rule as the register: unticking every account asks for none of
	// them, and a header showing the whole portfolio over an empty table is the
	// disagreement this exists to stop.
	l := buildPortfolioLedger(t)
	none := holdingsPage(l, "account_id=")

	require.Empty(t, none["items"])
	require.Equal(t, "0.00", none["totals"].(map[string]any)["market_value"])
	require.Equal(t, "0.00", none["totals"].(map[string]any)["account_balance_not_held"])

	all := holdingsPage(l, "")
	require.NotEmpty(t, all["items"])
}

// Adding a position by hand.
//
// Holdings arrive from the Simplifi import and nowhere else — SimpleFIN can
// parse them but the sync never persists them — so a brokerage held outside
// any connection would be invisible in the portfolio whatever the account
// says.

func TestAHoldingCanBeAddedByHandAndValuesItself(t *testing.T) {
	l := buildPortfolioLedger(t)

	created := l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "aaa", "shares": "5",
		"cost_basis": "300.00",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "AAA", created["symbol"], "the symbol is stored upper-cased")
	// The symbol already exists in this space, so no second security was made.
	require.Equal(t, l.str("aaa"), created["security_id"])

	page := holdingsPage(l, "account_id="+l.str("ira"))
	row := holdingBySymbol(t, page, "AAA")
	require.Equal(t, "500.00", row["market_value"], "5 shares at the stored 100.00 quote")
	require.Equal(t, "300.00", row["cost_basis"])
	require.Equal(t, "200.00", row["total_gain"])
}

func TestANewSymbolGetsASecurityAndReadsAsUnquoted(t *testing.T) {
	// A security carries no quote until something calls /securities/refresh,
	// so a hand-added one reads as unquoted. The em dash is the honest
	// answer; a zero would read as a worthless position rather than an
	// unpriced one.
	l := buildPortfolioLedger(t)

	// A symbol with no price on file has to say what it is worth, or the
	// portfolio has a holding it can neither price nor value.
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "zzz", "shares": "3",
	}).requireStatus(http.StatusUnprocessableEntity)

	created := l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": " zzz ", "name": "Zephyr Fund",
		"shares": "3", "market_value": "750.00",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "ZZZ", created["symbol"])
	require.NotEqual(t, l.str("aaa"), created["security_id"])

	securities := l.alex.get("/securities").requireStatus(http.StatusOK).list()
	names := map[string]string{}
	for _, one := range securities {
		names[one["symbol"].(string)] = one["name"].(string)
	}
	require.Equal(t, "Zephyr Fund", names["ZZZ"])

	row := holdingBySymbol(t, holdingsPage(l, "account_id="+l.str("ira")), "ZZZ")
	require.Nil(t, row["price"])
	require.Equal(t, true, row["is_unquoted"])
	require.Equal(t, "750.00", row["market_value"], "the figure the user gave it")
}

func TestASymbolWithNoNameIsNamedAfterItself(t *testing.T) {
	l := buildPortfolioLedger(t)
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "QQQ", "shares": "1",
		"market_value": "100.00",
	}).requireStatus(http.StatusCreated)

	for _, one := range l.alex.get("/securities").requireStatus(http.StatusOK).list() {
		if one["symbol"] == "QQQ" {
			require.Equal(t, "QQQ", one["name"])
		}
	}
}

func TestOneAccountHoldsOnePositionPerSecurity(t *testing.T) {
	// uq_holding_account_security. Merging the two silently would change a
	// share count nobody asked to change.
	l := buildPortfolioLedger(t)
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("brokerage"), "symbol": "AAA", "shares": "5",
	}).requireStatus(http.StatusConflict)
}

func TestAPositionRefusesAnAccountThatHoldsNoPositions(t *testing.T) {
	// A holding in a checking account would be counted by the portfolio and by
	// the cash balance — the double count this file opens by warning about.
	l := buildPortfolioLedger(t)
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("checking"), "symbol": "AAA", "shares": "5",
	}).requireStatus(http.StatusConflict)
}

func TestAPositionOfNoSharesIsRefused(t *testing.T) {
	l := buildPortfolioLedger(t)
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "AAA", "shares": "0",
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "  ", "shares": "1",
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestAHandAddedPositionCanBeRemoved(t *testing.T) {
	l := buildPortfolioLedger(t)
	created := l.alex.post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "AAA", "shares": "5",
	}).requireStatus(http.StatusCreated).json()

	l.alex.del("/holdings/" + created["id"].(string)).requireStatus(http.StatusNoContent)
	require.Empty(t, holdingsPage(l, "account_id="+l.str("ira"))["items"])
	l.alex.del("/holdings/" + created["id"].(string)).requireStatus(http.StatusNotFound)
}

func TestAViewerCannotAddAPosition(t *testing.T) {
	l := buildPortfolioLedger(t)
	l.as("vera").post("/holdings", map[string]any{
		"account_id": l.str("ira"), "symbol": "AAA", "shares": "5",
	}).requireStatus(http.StatusForbidden)
}

// --- Price refresh -----------------------------------------------------------

// fakePrices answers a fixed price sheet so no test reaches the network.
type fakePrices struct {
	sheet map[string]domain.Rate
	// history is the daily closes the source will hand back per symbol. A
	// symbol absent from it has no public history, which is the state an
	// employer plan's fund is permanently in.
	history map[string][]domain.PricePoint
	err     error
}

func (f *fakePrices) Name() string { return "fake" }
func (f *fakePrices) Search(context.Context, string, int) ([]provider.SymbolMatch, error) {
	return nil, nil
}
func (f *fakePrices) Quote(context.Context, string) (*provider.SymbolQuote, error) {
	return nil, nil
}
func (f *fakePrices) LatestPrices(_ context.Context, symbols []string) (map[string]domain.Rate, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := map[string]domain.Rate{}
	for _, symbol := range symbols {
		if rate, priced := f.sheet[symbol]; priced {
			out[symbol] = rate
		}
	}
	return out, nil
}

func (f *fakePrices) History(
	_ context.Context, symbol string, from, to domain.Date,
) ([]domain.PricePoint, error) {
	if f.err != nil {
		return nil, f.err
	}
	var out []domain.PricePoint
	for _, point := range f.history[symbol] {
		if point.On.Before(from) || to.Before(point.On) {
			continue
		}
		out = append(out, point)
	}
	return out, nil
}

func pricesClient(l *ledger, source provider.MarketPriceProvider) *client {
	l.t.Helper()
	env := NewEnv(testConfig(), db(l.t), WithPrices(source))
	return (&client{t: l.t, env: env, handler: RouterFor(env)}).
		as(l.users["alex"]).inSpace(store.SpaceIDOf(l.id("space")))
}

func TestRefreshingPricesStoresWhatTheSourceAnswered(t *testing.T) {
	l := buildPortfolioLedger(t)
	source := &fakePrices{sheet: map[string]domain.Rate{
		"AAA": decimal.RequireFromString("123.45"),
	}}

	body := pricesClient(l, source).post("/securities/refresh", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), body["updated"])
	// The sheet had no answer for the other symbols; they are named, and
	// their stored prices are left standing rather than zeroed.
	require.NotEmpty(t, body["unpriced"])

	page := holdingsPage(l, "account_id="+l.str("brokerage"))
	row := holdingBySymbol(l.t, page, "AAA")
	require.Equal(t, "1234.50", row["market_value"], "10 shares at the fresh 123.45 quote")
}

func TestARefusedPriceSourceIsAGatewayErrorNotAShrug(t *testing.T) {
	l := buildPortfolioLedger(t)
	source := &fakePrices{err: errors.New("rate limited")}
	pricesClient(l, source).post("/securities/refresh", nil).
		requireStatus(http.StatusBadGateway)
}

// --- What crosses the portfolio boundary -------------------------------------

// externalFlows decides which rows are deposits and withdrawals through
// domain.Posting.IsTransfer, not a hand-written inverse of the transfer rule
// that would read the category without first asking whether the row had
// one, and through domain.CountsTowardBalance for the separate question of
// whether the money moved at all.
//
// The account is 1,000.00 on 1 January and earns 100.00 on 30 June, which is a
// 10% return with nothing crossing the boundary. Every row below is checked by
// what it does to that figure: a real deposit moves it, and anything that is
// not a deposit must leave it exactly where it is.

func performanceOf(l *ledger, accountID uuid.UUID) map[string]any {
	l.t.Helper()
	return l.alex.get("/performance?from=2026-01-01&to=2026-06-30&account_id=" +
		accountID.String()).requireStatus(http.StatusOK).json()
}

func steadyFund(t *testing.T, l *ledger) *store.Account {
	t.Helper()
	space := store.SpaceIDOf(l.id("space"))
	account := &store.Account{
		Name: "Boundary Fund", Kind: domain.KindInvestment, Type: "brokerage",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalance:   domain.MustFromString("1000.00"),
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, account))
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.June, 30),
		Amount: domain.MustFromString("100.00"), Currency: "USD",
		StatementName: "DIVIDEND", Payee: "Dividend", Source: domain.SourceManual,
	}))
	return account
}

func TestBothShapesOfATransferAreDepositsIntoThePortfolio(t *testing.T) {
	// The two shapes domain.Posting.IsTransfer names: a matched leg, and a row
	// filed under a transfer category that never matched. Both are 500.00
	// arriving on 1 March, so both leave the same portfolio and must report
	// the same return.
	//
	// The number is the point. A time-weighted return divides out the money
	// the user put in, so a deposit the sieve fails to recognise is not a
	// rounding difference — it reads as growth the portfolio produced. Missing
	// the category shape turns this 6.6667% into 60%.
	const twrWithADeposit = "0.066667"
	space := func(l *ledger) store.SpaceID { return store.SpaceIDOf(l.id("space")) }

	t.Run("a matched transfer leg", func(t *testing.T) {
		l := buildLedger(t)
		account := steadyFund(t, l)
		require.NoError(t, db(t).CreateTransaction(t.Context(), space(l), &store.Transaction{
			AccountID: account.ID, Date: domain.NewDate(2026, time.March, 1),
			Amount: domain.MustFromString("500.00"), Currency: "USD",
			StatementName: "TRANSFER FROM CHECKING", Payee: "Transfer",
			TransferPairID: uuid.New(), Source: domain.SourceSync,
		}))
		body := performanceOf(l, account.ID)
		require.Equal(t, "1600.00", body["end_value"])
		require.Equal(t, twrWithADeposit, body["twr"],
			"a matched leg was not counted as money arriving")
	})

	t.Run("a row under a transfer category", func(t *testing.T) {
		l := buildLedger(t)
		account := steadyFund(t, l)
		transfer := &store.Category{
			Name: "Transfer", Kind: domain.CategoryTransfer,
			IsUserAssignable: true, IsEditable: true,
		}
		require.NoError(t, db(t).CreateCategory(t.Context(), space(l), transfer))
		require.NoError(t, db(t).CreateTransaction(t.Context(), space(l), &store.Transaction{
			AccountID: account.ID, Date: domain.NewDate(2026, time.March, 1),
			Amount: domain.MustFromString("500.00"), Currency: "USD",
			StatementName: "DEPOSIT", Payee: "Deposit",
			CategoryID: transfer.ID, Source: domain.SourceSync,
		}))
		body := performanceOf(l, account.ID)
		require.Equal(t, "1600.00", body["end_value"])
		require.Equal(t, twrWithADeposit, body["twr"],
			"an unmatched transfer-category deposit read as portfolio growth")
	})
}

func TestNothingButATransferCrossesThePortfolioBoundary(t *testing.T) {
	// Rows that look like deposits and are not. Both leave the balance alone,
	// so the boundary is the only thing being tested: if the sieve counted
	// either, the return would be divided by money that never arrived.
	//
	// Both are held out twice over — ListTransactions asks for neither by
	// default, and domain.CountsTowardBalance says so again. The second guard
	// is the one that survives a caller adding IncludeDeleted to the query for
	// some other reason, which is how the first guard stops holding.
	//
	// The internal case is already in the fixture: the 100.00 dividend is
	// earnings, not a deposit, and the 10% figure below is only 10% because
	// nothing treats it as one.
	cases := []struct {
		name string
		txn  func(accountID uuid.UUID) *store.Transaction
	}{
		{"a deleted transfer leg", func(id uuid.UUID) *store.Transaction {
			return &store.Transaction{
				AccountID: id, Date: domain.NewDate(2026, time.March, 1),
				Amount: domain.MustFromString("500.00"), Currency: "USD",
				StatementName: "TRANSFER FROM CHECKING", Payee: "Transfer",
				TransferPairID: uuid.New(), IsDeleted: true, Source: domain.SourceSync,
			}
		}},
		{"a forecast the provider wrote ahead", func(id uuid.UUID) *store.Transaction {
			return &store.Transaction{
				AccountID: id, Date: domain.NewDate(2026, time.March, 1),
				Amount: domain.MustFromString("500.00"), Currency: "USD",
				StatementName: "TRANSFER FROM CHECKING", Payee: "Transfer",
				TransferPairID: uuid.New(), EstimateStatus: store.ProjectedEstimate,
				Source: domain.SourceSync,
			}
		}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			l := buildLedger(t)
			account := steadyFund(t, l)
			require.NoError(t, db(t).CreateTransaction(t.Context(),
				store.SpaceIDOf(l.id("space")), tc.txn(account.ID)))

			body := performanceOf(l, account.ID)
			require.Equal(t, "1000.00", body["start_value"])
			require.Equal(t, "1100.00", body["end_value"])
			require.Equal(t, "0.1", body["twr"],
				"this row was treated as money crossing the boundary")
		})
	}
}
