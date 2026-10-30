package household

import (
	"context"
	"database/sql"
	"strings"
)

// Bookkeeping tables that WriteCounterSQL leaves alone: they only change
// alongside a table that already bumps the counter.
var writeCounterExempt = map[string]bool{
	"goose_db_version": true,
	"change_counter":   true,
	"tombstones":       true,
	"write_counter":    true,
}

// WriteCounted reports whether a table must carry the write_counter triggers.
func WriteCounted(table string) bool {
	return !writeCounterExempt[table] && !strings.HasPrefix(table, "sqlite_")
}

// WriteCounterSQL installs triggers that bump write_counter on every insert,
// update and delete in the tables given. Unlike change_counter, which only
// the tables synced to clients move, it moves for every write, so an
// unchanged write_counter means an unchanged file: a backup that finds it
// where the last set left it reuses that set's copy. A migration that adds a
// table runs this for it. It is safe to apply more than once.
func WriteCounterSQL(tables []string) string {
	var out strings.Builder
	for _, table := range tables {
		for _, event := range [...]struct{ suffix, verb string }{{"wi", "INSERT"}, {"wu", "UPDATE"}, {"wd", "DELETE"}} {
			out.WriteString("CREATE TRIGGER IF NOT EXISTS " + table + "_" + event.suffix + " AFTER " + event.verb + " ON " + table +
				" BEGIN UPDATE write_counter SET seq = seq + 1 WHERE id = 1; END;\n")
		}
	}
	return out.String()
}

// MigrateWriteCounter adds write_counter and its triggers on every table the
// schema has at this version.
func MigrateWriteCounter(ctx context.Context, tx *sql.Tx) error {
	tables, err := countedTables(ctx, tx)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `CREATE TABLE write_counter (id INTEGER PRIMARY KEY CHECK (id = 1), seq INTEGER NOT NULL) STRICT;
INSERT INTO write_counter VALUES (1, 0);
`+WriteCounterSQL(tables))
	return err
}

// UnmigrateWriteCounter removes what MigrateWriteCounter added.
func UnmigrateWriteCounter(ctx context.Context, tx *sql.Tx) error {
	tables, err := countedTables(ctx, tx)
	if err != nil {
		return err
	}
	var script strings.Builder
	for _, table := range tables {
		for _, suffix := range []string{"wi", "wu", "wd"} {
			script.WriteString("DROP TRIGGER IF EXISTS " + table + "_" + suffix + ";\n")
		}
	}
	script.WriteString("DROP TABLE write_counter;\n")
	_, err = tx.ExecContext(ctx, script.String())
	return err
}

func countedTables(ctx context.Context, tx *sql.Tx) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		if WriteCounted(name) {
			tables = append(tables, name)
		}
	}
	return tables, rows.Err()
}
