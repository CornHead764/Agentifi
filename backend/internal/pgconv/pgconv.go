// Package pgconv converts the domain's exact numbers to and from Postgres
// numeric columns, as pgtype.Numeric built from the decimal's coefficient and
// exponent. pgx would encode or decode a float64 if offered one, losing cents,
// so every numeric column goes through here.
//
// A NULL in a NOT NULL column is an error, never a zero: substituting zero is
// how a total comes out short with nothing in the logs.
package pgconv

import (
	"fmt"
	"math/big"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Numeric encodes any exact decimal — amount, rate, price, share count.
func Numeric(d decimal.Decimal) pgtype.Numeric {
	return pgtype.Numeric{Int: d.Coefficient(), Exp: d.Exponent(), Valid: true}
}

func Money(m domain.Money) pgtype.Numeric { return Numeric(m.Decimal()) }

// NullMoney encodes a nullable amount. present=false writes NULL, which for
// provider_balance means "this account is manual" and for credit_limit means
// "no limit" — neither is the same fact as zero.
func NullMoney(m domain.Money, present bool) pgtype.Numeric {
	if !present {
		return pgtype.Numeric{}
	}
	return Money(m)
}

func NullNumeric(d decimal.Decimal, present bool) pgtype.Numeric {
	if !present {
		return pgtype.Numeric{}
	}
	return Numeric(d)
}

// NullDate writes NULL for the zero date. An unset effective date means "same
// as the posted date"; 0001-01-01 would file the row two thousand years ago.
func NullDate(d domain.Date) *time.Time {
	if d.IsZero() {
		return nil
	}
	t := d.Time()
	return &t
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
func ReadMoney(n pgtype.Numeric, column string) (domain.Money, error) {
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
func ReadNullMoney(n pgtype.Numeric, column string) (domain.Money, bool, error) {
	d, present, err := ReadNullDecimal(n, column)
	return domain.FromDecimal(d), present, err
}

func ReadNullDecimal(n pgtype.Numeric, column string) (decimal.Decimal, bool, error) {
	if n.NaN || n.InfinityModifier != pgtype.Finite {
		return decimal.Zero, false, fmt.Errorf("%s is not a finite number", column)
	}
	if !n.Valid || n.Int == nil {
		return decimal.Zero, false, nil
	}
	return decimal.NewFromBigInt(new(big.Int).Set(n.Int), n.Exp), true, nil
}
