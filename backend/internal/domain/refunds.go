package domain

import "sort"

// Refunds: a credit that gives spending back. The sign cannot tell a paycheck
// from a grocery refund, and answering by sign inflates income and leaves the
// reversed spend standing: two wrong figures that add up. The category answers
// almost every row (LedgerKind); a RefundLink to the charge answers the rest.

// LedgerKind is which side of the ledger one amount filed this way counts on.
// The category decides, not the sign.
//
// An uncategorized amount is spending whichever way it points, as Simplifi
// lists Uncategorized among spending categories: an unfiled credit is usually
// an unplaced refund, and as income it would inflate the plan.
//
// A transfer returns its own kind, not CategoryExpense: callers drop transfers
// upstream, and this must not quietly put them back.
//
// Ask per part, never per row: a split row's category lives on its splits.
func LedgerKind(category Category, hasCategory bool) CategoryKind {
	if hasCategory {
		return category.Kind
	}
	return CategoryExpense
}

func (p Part) LedgerKind() CategoryKind {
	return LedgerKind(p.Category, p.HasCategory)
}

// IsRefund reports whether this part is money coming back out of spending
// rather than money earned.
func (p Part) IsRefund() bool {
	return p.Amount.IsPositive() && p.LedgerKind() == CategoryExpense
}

// IsEarnings reports whether this part belongs in the plan's Income bucket and
// on a report's income side.
func (p Part) IsEarnings() bool { return p.LedgerKind() == CategoryIncome }

// RefundLink is the user's statement that one credit gives back part or all of
// one earlier charge. Not one-to-one: partial refunds and several credits
// against one charge are allowed.
type RefundLink struct {
	RefundTxnID ID
	ChargeTxnID ID
	// ChargeCategoryID is carried because the charge is routinely outside the
	// loaded window. Empty when the charge is uncategorized or split.
	ChargeCategoryID ID
}

// ApplyRefundLinks refiles each linked credit under the category of the charge
// it gives back, so the money comes off the spend it reversed instead of
// reading as income.
//
// It rewrites the posting, not the stored row, so the register keeps showing
// what the user typed while calculations count it where it belongs.
//
// Left alone, because the answer would be a guess: a credit linked to charges
// in different categories, and a credit that already has splits. A link to an
// uncategorized charge refiles nothing.
func ApplyRefundLinks(postings []Posting, links []RefundLink, categories map[ID]Category) []Posting {
	if len(links) == 0 {
		return postings
	}

	refileTo := make(map[ID]ID, len(links))
	contested := map[ID]bool{}
	for _, link := range links {
		if link.ChargeCategoryID == "" {
			continue
		}
		if already, seen := refileTo[link.RefundTxnID]; seen && already != link.ChargeCategoryID {
			contested[link.RefundTxnID] = true
			continue
		}
		refileTo[link.RefundTxnID] = link.ChargeCategoryID
	}

	out := make([]Posting, len(postings))
	copy(out, postings)
	for i := range out {
		txn := out[i].Txn
		categoryID, linked := refileTo[txn.ID]
		if !linked || contested[txn.ID] || len(txn.Splits) > 0 {
			continue
		}
		category, known := categories[categoryID]
		if !known {
			continue
		}
		out[i].Txn.CategoryID = categoryID
		out[i].Category, out[i].HasCategory = category, true
	}
	return out
}

// CanBeARefund reports whether this row is a credit a user could sensibly point
// at a charge: a credit money moved for, not a transfer in either sense (which
// also covers a credit-card payment), and not filed under an income category.
func CanBeARefund(p Posting) bool {
	if !CountsTowardBalance(p) || !p.Txn.Amount.IsPositive() {
		return false
	}
	if p.IsTransfer() {
		return false
	}
	return !(p.HasCategory && p.Category.Kind == CategoryIncome)
}

// CanBeRefunded reports whether this row is a charge a credit could give back.
// A charge under an income category is allowed: a reversed paycheck is filed
// that way.
func CanBeRefunded(p Posting) bool {
	if !CountsTowardBalance(p) || !p.Txn.Amount.IsNegative() {
		return false
	}
	return !p.IsTransfer()
}

// RefundCandidateWindowDays is how far back a charge is offered for a credit
// when nobody has asked for one in particular: merchant return windows run to
// 90 days, and the credit lands days after the return.
const RefundCandidateWindowDays = 100

// RefundAge is how many days before a credit a charge was made, by the date
// each is reported on (trap 4), and whether a charge that old can be what the
// credit gives back: on or before the credit, and no more than withinDays
// before it. withinDays of zero means no bound, which a free search asks for.
func RefundAge(credit, charge Transaction, withinDays int) (int, bool) {
	days := DaysBetween(charge.ReportingDate(DateEffective), credit.ReportingDate(DateEffective))
	return days, days >= 0 && (withinDays <= 0 || days <= withinDays)
}

