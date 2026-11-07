package household

import (
	"context"
	"database/sql"
)

// MigrateReleasesObservationsAndMarks adds envelopes.released_amount and
// envelopes.roll_under, balance_observations with the account's pending
// reading, and series_paid_marks, and runs RepairImportedEnvelopes.
func MigrateReleasesObservationsAndMarks(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, `ALTER TABLE envelopes ADD COLUMN released_amount INTEGER;`); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, rollUnderSchema); err != nil {
		return err
	}
	if err := migrateBalanceObservations(ctx, tx); err != nil {
		return err
	}
	if err := migratePaidMarks(ctx, tx); err != nil {
		return err
	}
	return RepairImportedEnvelopes(ctx, tx)
}

// UnmigrateReleasesObservationsAndMarks removes what
// MigrateReleasesObservationsAndMarks added. The rows RepairImportedEnvelopes
// changed are not recorded, so they stay as it left them.
func UnmigrateReleasesObservationsAndMarks(ctx context.Context, tx *sql.Tx) error {
	if err := unmigratePaidMarks(ctx, tx); err != nil {
		return err
	}
	if err := unmigrateBalanceObservations(ctx, tx); err != nil {
		return err
	}
	_, err := tx.ExecContext(ctx, `ALTER TABLE envelopes DROP COLUMN roll_under;
ALTER TABLE envelopes DROP COLUMN released_amount;`)
	return err
}

// roll_under carries an overspend into the next month. Before it, one setting
// governed both directions: an envelope that did not auto-release carried an
// overspend too, and one that did carried neither, so each existing envelope
// rolls under exactly when it does not auto-release.
const rollUnderSchema = `
ALTER TABLE envelopes ADD COLUMN roll_under INTEGER DEFAULT 1 NOT NULL CHECK (roll_under IN (0, 1));
UPDATE envelopes SET roll_under = 0 WHERE auto_release_rollover = 1;
`

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
// marked paid or received, keyed by the series and the scheduled slot, with
// the amount (in currency) and the day (shown_on, when it was not the slot)
// the slot showed when it was marked. It is created here with its capture
// columns in place.
var PaidMarkTables = []SyncTable{
	{Name: "series_paid_marks", Keys: []string{"series_id", "due_on"}, HasUpdatedAt: true},
}

const paidMarkSchema = `
CREATE TABLE series_paid_marks (
    series_id TEXT NOT NULL,
    due_on TEXT NOT NULL CHECK (due_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    shown_on TEXT CHECK (shown_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]'),
    amount INTEGER NOT NULL,
    currency TEXT NOT NULL,
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

// An envelope the import wrote shares its month's created_at. Simplifi gave
// each month's copy of a recurring envelope its own id, and the import a group
// per id; the copies share a filter. The earliest copy's group becomes every
// copy's, and so that of any envelope rolled forward from one, unless two
// envelopes on that filter, or in that group, would share a month.
const linkImportedEnvelopes = `
WITH imported AS (
  SELECT e.id, e.filter_id, e.spending_plan_month_id, e.recurring_group_id AS old_group, m.month
  FROM envelopes e JOIN spending_plan_months m ON m.id = e.spending_plan_month_id
  WHERE e.created_at = m.created_at AND e.recurring = 1 AND e.recurring_group_id IS NOT NULL
),
candidate AS (
  SELECT DISTINCT filter_id, old_group,
    first_value(old_group) OVER (PARTITION BY filter_id ORDER BY month, id) AS new_group
  FROM imported
),
ambiguous AS (
  SELECT filter_id FROM imported GROUP BY filter_id, spending_plan_month_id HAVING count(*) > 1
  UNION
  SELECT filter_id FROM candidate WHERE old_group IN (
    SELECT old_group FROM candidate GROUP BY old_group HAVING count(DISTINCT filter_id) > 1)
),
relink AS (
  SELECT old_group, new_group FROM candidate WHERE filter_id NOT IN (SELECT filter_id FROM ambiguous)
),
crowded AS (
  SELECT r.new_group FROM envelopes e JOIN relink r ON r.old_group = e.recurring_group_id
  GROUP BY e.spending_plan_month_id, r.new_group HAVING count(*) > 1
)
UPDATE envelopes
SET recurring_group_id = (SELECT new_group FROM relink WHERE relink.old_group = envelopes.recurring_group_id)
WHERE recurring_group_id IN (
  SELECT old_group FROM relink
  WHERE old_group <> new_group AND new_group NOT IN (SELECT new_group FROM crowded));
`

// A nonzero rollover may be one a person set. The household file holds no
// timezone.
const carryImportedRollovers = `
UPDATE envelopes SET rollover_is_carried = 1
WHERE rollover_is_carried = 0 AND rollover_amount = 0
  AND EXISTS (
    SELECT 1 FROM spending_plan_months m
    WHERE m.id = envelopes.spending_plan_month_id
      AND m.is_closed_out = 0
      AND m.created_at = envelopes.created_at
      AND m.month > strftime('%Y-%m-01', m.created_at / 1000, 'unixepoch'));
`

// RepairImportedEnvelopes links the months of each envelope an earlier
// Simplifi import wrote, and marks carried those left holding Simplifi's
// placeholder rollover (data-model.md §5). Safe to run again.
func RepairImportedEnvelopes(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	for _, repair := range []string{linkImportedEnvelopes, carryImportedRollovers} {
		if _, err := db.ExecContext(ctx, repair); err != nil {
			return err
		}
	}
	return nil
}
