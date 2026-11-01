package store

import (
	"testing"

	. "github.com/go-jet/jet/v2/sqlite"
	"github.com/stretchr/testify/require"

	tb "github.com/CornHead764/agentifi/backend/internal/gen/jet/household/table"
)

func TestPagedOffsetWithoutLimit(t *testing.T) {
	space := newSpace(t)
	household := householdDB(t, space)
	for _, name := range []string{"Alpha", "Bravo", "Charlie", "Delta"} {
		require.NoError(t, db(t).CreateTag(t.Context(), space, &Tag{Name: name}))
	}
	// A go-jet statement is changed in place by LIMIT and OFFSET.
	names := func() SelectStatement { return SELECT(tb.Tags.Name).FROM(tb.Tags).ORDER_BY(tb.Tags.Name) }
	page := func(limit, offset int64) []string {
		t.Helper()
		var got []string
		query, args := paged(names(), limit, offset).Sql()
		rows, err := household.Read().QueryContext(t.Context(), query, args...)
		require.NoError(t, err)
		defer rows.Close()
		for rows.Next() {
			var name string
			require.NoError(t, rows.Scan(&name))
			got = append(got, name)
		}
		require.NoError(t, rows.Err())
		return got
	}
	require.Equal(t, []string{"Alpha", "Bravo", "Charlie", "Delta"}, page(0, 0))
	require.Equal(t, []string{"Alpha", "Bravo"}, page(2, 0))
	require.Equal(t, []string{"Charlie", "Delta"}, page(0, 2))
	require.Equal(t, []string{"Bravo", "Charlie"}, page(2, 1))
	require.Equal(t, []string{"Bravo", "Charlie", "Delta"}, page(-1, 1))

	// go-jet's own OFFSET, with no LIMIT, is SQL SQLite refuses. When this
	// starts passing, paged can go.
	query, args := names().OFFSET(1).Sql()
	_, err := household.Read().QueryContext(t.Context(), query, args...)
	require.ErrorContains(t, err, "syntax error")
}
