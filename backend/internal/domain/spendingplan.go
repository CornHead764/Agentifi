package domain

// The spending plan, materialized per month rather than derived on read, so a
// closed month keeps the figures the user saw and a rule change does not
// rewrite last year. See calculations.md §5 and §13.

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/shopspring/decimal"
)

// BucketKey names one of the six buckets.
type BucketKey string

const (
	BucketIncome       BucketKey = "income"
	BucketBills        BucketKey = "bills"
	BucketPlannedSpend BucketKey = "planned_spend"
	BucketOtherSpend   BucketKey = "other_spend"
	BucketGoals        BucketKey = "goals"
	BucketRollover     BucketKey = "rollover"
)

// BucketOrder is the order the UI stacks the buckets in.
var BucketOrder = []BucketKey{
	BucketIncome,
	BucketBills,
	BucketPlannedSpend,
	BucketOtherSpend,
	BucketGoals,
	BucketRollover,
}

type SeriesKind string

const (
	SeriesIncome            SeriesKind = "income"
	SeriesBill              SeriesKind = "bill"
	SeriesSubscription      SeriesKind = "subscription"
	SeriesTransfer          SeriesKind = "transfer"
	SeriesCreditCardPayment SeriesKind = "credit_card_payment"
	// SeriesRefund is money expected back. It has no bucket of its own: like
	// a refund filed under a spending category, it nets against spending.
	SeriesRefund SeriesKind = "refund"
)

// IsBill reports the kinds the Bills bucket lists: bills, subscriptions, and
// the transfers and card payments that net to zero inside it.
func (k SeriesKind) IsBill() bool {
	switch k {
	case SeriesBill, SeriesSubscription, SeriesTransfer, SeriesCreditCardPayment:
		return true
	}
	return false
}

// NetsToZero reports the series listed inside Bills that subtotal to $0.00:
// their money was already counted when the purchase posted.
func (k SeriesKind) NetsToZero() bool {
	return k == SeriesTransfer || k == SeriesCreditCardPayment
}

type ProjectionType string

const (
	ProjectionRunRate        ProjectionType = "run_rate"
	ProjectionPriorMonth     ProjectionType = "prior_month"
	ProjectionAverageNMonths ProjectionType = "average_n_months"
)

type Bucket struct {
	Key              BucketKey
	CalculatedAmount Money
	// ContributingTxnIDs are the exact rows behind CalculatedAmount.
	ContributingTxnIDs []ID
	// ExcludedTxnIDs are rows the user dropped from this month only.
	ExcludedTxnIDs []ID
	// HasOverwrittenAmount distinguishes an override of zero from none.
	OverwrittenAmount    Money
	HasOverwrittenAmount bool
	// ResetOverwritten clears the override without losing it.
	ResetOverwritten bool
	// PostedAmount is the part of CalculatedAmount money has actually moved
	// for. It differs only in Income and Bills, by the occurrences still
	// expected (§5 rule 1), and is the only figure comparable against
	// Simplifi's bucket figures, which sum the ids they list.
	PostedAmount Money
}

// Effective is the figure the plan adds up: the override, unless reset.
func (b Bucket) Effective() Money {
	if !b.HasOverwrittenAmount || b.ResetOverwritten {
		return b.CalculatedAmount
	}
	return b.OverwrittenAmount
}

// SeriesSlot identifies one due-date slot of one series. The slot, not the
// series, is what a posted transaction claims, or a twice-monthly bill's two
// occurrences collapse into one.
type SeriesSlot struct {
	SeriesID ID
	DueOn    Date
}

// SeriesOccurrence is one due-date slot of a scheduled series.
type SeriesOccurrence struct {
	SeriesID ID
	DueOn    Date
	Kind     SeriesKind
	// ExpectedAmount is signed like a transaction; used only while the
	// occurrence is still expected.
	ExpectedAmount Money
}

func (o SeriesOccurrence) Slot() SeriesSlot { return SeriesSlot{o.SeriesID, o.DueOn} }

// SeriesLink is a posted transaction claiming one occurrence slot (Simplifi's
// stModelId + stDueOn).
type SeriesLink struct {
	TxnID    ID
	SeriesID ID
	// DueOn is zero when the source named the series but not the occurrence,
	// as Simplifi does for a bill matched to a series whose schedule was
	// created later.
	DueOn Date
	// Kind routes a link that fills no slot to its series' bucket rather than
	// Other Spend.
	Kind SeriesKind
}

