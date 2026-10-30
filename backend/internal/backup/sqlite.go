package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"filippo.io/age"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/google/uuid"
	"golang.org/x/sys/unix"
)

const legacyFormatError = "Postgres backup format is not supported; restore this set with the previous release, then run import-postgres"

const migrationSnapshotMark = "-before-migration-"

// Lock serializes backup writers independently of the running server's lock.
func Lock(ctx context.Context, dataDir string) (func(), error) {
	for {
		lock, err := sqlitedb.AcquireLock(filepath.Join(dataDir, "backup.lock"))
		if err == nil {
			return func() { _ = lock.Close() }, nil
		}
		if !errors.Is(err, unix.EWOULDBLOCK) && !errors.Is(err, unix.EAGAIN) {
			return nil, err
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(50 * time.Millisecond):
		}
	}
}

// householdIDs names the household files in a data directory. A file the
// store would not open as a household is not one.
func householdIDs(dir string) ([]string, error) {
	entries, err := os.ReadDir(filepath.Join(dir, "households"))
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var ids []string
	for _, e := range entries {
		id, ok := strings.CutSuffix(e.Name(), ".db")
		if !e.IsDir() && ok && validHouseholdID(id) {
			ids = append(ids, id)
		}
	}
	return ids, nil
}

// stagedFiles is the databases unpacked into dir, as data-directory paths.
func stagedFiles(dir string) ([]string, error) {
	var files []string
	if _, err := os.Stat(filepath.Join(dir, "server.db")); err == nil {
		files = append(files, "server.db")
	}
	ids, err := householdIDs(dir)
	if err != nil {
		return nil, err
	}
	for _, id := range ids {
		files = append(files, "households/"+id+".db")
	}
	return files, nil
}

// snapshotFile writes a standalone copy of a live database, committed WAL
// contents included.
func snapshotFile(ctx context.Context, live, target string) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	db, err := sqlitedb.Open(ctx, live, time.Now)
	if err != nil {
		return err
	}
	err = db.Snapshot(ctx, target)
	return errors.Join(err, db.Close())
}

// householdState is what a household file's counters and schema say about
// it: the sync counter, the counter every write moves, and the migration
// version. Empty when the file predates write_counter, and is then
// snapshotted every time.
func householdState(ctx context.Context, path string) (string, int64, error) {
	db, err := sqlitedb.Open(ctx, path, time.Now)
	if err != nil {
		return "", 0, err
	}
	defer db.Close()
	var changes, writes, version int64
	err = db.Read().QueryRowContext(ctx, `SELECT (SELECT seq FROM change_counter), (SELECT seq FROM write_counter),
		(SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1)`).Scan(&changes, &writes, &version)
	if err != nil {
		var schema int64
		if versionErr := db.Read().QueryRowContext(ctx, `SELECT COALESCE(MAX(version_id), 0) FROM goose_db_version WHERE is_applied = 1`).Scan(&schema); versionErr != nil {
			return "", 0, nil
		}
		return "", schema, nil
	}
	return fmt.Sprintf("%d:%d:%d", changes, writes, version), version, nil
}

// fingerprint ties a household's counters to the file they were read from.
// The counters only ever rise within one file, and a file put back from a
// backup, or copied over, is a different inode, size or modification time,
// so two equal fingerprints are the same contents.
func fingerprint(key []byte, id, state string, live os.FileInfo) string {
	stat, ok := live.Sys().(*syscall.Stat_t)
	if state == "" || !ok {
		return ""
	}
	return keyedName(key, "household", id, state,
		fmt.Sprintf("%d:%d:%d:%d", stat.Dev, stat.Ino, live.Size(), live.ModTime().UnixNano()))
}

// liveFingerprint is a live household file's fingerprint now.
func liveFingerprint(ctx context.Context, key []byte, id, path string) (string, error) {
	state, _, err := householdState(ctx, path)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(path)
	if err != nil {
		return "", err
	}
	return fingerprint(key, id, state, info), nil
}

func gzipFile(w io.Writer, path string) error {
	gz := gzip.NewWriter(w)
	if err := copyFile(gz, path); err != nil {
		return err
	}
	return gz.Close()
}

