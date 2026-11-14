package domain

import (
	"sort"
	"strings"
	"time"
)

// The review queue: what a person should decide, in one place. Items are
// decisions that are not one new row; rows are the new transactions
// themselves. calculations.md §15.

// ReviewKind is what an item asks a person to decide.
type ReviewKind string

const (
	ReviewSignInCode          ReviewKind = "sign_in_code"
	ReviewSignIn              ReviewKind = "sign_in"
	ReviewBankReconnect       ReviewKind = "bank_reconnect"
	ReviewBillPastDue         ReviewKind = "bill_past_due"
	ReviewStatementDue        ReviewKind = "statement_due"
	ReviewDuplicate           ReviewKind = "duplicate"
	ReviewOrphanTransfers     ReviewKind = "orphan_transfers"
	ReviewHeldBalance         ReviewKind = "held_balance"
	ReviewProposal            ReviewKind = "proposal"
	ReviewRecurringSuggestion ReviewKind = "recurring_suggestion"
	ReviewUnmatchedOrder      ReviewKind = "unmatched_order"
)

// ReviewKinds is every kind, in tier order.
var ReviewKinds = []ReviewKind{
	ReviewSignInCode, ReviewSignIn, ReviewBankReconnect,
	ReviewBillPastDue, ReviewStatementDue, ReviewDuplicate, ReviewOrphanTransfers, ReviewHeldBalance,
	ReviewProposal,
	ReviewRecurringSuggestion, ReviewUnmatchedOrder,
}

// ReviewTier is what waiting on an item costs, most first.
type ReviewTier int

const (
	ReviewActNow ReviewTier = iota + 1
	ReviewMoneyAtStake
	ReviewTidyUp
	ReviewIdeas
)

// Tier is the kind's tier. A kind this package does not know is an idea, so
// it never counts toward the badge.
func (k ReviewKind) Tier() ReviewTier {
	switch k {
	case ReviewSignInCode, ReviewSignIn, ReviewBankReconnect:
		return ReviewActNow
	case ReviewBillPastDue, ReviewStatementDue, ReviewDuplicate, ReviewOrphanTransfers, ReviewHeldBalance:
		return ReviewMoneyAtStake
	case ReviewProposal:
		return ReviewTidyUp
	default:
		return ReviewIdeas
	}
}

// ReviewItem is one decision in the item lane.
type ReviewItem struct {
	// Key is stable across reads: the kind and the source's id, such as
	// "duplicate:<pair id>". A snooze names it.
	Key  string
	Kind ReviewKind
	// DueAt is when the item stops waiting (a code's expiry, a bill's due
	// date); zero when it never does.
	DueAt time.Time
	// Amount is the money at stake, as the ledger signs it; zero for none.
	Amount Money
	// Since is when the item started waiting.
	Since time.Time
	// Count is how many rows a grouped item covers. It counts once.
	Count int
}

// ReviewSnooze hides an item until a moment.
type ReviewSnooze struct {
	Key   string
	Until time.Time
}

// BuildReviewQueue orders the open items and leaves out the snoozed ones.
// calculations.md §15, review_queue.
func BuildReviewQueue(items []ReviewItem, snoozes []ReviewSnooze, now time.Time) []ReviewItem {
	snoozed := map[string]bool{}
	for _, s := range snoozes {
		if s.Until.After(now) {
			snoozed[s.Key] = true
		}
	}
	out := make([]ReviewItem, 0, len(items))
	for _, item := range items {
		if !snoozed[item.Key] {
			out = append(out, item)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return reviewItemBefore(out[i], out[j]) })
	return out
}

func reviewItemBefore(a, b ReviewItem) bool {
	if ta, tb := a.Kind.Tier(), b.Kind.Tier(); ta != tb {
		return ta < tb
	}
	if a.DueAt.IsZero() != b.DueAt.IsZero() {
		return !a.DueAt.IsZero()
	}
	if !a.DueAt.Equal(b.DueAt) {
		return a.DueAt.Before(b.DueAt)
	}
	if c := a.Amount.Abs().Cmp(b.Amount.Abs()); c != 0 {
		return c > 0
	}
	if !a.Since.Equal(b.Since) {
		return a.Since.Before(b.Since)
	}
	return a.Key < b.Key
}

// ReviewReason is why a new row needs a look; empty when it looks right.
type ReviewReason string

const (
	ReviewNotRecognised     ReviewReason = "not_recognised"
	ReviewPossibleDuplicate ReviewReason = "possible_duplicate"
	ReviewNoCategory        ReviewReason = "no_category"
	ReviewGuess             ReviewReason = "guess"
	ReviewUnusualAmount     ReviewReason = "unusual_amount"
)

// LooksRight reports whether no reason holds, which is what "Approve day"
// takes.
func (r ReviewReason) LooksRight() bool { return r == "" }

// ReviewGuessBelow is the suggestion confidence under which a row's category
// is a guess.
const ReviewGuessBelow = 0.7

