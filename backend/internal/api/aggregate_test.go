package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// GET /transactions/aggregate, against the seeded ledger.
//
// The assertions worth writing are the ones a total computed from a partial
// page would pass: the total describes the whole match set rather than a page
// of it, and every exclusion rule holds server-side.

func aggregateOf(l *ledger, query string) map[string]any {
	l.t.Helper()
	return l.alex.get("/transactions/aggregate?" + query).requireStatus(http.StatusOK).json()
}

// bucketsOf is the response's buckets as label → total, which is what every
// assertion here is actually about.
func bucketsOf(result map[string]any) map[string]string {
	out := map[string]string{}
	for _, entry := range result["buckets"].([]any) {
		bucket := entry.(map[string]any)
		out[bucket["label"].(string)] = bucket["total"].(string)
	}
	return out
}

func TestTheAggregateDescribesTheWholeMatchSetNotAPage(t *testing.T) {
	// The whole point of the endpoint. One row's worth of limit, and the
	// figure is still August's complete spending.
	l := buildLedger(t)
	result := aggregateOf(l, august+"&date_field=effective&direction=spending&limit=1")
	require.Equal(t, "-75.00", result["total"])
	require.EqualValues(t, 2, result["count"])
}

func TestTheAggregateRanksSpendingByCategoryWithSubcategoriesRolledUp(t *testing.T) {
	l := buildLedger(t)
	result := aggregateOf(l, august+"&date_field=effective&direction=spending&group_by=category")
	// Groceries is a child of Food & Dining, and the legend names the parent.
	require.Equal(t, map[string]string{
		"Food & Dining": "-50.00",
		"Uncategorized": "-25.00",
	}, bucketsOf(result))
}

func TestTheAggregateDrilledIntoAParentListsItsChildren(t *testing.T) {
	l := buildLedger(t)
	result := aggregateOf(l, august+"&date_field=effective&direction=spending&group_by=category&under="+
		l.str("food"))
	require.Equal(t, map[string]string{
		"Groceries":     "-50.00",
		"Uncategorized": "-25.00",
	}, bucketsOf(result))
}

func TestAFilteredAggregateCountsOnlyTheMatchingSplitsLikeTheRegister(t *testing.T) {
	// A $25 row split $10 groceries / $15 dining, under the groceries filter:
	// the tab, like the register's chip, counts the $10 and not the $15.
	l := buildLedger(t)
	l.alex.put("/transactions/"+l.str("august_corner")+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-10.00", "category_id": l.str("groceries")},
			{"amount": "-15.00", "category_id": l.str("food")},
		},
	}).requireStatus(http.StatusOK)

	query := august + "&filter_id=" + l.str("filter")
	result := aggregateOf(l, query+"&direction=spending&group_by=category")
	require.Equal(t, "-60.00", result["total"])
	require.EqualValues(t, 2, result["count"])
	require.Equal(t, map[string]string{"Food & Dining": "-60.00"}, bucketsOf(result))

	require.Equal(t, result["total"], registerPage(l, query)["total"],
		"one filter, one number: the tab and the register's chip")
}

func TestTheAggregateLeavesTransfersOut(t *testing.T) {
	// Both legs, not just the paying one. $200 moved from checking to the card
	// on 10 August and nobody spent anything.
	l := buildLedger(t)
	spending := aggregateOf(l, august+"&date_field=effective&direction=spending")
	require.Equal(t, "-75.00", spending["total"])

	income := aggregateOf(l, august+"&date_field=effective&direction=income")
	require.Equal(t, "0.00", income["total"])
	require.Empty(t, income["buckets"])
}

func TestTheAggregateFilesACardChargeUnderItsEffectiveMonth(t *testing.T) {
	// Trap 4. The seeded charge was swiped on 25 August against a statement
	// due on 10 September, so it is September's spending — and reading the
	// posted date would shift a whole month of card spending.
	l := buildLedger(t)
	effective := aggregateOf(l, "from=2026-08-01&to=2026-09-30&date_field=effective&direction=spending")
	require.Equal(t, []string{"2026-08", "2026-09"}, monthsOf(effective))

	posted := aggregateOf(l, "from=2026-08-01&to=2026-09-30&date_field=posted&direction=spending")
	require.Equal(t, []string{"2026-08"}, monthsOf(posted))
	require.Equal(t, "-150.00", posted["total"])
}

