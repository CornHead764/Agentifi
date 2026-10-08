// Package sqlitedb is the database handle: a SQLite file opened through the
// pure-Go modernc driver, behind the Query/QueryRow/Exec shape internal/store
// is written against.
//
// It owns the three translations between Go values and SQLite's storage
// classes so no query has to restate them:
//
//   - Placeholders. Queries are written with Postgres-style $N, which SQLite
//     would read as named parameters bound in order of first appearance; they
//     are rewritten to ?N, which binds by position.
//   - Arguments. A time.Time is written as Timestamp text, a domain.Date as
//     YYYY-MM-DD, a slice or a struct as a JSON document. A decimal is refused:
//     it goes through internal/dbconv, which knows the column's scale.
//   - Scan targets. A time column is parsed back from its text, and a slice,
//     map or struct target is decoded from JSON.
//
// One process owns the file. Writers are serialized by SQLite (transactions
// begin IMMEDIATE), so the row locks a Postgres query would take are not
// needed, and the advisory locks of internal/store are in-process.
package sqlitedb

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"modernc.org/sqlite"
	sqlite3 "modernc.org/sqlite/lib"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// TimestampFormat is how every timestamp column is stored: UTC, fixed width
// with microseconds, so that comparing the text orders the instants.
const TimestampFormat = "2006-01-02T15:04:05.000000Z"

// DateFormat is how every date column is stored.
const DateFormat = "2006-01-02"

// ErrNoRows is returned by a Row whose query matched nothing.
var ErrNoRows = sql.ErrNoRows

// NamedArgs binds @name parameters, as the only argument of a query.
type NamedArgs map[string]any

// Querier is what a database and a transaction share, so a query runs
// unchanged inside or outside one.
type Querier interface {
	Query(ctx context.Context, query string, args ...any) (*Rows, error)
	QueryRow(ctx context.Context, query string, args ...any) *Row
	Exec(ctx context.Context, query string, args ...any) (Result, error)
}

// Result reports what a write changed.
type Result struct{ rowsAffected int64 }

func (r Result) RowsAffected() int64 { return r.rowsAffected }

func init() {
	sqlite.MustRegisterScalarFunction("now", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return FormatTimestamp(time.Now()), nil
	})
	sqlite.MustRegisterScalarFunction("gen_random_uuid", 0, func(*sqlite.FunctionContext, []driver.Value) (driver.Value, error) {
		return uuid.NewString(), nil
	})
	sqlite.MustRegisterDeterministicScalarFunction("ts_add", 2, tsAdd)
	sqlite.MustRegisterDeterministicScalarFunction("regexp", 2, regexpMatch)
	sqlite.MustRegisterFunction("decimal_sum", &sqlite.FunctionImpl{
		NArgs:         1,
		Deterministic: true,
		MakeAggregate: func(sqlite.FunctionContext) (sqlite.AggregateFunction, error) {
			return &decimalSum{}, nil
		},
	})
}

// tsAdd is ts_add(timestamp, '±N unit'), the replacement for Postgres's
// `timestamp + interval`. Units are second, minute, hour, day, month and year,
// singular or plural. NULL in gives NULL out.
func tsAdd(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	if args[0] == nil || args[1] == nil {
		return nil, nil
	}
	text, ok := args[0].(string)
	if !ok {
		return nil, fmt.Errorf("ts_add: timestamp must be text, got %T", args[0])
	}
	t, err := ParseTime(text)
	if err != nil {
		return nil, fmt.Errorf("ts_add: %w", err)
	}
	spec, ok := args[1].(string)
	if !ok {
		return nil, fmt.Errorf("ts_add: interval must be text, got %T", args[1])
	}
	fields := strings.Fields(spec)
	if len(fields) != 2 {
		return nil, fmt.Errorf("ts_add: interval %q is not '±N unit'", spec)
	}
	n, err := strconv.Atoi(fields[0])
	if err != nil {
		return nil, fmt.Errorf("ts_add: interval %q: %w", spec, err)
	}
	switch strings.TrimSuffix(fields[1], "s") {
	case "second":
		t = t.Add(time.Duration(n) * time.Second)
	case "minute":
		t = t.Add(time.Duration(n) * time.Minute)
	case "hour":
		t = t.Add(time.Duration(n) * time.Hour)
	case "day":
		t = t.AddDate(0, 0, n)
	case "month":
		t = t.AddDate(0, n, 0)
	case "year":
		t = t.AddDate(n, 0, 0)
	default:
		return nil, fmt.Errorf("ts_add: unknown unit in %q", spec)
	}
	return FormatTimestamp(t), nil
}

