package store

import (
	"testing"

	. "github.com/go-jet/jet/v2/sqlite"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/gen/jet/household/model"
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

// The same stored units are a different amount in each currency, so a row's
// money is only right when it is read in the currency that row names.
func TestRowMoneyCurrency(t *testing.T) {
	space := newSpace(t)
	household := householdDB(t, space)
	dinar, yen := uuid.New(), uuid.New()
	for account, currency := range map[uuid.UUID]string{dinar: "KWD", yen: "JPY"} {
		_, err := household.Write().ExecContext(t.Context(), `INSERT INTO accounts (id, name, kind, type, currency, sort_order, created_at, updated_at)
			VALUES (?, ?, 'checking', 'checking', ?, 0, 1, 1)`, account, currency, currency)
		require.NoError(t, err)
		_, err = household.Write().ExecContext(t.Context(), `INSERT INTO transactions (id, account_id, date, amount, balance, currency, source, created_at, updated_at)
			VALUES (?, ?, '2026-01-01', -1234, CASE WHEN ? = 'JPY' THEN 5000 END, ?, 'manual', 1, 1)`, uuid.New(), account, currency, currency)
		require.NoError(t, err)
	}
	tx := tb.Transactions
	var rows []model.Transactions
	require.NoError(t, SELECT(tx.Amount, tx.Balance, tx.Currency).FROM(tx).ORDER_BY(tx.Currency).
		QueryContext(t.Context(), household.Read(), &rows))
	require.Len(t, rows, 2)

	var m rowMoney
	type read struct {
		amount, balance string
		hasBalance      bool
	}
	var got []read
	for _, row := range rows {
		balance, has := m.readNull(row.Balance, row.Currency)
		got = append(got, read{m.read(row.Amount, row.Currency).Decimal().String(), balance.Decimal().String(), has})
	}
	require.NoError(t, m.err)
	require.Equal(t, []read{{"-1234", "5000", true}, {"-1.234", "0", false}}, got)

	m.read(rows[0].Amount, "")
	m.read(rows[0].Amount, "KWD")
	m.read(rows[0].Amount, "NOT")
	require.ErrorContains(t, m.err, `""`)
}
