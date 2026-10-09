package api

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The ledger, end to end, against a real database.
//
// Each test drives the HTTP API as a signed-in member and reads the answers
// back, so the rules are checked where a user meets them.

const august = "from=2026-08-01&to=2026-08-31"

func registerPage(l *ledger, query string) map[string]any {
	l.t.Helper()
	return l.alex.get("/transactions?" + query).requireStatus(http.StatusOK).json()
}

func summary(l *ledger, query string) map[string]any {
	l.t.Helper()
	path := fmt.Sprintf("/accounts/%s/summary?%s", l.str("checking"), query)
	return l.alex.get(path).requireStatus(http.StatusOK).json()
}

func idsIn(page map[string]any) map[string]bool {
	out := map[string]bool{}
	for _, item := range page["items"].([]any) {
		out[item.(map[string]any)["id"].(string)] = true
	}
	return out
}

// --- Tenancy -----------------------------------------------------------------

func TestTheRegisterShowsOnlyThisSpace(t *testing.T) {
	l := buildLedger(t)
	page := registerPage(l, "limit=500")
	require.NotContains(t, idsIn(page), l.str("stranger_txn"))
	require.Contains(t, idsIn(page), l.str("august_groceries"))
}

func TestARowInAnotherSpaceIsInvisibleThroughEveryEndpoint(t *testing.T) {
	// 404, never 403: a 403 would confirm the row exists somewhere.
	l := buildLedger(t)
	reads := []string{
		"/transactions/" + l.str("stranger_txn"),
		"/accounts/" + l.str("stranger_account"),
		"/accounts/" + l.str("stranger_account") + "/summary",
		"/categories/" + l.str("stranger_category"),
		"/tags/" + l.str("stranger_tag"),
		"/filters/" + l.str("stranger_filter"),
	}
	for _, path := range reads {
		l.alex.get(path).requireStatus(http.StatusNotFound)
	}

	l.alex.patch("/transactions/"+l.str("stranger_txn"),
		map[string]any{"payee": "Mine now"}).requireStatus(http.StatusNotFound)
	l.alex.del("/transactions/" + l.str("stranger_txn")).requireStatus(http.StatusNotFound)
	l.alex.patch("/accounts/"+l.str("stranger_account"),
		map[string]any{"name": "Mine now"}).requireStatus(http.StatusNotFound)
	l.alex.del("/accounts/" + l.str("stranger_account")).requireStatus(http.StatusNotFound)
	l.alex.patch("/categories/"+l.str("stranger_category"),
		map[string]any{"name": "Mine now"}).requireStatus(http.StatusNotFound)
	l.alex.patch("/tags/"+l.str("stranger_tag"),
		map[string]any{"name": "Mine now"}).requireStatus(http.StatusNotFound)
	l.alex.patch("/filters/"+l.str("stranger_filter"),
		map[string]any{"name": "Mine"}).requireStatus(http.StatusNotFound)
}

func TestAnotherSpacesIDsCannotBeBorrowedOnAWrite(t *testing.T) {
	// A foreign id in the body is refused, not silently written into this space.
	l := buildLedger(t)
	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("stranger_account"),
		"date":       "2026-08-15",
		"amount":     "-10.00",
	}).requireStatus(http.StatusConflict)

	l.alex.patch("/transactions/"+l.str("august_groceries"), map[string]any{
		"tag_ids": []string{l.str("stranger_tag")},
	}).requireStatus(http.StatusConflict)
}

func TestASpaceTheCallerIsNotAMemberOfIsNotFound(t *testing.T) {
	l := buildLedger(t)
	other := l.as("alex")
	other.spaceID = l.str("other_space")
	other.get("/transactions").requireStatus(http.StatusNotFound)
}

func TestAViewerReadsButCannotWrite(t *testing.T) {
	l := buildLedger(t)
	vera := l.as("vera")
	vera.get("/transactions").requireStatus(http.StatusOK)
	vera.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"payee": "Nope"}).requireStatus(http.StatusForbidden)
}

// --- Two names ---------------------------------------------------------------

func TestEditingThePayeeLeavesTheStatementNameUntouched(t *testing.T) {
	// Ground rule 5. Display reads payee; matching reads statement_name.
	l := buildLedger(t)
	body := l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"payee": "Safeway Groceries"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "Safeway Groceries", body["payee"])
	require.Equal(t, "SAFEWAY #1234 SPRINGFIELD ZZ", body["statement_name"])
}

func TestEditingAnAmountClearsTheStaleForeignConversion(t *testing.T) {
	// The stored primary amount was computed against the old figure. Left in
	// place it is a wrong total StampSpace never revisits, because it skips a
	// row that already has a primary. Editing the amount must clear it so the
	// next conversion re-derives. The row is seeded here rather than in the
	// shared fixture, whose exact totals other tests reconcile by hand.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	foreign := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.August, 12),
		Amount: domain.MustFromString("-40.00"), Currency: "EUR",
		AmountPrimary: domain.MustFromString("-44.00"), HasAmountPrimary: true,
		FxRateUsed: rateOf(t, "1.10"), HasFxRateUsed: true,
		StatementName: "CAFE PARIS", Payee: "Cafe Paris", Source: domain.SourceManual,
	}
	require.NoError(t, l.env.DB.CreateTransaction(t.Context(), space, foreign))
	path := "/transactions/" + foreign.ID.String()

	before := l.alex.get(path).requireStatus(http.StatusOK).json()
	require.Equal(t, "-44.00", before["amount_primary"])

	after := l.alex.patch(path, map[string]any{"amount": "-60.00"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-60.00", after["amount"])
	require.Nil(t, after["amount_primary"], "the stale conversion must be cleared")
}

func rateOf(t *testing.T, text string) domain.Rate {
	t.Helper()
	r, err := decimal.NewFromString(text)
	require.NoError(t, err)
	return r
}

func TestTheStatementNameCannotBeRewritten(t *testing.T) {
	// A rule that could rewrite the string it matches on stops firing forever.
	l := buildLedger(t)
	response := l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"statement_name": "ANYTHING ELSE"})
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "statement_name")
}

