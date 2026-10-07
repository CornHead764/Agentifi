package domain

import "sort"

// Transfer pairing proposes which two rows are the halves of one movement
// between the household's own accounts. A debit in a credit card never pays
// (it is a purchase), and a run narrowed to candidates never joins two old
// rows to each other.

// DefaultDateToleranceDays is how many days apart the two legs of one transfer
// may post. Wider than this and same-amount coincidences start outnumbering
// real transfers.
const DefaultDateToleranceDays = 2

type TransferLeg struct {
	ID            ID
	AccountID     ID
	Amount        Money
	Currency      string
	On            Date
	AccountIsCard bool
}

// IsPaying is money out of a non-card account.
func (l TransferLeg) IsPaying() bool { return l.Amount.IsNegative() && !l.AccountIsCard }

func (l TransferLeg) IsReceiving() bool { return l.Amount.IsPositive() }

type PairProposal struct {
	PayingID    ID
	ReceivingID ID
	DaysApart   int
}

type PairOptions struct {
	// CandidateIDs: a pair is proposed only if at least one leg is a
	// candidate. Nil means every leg is a candidate; a non-nil empty slice
	// proposes nothing.
	CandidateIDs []ID
	// DateToleranceDays defaults to DefaultDateToleranceDays when zero.
	DateToleranceDays int
}

func (o PairOptions) Tolerance() int {
	if o.DateToleranceDays == 0 {
		return DefaultDateToleranceDays
	}
	return o.DateToleranceDays
}

// PlanPairs proposes which legs are the two halves of one transfer: a paying
// and a receiving leg of the same magnitude and currency, in different
// accounts, within the date tolerance. Paying legs are served oldest first
// (then by id), each taking the closest-dated free receiving leg, the older of
// two equally close, so the result does not depend on input order.
func PlanPairs(legs []TransferLeg, opts PairOptions) []PairProposal {
	if opts.CandidateIDs != nil && len(opts.CandidateIDs) == 0 {
		return nil
	}
	var candidate map[ID]bool
	if opts.CandidateIDs != nil {
		candidate = make(map[ID]bool, len(opts.CandidateIDs))
		for _, id := range opts.CandidateIDs {
			candidate[id] = true
		}
	}

	sorted := append([]TransferLeg(nil), legs...)
	sortLegs(sorted)

	type bucket struct{ amount, currency string }
	receiving := map[bucket][]int{}
	for i, leg := range sorted {
		if leg.IsReceiving() {
			key := bucket{amountKey(leg.Amount), leg.Currency}
			receiving[key] = append(receiving[key], i)
		}
	}

	tolerance := opts.Tolerance()
	taken := make([]bool, len(sorted))
	var out []PairProposal
	for i, paying := range sorted {
		if !paying.IsPaying() || taken[i] {
			continue
		}
		best, bestGap := -1, 0
		for _, j := range receiving[bucket{amountKey(paying.Amount), paying.Currency}] {
			into := sorted[j]
			if taken[j] || into.AccountID == paying.AccountID ||
				!into.Amount.Abs().Equal(paying.Amount.Abs()) {
				continue
			}
			if candidate != nil && !candidate[paying.ID] && !candidate[into.ID] {
				continue
			}
			gap := abs(DaysBetween(paying.On, into.On))
			if gap > tolerance {
				continue
			}
			if best < 0 || gap < bestGap {
				best, bestGap = j, gap
			}
		}
		if best < 0 {
			continue
		}
		taken[i], taken[best] = true, true
		out = append(out, PairProposal{PayingID: paying.ID, ReceivingID: sorted[best].ID, DaysApart: bestGap})
	}
	return out
}

// amountKey buckets legs by magnitude; the exact comparison is still made
// within the bucket.
func amountKey(m Money) string { return m.Abs().String() }

// sortLegs orders by date, then id; canonical UUID text sorts as its bytes do.
func sortLegs(legs []TransferLeg) {
	sort.SliceStable(legs, func(i, j int) bool {
		if legs[i].On != legs[j].On {
			return legs[i].On.Before(legs[j].On)
		}
		return legs[i].ID < legs[j].ID
	})
}