func (l SeriesLink) Slot() SeriesSlot { return SeriesSlot{l.SeriesID, l.DueOn} }

// Projection is how this month's other spending is projected forward. The
// method is stored on the month row so the projection stays explainable.
type Projection struct {
	Type ProjectionType
	// Buffer is a positive flat add-on of expected extra spend.
	Buffer       Money
	WindowMonths int
	// PriorOtherSpending is prior months' other spending as positive
	// magnitudes, oldest first.
	PriorOtherSpending []Money
	StartOn            Date
	EndOn              Date
}

// MonthInputs is everything one month's calculation reads.
type MonthInputs struct {
	Month             Month
	Postings          []Posting
	Occurrences       []SeriesOccurrence
	SeriesLinks       []SeriesLink
	Envelopes         []Envelope
	GoalContributions []GoalContribution
	ExcludedTxnIDs    map[BucketKey][]ID
	// Categories resolves a split's own category, which its parent row does
	// not carry; without it a split filed under income counts as spending.
	Categories       map[ID]Category
	Overrides        map[BucketKey]Money
	ResetOverwritten map[BucketKey]bool
	IsClosedOut      bool

	Projection    Projection
	HasProjection bool
}

// AllExcludedTxnIDs is every row dropped for this month, across all buckets:
// a row dropped from one bucket must not reappear in another.
func (in MonthInputs) AllExcludedTxnIDs() map[ID]bool {
	excluded := map[ID]bool{}
	for _, ids := range in.ExcludedTxnIDs {
		for _, id := range ids {
			excluded[id] = true
		}
	}
	return excluded
}

// SpendPart is the flat, storable form of Part: the row, the split within it,
// the category it was filed under and its own share. A list of transaction
// ids cannot say what a split row contributed.
type SpendPart struct {
	TxnID ID
	// SplitID is empty for a row with no splits.
	SplitID    ID
	CategoryID ID
	// ParentID is the category's parent; empty when top level or absent.
	ParentID    ID
	HasCategory bool
	Amount      Money
}

// SpendPartOf resolves the category once, so a part in the Other Spend chart
// and under an envelope can never be filed under different categories.
func SpendPartOf(part Part) SpendPart {
	out := SpendPart{TxnID: part.Posting.Txn.ID, Amount: part.Amount}
	if part.HasSplit {
		out.SplitID = part.Split.ID
	}
	if part.HasCategory {
		out.CategoryID, out.ParentID = part.Category.ID, part.Category.ParentID
		out.HasCategory = true
	}
	return out
}

func (p SpendPart) Key() ID {
	if p.SplitID != "" {
		return p.TxnID + ":" + p.SplitID
	}
	return p.TxnID
}

type SpendingPlanMonth struct {
	Month     Month
	Buckets   map[BucketKey]Bucket
	Envelopes []EnvelopeStatus
	// ContestedEnvelopeTxnIDs maps a transaction id to the envelopes that
	// matched it but lost the tie-break.
	ContestedEnvelopeTxnIDs map[ID][]ID
	// OtherSpendParts is the Other Spend bucket part by part, with each split's
	// own category, so a chart built from it sums to the bucket. Nil for a
	// closed month.
	OtherSpendParts []SpendPart
	IsClosedOut     bool

	Projection    Projection
	HasProjection bool
}

func (m SpendingPlanMonth) Bucket(key BucketKey) Bucket { return m.Buckets[key] }

// LeftThisMonth is the headline figure: every bucket's effective amount.
func (m SpendingPlanMonth) LeftThisMonth() Money {
	amounts := make([]Money, 0, len(BucketOrder))
	for _, key := range BucketOrder {
		amounts = append(amounts, m.Buckets[key].Effective())
	}
	return Total(amounts...)
}

// OtherSpendToDate is actual other spending so far, positive; never a
// projection.
func (m SpendingPlanMonth) OtherSpendToDate() Money {
	return m.Buckets[BucketOtherSpend].Effective().Abs()
}

// PerDay is LeftThisMonth over the days that remain, today included; ok is
// false once the month is over rather than dividing by zero.
func (m SpendingPlanMonth) PerDay(today Date) (Money, bool) {
	return m.spreadOverDaysLeft(m.LeftThisMonth(), today)
}

// MonthResult is every bucket but the rollover.
func (m SpendingPlanMonth) MonthResult() Money {
	return m.LeftThisMonth().Sub(m.Buckets[BucketRollover].Effective())
}

