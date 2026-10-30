package household

import (
	"context"
	"database/sql"
	"strings"
)

// MigrateChangeCapture handles both fresh schemas and files that already received
// capture columns and triggers from the original SQLite 00001 migration.
// Goose runs this atomically and snapshots existing files before applying it.
// All DDL runs as one script so SQLite does not re-prepare it statement by statement.
func MigrateChangeCapture(ctx context.Context, tx *sql.Tx) error {
	var script strings.Builder
	for _, table := range CaptureTables {
		for _, column := range [...]struct{ name, definition string }{
			{"change_seq", "INTEGER NOT NULL DEFAULT 0"},
			{"updated_at", "INTEGER"},
		} {
			if column.name == "updated_at" && table.HasUpdatedAt {
				continue
			}
			var exists bool
			if err := tx.QueryRowContext(ctx, "SELECT EXISTS (SELECT 1 FROM pragma_table_info(?1) WHERE name = ?2)", table.Name, column.name).Scan(&exists); err != nil {
				return err
			}
			if !exists {
				script.WriteString("ALTER TABLE " + table.Name + " ADD COLUMN " + column.name + " " + column.definition + ";\n")
			}
		}
		// Replace the old generated trigger names, never leaving two sets that
		// would increment the counter twice for the same write.
		for _, suffix := range []string{"ins", "upd", "del"} {
			script.WriteString("DROP TRIGGER IF EXISTS " + table.Name + "_" + suffix + ";\n")
		}
	}
	script.WriteString(ChangeCaptureSQL(CaptureTables))
	_, err := tx.ExecContext(ctx, script.String())
	return err
}