var patterns sync.Map // pattern text -> *regexp.Regexp

// regexpMatch backs `text REGEXP pattern`, with Go's (RE2) syntax. NULL in
// gives NULL out.
func regexpMatch(_ *sqlite.FunctionContext, args []driver.Value) (driver.Value, error) {
	pattern, ok := args[0].(string)
	if !ok {
		return nil, nil
	}
	text, ok := args[1].(string)
	if !ok {
		return nil, nil
	}
	compiled, ok := patterns.Load(pattern)
	if !ok {
		re, err := regexp.Compile(pattern)
		if err != nil {
			return nil, fmt.Errorf("regexp: %w", err)
		}
		compiled, _ = patterns.LoadOrStore(pattern, re)
	}
	return compiled.(*regexp.Regexp).MatchString(text), nil
}

// decimalSum is decimal_sum(text), an exact sum of decimal-text columns (rates,
// prices, share counts) that SQLite's SUM would add as floats. It returns the
// sum as text, or NULL over no non-NULL rows.
type decimalSum struct {
	sum decimal.Decimal
	any bool
}

func (s *decimalSum) Step(_ *sqlite.FunctionContext, args []driver.Value) error {
	if args[0] == nil {
		return nil
	}
	var d decimal.Decimal
	switch v := args[0].(type) {
	case string:
		parsed, err := decimal.NewFromString(v)
		if err != nil {
			return fmt.Errorf("decimal_sum: %w", err)
		}
		d = parsed
	case int64:
		d = decimal.NewFromInt(v)
	default:
		return fmt.Errorf("decimal_sum: %T is not exact", v)
	}
	s.sum = s.sum.Add(d)
	s.any = true
	return nil
}

func (s *decimalSum) WindowInverse(*sqlite.FunctionContext, []driver.Value) error {
	return errors.New("decimal_sum: not a window function")
}

func (s *decimalSum) WindowValue(*sqlite.FunctionContext) (driver.Value, error) {
	if !s.any {
		return nil, nil
	}
	return s.sum.String(), nil
}

func (s *decimalSum) Final(*sqlite.FunctionContext) {}

// FormatTimestamp is t as a timestamp column stores it.
func FormatTimestamp(t time.Time) string { return t.UTC().Format(TimestampFormat) }

// ParseTime reads a timestamp or date column's text.
func ParseTime(text string) (time.Time, error) {
	for _, layout := range []string{time.RFC3339Nano, DateFormat, "2006-01-02 15:04:05.999999999-07:00", "2006-01-02 15:04:05"} {
		if t, err := time.Parse(layout, text); err == nil {
			return t.UTC(), nil
		}
	}
	return time.Time{}, fmt.Errorf("sqlitedb: %q is not a date or timestamp", text)
}

// DB is an open database file.
type DB struct {
	sql  *sql.DB
	path string
}

// Open opens (creating if absent) the database at path, with foreign keys
// enforced, write-ahead logging, and IMMEDIATE transactions, so a transaction
// that will write takes the write lock up front instead of failing when it
// first writes.
func Open(ctx context.Context, path string) (*DB, error) {
	if path == "" {
		return nil, errors.New("sqlitedb: no database path")
	}
	query := url.Values{}
	query.Set("_txlock", "immediate")
	for _, pragma := range []string{"foreign_keys(1)", "journal_mode(WAL)", "busy_timeout(30000)", "synchronous(NORMAL)"} {
		query.Add("_pragma", pragma)
	}
	handle, err := sql.Open("sqlite", "file:"+path+"?"+query.Encode())
	if err != nil {
		return nil, fmt.Errorf("sqlitedb: opening %s: %w", path, err)
	}
	if err := handle.PingContext(ctx); err != nil {
		handle.Close()
		return nil, fmt.Errorf("sqlitedb: opening %s: %w", path, err)
	}
	return &DB{sql: handle, path: path}, nil
}

