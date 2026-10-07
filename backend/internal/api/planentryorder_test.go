package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The order a bucket's rows are read in.
//
// Bills is mostly occurrences, which the plan materializes by due date, and
// Income is mostly posted rows, which arrive in whatever order the ledger query
// returns them, so neither is ordered by its source. One rule orders every
// bucket, and this is what pins it.

func orderEntry(id, name, date string) PlanEntry {
	on, err := domain.ParseDate(date)
	if err != nil {
		panic(err)
	}
	return PlanEntry{ID: id, Name: name, DueOn: Date(on)}
}

func entryIDs(entries []PlanEntry) []string {
	out := make([]string, 0, len(entries))
	for _, one := range entries {
		out = append(out, one.ID)
	}
	return out
}

func TestPlanEntriesReadEarliestFirst(t *testing.T) {
	entries := []PlanEntry{
		orderEntry("c", "Second paycheck", "2026-09-25"),
		orderEntry("a", "Rent", "2026-09-01"),
		orderEntry("b", "First paycheck", "2026-09-10"),
	}
	sortPlanEntries(entries)
	require.Equal(t, []string{"a", "b", "c"}, entryIDs(entries))
}

// Income and bills sit in two sections on one screen, so the same call orders
// both and the two cannot disagree.
func TestIncomeAndBillsAreOrderedTheSameWay(t *testing.T) {
	income := []PlanEntry{
		orderEntry("pay-2", "Paycheck", "2026-09-30"),
		orderEntry("pay-1", "Paycheck", "2026-09-15"),
	}
	bills := []PlanEntry{
		orderEntry("card", "Rewards Card", "2026-09-20"),
		orderEntry("rent", "Rent", "2026-09-01"),
	}
	sortPlanEntries(income)
	sortPlanEntries(bills)

	require.Equal(t, []string{"pay-1", "pay-2"}, entryIDs(income))
	require.Equal(t, []string{"rent", "card"}, entryIDs(bills))
	for _, list := range [][]PlanEntry{income, bills} {
		for i := 1; i < len(list); i++ {
			require.True(t,
				domain.Date(list[i-1].DueOn).NotAfter(domain.Date(list[i].DueOn)),
				"every bucket reads by the date money moves, ascending")
		}
	}
}

// Two rows on one day must not swap places between two requests for the same
// month, which is what a sort with no tie-break does.
func TestRowsOnTheSameDayKeepAStableOrder(t *testing.T) {
	build := func() []PlanEntry {
		return []PlanEntry{
			orderEntry("z", "Water", "2026-09-05"),
			orderEntry("b", "Electric", "2026-09-05"),
			orderEntry("a", "Water", "2026-09-05"),
		}
	}
	first, second := build(), build()
	sortPlanEntries(first)
	sortPlanEntries(second)
	require.Equal(t, []string{"b", "a", "z"}, entryIDs(first),
		"name then id, so the order is a function of the rows and nothing else")
	require.Equal(t, entryIDs(first), entryIDs(second))
}

// The date on the row is already the right one — an occurrence's due date, and a
// posted row's effective date, which is the date the plan files it under.
func TestTheOrderReadsTheDateThePlanFilesTheRowUnder(t *testing.T) {
	posted := postingEntry
	require.NotNil(t, posted, "postingEntry is what sets DueOn for a posted row")

	// A card charge posted in August but effective in September sorts into
	// September, which is the month whose plan counts it (trap 4).
	august := domain.NewDate(2026, time.August, 28)
	september := domain.NewDate(2026, time.September, 3)
	txn := domain.Transaction{Date: august, EffectiveDate: september}
	require.Equal(t, september, txn.ReportingDate(domain.DateEffective))

	entries := []PlanEntry{
		orderEntry("later", "Card", september.String()),
		orderEntry("earlier", "Cash", august.String()),
	}
	sortPlanEntries(entries)
	require.Equal(t, []string{"earlier", "later"}, entryIDs(entries))
}
