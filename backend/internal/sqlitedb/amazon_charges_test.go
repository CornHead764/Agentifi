package sqlitedb

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/CornHead764/agentifi/backend/migrations/household"
	"github.com/pressly/goose/v3"
)

func TestChargesStoredUnderShiftedDatesKeepTheLatest(t *testing.T) {
	// docs/connectors/merchants.md: a charge read again under a date up to
	// three days off is one charge, at the later date. Of an order's charges
	// of one amount within three days of each other, the latest stays.
	d := openTestDB(t)
	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, household.FS, householdGoMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.UpTo(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	execTest(t, d.Write(), `INSERT INTO merchant_accounts (id, label, created_at, updated_at) VALUES ('login', 'Household', 1, 1)`)
	for _, row := range []struct {
		id, order, day string
		amount         int64
	}{
		{"moved-from", "111-0000000-0000001", "2026-10-11", -1600},
		{"moved-to", "111-0000000-0000001", "2026-10-12", -1600},
		{"other-amount", "111-0000000-0000001", "2026-10-12", -1200},
		{"weeks-later", "111-0000000-0000001", "2026-10-25", -1600},
		{"other-order", "111-0000000-0000002", "2026-10-11", -1600},
	} {
		execTest(t, d.Write(), `INSERT INTO merchant_charges (id, merchant_account_id, order_number, charged_on, amount, created_at)
			VALUES (?, 'login', ?, ?, ?, 1)`, row.id, row.order, row.day, row.amount)
	}

	if _, err := d.MigrateHousehold(t.Context(), filepath.Join(t.TempDir(), "before.db")); err != nil {
		t.Fatal(err)
	}

	rows, err := d.Read().QueryContext(t.Context(), `SELECT id FROM merchant_charges ORDER BY id`)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	var kept []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			t.Fatal(err)
		}
		kept = append(kept, id)
	}
	if want := []string{"moved-to", "other-amount", "other-order", "weeks-later"}; !slices.Equal(kept, want) {
		t.Fatalf("kept = %v; want %v", kept, want)
	}
	var tombstones int
	if err := d.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM tombstones WHERE table_name = 'merchant_charges'`).Scan(&tombstones); err != nil || tombstones != 1 {
		t.Fatalf("merchant_charges tombstones = %d, %v; a client holding the dropped copy is told it went", tombstones, err)
	}
	assertHealthySQLite(t, d)
}

func TestTheAmazonChargeColumnsComeAndGoWithTheirMigration(t *testing.T) {
	d := migratedHousehold(t)
	execTest(t, d.Write(), `INSERT INTO merchant_accounts (id, label, created_at, updated_at) VALUES ('login', 'Household', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO merchant_orders (id, merchant_account_id, order_number, ordered_on, source, created_at, updated_at, fsa_hsa_amount)
		VALUES ('order', 'login', '111-0000000-0000001', '2026-10-09', 'pull', 1, 1, 1600)`)
	execTest(t, d.Write(), `INSERT INTO merchant_order_items (id, order_id, position, title, shipment, shipment_on)
		VALUES ('item', 'order', 0, 'Desk lamp', 0, '2026-10-11')`)
	var eligible int
	if err := d.Read().QueryRowContext(t.Context(), `SELECT fsa_hsa_eligible FROM merchant_order_items`).Scan(&eligible); err != nil || eligible != 0 {
		t.Fatalf("fsa_hsa_eligible = %d, %v; an item no invoice marked is not eligible", eligible, err)
	}
	if _, err := d.Write().ExecContext(t.Context(), `UPDATE merchant_order_items SET shipment_on = 'October 11'`); err == nil {
		t.Fatal("shipment_on took a day that is not ISO")
	}

	provider, err := goose.NewProvider(goose.DialectSQLite3, d.write, household.FS, householdGoMigrations())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := provider.DownTo(t.Context(), 3); err != nil {
		t.Fatal(err)
	}
	for _, column := range []struct{ table, name string }{
		{"merchant_orders", "fsa_hsa_amount"},
		{"merchant_order_items", "fsa_hsa_eligible"},
		{"merchant_order_items", "shipment"},
		{"merchant_order_items", "shipment_on"},
	} {
		var count int
		if err := d.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_info(?1) WHERE name = ?2`, column.table, column.name).Scan(&count); err != nil || count != 0 {
			t.Fatalf("%s.%s after the down migration = %d, %v", column.table, column.name, count, err)
		}
	}
	assertHealthySQLite(t, d)
}
