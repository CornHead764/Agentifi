package moneycolumns_test

import (
	"database/sql"
	"maps"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/moneycolumns"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// currentSchema reads table → column → declared type from both schemas,
// migrated to the latest version and then changed by householdSQL.
func currentSchema(t *testing.T, householdSQL ...string) map[string]map[string]string {
	t.Helper()
	dir := t.TempDir()
	household, err := sqlitedb.OpenNewHousehold(t.Context(), filepath.Join(dir, "household.db"), time.Now)
	require.NoError(t, err)
	for _, statement := range householdSQL {
		_, err := household.Write().ExecContext(t.Context(), statement)
		require.NoError(t, err)
	}
	require.NoError(t, household.Close())
	server, err := sqlitedb.Open(t.Context(), filepath.Join(dir, "server.db"), time.Now)
	require.NoError(t, err)
	_, err = server.MigrateServer(t.Context(), "")
	require.NoError(t, err)
	require.NoError(t, server.Close())

	tables := map[string]map[string]string{}
	for _, name := range []string{"household.db", "server.db"} {
		db, err := sql.Open("sqlite", "file:"+filepath.Join(dir, name)+"?mode=ro")
		require.NoError(t, err)
		rows, err := db.Query(`SELECT m.name, p.name, p.type FROM sqlite_schema m, pragma_table_info(m.name) p WHERE m.type = 'table'`)
		require.NoError(t, err)
		for rows.Next() {
			var table, column, typ string
			require.NoError(t, rows.Scan(&table, &column, &typ))
			if tables[table] == nil {
				tables[table] = map[string]string{}
			}
			tables[table][column] = typ
		}
		require.NoError(t, rows.Err())
		require.NoError(t, db.Close())
	}
	return tables
}

func TestEveryColumnIsAnIntegerWithACurrency(t *testing.T) {
	tables := currentSchema(t)
	for key, d := range moneycolumns.Columns {
		table, column, _ := strings.Cut(key, ".")
		require.Equal(t, "INTEGER", tables[table][column], "%s is not an INTEGER column of the current schema", key)
		set := 0
		for _, ok := range []bool{d.Own, d.Primary, d.Fixed != "", d.Parent != ""} {
			if ok {
				set++
			}
		}
		require.Equal(t, 1, set, "%s: %+v", key, d)
		switch {
		case d.Own:
			require.Contains(t, tables[table], "currency", "%s is in its row's currency", key)
		case d.Fixed != "":
			_, err := minorunits.Decimals(d.Fixed)
			require.NoError(t, err, key)
		case d.Parent != "":
			require.Contains(t, tables[table], d.Via, key)
			require.Contains(t, tables[d.Parent], "currency", key)
		}
		if d.Parent == "" {
			require.Empty(t, d.Via, key)
		}
	}
}

// unclassified lists the INTEGER columns in neither money nor notMoney, the
// columns listed more than once, and the notMoney entries naming no INTEGER column.
func unclassified(tables map[string]map[string]string, money map[string]moneycolumns.Denomination, notMoney map[string][]string) (neither, twice, stale []string) {
	listed := map[string]int{}
	for column := range money {
		listed[column]++
	}
	for _, columns := range notMoney {
		for _, column := range columns {
			listed[column]++
			table, name, _ := strings.Cut(column, ".")
			if tables[table][name] != "INTEGER" {
				stale = append(stale, column)
			}
		}
	}
	for table, columns := range tables {
		for name, typ := range columns {
			if typ == "INTEGER" && listed[table+"."+name] == 0 {
				neither = append(neither, table+"."+name)
			}
		}
	}
	for column, n := range listed {
		if n > 1 {
			twice = append(twice, column)
		}
	}
	slices.Sort(neither)
	slices.Sort(twice)
	slices.Sort(stale)
	return neither, twice, stale
}

func TestEveryIntegerColumnIsClassified(t *testing.T) {
	neither, twice, stale := unclassified(currentSchema(t), moneycolumns.Columns, moneycolumns.NotMoney)
	require.Empty(t, neither, "INTEGER columns in neither moneycolumns.Columns nor NotMoney: a money column goes in Columns with its currency, any other in NotMoney under what it holds")
	require.Empty(t, twice, "columns listed more than once in moneycolumns.Columns and NotMoney")
	require.Empty(t, stale, "moneycolumns.NotMoney entries that are not INTEGER columns of the current schema")
}

// Each case is one mistake in a migration, Columns or NotMoney, and the column
// the check must name for it.
func TestAnUnclassifiedColumnIsCaught(t *testing.T) {
	withoutGoalBalance := maps.Clone(moneycolumns.Columns)
	delete(withoutGoalBalance, "accounts.goal_balance")
	goalBalanceTwice := maps.Clone(moneycolumns.NotMoney)
	goalBalanceTwice["a count"] = append(slices.Clone(goalBalanceTwice["a count"]), "accounts.goal_balance")
	droppedColumn := maps.Clone(moneycolumns.NotMoney)
	droppedColumn["a count"] = append(slices.Clone(droppedColumn["a count"]), "accounts.review_count")

	for _, tc := range []struct {
		name         string
		householdSQL []string
		money        map[string]moneycolumns.Denomination
		notMoney     map[string][]string
		neither      []string
		twice        []string
		stale        []string
	}{{
		name:         "a new money column with no entry",
		householdSQL: []string{"ALTER TABLE accounts ADD COLUMN review_bonus_amount INTEGER NOT NULL DEFAULT 0"},
		money:        moneycolumns.Columns,
		notMoney:     moneycolumns.NotMoney,
		neither:      []string{"accounts.review_bonus_amount"},
	}, {
		name:     "a money column deleted from Columns",
		money:    withoutGoalBalance,
		notMoney: moneycolumns.NotMoney,
		neither:  []string{"accounts.goal_balance"},
	}, {
		name:     "a money column also in NotMoney",
		money:    moneycolumns.Columns,
		notMoney: goalBalanceTwice,
		twice:    []string{"accounts.goal_balance"},
	}, {
		name:     "a NotMoney entry for no column",
		money:    moneycolumns.Columns,
		notMoney: droppedColumn,
		stale:    []string{"accounts.review_count"},
	}} {
		t.Run(tc.name, func(t *testing.T) {
			neither, twice, stale := unclassified(currentSchema(t, tc.householdSQL...), tc.money, tc.notMoney)
			require.Equal(t, tc.neither, neither)
			require.Equal(t, tc.twice, twice)
			require.Equal(t, tc.stale, stale)
		})
	}
}