// SQL is the database/sql handle, for the migration runner.
func (d *DB) SQL() *sql.DB { return d.sql }

// Path is the file this database lives in.
func (d *DB) Path() string { return d.path }

func (d *DB) Close() error { return d.sql.Close() }

func (d *DB) Ping(ctx context.Context) error { return d.sql.PingContext(ctx) }

func (d *DB) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return doQuery(ctx, d.sql, query, args)
}

func (d *DB) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return doQueryRow(ctx, d.sql, query, args)
}

func (d *DB) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return doExec(ctx, d.sql, query, args)
}

// Begin opens a transaction.
func (d *DB) Begin(ctx context.Context) (*Tx, error) {
	tx, err := d.sql.BeginTx(ctx, nil)
	if err != nil {
		return nil, err
	}
	return &Tx{tx: tx, savepoints: new(int)}, nil
}

// Tx is a transaction, or a savepoint inside one.
type Tx struct {
	tx         *sql.Tx
	savepoint  string
	savepoints *int
}

// Begin opens a savepoint, which commits and rolls back with its own Tx.
func (t *Tx) Begin(ctx context.Context) (*Tx, error) {
	*t.savepoints++
	name := "sp_" + strconv.Itoa(*t.savepoints)
	if _, err := t.tx.ExecContext(ctx, "SAVEPOINT "+name); err != nil {
		return nil, err
	}
	return &Tx{tx: t.tx, savepoint: name, savepoints: t.savepoints}, nil
}

func (t *Tx) Commit(ctx context.Context) error {
	if t.savepoint != "" {
		_, err := t.tx.ExecContext(ctx, "RELEASE "+t.savepoint)
		return err
	}
	return t.tx.Commit()
}

// Rollback undoes the transaction or savepoint. It is safe after Commit.
func (t *Tx) Rollback(ctx context.Context) error {
	if t.savepoint != "" {
		if _, err := t.tx.ExecContext(ctx, "ROLLBACK TO "+t.savepoint); err != nil {
			if errors.Is(err, sql.ErrTxDone) {
				return nil
			}
			return err
		}
		_, err := t.tx.ExecContext(ctx, "RELEASE "+t.savepoint)
		return err
	}
	err := t.tx.Rollback()
	if errors.Is(err, sql.ErrTxDone) {
		return nil
	}
	return err
}

func (t *Tx) Query(ctx context.Context, query string, args ...any) (*Rows, error) {
	return doQuery(ctx, t.tx, query, args)
}

func (t *Tx) QueryRow(ctx context.Context, query string, args ...any) *Row {
	return doQueryRow(ctx, t.tx, query, args)
}

func (t *Tx) Exec(ctx context.Context, query string, args ...any) (Result, error) {
	return doExec(ctx, t.tx, query, args)
}

type sqlQuerier interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
}

func doQuery(ctx context.Context, q sqlQuerier, query string, args []any) (*Rows, error) {
	query, converted, err := prepare(query, args)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, query, converted...)
	if err != nil {
		return nil, err
	}
	return &Rows{rows: rows}, nil
}

func doQueryRow(ctx context.Context, q sqlQuerier, query string, args []any) *Row {
	rows, err := doQuery(ctx, q, query, args)
	return &Row{rows: rows, err: err}
}

func doExec(ctx context.Context, q sqlQuerier, query string, args []any) (Result, error) {
	query, converted, err := prepare(query, args)
	if err != nil {
		return Result{}, err
	}
	result, err := q.ExecContext(ctx, query, converted...)
	if err != nil {
		return Result{}, err
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return Result{}, err
	}
	return Result{rowsAffected: affected}, nil
}

// Rows is a query's result set.
type Rows struct {
	rows *sql.Rows
}

func (r *Rows) Next() bool { return r.rows.Next() }
func (r *Rows) Err() error { return r.rows.Err() }
func (r *Rows) Close()     { _ = r.rows.Close() }
func (r *Rows) Scan(dest ...any) error {
	return r.rows.Scan(scanTargets(dest)...)
}

// Columns names the result's columns.
func (r *Rows) Columns() ([]string, error) { return r.rows.Columns() }

// Row is the first row of a query, read by Scan.
type Row struct {
	rows *Rows
	err  error
}

