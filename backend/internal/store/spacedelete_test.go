package store

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/require"
)

// Deleting a space is one DELETE that the schema carries to every row: a
// table with a space_id and no cascading key to spaces would keep its rows,
// and one with a restricting key DeleteSpace does not know of could refuse.

func TestEverySpaceScopedTableCascadesFromSpaces(t *testing.T) {
	var missing []string
	rows, err := db(t).Pool().Query(t.Context(), `
		SELECT c.table_name FROM information_schema.columns c
		JOIN information_schema.tables t
		  ON t.table_schema = c.table_schema AND t.table_name = c.table_name
		WHERE c.table_schema = current_schema() AND c.column_name = 'space_id'
		  AND t.table_type = 'BASE TABLE'
		  AND NOT EXISTS (
		    SELECT 1 FROM pg_constraint k
		    WHERE k.contype = 'f' AND k.confdeltype = 'c'
		      AND k.conrelid = (quote_ident(current_schema()) || '.' || quote_ident(c.table_name))::regclass
		      AND k.confrelid = (quote_ident(current_schema()) || '.spaces')::regclass)
		ORDER BY 1`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		missing = append(missing, name)
	}
	rows.Close()
	require.Empty(t, missing, "space-scoped tables with no ON DELETE CASCADE key to spaces")
}

func TestDeleteSpaceKnowsEveryRestrictingKey(t *testing.T) {
	var restricting []string
	rows, err := db(t).Pool().Query(t.Context(), `
		SELECT DISTINCT k.conrelid::regclass::text FROM pg_constraint k
		JOIN pg_namespace n ON n.oid = k.connamespace
		WHERE k.contype = 'f' AND k.confdeltype = 'r' AND n.nspname = current_schema()
		ORDER BY 1`)
	require.NoError(t, err)
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		restricting = append(restricting, name)
	}
	rows.Close()

	known := slices.Clone(restrictedBySpace)
	slices.Sort(known)
	require.Equal(t, known, restricting)
}
