package household

import (
	"context"
	"database/sql"
)

// PaidMarkTables holds series_paid_marks: one row per occurrence a person
// marked paid or received, keyed by the series and the scheduled slot. It is
// created here with its capture columns in place.
var PaidMarkTables = []SyncTable{
	{Name: "series_paid_marks", Keys: []string{"series_id", "due_on"}, HasUpdatedAt: true},
}

const paidMarkSchema = `
CREATE TABLE series_paid_marks (
    series_id TEXT NOT NULL,
    due_on TEXT NOT NULL CHECK (due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    marked_on TEXT NOT NULL CHECK (marked_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    change_seq INTEGER NOT NULL DEFAULT 0,
    CONSTRAINT series_paid_marks_pkey PRIMARY KEY (series_id, due_on),
    CONSTRAINT series_paid_marks_series_id_fkey FOREIGN KEY (series_id) REFERENCES series(id) ON DELETE CASCADE
) STRICT;
`

// MigratePaidMarks adds series_paid_marks.
func MigratePaidMarks(ctx context.Context, tx *sql.Tx) error {
	names := make([]string, len(PaidMarkTables))
	for i, table := range PaidMarkTables {
		names[i] = table.Name
	}
	_, err := tx.ExecContext(ctx, paidMarkSchema+ChangeCaptureSQL(PaidMarkTables)+WriteCounterSQL(names))
	return err
}

// UnmigratePaidMarks removes it, with its triggers.
func UnmigratePaidMarks(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE series_paid_marks;`)
	return err
}
