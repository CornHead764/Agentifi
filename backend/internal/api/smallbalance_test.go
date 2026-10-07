package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Hiding accounts that hold next to nothing, end to end. calculations.md §3.
//
// An exchange holds three coin accounts — nothing, forty cents and 25.00 —
// and a card bank on the same connection holds a card owing fifty cents. The
// exchange's rule must not reach the card.

type smallBalanceFixture struct {
	exchange, cardBank        uuid.UUID
	empty, dust, funded, card uuid.UUID
}

func seedSmallBalances(t *testing.T, l *ledger) smallBalanceFixture {
	t.Helper()
	ctx := t.Context()
	space := store.SpaceIDOf(l.id("space"))
	var f smallBalanceFixture
	var err error
	f.exchange, err = db(t).EnsureInstitution(ctx, space, "Example Exchange", "")
	require.NoError(t, err)
	f.cardBank, err = db(t).EnsureInstitution(ctx, space, "Example Card Bank", "")
	require.NoError(t, err)

	add := func(name string, kind domain.AccountKind, kindType string, institution uuid.UUID, balance string) uuid.UUID {
		account := &store.Account{
			Name: name, Kind: kind, Type: kindType, Currency: "USD", IncludeInNetWorth: true,
			InstitutionID:   institution,
			ProviderBalance: domain.MustFromString(balance), HasProviderBalance: true,
		}
		require.NoError(t, db(t).CreateAccount(ctx, space, account))
		return account.ID
	}
	f.empty = add("Coin A", domain.KindInvestment, "crypto", f.exchange, "0.00")
	f.dust = add("Coin B", domain.KindInvestment, "crypto", f.exchange, "0.40")
	f.funded = add("Coin C", domain.KindInvestment, "crypto", f.exchange, "25.00")
	f.card = add("Travel Card", domain.KindCreditCard, "credit_card", f.cardBank, "-0.50")
	return f
}

func hiddenSmallBalance(t *testing.T, l *ledger, id uuid.UUID) bool {
	t.Helper()
	return listedAccountByID(t, l, id)["hidden_small_balance"].(bool)
}

func TestNothingHidesUntilARuleIsSet(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)
	for _, id := range []uuid.UUID{f.empty, f.dust, f.funded, f.card} {
		require.False(t, hiddenSmallBalance(t, l, id))
	}
	require.Nil(t, listedAccountByID(t, l, f.empty)["hide_below_balance"])
}

func TestAnInstitutionRuleHidesItsOwnSmallBalancesOnly(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)

	body := l.alex.patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": "1.00",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "1.00", body["hide_below_balance"])
	require.Equal(t, "Example Exchange", body["name"])

	require.True(t, hiddenSmallBalance(t, l, f.empty))
	require.True(t, hiddenSmallBalance(t, l, f.dust))
	require.False(t, hiddenSmallBalance(t, l, f.funded))
	require.False(t, hiddenSmallBalance(t, l, f.card), "another institution's card is not swept in")

	// Hidden is display only: the row still carries its balance.
	require.Equal(t, "0.40", listedAccountByID(t, l, f.dust)["balances"].(map[string]any)["balance"])

	var listed map[string]any
	for _, row := range l.alex.get("/institutions").requireStatus(http.StatusOK).list() {
		if row["id"] == f.exchange.String() {
			listed = row
		}
	}
	require.Equal(t, "1.00", listed["hide_below_balance"])

	l.alex.patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": nil,
	}).requireStatus(http.StatusOK)
	require.False(t, hiddenSmallBalance(t, l, f.empty))
}

func TestAnAccountSettingOverridesItsInstitution(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)
	l.alex.patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": "1.00",
	}).requireStatus(http.StatusOK)

	body := l.alex.patch("/accounts/"+f.empty.String(), map[string]any{
		"hide_below_balance": "0",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", body["hide_below_balance"])
	require.False(t, hiddenSmallBalance(t, l, f.empty), "zero always shows")

	l.alex.patch("/accounts/"+f.funded.String(), map[string]any{
		"hide_below_balance": "50.00",
	}).requireStatus(http.StatusOK)
	require.True(t, hiddenSmallBalance(t, l, f.funded))

	l.alex.patch("/accounts/"+f.empty.String(), map[string]any{
		"hide_below_balance": nil,
	}).requireStatus(http.StatusOK)
	require.True(t, hiddenSmallBalance(t, l, f.empty), "cleared, it follows the institution again")
}

func TestSmallBalanceThresholdsRefuseTheWrongWrites(t *testing.T) {
	l := buildLedger(t)
	f := seedSmallBalances(t, l)

	l.as("vera").patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": "1.00",
	}).requireStatus(http.StatusForbidden)
	l.as("bob").patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": "1.00",
	}).requireStatus(http.StatusNotFound)
	l.alex.patch("/institutions/"+f.exchange.String(), map[string]any{
		"hide_below_balance": "-1.00",
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.patch("/institutions/"+f.exchange.String(), map[string]any{}).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.patch("/accounts/"+f.empty.String(), map[string]any{
		"hide_below_balance": "-0.01",
	}).requireStatus(http.StatusUnprocessableEntity)

	require.False(t, hiddenSmallBalance(t, l, f.empty))
}
