// Package sqlitedb opens SQLite files for the new storage layer.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"sync"
	"time"

	"database/sql/driver"
	"modernc.org/sqlite"
)

// Clocks are keyed by SQLite's absolute main-file path, so reopened handles and
// snapshots do not embed a process-specific identifier in persistent triggers.
var clocks sync.Map
var clocksMu sync.Mutex

type clockEntry struct {
	clock func() time.Time
	refs  int
}

func init() {
	if err := sqlite.RegisterScalarFunction("agentifi_now", 1, func(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
		path, ok := args[0].(string)
		if !ok {
			return nil, errors.New("sqlitedb: clock requires a database path")
		}
		clock, ok := clocks.Load(path)
		if !ok {
			return nil, fmt.Errorf("sqlitedb: no clock for %q; open through sqlitedb.Open", path)
		}
		return clock.(*clockEntry).clock().UnixMilli(), nil
	}); err != nil {
		panic(err)
	}
}

// Conn is the query handle shared by pools and transactions.
type Conn interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// DB owns one writer and a pool of four readers.
type DB struct {
	write, read *sql.DB
	tx          *sql.Tx
	depth       int
	clock       func() time.Time
	clockPath   string
	close       func() error
}

func Open(ctx context.Context, path string, clock func() time.Time) (*DB, error) {
	if clock == nil {
		return nil, errors.New("sqlitedb: clock is required")
	}
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	u := url.URL{Scheme: "file", Path: path}
	q := url.Values{}
	for _, pragma := range []string{"journal_mode=WAL", "synchronous=FULL", "foreign_keys=ON", "busy_timeout=5000", "temp_store=MEMORY", "cache_size=-16000", "recursive_triggers=OFF"} {
		q.Add("_pragma", pragma)
	}
	q.Set("_txlock", "immediate")
	u.RawQuery = q.Encode()
	writer, err := sql.Open("sqlite", u.String())
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: open writer: %w", err)
	}
	writer.SetMaxOpenConns(1)
	writer.SetMaxIdleConns(1)
	if err := writer.PingContext(ctx); err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("sqlitedb: connect writer: %w", err)
	}
	q.Del("_txlock")
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	reader, err := sql.Open("sqlite", u.String())
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("sqlitedb: open reader: %w", err)
	}
	reader.SetMaxOpenConns(4)
	reader.SetMaxIdleConns(4)
	if err := reader.PingContext(ctx); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("sqlitedb: connect reader: %w", err)
	}
	// SQLite resolves symlinks (notably /tmp on macOS); use its own path as key.
	var clockPath string
	if err := writer.QueryRowContext(ctx, "SELECT file FROM pragma_database_list WHERE name = 'main'").Scan(&clockPath); err != nil {
		_ = reader.Close()
		_ = writer.Close()
		return nil, fmt.Errorf("sqlitedb: resolve clock path: %w", err)
	}
	clocksMu.Lock()
	entry, _ := clocks.LoadOrStore(clockPath, &clockEntry{clock: clock})
	entry.(*clockEntry).refs++
	clocksMu.Unlock()
	closeDB := sync.OnceValue(func() error {
		err := errors.Join(reader.Close(), writer.Close())
		clocksMu.Lock()
		defer clocksMu.Unlock()
		entry.(*clockEntry).refs--
		if entry.(*clockEntry).refs == 0 {
			clocks.Delete(clockPath)
		}
		return err
	})
	return &DB{write: writer, read: reader, clock: clock, clockPath: clockPath, close: closeDB}, nil
}

// Read uses the transaction when called inside InTx.
func (d *DB) Read() Conn {
	if d.tx != nil {
		return d.tx
	}
	return d.read
}
func (d *DB) Write() Conn {
	if d.tx != nil {
		return d.tx
	}
	return d.write
}

// Now returns the clock time as Unix milliseconds for SQL parameters.
func (d *DB) Now() int64 { return d.clock().UnixMilli() }

func (d *DB) Close() error {
	if d.tx != nil {
		return errors.New("sqlitedb: cannot close a transaction handle")
	}
	return d.close()
}

// InTx rolls back on an error or panic. Nested calls use savepoints.
// The callback handle must not be shared between goroutines.
// Always write through the callback handle. The outer handle owns a single
// writer connection, held for this entire callback: using it here waits for
// itself until ctx is cancelled. Concurrent unrelated writers must still be
// allowed to queue, so DB-wide transaction flags cannot safely reject them.
func (d *DB) InTx(ctx context.Context, fn func(*DB) error) (err error) {
	if d.tx != nil {
		return d.inSavepoint(ctx, fn)
	}
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitedb: begin: %w", err)
	}
	defer tx.Rollback()
	child := *d
	child.tx = tx
	if err := fn(&child); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("sqlitedb: commit: %w", err)
	}
	return nil
}

func (d *DB) inSavepoint(ctx context.Context, fn func(*DB) error) (err error) {
	name := fmt.Sprintf("nested_%d", d.depth+1)
	if _, err := d.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return fmt.Errorf("sqlitedb: savepoint: %w", err)
	}
	released := false
	defer func() {
		if !released {
			cleanup := context.WithoutCancel(ctx)
			_, rollbackErr := d.tx.ExecContext(cleanup, "ROLLBACK TO SAVEPOINT "+name)
			_, releaseErr := d.tx.ExecContext(cleanup, "RELEASE SAVEPOINT "+name)
			err = errors.Join(err, rollbackErr, releaseErr)
		}
	}()
	child := *d
	child.depth++
	if err = fn(&child); err != nil {
		return err
	}
	if _, err = d.tx.ExecContext(ctx, "RELEASE SAVEPOINT "+name); err != nil {
		return fmt.Errorf("sqlitedb: release savepoint: %w", err)
	}
	released = true
	return nil
}
