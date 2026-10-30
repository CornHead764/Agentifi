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
	"github.com/CornHead764/agentifi/backend/internal/storetest"
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
func exec1(t *testing.T, db *sqlitedb.DB, query string, args ...any) {
	t.Helper()
	_, err := db.Write().ExecContext(t.Context(), query, args...)
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

// install is a data directory with a migrated server.db and the households
// asked for, created through the store as the server creates them.
type install struct {
	data  string
	store *store.Store
	ids   []store.SpaceID
}

func newInstall(t *testing.T, households int) *install {
	t.Helper()
	data := t.TempDir()
	st, err := store.OpenSQLite(t.Context(), data, time.Now)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	in := &install{data: data, store: st}
	for range households {
		in.ids = append(in.ids, storetest.NewHousehold(t, st))
	}
	return in
}

func (in *install) household(t *testing.T, i int) *sqlitedb.DB {
	t.Helper()
	db, err := in.store.HouseholdDB(t.Context(), in.ids[i])
	require.NoError(t, err)
	return db
}

func (in *install) rel(i int) string {
	return "households/" + in.ids[i].String() + ".db"
}

func addCategory(t *testing.T, db *sqlitedb.DB, id string) {
	t.Helper()
	exec1(t, db, `INSERT INTO categories (id, name, kind, sort_order, created_at, updated_at) VALUES (?, ?, 'expense', 0, 1, 1)`, id, id)
}

func (in *install) write(t *testing.T, dir string, recipients []string, trigger backup.Trigger, at time.Time) backup.Set {
	t.Helper()
	unlock, err := backup.Lock(t.Context(), in.data)
	require.NoError(t, err)
	defer unlock()
	set, err := backup.Write(t.Context(), dir, in.source(), recipients, trigger, at)
	require.NoError(t, err)
	require.True(t, set.Intact, set.Problem)
	return set
}

func (in *install) source() backup.Source {
	return backup.Source{DataDir: in.data, CredentialKeyID: backup.KeyID("an-invented-credential-key"),
		ObjectKey: backup.ObjectKey("an-invented-credential-key")}
}

func migrateAgain(data string) func(context.Context) error {
	return func(ctx context.Context) error {
		st, err := store.OpenSQLite(ctx, data, time.Now)
		if err != nil {
			return err
		}
		st.Close()
		return nil
	}
}

func TestABackupIsEncryptedRehearsedAndRestored(t *testing.T) {
	ctx := t.Context()
	in := newInstall(t, 1)
	data := in.data
	version, err := in.store.SchemaVersion(ctx)
	require.NoError(t, err)
	// Writers stay open during the backup, so committed WAL contents are
	// exercised, not merely the checkpointed main file.
	server := openFile(t, data, "server.db")
	exec1(t, server, `CREATE TABLE invented_settings (key TEXT); INSERT INTO invented_settings VALUES ('an-invented-plaintext-marker'),('two')`)
	householdRel := in.rel(0)
	household := in.household(t, 0)
	for _, id := range []string{"an-invented-groceries", "fuel", "rent"} {
		addCategory(t, household, id)
	}
	attachments := filepath.Join(data, "attachments")
	writeFiles(t, attachments, map[string]string{"space-1/receipt.pdf": "an invented receipt", "space-2/statement.pdf": "an invented statement"})
	secrets := t.TempDir()
	writeFiles(t, secrets, map[string]string{"SECRET_KEY": "an-invented-secret", "DATA_DIR": "/data/db"})
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	ids := []age.Identity{identity}
	dir := t.TempDir()
	source := in.source()
	source.SecretsDir, source.SchemaVersion = secrets, version
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
	require.Equal(t, backup.FormatVersion, set.Format)
	require.Equal(t, version, set.SchemaVersion)
	require.Equal(t, 1, set.Files[backup.PartDatabase].Count)
	require.EqualValues(t, 2, set.Files[backup.PartDatabase].Tables["invented_settings"])
	require.Len(t, set.Households, 1)
	require.Equal(t, in.ids[0].String(), set.Households[0].ID)
	require.Equal(t, set.Name, set.Households[0].TakenIn)
	require.Positive(t, set.Households[0].Tables["categories"])
	require.Equal(t, 2, set.Files[backup.PartAttachments].Count)
	require.Len(t, set.Objects, 2)
	require.Equal(t, []string{"DATA_DIR", "SECRET_KEY"}, set.Files[backup.PartSecrets].Names)
	stored := []string{}
	for _, file := range set.Files {
		require.True(t, strings.HasSuffix(file.Path, ".age"))
		stored = append(stored, filepath.Join(set.Dir, file.Path))
	}
	for _, object := range files(t, filepath.Join(dir, "objects")) {
		stored = append(stored, filepath.Join(dir, "objects", object))
	}
	require.Len(t, stored, 3+1+2)
	for _, path := range stored {
		body, err := os.ReadFile(path)
		require.NoError(t, err)
		require.True(t, strings.HasPrefix(string(body), "age-encryption.org/v1"), path)
		require.NotContains(t, string(body), "an-invented")
		info, err := os.Stat(path)
		require.NoError(t, err)
		require.Equal(t, os.FileMode(0600), info.Mode().Perm())
	}
	manifest, err := os.ReadFile(filepath.Join(set.Dir, "manifest.json"))
	require.NoError(t, err)
	require.NotContains(t, string(manifest), "receipt")
	report, err := backup.Rehearse(ctx, set, ids, data, takenAt)
	require.NoError(t, err)
	require.EqualValues(t, 2, rows(report, "server.db:invented_settings"))
	categories := rows(report, householdRel+":categories")
	require.Positive(t, categories)
	require.Equal(t, 2, report.Attachments)
	require.Equal(t, []string{"DATA_DIR", "SECRET_KEY"}, report.Secrets)
	stranger, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	_, err = backup.Rehearse(ctx, set, []age.Identity{stranger}, data, takenAt)
	require.ErrorIs(t, err, backup.ErrWrongIdentity)
	exec1(t, server, `DELETE FROM invented_settings WHERE key='two'`)
	exec1(t, household, `DELETE FROM categories WHERE id IN ('fuel', 'rent')`)
	writeFiles(t, attachments, map[string]string{"space-3/later.pdf": "an invented later file"})
	require.NoError(t, os.Remove(filepath.Join(attachments, "space-1/receipt.pdf")))
	require.NoError(t, server.Close())
	in.store.Close()
	holder, err := sqlitedb.AcquireLock(filepath.Join(data, "server.lock"))
	require.NoError(t, err)
	restoreAt := takenAt.Add(time.Hour)
	opts := backup.RestoreOptions{Set: set, Identities: ids, DataDir: data, Now: restoreAt,
		BackUp: func(ctx context.Context) (backup.Set, error) {
			return backup.Write(ctx, dir, source, []string{identity.Recipient().String()}, backup.TriggerRestore, restoreAt)
		},
		Migrate: migrateAgain(data)}
	_, err = backup.Restore(ctx, opts)
	require.ErrorContains(t, err, "stop the server")
	require.NoError(t, holder.Close())
	require.EqualValues(t, categories-2, count(t, data, householdRel, "categories"))
	restored, err := backup.Restore(ctx, opts)
	require.NoError(t, err)
	require.NotEmpty(t, restored.PreRestore)
	require.DirExists(t, restored.Previous)
	require.Empty(t, restored.SetAside)
	require.EqualValues(t, 2, count(t, data, "server.db", "invented_settings"))
	require.EqualValues(t, categories, count(t, data, householdRel, "categories"))
	require.ElementsMatch(t, []string{"space-1/receipt.pdf", "space-2/statement.pdf"}, files(t, attachments))
	for _, name := range files(t, data) {
		require.NotContains(t, name, "-before-migration-", "checking a snapshot through the store leaves nothing behind")
	}
	sets, err := backup.List(dir)
	require.NoError(t, err)
	before, err := backup.Resolve(sets, restored.PreRestore)
	require.NoError(t, err)
	require.Equal(t, backup.TriggerRestore, before.Trigger)
	undo, err := backup.Rehearse(ctx, before, ids, data, restoreAt)
	require.NoError(t, err)
	require.EqualValues(t, 1, rows(undo, "server.db:invented_settings"))
	require.EqualValues(t, categories-2, rows(undo, householdRel+":categories"))
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
