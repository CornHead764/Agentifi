package backup_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"filippo.io/age"
	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/stretchr/testify/require"
)

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
		p := filepath.Join(root, filepath.FromSlash(name))
		require.NoError(t, os.MkdirAll(filepath.Dir(p), 0700))
		require.NoError(t, os.WriteFile(p, []byte(body), 0600))
	}
}
func files(t *testing.T, root string) []string {
	t.Helper()
	var out []string
	require.NoError(t, filepath.Walk(root, func(p string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			rel, _ := filepath.Rel(root, p)
			out = append(out, filepath.ToSlash(rel))
		}
		return nil
	}))
	return out
}
func openFile(t *testing.T, dir, rel string) *sqlitedb.DB {
	t.Helper()
	db, err := sqlitedb.Open(t.Context(), filepath.Join(dir, rel), time.Now)
	require.NoError(t, err)
	t.Cleanup(func() { _ = db.Close() })
	return db
}
func exec1(t *testing.T, db *sqlitedb.DB, query string) {
	t.Helper()
	_, err := db.Write().ExecContext(t.Context(), query)
	require.NoError(t, err)
}
func count(t *testing.T, dir, rel, table string) int64 {
	t.Helper()
	db := openFile(t, dir, rel)
	var n int64
	require.NoError(t, db.Read().QueryRowContext(t.Context(), `SELECT count(*) FROM "`+table+`"`).Scan(&n))
	require.NoError(t, db.Close())
	return n
}

func TestABackupIsEncryptedRehearsedAndRestored(t *testing.T) {
	ctx := t.Context()
	data := t.TempDir()
	// Open a real migrated store, then leave writers open during the backup to
	// exercise committed WAL contents, not merely the checkpointed main file.
	st, err := store.OpenSQLite(ctx, data, time.Now)
	require.NoError(t, err)
	version, err := st.SchemaVersion(ctx)
	require.NoError(t, err)
	server := openFile(t, data, "server.db")
	exec1(t, server, `CREATE TABLE invented_settings (key TEXT); INSERT INTO invented_settings VALUES ('an-invented-plaintext-marker'),('two')`)
	householdRel := "households/11111111-1111-1111-1111-111111111111.db"
	household := openFile(t, data, householdRel)
	exec1(t, household, `CREATE TABLE invented_runs (id INTEGER); INSERT INTO invented_runs VALUES (1),(2),(3)`)
	attachments := filepath.Join(data, "attachments")
	writeFiles(t, attachments, map[string]string{"space-1/receipt.pdf": "an invented receipt", "space-2/statement.pdf": "an invented statement"})
	secrets := t.TempDir()
	writeFiles(t, secrets, map[string]string{"SECRET_KEY": "an-invented-secret", "DATA_DIR": "/data/db"})
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	ids := []age.Identity{identity}
	dir := t.TempDir()
	source := backup.Source{DataDir: data, SecretsDir: secrets, CredentialKeyID: backup.KeyID("an-invented-credential-key"), SchemaVersion: version}
	takenAt := time.Date(2026, 3, 3, 3, 30, 0, 0, time.Local)
	unlock, err := backup.Lock(ctx, data)
	require.NoError(t, err)
	set, err := backup.Write(ctx, dir, source, []string{identity.Recipient().String()}, backup.TriggerManual, takenAt)
	unlock()
	require.NoError(t, err)
	require.Equal(t, "2026-03-03_033000_manual", set.Name)
	require.True(t, set.Intact, set.Problem)
	require.True(t, set.Encrypted)
	require.True(t, set.Verified)
	require.Equal(t, 2, set.Format)
	require.Equal(t, version, set.SchemaVersion)
	require.Equal(t, 2, set.Files[backup.PartDatabase].Count)
	require.Equal(t, 2, set.Files[backup.PartAttachments].Count)
	require.Equal(t, []string{"DATA_DIR", "SECRET_KEY"}, set.Files[backup.PartSecrets].Names)
	for _, file := range set.Files {
		require.True(t, strings.HasSuffix(file.Path, ".age"))
		body, err := os.ReadFile(filepath.Join(set.Dir, file.Path))
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(body), "age-encryption.org/v1"))
		require.NotContains(t, string(body), "an-invented")
		info, err := os.Stat(filepath.Join(set.Dir, file.Path))
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	report, err := backup.Rehearse(ctx, set, ids, data, takenAt)
	require.NoError(t, err)
	require.EqualValues(t, 2, rows(report, "server.db:invented_settings"))
	require.EqualValues(t, 3, rows(report, householdRel+":invented_runs"))
	require.Equal(t, 2, report.Attachments)
	require.Equal(t, []string{"DATA_DIR", "SECRET_KEY"}, report.Secrets)
	stranger, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	_, err = backup.Rehearse(ctx, set, []age.Identity{stranger}, data, takenAt)
	require.ErrorIs(t, err, backup.ErrWrongIdentity)
	exec1(t, server, `DELETE FROM invented_settings WHERE key='two'`)
	exec1(t, household, `DELETE FROM invented_runs`)
	writeFiles(t, attachments, map[string]string{"space-3/later.pdf": "an invented later file"})
	require.NoError(t, os.Remove(filepath.Join(attachments, "space-1/receipt.pdf")))
	require.NoError(t, server.Close())
	require.NoError(t, household.Close())
	st.Close()
	holder, err := sqlitedb.AcquireLock(filepath.Join(data, "server.lock"))
	require.NoError(t, err)
	restoreAt := takenAt.Add(time.Hour)
	opts := backup.RestoreOptions{Set: set, Identities: ids, DataDir: data, Now: restoreAt,
		BackUp: func(ctx context.Context) (backup.Set, error) {
			return backup.Write(ctx, dir, source, []string{identity.Recipient().String()}, backup.TriggerRestore, restoreAt)
		},
		Migrate: func(ctx context.Context) error {
			st, err := store.OpenSQLite(ctx, data, time.Now)
			if err != nil {
				return err
			}
			st.Close()
			return nil
		}}
	_, err = backup.Restore(ctx, opts)
	require.ErrorContains(t, err, "stop the server")
	require.NoError(t, holder.Close())
	require.EqualValues(t, 0, count(t, data, householdRel, "invented_runs"))
	restored, err := backup.Restore(ctx, opts)
	require.NoError(t, err)
	require.NotEmpty(t, restored.PreRestore)
	require.DirExists(t, restored.Previous)
	require.Empty(t, restored.SetAside)
	require.EqualValues(t, 2, count(t, data, "server.db", "invented_settings"))
	require.EqualValues(t, 3, count(t, data, householdRel, "invented_runs"))
	require.ElementsMatch(t, []string{"space-1/receipt.pdf", "space-2/statement.pdf"}, files(t, attachments))
	sets, err := backup.List(dir)
	require.NoError(t, err)
	before, err := backup.Resolve(sets, restored.PreRestore)
	require.NoError(t, err)
	require.Equal(t, backup.TriggerRestore, before.Trigger)
	undo, err := backup.Rehearse(ctx, before, ids, data, restoreAt)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows(undo, "server.db:invented_settings"))
	require.EqualValues(t, 0, rows(undo, householdRel+":invented_runs"))
	require.Equal(t, 2, undo.Attachments)
}

