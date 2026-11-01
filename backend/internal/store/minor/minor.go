// Package minor is a money column as SQLite stores it: integer minor units of
// a currency that the row, or a row joined to it, names. Its value is not
// exported, so a generated row type holding an Amount cannot be read as a
// number by mistake; Money, given the currency, is the only way out
// (ground rule 1).
package minor

import (
	"database/sql/driver"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
)

type Amount struct{ units int64 }

// Money reads the amount in currency, the ISO 4217 code that decides where its
// decimal point goes.
func (a Amount) Money(currency string) (domain.Money, error) {
	value, err := minorunits.FromMinor(a.units, currency)
	if err != nil {
		return domain.Money{}, err
	}
	return domain.FromDecimal(value), nil
}

// FromMoney is the write side; an amount finer than currency's minor unit is
// refused rather than rounded.
func FromMoney(m domain.Money, currency string) (Amount, error) {
	units, err := minorunits.ToMinor(m.Decimal(), currency)
	return Amount{units}, err
}

func (a *Amount) Scan(src any) error {
	units, ok := src.(int64)
	if !ok {
		return fmt.Errorf("minor: scanning %T into an amount", src)
	}
	a.units = units
	return nil
}

func (a Amount) Value() (driver.Value, error) { return a.units, nil }
