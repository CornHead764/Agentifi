// Package dbconv converts the domain's exact numbers to and from SQLite
// columns. A money column stores INTEGER hundredths, so SQL can compare, order
// and SUM it exactly; every other decimal (a rate, a price, a share count, a
// percentage) stores its exact decimal text. SQLite would hand out a float64
// for a REAL, losing cents, so a float is refused on read.
//
// A NULL in a NOT NULL column is an error, never a zero: substituting zero is
// how a total comes out short with nothing in the logs.
package dbconv

import (
	"database/sql/driver"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Number is a numeric column's value, as a query argument or a scan target.
// Read, it accepts INTEGER hundredths (a money column, or an aggregate of one)
// and decimal text (any other decimal column).
type Number struct {
	Decimal decimal.Decimal
	Valid   bool
	cents   bool
}

// Value writes hundredths for a money column and decimal text otherwise.
func (n Number) Value() (driver.Value, error) {
	if !n.Valid {
		return nil, nil
	}
	if !n.cents {
		return n.Decimal.String(), nil
	}
	hundredths := n.Decimal.Round(2).Shift(2)
	if !hundredths.IsInteger() || hundredths.Abs().GreaterThanOrEqual(decimal.New(1, 18)) {
		return nil, fmt.Errorf("dbconv: %s does not fit a money column", n.Decimal)
	}
	return hundredths.IntPart(), nil
}

func (n *Number) Scan(src any) error {
	*n = Number{}
	switch v := src.(type) {
	case nil:
		return nil
	case int64:
		n.Decimal = decimal.New(v, -2)
	case string:
		d, err := decimal.NewFromString(v)
		if err != nil {
			return fmt.Errorf("dbconv: %q is not a decimal: %w", v, err)
		}
		n.Decimal = d
	case []byte:
		d, err := decimal.NewFromString(string(v))
		if err != nil {
			return fmt.Errorf("dbconv: %q is not a decimal: %w", v, err)
		}
		n.Decimal = d
	default:
		return fmt.Errorf("dbconv: a %T is not an exact number", src)
	}
	n.Valid = true
	return nil
}

// Money encodes an amount for a money column, as hundredths.
func Money(m domain.Money) Number { return Number{Decimal: m.Decimal(), Valid: true, cents: true} }

// NullMoney encodes a nullable amount. present=false writes NULL, which for
// provider_balance means "this account is manual" and for credit_limit means
// "no limit" — neither is the same fact as zero.
func NullMoney(m domain.Money, present bool) Number {
	if !present {
		return Number{}
	}
	return Money(m)
}

// Numeric encodes any other exact decimal — rate, price, share count — as
// decimal text.
func Numeric(d decimal.Decimal) Number { return Number{Decimal: d, Valid: true} }

func NullNumeric(d decimal.Decimal, present bool) Number {
	if !present {
		return Number{}
	}
	return Numeric(d)
}

// NullDate writes NULL for the zero date. An unset effective date means "same
// as the posted date"; 0001-01-01 would file the row two thousand years ago.
func NullDate(d domain.Date) *domain.Date {
	if d.IsZero() {
		return nil
	}
	return &d
}

// NullText writes NULL for the empty string, so a cleared note does not sort
// and filter as a zero-length one.
func NullText(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func NullUUID(id uuid.UUID) *uuid.UUID {
	if id == uuid.Nil {
		return nil
	}
	return &id
}

// ReadNullDate reads a nullable date column; NULL is the zero date, which
// NullDate writes back as NULL.
func ReadNullDate(t *time.Time) domain.Date {
	if t == nil {
		return domain.Date{}
	}
	return domain.DateOf(*t)
}

// ReadNullUUID reads a nullable uuid column; NULL is uuid.Nil, which NullUUID
// writes back as NULL.
func ReadNullUUID(id *uuid.UUID) uuid.UUID {
	if id == nil {
		return uuid.Nil
	}
	return *id
}

// ReadMoney reads a NOT NULL numeric column; column names it in the error.
func ReadMoney(n Number, column string) (domain.Money, error) {
	value, present, err := ReadNullMoney(n, column)
	if err != nil {
		return domain.Zero, err
	}
	if !present {
		return domain.Zero, fmt.Errorf("%s is NULL but the column is NOT NULL", column)
	}
	return value, nil
}

// ReadNullMoney reads a nullable numeric column, reporting whether it was set
// (the domain's Has flags).
func ReadNullMoney(n Number, column string) (domain.Money, bool, error) {
	d, present, err := ReadNullDecimal(n, column)
	return domain.FromDecimal(d), present, err
}

func ReadNullDecimal(n Number, column string) (decimal.Decimal, bool, error) {
	if !n.Valid {
		return decimal.Zero, false, nil
	}
	return n.Decimal, true, nil
}
