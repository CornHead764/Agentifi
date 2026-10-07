package domain

// Planned-spend envelopes (calculations.md §5). A part an envelope claims
// leaves Other Spend, and a part two envelopes claim belongs to the older one
// (Simplifi's creation order); the losers are recorded so a total that does
// not reconcile can say which envelope took the row.

import (
	"sort"
	"time"

	"github.com/shopspring/decimal"
)

type EnvelopeState string

const (
	EnvelopeNormal       EnvelopeState = "normal"
	EnvelopeWithRollover EnvelopeState = "with_rollover"
	EnvelopeOverspent    EnvelopeState = "overspent"
)

// FullyUsedPercent is what a fully consumed envelope reads, and the answer for
// a zero budget that has spend against it: 100% used, not a division by zero.
var FullyUsedPercent = decimal.NewFromInt(100)

// Envelope is one planned-spend item, as it stands in one month. Per month
// because closing a month must freeze what its envelopes said.
type Envelope struct {
	ID       ID
	Name     string
	FilterID ID

	TargetAmount Money
	// OverwrittenTargetAmount is a user override of the target for this month
	// only. HasOverwrittenTarget distinguishes an override of zero — a real
	// decision — from no override at all.
	OverwrittenTargetAmount Money
	HasOverwrittenTarget    bool

	// GroupID is what one recurring envelope keeps across months. Empty for the
	// month it was created in, whose own ID then names the group.
	GroupID ID

	// RolloverIn is carried from the prior month, and editable.
	RolloverIn Money
	// RolloverCarried says RolloverIn is still the engine's carry rather than a
	// figure the user set or released, so RecalculateChain keeps it following
	// the prior month's envelope while this month is open.
	RolloverCarried     bool
	AutoReleaseRollover bool
	// Recurring is false for a this-month-only envelope. Every construction
	// site must set it, or no envelope ever rolls forward.
	Recurring bool

	// CreatedAt is the tie-break when two envelopes match the same part, then
	// the id.
	CreatedAt time.Time
}

func (e Envelope) Group() ID {
	if e.GroupID != "" {
		return e.GroupID
	}
	return e.ID
}

func (e Envelope) Target() Money {
	if e.HasOverwrittenTarget {
		return e.OverwrittenTargetAmount
	}
	return e.TargetAmount
}

// EnvelopeStatus is one envelope's figures for one month. Spent is positive.
type EnvelopeStatus struct {
	EnvelopeID ID
	Name       string
	Target     Money
	RolloverIn Money
	Spent      Money
	TxnIDs     []ID
	// Parts is what Spent is made of. TxnIDs cannot say what a split row
	// contributed, so a list drawn from the ids alone stops summing to Spent.
	// Nil for a closed month, which is rebuilt from stored figures.
	Parts []SpendPart
}

func (s EnvelopeStatus) Budget() Money {
	return Total(s.Target, s.RolloverIn)
}

func (s EnvelopeStatus) Available() Money {
	return Total(s.Target, s.RolloverIn, s.Spent.Neg())
}

// PctUsed is the percent of budget consumed, unclamped as Simplifi shows it
// (250% used). Spend against a zero budget reads as fully used.
func (s EnvelopeStatus) PctUsed() Rate {
	budget := s.Budget()
	if !budget.IsPositive() {
		if s.Spent.IsPositive() {
			return FullyUsedPercent
		}
		return decimal.Zero
	}
	pct, ok := Percent(s.Spent, budget)
	if !ok {
		return decimal.Zero
	}
	return pct
}

// BarPct is what the progress bar fills to. The label overflows; the bar does not.
func (s EnvelopeStatus) BarPct() Rate {
	used := s.PctUsed()
	if used.GreaterThan(FullyUsedPercent) {
		return FullyUsedPercent
	}
	return used
}

// State is how the row paints. Overspending wins over carrying rollover.
func (s EnvelopeStatus) State() EnvelopeState {
	switch {
	case s.Available().IsNegative():
		return EnvelopeOverspent
	case s.RolloverIn.IsPositive():
		return EnvelopeWithRollover
	}
	return EnvelopeNormal
}

