package backup_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A whole backup and restore against a real SQLite file: snapshot, encrypt,
// rehearse into a scratch file, restore over the live one, and check the rows
// and the attachments came back.

func liveDatabase(t *testing.T) backup.SQLite {
	t.Helper()
	return backup.SQLite{Path: filepath.Join(t.TempDir(), "agentifi.db")}
}

func openStore(t *testing.T, db backup.SQLite) *store.Store {
	t.Helper()
	st, err := store.Open(t.Context(), db.Path)
	require.NoError(t, err)
	return st
}

func exec1(t *testing.T, db backup.SQLite, sql string, args ...any) {
	t.Helper()
	conn, err := sqlitedb.Open(t.Context(), db.Path)
	require.NoError(t, err)
	defer conn.Close()
	_, err = conn.Exec(t.Context(), sql, args...)
	require.NoError(t, err)
}

func count(t *testing.T, db backup.SQLite, table string) int64 {
	t.Helper()
	conn, err := sqlitedb.Open(t.Context(), db.Path)
	require.NoError(t, err)
	defer conn.Close()
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

// beside is what is in the database's directory other than the database's
// own files and the lock.
func beside(t *testing.T, db backup.SQLite) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Dir(db.Path))
	require.NoError(t, err)
	base := filepath.Base(db.Path)
	var out []string
	for _, entry := range entries {
		switch entry.Name() {
		case base, base + "-wal", base + "-shm", base + ".backup-lock":
		default:
			out = append(out, entry.Name())
		}
	}
	return out
}

