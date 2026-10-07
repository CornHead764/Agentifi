package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Foreign amounts on the write path.
//
// The failure these guard against is silent and total: a row whose
// amount_primary is NULL falls back to its native figure in Posting.Amount(),
// so a €40 charge is summed as 40 of the household's own currency in every
// report, envelope, watchlist and net-worth figure, with nothing on screen
// saying it happened.

func TestAHandEnteredForeignChargeIsConvertedOnTheWayIn(t *testing.T) {
	l := buildLedger(t)
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	seedRate(l, "EUR", "0.9", on)

	body := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": on.String(),
		"amount": "-40.00", "currency": "EUR", "payee": "Paris Cafe",
	}).requireStatus(http.StatusCreated).json()

	// 40 EUR at 0.9 EUR per USD is 44.44 USD, rounded once.
	require.Equal(t, "-44.44", body["amount_primary"])
	require.Equal(t, "1.1111111111", body["fx_rate_used"])
}

// No rate is not a licence to invent one. An unconverted row is visibly
// unconverted and the daily pass stamps it when a rate arrives; a figure
// converted at a guess is indistinguishable from a real one once stored.
func TestAForeignChargeWithNoRateIsStoredUnconverted(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12",
		"amount": "-40.00", "currency": "EUR", "payee": "Paris Cafe",
	}).requireStatus(http.StatusCreated).json()

	require.Nil(t, body["amount_primary"])
	require.Nil(t, body["fx_rate_used"])
}

// A row in the space's own currency is a genuine 1:1 — the one 1:1 that is a
// fact rather than a fallback — and needs no rate to be stored.
func TestADomesticChargeIsNotGivenAConversion(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-12",
		"amount": "-40.00", "currency": "USD", "payee": "Corner Store",
	}).requireStatus(http.StatusCreated).json()
	require.Nil(t, body["amount_primary"])
}

// Editing the amount must re-derive the conversion, not clear it. On an
// install that syncs no bank and imports no file, nothing would ever restore
// it.
func TestEditingAForeignAmountReDerivesItsConversion(t *testing.T) {
	l := buildLedger(t)
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	seedRate(l, "EUR", "0.9", on)

	created := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": on.String(),
		"amount": "-40.00", "currency": "EUR", "payee": "Paris Cafe",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "-44.44", created["amount_primary"])

	updated := l.alex.patch("/transactions/"+created["id"].(string),
		map[string]any{"amount": "-80.00"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "-88.89", updated["amount_primary"])
	require.Equal(t, "1.1111111111", updated["fx_rate_used"])
}

// Changing the currency re-derives against the new one rather than keeping the
// figure computed for the old.
func TestChangingTheCurrencyReConvertsTheAmount(t *testing.T) {
	l := buildLedger(t)
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	seedRate(l, "EUR", "0.9", on)
	seedRate(l, "GBP", "0.8", on)

	created := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": on.String(),
		"amount": "-40.00", "currency": "EUR", "payee": "Paris Cafe",
	}).requireStatus(http.StatusCreated).json()

	updated := l.alex.patch("/transactions/"+created["id"].(string),
		map[string]any{"currency": "GBP"}).requireStatus(http.StatusOK).json()
	// 40 GBP at 0.8 GBP per USD is 50.00 USD.
	require.Equal(t, "-50.00", updated["amount_primary"])
}

// --- Net worth across currencies ---------------------------------------------

// seedForeignAccount is an account held in a currency that is not the space's.
func seedForeignAccount(l *ledger, name, currency, balance string) string {
	l.t.Helper()
	body := l.alex.post("/accounts", map[string]any{
		"name": name, "kind": string(domain.KindCash), "type": "savings",
		"currency": currency, "opening_balance": balance,
	}).requireStatus(http.StatusCreated).json()
	return body["id"].(string)
}

// A euro account and a dollar account summed unconverted is a total that means
// nothing, presented as one number with one currency symbol.
func TestAForeignAccountIsConvertedIntoNetWorth(t *testing.T) {
	l := buildLedger(t)
	// One rate per day the series samples; the converter reads the window's
	// end, so a rate on that day is what it needs.
	for day := 1; day <= 31; day++ {
		seedRate(l, "EUR", "0.5", domain.Date{Year: 2026, Month: 12, Day: day})
	}
	seedForeignAccount(l, "Euro Savings", "EUR", "1000.00")

	body := netWorth(l, "start=2026-01-01&end=2026-12-31")
	assets := body["end"].(map[string]any)["assets"].(string)
	// 1,000 EUR at 0.5 EUR per USD is 2,000 USD, on top of the fixture's
	// 500.00 opening checking balance less its 375.00 of spend.
	require.Contains(t, assets, "2")
	require.Empty(t, body["unconverted_currencies"])

	// The same figure unconverted would be 1,000, so the total has to have
	// moved by the conversion.
	require.NotEqual(t, "1125.00", assets)
}

// A currency the deployment has no rate for is counted at face value — losing
// real money out of a total would be worse — but the response says so, so the
// screen can stop presenting the number as exact.
func TestACurrencyWithNoRateIsNamedRatherThanSilentlyMisAdded(t *testing.T) {
	l := buildLedger(t)
	seedForeignAccount(l, "Yen Savings", "JPY", "1000.00")

	body := netWorth(l, "start=2026-01-01&end=2026-12-31")
	require.Equal(t, []any{"JPY"}, body["unconverted_currencies"])
}

// A household whose accounts are all in the space's own currency pays nothing
// for any of this and is told there is nothing to admit.
func TestASingleCurrencyHouseholdReportsNothingUnconverted(t *testing.T) {
	l := buildLedger(t)
	body := netWorth(l, "start=2026-01-01&end=2026-12-31")
	require.Empty(t, body["unconverted_currencies"])
}
