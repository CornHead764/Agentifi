package domain

import (
	"fmt"
	"slices"
	"strings"

	"github.com/shopspring/decimal"
)

// Matching a posted transaction to the series it fulfills.
//
// Wording is learned: "Paycheck" shares no token with "ACME CORP DES:PAYROLL",
// so the first link is made by hand, and every already-linked charge's wording
// then counts as a wording of the series.
//
// The three outcomes differ mainly in whether the schedule pointer moves;
// getting that wrong either duplicates an occurrence or silently skips a
// payment still to come.

// SimilarityThreshold is the least TokenSimilarity at which two wordings count
// as the same counterparty: more than half the words of the longer one shared.
const SimilarityThreshold = 0.6

// Weights for ranking candidates that all clear the bar.
const (
	weightDescription = 0.5
	weightAmount      = 0.3
	weightDate        = 0.2
)

// autoPadding is how far an auto band widens beyond the amounts observed.
// Simplifi does not publish its figure; this one is ours.
var autoPadding = decimal.RequireFromString("0.10")

// MatchCriteria is one of Simplifi's four Match Criteria.
type MatchCriteria string

const (
	// CriteriaExact is the series amount to the cent. An unset tolerance reads
	// as this one, so an unconfigured series gets the strict band.
	CriteriaExact MatchCriteria = "exact"
	// CriteriaAny accepts any amount in the right direction.
	CriteriaAny   MatchCriteria = "any"
	CriteriaRange MatchCriteria = "range"
	// CriteriaAuto is a band widened from what this series has charged.
	CriteriaAuto MatchCriteria = "auto"
)

type AmountTolerance struct {
	Criteria MatchCriteria
	Low      Money
	High     Money
}

func ExactAmount() AmountTolerance { return AmountTolerance{Criteria: CriteriaExact} }

func AnyAmount() AmountTolerance { return AmountTolerance{Criteria: CriteriaAny} }

func BetweenAmounts(low, high Money) (AmountTolerance, error) {
	if low.GreaterThan(high) {
		return AmountTolerance{}, fmt.Errorf("matching: range tolerance is inverted: %s > %s", low, high)
	}
	return AmountTolerance{Criteria: CriteriaRange, Low: low, High: high}, nil
}

func AutoAmount() AmountTolerance { return AmountTolerance{Criteria: CriteriaAuto} }

// Bounds is the band this tolerance accepts; false for "any amount". Auto spans
// everything the series has charged, the estimate included, padded.
func (t AmountTolerance) Bounds(expected Money, observed []Money) (low, high Money, bounded bool) {
	switch t.Criteria {
	case CriteriaAny:
		return Zero, Zero, false
	case CriteriaRange:
		return t.Low, t.High, true
	case CriteriaAuto:
		low, high = expected, expected
		for _, seen := range observed {
			if seen.LessThan(low) {
				low = seen
			}
			if seen.GreaterThan(high) {
				high = seen
			}
		}
		padding := expected.Abs().Scale(autoPadding)
		return low.Sub(padding), high.Add(padding), true
	default:
		return expected, expected, true
	}
}

func (t AmountTolerance) Accepts(expected, actual Money, observed []Money) bool {
	low, high, bounded := t.Bounds(expected, observed)
	if !bounded {
		return true
	}
	return low.Cmp(actual) <= 0 && actual.Cmp(high) <= 0
}

// MatchContext is a series with everything matching needs to know about it.
type MatchContext struct {
	Series    Series
	Tolerance AmountTolerance
	// LearnedDescriptions is the wording of charges already linked to this
	// series.
	LearnedDescriptions []string
	ObservedAmounts     []Money
	// SettledOn is the scheduled slots a charge already records. A forecast
	// settles nothing: it holds the slot for projection but pays nothing.
	SettledOn []Date
	// Bills are the series' linked statements, read as OccurrenceAmount reads
	// them so a charge is weighed against the figure its slot shows.
	Bills []BillConnect
}

