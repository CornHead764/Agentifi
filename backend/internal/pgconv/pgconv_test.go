package pgconv

import (
	"math/big"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestReadMoneyRefusesNull(t *testing.T) {
	if _, err := ReadMoney(pgtype.Numeric{}, "t.amount"); err == nil {
		t.Error("a NULL in a NOT NULL column read as zero")
	}
	if _, present, err := ReadNullMoney(pgtype.Numeric{}, "t.amount"); err != nil || present {
		t.Errorf("a NULL in a nullable column: present=%v err=%v", present, err)
	}
}

func TestReadRefusesNaNAndInfinity(t *testing.T) {
	for _, n := range []pgtype.Numeric{
		{NaN: true, Valid: true},
		{InfinityModifier: pgtype.Infinity, Valid: true},
		{InfinityModifier: pgtype.NegativeInfinity, Valid: true},
	} {
		if _, _, err := ReadNullMoney(n, "t.amount"); err == nil {
			t.Errorf("%+v read without error", n)
		}
	}
}

func TestReadMoneyRoundTrips(t *testing.T) {
	m := domain.MustFromString("-1234.56")
	back, err := ReadMoney(Money(m), "t.amount")
	if err != nil || !back.Equal(m) {
		t.Fatalf("round-tripped %s as %s (%v)", m, back, err)
	}
}

func TestNumericKeepsTheCoefficientAndExponent(t *testing.T) {
	for _, literal := range []string{"0", "1234.56", "-0.07", "12345678901.23", "1.0000000001"} {
		d := decimal.RequireFromString(literal)
		n := Numeric(d)
		if !n.Valid {
			t.Fatalf("%s: encoded as NULL", literal)
		}
		back := decimal.NewFromBigInt(new(big.Int).Set(n.Int), n.Exp)
		if !back.Equal(d) {
			t.Errorf("%s: round-tripped as %s", literal, back)
		}
	}
}

func TestMoneyDoesNotRound(t *testing.T) {
	m := domain.MustFromString("1234.56")
	n := Money(m)
	back := domain.FromDecimal(decimal.NewFromBigInt(new(big.Int).Set(n.Int), n.Exp))
	if !back.Equal(m) {
		t.Fatalf("round-tripped %s as %s", m, back)
	}
}

func TestNullMoneyWritesNullWhenAbsent(t *testing.T) {
	if n := NullMoney(domain.Zero, false); n.Valid {
		t.Error("an absent amount encoded as a value")
	}
	// Zero present is a different fact from absent, and has to survive as one.
	n := NullMoney(domain.Zero, true)
	if !n.Valid {
		t.Fatal("a present zero encoded as NULL")
	}
	if n.InfinityModifier != pgtype.Finite {
		t.Errorf("zero encoded as %v", n.InfinityModifier)
	}
	if decimal.NewFromBigInt(new(big.Int).Set(n.Int), n.Exp).Sign() != 0 {
		t.Error("a present zero did not encode as zero")
	}
}

func TestNullDateAndUUIDRoundTrip(t *testing.T) {
	if got := ReadNullDate(NullDate(domain.Date{})); !got.IsZero() {
		t.Errorf("a zero date came back as %v", got)
	}
	day := domain.NewDate(2026, time.March, 14)
	if got := ReadNullDate(NullDate(day)); got != day {
		t.Errorf("a date came back as %v, want %v", got, day)
	}
	if NullUUID(uuid.Nil) != nil {
		t.Error("uuid.Nil was written as a value, not NULL")
	}
	if got := ReadNullUUID(nil); got != uuid.Nil {
		t.Errorf("NULL read as %v", got)
	}
	id := uuid.New()
	if got := ReadNullUUID(NullUUID(id)); got != id {
		t.Errorf("an id came back as %v, want %v", got, id)
	}
}
