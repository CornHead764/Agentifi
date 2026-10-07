package domain

import (
	"sort"

	"github.com/shopspring/decimal"
)

// Reading the materialized balance history against the ledger as it stands.
// The daily pass re-derives only the last week, so each derived point carries
// the provider figure it was walked from (its anchor) and RederiveHistory
// re-walks it over today's rows on every read; otherwise a deleted duplicate
// would leave older points where they were.
//
// An observed point (a balance an import carried over) is read as written,
// and is the better anchor for the derived points before it: it is what the
// bank said on a nearby day, rather than today's figure walked back across
// years of a ledger that may not quite reconcile.

// BalanceAnchor is the provider figure a derived point was walked back from,
// and the last day that figure covers.
type BalanceAnchor struct {
	On      Date
	Balance Money
}

// AnchorFor is the anchor a derived point written now records: the provider
// figure dated at the later of `on` and the account's newest row, so every
// row the figure contains lies on or before the anchor's day. False for a
// manual account, which has no outside figure.
func AnchorFor(account Account, postings []Posting, on Date) (BalanceAnchor, bool) {
	if !account.HasProviderBalance {
		return BalanceAnchor{}, false
	}
	day := on
	for _, posting := range postings {
		if posted := posting.Txn.ReportingDate(DatePosted); CountsTowardBalance(posting) && posted.After(day) {
			day = posted
		}
	}
	return BalanceAnchor{
		On:      day,
		Balance: LedgerBalanceAsOf(account, postings, day, DatePosted),
	}, true
}

// DroppedReadingTolerance is how closely the readings either side of a run of
// zeros must agree (a tenth of the balance before them) for the zeros to be a
// dropped figure rather than a card paid off and used again.
var DroppedReadingTolerance = decimal.RequireFromString("0.10")

// DroppedReadings is the days of one account's observed history (oldest
// first) that record the feed dropping its figure, keyed by day. It is
// SuspectBalanceReset with hindsight: a run of zeros is dropped when the
// readings either side agree within DroppedReadingTolerance of a balance of at
// least SuspectResetThreshold. A run with no reading after it is kept, as is
// every zero on an account that accepts zeros. A dropped reading stays stored;
// its day falls to the reading before it.
func DroppedReadings(account Account, observed []BalancePoint) map[Date]bool {
	out := map[Date]bool{}
	if account.AcceptZeroBalance {
		return out
	}
	for start := 0; start < len(observed); {
		if !observed[start].Balance.IsZero() {
			start++
			continue
		}
		end := start
		for end < len(observed) && observed[end].Balance.IsZero() {
			end++
		}
		if start > 0 && end < len(observed) &&
			readingCameBack(observed[start-1].Balance, observed[end].Balance) {
			for _, point := range observed[start:end] {
				out[point.On] = true
			}
		}
		start = end
	}
	return out
}

func readingCameBack(before, after Money) bool {
	if before.Abs().LessThan(SuspectResetThreshold) {
		return false
	}
	return !after.Sub(before).Abs().GreaterThan(before.Abs().Scale(DroppedReadingTolerance))
}

// RederiveHistory is the materialized history as the ledger reads it now:
// dropped readings out, every derived point re-walked, input order kept.
//
//   - Manual account: BalanceAsOf over its ledger.
//   - Connected account: walked back from the nearest anchor on or after its
//     day (the next non-dropped observed point or its own recorded anchor,
//     whichever is earlier). With neither, it stands as written.
//   - Before the history start: zero, as BalanceAsOf is.
//
// With the ledger unchanged and no observation between a point and its
// anchor, the re-walk equals the figure written, so re-deriving never moves a
// point nothing under it moved. Points of accounts not in accounts are kept.
func RederiveHistory(
	accounts []Account, postings map[ID][]Posting, history []BalancePoint,
) []BalancePoint {
	byID := make(map[ID]Account, len(accounts))
	for _, account := range accounts {
		byID[account.ID] = account
	}
	positions := map[ID][]int{}
	for i, point := range history {
		positions[point.AccountID] = append(positions[point.AccountID], i)
	}

	out := make([]BalancePoint, len(history))
	copy(out, history)
	dropped := make([]bool, len(history))
	for id, indexes := range positions {
		account, known := byID[id]
		if !known {
			continue
		}
		sort.SliceStable(indexes, func(a, b int) bool {
			return out[indexes[a]].On.Before(out[indexes[b]].On)
		})
		rederiveAccount(account, postings[id], out, indexes, dropped)
	}

	kept := out[:0]
	for i, point := range out {
		if !dropped[i] {
			kept = append(kept, point)
		}
	}
	return kept
}