func gunzipTo(target string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
		return err
	}
	gz, err := gzip.NewReader(r)
	if err != nil {
		return err
	}
	defer gz.Close()
	out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(out, gz)
	return errors.Join(err, out.Close())
}

// unpackDatabases writes the set's databases into dir, or only one
// household's when household is given, and checks them: integrity, row
// counts against the manifest, and opening each through the store, which
// migrates the copy and checks the household it says it is.
func unpackDatabases(ctx context.Context, set Set, ids []age.Identity, dir, household string) ([]TableCount, error) {
	var err error
	switch set.Format {
	case FormatVersion:
		err = unpackObjects(set, ids, dir, household)
	case tarFormat:
		err = unpackTar(set, ids, dir, household)
	default:
		return nil, errors.New(legacyFormatError)
	}
	if err != nil {
		return nil, err
	}
	files, err := stagedFiles(dir)
	if err != nil {
		return nil, err
	}
	counts, err := inspectDatabases(ctx, dir, files)
	if err != nil {
		return nil, err
	}
	if set.Format == FormatVersion {
		if err := compareCounts(set, counts, household == ""); err != nil {
			return nil, err
		}
	}
	if err := openThroughStore(ctx, dir); err != nil {
		return nil, err
	}
	return counts, nil
}

func unpackObjects(set Set, ids []age.Identity, dir, household string) error {
	if household == "" {
		if err := Checksum(set, PartDatabase); err != nil {
			return err
		}
		plain, closeFile, err := openPart(set, PartDatabase, ids)
		if err != nil {
			return err
		}
		err = gunzipTo(filepath.Join(dir, "server.db"), plain)
		closeFile()
		if err != nil {
			return fmt.Errorf("backup: unpacking server.db: %w", err)
		}
	}
	found := false
	for _, one := range set.Households {
		if household != "" && one.ID != household {
			continue
		}
		found = true
		if !validHouseholdID(one.ID) {
			return fmt.Errorf("backup: the set names a household %q", one.ID)
		}
		plain, closeFile, err := openObject(set, one.Object, Object{Bytes: one.Bytes, SHA256: one.Object}, ids)
		if err != nil {
			return err
		}
		err = gunzipTo(filepath.Join(dir, filepath.FromSlash(one.Label())), plain)
		closeFile()
		if err != nil {
			return fmt.Errorf("backup: unpacking %s: %w", one.Label(), err)
		}
	}
	if household != "" && !found {
		return fmt.Errorf("backup: set %s holds no household %s", set.Name, household)
	}
	return nil
}

func unpackTar(set Set, ids []age.Identity, dir, household string) error {
	if err := Checksum(set, PartDatabase); err != nil {
		return err
	}
	plain, closeFile, err := openPart(set, PartDatabase, ids)
	if err != nil {
		return err
	}
	defer closeFile()
	want := "households/" + household + ".db"
	found := false
	err = walkTar(plain, "databases", func(rel string, h *tar.Header, body io.Reader) error {
		valid := rel == "server.db" || (strings.HasPrefix(rel, "households/") && strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".db") && !strings.HasPrefix(strings.TrimPrefix(rel, "households/"), "."))
		if !valid || h.Typeflag != tar.TypeReg {
			return fmt.Errorf("backup: unexpected database entry %q", rel)
		}
		if household != "" && rel != want {
			return nil
		}
		found = true
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, body)
		return errors.Join(err, out.Close())
	})
	if err != nil {
		return err
	}
	if household != "" && !found {
		return fmt.Errorf("backup: set %s holds no household %s", set.Name, household)
	}
	return nil
}

