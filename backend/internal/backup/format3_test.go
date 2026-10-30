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
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func at(day int) time.Time {
	return time.Date(2026, 4, day, 3, 30, 0, 0, time.Local)
}

func objects(t *testing.T, dir string) []string {
	t.Helper()
	if _, err := os.Stat(filepath.Join(dir, "objects")); err != nil {
		return nil
	}
	return files(t, filepath.Join(dir, "objects"))
}

func objectExists(dir, name string) bool {
	_, err := os.Stat(filepath.Join(dir, "objects", name[:2], name))
	return err == nil
}

func household(t *testing.T, set backup.Set, id store.SpaceID) backup.Household {
	t.Helper()
	one, ok := set.Household(id.String())
	require.True(t, ok, "set %s holds household %s", set.Name, id)
	return one
}

func listed(t *testing.T, dir, name string) backup.Set {
	t.Helper()
	sets, err := backup.List(dir)
	require.NoError(t, err)
	for _, set := range sets {
		if set.Name == name {
			return set
		}
	}
	t.Fatalf("no set %s", name)
	return backup.Set{}
}

func addSubscription(t *testing.T, db *sqlitedb.DB, id string) {
	t.Helper()
	exec1(t, db, `INSERT INTO push_subscriptions (id, user_id, endpoint, p256dh, auth, created_at) VALUES (?, 'user', ?, 'key', 'auth', 1)`,
		id, "https://push.invalid/"+id)
}

func TestAnUnchangedHouseholdIsNotSnapshottedAgain(t *testing.T) {
	in := newInstall(t, 2)
	addCategory(t, in.household(t, 0), "groceries")
	addCategory(t, in.household(t, 1), "fuel")
	attachments := filepath.Join(in.data, "attachments")
	writeFiles(t, attachments, map[string]string{
		in.ids[0].String() + "/receipt.pdf": "an invented receipt",
		in.ids[1].String() + "/copy.pdf":    "an invented receipt",
	})
	dir := t.TempDir()

	first := in.write(t, dir, nil, backup.TriggerNightly, at(1))
	require.Len(t, first.Objects, 1, "two attachments with the same contents are one object")
	require.Len(t, objects(t, dir), 3)

	second := in.write(t, dir, nil, backup.TriggerNightly, at(2))
	for i := range in.ids {
		require.Equal(t, household(t, first, in.ids[i]), household(t, second, in.ids[i]))
	}
	require.Equal(t, first.Objects, second.Objects)
	require.Len(t, objects(t, dir), 3, "nothing unchanged is copied again")

	// push_subscriptions is not synced to clients, so only write_counter
	// says the file changed.
	addSubscription(t, in.household(t, 0), "phone")
	writeFiles(t, attachments, map[string]string{in.ids[0].String() + "/later.pdf": "an invented later file"})
	third := in.write(t, dir, nil, backup.TriggerNightly, at(3))
	require.Equal(t, third.Name, household(t, third, in.ids[0]).TakenIn)
	require.Equal(t, first.Name, household(t, third, in.ids[1]).TakenIn)
	require.Len(t, third.Objects, 2)
	require.Len(t, objects(t, dir), 5)

	report, err := backup.Rehearse(t.Context(), third, nil, in.data, at(3))
	require.NoError(t, err)
	require.EqualValues(t, 1, rows(report, in.rel(0)+":push_subscriptions"))
	require.Equal(t, 3, report.Attachments)

	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	sealed := in.write(t, dir, []string{identity.Recipient().String()}, backup.TriggerManual, at(4))
	for i := range in.ids {
		require.Equal(t, sealed.Name, household(t, sealed, in.ids[i]).TakenIn,
			"a snapshot sealed to other keys is not named by a set that lists these")
	}
	for name := range sealed.Objects {
		_, earlier := third.Objects[name]
		require.False(t, earlier)
	}
	report, err = backup.Rehearse(t.Context(), sealed, []age.Identity{identity}, in.data, at(4))
	require.NoError(t, err)
	require.Equal(t, 3, report.Attachments)
}

