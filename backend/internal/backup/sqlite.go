package backup

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// SQLite is the database file a set is taken of and a restore replaces.
type SQLite struct {
	Path string
}

// Tools says whether a set can be taken of the database right now.
type Tools struct {
	// Problem is why a backup cannot be taken, or "".
	Problem string
}

// CheckTools reports whether there is a database file to take a set of.
func CheckTools(_ context.Context, db SQLite) Tools {
	if db.Path == "" {
		return Tools{Problem: "DATABASE_PATH is not set"}
	}
	info, err := os.Stat(db.Path)
	switch {
	case err != nil:
		return Tools{Problem: "the database file " + db.Path + ": " + err.Error()}
	case !info.Mode().IsRegular():
		return Tools{Problem: db.Path + " is not a database file"}
	}
	return Tools{}
}

// snapshotPrefix names the directories a snapshot is written into, beside the
// database. The snapshot is plaintext, so it lives where the database already
// does and never in the backup directory.
const snapshotPrefix = ".agentifi-snapshot-"

// dumpDatabase writes a consistent copy of the database into w and returns the
// SQLite version that took it. VACUUM INTO reads in one transaction, which
// write-ahead logging lets writers go on beside, so it runs under a live
// server. The caller has checked the file is there, since opening a missing
// one would create it. The copy passes an integrity check before any of it is written: once
// sealed it cannot be checked without the identity.
func dumpDatabase(ctx context.Context, db SQLite, w io.Writer) (string, error) {
	dir, err := os.MkdirTemp(filepath.Dir(db.Path), snapshotPrefix)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	defer os.RemoveAll(dir)
	snapshot := filepath.Join(dir, "database.sqlite")

	live, err := sqlitedb.Open(ctx, db.Path)
	if err != nil {
		return "", fmt.Errorf("backup: %w", err)
	}
	var version string
	err = live.QueryRow(ctx, `SELECT sqlite_version()`).Scan(&version)
	if err == nil {
		_, err = live.Exec(ctx, `VACUUM INTO $1`, snapshot)
	}
	_ = live.Close()
	if err != nil {
		return "", fmt.Errorf("backup: copying the database: %w", err)
	}
	if _, err := inspect(ctx, snapshot, 0); err != nil {
		return "", err
	}
	if err := copyFile(w, snapshot); err != nil {
		return "", fmt.Errorf("backup: writing the database: %w", err)
	}
	return version, nil
}

// clearSnapshots removes the plaintext copies a run that died left beside the
// database. Only called under the lock, so nothing is still writing them.
func clearSnapshots(db SQLite) error {
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(db.Path), snapshotPrefix+"*"))
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	for _, match := range matches {
		if err := os.RemoveAll(match); err != nil {
			return fmt.Errorf("backup: clearing %s: %w", match, err)
		}
	}
	return nil
}

// inspect opens a database file read-only, checks its integrity and counts
// the rows in every table. A schemaVersion above zero must be the one the
// file's migrations table records.
func inspect(ctx context.Context, path string, schemaVersion int64) ([]TableCount, error) {
	db, err := sql.Open("sqlite", "file:"+path+"?mode=ro")
	if err != nil {
		return nil, fmt.Errorf("backup: opening %s: %w", path, err)
	}
	defer db.Close()

	var result string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&result); err != nil {
		return nil, fmt.Errorf("backup: checking the database: %w", err)
	}
	if result != "ok" {
		return nil, fmt.Errorf("backup: the database fails its integrity check: %s", result)
	}

	rows, err := db.QueryContext(ctx, `SELECT name FROM sqlite_schema
		WHERE type = 'table' AND name NOT LIKE 'sqlite\_%' ESCAPE '\' ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("backup: listing tables: %w", err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("backup: listing tables: %w", err)
		}
		tables = append(tables, name)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("backup: listing tables: %w", err)
	}

	out := make([]TableCount, 0, len(tables))
	for _, name := range tables {
		var count int64
		quoted := `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err := db.QueryRowContext(ctx, `SELECT count(*) FROM `+quoted).Scan(&count); err != nil {
			return nil, fmt.Errorf("backup: counting %s: %w", name, err)
		}
		out = append(out, TableCount{Table: name, Rows: count})
	}

	if schemaVersion > 0 {
		var recorded sql.NullInt64
		if err := db.QueryRowContext(ctx, `SELECT max(version_id) FROM goose_db_version WHERE is_applied`).
			Scan(&recorded); err != nil {
			return nil, fmt.Errorf("backup: reading the schema version: %w", err)
		}
		if recorded.Int64 != schemaVersion {
			return nil, fmt.Errorf("backup: the database is at schema version %d, not the %d its manifest records",
				recorded.Int64, schemaVersion)
		}
	}
	return out, nil
}