func (m SpendingPlanMonth) MonthResultPerDay(today Date) (Money, bool) {
	return m.spreadOverDaysLeft(m.MonthResult(), today)
}

// DaysElapsed is the run rate's divisor: none before the month starts, all of
// them once it is over.
func (m SpendingPlanMonth) DaysElapsed(today Date) int {
	switch {
	case today.Before(m.Month.FirstDay()):
		return 0
	case today.After(m.Month.LastDay()):
		return m.Month.Days()
	}
	return DaysElapsedInMonth(today)
}

func (m SpendingPlanMonth) spreadOverDaysLeft(amount Money, today Date) (Money, bool) {
	if today.After(m.Month.LastDay()) {
		return Zero, false
	}
	days := DaysRemainingInMonth(today)
	if today.Before(m.Month.FirstDay()) {
		days = m.Month.Days()
	}
	perDay, ok := amount.DivInt(days)
	if !ok {
		return Zero, false
	}
	return perDay.Round(), true
}

// MatchOccurrences reports which transactions fulfilled which occurrence slot.
// Keyed on series and due date: on the series alone, the first posting
// fulfils both of a twice-monthly bill's occurrences and the second is counted
// again at its expected amount.
func MatchOccurrences(occurrences []SeriesOccurrence, links []SeriesLink) map[SeriesSlot][]ID {
	fulfilled := make(map[SeriesSlot][]ID, len(occurrences))
	for _, occurrence := range occurrences {
		if _, seen := fulfilled[occurrence.Slot()]; !seen {
			fulfilled[occurrence.Slot()] = nil
		}
	}
	for _, link := range links {
		slot := link.Slot()
		if _, wanted := fulfilled[slot]; wanted {
			fulfilled[slot] = append(fulfilled[slot], link.TxnID)
		}
	}
	return fulfilled
}