// compareCounts checks every database unpacked has the rows its manifest
// recorded, table by table.
func compareCounts(set Set, counts []TableCount, withServer bool) error {
	want := map[string]map[string]int64{}
	if withServer {
		want["server.db"] = set.Files[PartDatabase].Tables
	}
	for _, household := range set.Households {
		want[household.Label()] = household.Tables
	}
	got := map[string]map[string]int64{}
	for _, count := range counts {
		label, table, _ := strings.Cut(count.Table, ":")
		if got[label] == nil {
			got[label] = map[string]int64{}
		}
		got[label][table] = count.Rows
	}
	labels := make([]string, 0, len(got))
	for label := range got {
		labels = append(labels, label)
	}
	sort.Strings(labels)
	for _, label := range labels {
		recorded, ok := want[label]
		if !ok {
			return fmt.Errorf("backup: %s is not in the set's manifest", label)
		}
		for table, rows := range got[label] {
			if recorded[table] != rows {
				return fmt.Errorf("backup: %s has %d rows in %s; the set recorded %d", label, rows, table, recorded[table])
			}
		}
		for table, rows := range recorded {
			if _, ok := got[label][table]; !ok {
				return fmt.Errorf("backup: %s has no table %s; the set recorded %d rows in it", label, table, rows)
			}
		}
	}
	return nil
}

// openThroughStore opens what is in dir the way the server would: server.db
// and every household, migrated to this binary's schema and each checked to
// be the household its name says. dir is a copy, and a migration's own
// snapshots are removed again.
func openThroughStore(ctx context.Context, dir string) error {
	ids, err := householdIDs(dir)
	if err != nil {
		return err
	}
	st, err := store.OpenSQLite(ctx, dir, time.Now)
	if err != nil {
		return fmt.Errorf("backup: opening the restored server.db: %w", err)
	}
	for _, id := range ids {
		if _, err := st.HouseholdDB(ctx, store.SpaceID(uuid.MustParse(id))); err != nil {
			st.Close()
			return fmt.Errorf("backup: opening households/%s.db: %w", id, err)
		}
	}
	st.Close()
	return removeMigrationSnapshots(dir)
}

func removeMigrationSnapshots(dir string) error {
	var errs []error
	for _, sub := range []string{dir, filepath.Join(dir, "households")} {
		entries, err := os.ReadDir(sub)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if strings.Contains(entry.Name(), migrationSnapshotMark) {
				errs = append(errs, os.Remove(filepath.Join(sub, entry.Name())))
			}
		}
	}
	return errors.Join(errs...)
}

func inspectDatabases(ctx context.Context, dir string, files []string) ([]TableCount, error) {
	var out []TableCount
	for _, rel := range files {
		counts, err := inspectFile(ctx, filepath.Join(dir, filepath.FromSlash(rel)), rel)
		if err != nil {
			return nil, err
		}
		out = append(out, counts...)
	}
	return out, nil
}

func inspectFile(ctx context.Context, path, label string) ([]TableCount, error) {
	db, err := sqlitedb.Open(ctx, path, time.Now)
	if err != nil {
		return nil, err
	}
	counts, err := inspectDatabase(ctx, db, label)
	if closeErr := db.Close(); err == nil {
		err = closeErr
	}
	return counts, err
}

// tables is one database's counts as a manifest records them.
func tables(counts []TableCount, label string) map[string]int64 {
	out := map[string]int64{}
	for _, count := range counts {
		out[strings.TrimPrefix(count.Table, label+":")] = count.Rows
	}
	return out
}

func inspectDatabase(ctx context.Context, db *sqlitedb.DB, label string) ([]TableCount, error) {
	rows, err := db.Read().QueryContext(ctx, "PRAGMA integrity_check")
	if err != nil {
		return nil, err
	}
	ok := false
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			rows.Close()
			return nil, err
		}
		if result != "ok" {
			rows.Close()
			return nil, fmt.Errorf("backup: integrity_check %s: %s", label, result)
		}
		ok = true
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, fmt.Errorf("backup: integrity_check %s returned no result", label)
	}
	rows, err = db.Read().QueryContext(ctx, `SELECT name FROM sqlite_schema WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, err
		}
		names = append(names, name)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	var counts []TableCount
	for _, name := range names {
		var n int64
		if err := db.Read().QueryRowContext(ctx, `SELECT count(*) FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`).Scan(&n); err != nil {
			return nil, err
		}
		counts = append(counts, TableCount{Table: label + ":" + name, Rows: n})
	}
	return counts, nil
}
