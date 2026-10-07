package domain

import "fmt"

// Guarding against a bank feed that silently drops a balance. A connected
// account with a stable, material balance that suddenly reports 0.00 on a
// healthy connection is almost always the aggregator's null coerced to zero,
// and applying it can drop a mortgage off the debt side of net worth. A card
// paid off in full reads 0.00 too, so a suspect zero is withheld only when the
// account's own transactions do not explain it (ExplainReportedBalance). When
// it is withheld, the sync keeps the last-known balance and records the zero
// for the household to confirm or dismiss.

const (
	// SuspectResetThresholdText is the smallest prior balance magnitude
	// worth guarding; below it, accounts legitimately drift to zero.
	SuspectResetThresholdText = "100"

	// SuspectResetMinDays is how long a non-zero balance must have stood
	// before a drop to zero is treated as suspect.
	SuspectResetMinDays = 14
)

var SuspectResetThreshold = MustFromString(SuspectResetThresholdText)

// SuspectBalanceReset reports whether an incoming balance should be withheld
// as the feed dropping a figure rather than a real change.
//
//   - establishedBalance / hasEstablished: the last-known non-zero balance,
//     read from snapshot history on purpose: an account whose feed already
//     dropped to 0 has a current balance of 0, and the figure to restore
//     lives only in history.
//   - incoming / providerReported: a provider that reported nothing is
//     treated like a suspicious zero.
//   - establishedDays: how long the non-zero run persisted; zero (no history)
//     means apply.
//   - acceptZero: the household confirmed zeros are real for this account.
func SuspectBalanceReset(
	establishedBalance Money, hasEstablished bool,
	incoming Money, providerReported bool,
	establishedDays int, acceptZero bool,
) bool {
	if acceptZero || !hasEstablished {
		return false
	}
	incomingIsZero := !providerReported || incoming.IsZero()
	if !incomingIsZero {
		return false
	}
	if establishedBalance.Abs().LessThan(SuspectResetThreshold) {
		return false
	}
	return establishedDays >= SuspectResetMinDays
}

// BalanceFeed is what a sync knows about the transactions behind a reported
// balance.
type BalanceFeed int

const (
	// FeedRead: this sync read the account's transactions.
	FeedRead BalanceFeed = iota
	// FeedUnread: the read failed, the provider flagged the account's bank, or
	// the run read no transactions at all.
	FeedUnread
	// FeedAbsent: the provider has never sent a transaction for this account,
	// as with a loan the aggregator reports only as a balance.
	FeedAbsent
)

// BalanceExplanation compares a reported balance with the trusted balance
// before it plus what the ledger gained since.
type BalanceExplanation struct {
	Feed        BalanceFeed
	Previous    Money
	Reported    Money
	HasReported bool
	// Changes counts the rows whose contribution to the balance changed, pending
	// rows included; Movement is their net. Expected is Previous + Movement.
	Changes  int
	Movement Money
	Expected Money
	// Explained: the ledger reaches the reported figure, so it is applied.
	Explained bool
}

// TrustedBalance is the figure a suspect balance is explained from, and the
// one kept while it is held: the stored provider figure while it is a hold or
// is not itself a zero, otherwise the last established balance from history,
// because a stored zero may be the dropped figure. fromStored says which.
func TrustedBalance(stored Money, hasStored, held bool, established Money) (balance Money, fromStored bool) {
	if hasStored && (held || !stored.IsZero()) {
		return stored, true
	}
	return established, false
}

// ExplainReportedBalance reports whether the account's transactions explain a
// reported balance. before and after are the account's rows either side of
// this sync's import, compared by id, so a new row, a pending row posting at a
// new amount and a pending row retired all count once.
//
// The reported figure is explained when previous plus the change lands on it
// to the cent, counting pending rows (as the ledger's walk does) or counting
// only posted rows: the feed does not say whether a balance includes pending
// rows, and banks differ. Nothing is explained without a reported figure or
// without transactions read this sync; Expected then still carries the ledger
// forward, so a held balance includes the rows that did arrive.
func ExplainReportedBalance(
	previous, reported Money, hasReported bool,
	before, after []Transaction, feed BalanceFeed,
) BalanceExplanation {
	withPending, changes := ledgerMovement(before, after, CountsTowardBalance)
	posted, _ := ledgerMovement(before, after, IsSettled)
	expected := Total(previous, withPending)
	out := BalanceExplanation{
		Feed:        feed,
		Previous:    previous,
		Reported:    reported,
		HasReported: hasReported,
		Changes:     changes,
		Movement:    withPending,
		Expected:    expected,
	}
	if !hasReported || feed != FeedRead {
		return out
	}
	out.Explained = expected.Round().Equal(reported.Round()) ||
		Total(previous, posted).Round().Equal(reported.Round())
	return out
}

// ledgerMovement is how far the rows counted shifted the balance between two
// reads, and how many rows shifted it.
func ledgerMovement(before, after []Transaction, counts func(Posting) bool) (Money, int) {
	contribution := func(txn Transaction) Money {
		if counts(Posting{Txn: txn}) {
			return txn.Amount
		}
		return Zero
	}
	was := make(map[ID]Money, len(before))
	for _, txn := range before {
		was[txn.ID] = contribution(txn)
	}
	movement := Zero
	changes := 0
	note := func(delta Money) {
		if !delta.IsZero() {
			movement = movement.Add(delta)
			changes++
		}
	}
	for _, txn := range after {
		note(contribution(txn).Sub(was[txn.ID]))
		delete(was, txn.ID)
	}
	for _, gone := range was {
		note(gone.Neg())
	}
	return movement, changes
}

// Reason is the household's sentence for a held balance: what the feed
// reported, what it was compared with, and the figure kept.
func (e BalanceExplanation) Reason() string {
	reported := "SimpleFIN reported " + moneyText(e.Reported)
	if !e.HasReported {
		reported = "SimpleFIN reported no balance"
	}
	switch {
	case !e.HasReported || e.Feed == FeedRead:
		movement := moneyText(e.Movement)
		if e.Movement.IsPositive() {
			movement = "+" + movement
		}
		return fmt.Sprintf("%s; the last balance %s plus %s (%s) comes to %s. Keeping %s until you confirm the change.",
			reported, moneyText(e.Previous), plural(e.Changes, "new transaction"), movement,
			moneyText(e.Expected), moneyText(e.Expected))
	case e.Feed == FeedAbsent:
		return fmt.Sprintf("%s for an account whose feed carries no transactions, so nothing can explain the change from %s. Keeping %s until you confirm the change.",
			reported, moneyText(e.Expected), moneyText(e.Expected))
	default:
		return fmt.Sprintf("%s without this account's transactions, so nothing explains the change from %s. Keeping %s until you confirm the change.",
			reported, moneyText(e.Expected), moneyText(e.Expected))
	}
}
