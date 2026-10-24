package store

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/google/uuid"
	"github.com/shopspring/decimal"
	"modernc.org/sqlite"
)

// Leave room for fixed predicates below SQLite's 32766-variable ceiling.
const sqliteBatchSize = 10000

// InArgs supplies the parenthesized placeholders and arguments replacing ANY.
// Empty input becomes IN (NULL), which never matches a non-null ID. Do not use
// this helper for NOT IN; handle empty exclusion lists by omitting the predicate.
func InArgs[T any](ids []T) (string, []any) {
	if len(ids) == 0 {
		return "(NULL)", nil
	}
	args := make([]any, len(ids))
	for i, id := range ids {
		args[i] = id
	}
	return "(" + strings.TrimSuffix(strings.Repeat("?,", len(ids)), ",") + ")", args
}

func toMinor(amount decimal.Decimal, currency string) (int64, error) {
	return minorunits.ToMinor(amount, currency)
}
func fromMinor(amount int64, currency string) (decimal.Decimal, error) {
	return minorunits.FromMinor(amount, currency)
}
func moneyFromMinor(amount int64, currency string) (domain.Money, error) {
	value, err := fromMinor(amount, currency)
	return domain.FromDecimal(value), err
}
func readMoney(dest *domain.Money, amount int64, currency string) error {
	value, err := moneyFromMinor(amount, currency)
	if err == nil {
		*dest = value
	}
	return err
}

// NullText maps an unset string to SQL/JSON null.
func NullText(value string) *string { return PtrIf(value, value != "") }

// NullUUID maps an unset identifier to SQL/JSON null.
func NullUUID(value uuid.UUID) *uuid.UUID { return PtrIf(value, value != uuid.Nil) }

// NullDate maps an unset date to SQL null.
func NullDate(value domain.Date) any {
	if value.IsZero() {
		return nil
	}
	return value.String()
}

func unixMillis(t time.Time) int64      { return t.UnixMilli() }
func fromUnixMillis(ms int64) time.Time { return time.UnixMilli(ms).UTC() }

// nullValue turns a scanned sql.Null[T] into the domain's optional pointer.
// Scan nullable timestamps into sql.Null[int64], then apply nullableTime.
func nullValue[T any](v sql.Null[T]) *T {
	if !v.Valid {
		return nil
	}
	return &v.V
}
func nullableTime(v sql.Null[int64]) *time.Time {
	if !v.Valid {
		return nil
	}
	t := fromUnixMillis(v.V)
	return &t
}
func nullableMillis(t *time.Time) any {
	if t == nil {
		return nil
	}
	return unixMillis(*t)
}

// JSONArray implements Scanner and Valuer for JSON TEXT array columns. A nil
// slice is written as [], never JSON null. SQL NULL remains a nil slice.
type JSONArray[T any] []T

func (a JSONArray[T]) Value() (driver.Value, error) {
	if a == nil {
		return "[]", nil
	}
	b, err := json.Marshal(a)
	return string(b), err
}
func (a *JSONArray[T]) Scan(src any) error {
	if src == nil {
		*a = nil
		return nil
	}
	var b []byte
	switch v := src.(type) {
	case string:
		b = []byte(v)
	case []byte:
		b = v
	default:
		return fmt.Errorf("store: JSON array from %T", src)
	}
	if !strings.HasPrefix(strings.TrimSpace(string(b)), "[") {
		return errors.New("store: expected JSON array")
	}
	return json.Unmarshal(b, a)
}

// ConstraintError classifies SQLite constraints without exposing driver types.
type ConstraintError struct {
	Code  string
	Cause error
}

func (e *ConstraintError) Error() string { return e.Cause.Error() }
func (e *ConstraintError) Unwrap() error { return e.Cause }

// sqliteError retains the underlying driver error for errors.As callers.
func sqliteError(err error) error {
	var constraint *sqlite.Error
	if !errors.As(err, &constraint) {
		return err
	}
	code := ""
	switch constraint.Code() {
	case 1555, 2067:
		code = "23505" // PRIMARYKEY, UNIQUE
	case 787:
		code = "23503" // FOREIGNKEY
	case 1299:
		code = "23502" // NOTNULL
	case 275:
		code = "23514" // CHECK
	}
	if code == "" {
		return err
	}
	return &ConstraintError{Code: code, Cause: err}
}

func sqliteExecOne(ctx context.Context, db sqlitedb.Conn, what, query string, args ...any) error {
	result, err := db.ExecContext(ctx, query, args...)
	if err != nil {
		return wrap(what, err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return wrap(what, err)
	}
	if n == 0 {
		return wrap(what, ErrNotFound)
	}
	return nil
}

func sqliteQueryAll[T any](ctx context.Context, db sqlitedb.Conn, what string, scan func(scanner) (T, error), query string, args ...any) ([]T, error) {
	rows, err := db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, wrap(what, err)
	}
	defer rows.Close()
	out := []T{}
	for rows.Next() {
		item, err := scan(rows)
		if err != nil {
			return nil, wrap(what, err)
		}
		out = append(out, item)
	}
	return out, wrap(what, rows.Err())
}
