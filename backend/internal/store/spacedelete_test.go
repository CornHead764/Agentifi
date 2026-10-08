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
		SELECT m.name FROM sqlite_schema m
		WHERE m.type = 'table'
		  AND EXISTS (SELECT 1 FROM pragma_table_info(m.name) c WHERE c.name = 'space_id')
		  AND NOT EXISTS (
		    SELECT 1 FROM pragma_foreign_key_list(m.name) k
		    WHERE k."table" = 'spaces' AND k.on_delete = 'CASCADE')
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
		SELECT DISTINCT m.name FROM sqlite_schema m, pragma_foreign_key_list(m.name) k
		WHERE m.type = 'table' AND k.on_delete = 'RESTRICT'
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
