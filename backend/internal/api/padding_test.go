package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Two invented lunches deducted from pay, each with the income row
// a padding mail rule writes beside it, in a lunch account of their own:
// $6.50 on the 12th and $8.50 on the 13th of August. The pads are $15.00 of
// income and the account nets to nothing.

type paddingSeed struct {
	card      uuid.UUID
	purchases []uuid.UUID
	pads      []uuid.UUID
}

func seedPadding(l *ledger, padAccount func(card uuid.UUID) uuid.UUID) paddingSeed {
	l.t.Helper()
	t := l.t
	ctx := t.Context()
	space := store.SpaceIDOf(l.id("space"))

	card := &store.Account{
		Name: "Lunch Card", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(ctx, space, card))
	lunch := &store.Category{Name: "Lunch", Kind: domain.CategoryExpense, IsUserAssignable: true, IsEditable: true}
	paycheck := &store.Category{Name: "Paycheck", Kind: domain.CategoryIncome, IsUserAssignable: true, IsEditable: true}
	require.NoError(t, db(t).CreateCategory(ctx, space, lunch))
	require.NoError(t, db(t).CreateCategory(ctx, space, paycheck))

	seed := paddingSeed{card: card.ID}
	for i, amount := range []string{"6.50", "8.50"} {
		day := domain.NewDate(2026, time.August, 12+i)
		external := "mail:<lunch-" + amount + "@lunch.example.invalid>"
		purchase := &store.Transaction{
			AccountID: card.ID, ExternalID: external, Date: day,
			Amount: domain.MustFromString("-" + amount), Currency: "USD",
			StatementName: "Lunch", Payee: "Lunch",
			CategoryID: lunch.ID, Source: domain.SourceEmail,
		}
		require.NoError(t, db(t).CreateTransaction(ctx, space, purchase))
		pad := &store.Transaction{
			AccountID: padAccount(card.ID), ExternalID: external + ":income", Date: day,
			Amount: domain.MustFromString(amount), Currency: "USD",
			StatementName: "Lunch (paycheck deduction)", Payee: "Lunch (paycheck deduction)",
			CategoryID: paycheck.ID, Source: domain.SourceEmail,
		}
		require.NoError(t, db(t).CreateTransaction(ctx, space, pad))
		require.NoError(t, db(t).LinkPadding(ctx, space, pad.ID, purchase.ID))
		seed.purchases = append(seed.purchases, purchase.ID)
		seed.pads = append(seed.pads, pad.ID)
	}
	return seed
}

func sameAccount(card uuid.UUID) uuid.UUID { return card }

func itemIDs(page map[string]any) map[string]map[string]any {
	out := map[string]map[string]any{}
	for _, raw := range page["items"].([]any) {
		item := raw.(map[string]any)
		out[item["id"].(string)] = item
	}
	return out
}