// EnvelopeMatcher reports whether an envelope's filter claims one part.
//
// The verdict is per part, not per row: a row split between two envelopes'
// categories must charge each its share. A part belongs to exactly one
// envelope, and Other Spend is the parts none took, so the month reconciles.
// calculations.md §5 rule 3, and the choice §13 records.
type EnvelopeMatcher func(Envelope, Part) bool

// EnvelopeAssignment records which envelope owns which part, and who else
// wanted it.
type EnvelopeAssignment struct {
	// EnvelopeByPart and Contested are keyed by Part.Key, which names a split
	// rather than its row.
	EnvelopeByPart  map[ID]ID
	PartsByEnvelope map[ID][]Part
	// Contested maps a part key to the envelopes that matched but lost, in
	// creation order.
	Contested map[ID][]ID
}

func (a EnvelopeAssignment) Claimed(partKey ID) bool {
	_, ok := a.EnvelopeByPart[partKey]
	return ok
}

// ContestedByTxn folds the contest record back up to transaction ids, which is
// what the month stores.
func (a EnvelopeAssignment) ContestedByTxn(parts []Part) map[ID][]ID {
	txnByPart := make(map[ID]ID, len(parts))
	for _, part := range parts {
		txnByPart[part.Key()] = part.Posting.Txn.ID
	}
	out := map[ID][]ID{}
	for partKey, envelopeIDs := range a.Contested {
		txnID, ok := txnByPart[partKey]
		if !ok {
			continue
		}
		seen := map[ID]bool{}
		for _, existing := range out[txnID] {
			seen[existing] = true
		}
		for _, envelopeID := range envelopeIDs {
			if !seen[envelopeID] {
				seen[envelopeID] = true
				out[txnID] = append(out[txnID], envelopeID)
			}
		}
	}
	return out
}

// EnvelopeResolutionOrder returns the envelopes in the order that decides a contested
// part. The input slice is left untouched: callers pass stored slices.
func EnvelopeResolutionOrder(envelopes []Envelope) []Envelope {
	ordered := make([]Envelope, len(envelopes))
	copy(ordered, envelopes)
	sort.SliceStable(ordered, func(i, j int) bool {
		if !ordered[i].CreatedAt.Equal(ordered[j].CreatedAt) {
			return ordered[i].CreatedAt.Before(ordered[j].CreatedAt)
		}
		return ordered[i].ID < ordered[j].ID
	})
	return ordered
}

// AssignEnvelopes gives every matched part to exactly one envelope, the first
// by creation order. Sharing a part between envelopes would stop Other Spend
// reconciling with the sum of the envelopes.
func AssignEnvelopes(envelopes []Envelope, parts []Part, matches EnvelopeMatcher) EnvelopeAssignment {
	ordered := EnvelopeResolutionOrder(envelopes)

	assignment := EnvelopeAssignment{
		EnvelopeByPart:  map[ID]ID{},
		PartsByEnvelope: map[ID][]Part{},
		Contested:       map[ID][]ID{},
	}
	for _, envelope := range ordered {
		assignment.PartsByEnvelope[envelope.ID] = []Part{}
	}
	if matches == nil {
		return assignment
	}

	for _, part := range parts {
		var claimants []ID
		for _, envelope := range ordered {
			if matches(envelope, part) {
				claimants = append(claimants, envelope.ID)
			}
		}
		if len(claimants) == 0 {
			continue
		}
		winner, losers := claimants[0], claimants[1:]
		key := part.Key()
		assignment.EnvelopeByPart[key] = winner
		assignment.PartsByEnvelope[winner] = append(assignment.PartsByEnvelope[winner], part)
		if len(losers) > 0 {
			assignment.Contested[key] = losers
		}
	}
	return assignment
}

// EnvelopeSpent is what an envelope has consumed, as a positive figure.
//
// Sums signed amounts and flips once, so a refund gives the money back.
// Summing |amount| as calculations.md §5 phrases it would make a return consume
// the envelope twice; §13 records the departure.
func EnvelopeSpent(parts []Part) Money {
	total := Zero
	for _, part := range parts {
		total = total.Add(part.Amount)
	}
	return total.Neg()
}

