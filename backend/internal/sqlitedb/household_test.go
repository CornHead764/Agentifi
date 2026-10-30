package sqlitedb

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/migrations/household"
)

func migratedHousehold(t *testing.T) *DB {
	t.Helper()
	d := openTestDB(t)
	if snapshot, err := d.MigrateHousehold(t.Context(), ""); err != nil || snapshot != "" {
		t.Fatalf("fresh household migration = %q, %v", snapshot, err)
	}
	return d
}

func assertHealthySQLite(t *testing.T, d *DB) {
	t.Helper()
	rows, err := d.Read().QueryContext(t.Context(), "PRAGMA foreign_key_check")
	if err != nil {
		t.Fatal(err)
	}
	if rows.Next() {
		t.Error("foreign_key_check returned a violation")
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	var result string
	if err := d.Read().QueryRowContext(t.Context(), "PRAGMA integrity_check").Scan(&result); err != nil || result != "ok" {
		t.Fatalf("integrity_check = %q, %v", result, err)
	}
}

func TestSQLiteBaselines(t *testing.T) {
	for _, name := range []string{"server", "household"} {
		t.Run(name, func(t *testing.T) {
			d := openTestDB(t)
			var err error
			count := 11
			if name == "server" {
				_, err = d.MigrateServer(t.Context(), "")
			} else {
				_, err = d.MigrateHousehold(t.Context(), "")
				count = 59 // 57 household tables (incl. household_metadata) plus counter and tombstones.
			}
			if err != nil {
				t.Fatal(err)
			}
			var got int
			if err := d.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM pragma_table_list WHERE schema = 'main' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version' AND strict = 1`).Scan(&got); err != nil || got != count {
				t.Fatalf("STRICT table count = %d, %v; want %d", got, err, count)
			}
			assertHealthySQLite(t, d)
		})
	}
}

func categoryFixture(t *testing.T, d *DB, id string) {
	t.Helper()
	execTest(t, d.Write(), `INSERT INTO categories (id, name, kind, sort_order, created_at, updated_at) VALUES (?, ?, 'expense', 0, 1, 1)`, id, id)
}

func TestHouseholdChangeCapture(t *testing.T) {
	d := migratedHousehold(t)
	categoryFixture(t, d, "category")
	var seq, updated int64
	read := func() {
		t.Helper()
		if err := d.Read().QueryRowContext(t.Context(), `SELECT change_seq, updated_at FROM categories WHERE id = 'category'`).Scan(&seq, &updated); err != nil {
			t.Fatal(err)
		}
	}
	read()
	if seq != 1 {
		t.Fatalf("insert change_seq = %d", seq)
	}
	updatedAt := time.Now().Add(-time.Hour).UnixMilli()
	execTest(t, d.Write(), `UPDATE categories SET name = 'renamed', updated_at = ? WHERE id = 'category'`, updatedAt)
	read()
	if seq != 2 || updated != updatedAt {
		t.Fatalf("update seq/time = %d/%d", seq, updated)
	}
	execTest(t, d.Write(), `DELETE FROM categories WHERE id = 'category'`)
	var table, row string
	var deleted int64
	if err := d.Read().QueryRowContext(t.Context(), `SELECT seq, table_name, row_id, deleted_at FROM tombstones`).Scan(&seq, &table, &row, &deleted); err != nil {
		t.Fatal(err)
	}
	if seq != 3 || table != "categories" || row != "category" || deleted != d.Now() {
		t.Fatalf("tombstone = %d %s %s %d", seq, table, row, deleted)
	}
	var counter int64
	if err := d.Read().QueryRowContext(t.Context(), "SELECT seq FROM change_counter").Scan(&counter); err != nil || counter != 3 {
		t.Fatalf("counter = %d, %v", counter, err)
	}
	// Every manifest entry has an operational change_seq index/column, including
	// composite-key relationship tables (which have no synthetic id).
	for _, table := range household.CaptureTables {
		rows, err := d.Read().QueryContext(t.Context(), "SELECT change_seq, updated_at FROM "+table.Name+" INDEXED BY "+table.Name+"_change_seq WHERE change_seq > 0")
		if err != nil {
			t.Fatalf("%s: %v", table.Name, err)
		}
		rows.Close()
		for _, suffix := range []string{"ai", "au", "ad"} {
			var count int
			if err := d.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM sqlite_schema WHERE type = 'trigger' AND tbl_name = ?1 AND name = ?2", table.Name, table.Name+"_"+suffix).Scan(&count); err != nil || count != 1 {
				t.Fatalf("%s_%s trigger count = %d, %v", table.Name, suffix, count, err)
			}
		}
	}
	assertHealthySQLite(t, d)
}

