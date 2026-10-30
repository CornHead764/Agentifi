// Package sqlitedb opens SQLite files for the new storage layer.
package sqlitedb

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
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

// ErrOuterWriteInTx reports a write through the outer handle by the goroutine
// running its InTx callback. Use the callback's transaction handle instead.
var ErrOuterWriteInTx = errors.New("sqlitedb: outer-handle write inside InTx; use the callback handle")

// A failed connector lets QueryRowContext preserve the same sentinel error in
// sql.Row.Err/Scan; sql.Row does not expose a constructor for an error row.
var rejectedOuterWrite = sql.OpenDB(outerWriteErrorConnector{})

type outerWriteErrorConnector struct{}

func (outerWriteErrorConnector) Connect(context.Context) (driver.Conn, error) {
	return nil, ErrOuterWriteInTx
}
func (outerWriteErrorConnector) Driver() driver.Driver { return outerWriteErrorDriver{} }

type outerWriteErrorDriver struct{}

func (outerWriteErrorDriver) Open(string) (driver.Conn, error) {
	return nil, ErrOuterWriteInTx
}

type guardedWriter struct {
	db    *sql.DB
	owner atomic.Uint64
}

// Go has no goroutine-local storage API. runtime.Stack's goroutine header is
// used only while a transaction holds the writer, to distinguish self-deadlock
// from unrelated callers which must remain free to queue on the pool.
func currentGoroutine() uint64 {
	var buf [64]byte
	n := runtime.Stack(buf[:], false)
	var id uint64
	for _, c := range buf[len("goroutine "):n] {
		if c < '0' || c > '9' {
			break
		}
		id = id*10 + uint64(c-'0')
	}
	return id
}

func (w *guardedWriter) isOwner() bool {
	owner := w.owner.Load()
	return owner != 0 && owner == currentGoroutine()
}

func (w *guardedWriter) ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error) {
	if w.isOwner() {
		return nil, ErrOuterWriteInTx
	}
	return w.db.ExecContext(ctx, query, args...)
}

func (w *guardedWriter) QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error) {
	if w.isOwner() {
		return nil, ErrOuterWriteInTx
	}
	return w.db.QueryContext(ctx, query, args...)
}

func (w *guardedWriter) QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row {
	if w.isOwner() {
		// No SQL runs: the connector always fails. Preserve the sentinel even
		// when ctx is already cancelled, as ExecContext and QueryContext do.
		return rejectedOuterWrite.QueryRowContext(context.Background(), query, args...)
	}
	return w.db.QueryRowContext(ctx, query, args...)
}

var testReaders atomic.Int32

// SetReadersForTests caps every database's reader pool, only in Go test
// binaries: each reader connection parses the whole schema on first use, which
// dominates a test that opens a fresh household. Call it from TestMain before
// any database is opened. Production always pools four readers.
func SetReadersForTests(n int) {
	if flag.Lookup("test.v") == nil {
		panic("sqlitedb: reader count cannot be set outside a test binary")
	}
	if n < 1 || n > 4 {
		panic("sqlitedb: invalid test reader count")
	}
	testReaders.Store(int32(n))
}

func readerPool() int {
	if n := testReaders.Load(); n != 0 {
		return int(n)
	}
	return 4
}

// DB owns one writer and a pool of four readers.
type DB struct {
	write, read *sql.DB
	writer      *guardedWriter
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
	// WAL is persistent and was set by the writer. Reapplying journal_mode on
	// every reader forces SQLite to parse the entire schema unnecessarily.
	q["_pragma"] = slices.DeleteFunc(q["_pragma"], func(pragma string) bool { return pragma == "journal_mode=WAL" })
	q.Set("mode", "ro")
	u.RawQuery = q.Encode()
	reader, err := sql.Open("sqlite", u.String())
	if err != nil {
		_ = writer.Close()
		return nil, fmt.Errorf("sqlitedb: open reader: %w", err)
	}
	reader.SetMaxOpenConns(readerPool())
	reader.SetMaxIdleConns(readerPool())
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
	return &DB{write: writer, writer: &guardedWriter{db: writer}, read: reader, clock: clock, clockPath: clockPath, close: closeDB}, nil
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
	return d.writer
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
// Always write through the callback handle. Writes through the outer handle
// (including a Conn obtained before InTx) return ErrOuterWriteInTx when made by
// the callback's goroutine. Unrelated goroutines still queue until the writer
// connection is released. Nest transactions through the callback handle too.
func (d *DB) InTx(ctx context.Context, fn func(*DB) error) (err error) {
	if d.tx != nil {
		return d.inSavepoint(ctx, fn)
	}
	if d.writer.isOwner() {
		return ErrOuterWriteInTx
	}
	tx, err := d.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("sqlitedb: begin: %w", err)
	}
	owner := currentGoroutine()
	d.writer.owner.Store(owner)
	defer func() {
		_ = tx.Rollback()
		// Commit/rollback releases the pool connection before this cleanup;
		// a queued transaction may already have installed its own owner.
		d.writer.owner.CompareAndSwap(owner, 0)
	}()
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
