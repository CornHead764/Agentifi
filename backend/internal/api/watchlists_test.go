package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Watchlists, end to end.
//
// The card sends a trend and no average: the client averages the full months of
// the trend so the bar and the figure beside it cannot disagree. Every test
// about the average is therefore a test about the `is_partial` flag, because
// that flag is the only thing keeping the current month out of it.

// seedMonthlyGroceries puts one 12.00 grocery shop in each of the twelve full
// months before August 2026, so a trailing average has something to average.
func seedMonthlyGroceries(l *ledger) {
	l.t.Helper()
	for offset := 12; offset >= 1; offset-- {
		month := domain.MonthOf(domain.DateOf(planClock)).Shift(-offset)
		seedTxn(l, fmt.Sprintf("shop_%d", offset), &store.Transaction{
			AccountID: l.id("checking"), Date: month.Day(10),
			Amount: domain.MustFromString("-12.00"), StatementName: "SAFEWAY",
			Payee: "Safeway", CategoryID: l.id("groceries"),
		})
	}
}

func newWatchlist(l *ledger, body map[string]any) map[string]any {
	l.t.Helper()
	return l.alex.post("/watchlists", body).requireStatus(http.StatusCreated).json()
}

// groceryWatchlist points at the seeded groceries filter.
func groceryWatchlist(l *ledger, overrides map[string]any) map[string]any {
	l.t.Helper()
	body := map[string]any{
		"name": "Groceries", "filter_id": l.str("filter"), "target_amount": "60.00",
	}
	for key, value := range overrides {
		body[key] = value
	}
	return newWatchlist(l, body)
}

func trend(t *testing.T, card map[string]any) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, raw := range card["monthly_trend"].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

// averageOfFullMonths is the arithmetic the client does, and the reason the
// server sends no average of its own.
func averageOfFullMonths(t *testing.T, card map[string]any) domain.Money {
	t.Helper()
	var full []domain.Money
	for _, entry := range trend(t, card) {
		if entry["is_partial"] == true {
			continue
		}
		full = append(full, domain.MustFromString(entry["spent"].(string)))
	}
	require.NotEmpty(t, full)
	mean, ok := domain.Total(full...).DivInt(len(full))
	require.True(t, ok)
	return mean.Round()
}

