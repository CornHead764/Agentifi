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
//
// The account's threshold is in its own currency and is compared with
// balance; the institution's is in the space's primary currency and is
// compared with inPrimary, the same balance restated in it.
func HiddenForSmallBalance(balance, inPrimary Money, account, institution SmallBalanceThreshold) bool {
	if account.Set {
		return balance.Abs().LessThan(account.Amount)
	}
	if !institution.Set {
		return false
	}
	return inPrimary.Abs().LessThan(institution.Amount)
}

// HoldsNothing reports whether an account's balance is zero to the cent: the
// accounts "ignore every empty account at this institution" reaches
// (calculations.md §3). Not a display rule.
func HoldsNothing(balance Money) bool { return balance.Round().IsZero() }
