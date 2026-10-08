package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// TestFilterRoundTrip checks that a saved filter comes back with every clause
// intact. A filter that loses an item does not become a narrower filter — it
// becomes a report over the whole ledger.
func TestFilterRoundTrip(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)
	dining := newCategory(t, spaceID, "Dining", domain.CategoryExpense)
	unreviewed := false

	filter := &Filter{
		Name:      "Big food",
		Scope:     "saved_search",
		QueryText: "market",
		Items: []FilterItem{
			{
				Field:      "category",
				Operator:   "in",
				GroupIndex: 0,
				ValueIDs:   []uuid.UUID{groceries.ID, dining.ID},
			},
			{
				Field:        "amount",
				Operator:     "between",
				GroupIndex:   1,
				AmountMin:    domain.MustFromString("-250.00"),
				HasAmountMin: true,
				AmountMax:    domain.MustFromString("-50.00"),
				HasAmountMax: true,
			},
			{
				Field:      "date",
				Operator:   "between",
				GroupIndex: 2,
				DateFrom:   domain.NewDate(2026, 1, 1),
				DateTo:     domain.NewDate(2026, 12, 31),
				DatePreset: "this_year",
			},
			{
				Field:      "reviewed",
				Operator:   "is",
				GroupIndex: 3,
				State:      &unreviewed,
				Negated:    true,
				ValueTexts: []string{"anything"},
			},
		},
	}
	require.NoError(t, db(t).CreateFilter(ctx, spaceID, filter))

	read, err := db(t).GetFilter(ctx, spaceID, filter.ID)
	require.NoError(t, err)
	require.Equal(t, "Big food", read.Name)
	require.Equal(t, "market", read.QueryText)
	require.Len(t, read.Items, 4)

	require.ElementsMatch(t, []uuid.UUID{groceries.ID, dining.ID}, read.Items[0].ValueIDs)
	require.Equal(t, "-250.00", read.Items[1].AmountMin.String())
	require.Equal(t, "-50.00", read.Items[1].AmountMax.String())
	require.True(t, read.Items[1].HasAmountMax)
	require.Equal(t, domain.NewDate(2026, 1, 1), read.Items[2].DateFrom)
	require.Equal(t, "this_year", read.Items[2].DatePreset)

	// Tri-state: "matches unreviewed" and "no opinion about reviewed" are
	// different filters and a bool would collapse them.
	require.NotNil(t, read.Items[3].State)
	require.False(t, *read.Items[3].State)
	require.True(t, read.Items[3].Negated)
	require.Equal(t, []string{"anything"}, read.Items[3].ValueTexts)

	// An item with no amount bound reads back as absent, not as zero.
	require.False(t, read.Items[0].HasAmountMin)
	require.True(t, read.Items[0].AmountMin.IsZero())
}

// TestReplaceFilterItems checks that editing a filter's clauses does not leave
// the old ones behind, which would silently AND two contradictory groups.
func TestReplaceFilterItems(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)

	filter := &Filter{
		Name:  "Rewritten",
		Scope: "watchlist",
		Items: []FilterItem{{Field: "payee", Operator: "contains", Text: "coffee"}},
	}
	require.NoError(t, db(t).CreateFilter(ctx, spaceID, filter))

	filter.Items = []FilterItem{
		{Field: "payee", Operator: "contains", Text: "tea"},
		{Field: "account", Operator: "in", GroupIndex: 1, Position: 0},
	}
	require.NoError(t, db(t).ReplaceFilterItems(ctx, spaceID, filter))

	read, err := db(t).GetFilter(ctx, spaceID, filter.ID)
	require.NoError(t, err)
	require.Len(t, read.Items, 2)
	require.Equal(t, "tea", read.Items[0].Text)
}

// TestListFiltersAttachesItems checks that the listing does not return
// filters with empty item lists — a filter panel that renders those shows
// every saved search as "matches everything".
func TestListFiltersAttachesItems(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)

	for _, name := range []string{"one", "two"} {
		filter := &Filter{
			Name:  name,
			Scope: "saved_search",
			Items: []FilterItem{{Field: "payee", Operator: "contains", Text: name}},
		}
		require.NoError(t, db(t).CreateFilter(ctx, spaceID, filter))
	}

	filters, err := db(t).ListFilters(ctx, spaceID, false)
	require.NoError(t, err)
	require.Len(t, filters, 2)
	for _, filter := range filters {
		require.Len(t, filter.Items, 1)
		require.Equal(t, filter.Name, filter.Items[0].Text)
	}
}

func TestOnlyAnOldUnreferencedAdHocFilterIsPruned(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	create := func(scope string) uuid.UUID {
		t.Helper()
		filter := &Filter{Scope: scope, Items: []FilterItem{
			{Field: "payee", Operator: "in", ValueTexts: []string{"Corner Store"}},
		}}
		require.NoError(t, db(t).CreateFilter(ctx, spaceID, filter))
		return filter.ID
	}
	age := func(id uuid.UUID) {
		t.Helper()
		_, err := db(t).db.Exec(ctx,
			`UPDATE filters SET created_at = ts_add(now(), '-2 days'),
			 updated_at = ts_add(now(), '-2 days') WHERE id = $1`, id)
		require.NoError(t, err)
	}

	stale, fresh, watched, saved := create("ad_hoc"), create("ad_hoc"), create("ad_hoc"), create("saved_view")
	for _, id := range []uuid.UUID{stale, watched, saved} {
		age(id)
	}
	watchlist := &Watchlist{FilterID: watched, Name: "Corner", Period: "month"}
	require.NoError(t, db(t).CreateWatchlist(ctx, spaceID, watchlist))
	require.NoError(t, db(t).DeleteWatchlist(ctx, spaceID, watchlist.ID))

	pruned, err := db(t).PruneAdHocFilters(ctx, time.Now().Add(-24*time.Hour))
	require.NoError(t, err)
	require.GreaterOrEqual(t, pruned, int64(1))

	_, err = db(t).GetFilter(ctx, spaceID, stale)
	require.ErrorIs(t, err, ErrNotFound)
	for _, kept := range []uuid.UUID{fresh, watched, saved} {
		_, err := db(t).GetFilter(ctx, spaceID, kept)
		require.NoError(t, err)
	}
}