// ComputeMonth computes one month's buckets. Every eligible transaction lands
// in exactly one bucket, and the classification order is the rule: a row
// claimed by a series is a bill, a row funding a goal is a goal contribution,
// and only the rest can be envelope or other spend. A nil matcher claims
// nothing.
func ComputeMonth(inputs MonthInputs, rolloverIn Money, matches EnvelopeMatcher) SpendingPlanMonth {
	excluded := inputs.AllExcludedTxnIDs()
	// Money spent on a goal leaves the plan, like a hand-excluded row: it was
	// planned across the months it was saved in, and charging it to the month
	// it is booked in reads as an overspend. The rows still count in reports.
	for _, c := range inputs.GoalContributions {
		if c.Kind == GoalSpent {
			excluded[c.TxnID] = true
		}
	}

	// Postings are the whole chain's, so a slot due Jan 31 whose payment posts
	// Feb 2 is seen as filled in January. Only the month's own postings are
	// counted.
	allEligible := make([]Posting, 0, len(inputs.Postings))
	for _, p := range inputs.Postings {
		if CountsTowardSpendingPlan(p, excluded) {
			allEligible = append(allEligible, p)
		}
	}
	eligible := inMonth(inputs.Month, allEligible)

	// A link claims its slot only when the plan counts the transaction at all:
	// a forecast row or a payment on an ignored account must not cancel the
	// bill. The month's own exclusion list is deliberately not consulted — a
	// dropped posted bill stays fulfilled and contributes zero (§13).
	claimable := make(map[ID]bool, len(inputs.Postings))
	for _, p := range inputs.Postings {
		if CountsTowardSpendingPlan(p, nil) {
			claimable[p.Txn.ID] = true
		}
	}
	claims := make([]SeriesLink, 0, len(inputs.SeriesLinks))
	for _, link := range inputs.SeriesLinks {
		if claimable[link.TxnID] {
			claims = append(claims, link)
		}
	}

	fulfilled := MatchOccurrences(inputs.Occurrences, claims)
	// Any series-linked transaction is claimed by its series even when its
	// slot is not among this month's occurrences, or it resurfaces as Other
	// Spend in the month it posted.
	claimedBySeries := make(map[ID]bool, len(claims))
	linkFor := make(map[ID]SeriesLink, len(claims))
	for _, link := range claims {
		claimedBySeries[link.TxnID] = true
		linkFor[link.TxnID] = link
	}

	var (
		incomeAmounts []Money
		incomePosted  []Money
		incomeTxnIDs  []ID
		billsAmounts  []Money
		billsPosted   []Money
		billsTxnIDs   []ID
	)
	// An occurrence still expected is what the month has yet to pay. A filled
	// one contributes nothing here: its posting is counted in the month it
	// posted, below, since slot month and posting month often differ.
	for _, occurrence := range inputs.Occurrences {
		if len(fulfilled[occurrence.Slot()]) > 0 {
			continue
		}
		expected := occurrence.ExpectedAmount
		if occurrence.Kind.NetsToZero() {
			expected = Zero
		}
		if occurrence.Kind == SeriesIncome {
			incomeAmounts = append(incomeAmounts, expected)
			incomePosted = append(incomePosted, Zero)
			continue
		}
		billsAmounts = append(billsAmounts, expected)
		billsPosted = append(billsPosted, Zero)
	}

	// Every posting that belongs to a series counts under it in the month it
	// posted. That covers a posting filling a slot in another month, no slot,
	// or a schedule the recurrence cannot express, and cannot double count.
	// The kind falls back to the occurrence's so an unset link kind cannot let
	// a credit-card payment through as a bill.
	kindBySeries := make(map[ID]SeriesKind, len(inputs.Occurrences))
	for _, occurrence := range inputs.Occurrences {
		kindBySeries[occurrence.SeriesID] = occurrence.Kind
	}
	for _, p := range eligible {
		link, found := linkFor[p.Txn.ID]
		if !found {
			continue
		}
		kind := link.Kind
		if kind == "" {
			kind = kindBySeries[link.SeriesID]
		}
		amount := p.Amount()
		if kind.NetsToZero() {
			amount = Zero
		}
		if kind == SeriesIncome {
			incomeAmounts = append(incomeAmounts, amount)
			incomePosted = append(incomePosted, amount)
			incomeTxnIDs = append(incomeTxnIDs, p.Txn.ID)
			continue
		}
		billsAmounts = append(billsAmounts, amount)
		billsPosted = append(billsPosted, amount)
		billsTxnIDs = append(billsTxnIDs, p.Txn.ID)
	}

	// A contribution counts in the month it posted: the caller passes the
	// whole chain's contributions.
	goalTxnIDs := map[ID]bool{}
	var countedGoals []GoalContribution
	for _, c := range inputs.GoalContributions {
		// Spending against a goal already left the plan above, and the Goals
		// bucket reserves this month's savings, not savings leaving.
		if c.Kind == GoalSpent {
			continue
		}
		goalTxnIDs[c.TxnID] = true
		if !excluded[c.TxnID] && c.CountsInGoalsBucket(inputs.Month) {
			countedGoals = append(countedGoals, c)
		}
	}

	var unclaimed []Posting
	for _, p := range eligible {
		if !claimedBySeries[p.Txn.ID] && !goalTxnIDs[p.Txn.ID] {
			unclaimed = append(unclaimed, p)
		}
	}

	// An envelope sees its charges and the refunds against them, so a refund
	// nets inside its envelope (§13's departure from §5). A positive is a
	// refund only when not filed as income; an uncategorized one qualifies.
	// Parts, not rows: a split's halves can belong to different envelopes.
	var parts []Part
	for _, p := range unclaimed {
		parts = append(parts, PartsOf(p, inputs.Categories)...)
	}

	var envelopeCandidates []Part
	var spend []Part
	for _, part := range parts {
		switch {
		case part.IsEarnings():
			// Income, whichever way it points; no envelope spends it.
		case part.Amount.IsNegative():
			spend = append(spend, part)
			envelopeCandidates = append(envelopeCandidates, part)
		case part.IsRefund():
			envelopeCandidates = append(envelopeCandidates, part)
		}
	}

	assignment := AssignEnvelopes(inputs.Envelopes, envelopeCandidates, matches)
	statuses := EnvelopeStatusesFor(inputs.Envelopes, assignment)

	// Actuals only, refunds netting spending. Adding the projection here
	// would spend the same money twice in "left this month".
	var other []Part
	var otherTxnIDs []ID
	var otherParts []SpendPart
	seenOtherTxn := map[ID]bool{}
	addOther := func(part Part) {
		other = append(other, part)
		txnID := part.Posting.Txn.ID
		// One id per row, or a split row is drawn twice in the audit trail.
		if !seenOtherTxn[txnID] {
			seenOtherTxn[txnID] = true
			otherTxnIDs = append(otherTxnIDs, txnID)
		}
		otherParts = append(otherParts, SpendPartOf(part))
	}
	for _, p := range parts {
		// A part filed as income is income whichever way it points: a
		// clawed-back paycheck lowers Income rather than raising Other Spend.
		// A positive an envelope claimed is a refund against it, not income.
		if (!p.Amount.IsPositive() && !p.IsEarnings()) || assignment.Claimed(p.Key()) {
			continue
		}
		// Part.IsRefund is the same rule the reports engine files by: a
		// non-income positive, uncategorized included, nets against Other Spend
		// rather than inflating Income and Other Spend alike.
		if p.IsRefund() {
			addOther(p)
			continue
		}
		incomeAmounts = append(incomeAmounts, p.Amount)
		incomePosted = append(incomePosted, p.Amount)
		incomeTxnIDs = append(incomeTxnIDs, p.Posting.Txn.ID)
	}

	for _, p := range spend {
		if !assignment.Claimed(p.Key()) {
			addOther(p)
		}
	}

	// Planned spend reserves the targets, not actual spend, and ignores
	// envelope rollover, which already shows in the envelope's Available.
	var envelopeTargets []Money
	for _, envelope := range inputs.Envelopes {
		envelopeTargets = append(envelopeTargets, envelope.Target())
	}
	var envelopeTxnIDs []ID
	for _, status := range statuses {
		envelopeTxnIDs = append(envelopeTxnIDs, status.TxnIDs...)
	}

	// Money set aside leaves free-to-spend. Saved() rather than the raw
	// amount: the leg posted on the goal's own account is positive.
	var goalAmounts []Money
	var goalContribTxnIDs []ID
	for _, c := range countedGoals {
		goalAmounts = append(goalAmounts, c.Saved().Neg())
		goalContribTxnIDs = append(goalContribTxnIDs, c.TxnID)
	}

	buckets := map[BucketKey]Bucket{
		BucketIncome: newBucketPosted(BucketIncome,
			Total(incomeAmounts...), Total(incomePosted...), incomeTxnIDs, inputs),
		BucketBills: newBucketPosted(BucketBills,
			Total(billsAmounts...), Total(billsPosted...), billsTxnIDs, inputs),
		BucketPlannedSpend: newBucket(BucketPlannedSpend, Total(envelopeTargets...).Neg(), envelopeTxnIDs, inputs),
		BucketOtherSpend:   newBucket(BucketOtherSpend, Sum(other, Part.Signed), otherTxnIDs, inputs),
		BucketGoals:        newBucket(BucketGoals, Total(goalAmounts...), goalContribTxnIDs, inputs),
		BucketRollover:     newBucket(BucketRollover, rolloverIn, nil, inputs),
	}

	return SpendingPlanMonth{
		Month:                   inputs.Month,
		Buckets:                 buckets,
		Envelopes:               statuses,
		ContestedEnvelopeTxnIDs: assignment.ContestedByTxn(envelopeCandidates),
		OtherSpendParts:         otherParts,
		IsClosedOut:             inputs.IsClosedOut,
		Projection:              inputs.Projection,
		HasProjection:           inputs.HasProjection,
	}
}

