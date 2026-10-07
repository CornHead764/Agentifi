package domain

// Equity in a financed asset. Debt is stored negative, so equity is a sum, not
// a subtraction: a $420,000 house against a mortgage stored as -$284,000 is
// $136,000, and subtracting the stored balance would plausibly report $704,000.

// Equity is what is left of an asset once the loans secured on it are counted.
// loanBalances are in storage form (negative); one stored positive still
// counts as debt rather than inflating the asset.
func Equity(assetValue Money, loanBalances []Money) Money {
	equity := assetValue
	for _, balance := range loanBalances {
		equity = equity.Add(balance.Abs().Neg())
	}
	return equity.Round()
}

// EquityAt is the household's total equity on one day, as the Net Worth
// chart's Equity view draws it: every asset account's balance less |balance|
// of every loan with a SecuredByAccountID, honoring IncludeInNetWorth on each
// side as NetWorthAt does. Everything else is ignored.
func EquityAt(accounts []Account, balances map[ID]Money) Money {
	equity := Zero
	for _, account := range accounts {
		if !account.CountsInNetWorth() {
			continue
		}
		switch {
		case account.Kind == KindAsset:
			equity = equity.Add(balances[account.ID])
		case account.SecuredByAccountID != "":
			equity = equity.Add(balances[account.ID].Abs().Neg())
		}
	}
	return equity.Round()
}

// LoanToValue is the fraction of the asset financed, as a plain ratio (0.6762
// for 67.62%). False for an asset worth nothing: zero would read as "nothing
// is owed".
func LoanToValue(assetValue Money, loanBalances []Money) (Rate, bool) {
	owed := Zero
	for _, balance := range loanBalances {
		owed = owed.Add(balance.Abs())
	}
	return Ratio(owed, assetValue)
}