func (r *Row) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	defer r.rows.Close()
	if !r.rows.Next() {
		if err := r.rows.Err(); err != nil {
			return err
		}
		return ErrNoRows
	}
	if err := r.rows.Scan(dest...); err != nil {
		return err
	}
	return r.rows.rows.Close()
}

// IsUniqueViolation reports whether err is a UNIQUE or PRIMARY KEY conflict.
func IsUniqueViolation(err error) bool {
	var sqliteErr *sqlite.Error
	if !errors.As(err, &sqliteErr) {
		return false
	}
	code := sqliteErr.Code()
	return code == sqlite3.SQLITE_CONSTRAINT_UNIQUE || code == sqlite3.SQLITE_CONSTRAINT_PRIMARYKEY
}

// IsForeignKeyViolation reports whether err is a FOREIGN KEY conflict.
func IsForeignKeyViolation(err error) bool {
	var sqliteErr *sqlite.Error
	return errors.As(err, &sqliteErr) && sqliteErr.Code() == sqlite3.SQLITE_CONSTRAINT_FOREIGNKEY
}

var rewritten sync.Map // query text -> rewritten text

// prepare rewrites the placeholders and converts the arguments.
func prepare(query string, args []any) (string, []any, error) {
	if len(args) == 1 {
		if named, ok := args[0].(NamedArgs); ok {
			out := make([]any, 0, len(named))
			for name, value := range named {
				converted, err := Arg(value)
				if err != nil {
					return "", nil, fmt.Errorf("sqlitedb: argument @%s: %w", name, err)
				}
				out = append(out, sql.Named(name, converted))
			}
			return query, out, nil
		}
	}
	if cached, ok := rewritten.Load(query); ok {
		query = cached.(string)
	} else {
		out := rewritePlaceholders(query)
		rewritten.Store(query, out)
		query = out
	}
	out := make([]any, len(args))
	for i, value := range args {
		converted, err := Arg(value)
		if err != nil {
			return "", nil, fmt.Errorf("sqlitedb: argument $%d: %w", i+1, err)
		}
		out[i] = converted
	}
	return query, out, nil
}