func TestTheAggregateTakesTheRegistersOwnAccountFilter(t *testing.T) {
	// Same parser as the list, so the chart and the activity table under it
	// cannot describe different sets of rows.
	l := buildLedger(t)
	checking := aggregateOf(l,
		august+"&date_field=effective&direction=spending&account_id="+l.str("checking"))
	require.Equal(t, "-75.00", checking["total"])

	card := aggregateOf(l,
		august+"&date_field=posted&direction=spending&account_id="+l.str("card"))
	require.Equal(t, "-75.00", card["total"])
	require.Equal(t, map[string]string{"Uncategorized": "-75.00"}, bucketsOf(card))
}

func TestTheAggregateGroupsByPayeeAndByTag(t *testing.T) {
	l := buildLedger(t)
	l.alex.patch("/transactions/"+l.str("august_corner"), map[string]any{
		"tag_ids": []string{l.str("tag")},
	}).requireStatus(http.StatusOK)

	byPayee := aggregateOf(l, august+"&date_field=effective&direction=spending&group_by=payee")
	require.Equal(t, map[string]string{
		"Safeway":      "-50.00",
		"Corner Store": "-25.00",
	}, bucketsOf(byPayee))

	byTag := aggregateOf(l, august+"&date_field=effective&direction=spending&group_by=tag")
	require.Equal(t, map[string]string{
		"No tag":       "-50.00",
		"reimbursable": "-25.00",
	}, bucketsOf(byTag))
}

func TestTheAggregateFilesEachSplitUnderItsOwnCategory(t *testing.T) {
	// A $50 receipt split $20 groceries / $30 unfiled is $20 under Food &
	// Dining, not $50 under whichever split happened to come first.
	l := buildLedger(t)
	l.alex.put("/transactions/"+l.str("august_groceries")+"/splits", map[string]any{
		"splits": []map[string]any{
			{"amount": "-20.00", "category_id": l.str("groceries")},
			{"amount": "-30.00", "category_id": nil},
		},
	}).requireStatus(http.StatusOK)

	result := aggregateOf(l, august+"&date_field=effective&direction=spending")
	require.Equal(t, "-75.00", result["total"])
	require.EqualValues(t, 3, result["count"])
	require.Equal(t, map[string]string{
		"Food & Dining": "-20.00",
		"Uncategorized": "-55.00",
	}, bucketsOf(result))
}

func TestTheAggregateDropsARowExcludedFromReports(t *testing.T) {
	// It still renders, dimmed, in the activity table below the chart. It is
	// absent from every figure above it.
	l := buildLedger(t)
	l.alex.patch("/transactions/"+l.str("august_groceries"), map[string]any{
		"excluded_from_reports": true,
	}).requireStatus(http.StatusOK)

	result := aggregateOf(l, august+"&date_field=effective&direction=spending")
	require.Equal(t, "-25.00", result["total"])
	require.Equal(t, map[string]string{"Uncategorized": "-25.00"}, bucketsOf(result))
}

func TestTheAggregateRefusesAKnobItDoesNotHave(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/transactions/aggregate?direction=sideways").requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/transactions/aggregate?group_by=account").requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/transactions/aggregate?under=food").requireStatus(http.StatusUnprocessableEntity)
}

func TestTheAggregateIsScopedToTheCallersSpace(t *testing.T) {
	l := buildLedger(t)
	other := l.as("alex")
	other.spaceID = l.str("other_space")
	other.get("/transactions/aggregate").requireStatus(http.StatusNotFound)

	// And a viewer may run it: it is a read.
	l.as("vera").get("/transactions/aggregate").requireStatus(http.StatusOK)
}

func monthsOf(result map[string]any) []string {
	out := []string{}
	for _, entry := range result["months"].([]any) {
		out = append(out, entry.(map[string]any)["month"].(string))
	}
	return out
}