func TestHouseholdAlreadyCapturedMigration(t *testing.T) {
	d := openTestDB(t)
	// Recreate the original version-1 schema, including capture columns and
	// generated trigger names, without marking Go migration 2 as applied.
	if _, err := d.Migrate(t.Context(), household.FS, ""); err != nil {
		t.Fatal(err)
	}
	tx, err := d.write.BeginTx(t.Context(), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	if err := household.MigrateChangeCapture(t.Context(), tx); err != nil {
		t.Fatal(err)
	}
	for _, table := range household.CaptureTables {
		for _, suffix := range []string{"ai", "au", "ad"} {
			if _, err := tx.ExecContext(t.Context(), "DROP TRIGGER "+table.Name+"_"+suffix); err != nil {
				t.Fatal(err)
			}
		}
	}
	legacySQL := strings.NewReplacer("_ai AFTER", "_ins AFTER", "_au AFTER", "_upd AFTER", "_ad AFTER", "_del AFTER").Replace(household.ChangeCaptureSQL(household.CaptureTables))
	if _, err := tx.ExecContext(t.Context(), legacySQL); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	categoryFixture(t, d, "before")
	snapshot := filepath.Join(t.TempDir(), "before.db")
	if got, err := d.MigrateHousehold(t.Context(), snapshot); err != nil || got != snapshot {
		t.Fatalf("already captured migration = %q, %v", got, err)
	}
	categoryFixture(t, d, "after")
	var seq int64
	if err := d.Read().QueryRowContext(t.Context(), "SELECT change_seq FROM categories WHERE id = 'after'").Scan(&seq); err != nil || seq != 2 {
		t.Fatalf("upgraded capture sequence = %d, %v", seq, err)
	}
	if got, err := d.MigrateHousehold(t.Context(), ""); err != nil || got != "" {
		t.Fatalf("repeat household migration = %q, %v", got, err)
	}
	assertHealthySQLite(t, d)
}

func TestHouseholdCompositeChangeCapture(t *testing.T) {
	d := migratedHousehold(t)
	execTest(t, d.Write(), `INSERT INTO documents (id, content_sha256, content_type, size_bytes, filename, storage_key, source, created_at, updated_at) VALUES ('doc', 'hash', 'text/plain', 1, 'file', 'key', 'upload', 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO document_links (document_id, kind, target_id, created_at) VALUES ('doc', 'transaction', 'txn', 1)`)
	var seq int64
	if err := d.Read().QueryRowContext(t.Context(), "SELECT change_seq FROM document_links").Scan(&seq); err != nil || seq != 2 {
		t.Fatalf("insert seq = %d, %v", seq, err)
	}
	updatedAt := d.Now()
	execTest(t, d.Write(), "UPDATE document_links SET role = 'receipt', updated_at = ?", updatedAt)
	var updated int64
	if err := d.Read().QueryRowContext(t.Context(), "SELECT change_seq, updated_at FROM document_links").Scan(&seq, &updated); err != nil || seq != 3 || updated != updatedAt {
		t.Fatalf("update = %d/%d, %v", seq, updated, err)
	}
	execTest(t, d.Write(), "DELETE FROM document_links")
	var row string
	if err := d.Read().QueryRowContext(t.Context(), "SELECT seq, row_id FROM tombstones").Scan(&seq, &row); err != nil || seq != 4 || row != `["doc","transaction","txn"]` {
		t.Fatalf("composite tombstone = %d/%s, %v", seq, row, err)
	}
	assertHealthySQLite(t, d)
}

// Ports the underlying SQL semantics exercised by store's
// TestACategoryAPersonSetsOnAPairedLegSurvivesTheRelease; store queries remain PG.
func TestHouseholdCategoryFromPair(t *testing.T) {
	d := migratedHousehold(t)
	categoryFixture(t, d, "transfer")
	categoryFixture(t, d, "gifts")
	execTest(t, d.Write(), `INSERT INTO accounts (id, name, kind, type, currency, sort_order, created_at, updated_at) VALUES ('account', 'Checking', 'cash', 'checking', 'USD', 0, 1, 1)`)
	execTest(t, d.Write(), `INSERT INTO transactions (id, account_id, date, amount, currency, source, created_at, updated_at) VALUES ('txn', 'account', '2026-03-04', -7500, 'USD', 'sync', 1, 1)`)
	cases := []struct {
		name, set string
		category  any
		fromPair  int
	}{
		{"pair sets category and flag", "category_id = 'transfer', category_from_pair = 1", "transfer", 1},
		{"same category preserves pairing", "category_id = 'transfer'", "transfer", 1},
		{"person changes category", "category_id = 'gifts'", "gifts", 0},
		{"manual transfer choice stays manual", "category_id = 'transfer'", "transfer", 0},
		{"release cannot clear manual category", "category_id = CASE WHEN category_from_pair = 1 THEN NULL ELSE category_id END, category_from_pair = 0", "transfer", 0},
		{"clear category handles NULL", "category_id = NULL", nil, 0},
		{"pair from NULL", "category_id = 'transfer', category_from_pair = 1", "transfer", 1},
		{"clear paired category handles NULL", "category_id = NULL", nil, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			execTest(t, d.Write(), "UPDATE transactions SET "+tc.set+" WHERE id = 'txn'")
			var category sql.NullString
			var flag int
			if err := d.Read().QueryRowContext(t.Context(), "SELECT category_id, category_from_pair FROM transactions WHERE id = 'txn'").Scan(&category, &flag); err != nil {
				t.Fatal(err)
			}
			if flag != tc.fromPair || category.Valid != (tc.category != nil) || (category.Valid && category.String != tc.category) {
				t.Fatalf("filing = %v/%d; want %v/%d", category, flag, tc.category, tc.fromPair)
			}
		})
	}
	assertHealthySQLite(t, d)
}
