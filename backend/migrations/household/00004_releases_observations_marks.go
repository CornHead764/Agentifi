package household

import (
	"context"
	"database/sql"
)

// MigrateReleasesObservationsAndMarks adds envelopes.released_amount,
// balance_observations with the account's pending reading, and
// series_paid_marks.
func MigrateReleasesObservationsAndMarks(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE envelopes ADD COLUMN released_amount INTEGER;`); err != nil {
		return err
	}
	if err := migrateBalanceObservations(ctx, tx); err != nil {
		return err
	}
	return migratePaidMarks(ctx, tx)
}

// UnmigrateReleasesObservationsAndMarks removes what
// MigrateReleasesObservationsAndMarks added.
func UnmigrateReleasesObservationsAndMarks(ctx context.Context, tx *sql.Tx) error {
	if err := unmigratePaidMarks(ctx, tx); err != nil {
		return err
	}
	if err := unmigrateBalanceObservations(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE envelopes DROP COLUMN released_amount;`)
	return err
}

// balance_observations keeps what each sync saw of a connected account
// (domain.BalanceObservation): the balance the bank reported, the ledger's
// totals once the sync's rows were in, and how far the sync's own rows moved
// them. Amounts are in the account's currency.
const balanceObservationsSchema = `
ALTER TABLE accounts ADD COLUMN bank_includes_pending INTEGER DEFAULT 0 NOT NULL CHECK (bank_includes_pending IN (0, 1));
CREATE TABLE balance_observations (
    id TEXT NOT NULL,
    account_id TEXT NOT NULL,
    observed_at INTEGER NOT NULL,
    reported INTEGER NOT NULL,
    ledger_total INTEGER NOT NULL,
    settled_total INTEGER NOT NULL,
    movement INTEGER NOT NULL,
    settled_movement INTEGER NOT NULL,
    CONSTRAINT balance_observations_pkey PRIMARY KEY (id),
    CONSTRAINT balance_observations_account_id_fkey FOREIGN KEY (account_id) REFERENCES accounts(id) ON DELETE CASCADE
) STRICT;
CREATE INDEX ix_balance_observations_account ON balance_observations (account_id, observed_at);
`

// migrateBalanceObservations adds balance_observations and the account's
// pending reading.
func migrateBalanceObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, balanceObservationsSchema+WriteCounterSQL([]string{"balance_observations"}))
	return err
}

// unmigrateBalanceObservations removes what migrateBalanceObservations added.
func unmigrateBalanceObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE balance_observations;
ALTER TABLE accounts DROP COLUMN bank_includes_pending;
`)
	return err
}

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

// migratePaidMarks adds series_paid_marks.
func migratePaidMarks(ctx context.Context, tx *sql.Tx) error {
	names := make([]string, len(PaidMarkTables))
	for i, table := range PaidMarkTables {
		names[i] = table.Name
	}
	_, err := tx.ExecContext(ctx, paidMarkSchema+ChangeCaptureSQL(PaidMarkTables)+WriteCounterSQL(names))
	return err
}

// unmigratePaidMarks removes it, with its triggers.
func unmigratePaidMarks(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE series_paid_marks;`)
	return err
}
