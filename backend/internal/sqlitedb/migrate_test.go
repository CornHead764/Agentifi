package sqlitedb

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"
	"testing/fstest"
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