// Expected is what the occurrence in this scheduled slot is worth: the one
// figure the reminder shows and a charge is weighed against.
func (c MatchContext) Expected(occurrenceOn Date) Money {
	return OccurrenceAmount(c.Series, occurrenceOn, c.Bills)
}

// Settled reports whether a charge already records this occurrence, so a
// second charge cannot pay it too.
func (c MatchContext) Settled(occurrenceOn Date) bool {
	return slices.Contains(c.SettledOn, occurrenceOn)
}

// Texts is every wording that has stood for this series. DisplayName is
// deliberately absent: renaming a series must not change what it matches.
func (c MatchContext) Texts() []string {
	out := make([]string, 0, len(c.LearnedDescriptions)+1)
	out = append(out, c.Series.Description)
	return append(out, c.LearnedDescriptions...)
}

// MatchPlaceholder is a materialized occurrence, written ahead of the charge
// that pays it.
type MatchPlaceholder struct {
	ID       ID
	SeriesID ID
	DueOn    Date
	Amount   Money
}

// MatchOutcome is what the caller should do with a matched charge.
type MatchOutcome string

const (
	// OutcomeUpgradePlaceholder means a placeholder already stands for this
	// occurrence: upgrade it in place, keeping its id, and move the pointer on.
	OutcomeUpgradePlaceholder MatchOutcome = "upgrade_placeholder"
	// OutcomeStampSeries means no placeholder was generated yet: stamp the
	// series onto the charge and move the pointer on, so generation does not
	// later duplicate it.
	OutcomeStampSeries MatchOutcome = "stamp_series"
	// OutcomeBackfill is an occurrence older than the pointer: link it and
	// leave the pointer alone, or the payment it is still waiting for is
	// silently skipped.
	OutcomeBackfill MatchOutcome = "backfill"
)

type MatchDecision struct {
	Outcome       MatchOutcome
	SeriesID      ID
	ChargeID      ID
	OccurrenceOn  Date
	PlaceholderID ID
	// AdvancePointerTo is where the schedule pointer should land. Zero means
	// leave it where it is.
	AdvancePointerTo Date
	// AdoptAmount is what an upgraded placeholder should take on when what
	// posted is not what was estimated.
	AdoptAmount    Money
	HasAdoptAmount bool
}

// TokenSimilarity is the share of words two descriptions have in common, from
// 0 to 1: the distinct words both contain, over the distinct word count of
// whichever has more, so a one-word wording does not score 1 against every
// description containing it. Split on whitespace only: "DES:PAYROLL" is one word.
func TokenSimilarity(left, right string) float64 {
	a, b := tokenSet(left), tokenSet(right)
	if len(a) == 0 || len(b) == 0 {
		return 0
	}
	shared := 0
	for token := range a {
		if b[token] {
			shared++
		}
	}
	return float64(shared) / float64(max(len(a), len(b)))
}

func tokenSet(text string) map[string]bool {
	out := map[string]bool{}
	for _, token := range strings.Fields(strings.ToLower(text)) {
		out[token] = true
	}
	return out
}

// BestSimilarity is the highest similarity across every wording of the series
// and of the charge.
func BestSimilarity(texts, candidates []string) float64 {
	best := 0.0
	for _, text := range texts {
		for _, candidate := range candidates {
			if found := TokenSimilarity(text, candidate); found > best {
				best = found
			}
		}
	}
	return best
}

// ChargeTexts is the wordings of a charge that matching may read: the bank's
// and the rewritten payee, since the series may resemble either.
func ChargeTexts(txn Transaction) []string {
	out := make([]string, 0, 2)
	for _, text := range []string{txn.StatementName, txn.Payee} {
		if text != "" {
			out = append(out, text)
		}
	}
	return out
}

