package domain

import "sort"

// Possible duplicates: the same money recorded twice because two sources both
// wrote it. The usual cause is a Simplifi import followed by a SimpleFIN link,
// where the bank's rows overlap the imported history but read differently
// enough (another wording, a posting day or two off) that neither the sync
// floor nor the sync's own matching recognises them. A pair is only proposed;
// a person decides.

// DuplicateToleranceDays is how many days apart two copies of one charge may
// be dated. Simplifi dates a row by when it happened and the bank by when it
// posted, which is a day or two later; wider than this and equal amounts start
// to outnumber real repeats.
const DuplicateToleranceDays = 2

// DuplicateCandidate is two rows proposed as one charge recorded twice.
// First is the lower id, so a pair has one spelling however it was found.
type DuplicateCandidate struct {
	First, Second ID
	DaysApart     int
	// Keep is the row to keep if the pair is a duplicate; the other is the
	// copy to retire.
	Keep ID
}

// DuplicateKey is the one spelling of a pair of rows, lower id first.
func DuplicateKey(a, b ID) [2]ID {
	if b < a {
		a, b = b, a
	}
	return [2]ID{a, b}
}

// DuplicateOptions narrows FindDuplicateCandidates.
type DuplicateOptions struct {
	// CandidateIDs: a pair is proposed only if at least one row is a
	// candidate. Nil means every row is a candidate; a non-nil empty slice
	// proposes nothing.
	CandidateIDs []ID
	// Distinct holds the pairs a person has said are two real transactions,
	// by DuplicateKey. They are never proposed again.
	Distinct map[[2]ID]bool
	// ToleranceDays defaults to DuplicateToleranceDays when zero.
	ToleranceDays int
}

func (o DuplicateOptions) tolerance() int {
	if o.ToleranceDays == 0 {
		return DuplicateToleranceDays
	}
	return o.ToleranceDays
}

// CanBeDuplicated reports whether a row may be half of a duplicate pair: a
// live, non-zero row that is real money. Forecasts, bookkeeping rows
// (opening balances, adjustments) and deleted rows never are.
func (t Transaction) CanBeDuplicated() bool {
	return !t.IsDeleted && !t.IsEstimate && t.Source.IsCashFlow() && !t.Amount.IsZero()
}

// FindDuplicateCandidates proposes pairs of rows that look like one charge
// recorded by two sources: the same account, currency and amount to the cent,
// posted dates within the tolerance, and different sources. Two rows of one
// source are never paired, because each source dedupes itself; the wording is
// not compared, because it is what differs between sources.
//
// A row is in at most one pair. Pairs are taken nearest dates first, then
// oldest, then by id, so the result does not depend on input order. A pair in
// opts.Distinct is skipped before the rows are assigned, so ruling one out
// frees both rows to be paired with others.
func FindDuplicateCandidates(rows []Transaction, opts DuplicateOptions) []DuplicateCandidate {
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

	type bucket struct {
		account  ID
		currency string
		amount   string
	}
	buckets := map[bucket][]Transaction{}
	for _, row := range rows {
		if !row.CanBeDuplicated() {
			continue
		}
		key := bucket{row.AccountID, row.Currency, row.Amount.String()}
		buckets[key] = append(buckets[key], row)
	}

	type proposal struct {
		DuplicateCandidate
		older Date
	}
	tolerance := opts.tolerance()
	var proposals []proposal
	for _, group := range buckets {
		for i, a := range group {
			for _, b := range group[i+1:] {
				if a.Source == b.Source || a.ID == b.ID || !a.Amount.Equal(b.Amount) {
					continue
				}
				if candidate != nil && !candidate[a.ID] && !candidate[b.ID] {
					continue
				}
				gap := abs(DaysBetween(a.Date, b.Date))
				key := DuplicateKey(a.ID, b.ID)
				if gap > tolerance || opts.Distinct[key] {
					continue
				}
				older := a.Date
				if b.Date.Before(older) {
					older = b.Date
				}
				proposals = append(proposals, proposal{
					DuplicateCandidate{First: key[0], Second: key[1], DaysApart: gap, Keep: SuggestedDuplicateKeep(a, b)},
					older,
				})
			}
		}
	}

	sort.Slice(proposals, func(x, y int) bool {
		p, q := proposals[x], proposals[y]
		switch {
		case p.DaysApart != q.DaysApart:
			return p.DaysApart < q.DaysApart
		case p.older != q.older:
			return p.older.Before(q.older)
		case p.First != q.First:
			return p.First < q.First
		}
		return p.Second < q.Second
	})

	taken := map[ID]bool{}
	var out []DuplicateCandidate
	for _, p := range proposals {
		if taken[p.First] || taken[p.Second] {
			continue
		}
		taken[p.First], taken[p.Second] = true, true
		out = append(out, p.DuplicateCandidate)
	}
	return out
}

// SuggestedDuplicateKeep is the copy worth keeping: the one a person or an import
// wrote rather than the aggregator, since that is the one that may carry a
// category, a note or a renamed payee; else the earlier dated; else the lower
// id.
func SuggestedDuplicateKeep(a, b Transaction) ID {
	switch {
	case a.Source == SourceSync && b.Source != SourceSync:
		return b.ID
	case b.Source == SourceSync && a.Source != SourceSync:
		return a.ID
	case a.Date != b.Date:
		if a.Date.Before(b.Date) {
			return a.ID
		}
		return b.ID
	case a.ID < b.ID:
		return a.ID
	}
	return b.ID
}
