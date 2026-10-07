package backup_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A whole backup and restore against a real Postgres: dump, encrypt, rehearse
// into a scratch database, restore over the live one, and check the rows and
// the attachments came back. It needs TEST_DATABASE_URL and the client tools
// (pg_dump, pg_restore) at the server's major version on PATH.
//
// It works in a database of its own rather than the shared test schema: a
// restore renames databases, which no other test may be connected to.

func requireTools(t *testing.T) backup.Postgres {
	t.Helper()
	url := os.Getenv("TEST_DATABASE_URL")
	if url == "" {
		t.Skip("TEST_DATABASE_URL is unset; see CONTRIBUTING.md for the development Postgres")
	}
	// A missing or mismatched client skips on a workstation but is fatal in
	// CI, where a skipped round trip would read exactly like a passing one.
	fatal := t.Skip
	if os.Getenv("CI") != "" {
		fatal = t.Fatal
	}
	for _, tool := range []string{"pg_dump", "pg_restore"} {
		if _, err := exec.LookPath(tool); err != nil {
			fatal(fmt.Sprintf("%s is not on PATH; put the client tools of the test server's major "+
				"version there (the development Postgres ships them beside its server binary)", tool))
		}
	}
	tools := backup.CheckTools(t.Context(), backup.Postgres{URL: url})
	if tools.Problem != "" {
		fatal(tools.Problem)
	}
	return backup.Postgres{URL: url}
}

func freshDatabase(t *testing.T, server backup.Postgres) backup.Postgres {
	t.Helper()
	ctx := t.Context()
	name := fmt.Sprintf("agentifi_backup_e2e_%d", os.Getpid())
	maintenance, err := pgx.Connect(ctx, server.On("postgres").URL)
	require.NoError(t, err)
	dropAll := func() {
		ctx := context.Background()
		rows, err := maintenance.Query(ctx, `SELECT datname FROM pg_database WHERE datname LIKE $1`, name+"%")
		require.NoError(t, err)
		names, err := pgx.CollectRows(rows, pgx.RowTo[string])
		require.NoError(t, err)
		for _, one := range names {
			_, err := maintenance.Exec(ctx, `DROP DATABASE IF EXISTS `+pgx.Identifier{one}.Sanitize()+` WITH (FORCE)`)
			require.NoError(t, err)
		}
	}
	dropAll()
	_, err = maintenance.Exec(ctx, `CREATE DATABASE `+pgx.Identifier{name}.Sanitize())
	require.NoError(t, err)
	t.Cleanup(func() {
		dropAll()
		_ = maintenance.Close(context.Background())
	})
	return server.On(name)
}

func openStore(t *testing.T, db backup.Postgres) *store.Store {
	t.Helper()
	cfg, err := store.ParseConfig(db.URL)
	require.NoError(t, err)
	st, err := store.OpenPool(t.Context(), cfg)
	require.NoError(t, err)
	return st
}

func exec1(t *testing.T, db backup.Postgres, sql string, args ...any) {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), db.URL)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	_, err = conn.Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

func count(t *testing.T, db backup.Postgres, table string) int64 {
	t.Helper()
	conn, err := pgx.Connect(t.Context(), db.URL)
	require.NoError(t, err)
	defer conn.Close(context.Background())
	var n int64
	require.NoError(t, conn.QueryRow(t.Context(), `SELECT count(*) FROM `+table).Scan(&n))
	return n
}

func rows(report backup.Report, table string) int64 {
	for _, one := range report.Tables {
		if one.Table == table {
			return one.Rows
		}
	}
	return -1
}

func writeFiles(t *testing.T, root string, files map[string]string) {
	t.Helper()
	for name, body := range files {
		path := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o700))
		require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	}
}

func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		require.NoError(t, err)
		if info.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, path)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	}))
	return out
}