// MatchWindow is how many days before and after a scheduled occurrence a
// charge may post and still be that occurrence, chosen from the series'
// average period. Each window stays well inside one period, so a charge can
// never be close enough to be claimed by the neighbouring occurrence too.
func MatchWindow(recurrence Recurrence) (before, after int) {
	switch period := PeriodDays(recurrence); {
	case period <= 2:
		return 0, 0
	case period <= 9:
		return 2, 2
	case period <= 16:
		return 3, 4
	default:
		return 3, 5
	}
}

// sameSignedDirection reports money moving the same way. A refund is not the
// bill it reverses.
func sameSignedDirection(left, right Money) bool {
	if left.IsZero() || right.IsZero() {
		return left.Equal(right)
	}
	return left.IsPositive() == right.IsPositive()
}

// IsCandidate reports whether this charge could be the series' occurrence on
// the given date.
func IsCandidate(context MatchContext, txn Transaction, occurrenceOn Date) bool {
	series := context.Series
	if txn.IsDeleted {
		return false
	}
	if txn.AccountID != series.AccountID {
		return false
	}
	if txn.Currency != series.Currency {
		return false
	}
	expected := context.Expected(occurrenceOn)
	if !sameSignedDirection(txn.Amount, expected) {
		return false
	}
	before, after := MatchWindow(series.Recurrence)
	earliest := occurrenceOn.AddDays(-before)
	latest := occurrenceOn.AddDays(after)
	if txn.Date.Before(earliest) || txn.Date.After(latest) {
		return false
	}
	if !context.Tolerance.Accepts(expected, txn.Amount, context.ObservedAmounts) {
		return false
	}
	return BestSimilarity(context.Texts(), ChargeTexts(txn)) >= SimilarityThreshold
}

// amountCloseness is 1.0 on the slot's expected amount, falling to 0.0 at the
// edge of the band.
func amountCloseness(context MatchContext, actual Money, occurrenceOn Date) float64 {
	expected := context.Expected(occurrenceOn)
	low, high, bounded := context.Tolerance.Bounds(expected, context.ObservedAmounts)
	if !bounded {
		return 1.0
	}
	span := expected.Sub(low)
	if high.Sub(expected).GreaterThan(span) {
		span = high.Sub(expected)
	}
	if !span.IsPositive() {
		return 1.0
	}
	distance, ok := Ratio(actual.Sub(expected).Abs(), span)
	if !ok {
		return 1.0
	}
	return max(0.0, 1.0-distance.InexactFloat64())
}

func dateCloseness(actual, expected Date, before, after int) float64 {
	span := max(before, after)
	if span <= 0 {
		return 1.0
	}
	off := DaysBetween(expected, actual)
	if off < 0 {
		off = -off
	}
	return max(0.0, 1.0-float64(off)/float64(span))
}

func Score(context MatchContext, txn Transaction, occurrenceOn Date) float64 {
	before, after := MatchWindow(context.Series.Recurrence)
	similarity := BestSimilarity(context.Texts(), ChargeTexts(txn))
	return weightDescription*similarity +
		weightAmount*amountCloseness(context, txn.Amount, occurrenceOn) +
		weightDate*dateCloseness(txn.Date, occurrenceOn, before, after)
}

// MatchingOccurrence is the occurrence of this series that the charge fulfills,
// if any. Searches around the charge rather than only at the pointer, because
// a charge for a long-ignored occurrence is a back-fill, not a non-match.
func MatchingOccurrence(context MatchContext, txn Transaction) (Date, bool) {
	before, after := MatchWindow(context.Series.Recurrence)
	// Scheduled slots only: a bill or one-off override moves where an
	// occurrence is shown, not which occurrence a charge was.
	candidates := ScheduledDueDates(context.Series, txn.Date.AddDays(-after), txn.Date.AddDays(before))
	var best Date
	bestOff := 0
	for _, on := range candidates {
		if !IsCandidate(context, txn, on) {
			continue
		}
		off := DaysBetween(on, txn.Date)
		if off < 0 {
			off = -off
		}
		if best.IsZero() || off < bestOff || (off == bestOff && on.Before(best)) {
			best, bestOff = on, off
		}
	}
	return best, !best.IsZero()
}

