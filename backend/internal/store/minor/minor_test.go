package minor

import (
	"testing"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestAmountReadsInItsCurrency(t *testing.T) {
	var a Amount
	if err := a.Scan(int64(-1234)); err != nil {
		t.Fatal(err)
	}
	for currency, want := range map[string]string{"USD": "-12.34", "JPY": "-1234", "KWD": "-1.234"} {
		got, err := a.Money(currency)
		if err != nil || got.Decimal().String() != want {
			t.Errorf("Money(%s) = %s, %v; want %s", currency, got, err, want)
		}
	}
	if err := a.Scan("12.34"); err == nil {
		t.Error("a text value scanned as an amount")
	}
	if _, err := FromMoney(domain.MustFromString("0.001"), "USD"); err == nil {
		t.Error("an amount finer than a cent was written")
	}
}

func TestAmountStoresMinorUnits(t *testing.T) {
	for _, c := range []struct {
		amount, currency string
		want             int64
	}{{"12.34", "USD", 1234}, {"12", "JPY", 12}, {"12.34", "KWD", 12340}} {
		amount, err := FromMoney(domain.MustFromString(c.amount), c.currency)
		if err != nil {
			t.Fatal(err)
		}
		if got, err := amount.Value(); err != nil || got != c.want {
			t.Errorf("FromMoney(%s %s) stores %v, %v; want %d", c.amount, c.currency, got, err, c.want)
		}
	}
}