// SameRefundMerchant reports whether a charge is from the merchant a credit
// came from, by the bank's wording rather than the payee, since a rename must
// not move a charge (SharesMerchantWording, Transaction.MatchName).
func SameRefundMerchant(credit, charge Transaction) bool {
	return SharesMerchantWording(credit.MatchName(), charge.MatchName())
}

// OriginalPurchaseAge is RefundAge for the clear original purchase of a
// credit: a charge from the same merchant (SameRefundMerchant) for the same
// amount to the cent, within RefundCandidateWindowDays. False for any other
// row.
func OriginalPurchaseAge(credit, charge Transaction) (int, bool) {
	if !credit.Amount.IsPositive() || !charge.Amount.IsNegative() ||
		!charge.Amount.Abs().Equal(credit.Amount) || !SameRefundMerchant(credit, charge) {
		return 0, false
	}
	return RefundAge(credit, charge, RefundCandidateWindowDays)
}

// RankRefundCandidates orders the charges offered for one credit, likeliest
// first, and drops the ones no credit of this size could be giving back.
//
// Ranked by: the same amount to the cent; the same merchant
// (SameRefundMerchant); the same account; the nearest date before the credit.
//
// A charge smaller than the credit is dropped, even one the user searched
// for, since linking it would refile more money than it held. So is a charge
// out of RefundAge's reach.
func RankRefundCandidates(refund Posting, charges []Posting, withinDays int) []Posting {
	amount := refund.Txn.Amount.Abs()

	type scored struct {
		posting Posting
		rank    int
		age     int
	}
	var kept []scored
	for _, charge := range charges {
		if charge.Txn.ID == refund.Txn.ID || !CanBeRefunded(charge) {
			continue
		}
		age, reaches := RefundAge(refund.Txn, charge.Txn, withinDays)
		if !reaches {
			continue
		}
		magnitude := charge.Txn.Amount.Abs()
		if magnitude.LessThan(amount) {
			continue
		}
		rank := 0
		if magnitude.Equal(amount) {
			rank -= 8
		}
		if SameRefundMerchant(refund.Txn, charge.Txn) {
			rank -= 4
		}
		if charge.Account.ID == refund.Account.ID {
			rank -= 2
		}
		kept = append(kept, scored{posting: charge, rank: rank, age: age})
	}

	// Fully ordered, ties included, or the list reorders between keystrokes.
	sort.Slice(kept, func(i, j int) bool {
		a, b := kept[i], kept[j]
		if a.rank != b.rank {
			return a.rank < b.rank
		}
		if a.age != b.age {
			return a.age < b.age
		}
		return a.posting.Txn.ID < b.posting.Txn.ID
	})

	out := make([]Posting, 0, len(kept))
	for _, one := range kept {
		out = append(out, one.posting)
	}
	return out
}

// RefundTarget is a charge a merchant credit might give back: the bank row
// that paid, and the credits already linked to it.
type RefundTarget struct {
	Ref string
	On  Date
	// Amount is negative, as the bank row is.
	Amount Money
	// Refunded is the sum of the credits already linked to the charge, positive.
	Refunded Money
}

// ChooseRefundedCharge picks the charge a credit of the given amount gives
// back, among the rows that paid for the order the merchant says it belongs
// to. A charge can take a credit only while it has the room: the credit may be
// the whole charge or a part of it, and several partial credits may share one
// charge, but never more than the charge held. Of the charges with room, the
// one the credit exactly settles comes first, then the oldest.
func ChooseRefundedCharge(credit Money, charges []RefundTarget) (string, bool) {
	best, found := RefundTarget{}, false
	bestExact := false
	for _, charge := range charges {
		room := charge.Amount.Abs().Sub(charge.Refunded)
		if !credit.IsPositive() || !charge.Amount.IsNegative() || room.LessThan(credit) {
			continue
		}
		exact := room.Equal(credit)
		better := !found
		switch {
		case found && exact != bestExact:
			better = exact
		case found && charge.On != best.On:
			better = charge.On.Time().Before(best.On.Time())
		case found:
			better = charge.Ref < best.Ref
		}
		if better {
			best, bestExact, found = charge, exact, true
		}
	}
	return best.Ref, found
}

// How much of a charge its linked credits give back.
const (
	RefundStateNone    = ""
	RefundStatePartial = "partial"
	RefundStateFull    = "full"
)

// RefundState says whether the credits linked to a charge give back all of it
// or part. Credits worth the charge or more are a full refund; any less is a
// partial one, and none is no refund.
func RefundState(charge Money, credits []Money) string {
	if len(credits) == 0 {
		return RefundStateNone
	}
	given := Zero
	for _, credit := range credits {
		given = given.Add(credit.Abs())
	}
	if !given.IsPositive() {
		return RefundStateNone
	}
	if given.LessThan(charge.Abs()) {
		return RefundStatePartial
	}
	return RefundStateFull
}