func TestRetentionNeverRemovesWhatAKeptSetNames(t *testing.T) {
	in := newInstall(t, 2)
	addCategory(t, in.household(t, 0), "groceries")
	addCategory(t, in.household(t, 1), "fuel")
	attachments := filepath.Join(in.data, "attachments")
	writeFiles(t, attachments, map[string]string{in.ids[1].String() + "/gone.pdf": "an invented file deleted later"})
	dir := t.TempDir()

	oldest := in.write(t, dir, nil, backup.TriggerNightly, at(1))
	require.NoError(t, os.Remove(filepath.Join(attachments, in.ids[1].String(), "gone.pdf")))
	addCategory(t, in.household(t, 1), "rent")
	older := in.write(t, dir, nil, backup.TriggerNightly, at(10))
	kept := in.write(t, dir, nil, backup.TriggerNightly, at(25))
	newest := in.write(t, dir, nil, backup.TriggerNightly, at(29))

	unchanged := household(t, newest, in.ids[0])
	require.Equal(t, oldest.Name, unchanged.TakenIn, "household 0 has not changed since the oldest set")
	require.Equal(t, older.Name, household(t, newest, in.ids[1]).TakenIn)
	firstOfChanged := household(t, oldest, in.ids[1]).Object
	var gone string
	for name := range oldest.Objects {
		gone = name
	}

	removed, err := backup.Prune(t.Context(), dir, at(30), 14)
	require.NoError(t, err)
	require.ElementsMatch(t, []string{oldest.Name, older.Name}, removed)
	require.True(t, objectExists(dir, unchanged.Object), "the snapshot kept sets name outlives the set that took it")
	require.False(t, objectExists(dir, firstOfChanged), "a snapshot only removed sets named is collected")
	require.False(t, objectExists(dir, gone), "an attachment only removed sets named is collected")
	for _, name := range []string{kept.Name, newest.Name} {
		set := listed(t, dir, name)
		require.True(t, set.Intact, set.Problem)
		report, err := backup.Rehearse(t.Context(), set, nil, in.data, at(30))
		require.NoError(t, err)
		require.Positive(t, rows(report, in.rel(0)+":categories"))
		require.EqualValues(t, rows(report, in.rel(1)+":categories"), count(t, in.data, in.rel(1), "categories"))
	}

	// A set whose manifest cannot be read might name anything.
	orphan := "ab" + strings.Repeat("0", 62)
	writeFiles(t, filepath.Join(dir, "objects"), map[string]string{orphan[:2] + "/" + orphan: "unnamed", ".partial-123": "half"})
	writeFiles(t, dir, map[string]string{"2026-04-28_033000_manual/manifest.json": "{"})
	_, err = backup.CollectObjects(t.Context(), dir)
	require.ErrorContains(t, err, "cannot be read")
	require.True(t, objectExists(dir, orphan))
	require.NoError(t, os.RemoveAll(filepath.Join(dir, "2026-04-28_033000_manual")))
	collected, err := backup.CollectObjects(t.Context(), dir)
	require.NoError(t, err)
	require.Equal(t, 1, collected)
	require.False(t, objectExists(dir, orphan))
	require.NoFileExists(t, filepath.Join(dir, "objects", ".partial-123"))
	require.True(t, listed(t, dir, newest.Name).Intact)
}