// ReviewRow is a new row with what the review rules read about it.
type ReviewRow struct {
	Txn Transaction
	// NotRecognised is an open "I don't recognise this" flag.
	NotRecognised bool
	// DuplicateLeg is a leg of an open possible-duplicate pair.
	DuplicateLeg bool
	// Undetermined is a row the category check could not place.
	Undetermined bool
	// HasSuggestion is a pending suggestion approving the row would apply.
	// Its confidence is 0..1; HasConfidence is false when it states none.
	HasSuggestion bool
	HasConfidence bool
	Confidence    float64
	// Amount is AmountUsualness for the row.
	Amount Usualness
}

// ReviewReasonOf is the first reason that holds for the row.
// calculations.md §15, review_reason.
func ReviewReasonOf(row ReviewRow) ReviewReason {
	switch {
	case row.NotRecognised:
		return ReviewNotRecognised
	case row.DuplicateLeg:
		return ReviewPossibleDuplicate
	case row.Undetermined, row.Txn.IsUncategorized() && !row.HasSuggestion:
		return ReviewNoCategory
	case row.HasSuggestion && (!row.HasConfidence || row.Confidence < ReviewGuessBelow):
		return ReviewGuess
	case row.Amount == AmountUnusual:
		return ReviewUnusualAmount
	default:
		return ""
	}
}

// Usualness is whether an amount is like the payee's usual ones.
type Usualness string

const (
	AmountUnknown Usualness = ""
	AmountUsual   Usualness = "usual"
	AmountUnusual Usualness = "unusual"
)

const (
	// UsualAmountHistory is how many earlier rows the history reads.
	UsualAmountHistory = 6
	// UsualAmountMinHistory is the fewest that say anything.
	UsualAmountMinHistory = 3
	// UnusualAmountFactor times the history's median size is unusual.
	UnusualAmountFactor = 2
)

// SeriesRange is the range of amounts a series matches on, when it has one
// with both bounds.
type SeriesRange struct {
	Low, High Money
	Set       bool
}

// AmountUsualness compares the row's size with its series' range, or else
// with the median of its statement name's recent reviewed rows.
// calculations.md §15, usual_amount.
func AmountUsualness(row Transaction, history []Transaction, series SeriesRange) Usualness {
	size := row.Amount.Abs()
	if series.Set {
		low, high := series.Low.Abs(), series.High.Abs()
		if high.LessThan(low) {
			low, high = high, low
		}
		if size.LessThan(low) || size.GreaterThan(high) {
			return AmountUnusual
		}
		return AmountUsual
	}

	name := FoldName(strings.TrimSpace(row.StatementName))
	var past []Transaction
	for _, h := range history {
		if h.ID == row.ID || !h.IsReviewed || h.Amount.IsZero() || h.Currency != row.Currency ||
			h.Amount.IsNegative() != row.Amount.IsNegative() || h.Date.After(row.Date) ||
			FoldName(strings.TrimSpace(h.StatementName)) != name {
			continue
		}
		past = append(past, h)
	}
	sort.Slice(past, func(i, j int) bool {
		if past[i].Date != past[j].Date {
			return past[i].Date.After(past[j].Date)
		}
		return past[i].ID > past[j].ID
	})
	if len(past) > UsualAmountHistory {
		past = past[:UsualAmountHistory]
	}
	if len(past) < UsualAmountMinHistory {
		return AmountUnknown
	}

	sizes := make([]Money, len(past))
	for i, h := range past {
		sizes[i] = h.Amount.Abs()
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i].LessThan(sizes[j]) })
	mid := len(sizes) / 2
	twiceMedian := sizes[mid].MulInt(2)
	if len(sizes)%2 == 0 {
		twiceMedian = sizes[mid-1].Add(sizes[mid])
	}
	if !size.MulInt(2).LessThan(twiceMedian.MulInt(UnusualAmountFactor)) {
		return AmountUnusual
	}
	return AmountUsual
}

// ReviewSummary is the queue's counts. calculations.md §15, review_counts.
type ReviewSummary struct {
	ActNow       int
	MoneyAtStake int
	TidyUp       int
	Ideas        int
	New          int
	NeedsALook   int
}

// Badge is what needs a person: every item but ideas, and the rows that need
// a look.
func (s ReviewSummary) Badge() int {
	return s.ActNow + s.MoneyAtStake + s.TidyUp + s.NeedsALook
}

// ReviewCounts counts the queue BuildReviewQueue returned and the row lane's
// reasons.
func ReviewCounts(items []ReviewItem, rows []ReviewReason) ReviewSummary {
	var s ReviewSummary
	for _, item := range items {
		switch item.Kind.Tier() {
		case ReviewActNow:
			s.ActNow++
		case ReviewMoneyAtStake:
			s.MoneyAtStake++
		case ReviewTidyUp:
			s.TidyUp++
		default:
			s.Ideas++
		}
	}
	s.New = len(rows)
	for _, reason := range rows {
		if !reason.LooksRight() {
			s.NeedsALook++
		}
	}
	return s
}