// EnvelopeStatusFor is one envelope's month, over the parts assigned to it.
//
// TxnIDs is deduplicated transaction ids (the schema's uuid[]). A row under
// both an envelope and Other Spend is a split row whose parts went to both,
// not a double count.
func EnvelopeStatusFor(envelope Envelope, parts []Part) EnvelopeStatus {
	txnIDs := make([]ID, 0, len(parts))
	charged := make([]SpendPart, 0, len(parts))
	seen := map[ID]bool{}
	for _, part := range parts {
		charged = append(charged, SpendPartOf(part))
		id := part.Posting.Txn.ID
		if seen[id] {
			continue
		}
		seen[id] = true
		txnIDs = append(txnIDs, id)
	}
	return EnvelopeStatus{
		EnvelopeID: envelope.ID,
		Name:       envelope.Name,
		Target:     envelope.Target().Round(),
		RolloverIn: envelope.RolloverIn.Round(),
		Spent:      EnvelopeSpent(parts),
		TxnIDs:     txnIDs,
		Parts:      charged,
	}
}

// EnvelopeStatusesFor is every envelope's month, in the order the resolution used.
func EnvelopeStatusesFor(envelopes []Envelope, assignment EnvelopeAssignment) []EnvelopeStatus {
	ordered := EnvelopeResolutionOrder(envelopes)
	statuses := make([]EnvelopeStatus, 0, len(ordered))
	for _, envelope := range ordered {
		statuses = append(statuses, EnvelopeStatusFor(envelope, assignment.PartsByEnvelope[envelope.ID]))
	}
	return statuses
}

// SetRollover sets the carried figure directly; it is the user's from then on,
// so the chain stops recomputing it.
func SetRollover(envelope Envelope, amount Money) Envelope {
	envelope.RolloverIn = amount.Round()
	envelope.RolloverCarried = false
	return envelope
}

// ReleaseRollover returns the envelope with nothing carried and the amount the
// caller must add back to this month's free-to-spend; dropping it is how
// released funds silently disappear.
func ReleaseRollover(envelope Envelope) (Envelope, Money) {
	released := envelope.RolloverIn.Round()
	envelope.RolloverIn = Zero
	envelope.RolloverCarried = false
	return envelope, released
}

// CarriedRollover is what next month carries in from this one: nothing under
// auto-release, otherwise the whole of Available, negative included.
func CarriedRollover(envelope Envelope, status EnvelopeStatus) Money {
	if envelope.AutoReleaseRollover {
		return Zero
	}
	return status.Available()
}

// RollForward is the envelope as it starts next month; ok is false for a
// one-month item. The caller gives it the new month's ID. The target override
// is a decision about one month and does not carry.
func RollForward(envelope Envelope, status EnvelopeStatus) (Envelope, bool) {
	if !envelope.Recurring {
		return Envelope{}, false
	}
	envelope.GroupID = envelope.Group()
	envelope.OverwrittenTargetAmount = Zero
	envelope.HasOverwrittenTarget = false
	envelope.RolloverIn = CarriedRollover(envelope, status)
	envelope.RolloverCarried = true
	return envelope, true
}

// CarryRollovers is a month's envelopes with every carried rollover recomputed
// from the month before: each envelope still marked RolloverCarried takes
// CarriedRollover of its group's envelope in prior, as prior's statuses left
// it. The input slice is left untouched. §5's rollover chain: carrying the
// figure once at materialization would freeze the prior month mid-spend.
func CarryRollovers(envelopes, prior []Envelope, priorStatuses []EnvelopeStatus) []Envelope {
	byGroup := make(map[ID]Envelope, len(prior))
	for _, envelope := range prior {
		byGroup[envelope.Group()] = envelope
	}
	statusByID := make(map[ID]EnvelopeStatus, len(priorStatuses))
	for _, status := range priorStatuses {
		statusByID[status.EnvelopeID] = status
	}
	out := make([]Envelope, len(envelopes))
	copy(out, envelopes)
	for i, envelope := range out {
		if !envelope.RolloverCarried {
			continue
		}
		previous, ok := byGroup[envelope.Group()]
		if !ok {
			continue
		}
		status, ok := statusByID[previous.ID]
		if !ok {
			continue
		}
		out[i].RolloverIn = CarriedRollover(previous, status).Round()
	}
	return out
}