// quiet refuses while anything else has the database open, and otherwise
// folds the write-ahead log into the file, so the file alone is the whole
// database. A connection in WAL mode holds a shared lock on the file for as
// long as it is open, so leaving WAL fails while any other connection, in any
// process, is open.
func quiet(ctx context.Context, db SQLite) error {
	if !exists(db.Path) {
		return nil
	}
	conn, err := sql.Open("sqlite", "file:"+db.Path+"?_pragma=busy_timeout(0)")
	if err != nil {
		return fmt.Errorf("backup: opening %s: %w", db.Path, err)
	}
	defer conn.Close()
	var mode string
	err = conn.QueryRowContext(ctx, `PRAGMA journal_mode=DELETE`).Scan(&mode)
	if err == nil && mode != "delete" {
		err = fmt.Errorf("the journal mode stayed %s", mode)
	}
	if err != nil {
		return fmt.Errorf("backup: something else has %s open; stop the application first "+
			"(docker compose stop agentifi): %w", db.Path, err)
	}
	return nil
}

// databaseFiles are the files a database is made of on disk, beside its own.
var databaseFiles = []string{"", "-wal", "-shm", "-journal"}

func removeDatabase(path string) error {
	var errs []error
	for _, suffix := range databaseFiles {
		if err := os.Remove(path + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// swapDatabases moves live, with whatever files of its own are beside it, to
// previous, and scratch to live. If the second move fails the first is
// undone, so live always names a whole database. A -wal left under the live
// name would be replayed into the restored file, so none is.
func swapDatabases(live, scratch, previous string) error {
	var moved []string
	for _, suffix := range databaseFiles {
		if !exists(live + suffix) {
			continue
		}
		if err := os.Rename(live+suffix, previous+suffix); err != nil {
			undoMoves(moved, live, previous)
			return fmt.Errorf("backup: setting %s aside: %w", live+suffix, err)
		}
		moved = append(moved, suffix)
	}
	if err := os.Rename(scratch, live); err != nil {
		if undo := undoMoves(moved, live, previous); undo != nil {
			return fmt.Errorf("backup: moving %s to %s failed (%v), and so did putting %s back: %w; "+
				"rename it by hand", scratch, live, err, previous, undo)
		}
		return fmt.Errorf("backup: moving %s to %s: %w (the live database was put back)", scratch, live, err)
	}
	return syncDir(filepath.Dir(live))
}

func undoMoves(moved []string, live, previous string) error {
	var errs []error
	for _, suffix := range moved {
		if err := os.Rename(previous+suffix, live+suffix); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// syncDir makes the renames in dir durable.
func syncDir(dir string) error {
	handle, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	defer handle.Close()
	if err := handle.Sync(); err != nil {
		return fmt.Errorf("backup: %w", err)
	}
	return nil
}

// Lock waits for the backup lock and returns its release. Every backup and
// restore holds it, so a nightly run, `agentifi backup` and `agentifi
// migrate` in containers of their own, and a restore never overlap. It is an
// flock on a file beside the database, the one directory every process that
// takes a set has mounted; flock locks are per open file, so two holders in
// one process exclude each other too.
func Lock(ctx context.Context, db SQLite) (func(), error) {
	if db.Path == "" {
		return nil, errors.New("backup: DATABASE_PATH is not set")
	}
	file, err := os.OpenFile(db.Path+".backup-lock", os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("backup: opening the backup lock: %w", err)
	}
	for {
		err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return func() {
				_ = syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
				_ = file.Close()
			}, nil
		}
		if !errors.Is(err, syscall.EWOULDBLOCK) {
			_ = file.Close()
			return nil, fmt.Errorf("backup: taking the backup lock: %w", err)
		}
		select {
		case <-ctx.Done():
			_ = file.Close()
			return nil, fmt.Errorf("backup: waiting for the backup lock: %w", ctx.Err())
		case <-time.After(50 * time.Millisecond):
		}
	}
}