// rewritePlaceholders turns $N into ?N outside string literals, quoted
// identifiers and comments.
func rewritePlaceholders(query string) string {
	var b strings.Builder
	b.Grow(len(query))
	for i := 0; i < len(query); i++ {
		c := query[i]
		switch {
		case c == '\'' || c == '"':
			end := i + 1
			for end < len(query) {
				if query[end] == c {
					if end+1 < len(query) && query[end+1] == c {
						end += 2
						continue
					}
					break
				}
				end++
			}
			b.WriteString(query[i:min(end+1, len(query))])
			i = end
		case c == '-' && i+1 < len(query) && query[i+1] == '-':
			end := strings.IndexByte(query[i:], '\n')
			if end < 0 {
				b.WriteString(query[i:])
				return b.String()
			}
			b.WriteString(query[i : i+end])
			i += end - 1
		case c == '$' && i+1 < len(query) && query[i+1] >= '0' && query[i+1] <= '9':
			b.WriteByte('?')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

var (
	timeType    = reflect.TypeOf(time.Time{})
	rawJSONType = reflect.TypeOf(json.RawMessage(nil))
)

// Arg converts one query argument to the value its column stores.
func Arg(value any) (any, error) {
	switch v := value.(type) {
	case nil:
		return nil, nil
	case string, int64, bool, []byte, float64:
		return v, nil
	case json.RawMessage:
		if v == nil {
			return nil, nil
		}
		return string(v), nil
	case time.Time:
		return FormatTimestamp(v), nil
	case domain.Date:
		return v.String(), nil
	case decimal.Decimal, domain.Money:
		return nil, fmt.Errorf("%T must be written through internal/dbconv, which knows the column's scale", v)
	case driver.Valuer:
		rv := reflect.ValueOf(v)
		if rv.Kind() == reflect.Pointer && rv.IsNil() {
			return nil, nil
		}
		inner, err := v.Value()
		if err != nil {
			return nil, err
		}
		return Arg(inner)
	}

	rv := reflect.ValueOf(value)
	switch rv.Kind() {
	case reflect.Pointer:
		if rv.IsNil() {
			return nil, nil
		}
		return Arg(rv.Elem().Interface())
	case reflect.String:
		return rv.String(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		u := rv.Uint()
		if u > 1<<63-1 {
			return nil, fmt.Errorf("%d overflows an integer column", u)
		}
		return int64(u), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Slice:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return rv.Bytes(), nil
		}
		if rv.IsNil() {
			return "[]", nil
		}
		return marshal(value)
	case reflect.Array:
		if rv.Type().Elem().Kind() == reflect.Uint8 {
			return nil, fmt.Errorf("%T is not a column value", value)
		}
		return marshal(value)
	case reflect.Map, reflect.Struct:
		return marshal(value)
	}
	return nil, fmt.Errorf("%T is not a column value", value)
}

func marshal(value any) (any, error) {
	encoded, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(encoded), nil
}

// scanTargets wraps each destination SQLite cannot fill directly.
func scanTargets(dest []any) []any {
	out := make([]any, len(dest))
	for i, d := range dest {
		out[i] = scanTarget(d)
	}
	return out
}

func scanTarget(dest any) any {
	switch dest.(type) {
	case nil, sql.Scanner, *any, *string, *[]byte, *bool, *int, *int16, *int32, *int64, *float64:
		return dest
	case *json.RawMessage:
		return &rawJSONTarget{dest: dest.(*json.RawMessage)}
	}
	rv := reflect.ValueOf(dest)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return dest
	}
	target := rv.Elem()
	switch {
	case target.Type() == timeType:
		return &timeTarget{dest: target}
	case target.Kind() == reflect.Pointer && target.Type().Elem() == timeType:
		return &timeTarget{dest: target, nullable: true}
	case target.Kind() == reflect.Pointer && isJSONKind(target.Type().Elem()):
		return &jsonTarget{dest: target, nullable: true}
	case isJSONKind(target.Type()):
		return &jsonTarget{dest: target}
	}
	return dest
}

func isJSONKind(t reflect.Type) bool {
	if t == rawJSONType || t == timeType || reflect.PointerTo(t).Implements(reflect.TypeOf((*sql.Scanner)(nil)).Elem()) {
		return false
	}
	switch t.Kind() {
	case reflect.Slice:
		return t.Elem().Kind() != reflect.Uint8
	case reflect.Map, reflect.Struct:
		return true
	}
	return false
}

type timeTarget struct {
	dest     reflect.Value
	nullable bool
}

func (t *timeTarget) Scan(src any) error {
	if src == nil {
		if !t.nullable {
			return errors.New("sqlitedb: NULL in a time.Time; scan into *time.Time")
		}
		t.dest.SetZero()
		return nil
	}
	var parsed time.Time
	switch v := src.(type) {
	case time.Time:
		parsed = v.UTC()
	case string:
		p, err := ParseTime(v)
		if err != nil {
			return err
		}
		parsed = p
	case []byte:
		p, err := ParseTime(string(v))
		if err != nil {
			return err
		}
		parsed = p
	default:
		return fmt.Errorf("sqlitedb: %T is not a time", src)
	}
	if t.nullable {
		t.dest.Set(reflect.ValueOf(&parsed))
	} else {
		t.dest.Set(reflect.ValueOf(parsed))
	}
	return nil
}

type jsonTarget struct {
	dest     reflect.Value
	nullable bool
}

func (j *jsonTarget) Scan(src any) error {
	var text []byte
	switch v := src.(type) {
	case nil:
		j.dest.SetZero()
		return nil
	case string:
		text = []byte(v)
	case []byte:
		text = v
	default:
		return fmt.Errorf("sqlitedb: %T is not a JSON document", src)
	}
	target := j.dest
	if j.nullable {
		fresh := reflect.New(target.Type().Elem())
		target.Set(fresh)
		target = fresh.Elem()
	}
	return json.Unmarshal(text, target.Addr().Interface())
}

type rawJSONTarget struct{ dest *json.RawMessage }

func (r *rawJSONTarget) Scan(src any) error {
	switch v := src.(type) {
	case nil:
		*r.dest = nil
	case string:
		*r.dest = json.RawMessage(v)
	case []byte:
		*r.dest = append(json.RawMessage(nil), v...)
	default:
		return fmt.Errorf("sqlitedb: %T is not a JSON document", src)
	}
	return nil
}
