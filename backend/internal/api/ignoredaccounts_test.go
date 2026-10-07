package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Ignoring an account, end to end. calculations.md §3.
//
// The seeded ledger's card owes 300.00, holds September's 75.00 coffee charge
// (effective date) and the far leg of August's 200.00 payment from checking.
// Ignoring it must take it out of the account list, net worth and the
// spending figures, leave the checking leg a transfer rather than spending,
// and put everything back when it is un-ignored.

const septemberSpending = "from=2026-09-01&to=2026-09-30&date_field=effective&direction=spending"

func listedAccountIDs(l *ledger) map[string]bool {
	l.t.Helper()
	out := map[string]bool{}
	for _, row := range l.alex.get("/accounts").requireStatus(http.StatusOK).list() {
		out[row["id"].(string)] = true
	}
	return out
}

func ignoreByID(l *ledger, ids ...uuid.UUID) []any {
	l.t.Helper()
	body := l.alex.post("/ignored-accounts", map[string]any{"account_ids": ids}).
		requireStatus(http.StatusOK).json()
	return body["account_ids"].([]any)
}

func TestAnIgnoredAccountLeavesTheListsAndEveryFigure(t *testing.T) {
	l := buildLedger(t)
	card := l.id("card")
	require.Equal(t, "-75.00", aggregateOf(l, septemberSpending)["total"])

	require.Equal(t, []any{card.String()}, ignoreByID(l, card))

	require.False(t, listedAccountIDs(l)[card.String()], "out of the account list")
	require.True(t, listedAccountIDs(l)[l.str("checking")])

	ignored := l.alex.get("/ignored-accounts").requireStatus(http.StatusOK).list()
	require.Len(t, ignored, 1)
	require.Equal(t, card.String(), ignored[0]["id"])
	require.Equal(t, "Rewards Card", ignored[0]["name"])
	require.Equal(t, "-300.00", ignored[0]["balance"], "the list says what is being left out")
	require.NotEmpty(t, ignored[0]["ignored_at"])

	// Net worth: only checking's 125.00 is left, and no card group at all.
	body := netWorth(l, "from=2026-08-01&to=2026-08-31")
	require.Equal(t, "125.00", body["end"].(map[string]any)["net"])
	for _, raw := range body["groups"].([]any) {
		require.NotEqual(t, string(domain.KindCreditCard), raw.(map[string]any)["kind"])
	}
	require.EqualValues(t, 1, body["total_accounts"])

	// Spending: the card's September charge is gone. August's payment from
	// checking is still the near leg of a transfer, so August is unchanged.
	emptied := aggregateOf(l, septemberSpending)
	require.Equal(t, "0.00", emptied["total"])
	require.EqualValues(t, 0, emptied["count"])
	require.Equal(t, "-75.00", aggregateOf(l, august+"&date_field=effective&direction=spending")["total"])

	// The register leaves its rows out, except in the account's own register.
	for _, raw := range registerPage(l, "from=2026-01-01&to=2026-12-31")["items"].([]any) {
		require.NotEqual(t, card.String(), raw.(map[string]any)["account_id"])
	}
	own := registerPage(l, "from=2026-01-01&to=2026-12-31&account_id="+card.String())["items"].([]any)
	require.Len(t, own, 2)

	// Un-ignored, everything is back as it was.
	l.alex.del("/ignored-accounts/" + card.String()).requireStatus(http.StatusNoContent)
	require.True(t, listedAccountIDs(l)[card.String()])
	require.Empty(t, l.alex.get("/ignored-accounts").requireStatus(http.StatusOK).list())
	require.Equal(t, "-75.00", aggregateOf(l, septemberSpending)["total"])
	require.Equal(t, "-175.00",
		netWorth(l, "from=2026-08-01&to=2026-08-31")["end"].(map[string]any)["net"])
}