func TestABackupIsEncryptedRehearsedAndRestored(t *testing.T) {
	db := liveDatabase(t)
	ctx := t.Context()

	// The server's own handle stays open throughout the backup, as it does
	// under a running server.
	st := openStore(t, db)
	_, err := st.Migrate(ctx)
	require.NoError(t, err)
	version, err := st.SchemaVersion(ctx)
	require.NoError(t, err)

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
	writeFiles(t, secrets, map[string]string{"SECRET_KEY": "an-invented-secret", "CAMOUFOX_URL": "ws://x"})

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
	st.Close()

	require.Equal(t, "2026-03-03_033000_manual", set.Name)
	require.True(t, set.Intact, set.Problem)
	require.True(t, set.Encrypted)
	require.True(t, set.Verified)
	require.Equal(t, version, set.SchemaVersion)
	require.NotEmpty(t, set.ServerVersion)
	require.Equal(t, "database.sqlite.age", set.Files[backup.PartDatabase].Path)
	require.Equal(t, 2, set.Files[backup.PartAttachments].Count)
	require.Equal(t, []string{"CAMOUFOX_URL", "SECRET_KEY"}, set.Files[backup.PartSecrets].Names)
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
	require.Empty(t, beside(t, db), "the plaintext snapshot is removed once it is sealed")

	// A rehearsal reads everything back and leaves the live database alone.
	report, err := backup.Rehearse(ctx, set, identities, db, takenAt)
	require.NoError(t, err)
	require.EqualValues(t, 2, rows(report, "server_settings"))
	require.EqualValues(t, 3, rows(report, "backup_runs"))
	require.Positive(t, rows(report, "goose_db_version"), "the migrations table comes back with the rest")
	require.Equal(t, 2, report.Attachments)
	require.Equal(t, []string{"CAMOUFOX_URL", "SECRET_KEY"}, report.Secrets)
	require.Empty(t, beside(t, db), "the rehearsal's scratch file is removed")

	stranger, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	_, err = backup.Rehearse(ctx, set, []age.Identity{stranger}, db, takenAt)
	require.ErrorIs(t, err, backup.ErrWrongIdentity)

	// The live install moves on after the set was taken.
	exec1(t, db, `DELETE FROM server_settings WHERE key = 'invented_two'`)
	exec1(t, db, `DELETE FROM backup_runs`)
	writeFiles(t, attachments, map[string]string{"space-3/later.pdf": "an invented later file"})
	require.NoError(t, os.Remove(filepath.Join(attachments, "space-1", "receipt.pdf")))

	// Something with the live database open holds the restore off.
	holder, err := sqlitedb.Open(ctx, db.Path)
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
	require.ErrorContains(t, err, "something else has")
	require.NoError(t, holder.Close())
	require.EqualValues(t, 0, count(t, db, "backup_runs"), "a refused restore changes nothing")

	restored, err := backup.Restore(ctx, options)
	require.NoError(t, err)
	require.NotEmpty(t, restored.PreRestore)
	require.Empty(t, restored.Previous, "the replaced database is removed once the restore succeeds")
	require.Empty(t, restored.SetAside)
	require.EqualValues(t, 2, rows(restored, "server_settings"))

	require.EqualValues(t, 2, count(t, db, "server_settings"))
	require.EqualValues(t, 3, count(t, db, "backup_runs"))
	require.ElementsMatch(t, []string{"space-1/receipt.pdf", "space-2/statement.pdf"}, files(t, attachments))
	require.Empty(t, beside(t, db), "nothing is left beside the restored database")

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

func TestKeepPreviousKeepsTheReplacedDatabase(t *testing.T) {
	db := liveDatabase(t)
	ctx := t.Context()
	exec1(t, db, `CREATE TABLE invented (id INTEGER)`)
	exec1(t, db, `INSERT INTO invented VALUES (1), (2), (3)`)

	dir := t.TempDir()
	unlock, err := backup.Lock(ctx, db)
	require.NoError(t, err)
	set, err := backup.Write(ctx, dir, backup.Source{Database: db}, nil, backup.TriggerManual, time.Now())
	unlock()
	require.NoError(t, err)
	require.False(t, set.Encrypted)
	exec1(t, db, `DELETE FROM invented`)

	report, err := backup.Restore(ctx, backup.RestoreOptions{
		Set: set, Database: db, KeepPrevious: true, Now: time.Now(),
		BackUp:  func(context.Context) (backup.Set, error) { return backup.Set{}, nil },
		Migrate: func(context.Context) error { return nil },
	})
	require.NoError(t, err)
	require.EqualValues(t, 3, count(t, db, "invented"))
	require.NotEmpty(t, report.Previous)
	require.EqualValues(t, 0, count(t, backup.SQLite{Path: report.Previous}, "invented"))
}

func TestARestoreThatFailsLeavesTheLiveDatabaseAlone(t *testing.T) {
	db := liveDatabase(t)
	ctx := t.Context()
	exec1(t, db, `CREATE TABLE kept (id INTEGER)`)
	exec1(t, db, `INSERT INTO kept VALUES (1), (2)`)

	dir := t.TempDir()
	setDir := filepath.Join(dir, "2026-03-03_033000_manual")
	require.NoError(t, os.MkdirAll(setDir, 0o700))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "database.sqlite"), []byte("not a database"), 0o600))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "manifest.json"), []byte(`{"format":2,
		"created_at":"2026-03-03T03:30:00Z","trigger":"manual","encrypted":false,"verified":true,
		"files":{"database":{"path":"database.sqlite","bytes":14}}}`), 0o600))
	sets, err := backup.List(dir)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	require.True(t, sets[0].Intact, sets[0].Problem)

	backedUp := false
	_, err = backup.Restore(ctx, backup.RestoreOptions{
		Set: sets[0], Database: db, Now: time.Now(),
		BackUp:  func(context.Context) (backup.Set, error) { backedUp = true; return backup.Set{}, nil },
		Migrate: func(context.Context) error { t.Fatal("nothing to migrate"); return nil },
	})
	require.ErrorContains(t, err, "the live database was not changed")
	require.True(t, backedUp)
	require.EqualValues(t, 2, count(t, db, "kept"))
	require.Empty(t, beside(t, db), "the scratch file is removed")
}

func TestTheBackupLockIsHeldByOneAtATime(t *testing.T) {
	db := liveDatabase(t)
	unlock, err := backup.Lock(t.Context(), db)
	require.NoError(t, err)

	waiting, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	_, err = backup.Lock(waiting, db)
	require.ErrorIs(t, err, context.DeadlineExceeded)

	unlock()
	again, err := backup.Lock(t.Context(), db)
	require.NoError(t, err)
	again()
}

func TestNoSetIsTakenOfADatabaseThatIsNotThere(t *testing.T) {
	db := liveDatabase(t)
	_, err := backup.Write(t.Context(), t.TempDir(), backup.Source{Database: db}, nil, backup.TriggerManual, time.Now())
	require.ErrorContains(t, err, "the database file")
	_, err = os.Stat(db.Path)
	require.ErrorIs(t, err, os.ErrNotExist, "checking for the database does not create it")
}
