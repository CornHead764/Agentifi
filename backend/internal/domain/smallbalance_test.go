package domain

import "testing"

func TestHiddenForSmallBalance(t *testing.T) {
	none := SmallBalanceThreshold{}
	under := func(amount string) SmallBalanceThreshold {
		return SmallBalanceThreshold{Amount: MustFromString(amount), Set: true}
	}

	cases := []struct {
		name        string
		balance     string
		account     SmallBalanceThreshold
		institution SmallBalanceThreshold
		want        bool
	}{
		{"no rule anywhere shows a zero balance", "0.00", none, none, false},
		{"institution rule hides a zero balance", "0.00", none, under("1.00"), true},
		{"institution rule hides dust", "0.37", none, under("1.00"), true},
		{"institution rule shows a balance above it", "18.40", none, under("1.00"), false},
		{"a balance exactly at the threshold shows", "1.00", none, under("1.00"), false},
		{"negative dust hides by its magnitude", "-0.12", none, under("1.00"), true},
		{"a debt above the threshold shows", "-640.25", none, under("1.00"), false},
		{"account threshold overrides the institution's", "7.50", under("10.00"), under("1.00"), true},
		{"a lower account threshold shows what the institution would hide", "0.60", under("0.50"), under("1.00"), false},
		{"account zero always shows under an institution rule", "0.00", under("0.00"), under("1.00"), false},
		{"account rule alone hides", "3.00", under("5.00"), none, true},
		{"institution zero hides nothing", "0.00", none, under("0.00"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			balance := MustFromString(tc.balance)
			got := HiddenForSmallBalance(balance, balance, tc.account, tc.institution)
			if got != tc.want {
				t.Fatalf("HiddenForSmallBalance(%s) = %v, want %v", tc.balance, got, tc.want)
			}
		})
	}
}

// A yen account under an institution rule kept in dollars: 500 yen is 3.25
// dollars at 0.0065, above a 1.00 threshold, and 100 yen (0.65) is below it.
// The account's own rule is in yen.
func TestASmallBalanceRuleComparesInTheRulesOwnCurrency(t *testing.T) {
	dollars := SmallBalanceThreshold{Amount: FromMinor(100, 2), Set: true}
	yen := SmallBalanceThreshold{Amount: FromMinor(1000, 0), Set: true}
	rate := MustFromString("0.0065").Decimal()
	for _, tc := range []struct {
		yen     int64
		account SmallBalanceThreshold
		want    bool
	}{
		{500, SmallBalanceThreshold{}, false},
		{100, SmallBalanceThreshold{}, true},
		{500, yen, true},
	} {
		balance := FromMinor(tc.yen, 0)
		got := HiddenForSmallBalance(balance, balance.Convert(rate, 2).Round(), tc.account, dollars)
		if got != tc.want {
			t.Errorf("%d yen: hidden = %v, want %v", tc.yen, got, tc.want)
		}
	}
}

func TestHoldsNothing(t *testing.T) {
	cases := []struct {
		balance string
		want    bool
	}{
		{"0.00", true},
		{"-0.00", true},
		{"0.004", true},
		{"0.01", false},
		{"-0.01", false},
		{"12.50", false},
	}
	for _, tc := range cases {
		if got := HoldsNothing(MustFromString(tc.balance)); got != tc.want {
			t.Errorf("HoldsNothing(%s) = %v, want %v", tc.balance, got, tc.want)
		}
	}
}