func TestOneHouseholdIsRestoredAlone(t *testing.T) {
	ctx := t.Context()
	in := newInstall(t, 2)
	server := openFile(t, in.data, "server.db")
	exec1(t, server, `CREATE TABLE invented_settings (key TEXT); INSERT INTO invented_settings VALUES ('kept')`)
	for _, id := range []string{"groceries", "fuel"} {
		addCategory(t, in.household(t, 0), id)
	}
	attachments := filepath.Join(in.data, "attachments")
	mine, theirs := in.ids[0].String(), in.ids[1].String()
	writeFiles(t, attachments, map[string]string{mine + "/receipt.pdf": "an invented receipt", theirs + "/statement.pdf": "an invented statement"})
	dir := t.TempDir()
	set := in.write(t, dir, nil, backup.TriggerNightly, at(1))
	categories := count(t, in.data, in.rel(0), "categories")

	report, err := backup.RehearseHousehold(ctx, set, nil, mine)
	require.NoError(t, err)
	require.EqualValues(t, categories, rows(report, in.rel(0)+":categories"))
	require.EqualValues(t, -1, rows(report, "server.db:invented_settings"))
	require.EqualValues(t, -1, rows(report, in.rel(1)+":categories"))
	require.Equal(t, 1, report.Attachments)

	exec1(t, in.household(t, 0), `DELETE FROM categories WHERE id IN ('groceries', 'fuel')`)
	addCategory(t, in.household(t, 1), "later")
	exec1(t, server, `INSERT INTO invented_settings VALUES ('later')`)
	writeFiles(t, attachments, map[string]string{
		mine + "/receipt.pdf": "a changed receipt", mine + "/new.pdf": "an invented new file",
		theirs + "/statement.pdf": "a changed statement",
	})
	theirCategories := count(t, in.data, in.rel(1), "categories")
	require.NoError(t, server.Close())
	in.store.Close()

	_, err = backup.PlanRestore(backup.RestoreRequest{Set: set, Confirm: set.Name, Household: uuid.NewString(),
		CurrentKeyID: set.CredentialKeyID})
	require.ErrorContains(t, err, "holds no household")
	_, err = backup.Restore(ctx, backup.RestoreOptions{Set: set, DataDir: in.data, Now: at(2), Household: uuid.NewString()})
	require.ErrorContains(t, err, "has no household")

	plan, err := backup.PlanRestore(backup.RestoreRequest{Set: set, Confirm: set.Name, Household: mine,
		CurrentKeyID: set.CredentialKeyID})
	require.NoError(t, err)
	require.Contains(t, plan.Steps, "set DATA_DIR/attachments/"+mine+" aside and move the restored one in")
	restored, err := backup.Restore(ctx, backup.RestoreOptions{Set: set, DataDir: in.data, Now: at(2), Household: mine,
		BackUp: func(ctx context.Context) (backup.Set, error) {
			return backup.Write(ctx, dir, in.source(), nil, backup.TriggerRestore, at(2))
		},
		Migrate: migrateAgain(in.data)})
	require.NoError(t, err)
	require.Equal(t, mine, restored.Household)
	require.NotEmpty(t, restored.PreRestore)
	require.FileExists(t, filepath.Join(restored.Previous, "households", mine+".db"))
	require.EqualValues(t, categories, count(t, in.data, in.rel(0), "categories"))
	require.EqualValues(t, theirCategories, count(t, in.data, in.rel(1), "categories"), "the other household is not touched")
	require.EqualValues(t, 2, count(t, in.data, "server.db", "invented_settings"), "server.db is not touched")
	require.Equal(t, map[string]string{
		mine + "/receipt.pdf": "an invented receipt", theirs + "/statement.pdf": "a changed statement",
	}, readTree(t, attachments))

	before := listed(t, dir, restored.PreRestore)
	undo, err := backup.RehearseHousehold(ctx, before, nil, mine)
	require.NoError(t, err)
	require.EqualValues(t, categories-2, rows(undo, in.rel(0)+":categories"))
	require.Equal(t, 2, undo.Attachments)
}

func readTree(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	for _, name := range files(t, root) {
		body, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(name)))
		require.NoError(t, err)
		out[name] = string(body)
	}
	return out
}