func TestABackupIsEncryptedRehearsedAndRestored(t *testing.T) {
	db := freshDatabase(t, requireTools(t))
	ctx := t.Context()

	st := openStore(t, db)
	_, err := st.Migrate(ctx)
	require.NoError(t, err)
	version, err := st.SchemaVersion(ctx)
	require.NoError(t, err)
	st.Close()

	// Invented rows in two tables the schema always has.
	exec1(t, db, `INSERT INTO server_settings (key, value_encrypted) VALUES
		('invented_one', 'an-invented-plaintext-marker'), ('invented_two', 'x')`)
	exec1(t, db, `INSERT INTO backup_runs (id, trigger, status) VALUES
		(gen_random_uuid(), 'manual', 'succeeded'), (gen_random_uuid(), 'nightly', 'failed'),
		(gen_random_uuid(), 'nightly', 'skipped')`)

	attachments := t.TempDir()
	writeFiles(t, attachments, map[string]string{
		"space-1/receipt.pdf": "an invented receipt", "space-2/statement.pdf": "an invented statement",
	})
	secrets := t.TempDir()
	writeFiles(t, secrets, map[string]string{"SECRET_KEY": "an-invented-secret", "DATABASE_URL": "postgres://x"})

	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	identities := []age.Identity{identity}
	dir := t.TempDir()
	source := backup.Source{
		Database: db, StoragePath: attachments, SecretsDir: secrets,
		CredentialKeyID: backup.KeyID("an-invented-credential-key"), SchemaVersion: version,
	}
	takenAt := time.Date(2026, 3, 3, 3, 30, 0, 0, time.Local)

	unlock, err := backup.Lock(ctx, db)
	require.NoError(t, err)
	set, err := backup.Write(ctx, dir, source, []string{identity.Recipient().String()}, backup.TriggerManual, takenAt)
	unlock()
	require.NoError(t, err)

	require.Equal(t, "2026-03-03_033000_manual", set.Name)
	require.True(t, set.Intact, set.Problem)
	require.True(t, set.Encrypted)
	require.True(t, set.Verified)
	require.Equal(t, version, set.SchemaVersion)
	require.Equal(t, 2, set.Files[backup.PartAttachments].Count)
	require.Equal(t, []string{"DATABASE_URL", "SECRET_KEY"}, set.Files[backup.PartSecrets].Names)
	for _, file := range set.Files {
		require.True(t, strings.HasSuffix(file.Path, ".age"), file.Path)
		data, err := os.ReadFile(filepath.Join(set.Dir, file.Path))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(data), "age-encryption.org/v1"), file.Path)
		require.NotContains(t, string(data), "an-invented")
		info, err := os.Stat(filepath.Join(set.Dir, file.Path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0o600), info.Mode().Perm())
	}

	// A rehearsal reads everything back and leaves the live database alone.
	report, err := backup.Rehearse(ctx, set, identities, db, takenAt)
	require.NoError(t, err)
	require.EqualValues(t, 2, rows(report, "server_settings"))
	require.EqualValues(t, 3, rows(report, "backup_runs"))
	require.Equal(t, 2, report.Attachments)
	require.Equal(t, []string{"DATABASE_URL", "SECRET_KEY"}, report.Secrets)

	stranger, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	_, err = backup.Rehearse(ctx, set, []age.Identity{stranger}, db, takenAt)
	require.ErrorIs(t, err, backup.ErrWrongIdentity)

	// The live install moves on after the set was taken.
	exec1(t, db, `DELETE FROM server_settings WHERE key = 'invented_two'`)
	exec1(t, db, `DELETE FROM backup_runs`)
	writeFiles(t, attachments, map[string]string{"space-3/later.pdf": "an invented later file"})
	require.NoError(t, os.Remove(filepath.Join(attachments, "space-1", "receipt.pdf")))

	// Something connected to the live database holds the restore off.
	holder, err := pgx.Connect(ctx, db.URL)
	require.NoError(t, err)
	restoreAt := takenAt.Add(time.Hour)
	options := backup.RestoreOptions{
		Set: set, Identities: identities, Database: db, StoragePath: attachments, Now: restoreAt,
		BackUp: func(ctx context.Context) (backup.Set, error) {
			unlock, err := backup.Lock(ctx, db)
			if err != nil {
				return backup.Set{}, err
			}
			defer unlock()
			return backup.Write(ctx, dir, source, []string{identity.Recipient().String()},
				backup.TriggerRestore, restoreAt)
		},
		Migrate: func(ctx context.Context) error {
			restored := openStore(t, db)
			defer restored.Close()
			_, err := restored.Migrate(ctx)
			return err
		},
	}
	_, err = backup.Restore(ctx, options)
	require.ErrorContains(t, err, "other sessions are connected")
	require.NoError(t, holder.Close(ctx))
	require.EqualValues(t, 0, count(t, db, "backup_runs"), "a refused restore changes nothing")

	restored, err := backup.Restore(ctx, options)
	require.NoError(t, err)
	require.NotEmpty(t, restored.PreRestore)
	require.Empty(t, restored.Previous, "the replaced database is dropped once the restore succeeds")
	require.Empty(t, restored.SetAside)

	require.EqualValues(t, 2, count(t, db, "server_settings"))
	require.EqualValues(t, 3, count(t, db, "backup_runs"))
	require.ElementsMatch(t, []string{"space-1/receipt.pdf", "space-2/statement.pdf"}, files(t, attachments))

	// The set taken before the restore holds the state the restore replaced.
	sets, err := backup.List(dir)
	require.NoError(t, err)
	before, err := backup.Resolve(sets, restored.PreRestore)
	require.NoError(t, err)
	require.Equal(t, backup.TriggerRestore, before.Trigger)
	undo, err := backup.Rehearse(ctx, before, identities, db, restoreAt.Add(time.Minute))
	require.NoError(t, err)
	require.EqualValues(t, 1, rows(undo, "server_settings"))
	require.EqualValues(t, 0, rows(undo, "backup_runs"))
	require.Equal(t, 2, undo.Attachments)
}

func TestARestoreThatFailsLeavesTheLiveDatabaseAlone(t *testing.T) {
	db := freshDatabase(t, requireTools(t))
	ctx := t.Context()
	exec1(t, db, `CREATE TABLE kept (id int)`)
	exec1(t, db, `INSERT INTO kept VALUES (1), (2)`)

	dir := t.TempDir()
	setDir := filepath.Join(dir, "2026-03-03_033000_manual")
	require.NoError(t, os.MkdirAll(setDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "database.dump"), []byte("not a dump"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "manifest.json"), []byte(`{"format":1,
		"created_at":"2026-03-03T03:30:00Z","trigger":"manual","encrypted":false,"verified":true,
		"files":{"database":{"path":"database.dump","bytes":10}}}`), 0o600))
	sets, err := backup.List(dir)
	require.NoError(t, err)
	require.Len(t, sets, 1)

	backedUp := false
	_, err = backup.Restore(ctx, backup.RestoreOptions{
		Set: sets[0], Database: db, Now: time.Now(),
		BackUp:  func(context.Context) (backup.Set, error) { backedUp = true; return backup.Set{}, nil },
		Migrate: func(context.Context) error { t.Fatal("nothing to migrate"); return nil },
	})
	require.ErrorContains(t, err, "the live database was not changed")
	require.True(t, backedUp)
	require.EqualValues(t, 2, count(t, db, "kept"))
}
