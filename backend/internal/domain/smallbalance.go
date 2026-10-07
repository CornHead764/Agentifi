package domain

// Hiding accounts that hold next to nothing, such as the per-coin dust
// accounts a crypto exchange reports through SimpleFIN. Display only: the
// account still counts toward every total, report and picker.

// SmallBalanceThreshold is one level's setting. Set false is "no rule here",
// which differs from a threshold of zero: an account's zero means "always
// show" and overrides its institution's rule.
type SmallBalanceThreshold struct {
	Amount Money
	Set    bool
}

// HiddenForSmallBalance reports whether an account is left out of the account
// list for holding too little (calculations.md §3). The account's threshold
// wins over the institution's. The comparison is on the magnitude and strict:
// a balance exactly at the threshold shows.
func HiddenForSmallBalance(balance Money, account, institution SmallBalanceThreshold) bool {
	threshold := institution
	if account.Set {
		threshold = account
	}
	if !threshold.Set {
		return false
	}
	return balance.Abs().LessThan(threshold.Amount)
}

// HoldsNothing reports whether an account's balance is zero to the cent: the
// accounts "ignore every empty account at this institution" reaches
// (calculations.md §3). Not a display rule.
func HoldsNothing(balance Money) bool { return balance.Round().IsZero() }
