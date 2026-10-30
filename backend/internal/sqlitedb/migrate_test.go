package sqlitedb

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"
	"time"
)

func migrationSet(second string) fstest.MapFS {
	files := fstest.MapFS{
		"00001_initial.sql": &fstest.MapFile{Data: []byte("-- +goose Up\nCREATE TABLE things (id INTEGER PRIMARY KEY) STRICT;\n")},
	}
	if second != "" {
		files["00002_more.sql"] = &fstest.MapFile{Data: []byte("-- +goose Up\n" + second + "\n")}
	}
	return files
}

func TestMigrationSnapshots(t *testing.T) {
	d := openTestDB(t)
	dir := t.TempDir()
	fresh := filepath.Join(dir, "fresh.db")
	if got, err := d.Migrate(t.Context(), migrationSet(""), fresh); err != nil || got != "" {
		t.Fatalf("fresh migration = %q, %v", got, err)
	}
	if _, err := os.Stat(fresh); !os.IsNotExist(err) {
		t.Fatalf("fresh snapshot exists or stat failed: %v", err)
	}
	execTest(t, d.Write(), "INSERT INTO things VALUES (7)")
	none := filepath.Join(dir, "none.db")
	if got, err := d.Migrate(t.Context(), migrationSet(""), none); err != nil || got != "" {
		t.Fatalf("no pending migration = %q, %v", got, err)
	}
	if _, err := os.Stat(none); !os.IsNotExist(err) {
		t.Fatalf("unneeded snapshot exists or stat failed: %v", err)
	}
	pending := filepath.Join(dir, "pending.db")
	got, err := d.Migrate(t.Context(), migrationSet("ALTER TABLE things ADD COLUMN label TEXT;"), pending)
	if err != nil || got != pending {
		t.Fatalf("pending migration = %q, %v", got, err)
	}
	snapshot, err := sql.Open("sqlite", pending)
	if err != nil {
		t.Fatal(err)
	}
	defer snapshot.Close()
	var id int
	if err := snapshot.QueryRowContext(t.Context(), "SELECT id FROM things").Scan(&id); err != nil || id != 7 {
		t.Fatalf("snapshot data = %d, %v", id, err)
	}
	if _, err := snapshot.ExecContext(t.Context(), "SELECT label FROM things"); err == nil {
		t.Fatal("snapshot contains migration changes")
	}
	var label sql.NullString
	if err := d.Read().QueryRowContext(t.Context(), "SELECT label FROM things").Scan(&label); err != nil {
		t.Fatalf("migration was not applied: %v", err)
	}
}

func TestFailedMigrationKeepsSnapshot(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.Migrate(t.Context(), migrationSet(""), ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "before.db")
	got, err := d.Migrate(t.Context(), migrationSet("THIS IS NOT SQL;"), path)
	if err == nil || got != path {
		t.Fatalf("failed migration = %q, %v", got, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("snapshot lost: %v", err)
	}
}

func TestSnapshotFailureStopsMigration(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.Migrate(t.Context(), migrationSet(""), ""); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "exists.db")
	if err := os.WriteFile(path, []byte("keep"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := d.Migrate(t.Context(), migrationSet("ALTER TABLE things ADD COLUMN label TEXT;"), path); err == nil {
		t.Fatal("snapshot overwrite was allowed")
	}
	var count int
	if err := d.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM pragma_table_info('things') WHERE name='label'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("migration ran after snapshot failure: %d, %v", count, err)
	}
}

func TestServerMigration(t *testing.T) {
	d := openTestDB(t)
	if _, err := d.MigrateServer(t.Context(), ""); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := d.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	rows, err := d.Read().QueryContext(t.Context(), "SELECT name, strict FROM pragma_table_list WHERE schema='main' AND name NOT LIKE 'sqlite_%' AND name <> 'goose_db_version'")
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		var strict int
		if err := rows.Scan(&name, &strict); err != nil {
			t.Fatal(err)
		}
		if strict != 1 {
			t.Errorf("%s is not STRICT", name)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
}

func TestNewHouseholdSchema(t *testing.T) {
	ctx := t.Context()
	baseline := migratedHousehold(t)
	schema := func(db *DB) []string {
		t.Helper()
		rows, err := db.Read().QueryContext(ctx, `SELECT type || ':' || name || ':' || coalesce(sql, '') FROM sqlite_schema ORDER BY type, name`)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		var result []string
		for rows.Next() {
			var value string
			if err := rows.Scan(&value); err != nil {
				t.Fatal(err)
			}
			result = append(result, value)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
		return result
	}
	wantSchema := schema(baseline)
	dir := t.TempDir()
	for i, name := range []string{"first", "second"} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(dir, name+".db")
			now := time.UnixMilli(int64(1000 + i))
			db, err := OpenNewHousehold(ctx, path, func() time.Time { return now })
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { db.Close() })
			if got := schema(db); !reflect.DeepEqual(got, wantSchema) {
				t.Fatal("template schema differs from fresh migrations")
			}
			if got, err := db.MigrateHousehold(ctx, ""); err != nil || got != "" {
				t.Fatalf("template has pending migrations: %q, %v", got, err)
			}
			var count int
			if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM household_metadata`).Scan(&count); err != nil || count != 0 {
				t.Fatalf("template contains household identity: %d, %v", count, err)
			}
			execTest(t, db.Write(), `INSERT INTO household_metadata (id, household_uuid, primary_currency) VALUES (1, ?, 'USD')`, name)
			categoryFixture(t, db, name)
			execTest(t, db.Write(), `DELETE FROM categories WHERE id = ?`, name)
			var deleted int64
			if err := db.Read().QueryRowContext(ctx, `SELECT deleted_at FROM tombstones WHERE row_id = ?`, name).Scan(&deleted); err != nil || deleted != now.UnixMilli() {
				t.Fatalf("template clock = %d, %v; want %d", deleted, err, now.UnixMilli())
			}
			assertHealthySQLite(t, db)
			if _, err := OpenNewHousehold(ctx, path, time.Now); !os.IsExist(err) {
				t.Fatalf("existing household was not protected: %v", err)
			}
			var identity string
			if err := db.Read().QueryRowContext(ctx, `SELECT household_uuid FROM household_metadata`).Scan(&identity); err != nil || identity != name {
				t.Fatalf("household identity overwritten: %q, %v", identity, err)
			}
		})
	}
}

func TestNewHouseholdFailureRemovesOnlyNewFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "household.db")
	if _, err := OpenNewHousehold(t.Context(), path, nil); err == nil {
		t.Fatal("nil clock accepted")
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("failed household creation left a file: %v", err)
	}
}