func breakdown(t *testing.T, detail map[string]any, dimension string) []map[string]any {
	t.Helper()
	out := []map[string]any{}
	for _, raw := range detail[dimension].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

// --- The trailing average ----------------------------------------------------

func TestTheTwelveMonthAverageUsesFullMonthsOnly(t *testing.T) {
	// calculations.md §7. Including the current partial month drags the figure
	// down every time and makes it meaningless on the 2nd, which is exactly
	// when somebody checks whether they are overspending.
	l := planLedger(t)
	seedMonthlyGroceries(l)
	card := groceryWatchlist(l, nil)

	entries := trend(t, card)
	require.Len(t, entries, 13, "thirteen bars, so this month can be read against the same month a year ago")
	for _, entry := range entries[:12] {
		require.Equal(t, false, entry["is_partial"], entry["month"])
		require.Equal(t, "12.00", entry["spent"], entry["month"])
	}
	current := entries[12]
	require.Equal(t, "2026-08", current["month"])
	require.Equal(t, true, current["is_partial"])
	require.Equal(t, "50.00", current["spent"])

	require.Equal(t, "12.00", averageOfFullMonths(t, card).String())
	// Averaging all thirteen would read 14.92 — the number the flag exists to
	// stop the client computing.
}

func TestTheCardCarriesNoAverageOfItsOwnToContradictItsBars(t *testing.T) {
	l := planLedger(t)
	seedMonthlyGroceries(l)
	card := groceryWatchlist(l, nil)

	require.NotContains(t, card, "twelve_month_average")
	require.Equal(t, "2026-08-20", card["as_of"])
}

// --- The card's four figures -------------------------------------------------

func TestTheCardsFiguresAllComeFromTheSamePass(t *testing.T) {
	l := planLedger(t)
	seedMonthlyGroceries(l)
	card := groceryWatchlist(l, nil)

	require.Equal(t, "50.00", card["this_month_spent"])
	// 50.00 over 20 elapsed days, carried across all 31.
	require.Equal(t, "77.50", card["month_projection"])
	// January through July at 12.00 each, plus August's 50.00.
	require.Equal(t, "134.00", card["year_to_date"])

	require.Equal(t, "60.00", card["target_amount"])
	require.Equal(t, "10.00", card["left_to_target"])
	requireRate(t, "83.33", card["pct_of_target"])
	require.Equal(t, false, card["is_over_target"])
	require.Equal(t, true, card["is_projected_over_target"])
}

func TestAWatchlistWithNoTargetCannotBreachOne(t *testing.T) {
	// A missing target must read as "no target" rather than as a target of
	// zero, which every row would breach.
	l := planLedger(t)
	card := groceryWatchlist(l, map[string]any{"target_amount": nil})

	require.Nil(t, card["target_amount"])
	require.Nil(t, card["left_to_target"])
	require.Nil(t, card["pct_of_target"])
	require.Equal(t, false, card["is_over_target"])
	require.Equal(t, false, card["is_projected_over_target"])
}

func TestARefundNetsAgainstTheMonthsSpendRatherThanAddingToIt(t *testing.T) {
	// calculations.md §13's departure from §7: taking magnitudes makes a
	// returned 100.00 purchase read as 200.00 of spending.
	l := planLedger(t)
	seedTxn(l, "grocery_refund", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 18),
		Amount: domain.MustFromString("20.00"), StatementName: "SAFEWAY REFUND",
		Payee: "Safeway", CategoryID: l.id("groceries"),
	})
	card := groceryWatchlist(l, nil)

	require.Equal(t, "30.00", card["this_month_spent"])
}

func TestARowExcludedFromReportsLeavesTheWatchlist(t *testing.T) {
	// The detail modal promises the Reports checkbox affects watchlists too, so
	// a watchlist that ignored it would contradict its own tooltip.
	l := planLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"excluded_from_reports": true}).requireStatus(http.StatusOK)

	card := groceryWatchlist(l, nil)
	require.Equal(t, "0.00", card["this_month_spent"])
}

func TestExcludingARowFromTheSpendingPlanLeavesTheWatchlistAlone(t *testing.T) {
	// Ground rule 4: the two flags are independent questions.
	l := planLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"excluded_from_spending_plan": true}).requireStatus(http.StatusOK)

	card := groceryWatchlist(l, nil)
	require.Equal(t, "50.00", card["this_month_spent"])
}

// --- The detail view ---------------------------------------------------------

func TestTheBreakdownIsAboutTheSelectedMonthWhileTheCardIsAboutNow(t *testing.T) {
	l := planLedger(t)
	seedMonthlyGroceries(l)
	created := groceryWatchlist(l, nil)

	detail := l.alex.get("/watchlists/" + created["id"].(string) + "?month=2026-07").
		requireStatus(http.StatusOK).json()

	require.Equal(t, "2026-07", detail["month"])
	require.Equal(t, "12.00", detail["spent"])
	// The card half of the response is still about today.
	require.Equal(t, "50.00", detail["this_month_spent"])

	byCategory := breakdown(t, detail, "by_category")
	require.Len(t, byCategory, 1)
	require.Equal(t, l.str("groceries"), byCategory[0]["key"])
	require.Equal(t, "12.00", byCategory[0]["spent"])
	requireRate(t, "1", byCategory[0]["share"])

	byPayee := breakdown(t, detail, "by_payee")
	require.Len(t, byPayee, 1)
	require.Equal(t, "Safeway", byPayee[0]["key"])
}

