package domain

import "sort"

// Net worth.
//
// Assets less debt, over the accounts flagged into net worth, read from each
// account's materialized balance history. Debt balances are stored negative,
// so a sum of balances is already the net figure; the parts are reported
// separately so the chart can show both.

// NetWorth is one point on the net-worth chart, with its parts.
type NetWorth struct {
	On     Date
	Assets Money
	// Debt is reported as a positive figure even though balances are stored
	// negative.
	Debt Money
	Net  Money
}

// DebtToAsset reports ok=false when there are no assets to divide by.
func (n NetWorth) DebtToAsset() (Rate, bool) {
	return Ratio(n.Debt, n.Assets)
}

// BalancesOn is each account's most recent balance at or before on.
//
// Reads the materialized daily history rather than re-walking the ledger. An
// account with no point at or before on did not exist yet and is absent from
// the result — not zero, absent, so callers can tell the difference.
func BalancesOn(history []BalancePoint, on Date) map[ID]Money {
	latest := map[ID]BalancePoint{}
	for _, point := range history {
		if point.On.After(on) {
			continue
		}
		current, seen := latest[point.AccountID]
		if !seen || point.On.After(current.On) {
			latest[point.AccountID] = point
		}
	}

	out := make(map[ID]Money, len(latest))
	for accountID, point := range latest {
		out[accountID] = point.Balance
	}
	return out
}

// NetWorthAt is net worth on one day, over the accounts flagged into it.
//
// IncludeInNetWorth is its own flag — an account can be reported on and still
// be left out of net worth, or the reverse. An ignored account is out whatever
// the flag says (Account.CountsInNetWorth).
func NetWorthAt(accounts []Account, balances map[ID]Money, on Date) NetWorth {
	assets, debt := Zero, Zero
	for _, account := range accounts {
		if !account.CountsInNetWorth() {
			continue
		}
		switch balance := balances[account.ID]; {
		case balance.IsPositive():
			assets = assets.Add(balance)
		case balance.IsNegative():
			debt = debt.Add(balance)
		}
	}
	return NetWorth{
		On:     on,
		Assets: assets.Round(),
		Debt:   debt.Neg().Round(),
		Net:    Total(assets, debt),
	}
}

func NetWorthChange(start, end NetWorth) Money {
	return Total(end.Net, start.Net.Neg())
}

// NetWorthChangePct is the percentage move over a window, reporting ok=false
// when the window opened at zero.
//
// Divides by the magnitude, so a net worth climbing out of debt reads as a
// positive change rather than an inverted one.
func NetWorthChangePct(start, end NetWorth) (Rate, bool) {
	return Percent(end.Net.Sub(start.Net), start.Net.Abs())
}

// GroupChange is a row in the Net Worth accounts panel.
//
// The figures are for the selected window, not month over month, and the
// percentage is relative to the window's start.
type GroupChange struct {
	Key   string
	Start Money
	End   Money
}

func (g GroupChange) Change() Money {
	return Total(g.End, g.Start.Neg())
}

func (g GroupChange) ChangePct() (Rate, bool) {
	return Percent(g.End.Sub(g.Start), g.Start.Abs())
}

// GroupChanges rolls accounts up by kind over a window, or into one group named
// key when the caller supplies one.
//
// Ordered by key so the panel does not reshuffle between renders.
func GroupChanges(
	accounts []Account,
	startBalances, endBalances map[ID]Money,
	key string,
) []GroupChange {
	starts, ends := map[string]Money{}, map[string]Money{}
	var order []string
	for _, account := range accounts {
		if !account.CountsInNetWorth() {
			continue
		}
		group := key
		if group == "" {
			group = string(account.Kind)
		}
		if _, seen := starts[group]; !seen {
			order = append(order, group)
		}
		starts[group] = starts[group].Add(startBalances[account.ID])
		ends[group] = ends[group].Add(endBalances[account.ID])
	}

	sort.Strings(order)
	out := make([]GroupChange, 0, len(order))
	for _, group := range order {
		out = append(out, GroupChange{
			Key:   group,
			Start: starts[group].Round(),
			End:   ends[group].Round(),
		})
	}
	return out
}