// rederiveAccount rewrites one account's points in place; indexes are its
// positions in points, oldest first.
func rederiveAccount(
	account Account, postings []Posting, points []BalancePoint, indexes []int, dropped []bool,
) {
	var observed []BalancePoint
	var first, last Date
	for _, i := range indexes {
		point := points[i]
		if point.Observed {
			observed = append(observed, point)
			continue
		}
		if first.IsZero() {
			first = point.On
		}
		last = point.On
	}
	drop := DroppedReadings(account, observed)
	if first.IsZero() {
		for _, i := range indexes {
			dropped[i] = drop[points[i].On]
		}
		return
	}

	if !account.HasProviderBalance {
		byDay := map[Date]Money{}
		for _, point := range BalanceHistory(account, postings, first, last) {
			byDay[point.On] = point.Balance
		}
		for _, i := range indexes {
			switch point := points[i]; {
			case point.Observed:
				dropped[i] = drop[point.On]
			default:
				points[i].Balance = byDay[point.On]
			}
		}
		return
	}

	start := HistoryStart(account, postings)
	ledger := newLedgerWalk(postings)
	var next BalanceAnchor
	hasNext := false
	for k := len(indexes) - 1; k >= 0; k-- {
		i := indexes[k]
		point := points[i]
		if point.Observed {
			if drop[point.On] {
				dropped[i] = true
				continue
			}
			next, hasNext = BalanceAnchor{On: point.On, Balance: point.Balance}, true
			continue
		}
		anchor, has := point.Anchor, point.HasAnchor
		if hasNext && (!has || next.On.Before(anchor.On)) {
			anchor, has = next, true
		}
		switch {
		case !start.IsZero() && point.On.Before(start):
			points[i].Balance = Zero
		case has:
			moved := ledger.through(anchor.On).Sub(ledger.through(point.On))
			points[i].Balance = anchor.Balance.Sub(moved).Round()
		}
	}
}

// ledgerWalk is an account's cumulative total of counted rows by posted date,
// so the rows between two days are one subtraction.
type ledgerWalk struct {
	days  []Date
	total []Money
}

func newLedgerWalk(postings []Posting) ledgerWalk {
	byDay := map[Date]Money{}
	for _, posting := range postings {
		if CountsTowardBalance(posting) {
			on := posting.Txn.ReportingDate(DatePosted)
			byDay[on] = byDay[on].Add(posting.NativeAmount())
		}
	}
	walk := ledgerWalk{days: make([]Date, 0, len(byDay))}
	for on := range byDay {
		walk.days = append(walk.days, on)
	}
	sort.Slice(walk.days, func(a, b int) bool { return walk.days[a].Before(walk.days[b]) })
	running := Zero
	walk.total = make([]Money, len(walk.days))
	for i, on := range walk.days {
		running = running.Add(byDay[on])
		walk.total[i] = running
	}
	return walk
}

// through is the total of every counted row posted on or before the day.
func (w ledgerWalk) through(on Date) Money {
	n := sort.Search(len(w.days), func(i int) bool { return w.days[i].After(on) })
	if n == 0 {
		return Zero
	}
	return w.total[n-1]
}
