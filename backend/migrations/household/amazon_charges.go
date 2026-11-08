package household

import (
	"context"
	"database/sql"
)

// amazonChargesSchema holds what Amazon states about which charge paid for
// which items of an order it billed in parts: the FSA/HSA-eligible total its
// invoice shows (NULL until an invoice has been read for it, zero when it
// showed none, in the order's currency), the items it marks eligible, and the
// shipment box each item sits in with the day that box shipped or arrives.
const amazonChargesSchema = `
ALTER TABLE merchant_orders ADD COLUMN fsa_hsa_amount INTEGER;
ALTER TABLE merchant_order_items ADD COLUMN fsa_hsa_eligible INTEGER DEFAULT 0 NOT NULL CHECK (fsa_hsa_eligible IN (0, 1));
ALTER TABLE merchant_order_items ADD COLUMN shipment INTEGER;
ALTER TABLE merchant_order_items ADD COLUMN shipment_on TEXT CHECK (shipment_on GLOB '[0-9][0-9][0-9][0-9]-[0-9][0-9]-[0-9][0-9]');
`

// A charge read on two days under a shifted date was stored twice, the unique
// key holding the date as well. Of the charges of one order and amount within
// three days of each other, the latest stays.
const dropChargeDateTwins = `
DELETE FROM merchant_charges
WHERE EXISTS (
  SELECT 1 FROM merchant_charges later
  WHERE later.merchant_account_id = merchant_charges.merchant_account_id
    AND later.order_number = merchant_charges.order_number
    AND later.amount = merchant_charges.amount
    AND later.charged_on > merchant_charges.charged_on
    AND julianday(later.charged_on) - julianday(merchant_charges.charged_on) <= 3);
`

// migrateAmazonCharges adds the columns above and runs DropChargeDateTwins.
func migrateAmazonCharges(ctx context.Context, tx *sql.Tx) error {
	if _, err := tx.ExecContext(ctx, amazonChargesSchema); err != nil {
		return err
	}
	return DropChargeDateTwins(ctx, tx)
}

// DropChargeDateTwins drops the merchant charges stored twice under shifted
// dates, keeping the latest. Safe to run again.
func DropChargeDateTwins(ctx context.Context, db interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}) error {
	_, err := db.ExecContext(ctx, dropChargeDateTwins)
	return err
}

// unmigrateAmazonCharges removes the columns. The charges dropped were copies
// of charges that remain, so nothing restores them.
func unmigrateAmazonCharges(ctx context.Context, tx *sql.Tx) error {
	_, err := tx.ExecContext(ctx, `ALTER TABLE merchant_order_items DROP COLUMN shipment_on;
ALTER TABLE merchant_order_items DROP COLUMN shipment;
ALTER TABLE merchant_order_items DROP COLUMN fsa_hsa_eligible;
ALTER TABLE merchant_orders DROP COLUMN fsa_hsa_amount;
`)
	return err
}