// FulfillsPointer reports whether this is the occurrence the series is
// currently waiting on, or a later one; anything before is a back-fill.
// Compared on the occurrence, not the charge date, so an early charge still
// pays its occurrence; and against the scheduled pointer, not the displayed
// date, or an override would make the occurrence it moved read as a back-fill.
func FulfillsPointer(context MatchContext, occurrenceOn Date) bool {
	return occurrenceOn.NotBefore(context.Series.ScheduledDueOn())
}

// AdvancePointerPast is where the pointer lands once the occurrence is
// fulfilled. Floored at the current due date so an early charge still moves
// the pointer, or generation would write the paid occurrence again. False once
// the series has run out of occurrences: the caller deactivates it.
func AdvancePointerPast(context MatchContext, occurrenceOn Date) (Date, bool) {
	series := context.Series
	target := occurrenceOn
	if series.ScheduledDueOn().After(target) {
		target = series.ScheduledDueOn()
	}
	return NextOccurrenceAfter(series.Recurrence, series.StartOn, target, series.EndOn)
}

// Decide reports what to do with a posted charge, across every series it could
// belong to; false when nothing matches, the common case.
func Decide(txn Transaction, contexts []MatchContext, placeholders []MatchPlaceholder) (MatchDecision, bool) {
	var best MatchContext
	var bestOn Date
	bestScore := 0.0
	found := false
	for _, context := range contexts {
		if !context.Series.IsLive() {
			continue
		}
		occurrenceOn, ok := MatchingOccurrence(context, txn)
		if !ok || context.Settled(occurrenceOn) {
			continue
		}
		candidate := Score(context, txn, occurrenceOn)
		if !found || candidate > bestScore ||
			(candidate == bestScore && context.Series.ID > best.Series.ID) {
			best, bestOn, bestScore, found = context, occurrenceOn, candidate, true
		}
	}
	if !found {
		return MatchDecision{}, false
	}
	return DecideOccurrence(best, txn, bestOn, placeholders), true
}

// DecideOccurrence is what filing the charge under this occurrence does: the
// outcome, the placeholder it upgrades and where the pointer lands. A hand
// link calls it with the occurrence the user chose.
func DecideOccurrence(
	context MatchContext, txn Transaction, occurrenceOn Date, placeholders []MatchPlaceholder,
) MatchDecision {
	seriesID := context.Series.ID
	var placeholderID ID
	var placeholderAmount Money
	hasPlaceholder := false
	for _, row := range placeholders {
		if row.SeriesID == seriesID && row.DueOn == occurrenceOn {
			placeholderID, placeholderAmount, hasPlaceholder = row.ID, row.Amount, true
			break
		}
	}

	adopt, hasAdopt := adoptedAmount(placeholderAmount, hasPlaceholder, txn)
	decision := MatchDecision{
		SeriesID:       seriesID,
		ChargeID:       txn.ID,
		OccurrenceOn:   occurrenceOn,
		PlaceholderID:  placeholderID,
		AdoptAmount:    adopt,
		HasAdoptAmount: hasAdopt,
	}

	if !FulfillsPointer(context, occurrenceOn) {
		decision.Outcome = OutcomeBackfill
		return decision
	}

	decision.Outcome = OutcomeStampSeries
	if hasPlaceholder {
		decision.Outcome = OutcomeUpgradePlaceholder
	}
	if advance, ok := AdvancePointerPast(context, occurrenceOn); ok {
		decision.AdvancePointerTo = advance
	}
	return decision
}

// adoptedAmount is what an estimated placeholder should be rewritten to, if
// anything, once the real charge posts.
func adoptedAmount(placeholder Money, hasPlaceholder bool, txn Transaction) (Money, bool) {
	if !hasPlaceholder || placeholder.Equal(txn.Amount) {
		return Zero, false
	}
	return txn.Amount, true
}
