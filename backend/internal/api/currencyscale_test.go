package api

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Amounts at their currency's ISO 4217 scale (calculations.md §1): three
// places for a dinar, none for a yen, each refused rather than rounded when
// it carries more, and converted before it meets another currency's figures.

// rateDay is the one day the rates below are stored on; every lookup in these
// tests falls back to it as the closest stored day.
var rateDay = domain.Date{Year: 2026, Month: 6, Day: 1}

func accountBalance(l *ledger, id string) string {
	l.t.Helper()
	for _, row := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		if row["id"] == id {
			return row["balances"].(map[string]any)["balance"].(string)
		}
	}
	l.t.Fatalf("account %s is not listed", id)
	return ""
}

func TestADinarAmountRoundTripsAtThreePlaces(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	account := seedForeignAccount(l, "Dinar Savings", "KWD", "100.125")

	created := l.alex.post("/transactions", map[string]any{
		"account_id": account, "date": "2026-08-12",
		"amount": "-1.234", "currency": "KWD", "payee": "Corner Store",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "-1.234", created["amount"])

	read := l.alex.get("/transactions/" + created["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, "-1.234", read["amount"])
	// 100.125 less 1.234.
	require.Equal(t, "98.891", accountBalance(l, account))
}

func TestAYenAmountRoundTripsWithNoDecimals(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	account := seedForeignAccount(l, "Yen Wallet", "JPY", "1500")

	created := l.alex.post("/transactions", map[string]any{
		"account_id": account, "date": "2026-08-12",
		"amount": "-250", "currency": "JPY", "payee": "Corner Store",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "-250", created["amount"])
	require.Equal(t, "1250", accountBalance(l, account))
}

// A fourth dinar decimal, or a fraction of a yen, is not an amount in that
// currency; rounding it would store a figure nobody entered.
func TestAnAmountFinerThanItsCurrencyIsRefused(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	dinars := seedForeignAccount(l, "Dinar Savings", "KWD", "100.000")
	yen := seedForeignAccount(l, "Yen Wallet", "JPY", "1500")

	l.alex.post("/transactions", map[string]any{
		"account_id": dinars, "date": "2026-08-12",
		"amount": "-1.2345", "currency": "KWD", "payee": "Corner Store",
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/transactions", map[string]any{
		"account_id": yen, "date": "2026-08-12",
		"amount": "-0.50", "currency": "JPY", "payee": "Corner Store",
	}).requireStatus(http.StatusUnprocessableEntity)
}

// Zeros past a currency's places are the same amount: "1.000" is a dollar,
// "1500.00" is 1500 yen.
func TestTrailingZerosAreNotTooManyDecimals(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	yen := seedForeignAccount(l, "Yen Wallet", "JPY", "1500.00")
	for _, tc := range []struct{ account, currency, amount, want string }{
		{l.str("checking"), "USD", "-1.000", "-1.00"},
		{yen, "JPY", "-1500.00", "-1500"},
	} {
		created := l.alex.post("/transactions", map[string]any{
			"account_id": tc.account, "date": "2026-08-12",
			"amount": tc.amount, "currency": tc.currency, "payee": "Corner Store",
		}).requireStatus(http.StatusCreated).json()
		require.Equal(t, tc.want, created["amount"])
	}
	require.Equal(t, "0", accountBalance(l, yen))
}

// The client holds money exactly to about ±900.7 billion; an amount past
// ±900 billion is refused where too many decimals are, before it is stored.
func TestAnAmountBeyondTheClientsRangeIsRefused(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12",
		"amount": "-900000000000.01", "currency": "USD", "payee": "Corner Store",
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12",
		"amount": "-900000000000.00", "currency": "USD", "payee": "Corner Store",
	}).requireStatus(http.StatusCreated)
}

// Two currencies' scales combined without a conversion is a refusal the
// handler never meant to make: a 409 that says so, not a bare 500.
func TestMixedCurrenciesInAHandlerAreAConflictNotAServerError(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	route := Route{kind: kindPublic, public: func(*Env, http.ResponseWriter, *http.Request) error {
		domain.FromMinor(1234, 3).Add(domain.FromMinor(1234, 2))
		return nil
	}}
	recorder := httptest.NewRecorder()
	l.env.adapt(route).ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/", nil))
	require.Equal(t, http.StatusConflict, recorder.Code)
	require.Contains(t, recorder.Body.String(), "mixed_currencies")
	require.Contains(t, recorder.Body.String(), "without an exchange rate")
}

// An account's figures are stored in its currency's minor units, so moving it
// to a currency with another number of decimals would misread every one; one
// with the same number keeps them.
func TestAnAccountCannotChangeToACurrencyAtAnotherScale(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	account := seedForeignAccount(l, "Travel", "USD", "100.00")

	l.alex.patch("/accounts/"+account, map[string]any{"currency": "JPY"}).
		requireStatus(http.StatusConflict)
	l.alex.patch("/accounts/"+account, map[string]any{"currency": "EUR"}).
		requireStatus(http.StatusOK)
	require.Equal(t, "100.00", accountBalance(l, account))
}

// A transaction moved to a currency with another number of decimals must
// bring its amount in that currency; its stored figure is in the old one's
// units.
func TestATransactionsCurrencyChangeAcrossScalesNeedsItsAmount(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	created := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12",
		"amount": "-12.50", "currency": "USD", "payee": "Corner Store",
	}).requireStatus(http.StatusCreated).json()
	path := "/transactions/" + created["id"].(string)

	l.alex.patch(path, map[string]any{"currency": "JPY"}).requireStatus(http.StatusConflict)
	moved := l.alex.patch(path, map[string]any{"currency": "JPY", "amount": "-1900"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-1900", moved["amount"])
}

// A dinar and a yen balance are converted into dollars before they are added
// to the dollar accounts: 100.125 KWD at 0.25 KWD per USD is 400.50 USD, and
// 1500 JPY at 150 JPY per USD is 10.00 USD.
func TestNetWorthAddsMixedScaleAccountsAfterConvertingThem(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	seedRate(l, "KWD", "0.25", rateDay)
	seedRate(l, "JPY", "150", rateDay)
	assets := func() domain.Money {
		body := netWorth(l, "start=2026-01-01&end=2026-12-31")
		require.Empty(t, body["unconverted_currencies"])
		return domain.MustFromString(body["end"].(map[string]any)["assets"].(string))
	}

	before := assets()
	seedForeignAccount(l, "Dinar Savings", "KWD", "100.125")
	seedForeignAccount(l, "Yen Wallet", "JPY", "1500")
	require.Equal(t, "410.50", assets().Sub(before).String())
}

// The combined cash-flow line is in the household's currency; each account's
// line stays in its own.
func TestTheCombinedCashFlowLineConvertsEachAccount(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	seedRate(l, "KWD", "0.25", rateDay)
	account := seedForeignAccount(l, "Dinar Savings", "KWD", "100.125")

	today := domain.DateOf(l.env.now())
	window := "from=" + today.String() + "&to=" + today.AddDays(1).String()
	body := l.alex.get("/cash-flow?" + window).requireStatus(http.StatusOK).json()
	require.Empty(t, body["unconverted_currencies"])
	var dinars, total domain.Money
	for _, raw := range body["accounts"].([]any) {
		line := raw.(map[string]any)
		opening := domain.MustFromString(line["starting_balance"].(string))
		if line["account_id"] == account {
			require.Equal(t, "100.125", line["starting_balance"])
			dinars = opening
			continue
		}
		total = total.Add(opening)
	}
	require.False(t, dinars.IsZero(), "the dinar account has a line")
	combined := body["combined"].([]any)[0].(map[string]any)["balance"].(string)
	require.Equal(t, total.Add(domain.MustFromString("400.50")).Round().String(), combined)
}

// The Bills & Income summary adds expected occurrences across series, so a
// dinar bill and a yen bill are converted first: 1.250 KWD at 0.25 is 5.00 USD
// and 1500 JPY at 150 is 10.00 USD. Each occurrence keeps its own figure.
func TestTheOccurrenceSummaryConvertsSeriesInOtherCurrencies(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	seedRate(l, "KWD", "0.25", rateDay)
	seedRate(l, "JPY", "150", rateDay)
	window := "from=2026-11-01&to=2026-11-30"
	expenses := func() domain.Money {
		summary := occurrencesIn(l.alex, window)["summary"].(map[string]any)
		return domain.MustFromString(summary["expenses"].(string))
	}

	before := expenses()
	for _, bill := range []struct{ name, currency, amount string }{
		{"Dinar Gym", "KWD", "-1.250"},
		{"Yen Phone", "JPY", "-1500"},
	} {
		account := seedForeignAccount(l, bill.name+" account", bill.currency, "0")
		created := newSeries(l.alex, l, map[string]any{
			"account_id": account, "kind": string(domain.SeriesBill),
			"description": bill.name, "amount": bill.amount, "currency": bill.currency,
			"start_on":   "2026-11-15",
			"recurrence": map[string]any{"frequency": "MONTHLY", "by_month_day": []int{15}},
		})
		require.Equal(t, bill.amount, created["amount"])
	}
	require.Equal(t, "15.00", expenses().Sub(before).Abs().String())

	amounts := map[string]bool{}
	for _, raw := range occurrencesIn(l.alex, window)["items"].([]any) {
		amounts[raw.(map[string]any)["amount"].(string)] = true
	}
	require.True(t, amounts["-1.250"], "the dinar occurrence keeps its three places")
	require.True(t, amounts["-1500"], "the yen occurrence has no decimals")
}
