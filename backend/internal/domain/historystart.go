package domain

// Where an account's history begins. A past balance walks back from the
// provider's figure (calculations.md §3) with no floor of its own, so an
// account linked today would read as holding today's balance for years. The
// floor (§4): before its history start an account holds nothing and has no
// point on any balance line.

// AutomaticHistoryStart is the earliest evidence the account held a balance:
//
//   - the posted date of its earliest row that counts toward the balance
//     (pending included; an asset's imported value history is rows too);
//   - OpeningBalanceOn, the day a stated opening balance was true;
//   - ObservedSince, the earliest balance an import carried over;
//   - AddedOn, the day the account was added here.
//
// Importing older rows moves it back with no other step. Zero means no floor.
func AutomaticHistoryStart(account Account, postings []Posting) Date {
	start := EarliestOf(account.AddedOn, account.OpeningBalanceOn, account.ObservedSince)
	for _, posting := range postings {
		if CountsTowardBalance(posting) {
			start = EarliestOf(start, posting.Txn.ReportingDate(DatePosted))
		}
	}
	return start
}

// HistoryStart is the household's HistoryStartsOn when set (earlier or later
// than the evidence), AutomaticHistoryStart otherwise.
func HistoryStart(account Account, postings []Posting) Date {
	if !account.HistoryStartsOn.IsZero() {
		return account.HistoryStartsOn
	}
	return AutomaticHistoryStart(account, postings)
}

// HasHistoryOn reports whether the account's history has begun by the end of
// the day.
func HasHistoryOn(account Account, postings []Posting, on Date) bool {
	start := HistoryStart(account, postings)
	return start.IsZero() || !on.Before(start)
}

// HistoryWithin drops every materialized point dated before its account's
// history start (one written before the start moved later),
// leaving that day to BalanceAsOf, which is zero there. An account missing
// from starts is kept whole.
func HistoryWithin(history []BalancePoint, starts map[ID]Date) []BalancePoint {
	out := make([]BalancePoint, 0, len(history))
	for _, point := range history {
		if start, known := starts[point.AccountID]; known && !start.IsZero() &&
			point.On.Before(start) {
			continue
		}
		out = append(out, point)
	}
	return out
}

// EarliestOf is the earliest of the given dates, zero dates skipped; zero
// when every date is.
func EarliestOf(dates ...Date) Date {
	var out Date
	for _, on := range dates {
		if on.IsZero() {
			continue
		}
		if out.IsZero() || on.Before(out) {
			out = on
		}
	}
	return out
}