func TestEveryBreakdownSliceCarriesTheNameItsChipPrints(t *testing.T) {
	// A slice groups by id. A chip that printed the key would read as a uuid,
	// and the client cannot fetch a row per chip to name it.
	l := planLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"tag_ids": []string{l.str("tag")}}).requireStatus(http.StatusOK)
	created := groceryWatchlist(l, nil)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()

	byCategory := breakdown(t, detail, "by_category")
	require.Equal(t, l.str("groceries"), byCategory[0]["key"])
	require.Equal(t, "Groceries", byCategory[0]["label"])

	byTag := breakdown(t, detail, "by_tag")
	require.Equal(t, l.str("tag"), byTag[0]["key"])
	require.Equal(t, "reimbursable", byTag[0]["label"])

	// A payee is grouped by the name itself, so the label is the key.
	byPayee := breakdown(t, detail, "by_payee")
	require.Equal(t, "Safeway", byPayee[0]["key"])
	require.Equal(t, "Safeway", byPayee[0]["label"])
}

func TestTheUntaggedSliceHasNoNameRatherThanABorrowedOne(t *testing.T) {
	// It is a slice and not an absence — dropping it would make the parts sum
	// to less than the total — and naming it is the client's business.
	l := planLedger(t)
	created := groceryWatchlist(l, nil)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()

	byTag := breakdown(t, detail, "by_tag")
	require.Len(t, byTag, 1)
	require.Equal(t, "", byTag[0]["key"])
	require.Equal(t, "", byTag[0]["label"])
	require.Equal(t, "50.00", byTag[0]["spent"])
}

func TestABreakdownShareIsAFractionAndNotAPercentage(t *testing.T) {
	// The client renders it as a percentage. A server that sent 50 for a half
	// would have every chip read 5000%.
	l := planLedger(t)
	seedTxn(l, "august_other_grocer", &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-50.00"), StatementName: "TRADER JOES",
		Payee: "Trader Joe's", CategoryID: l.id("groceries"),
	})
	created := groceryWatchlist(l, nil)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()

	byPayee := breakdown(t, detail, "by_payee")
	require.Len(t, byPayee, 2)
	requireRate(t, "0.5", byPayee[0]["share"])
	requireRate(t, "0.5", byPayee[1]["share"])
}

func TestTheDetailDefaultsToTheMonthTheUserIsStandingIn(t *testing.T) {
	l := planLedger(t)
	created := groceryWatchlist(l, nil)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-08", detail["month"])
	require.Equal(t, "50.00", detail["spent"])
}

func TestAMonthQueryThatIsNotAMonthIsRefused(t *testing.T) {
	l := planLedger(t)
	created := groceryWatchlist(l, nil)
	l.alex.get("/watchlists/" + created["id"].(string) + "?month=2026-08-01").
		requireStatus(http.StatusUnprocessableEntity)
}

// --- Creating and deleting ---------------------------------------------------