// inMonth filters by effective date, not posted date: a card charge belongs to
// its statement's month.
func inMonth(month Month, postings []Posting) []Posting {
	var out []Posting
	for _, p := range postings {
		if month.Contains(p.Txn.ReportingDate(DateEffective)) {
			out = append(out, p)
		}
	}
	return out
}

func newBucket(key BucketKey, calculated Money, txnIDs []ID, inputs MonthInputs) Bucket {
	return newBucketPosted(key, calculated, calculated, txnIDs, inputs)
}

func newBucketPosted(key BucketKey, calculated, posted Money, txnIDs []ID, inputs MonthInputs) Bucket {
	dropped := append([]ID(nil), inputs.ExcludedTxnIDs[key]...)
	sort.Slice(dropped, func(i, j int) bool { return dropped[i] < dropped[j] })

	override, hasOverride := inputs.Overrides[key]
	return Bucket{
		Key:                  key,
		CalculatedAmount:     calculated.Round(),
		ContributingTxnIDs:   txnIDs,
		ExcludedTxnIDs:       dropped,
		OverwrittenAmount:    override,
		HasOverwrittenAmount: hasOverride,
		ResetOverwritten:     inputs.ResetOverwritten[key],
		PostedAmount:         posted.Round(),
	}
}

// RecalculateChain recomputes consecutive months, carrying rollover forward
// (month N's rollover is N−1's LeftThisMonth). A closed month stops the
// cascade: it is returned as stored and later months carry its stored figure,
// and a closed month with nothing stored is an error. The matcher must be the
// one ComputeMonth gets, or envelope spend falls into Other Spend while
// planned_spend still reserves it.
func RecalculateChain(inputs []MonthInputs, openingRollover Money, closed map[Month]SpendingPlanMonth, matches EnvelopeMatcher) ([]SpendingPlanMonth, error) {
	results := make([]SpendingPlanMonth, 0, len(inputs))
	carry := openingRollover

	for i, monthInputs := range inputs {
		if monthInputs.IsClosedOut {
			frozen, ok := closed[monthInputs.Month]
			if !ok {
				return nil, fmt.Errorf("spending plan: month %s is closed out but no stored plan was supplied", monthInputs.Month)
			}
			results = append(results, frozen)
			carry = frozen.LeftThisMonth()
			continue
		}
		if i > 0 && inputs[i-1].Month.Next() == monthInputs.Month {
			monthInputs.Envelopes = CarryRollovers(
				monthInputs.Envelopes, inputs[i-1].Envelopes, results[i-1].Envelopes)
		}
		computed := ComputeMonth(monthInputs, carry, matches)
		results = append(results, computed)
		carry = computed.LeftThisMonth()
	}

	return results, nil
}

