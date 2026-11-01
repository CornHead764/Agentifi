package moneycolumns_test

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/moneycolumns"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// currentSchema reads table → column → declared type from both schemas,
// migrated to the latest version.
func currentSchema(t *testing.T) map[string]map[string]string {
	t.Helper()
	dir := t.TempDir()
	household, err := sqlitedb.OpenNewHousehold(t.Context(), filepath.Join(dir, "household.db"), time.Now)
	require.NoError(t, err)
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
