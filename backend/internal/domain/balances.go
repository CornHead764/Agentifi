package domain

import (
	"fmt"
	"sort"

	"github.com/shopspring/decimal"
)

// Account balances. A past balance walks backwards from the provider's current
// figure; it never accumulates forward from the ledger, which drifts whenever
// a payment has left the payer but not been applied by the lender.
//
// Every sum here is native (NativeAmount, not Amount): the opening balance,
// provider balance, goal reserves and holds are all in the account's own
// currency, and Amount is converted into the space's primary.

// LedgerTotal sums everything that moved money, pending included.
func LedgerTotal(postings []Posting) Money {
	return balanceSumIf(postings, CountsTowardBalance)
}

func SettledTotal(postings []Posting) Money {
	return balanceSumIf(postings, IsSettled)
}

func PendingTotal(postings []Posting) Money {
	return balanceSumIf(postings, func(p Posting) bool {
		return CountsTowardBalance(p) && p.Txn.IsPending
	})
}

// AccountBalance is the account's current balance. Connected accounts take the
// provider's figure, normalized on ingest so debt is negative; for manual
// accounts the transactions are the balance.
func AccountBalance(account Account, postings []Posting) Money {
	if account.HasProviderBalance {
		return account.ProviderBalance.Round()
	}
	return Total(account.OpeningBalance, SettledTotal(postings))
}

// BalanceWithPending is the balance plus authorizations the bank has not
// settled.
func BalanceWithPending(account Account, postings []Posting) Money {
	return Total(AccountBalance(account, postings), PendingTotal(postings))
}

// AvailableBalance is the balance less goal reserves and pending holds.
func AvailableBalance(account Account, postings []Posting) Money {
	balance := AccountBalance(account, postings)
	return Total(balance, account.GoalBalance.Neg(), account.PendingHolds.Neg())
}

// BalanceAsOf is the account's balance at the end of asOf: zero before its
// HistoryStart, LedgerBalanceAsOf from then on. Every balance line and
// net-worth total reads this.
func BalanceAsOf(account Account, postings []Posting, asOf Date, mode DateMode) Money {
	if !HasHistoryOn(account, postings, asOf) {
		return Zero
	}
	return LedgerBalanceAsOf(account, postings, asOf, mode)
}

// LedgerBalanceAsOf is the ledger's balance at the end of asOf, blind to the
// history start: connected accounts walk back from the provider's figure,
// manual ones forward from the opening balance. Before the history start it is
// the balance the ledger implies (what a register window seeds from), not what
// the account held.
func LedgerBalanceAsOf(account Account, postings []Posting, asOf Date, mode DateMode) Money {
	if !account.HasProviderBalance {
		settledThrough := balanceSumIf(postings, func(p Posting) bool {
			return CountsTowardBalance(p) &&
				!p.Txn.IsPending &&
				ReportingDate(p.Txn, mode).NotAfter(asOf)
		})
		return Total(account.OpeningBalance, settledThrough)
	}

	after := balanceSumIf(postings, func(p Posting) bool {
		return CountsTowardBalance(p) && ReportingDate(p.Txn, mode).After(asOf)
	})
	return Total(account.ProviderBalance, after.Neg())
}

// OpeningBalanceForConnected is the synthetic row that reconciles a connected
// ledger to the provider. Only for when the user has not stated an opening
// balance; a stated one is written verbatim by the caller.
func OpeningBalanceForConnected(account Account, postings []Posting) (Money, error) {
	if !account.HasProviderBalance {
		return Zero, fmt.Errorf("account %s is manual; its ledger is the balance", account.ID)
	}
	return Total(account.ProviderBalance, SettledTotal(postings).Neg()), nil
}

// CreditUsedPct is the fraction of the credit line in use, false when there is
// no limit. It takes the balance so the header card and this cannot disagree.
func CreditUsedPct(account Account, balance Money) (Rate, bool) {
	if !account.HasCreditLimit || account.CreditLimit.IsZero() {
		return Rate{}, false
	}
	return Ratio(balance.Abs(), account.CreditLimit)
}

// APRAsRate reads a reported APR as a rate: 24.99% arrives as 24.99 from one
// source and 0.2499 from another, with nothing saying which. A figure of 1 or
// more is a percentage, since no household card charges 100%. It misreads a
// promotional rate under 1% sent as a percentage (0.75 read as 75%); those are
// almost always exactly 0. False for a negative figure or one still above 100%
// after scaling: a wrong APR is worse than an absent one.
func APRAsRate(reported Rate) (Rate, bool) {
	if reported.IsNegative() {
		return Rate{}, false
	}
	rate := reported
	if reported.GreaterThanOrEqual(oneHundredPercent) {
		rate = reported.Div(hundred)
	}
	if rate.GreaterThan(oneHundredPercent) {
		return Rate{}, false
	}
	return rate, true
}

var (
	// A rate of 1 is 100%: the scale boundary and the ceiling.
	oneHundredPercent = decimal.NewFromInt(1)
	hundred           = decimal.NewFromInt(100)
)

// RunningBalances is every row's running balance, oldest first by date then
// id. It returns the whole account because a change to any row recomputes
// the account's stored figures in full (service.RecomputeRunningBalances).
func RunningBalances(account Account, postings []Posting, mode DateMode) map[ID]Money {
	counted := make([]Posting, 0, len(postings))
	for _, p := range postings {
		if CountsTowardBalance(p) {
			counted = append(counted, p)
		}
	}
	sort.Slice(counted, func(i, j int) bool {
		on, other := ReportingDate(counted[i].Txn, mode), ReportingDate(counted[j].Txn, mode)
		if on != other {
			return on.Before(other)
		}
		return counted[i].Txn.ID < counted[j].Txn.ID
	})

	running := account.OpeningBalance
	if account.HasProviderBalance {
		// A connected ledger must end on the provider's figure, so anchor at
		// that end.
		running = Total(account.ProviderBalance, Sum(counted, Posting.NativeAmount).Neg())
	}

	out := make(map[ID]Money, len(counted))
	for _, posting := range counted {
		running = running.Add(posting.NativeAmount())
		out[posting.Txn.ID] = running.Round()
	}
	return out
}

func balanceSumIf(postings []Posting, keep func(Posting) bool) Money {
	sum := Zero
	for _, p := range postings {
		if keep(p) {
			sum = sum.Add(p.NativeAmount())
		}
	}
	return sum.Round()
}