func TestFormat2SetsStayRestorable(t *testing.T) {
	ctx := t.Context()
	in := newInstall(t, 2)
	for _, id := range []string{"groceries", "fuel"} {
		addCategory(t, in.household(t, 0), id)
	}
	addCategory(t, in.household(t, 1), "rent")
	attachments := filepath.Join(in.data, "attachments")
	mine := in.ids[0].String()
	writeFiles(t, attachments, map[string]string{mine + "/receipt.pdf": "an invented receipt", in.ids[1].String() + "/b.pdf": "b"})
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	ids := []age.Identity{identity}
	keys := []string{identity.Recipient().String()}
	dir := t.TempDir()
	unlock, err := backup.Lock(ctx, in.data)
	require.NoError(t, err)
	_, err = backup.WriteFormat2(ctx, dir, in.source(), keys, backup.TriggerNightly, at(1))
	unlock()
	require.NoError(t, err)
	old := listed(t, dir, backup.NewName(at(1), backup.TriggerNightly))
	require.Equal(t, 2, old.Format)
	require.True(t, old.Intact, old.Problem)

	report, err := backup.Rehearse(ctx, old, ids, in.data, at(1))
	require.NoError(t, err)
	require.Equal(t, 2, report.Attachments)
	categories := rows(report, in.rel(0)+":categories")
	require.Positive(t, categories)

	next := in.write(t, dir, keys, backup.TriggerNightly, at(2))
	require.Equal(t, next.Name, household(t, next, in.ids[0]).TakenIn, "a format-2 set's snapshot is not one to name")

	exec1(t, in.household(t, 0), `DELETE FROM categories WHERE id IN ('groceries', 'fuel')`)
	addCategory(t, in.household(t, 1), "later")
	in.store.Close()
	restored, err := backup.Restore(ctx, backup.RestoreOptions{Set: old, Identities: ids, DataDir: in.data, Now: at(3),
		Household: mine, Migrate: migrateAgain(in.data)})
	require.NoError(t, err)
	require.Equal(t, 1, restored.Attachments)
	require.EqualValues(t, categories, count(t, in.data, in.rel(0), "categories"))
	require.EqualValues(t, rows(report, in.rel(1)+":categories")+1, count(t, in.data, in.rel(1), "categories"))

	_, err = backup.Restore(ctx, backup.RestoreOptions{Set: old, Identities: ids, DataDir: in.data, Now: at(4),
		Migrate: migrateAgain(in.data)})
	require.NoError(t, err)
	require.EqualValues(t, rows(report, in.rel(1)+":categories"), count(t, in.data, in.rel(1), "categories"))

	removed, err := backup.Prune(t.Context(), dir, at(30), 14)
	require.NoError(t, err)
	require.Equal(t, []string{old.Name}, removed, "the newest intact set is kept whatever its age")
	require.True(t, listed(t, dir, next.Name).Intact)
}

// copySet copies a set's own files under another name, beside it, with its
// manifest changed.
func copySet(t *testing.T, set backup.Set, name string, change func(*backup.Manifest)) backup.Set {
	t.Helper()
	target := filepath.Join(filepath.Dir(set.Dir), name)
	require.NoError(t, os.Mkdir(target, 0o700))
	for _, file := range set.Files {
		body, err := os.ReadFile(filepath.Join(set.Dir, file.Path))
		require.NoError(t, err)
		require.NoError(t, os.WriteFile(filepath.Join(target, file.Path), body, 0o600))
	}
	manifest := set.Manifest
	manifest.Households = append([]backup.Household(nil), set.Households...)
	change(&manifest)
	body, err := json.Marshal(manifest)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(target, "manifest.json"), body, 0o600))
	return listed(t, filepath.Dir(set.Dir), name)
}

func TestARehearsalChecksRowCountsAndWhoseHouseholdEachFileIs(t *testing.T) {
	in := newInstall(t, 2)
	addCategory(t, in.household(t, 0), "groceries")
	addCategory(t, in.household(t, 1), "fuel")
	dir := t.TempDir()
	set := in.write(t, dir, nil, backup.TriggerNightly, at(1))
	_, err := backup.Rehearse(t.Context(), set, nil, in.data, at(1))
	require.NoError(t, err)

	miscounted := copySet(t, set, "miscounted", func(m *backup.Manifest) {
		tables := map[string]int64{}
		for table, rows := range m.Households[0].Tables {
			tables[table] = rows
		}
		tables["categories"]++
		m.Households[0].Tables = tables
	})
	require.True(t, miscounted.Intact, miscounted.Problem)
	_, err = backup.Rehearse(t.Context(), miscounted, nil, in.data, at(1))
	require.ErrorContains(t, err, "the set recorded")

	swapped := copySet(t, set, "swapped", func(m *backup.Manifest) {
		m.Households[0].ID, m.Households[1].ID = m.Households[1].ID, m.Households[0].ID
	})
	require.True(t, swapped.Intact, swapped.Problem)
	_, err = backup.Rehearse(t.Context(), swapped, nil, in.data, at(1))
	require.ErrorContains(t, err, "refusing to open it")

	escaping := copySet(t, set, "escaping", func(m *backup.Manifest) { m.Households[0].ID = "../server" })
	require.False(t, escaping.Intact)

	require.NoError(t, os.WriteFile(filepath.Join(dir, "objects", set.Households[0].Object[:2], set.Households[0].Object), []byte("rot"), 0o600))
	require.False(t, listed(t, dir, set.Name).Intact)
}