func TestIgnoringLeavesTheExclusionFlagsAsTheyWere(t *testing.T) {
	l := buildLedger(t)
	card := l.id("card")
	l.alex.patch("/accounts/"+card.String(), map[string]any{
		"excluded_from_spending_plan": true,
	}).requireStatus(http.StatusOK)

	ignoreByID(l, card)
	l.alex.del("/ignored-accounts/" + card.String()).requireStatus(http.StatusNoContent)

	row := listedAccountByID(t, l, card)
	require.Equal(t, true, row["excluded_from_spending_plan"])
	require.Equal(t, false, row["excluded_from_reports"])
	require.Equal(t, true, row["include_in_net_worth"])
}

func TestIgnoringEmptyAccountsTakesOnlyThatInstitutionsZeroBalances(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)
	space := store.SpaceIDOf(l.id("space"))
	emptyCard := &store.Account{
		Name: "Spare Card", Kind: domain.KindCreditCard, Type: "credit_card", Currency: "USD",
		IncludeInNetWorth: true, InstitutionID: f.cardBank,
		ProviderBalance: domain.MustFromString("0.00"), HasProviderBalance: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, emptyCard))

	body := l.alex.post("/ignored-accounts/empty", map[string]any{
		"institution_id": f.exchange,
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, []any{f.empty.String()}, body["account_ids"],
		"forty cents is not nothing, and the card bank's empty card is not the exchange's")

	listed := listedAccountIDs(l)
	require.False(t, listed[f.empty.String()])
	for _, kept := range []uuid.UUID{f.dust, f.funded, f.card, emptyCard.ID} {
		require.True(t, listed[kept.String()])
	}

	again := l.alex.post("/ignored-accounts/empty", map[string]any{
		"institution_id": f.exchange,
	}).requireStatus(http.StatusOK).json()
	require.Empty(t, again["account_ids"], "an account already ignored is not ignored twice")
}

func TestIgnoringRefusesTheWrongWrites(t *testing.T) {
	l := buildLedger(t)
	card := l.id("card")

	l.as("vera").post("/ignored-accounts", map[string]any{"account_ids": []uuid.UUID{card}}).
		requireStatus(http.StatusForbidden)
	l.alex.post("/ignored-accounts", map[string]any{"account_ids": []uuid.UUID{}}).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/ignored-accounts/empty", map[string]any{"institution_id": uuid.New()}).
		requireStatus(http.StatusNotFound)

	// Another household's account is not ignored and not reported as ignored.
	require.Empty(t, ignoreByID(l, l.id("stranger_account")))
	theirs, err := db(t).GetAccount(t.Context(), store.SpaceIDOf(l.id("other_space")), l.id("stranger_account"))
	require.NoError(t, err)
	require.False(t, theirs.IsIgnored())

	ignoreByID(l, card)
	l.as("bob").del("/ignored-accounts/" + card.String()).requireStatus(http.StatusNotFound)
	require.False(t, listedAccountIDs(l)[card.String()])

	l.alex.del("/ignored-accounts/" + card.String()).requireStatus(http.StatusNoContent)
	l.alex.del("/ignored-accounts/" + card.String()).requireStatus(http.StatusNotFound)
}

func TestARowInAnIgnoredAccountIsOfferedToNoOrder(t *testing.T) {
	// The same two rows TestABankRowIsMatchedToTheOrderThatChargedIt matches,
	// on a card the household has ignored.
	l := buildLedger(t)
	account := merchantAccount(l, "Casey")
	whole := merchantRow(l, "2026-08-03", "-40.00")
	merchantRow(l, "2026-08-11", "-16.00")
	ignoreByID(l, l.id("card"))

	report := uploadTo(l.alex, "/merchants/amazon/imports", "orders.csv", merchantCSV,
		map[string]string{"merchant_account_id": account}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), report["matched"])
	l.alex.get("/merchants/transactions/" + whole).requireStatus(http.StatusNotFound)

	summary := l.alex.get("/merchants/amazon/summary").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), summary["merchant_transactions"])

	orders := l.alex.get("/merchants/amazon/orders").requireStatus(http.StatusOK).json()["orders"].([]any)
	for _, raw := range orders {
		id := raw.(map[string]any)["id"].(string)
		require.Empty(t, l.alex.get("/merchants/amazon/orders/" + id + "/candidates?q=amazon").
			requireStatus(http.StatusOK).json()["candidates"])
	}
}