func TestTheSearchBoxFindsARenamedRowByTheBanksWording(t *testing.T) {
	l := buildLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"payee": "Weekly shop"}).requireStatus(http.StatusOK)
	page := registerPage(l, "search=SAFEWAY")
	require.Contains(t, idsIn(page), l.str("august_groceries"))
}

// --- The two exclusion flags -------------------------------------------------

func TestTheExclusionFlagsMoveIndependentlyOnATransaction(t *testing.T) {
	// Ground rule 4: a reimbursed work charge is a real expense the user does
	// not plan around, so reports and the spending plan disagree on purpose.
	l := buildLedger(t)
	path := "/transactions/" + l.str("august_groceries")

	first := l.alex.patch(path, map[string]any{"excluded_from_spending_plan": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, first["excluded_from_spending_plan"])
	require.Equal(t, false, first["excluded_from_reports"])

	second := l.alex.patch(path, map[string]any{"excluded_from_reports": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, second["excluded_from_reports"])
	require.Equal(t, true, second["excluded_from_spending_plan"])
}

func TestTheExclusionFlagsMoveIndependentlyOnAnAccount(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.patch("/accounts/"+l.str("card"),
		map[string]any{"excluded_from_spending_plan": true}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["excluded_from_spending_plan"])
	require.Equal(t, false, body["excluded_from_reports"])
	// And the other two flags, which are separate questions again.
	require.Equal(t, true, body["include_in_net_worth"])
	require.Equal(t, false, body["excluded_from_account_bar"])
}

// --- The two dates -----------------------------------------------------------

func TestTheRegisterFiltersOnThePostedDate(t *testing.T) {
	// The card charge posted on 25 August and hits cash flow on 10 September;
	// a register that filtered on the wrong one would move a month of card
	// spending.
	l := buildLedger(t)
	page := registerPage(l, "account_id="+l.str("card")+"&date_field=posted&"+august)
	require.Contains(t, idsIn(page), l.str("card_charge"))
	require.Equal(t, "posted", page["window"].(map[string]any)["date_field"])
}

func TestTheEffectiveWindowFilesTheCardChargeInSeptember(t *testing.T) {
	l := buildLedger(t)
	augustPage := registerPage(l, "account_id="+l.str("card")+"&date_field=effective&"+august)
	require.NotContains(t, idsIn(augustPage), l.str("card_charge"))

	september := registerPage(l, "account_id="+l.str("card")+
		"&date_field=effective&from=2026-09-01&to=2026-09-30")
	require.Contains(t, idsIn(september), l.str("card_charge"))
	require.Equal(t, "effective", september["window"].(map[string]any)["date_field"])
}

func TestAnEffectiveWindowStillFindsRowsWithNoEffectiveDate(t *testing.T) {
	// effective_date IS NULL means "same as date". Most rows have no effective
	// date at all; a window that did not coalesce would drop every one of them
	// out of every report.
	l := buildLedger(t)
	page := registerPage(l, "account_id="+l.str("checking")+"&date_field=effective&"+august)
	require.Contains(t, idsIn(page), l.str("august_groceries"))
}

func TestMovingACardsCycleRestampsTheChargesAlreadyOnFile(t *testing.T) {
	// The effective date decides which month a charge reaches cash flow, and
	// every row on file was stamped against the cycle as it stood. Changing the
	// close day and stopping there would leave that history filed under the old
	// cycle, disagreeing with the statements for good.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	// The cycle needs both days and the seeded card has only the close day to
	// come. Written through the store here to keep this test about the cycle;
	// the endpoint accepts a due date too, which the statement test covers.
	card, err := db(t).GetAccount(t.Context(), space, l.id("card"))
	require.NoError(t, err)
	card.DueDate = domain.NewDate(2026, time.September, 10)
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, &card))

	l.alex.patch("/accounts/"+l.str("card"), map[string]any{"statement_close_day": 20}).
		requireStatus(http.StatusOK)

	// Charged on 25 August, past a cycle closing on the 20th: it lands in the
	// one closing 20 September and falls due on 10 October.
	moved := l.alex.get("/transactions/" + l.str("card_charge")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-10-10", moved["effective_date"])
}

// SimpleFIN has no statement: no field for a statement balance, a minimum, a
// due date or an APR, and nothing else in the tree ever wrote those four
// columns. So they are typed in, or the account header has nothing to draw —
// which is why it leaves an absent figure out rather than drawing a dash.
func TestACardsStatementCanBeTypedInWhenNoBankSendsOne(t *testing.T) {
	l := buildLedger(t)

	saved := l.alex.patch("/accounts/"+l.str("card"), map[string]any{
		"statement_balance": "1200.00",
		"minimum_due":       "35.00",
		"due_date":          "2026-09-28",
		"interest_rate":     "24.99",
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "1200.00", saved["statement_balance"])
	require.Equal(t, "35.00", saved["minimum_due"])
	require.Equal(t, "2026-09-28", saved["due_date"])
	// Typed as a percentage and stored as a rate: 24.99 and 0.2499 are the same
	// figure, and domain.APRAsRate is the one place that decides which is meant.
	require.Equal(t, "0.2499", saved["interest_rate"])

	cleared := l.alex.patch("/accounts/"+l.str("card"), map[string]any{
		"statement_balance": nil,
		"minimum_due":       nil,
		"due_date":          nil,
		"interest_rate":     nil,
	}).requireStatus(http.StatusOK).json()

	// Cleared is a figure the header omits, not a zero it prints.
	require.Nil(t, cleared["statement_balance"])
	require.Nil(t, cleared["minimum_due"])
	require.Nil(t, cleared["due_date"])
	require.Nil(t, cleared["interest_rate"])
}

func TestARateNoCardChargesIsRefusedRatherThanStored(t *testing.T) {
	// 400 as a percentage is a 400% APR and as a rate is 40,000%. Neither is a
	// card's rate, and a wrong figure in the header is worse than none.
	l := buildLedger(t)
	l.alex.patch("/accounts/"+l.str("card"), map[string]any{"interest_rate": "400"}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAnEditThatLeavesTheCycleAloneLeavesTheDatesAlone(t *testing.T) {
	// Re-stamping overwrites, so it has to be the cycle moving that triggers
	// it. A rename that restamped would throw away a hand-corrected date.
	l := buildLedger(t)
	l.alex.patch("/accounts/"+l.str("card"), map[string]any{"name": "Rewards Visa"}).
		requireStatus(http.StatusOK)

	body := l.alex.get("/transactions/" + l.str("card_charge")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-09-10", body["effective_date"])
}

func TestANullOpeningBalanceIsZeroOnCreateAndEdit(t *testing.T) {
	l := buildLedger(t)
	created := l.alex.post("/accounts", map[string]any{
		"name": "Cash Jar", "kind": string(domain.KindCash), "type": "cash", "opening_balance": nil,
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "0.00", created["opening_balance"])

	edited := l.alex.patch("/accounts/"+l.str("checking"), map[string]any{"opening_balance": nil}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", edited["opening_balance"])
}

// --- One window, two endpoints -----------------------------------------------

func TestAnOmittedWindowMeansTheSameThingInBothEndpoints(t *testing.T) {
	// Neither request names a window, and both must answer the same one.
	l := buildLedger(t)
	page := registerPage(l, "account_id="+l.str("checking")+"&limit=500")
	card := summary(l, "")

	require.Equal(t, page["window"], card["window"])
	require.Equal(t, page["count"], card["count"])
	require.Equal(t, page["total"], card["total"])
	require.Equal(t, "500.00", card["opening_balance"])
	require.Equal(t, "125.00", card["ending_balance"])

	opening := domain.MustFromString(card["opening_balance"].(string))
	total := domain.MustFromString(page["total"].(string))
	require.Equal(t, card["ending_balance"], opening.Add(total).String())
}

func TestAnExplicitWindowReconcilesTheSameWay(t *testing.T) {
	l := buildLedger(t)
	page := registerPage(l, "account_id="+l.str("checking")+"&limit=500&"+august)
	card := summary(l, august)

	expected := map[string]any{
		"from": "2026-08-01", "to": "2026-08-31", "date_field": "posted",
	}
	require.Equal(t, expected, page["window"])
	require.Equal(t, expected, card["window"])
	// July's 100.00 is inside the opening balance and outside the register.
	require.Equal(t, "400.00", card["opening_balance"])
	require.Equal(t, "-275.00", card["total"])
	require.Equal(t, "-275.00", page["total"])
	require.Equal(t, "125.00", card["ending_balance"])
}

func TestAnInvertedWindowIsRefusedRatherThanReturningNothing(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/transactions?from=2026-08-31&to=2026-08-01").
		requireStatus(http.StatusUnprocessableEntity)
}

func TestPagingTheRegisterKeepsOneOrderAndOneTotal(t *testing.T) {
	// The count and the total describe the result set, not the page. The
	// active-filter chip reports "120 results · −$12,345.67" beside a grid
	// showing twenty of them; a total that shrank with the page size would be
	// a different number every time somebody scrolled.
	l := buildLedger(t)
	account := "account_id=" + l.str("checking")
	whole := registerPage(l, account+"&limit=500")
	first := registerPage(l, account+"&limit=2&offset=0")
	second := registerPage(l, account+"&limit=2&offset=2")

	require.Equal(t, whole["count"], first["count"])
	require.Equal(t, whole["count"], second["count"])
	require.Equal(t, whole["total"], first["total"])
	require.Equal(t, whole["total"], second["total"])
	require.Len(t, first["items"], 2)

	for id := range idsIn(first) {
		require.NotContains(t, idsIn(second), id)
		require.Contains(t, idsIn(whole), id)
	}
	for id := range idsIn(second) {
		require.Contains(t, idsIn(whole), id)
	}
}

func TestTheRegisterReadsNewestFirstUnlessAskedOtherwise(t *testing.T) {
	l := buildLedger(t)
	account := "account_id=" + l.str("checking")
	newestFirst := registerPage(l, account+"&limit=500")
	oldestFirst := registerPage(l, account+"&limit=500&order=asc")

	dates := itemField(newestFirst, "date")
	for i := 1; i < len(dates); i++ {
		require.GreaterOrEqual(t, dates[i-1], dates[i], "the default order is newest first")
	}

	forward := itemField(newestFirst, "id")
	backward := itemField(oldestFirst, "id")
	require.Len(t, backward, len(forward))
	for i := range forward {
		require.Equal(t, forward[len(forward)-1-i], backward[i],
			"asc must be exactly the reverse of desc")
	}
}

// --- Transfers ---------------------------------------------------------------

func TestDeletingATransferLegReleasesItsPartner(t *testing.T) {
	// A surviving leg still marked as paired would stay out of the reports.
	l := buildLedger(t)
	l.alex.del("/transactions/" + l.str("transfer_out")).requireStatus(http.StatusNoContent)

	survivor := l.alex.get("/transactions/" + l.str("transfer_in")).
		requireStatus(http.StatusOK).json()
	require.Nil(t, survivor["transfer_pair_id"])

	rows, err := db(t).ListTransactions(t.Context(),
		store.SpaceIDOf(l.id("space")), store.TransactionQuery{})
	require.NoError(t, err)
	require.Empty(t, domain.FindOrphanTransferLegs(store.DomainTransactions(rows)))
}

func TestADeletedRowLeavesTheRegister(t *testing.T) {
	l := buildLedger(t)
	l.alex.del("/transactions/" + l.str("july")).requireStatus(http.StatusNoContent)
	page := registerPage(l, "limit=500")
	require.NotContains(t, idsIn(page), l.str("july"))
	l.alex.get("/transactions/" + l.str("july")).requireStatus(http.StatusNotFound)
}

// --- Reviewed ----------------------------------------------------------------

func TestMarkAllAsReviewedAppliesToTheQueryNotThePage(t *testing.T) {
	// The review queue's button acts on what the filter selected. limit=1 here
	// is the point: a bulk action that only touched the page would report 1.
	l := buildLedger(t)
	unreviewed := registerPage(l, "reviewed=false&limit=500")
	require.Equal(t, float64(5), unreviewed["count"])

	result := l.alex.post("/transactions/mark-reviewed?reviewed=false&limit=1",
		map[string]any{"is_reviewed": true}).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(5), result["updated"])

	require.Equal(t, float64(0), registerPage(l, "reviewed=false&limit=500")["count"])
}

func TestTheReviewedFlagIsSettablePerRow(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.patch("/transactions/"+l.str("august_groceries"),
		map[string]any{"is_reviewed": true}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, body["is_reviewed"])
}

// --- Splits and tags ---------------------------------------------------------

func TestAnUpdateCanCarryTheSplitsAndSavesThemWithTheRow(t *testing.T) {
	l := buildLedger(t)
	id := l.str("august_groceries")
	body := l.alex.patch("/transactions/"+id, map[string]any{
		"notes": "shared shop",
		"splits": []map[string]any{
			{"amount": "-30.00", "category_id": l.str("groceries")},
			{"amount": "-20.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, "shared shop", body["notes"])
	require.Nil(t, body["category_id"])
	require.Len(t, body["splits"].([]any), 2)

	cleared := l.alex.patch("/transactions/"+id, map[string]any{
		"category_id": l.str("groceries"),
		"splits":      []map[string]any{},
	}).requireStatus(http.StatusOK).json()
	require.Empty(t, cleared["splits"])
	require.Equal(t, l.str("groceries"), cleared["category_id"])
}

func TestAnUpdateWithSplitsThatDoNotSumChangesNothing(t *testing.T) {
	l := buildLedger(t)
	id := l.str("august_groceries")
	l.alex.patch("/transactions/"+id, map[string]any{
		"notes":  "should not stick",
		"splits": []map[string]any{{"amount": "-10.00"}, {"amount": "-10.00"}},
	}).requireStatus(http.StatusConflict)

	row := l.alex.get("/transactions/" + id).requireStatus(http.StatusOK).json()
	require.NotEqual(t, "should not stick", row["notes"])
	require.Empty(t, row["splits"])
}

func TestSplitsMustSumToTheTransaction(t *testing.T) {
	// A row whose parts do not add up is one figure in the register and a
	// different one in every report.
	l := buildLedger(t)
	l.alex.put("/transactions/"+l.str("august_groceries")+"/splits", map[string]any{
		"splits": []map[string]any{{"amount": "-10.00"}, {"amount": "-10.00"}},
	}).requireStatus(http.StatusConflict)
}

func TestSplittingARowClearsTheParentsCategory(t *testing.T) {
	// Reports read the splits when they exist and the parent otherwise; a
	// category on both counts the row twice.
	l := buildLedger(t)
	body := l.alex.put("/transactions/"+l.str("august_groceries")+"/splits", map[string]any{
		"splits": []map[string]any{
			{
				"amount":      "-30.00",
				"category_id": l.str("groceries"),
				"memo":        "food",
				"tag_ids":     []string{l.str("tag")},
			},
			{"amount": "-20.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK).json()

	require.Nil(t, body["category_id"])
	splits := body["splits"].([]any)
	require.Len(t, splits, 2)
	require.Equal(t, "-30.00", splits[0].(map[string]any)["amount"])
	require.Equal(t, "-20.00", splits[1].(map[string]any)["amount"])
	require.Equal(t, []any{l.str("tag")}, splits[0].(map[string]any)["tag_ids"])
}

func TestAFilteredRegisterShowsOnlyTheMatchingPartOfASplitRow(t *testing.T) {
	// A $25 row split $10 groceries / $15 dining, under a groceries filter, is
	// one row worth $10 — the figure the groceries report and envelope count —
	// and the chip carries the whole-row total beside it.
	l := buildLedger(t)
	corner := l.str("august_corner")
	split := l.alex.put("/transactions/"+corner+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-10.00", "category_id": l.str("groceries")},
			{"amount": "-15.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK).json()
	groceriesSplit := split["splits"].([]any)[0].(map[string]any)["id"]

	page := registerPage(l, august+"&filter_id="+l.str("filter"))
	require.Equal(t, float64(2), page["count"])
	require.Equal(t, "-60.00", page["total"])
	require.Equal(t, "-75.00", page["full_total"])
	require.Equal(t, float64(1), page["partial_count"])

	byID := map[string]map[string]any{}
	for _, item := range page["items"].([]any) {
		row := item.(map[string]any)
		byID[row["id"].(string)] = row
	}
	require.Equal(t, "-10.00", byID[corner]["matched_amount"])
	require.Equal(t, []any{groceriesSplit}, byID[corner]["matched_split_ids"])
	require.Equal(t, "-25.00", byID[corner]["amount"])
	whole := byID[l.str("august_groceries")]
	require.Nil(t, whole["matched_amount"])
	require.Nil(t, whole["matched_split_ids"])
}

func TestAnUnfilteredRegisterCountsASplitRowWhole(t *testing.T) {
	l := buildLedger(t)
	corner := l.str("august_corner")
	l.alex.put("/transactions/"+corner+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-10.00", "category_id": l.str("groceries")},
			{"amount": "-15.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK)

	page := registerPage(l, august+"&account_id="+l.str("checking"))
	require.Equal(t, page["total"], page["full_total"])
	require.Equal(t, float64(0), page["partial_count"])
	for _, item := range page["items"].([]any) {
		require.Nil(t, item.(map[string]any)["matched_amount"])
	}
}

func TestChangingTheAmountOfASplitRowIsRefused(t *testing.T) {
	// The allocations were written against the old figure. Rescaling them
	// would invent a split nobody made, and leaving them would make the
	// register and every report disagree about what the row cost.
	l := buildLedger(t)
	path := "/transactions/" + l.str("august_groceries")
	l.alex.put(path+"/splits", map[string]any{
		"splits": []map[string]any{{"amount": "-30.00"}, {"amount": "-20.00"}},
	}).requireStatus(http.StatusOK)

	l.alex.patch(path, map[string]any{"amount": "-60.00"}).requireStatus(http.StatusConflict)
}

func TestOneCategoryForASplitRowFilesEverySplitAndNotTheParent(t *testing.T) {
	// The categories are on the splits, and reports read only those. A
	// category left on the parent beside them would show in the register and
	// count nowhere.
	l := buildLedger(t)
	path := "/transactions/" + l.str("august_corner")
	l.alex.put(path+"/splits", map[string]any{"splits": []map[string]any{
		{"amount": "-15.00", "category_id": l.str("groceries"), "memo": "Bread"},
		{"amount": "-10.00", "category_id": l.str("food"), "memo": "Soup"},
	}}).requireStatus(http.StatusOK)

	row := l.alex.patch(path, map[string]any{"category_id": l.str("food")}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"])
	splits := row["splits"].([]any)
	require.Len(t, splits, 2, "the parts and their memos are kept")
	for _, raw := range splits {
		split := raw.(map[string]any)
		require.Equal(t, l.str("food"), split["category_id"])
		require.NotEmpty(t, split["memo"])
	}

	// Saving a split row resends its empty parent category; that changes nothing.
	row = l.alex.patch(path, map[string]any{"category_id": nil, "is_reviewed": true}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, row["category_id"])
	require.Equal(t, l.str("food"), row["splits"].([]any)[0].(map[string]any)["category_id"])
}

func TestARequiredFieldCannotBeClearedByAPatch(t *testing.T) {
	// {"name": null} is a well-formed request for something the schema cannot
	// do, and it has to come back as a refusal rather than a 500 at flush.
	l := buildLedger(t)
	response := l.alex.raw(http.MethodPatch, "/accounts/"+l.str("checking"), `{"name": null}`)
	response.requireStatus(http.StatusConflict)
	require.Contains(t, response.Body.String(), "cannot be cleared")
}

func TestTaggingARowReadsBackAsIDs(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.patch("/transactions/"+l.str("august_corner"), map[string]any{
		"tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, []any{l.str("tag")}, body["tag_ids"])
}

func TestBulkTaggingAddsToEachRowsOwnTagsAndRemoves(t *testing.T) {
	l := buildLedger(t)
	extra := spareTag(t, l, "Trip")
	first, second := l.str("august_corner"), l.str("august_groceries")
	l.alex.patch("/transactions/"+first, map[string]any{"tag_ids": []string{l.str("tag")}}).
		requireStatus(http.StatusOK)

	result := l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{first, second, first},
		"add_tag_ids":     []string{extra.ID.String()},
	}).requireStatus(http.StatusOK).json()
	require.EqualValues(t, 2, result["updated"])

	one := l.alex.get("/transactions/" + first).requireStatus(http.StatusOK).json()
	require.ElementsMatch(t, []any{l.str("tag"), extra.ID.String()}, one["tag_ids"])
	two := l.alex.get("/transactions/" + second).requireStatus(http.StatusOK).json()
	require.Equal(t, []any{extra.ID.String()}, two["tag_ids"])

	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{first, second},
		"remove_tag_ids":  []string{extra.ID.String()},
	}).requireStatus(http.StatusOK)
	one = l.alex.get("/transactions/" + first).requireStatus(http.StatusOK).json()
	require.Equal(t, []any{l.str("tag")}, one["tag_ids"])
	two = l.alex.get("/transactions/" + second).requireStatus(http.StatusOK).json()
	require.Empty(t, two["tag_ids"])
}

func TestBulkTaggingATaggedSplitRowKeepsItsSplitsUntouched(t *testing.T) {
	l := buildLedger(t)
	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-18", "amount": "-30.00",
		"splits": []map[string]any{
			{"amount": "-20.00", "category_id": l.str("groceries")},
			{"amount": "-10.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusCreated).json()
	id := row["id"].(string)

	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{id}, "add_tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + id).requireStatus(http.StatusOK).json()
	require.Equal(t, []any{l.str("tag")}, after["tag_ids"])
	splits := after["splits"].([]any)
	require.Len(t, splits, 2)
	require.Empty(t, splits[0].(map[string]any)["tag_ids"])
}

func TestBulkTaggingRefusesAnotherSpacesTagOrTransactionAndChangesNothing(t *testing.T) {
	l := buildLedger(t)
	mine := l.str("august_corner")

	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{mine}, "add_tag_ids": []string{l.str("stranger_tag")},
	}).requireStatus(http.StatusConflict)
	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{mine}, "remove_tag_ids": []string{l.str("stranger_tag")},
	}).requireStatus(http.StatusConflict)
	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{mine, l.str("stranger_txn")}, "add_tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusNotFound)

	body := l.alex.get("/transactions/" + mine).requireStatus(http.StatusOK).json()
	require.Empty(t, body["tag_ids"])
}

func TestBulkTaggingNeedsRowsAndTags(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{}, "add_tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusConflict)
	l.alex.post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{l.str("august_corner")},
	}).requireStatus(http.StatusConflict)
}

func TestAViewerCannotBulkTag(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").post("/transactions/bulk-tags", map[string]any{
		"transaction_ids": []string{l.str("august_corner")}, "add_tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusForbidden)
}

func TestDeletingATagTakesItOffTheTransactions(t *testing.T) {
	// A soft-deleted tag left linked renders as a chip nobody can remove.
	l := buildLedger(t)
	path := "/transactions/" + l.str("august_corner")
	l.alex.patch(path, map[string]any{"tag_ids": []string{l.str("tag")}}).
		requireStatus(http.StatusOK)

	l.alex.del("/tags/" + l.str("tag")).requireStatus(http.StatusNoContent)
	body := l.alex.get(path).requireStatus(http.StatusOK).json()
	require.Empty(t, body["tag_ids"])
}

// --- Filters -----------------------------------------------------------------

func TestAStoredFilterSelectsTheRowsItNames(t *testing.T) {
	l := buildLedger(t)
	page := registerPage(l, "filter_id="+l.str("filter")+"&limit=500")
	require.Equal(t, map[string]bool{l.str("august_groceries"): true}, idsIn(page))
	require.Equal(t, "-50.00", page["total"])
}

func TestAFilterFromAnotherSpaceCannotSelectTheseRows(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/transactions?filter_id=" + l.str("stranger_filter")).
		requireStatus(http.StatusConflict)
}

func TestAFilterRoundTripsItsItems(t *testing.T) {
	l := buildLedger(t)
	created := l.alex.post("/filters", map[string]any{
		"name":  "Big grocery runs",
		"scope": "watchlist",
		"items": []map[string]any{
			{"field": "category", "operator": "in", "value_ids": []string{l.str("groceries")}},
			{
				"field": "amount", "operator": "between",
				"amount_min": "40.00", "amount_max": "100.00", "position": 1,
			},
		},
	}).requireStatus(http.StatusCreated).json()

	items := created["items"].([]any)
	require.Len(t, items, 2)
	require.Equal(t, "category", items[0].(map[string]any)["field"])
	require.Equal(t, "amount", items[1].(map[string]any)["field"])
	require.Equal(t, "40.00", items[1].(map[string]any)["amount_min"])

	page := registerPage(l, "filter_id="+created["id"].(string)+"&limit=500")
	require.Equal(t, map[string]bool{l.str("august_groceries"): true}, idsIn(page))
}

// --- Categories, accounts, money on the wire ---------------------------------

func TestASystemCategoryRefusesEdits(t *testing.T) {
	// Renaming Opening Balance turns every row it holds back into spending.
	l := buildLedger(t)
	l.alex.patch("/categories/"+l.str("system_category"),
		map[string]any{"name": "Free money"}).requireStatus(http.StatusConflict)
}

func TestMoneyCrossesTheWireAsAString(t *testing.T) {
	// Ground rule 1: one coercion point on the client, and it is a string.
	l := buildLedger(t)
	body := l.alex.get("/transactions/" + l.str("august_groceries")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "-50.00", body["amount"])
	require.IsType(t, "", body["amount"])

	accounts := l.alex.get("/accounts").requireStatus(http.StatusOK).list()
	checking := findByID(t, accounts, l.str("checking"))
	require.Equal(t, "125.00", checking["balances"].(map[string]any)["balance"])
	require.IsType(t, "", checking["opening_balance"])
}

func TestAConnectedAccountsBalanceIsTheProvidersFigure(t *testing.T) {
	// Debt is stored negative, and the provider's figure wins over the ledger.
	l := buildLedger(t)
	accounts := l.alex.get("/accounts").requireStatus(http.StatusOK).list()
	card := findByID(t, accounts, l.str("card"))
	balances := card["balances"].(map[string]any)
	require.Equal(t, "-300.00", balances["balance"])
	require.NotNil(t, balances["credit_used_pct"])
}

func TestCreatingATransactionMovesTheBalanceAndStoresARunningOne(t *testing.T) {
	l := buildLedger(t)
	body := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"),
		"date":       "2026-08-28",
		"amount":     "-25.00",
		"payee":      "Hardware store",
		"tag_ids":    []string{l.str("tag")},
	}).requireStatus(http.StatusCreated).json()

	require.Equal(t, "Hardware store", body["payee"])
	// Nothing typed it, so the bank's wording is empty rather than a copy of
	// the payee — a matching path must never see a name a person chose.
	require.Equal(t, "", body["statement_name"])
	require.Equal(t, []any{l.str("tag")}, body["tag_ids"])

	require.Equal(t, "100.00", summary(l, "")["ending_balance"])

	page := registerPage(l, "account_id="+l.str("checking")+"&limit=500")
	for _, item := range page["items"].([]any) {
		require.NotNil(t, item.(map[string]any)["balance"])
	}
}

func TestANewTransactionNeedsAStringAmount(t *testing.T) {
	// A float in a money field is refused at the door, not rounded later.
	l := buildLedger(t)
	response := l.alex.raw(http.MethodPost, "/transactions", fmt.Sprintf(
		`{"account_id": %q, "date": "2026-08-28", "amount": -25.5}`, l.str("checking")))
	response.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, response.Body.String(), "string")
}

func TestAnUnknownRowIsA404NotA500(t *testing.T) {
	l := buildLedger(t)
	missing := uuid.NewString()
	l.alex.get("/transactions/" + missing).requireStatus(http.StatusNotFound)
	l.alex.get("/accounts/" + missing).requireStatus(http.StatusNotFound)
	l.alex.get("/categories/" + missing).requireStatus(http.StatusNotFound)
}

func itemField(page map[string]any, key string) []string {
	items := page["items"].([]any)
	out := make([]string, 0, len(items))
	for _, item := range items {
		out = append(out, item.(map[string]any)[key].(string))
	}
	return out
}

func findByID(t *testing.T, rows []map[string]any, id string) map[string]any {
	t.Helper()
	for _, row := range rows {
		if row["id"] == id {
			return row
		}
	}
	t.Fatalf("no row with id %s", id)
	return nil
}

// --- No accounts is not every account ----------------------------------------

func TestAnEmptyAccountSelectionSelectsNoAccounts(t *testing.T) {
	// The picker with every box unticked. Answering it with the whole ledger
	// puts a total on screen above a list that says nothing is selected.
	l := buildLedger(t)
	none := registerPage(l, "account_id=&limit=500")

	require.Equal(t, float64(0), none["count"])
	require.Equal(t, "0.00", none["total"])
	require.Empty(t, none["items"])

	all := registerPage(l, "limit=500")
	require.Greater(t, all["count"], float64(0))
}

// A client that navigates away mid-query is not a fault.
//
// The register cancels its in-flight read on every click, and the store
// returns context.Canceled. Mapped to a 500, it would produce an "unhandled
// request error" in the log for the most ordinary thing a user does — a
// fault somebody would one day spend an afternoon chasing.
func TestAClientThatHangsUpIsNotLoggedAsAServerFault(t *testing.T) {
	l := buildLedger(t)

	ctx, cancel := context.WithCancel(t.Context())
	request := httptest.NewRequestWithContext(ctx, http.MethodGet, "/transactions", nil)
	request.Header.Set("Authorization", "Bearer "+l.alex.token)
	request.Header.Set("X-Space-Id", l.alex.spaceID)
	cancel()

	recorder := httptest.NewRecorder()
	RouterFor(l.env).ServeHTTP(recorder, request)

	require.Equal(t, clientClosedRequest, recorder.Code)
	require.Empty(t, recorder.Body.String(), "there is nobody left to read a body")
}

// --- Moving a row between accounts -------------------------------------------

// cycleCard is a card with a configured statement cycle: charges close on the
// 20th and the bill falls due on the 15th, so an August 5th charge is cash flow
// on September 15th.
func cycleCard(l *ledger) string {
	l.t.Helper()
	closeDay := int16(20)
	card := &store.Account{
		Name: "Cycle Card", Kind: domain.KindCreditCard, Type: "credit_card",
		Currency: "USD", IncludeInNetWorth: true,
		StatementCloseDay: &closeDay,
		DueDate:           domain.NewDate(2026, time.September, 15),
	}
	require.NoError(l.t, l.env.DB.CreateAccount(
		l.t.Context(), store.SpaceIDOf(l.id("space")), card))
	return card.ID.String()
}

func TestMovingARowToACardRederivesItsEffectiveDate(t *testing.T) {
	// Trap 4: effective_date is the reporting date. Carried over from the
	// account the row left, it files the charge against a statement cycle the
	// row no longer belongs to.
	l := buildLedger(t)
	card := cycleCard(l)

	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-05", "amount": "-60.00",
	}).requireStatus(http.StatusCreated).json()
	require.Nil(t, row["effective_date"])

	moved := l.alex.patch("/transactions/"+row["id"].(string),
		map[string]any{"account_id": card}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-09-15", moved["effective_date"])
}

func TestMovingARowOffACardDropsTheCardsEffectiveDate(t *testing.T) {
	l := buildLedger(t)
	card := cycleCard(l)

	row := l.alex.post("/transactions", map[string]any{
		"account_id": card, "date": "2026-08-05", "amount": "-60.00",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "2026-09-15", row["effective_date"])

	moved := l.alex.patch("/transactions/"+row["id"].(string),
		map[string]any{"account_id": l.str("checking")}).requireStatus(http.StatusOK).json()
	require.Nil(t, moved["effective_date"],
		"a checking row has no statement cycle, so it reports on its own date")
}

func TestAHandCorrectedEffectiveDateSurvivesAnAccountMove(t *testing.T) {
	// The stored date is the only record that the correction happened.
	l := buildLedger(t)
	card := cycleCard(l)

	row := l.alex.post("/transactions", map[string]any{
		"account_id": card, "date": "2026-08-05", "amount": "-60.00",
		"effective_date": "2026-10-01",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "2026-10-01", row["effective_date"])

	moved := l.alex.patch("/transactions/"+row["id"].(string),
		map[string]any{"account_id": l.str("checking")}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-10-01", moved["effective_date"])
}

func TestAnExplicitEffectiveDateWinsOverTheMove(t *testing.T) {
	l := buildLedger(t)
	card := cycleCard(l)

	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-05", "amount": "-60.00",
	}).requireStatus(http.StatusCreated).json()

	moved := l.alex.patch("/transactions/"+row["id"].(string), map[string]any{
		"account_id": card, "effective_date": "2026-11-02",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "2026-11-02", moved["effective_date"])
}

func TestMovingARowTakesTheNewAccountsCurrency(t *testing.T) {
	// The old account's currency is not a fact about the row once it has left.
	// Left alone, a EUR figure in a USD account is summed at face value.
	l := buildLedger(t)
	euro := &store.Account{
		Name: "Berlin Current", Kind: domain.KindCash, Type: "checking",
		Currency: "EUR", IncludeInNetWorth: true,
	}
	require.NoError(t, l.env.DB.CreateAccount(
		t.Context(), store.SpaceIDOf(l.id("space")), euro))

	row := l.alex.post("/transactions", map[string]any{
		"account_id": euro.ID.String(), "date": "2026-08-05", "amount": "-40.00",
	}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "EUR", row["currency"])

	moved := l.alex.patch("/transactions/"+row["id"].(string),
		map[string]any{"account_id": l.str("checking")}).requireStatus(http.StatusOK).json()
	require.Equal(t, "USD", moved["currency"])
}

func TestACurrencyTheClientSentSurvivesAnAccountMove(t *testing.T) {
	l := buildLedger(t)
	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-05", "amount": "-40.00",
	}).requireStatus(http.StatusCreated).json()

	moved := l.alex.patch("/transactions/"+row["id"].(string), map[string]any{
		"account_id": l.str("card"), "currency": "EUR",
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, "EUR", moved["currency"])
}

func TestAPairedLegCannotBeMovedOrRepriced(t *testing.T) {
	// Both legs in one account is not a transfer, and two legs that disagree
	// about how much moved is not one either. Nothing re-checks a pair once the
	// token is written, so the edit is refused rather than repaired.
	l := buildLedger(t)

	l.alex.patch("/transactions/"+l.str("transfer_out"),
		map[string]any{"account_id": l.str("card")}).requireStatus(http.StatusConflict)
	l.alex.patch("/transactions/"+l.str("transfer_out"),
		map[string]any{"amount": "-150.00"}).requireStatus(http.StatusConflict)

	// The pairing is untouched, and an edit that is not the account or the
	// amount still goes through.
	l.alex.patch("/transactions/"+l.str("transfer_out"),
		map[string]any{"payee": "Moved to the card"}).requireStatus(http.StatusOK)
	leg := l.alex.get("/transactions/" + l.str("transfer_out")).
		requireStatus(http.StatusOK).json()
	require.Equal(t, l.str("checking"), leg["account_id"])
	require.Equal(t, "-200.00", leg["amount"])
	require.Equal(t, l.str("transfer_pair"), leg["transfer_pair_id"])
}

// --- Narrow writes on the hot paths ------------------------------------------

func TestMarkingReviewedKeepsSplitsAndTags(t *testing.T) {
	// The review flag is one column. Rewriting the whole row to set it deletes
	// and reinserts the splits, the split tags and the tags every time.
	l := buildLedger(t)
	row := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-18", "amount": "-30.00",
		"tag_ids": []string{l.str("tag")},
		"splits": []map[string]any{
			{"amount": "-20.00", "category_id": l.str("groceries"),
				"tag_ids": []string{l.str("tag")}},
			{"amount": "-10.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusCreated).json()

	l.alex.patch("/transactions/"+row["id"].(string),
		map[string]any{"is_reviewed": true}).requireStatus(http.StatusOK)
	l.alex.post("/transactions/mark-reviewed?"+august, map[string]any{"is_reviewed": true}).
		requireStatus(http.StatusOK)

	after := l.alex.get("/transactions/" + row["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, after["is_reviewed"])
	require.Equal(t, []any{l.str("tag")}, after["tag_ids"])
	require.Len(t, after["splits"], 2)
	splits := after["splits"].([]any)
	require.Equal(t, "-20.00", splits[0].(map[string]any)["amount"])
	require.Equal(t, []any{l.str("tag")}, splits[0].(map[string]any)["tag_ids"])
}

func TestABackDatedInsertRewritesBalancesWithoutTouchingAllocations(t *testing.T) {
	// Every later row's balance moves. The batched write that moves them is
	// store.SetTransactionBalances, and this is the end-to-end check that it
	// leaves everything else on the row where it was.
	l := buildLedger(t)
	tagged := l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-08-18", "amount": "-30.00",
		"tag_ids": []string{l.str("tag")},
		"splits": []map[string]any{
			{"amount": "-30.00", "category_id": l.str("groceries"),
				"tag_ids": []string{l.str("tag")}},
		},
	}).requireStatus(http.StatusCreated).json()
	before := tagged["balance"].(string)

	l.alex.post("/transactions", map[string]any{
		"account_id": l.str("checking"), "date": "2026-02-01", "amount": "-40.00",
	}).requireStatus(http.StatusCreated)

	after := l.alex.get("/transactions/" + tagged["id"].(string)).
		requireStatus(http.StatusOK).json()
	require.Equal(t, domain.MustFromString(before).Add(domain.MustFromString("-40.00")).String(),
		after["balance"])
	require.Equal(t, []any{l.str("tag")}, after["tag_ids"])
	require.Len(t, after["splits"], 1)
	require.Equal(t, []any{l.str("tag")}, after["splits"].([]any)[0].(map[string]any)["tag_ids"])
}