func TestAWatchlistBuiltFromCategoriesGetsAFilterOfItsOwn(t *testing.T) {
	// Ground rule 3: one filter entity, mounted everywhere.
	l := planLedger(t)
	card := newWatchlist(l, map[string]any{
		"name": "Groceries", "category_ids": []string{l.str("groceries")},
	})

	require.NotEmpty(t, card["filter_id"])
	require.NotEqual(t, l.str("filter"), card["filter_id"])
	require.Equal(t, "50.00", card["this_month_spent"])

	filter := l.alex.get("/filters/" + card["filter_id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, WatchlistFilterScope, filter["scope"])
}

func TestAWatchlistNeedsExactlyOneSubject(t *testing.T) {
	// None is a watchlist over the whole ledger — a filter with no items
	// matches everything — and more than one leaves it ambiguous which
	// selection the card is about.
	l := planLedger(t)
	neither := l.alex.post("/watchlists", map[string]any{"name": "Everything"})
	neither.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, neither.Body.String(), "filter_id")

	l.alex.post("/watchlists", map[string]any{
		"name": "Both", "filter_id": l.str("filter"),
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusUnprocessableEntity)

	l.alex.post("/watchlists", map[string]any{
		"name": "Two shortcuts", "category_ids": []string{l.str("groceries")},
		"payee_names": []string{"Safeway"},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestAWatchlistNeedsAName(t *testing.T) {
	l := planLedger(t)
	response := l.alex.post("/watchlists", map[string]any{
		"name": " ", "filter_id": l.str("filter"),
	})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "name")
}

func TestDeletingAWatchlistLeavesTheFilterForWhoeverElseUsesIt(t *testing.T) {
	// Filters are shared; deleting one out from under a rule or an envelope
	// would make that surface match everything.
	l := planLedger(t)
	report := l.alex.post("/filters", map[string]any{
		"name": "Groceries report", "scope": "report", "items": []any{categoryItem(l, "groceries")},
	}).requireStatus(http.StatusCreated).json()
	card := groceryWatchlist(l, map[string]any{"filter_id": report["id"]})

	l.alex.del("/watchlists/" + card["id"].(string)).requireStatus(http.StatusNoContent)
	l.alex.get("/watchlists/" + card["id"].(string)).requireStatus(http.StatusNotFound)
	require.Empty(t, l.alex.get("/watchlists").requireStatus(http.StatusOK).list())

	l.alex.get("/filters/" + report["id"].(string)).requireStatus(http.StatusOK)
}

func TestDeletingAWatchlistDeletesTheFilterOnlyItUses(t *testing.T) {
	l := planLedger(t)
	card := newWatchlist(l, map[string]any{"name": "Safeway", "items": []any{payeeItem("Safeway")}})
	owned := card["filter_id"].(string)
	borrower := groceryWatchlist(l, map[string]any{"name": "Also Safeway", "filter_id": owned})

	l.alex.del("/watchlists/" + card["id"].(string)).requireStatus(http.StatusNoContent)
	l.alex.get("/filters/" + owned).requireStatus(http.StatusOK)

	l.alex.del("/watchlists/" + borrower["id"].(string)).requireStatus(http.StatusNoContent)
	l.alex.get("/filters/" + owned).requireStatus(http.StatusNotFound)
}

// --- Money on the wire -------------------------------------------------------

func TestMoneyCrossesTheWatchlistWireAsAString(t *testing.T) {
	l := planLedger(t)
	card := groceryWatchlist(l, nil)
	for _, field := range []string{"this_month_spent", "month_projection",
		"year_to_date", "target_amount", "left_to_target"} {
		require.IsType(t, "", card[field], field)
	}
	require.IsType(t, "", trend(t, card)[0]["spent"])
}

func TestAJsonNumberInAWatchlistsMoneyFieldIsRefused(t *testing.T) {
	l := planLedger(t)
	response := l.alex.raw(http.MethodPost, "/watchlists", fmt.Sprintf(
		`{"name": "Groceries", "filter_id": %q, "target_amount": 60}`, l.str("filter")))
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "string")
}

// --- Tenancy -----------------------------------------------------------------

func seedStrangerWatchlist(l *ledger) uuid.UUID {
	l.t.Helper()
	id := uuid.New()
	_, err := l.env.DB.Pool().Exec(l.t.Context(), `
		INSERT INTO watchlists (id, space_id, filter_id, name, period)
		VALUES ($1, $2, $3, 'Theirs', 'monthly')`,
		id, l.id("other_space"), l.id("stranger_filter"))
	require.NoError(l.t, err)
	return id
}

func TestAWatchlistFromAnotherSpaceIsInvisible(t *testing.T) {
	l := planLedger(t)
	strangerWatchlist := seedStrangerWatchlist(l)
	groceryWatchlist(l, nil)

	cards := l.alex.get("/watchlists").requireStatus(http.StatusOK).list()
	require.Len(t, cards, 1)
	require.Equal(t, "Groceries", cards[0]["name"])

	l.alex.get("/watchlists/" + strangerWatchlist.String()).requireStatus(http.StatusNotFound)
	l.alex.del("/watchlists/" + strangerWatchlist.String()).requireStatus(http.StatusNotFound)
}

func TestAWatchlistCannotBorrowAnotherSpacesFilterOrCategories(t *testing.T) {
	l := planLedger(t)
	l.alex.post("/watchlists", map[string]any{
		"name": "Theirs", "filter_id": l.str("stranger_filter"),
	}).requireStatus(http.StatusConflict)

	l.alex.post("/watchlists", map[string]any{
		"name": "Theirs", "category_ids": []string{l.str("stranger_category")},
	}).requireStatus(http.StatusConflict)
}

func TestAViewerReadsTheWatchlistsButCannotChangeThem(t *testing.T) {
	l := planLedger(t)
	card := groceryWatchlist(l, nil)
	vera := l.as("vera")

	vera.get("/watchlists").requireStatus(http.StatusOK)
	vera.get("/watchlists/" + card["id"].(string)).requireStatus(http.StatusOK)
	vera.post("/watchlists", map[string]any{
		"name": "Theirs", "filter_id": l.str("filter"),
	}).requireStatus(http.StatusForbidden)
	vera.del("/watchlists/" + card["id"].(string)).requireStatus(http.StatusForbidden)
}

// The other two thirds of the reference's picker.
//
// The spend engine does not assume categories: it evaluates the filter
// through the same entry point the register uses, so a payee or tag
// watchlist computes correctly given a filter. The picker is the way to say
// so without building a saved filter by hand first.

func TestAWatchlistCanWatchOnePayee(t *testing.T) {
	l := planLedger(t)
	card := newWatchlist(l, map[string]any{
		"name": "Safeway", "payee_names": []string{"Safeway"},
	})
	require.Equal(t, "50.00", card["this_month_spent"],
		"the payee's August charge did not reach the card")

	filter := l.alex.get("/filters/" + card["filter_id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, WatchlistFilterScope, filter["scope"])
	item := filter["items"].([]any)[0].(map[string]any)
	require.Equal(t, string(domain.FieldPayee), item["field"])
	require.Equal(t, []any{"Safeway"}, item["value_texts"])

	// Exact membership, not contains: the picker offers names that exist, and
	// a substring match would pull in every payee sharing a word with one.
	require.Equal(t, string(domain.OpIn), item["operator"])
}

func TestAWatchlistCanWatchOneTag(t *testing.T) {
	l := planLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"tag_ids": []string{l.str("tag")}}).requireStatus(http.StatusOK)

	card := newWatchlist(l, map[string]any{
		"name": "Reimbursable", "tag_ids": []string{l.str("tag")},
	})
	require.Equal(t, "50.00", card["this_month_spent"])

	filter := l.alex.get("/filters/" + card["filter_id"].(string)).
		requireStatus(http.StatusOK).json()
	item := filter["items"].([]any)[0].(map[string]any)
	require.Equal(t, string(domain.FieldTag), item["field"])
	require.Equal(t, []any{l.str("tag")}, item["value_ids"])
}

func TestAPayeeWatchlistIgnoresBlanksAndRepeats(t *testing.T) {
	l := planLedger(t)
	card := newWatchlist(l, map[string]any{
		"name": "Safeway", "payee_names": []string{" Safeway ", "Safeway", "   "},
	})
	filter := l.alex.get("/filters/" + card["filter_id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, []any{"Safeway"}, filter["items"].([]any)[0].(map[string]any)["value_texts"])

	// Whitespace alone is no subject at all.
	l.alex.post("/watchlists", map[string]any{
		"name": "Nobody", "payee_names": []string{"  "},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestAWatchlistCannotBorrowAnotherSpacesTag(t *testing.T) {
	l := planLedger(t)
	l.alex.post("/watchlists", map[string]any{
		"name": "Theirs", "tag_ids": []string{l.str("stranger_tag")},
	}).requireStatus(http.StatusConflict)
}

// The payees a picker can offer.
//
// A payee is a column rather than a row, so nothing could list them: the
// register's own facet offers whatever rows it happens to have loaded and
// silently omits the rest.

func TestThePayeeListIsOrderedByHowOftenEachIsUsed(t *testing.T) {
	l := planLedger(t)
	seedMonthlyGroceries(l)
	list := payeeNames(l.alex)
	require.Contains(t, list, "Safeway")
	require.Equal(t, "Safeway", list[0], "the most-used payee is not first")
	require.NotContains(t, list, "", "a nameless row is not a payee")
}

func TestThePayeeListStopsAtTheSpaceBoundary(t *testing.T) {
	l := planLedger(t)
	theirs := l.as("vera")
	require.NotNil(t, theirs)
	// Vera reads this space, so she sees this space's payees.
	require.NotEmpty(t, payeeNames(theirs))
}

// payeeNames decodes the bare array the endpoint answers with — the same shape
// /tags and /categories use, rather than a wrapper this one alone would need.
func payeeNames(c *client) []string {
	c.t.Helper()
	var out []string
	require.NoError(c.t,
		json.Unmarshal(c.get("/transactions/payees").requireStatus(http.StatusOK).Body.Bytes(), &out))
	return out
}

// --- The category's own exclusion -------------------------------------------

// calculations.md §13: watchlists respect `excluded_from_reports`, per the
// detail modal's promise that the Reports checkbox affects watchlists — at
// the transaction, the account and the category alike; the category flag is
// the one a watchlist most easily leaves unread.

func TestACategoryExcludedFromReportsLeavesTheWatchlist(t *testing.T) {
	l := planLedger(t)
	created := groceryWatchlist(l, nil)
	require.Equal(t, "50.00", created["this_month_spent"])

	l.alex.patch("/categories/"+l.str("groceries"),
		map[string]any{"excluded_from_reports": true}).requireStatus(http.StatusOK)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", detail["spent"],
		"the excluded category is still being watched")
}

func TestOnlyTheSplitUnderAnExcludedCategoryLeavesTheWatchlist(t *testing.T) {
	// A watchlist counts allocations, and the row-level question above them is
	// asked of the parent — which a split row has no category on. The
	// watchlist's filter selects the Groceries category, so a receipt split
	// between Groceries and Food must lose only the excluded half.
	l := planLedger(t)
	row := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 14),
		Currency: "USD", Amount: domain.MustFromString("-30.00"),
		StatementName: "BIG BOX #77", Payee: "Big Box",
		Splits: []store.Split{
			{Position: 0, Amount: domain.MustFromString("-18.00"), CategoryID: l.id("groceries")},
			{Position: 1, Amount: domain.MustFromString("-12.00"), CategoryID: l.id("food")},
		},
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(),
		store.SpaceIDOf(l.id("space")), row))

	created := groceryWatchlist(l, nil)
	require.Equal(t, "68.00", created["this_month_spent"],
		"the seeded 50.00 plus the groceries half of the receipt")

	l.alex.patch("/categories/"+l.str("groceries"),
		map[string]any{"excluded_from_reports": true}).requireStatus(http.StatusOK)

	detail := l.alex.get("/watchlists/" + created["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", detail["spent"],
		"the split filed under the excluded category is still being counted")
}

// --- The selection as filter items, and editing ------------------------------
//
// The create sheet is the one filter editor, so it sends filter items rather
// than one shortcut, and the same sheet reopens to edit a watchlist.

func categoryItem(l *ledger, key string) map[string]any {
	return map[string]any{
		"field": "category", "operator": "in", "value_ids": []string{l.str(key)},
	}
}

func payeeItem(name string) map[string]any {
	return map[string]any{"field": "payee", "operator": "in", "value_texts": []string{name}}
}

func TestAWatchlistCanWatchSeveralFacetsAtOnce(t *testing.T) {
	// The items are AND-ed, as they are in the register: groceries at Safeway
	// is the August shop, and groceries at the corner store is nothing,
	// because the corner store's row has no category.
	l := planLedger(t)
	atSafeway := newWatchlist(l, map[string]any{
		"name":  "Groceries at Safeway",
		"items": []any{categoryItem(l, "groceries"), payeeItem("Safeway")},
	})
	require.Equal(t, "50.00", atSafeway["this_month_spent"])

	filter := l.alex.get("/filters/" + atSafeway["filter_id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, WatchlistFilterScope, filter["scope"])
	require.Len(t, filter["items"], 2)

	atCorner := newWatchlist(l, map[string]any{
		"name":  "Groceries at the corner",
		"items": []any{categoryItem(l, "groceries"), payeeItem("Corner Store")},
	})
	require.Equal(t, "0.00", atCorner["this_month_spent"])
}

func TestItemsAreASubjectOfTheirOwn(t *testing.T) {
	l := planLedger(t)
	l.alex.post("/watchlists", map[string]any{
		"name": "Both", "items": []any{payeeItem("Safeway")},
		"category_ids": []string{l.str("groceries")},
	}).requireStatus(http.StatusUnprocessableEntity)

	l.alex.post("/watchlists", map[string]any{
		"name": "Nothing", "items": []any{},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestEditingAWatchlistRewritesItsOwnFilterInPlace(t *testing.T) {
	l := planLedger(t)
	card := newWatchlist(l, map[string]any{
		"name": "Corner", "items": []any{categoryItem(l, "groceries"), payeeItem("Corner Store")},
		"target_amount": "40.00",
	})
	require.Equal(t, "0.00", card["this_month_spent"])

	edited := l.alex.patch("/watchlists/"+card["id"].(string), map[string]any{
		"name": "Safeway", "period": "year", "target_amount": nil,
		"items": []any{payeeItem("Safeway")},
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "Safeway", edited["name"])
	require.Equal(t, "year", edited["period"])
	require.Nil(t, edited["target_amount"])
	require.Equal(t, "50.00", edited["this_month_spent"])
	require.Equal(t, card["filter_id"], edited["filter_id"],
		"the watchlist's own filter should be rewritten, not replaced")

	reread := l.alex.get("/watchlists/" + card["id"].(string)).requireStatus(http.StatusOK).json()
	require.Equal(t, "year", reread["period"])
	require.Equal(t, "50.00", reread["this_month_spent"])
}

func TestEditingAWatchlistOverAReportLeavesTheReportAlone(t *testing.T) {
	l := planLedger(t)
	report := l.alex.post("/filters", map[string]any{
		"name": "Groceries report", "scope": "report",
		"items": []any{categoryItem(l, "groceries")},
	}).requireStatus(http.StatusCreated).json()
	card := newWatchlist(l, map[string]any{"name": "Groceries", "filter_id": report["id"]})
	require.Equal(t, "50.00", card["this_month_spent"])

	edited := l.alex.patch("/watchlists/"+card["id"].(string), map[string]any{
		"items": []any{categoryItem(l, "groceries"), payeeItem("Corner Store")},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", edited["this_month_spent"])
	require.NotEqual(t, report["id"], edited["filter_id"])

	kept := l.alex.get("/filters/" + report["id"].(string)).requireStatus(http.StatusOK).json()
	require.Len(t, kept["items"], 1, "editing the card changed the saved report")

	repointed := l.alex.patch("/watchlists/"+card["id"].(string), map[string]any{
		"filter_id": report["id"],
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, report["id"], repointed["filter_id"])
	require.Equal(t, "50.00", repointed["this_month_spent"])
}

func TestAnEditCannotEmptyOrMuddleTheSelection(t *testing.T) {
	l := planLedger(t)
	card := groceryWatchlist(l, nil)
	path := "/watchlists/" + card["id"].(string)

	l.alex.patch(path, map[string]any{"items": []any{}}).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.patch(path, map[string]any{
		"filter_id": l.str("filter"), "items": []any{payeeItem("Safeway")},
	}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.patch(path, map[string]any{"filter_id": l.str("stranger_filter")}).
		requireStatus(http.StatusConflict)
	l.alex.patch(path, map[string]any{"period": ""}).
		requireStatus(http.StatusUnprocessableEntity)
}