func TestTheRegisterHidesPaddingWhenAskedAndSaysWhatItHid(t *testing.T) {
	l := buildLedger(t)
	seed := seedPadding(l, sameAccount)
	scope := "/transactions?" + august + "&account_id=" + seed.card.String()

	hidden := l.alex.get(scope + "&padding=hide").requireStatus(http.StatusOK).json()
	items := itemIDs(hidden)
	require.Len(t, items, 2)
	for i, purchase := range seed.purchases {
		item, ok := items[purchase.String()]
		require.True(t, ok, "the purchase is listed")
		require.Equal(t, seed.pads[i].String(), item["padding_txn_id"], "and names its pad")
		require.Nil(t, item["padded_txn_id"])
	}
	require.EqualValues(t, 2, hidden["count"])
	require.Equal(t, "-15.00", hidden["total"])
	require.Equal(t, "-15.00", hidden["full_total"])
	require.EqualValues(t, 2, hidden["padding_count"])
	require.Equal(t, "15.00", hidden["padding_total"])

	shown := l.alex.get(scope + "&padding=show").requireStatus(http.StatusOK).json()
	items = itemIDs(shown)
	require.Len(t, items, 4)
	for i, pad := range seed.pads {
		require.Equal(t, seed.purchases[i].String(), items[pad.String()]["padded_txn_id"])
	}
	require.EqualValues(t, 4, shown["count"])
	require.Equal(t, "0.00", shown["total"])
	require.EqualValues(t, 0, shown["padding_count"])
	require.Equal(t, "0.00", shown["padding_total"])

	plain := l.alex.get(scope).requireStatus(http.StatusOK).json()
	require.EqualValues(t, 4, plain["count"], "hiding is asked for, never assumed")

	everything := l.alex.get("/transactions?" + august + "&padding=hide").requireStatus(http.StatusOK).json()
	for _, pad := range seed.pads {
		require.NotContains(t, itemIDs(everything), pad.String(), "All accounts hides them too")
	}
	require.EqualValues(t, 2, everything["padding_count"])

	// Every figure still counts the pads: the income chart over the same
	// query, and the account's balance.
	income := aggregateOf(l, august+"&account_id="+seed.card.String()+
		"&padding=hide&date_field=effective&direction=income")
	require.Equal(t, "15.00", income["total"])
	summary := l.alex.get("/accounts/" + seed.card.String() + "/summary?" + august).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", summary["ending_balance"])

	l.alex.get(scope + "&padding=folded").requireStatus(http.StatusUnprocessableEntity)
}

func TestMarkingAllReviewedLeavesHiddenPaddingAlone(t *testing.T) {
	l := buildLedger(t)
	seed := seedPadding(l, sameAccount)
	scope := "?" + august + "&account_id=" + seed.card.String() + "&padding=hide"

	result := l.alex.post("/transactions/mark-reviewed"+scope, map[string]any{}).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 2, result["updated"], "the rows on screen, not the ones folded away")

	space := store.SpaceIDOf(l.id("space"))
	for _, pad := range seed.pads {
		row, err := db(t).GetTransaction(t.Context(), space, pad)
		require.NoError(t, err)
		require.False(t, row.IsReviewed)
	}
}

func TestDeletingAPaddedPurchaseDeletesThePadAndRebalancesItsAccount(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	payroll := &store.Account{
		Name: "Payroll Checking", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
		OpeningBalanceOn: domain.NewDate(2026, time.January, 1),
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), space, payroll))
	wages := &store.Transaction{
		AccountID: payroll.ID, Date: domain.NewDate(2026, time.August, 1),
		Amount: domain.MustFromString("900.00"), Currency: "USD",
		StatementName: "EMPLOYER PAYROLL", Payee: "Employer", Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, wages))
	seed := seedPadding(l, func(uuid.UUID) uuid.UUID { return payroll.ID })

	l.alex.del("/transactions/" + seed.purchases[0].String()).requireStatus(http.StatusNoContent)

	l.alex.get("/transactions/" + seed.pads[0].String()).requireStatus(http.StatusNotFound)
	kept := l.alex.get("/transactions/" + seed.pads[1].String()).requireStatus(http.StatusOK).json()
	require.Equal(t, seed.purchases[1].String(), kept["padded_txn_id"], "the other pair is untouched")

	row, err := db(t).GetTransaction(t.Context(), space, wages.ID)
	require.NoError(t, err)
	require.True(t, row.HasBalance, "the pad's own account was rebalanced")
	require.Equal(t, domain.MustFromString("900.00"), row.Balance)
}

func TestThePlanCountsPaddingAsIncomeAndMarksIt(t *testing.T) {
	l := planLedger(t)
	seed := seedPadding(l, sameAccount)
	month := planFor(l, augustMonth)

	require.Equal(t, "15.00", effective(t, month, "income"))
	rows := entriesIn(t, month, "income", "contributing")
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.Equal(t, true, row["is_padding"])
		require.Equal(t, "Paycheck", row["category_name"])
	}
	for _, row := range entriesIn(t, month, "other_spend", "contributing") {
		require.Equal(t, false, row["is_padding"])
		if row["txn_id"] == seed.purchases[0].String() {
			require.Equal(t, "-6.50", row["amount"])
		}
	}
}
