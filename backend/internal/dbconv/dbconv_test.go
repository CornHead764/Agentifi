package dbconv

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestReadMoneyRefusesNull(t *testing.T) {
	if _, err := ReadMoney(Number{}, "t.amount"); err == nil {
		t.Error("a NULL in a NOT NULL column read as zero")
	}
	if _, present, err := ReadNullMoney(Number{}, "t.amount"); err != nil || present {
		t.Errorf("a NULL in a nullable column: present=%v err=%v", present, err)
	}
}

func TestScanRefusesFloats(t *testing.T) {
	var n Number
	if err := n.Scan(12.5); err == nil {
		t.Error("a REAL read without error")
	}
}

func TestMoneyStoresHundredths(t *testing.T) {
	for literal, want := range map[string]int64{"0": 0, "1234.56": 123456, "-0.07": -7, "12345678901.23": 1234567890123, "5": 500} {
		got, err := Money(domain.MustFromString(literal)).Value()
		if err != nil || got != want {
			t.Errorf("%s: stored %v (%v), want %d", literal, got, err, want)
		}
	}
}

func TestMoneyRoundTrips(t *testing.T) {
	m := domain.MustFromString("-1234.56")
	stored, err := Money(m).Value()
	if err != nil {
		t.Fatal(err)
	}
	var back Number
	if err := back.Scan(stored); err != nil {
		t.Fatal(err)
	}
	got, err := ReadMoney(back, "t.amount")
	if err != nil || !got.Equal(m) {
		t.Fatalf("round-tripped %s as %s (%v)", m, got, err)
	}
}

func TestNumericKeepsEveryDigit(t *testing.T) {
	for _, literal := range []string{"0", "1234.56", "-0.07", "12345678901.23", "1.0000000001"} {
		d := decimal.RequireFromString(literal)
		stored, err := Numeric(d).Value()
		if err != nil {
			t.Fatal(err)
		}
		var back Number
		if err := back.Scan(stored); err != nil {
			t.Fatal(err)
		}
		if !back.Decimal.Equal(d) {
			t.Errorf("%s: round-tripped as %s", literal, back.Decimal)
		}
	}
}

func TestNullMoneyWritesNullWhenAbsent(t *testing.T) {
	if n := NullMoney(domain.Zero, false); n.Valid {
		t.Error("an absent amount encoded as a value")
	}
	// Zero present is a different fact from absent, and has to survive as one.
	stored, err := NullMoney(domain.Zero, true).Value()
	if err != nil || stored != int64(0) {
		t.Errorf("a present zero stored as %v (%v)", stored, err)
	}
}

func TestNullDateAndUUIDRoundTrip(t *testing.T) {
	if NullDate(domain.Date{}) != nil {
		t.Error("a zero date was written as a value, not NULL")
	}
	day := domain.NewDate(2026, time.March, 14)
	if got := NullDate(day); got == nil || *got != day {
		t.Errorf("a date was written as %v, want %v", got, day)
	}
	midnight := day.Time()
	if got := ReadNullDate(&midnight); got != day {
		t.Errorf("a date came back as %v, want %v", got, day)
	}
	if got := ReadNullDate(nil); !got.IsZero() {
		t.Errorf("NULL read as %v", got)
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