func trailingWindow(prior []Money, months int) []Money {
	if months <= 0 || months >= len(prior) {
		return prior
	}
	return prior[len(prior)-months:]
}

// ProjectedOtherSpending is this month's expected other spending, positive.
// It never lands below what is already spent; Buffer is added on top.
func ProjectedOtherSpending(month SpendingPlanMonth, asOf Date) Money {
	toDate := month.OtherSpendToDate()
	if !month.HasProjection {
		return toDate
	}
	projection := month.Projection

	var base Money
	switch projection.Type {
	case ProjectionRunRate:
		base = runRate(month, toDate, asOf)
		// Before the month starts there is no rate; use the prior months'
		// average until it begins.
		if asOf.Before(month.Month.FirstDay()) {
			window := trailingWindow(projection.PriorOtherSpending, projection.WindowMonths)
			if mean, ok := Total(window...).DivInt(len(window)); ok {
				base = mean
			}
		}
	case ProjectionPriorMonth:
		prior := projection.PriorOtherSpending
		base = toDate
		if len(prior) > 0 {
			base = prior[len(prior)-1]
		}
	case ProjectionAverageNMonths:
		window := trailingWindow(projection.PriorOtherSpending, projection.WindowMonths)
		base = toDate
		if mean, ok := Total(window...).DivInt(len(window)); ok {
			base = mean
		}
	default:
		// An unknown type must not silently choose a method; fall back to the
		// run rate and log it.
		slog.Warn("unknown spending plan projection type; using run rate",
			"projection_type", string(projection.Type))
		base = runRate(month, toDate, asOf)
	}

	if base.LessThan(toDate) {
		base = toDate
	}
	return Total(base, projection.Buffer)
}

// runRate is spend so far per elapsed day, over the whole month; before the
// month starts it is what has been spent.
func runRate(month SpendingPlanMonth, toDate Money, asOf Date) Money {
	if asOf.Before(month.Month.FirstDay()) {
		return toDate
	}
	perDay, ok := toDate.DivInt(month.DaysElapsed(asOf))
	if !ok {
		return toDate
	}
	return perDay.Scale(decimal.NewFromInt(int64(month.Month.Days())))
}

// ProjectedLeft is LeftThisMonth after the other spending still expected.
func ProjectedLeft(month SpendingPlanMonth, asOf Date) Money {
	return month.LeftThisMonth().Sub(otherSpendStillToCome(month, asOf)).Round()
}

// ProjectedMonthResult is ProjectedLeft without the rollover.
func ProjectedMonthResult(month SpendingPlanMonth, asOf Date) Money {
	return month.MonthResult().Sub(otherSpendStillToCome(month, asOf)).Round()
}

func otherSpendStillToCome(month SpendingPlanMonth, asOf Date) Money {
	return ProjectedOtherSpending(month, asOf).Sub(month.OtherSpendToDate())
}