func TestARestoreThatFailsLeavesTheLiveDatabaseAlone(t *testing.T) {
	data := t.TempDir()
	db := openFile(t, data, "server.db")
	exec1(t, db, `CREATE TABLE kept (id int); INSERT INTO kept VALUES (1),(2)`)
	require.NoError(t, db.Close())
	dir := t.TempDir()
	setDir := filepath.Join(dir, "bad")
	require.NoError(t, os.Mkdir(setDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "database.tar.gz"), []byte("not a dump"), 0600))
	manifest := backup.Manifest{Format: backup.FormatVersion, Name: "bad", Files: map[backup.Part]backup.File{backup.PartDatabase: {Path: "database.tar.gz", Bytes: 10}}}
	body, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(setDir, "manifest.json"), body, 0600))
	sets, err := backup.List(dir)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	backedUp := false
	_, err = backup.Restore(t.Context(), backup.RestoreOptions{Set: sets[0], DataDir: data, Now: time.Now(), BackUp: func(context.Context) (backup.Set, error) { backedUp = true; return backup.Set{}, nil }, Migrate: func(context.Context) error { t.Fatal("nothing to migrate"); return nil }})
	require.Error(t, err)
	require.False(t, backedUp, "corrupt sets are rejected before backing up or touching live data")
	require.EqualValues(t, 2, count(t, data, "server.db", "kept"))
}

func TestOldPostgresSetsAreRefused(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "old")
	require.NoError(t, os.Mkdir(p, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(p, "manifest.json"), []byte(`{"format":1,"files":{"database":{"path":"database.dump","bytes":0}}}`), 0600))
	sets, err := backup.List(dir)
	require.NoError(t, err)
	require.Len(t, sets, 1)
	require.False(t, sets[0].Intact)
	require.Contains(t, sets[0].Problem, "previous release")
	require.Contains(t, sets[0].Problem, "import-postgres")
	_, err = backup.Restore(t.Context(), backup.RestoreOptions{Set: sets[0], DataDir: t.TempDir(), Now: time.Now()})
	require.ErrorContains(t, err, "previous release")
}
