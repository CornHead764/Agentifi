package household

import (
	"context"
	"database/sql"
)

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

// MigrateBalanceObservations adds balance_observations and the account's
// pending reading.
func MigrateBalanceObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, balanceObservationsSchema+WriteCounterSQL([]string{"balance_observations"}))
	return err
}

// UnmigrateBalanceObservations removes what MigrateBalanceObservations added.
func UnmigrateBalanceObservations(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `DROP TABLE balance_observations;
ALTER TABLE accounts DROP COLUMN bank_includes_pending;
`)
	return err
}
