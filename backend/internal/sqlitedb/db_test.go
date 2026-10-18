package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func openTestDB(t *testing.T) *DB {
	t.Helper()
	d, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"), func() time.Time { return time.UnixMilli(123456789) })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := d.Close(); err != nil {
			t.Error(err)
		}
	})
	return d
}

func execTest(t *testing.T, conn Conn, query string, args ...any) {
	t.Helper()
	if _, err := conn.ExecContext(t.Context(), query, args...); err != nil {
		t.Fatal(err)
	}
}

func TestConnectionPragmas(t *testing.T) {
	d := openTestDB(t)
	ctx := t.Context()
	if d.write.Stats().MaxOpenConnections != 1 || d.read.Stats().MaxOpenConnections != 4 {
		t.Fatal("wrong pool limits")
	}
	if got := d.Now(); got != 123456789 {
		t.Fatalf("clock = %d", got)
	}
	for name, pool := range map[string]Conn{"writer": d.write, "reader": d.read} {
		for pragma, want := range map[string]string{"journal_mode": "wal", "synchronous": "2", "foreign_keys": "1", "busy_timeout": "5000", "temp_store": "2", "cache_size": "-16000", "recursive_triggers": "0"} {
			var got string
			if err := pool.QueryRowContext(ctx, "PRAGMA "+pragma).Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != want {
				t.Errorf("%s %s = %s; want %s", name, pragma, got, want)
			}
		}
	}
	// Hold all four connections so the checks cover new connections, too.
	for range 4 {
		conn, err := d.read.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		var enabled int
		if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&enabled); err != nil || enabled != 1 {
			t.Fatalf("new reader foreign keys = %d, %v", enabled, err)
		}
		if _, err := conn.ExecContext(ctx, "CREATE TABLE forbidden (id INTEGER) STRICT"); err == nil {
			t.Fatal("reader accepted a write")
		}
	}
}

func TestReadsDuringWrite(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE items (id INTEGER PRIMARY KEY, value TEXT NOT NULL) STRICT")
	execTest(t, d.Write(), "INSERT INTO items VALUES (1, 'before')")
	if err := d.InTx(t.Context(), func(tx *DB) error {
		execTest(t, tx.Write(), "UPDATE items SET value = 'after'")
		results := make(chan error, 4)
		for range 4 {
			go func() {
				ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
				defer cancel()
				var got string
				err := d.Read().QueryRowContext(ctx, "SELECT value FROM items").Scan(&got)
				if err == nil && got != "before" {
					err = errors.New("reader saw uncommitted write")
				}
				results <- err
			}()
		}
		for range 4 {
			if err := <-results; err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var got string
	if err := d.Read().QueryRowContext(t.Context(), "SELECT value FROM items").Scan(&got); err != nil || got != "after" {
		t.Fatalf("committed value = %q, %v", got, err)
	}
}

func TestNestedRollback(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE items (id INTEGER PRIMARY KEY) STRICT")
	sentinel := errors.New("rollback inner")
	if err := d.InTx(t.Context(), func(outer *DB) error {
		execTest(t, outer.Write(), "INSERT INTO items VALUES (1)")
		err := outer.InTx(t.Context(), func(inner *DB) error {
			execTest(t, inner.Write(), "INSERT INTO items VALUES (2)")
			return sentinel
		})
		if !errors.Is(err, sentinel) {
			t.Fatalf("inner error = %v", err)
		}
		return outer.InTx(t.Context(), func(inner *DB) error {
			execTest(t, inner.Write(), "INSERT INTO items VALUES (3)")
			return nil
		})
	}); err != nil {
		t.Fatal(err)
	}
	var count, sum int
	if err := d.Read().QueryRowContext(t.Context(), "SELECT count(*), sum(id) FROM items").Scan(&count, &sum); err != nil {
		t.Fatal(err)
	}
	if count != 2 || sum != 4 {
		t.Fatalf("rows count=%d sum=%d", count, sum)
	}
}

func TestRollbackOnPanic(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE items (id INTEGER PRIMARY KEY) STRICT")
	func() {
		defer func() {
			if recover() == nil {
				t.Error("panic was lost")
			}
		}()
		_ = d.InTx(t.Context(), func(tx *DB) error {
			execTest(t, tx.Write(), "INSERT INTO items VALUES (1)")
			panic("stop")
		})
	}()
	var count int
	if err := d.Read().QueryRowContext(t.Context(), "SELECT count(*) FROM items").Scan(&count); err != nil || count != 0 {
		t.Fatalf("rows=%d, error=%v", count, err)
	}
}

func TestLockExclusion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.lock")
	first, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = first.Close() })
	second, err := AcquireLock(path)
	if err == nil {
		_ = second.Close()
		t.Fatal("second instance acquired lock")
	}
	if !strings.Contains(err.Error(), "another instance") {
		t.Fatalf("unclear error: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	next, err := AcquireLock(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestForeignKeysAndStrictTypes(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE parents (id INTEGER PRIMARY KEY) STRICT")
	execTest(t, d.Write(), "CREATE TABLE children (id INTEGER PRIMARY KEY, parent_id INTEGER REFERENCES parents(id)) STRICT")
	if _, err := d.Write().ExecContext(t.Context(), "INSERT INTO children VALUES (1, 999)"); err == nil {
		t.Fatal("foreign key accepted missing parent")
	}
	if _, err := d.Write().ExecContext(t.Context(), "INSERT INTO children VALUES (1, 'wrong')"); err == nil {
		t.Fatal("STRICT accepted text as integer")
	}
}

func TestTransactionBeginsImmediate(t *testing.T) {
	d := openTestDB(t)
	execTest(t, d.Write(), "CREATE TABLE items (id INTEGER PRIMARY KEY) STRICT")
	var path string
	var seq int
	var name string
	if err := d.write.QueryRowContext(t.Context(), "PRAGMA database_list").Scan(&seq, &name, &path); err != nil {
		t.Fatal(err)
	}
	other, err := sql.Open("sqlite", path+"?_pragma=busy_timeout%3D1&_txlock=immediate")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := d.InTx(t.Context(), func(tx *DB) error {
		// No statement has run in tx: BEGIN itself must already hold the write lock.
		competing, err := other.BeginTx(t.Context(), nil)
		if err == nil {
			_ = competing.Rollback()
			t.Fatal("transaction did not take the write lock at begin")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
