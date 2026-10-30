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
	"strings"
	"time"

	"filippo.io/age"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"golang.org/x/sys/unix"
)

const legacyFormatError = "Postgres backup format is not supported; restore this set with the previous release, then run import-postgres"

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

func databaseFiles(dir string) ([]string, error) {
	if _, err := os.Stat(filepath.Join(dir, "server.db")); err != nil {
		return nil, err
	}
	files := []string{"server.db"}
	entries, err := os.ReadDir(filepath.Join(dir, "households"))
	if errors.Is(err, os.ErrNotExist) {
		return files, nil
	}
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		if !e.IsDir() && !strings.HasPrefix(e.Name(), ".") && strings.HasSuffix(e.Name(), ".db") && !strings.Contains(e.Name(), "-before-migration-") {
			files = append(files, filepath.Join("households", e.Name()))
		}
	}
	return files, nil
}

func snapshotDatabases(ctx context.Context, dir string, w io.Writer) (File, error) {
	files, err := databaseFiles(dir)
	if err != nil {
		return File{}, err
	}
	scratch, err := os.MkdirTemp("", "agentifi-snapshots-")
	if err != nil {
		return File{}, err
	}
	defer os.RemoveAll(scratch)
	for _, rel := range files {
		target := filepath.Join(scratch, rel)
		if err := os.MkdirAll(filepath.Dir(target), 0700); err != nil {
			return File{}, err
		}
		db, err := sqlitedb.Open(ctx, filepath.Join(dir, rel), time.Now)
		if err != nil {
			return File{}, err
		}
		err = db.Snapshot(ctx, target)
		closeErr := db.Close()
		if err != nil {
			return File{}, err
		}
		if closeErr != nil {
			return File{}, closeErr
		}
	}
	if _, err := inspectDatabases(ctx, scratch); err != nil {
		return File{}, err
	}
	gz := gzip.NewWriter(w)
	tw := tar.NewWriter(gz)
	for _, rel := range files {
		p := filepath.Join(scratch, rel)
		info, err := os.Stat(p)
		if err != nil {
			return File{}, err
		}
		if err := tw.WriteHeader(&tar.Header{Name: "databases/" + filepath.ToSlash(rel), Mode: 0600, Size: info.Size(), Typeflag: tar.TypeReg}); err != nil {
			return File{}, err
		}
		if err := copyFile(tw, p); err != nil {
			return File{}, err
		}
	}
	if err := tw.Close(); err != nil {
		return File{}, err
	}
	return File{Count: len(files)}, gz.Close()
}

func unpackDatabases(ctx context.Context, set Set, ids []age.Identity, dir string) ([]TableCount, error) {
	if set.Format != FormatVersion {
		return nil, errors.New(legacyFormatError)
	}
	if err := Checksum(set, PartDatabase); err != nil {
		return nil, err
	}
	plain, closeFile, err := openPart(set, PartDatabase, ids)
	if err != nil {
		return nil, err
	}
	defer closeFile()
	err = walkTar(plain, "databases", func(rel string, h *tar.Header, body io.Reader) error {
		valid := rel == "server.db" || (strings.HasPrefix(rel, "households/") && strings.Count(rel, "/") == 1 && strings.HasSuffix(rel, ".db") && !strings.HasPrefix(strings.TrimPrefix(rel, "households/"), "."))
		if !valid || h.Typeflag != tar.TypeReg {
			return fmt.Errorf("backup: unexpected database entry %q", rel)
		}
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
		return nil, err
	}
	return inspectDatabases(ctx, dir)
}

func inspectDatabases(ctx context.Context, dir string) ([]TableCount, error) {
	files, err := databaseFiles(dir)
	if err != nil {
		return nil, err
	}
	var out []TableCount
	for _, rel := range files {
		db, err := sqlitedb.Open(ctx, filepath.Join(dir, rel), time.Now)
		if err != nil {
			return nil, err
		}
		counts, err := inspectDatabase(ctx, db, filepath.ToSlash(rel))
		closeErr := db.Close()
		if err != nil {
			return nil, err
		}
		if closeErr != nil {
			return nil, closeErr
		}
		out = append(out, counts...)
	}
	return out, nil
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