func TestARestoredFileIsNotTakenForTheFileItReplaced(t *testing.T) {
	ctx := t.Context()
	in := newInstall(t, 1)
	addCategory(t, in.household(t, 0), "groceries")
	dir := t.TempDir()
	first := in.write(t, dir, nil, backup.TriggerNightly, at(1))
	addCategory(t, in.household(t, 0), "fuel")
	second := in.write(t, dir, nil, backup.TriggerNightly, at(2))
	require.Equal(t, second.Name, household(t, second, in.ids[0]).TakenIn)
	in.store.Close()

	_, err := backup.Restore(ctx, backup.RestoreOptions{Set: first, DataDir: in.data, Now: at(3),
		Household: in.ids[0].String(), Migrate: migrateAgain(in.data)})
	require.NoError(t, err)
	st, err := store.OpenSQLite(ctx, in.data, time.Now)
	require.NoError(t, err)
	t.Cleanup(st.Close)
	in.store = st
	_, err = in.store.HouseholdDB(ctx, in.ids[0])
	require.NoError(t, err)

	third := in.write(t, dir, nil, backup.TriggerNightly, at(4))
	require.Equal(t, third.Name, household(t, third, in.ids[0]).TakenIn,
		"the restored file has the first set's counters but is another file")
	fourth := in.write(t, dir, nil, backup.TriggerNightly, at(5))
	require.Equal(t, third.Name, household(t, fourth, in.ids[0]).TakenIn)
}

func TestAnAttachmentSealedAgainIsANewObjectAndOlderSetsSaySo(t *testing.T) {
	ctx := t.Context()
	in := newInstall(t, 1)
	writeFiles(t, filepath.Join(in.data, "attachments"), map[string]string{in.ids[0].String() + "/receipt.pdf": "an invented receipt"})
	identity, err := age.GenerateX25519Identity()
	require.NoError(t, err)
	ids, keys := []age.Identity{identity}, []string{identity.Recipient().String()}
	dir := t.TempDir()
	only := func(set backup.Set) string {
		t.Helper()
		require.Len(t, set.Objects, 1)
		for name := range set.Objects {
			return name
		}
		return ""
	}
	path := func(name string) string { return filepath.Join(dir, "objects", name[:2], name) }

	first := in.write(t, dir, keys, backup.TriggerNightly, at(1))
	require.NoError(t, os.Remove(path(only(first))))
	second := in.write(t, dir, keys, backup.TriggerNightly, at(2))
	third := in.write(t, dir, keys, backup.TriggerNightly, at(3))
	require.NotEqual(t, only(first), only(second), "the object a set named is never written over")
	require.Equal(t, second.Objects, third.Objects)
	require.False(t, listed(t, dir, first.Name).Intact)
	for _, set := range []backup.Set{second, third} {
		_, err := backup.Rehearse(ctx, set, ids, in.data, at(3))
		require.NoError(t, err, set.Name)
	}

	// Damage that keeps the size is found by the checksum before a set names
	// the object again.
	body, err := os.ReadFile(path(only(third)))
	require.NoError(t, err)
	body[len(body)-1] ^= 0xff
	require.NoError(t, os.WriteFile(path(only(third)), body, 0o600))
	fourth := in.write(t, dir, keys, backup.TriggerNightly, at(4))
	require.NotEqual(t, only(third), only(fourth))
	_, err = backup.Rehearse(ctx, fourth, ids, in.data, at(4))
	require.NoError(t, err)
	_, err = backup.Rehearse(ctx, listed(t, dir, third.Name), ids, in.data, at(4))
	require.Error(t, err)

	_, err = backup.Prune(ctx, dir, at(30), 14)
	require.NoError(t, err)
	newest := listed(t, dir, fourth.Name)
	require.True(t, newest.Intact, newest.Problem)
	_, err = backup.Rehearse(ctx, newest, ids, in.data, at(30))
	require.NoError(t, err, "the set retention keeps is one that restores")
}
