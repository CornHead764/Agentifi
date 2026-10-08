package service

// The spending plan engine: loads the rows, routes them into MonthInputs, calls
// domain.RecalculateChain and writes the answer back. It lives here rather than
// in internal/api so the background alert sweep can ask it too.
//
//   - Every figure comes from internal/domain; this file decides which rows to
//     read.
//   - The chain is computed whole, because a month's carry-in is the month
//     before it (see PlanView).
//   - Compute never persists; SaveResults does, and only a caller entitled to
//     write calls it, so a viewer cannot change what a closed month says.
//
// The spending_plan_months and envelopes tables have no module in
// internal/store, so their SQL is here.

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

type Plan struct {
	base
	// Now is nil for the real clock. It is read once per computation, as the as-of
	// date every projection is measured against.
	Now func() time.Time
}

func NewPlan(st *store.Store) *Plan { return &Plan{base: newBase(st)} }

func (p *Plan) now() time.Time {
	if p.Now == nil {
		return time.Now()
	}
	return p.Now()
}

// ErrMonthClosedOut is a mutation aimed at a closed month. Closing stops
// recalculation, so the write would be stored and never reflected.
var ErrMonthClosedOut = errors.New("service: the spending plan month is closed out")

// defaultProjectionWindow is how many prior months an average projection reads
// when the row does not say.
const defaultProjectionWindow = 3

// uuidArray converts domain ids back to keys, dropping anything unparseable,
// which is a row that no longer exists.
func uuidArray(ids []domain.ID) []uuid.UUID {
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if key, err := store.ParseID(id); err == nil {
			out = append(out, key)
		}
	}
	return out
}

// --- Storage families --------------------------------------------------------

// Seven column families in storage, six buckets on the wire. Bills,
// subscriptions and transfers are stored apart so the golden test can compare
// them with Simplifi's calculatedBillsAmount / calculatedSubscriptionsAmount /
// calculatedTransferAmount, and folded into one bucket for the UI.
type Family int

const (
	FamIncome Family = iota
	FamBills
	FamSubscriptions
	FamTransfer
	FamGoals
	FamPlannedSpending
	FamSpent
	FamilyCount
)

var FamilyNames = [FamilyCount]string{
	"income", "bills", "subscriptions", "transfer", "goals", "planned_spending", "spent",
}

// BillGroups maps the wire's `group` to the series kinds that land in it and
// the column Family that stores it.
var BillGroups = []struct {
	Name   string
	Family Family
	Kinds  []domain.SeriesKind
}{
	{"bill", FamBills, []domain.SeriesKind{domain.SeriesBill}},
	{"subscription", FamSubscriptions, []domain.SeriesKind{domain.SeriesSubscription}},
	{"transfer", FamTransfer, []domain.SeriesKind{domain.SeriesTransfer, domain.SeriesCreditCardPayment}},
}

// GroupForKind is the wire group and the storage Family a series' occurrences
// belong to. Income has its own bucket but expands into occurrence rows the same
// way, so it is named here too.
func GroupForKind(kind domain.SeriesKind) (string, Family) {
	if kind == domain.SeriesIncome {
		return "income", FamIncome
	}
	for _, group := range BillGroups {
		for _, candidate := range group.Kinds {
			if candidate == kind {
				return group.Name, group.Family
			}
		}
	}
	return "bill", FamBills
}

// BucketFamily is the column Family a wire bucket's override and exclusions are
// kept in. The bills bucket keeps both in the `bills` Family even though its
// figure spans three, since nobody has stated a rule for apportioning an
// override across them.
func BucketFamily(key domain.BucketKey) (Family, bool) {
	switch key {
	case domain.BucketIncome:
		return FamIncome, true
	case domain.BucketBills:
		return FamBills, true
	case domain.BucketPlannedSpend:
		return FamPlannedSpending, true
	case domain.BucketOtherSpend:
		return FamSpent, true
	case domain.BucketGoals:
		return FamGoals, true
	}
	return 0, false
}

// PlanOccurrence is a bill row plus the bucket the occurrence lands in and the
// category its series is filed under. Income expands the same way but is not
// part of the wire's `bills`.
type PlanOccurrence struct {
	// ID is the transaction id once something has posted against the slot, and
	// the composite `{series_id}:{due_on}` while the occurrence is still
	// expected. Both forms are accepted by the exclusion endpoints.
	ID          string
	Group       string
	SeriesID    uuid.UUID
	Name        string
	DueOn       domain.Date
	Amount      domain.Money
	IsFulfilled bool
	TxnIDs      []uuid.UUID
	IsExcluded  bool

	Bucket     domain.BucketKey
	CategoryID uuid.UUID
	Kind       domain.SeriesKind
}

// PlanView is one computed chain, kept whole so the target month's response
// and the rows written back come from the same pass.
type PlanView struct {
	AsOf    domain.Date
	Target  domain.Month
	Months  []domain.Month
	Rows    map[domain.Month]*PlanMonthRow
	Results map[domain.Month]domain.SpendingPlanMonth
	Bills   map[domain.Month][]PlanOccurrence
	// Envelopes is the stored rows per month, in the same order as the
	// statuses the domain returned for that month.
	Envelopes map[domain.Month][]EnvelopeRow
	// Postings, categories and filters turn an id in a bucket's audit trail back
	// into a readable row.
	Postings   map[domain.ID]domain.Posting
	Categories map[domain.ID]domain.Category
	Filters    map[domain.ID]domain.Filter
}

// Compute recalculates every month from the freeze point up to the target: the
// latest closed-out month at or before the target, whose stored figure the later
// months carry in, or else the space's earliest month.
func (p *Plan) Compute(ctx context.Context, spaceID store.SpaceID, target domain.Month) (PlanView, error) {
	asOf := domain.DateOf(p.now())

	stored, err := p.listMonths(ctx, spaceID)
	if err != nil {
		return PlanView{}, err
	}

	start := target
	for _, month := range SortedMonths(stored) {
		if month.After(target) {
			continue
		}
		if start.After(month) {
			start = month
		}
		if stored[month].IsClosedOut {
			start = month
		}
	}
	months := domain.MonthsBetween(start, target)

	// Months with nothing stored get an in-memory row so they still compute.
	rows := make(map[domain.Month]*PlanMonthRow, len(months))
	for _, month := range months {
		if row, ok := stored[month]; ok {
			rows[month] = row
			continue
		}
		rows[month] = NewPlanMonthRow(month)
	}

	seriesRows, err := p.activeSeries(ctx, spaceID)
	if err != nil {
		return PlanView{}, err
	}

	// The window reaches past the target by however late a payment may post and
	// still fill a slot inside the chain. Otherwise a January slot paid on
	// February 2 reads unpaid or paid depending on which month is open, and
	// SaveResults materializes whichever was computed last.
	postings, txnRows, err := LoadPostings(ctx, p.store, spaceID, store.TransactionQuery{
		From:     start.FirstDay(),
		To:       target.LastDay().AddDays(latePaymentDays(seriesRows)),
		DateMode: domain.DateEffective,
	})
	if err != nil {
		return PlanView{}, err
	}
	postingsByID := make(map[domain.ID]domain.Posting, len(postings))
	for _, posting := range postings {
		postingsByID[posting.Txn.ID] = posting
	}

	categories, err := p.store.ListCategories(ctx, spaceID, true)
	if err != nil {
		return PlanView{}, err
	}
	// For resolving a split's category: a split row's parent carries none.
	domainCategories := make(map[domain.ID]domain.Category, len(categories))
	for _, category := range categories {
		domainCategories[domain.ID(category.ID.String())] = store.DomainCategory(category)
	}

	contributions, err := p.goalContributions(ctx, spaceID, txnRows)
	if err != nil {
		return PlanView{}, err
	}

	envelopesByMonth := make(map[domain.Month][]EnvelopeRow, len(months))
	var filterIDs []uuid.UUID
	for _, month := range months {
		row := rows[month]
		if row.ID == uuid.Nil {
			continue
		}
		envelopes, err := p.ListEnvelopes(ctx, spaceID, row.ID)
		if err != nil {
			return PlanView{}, err
		}
		envelopesByMonth[month] = envelopes
		for _, envelope := range envelopes {
			filterIDs = append(filterIDs, envelope.FilterID)
		}
	}
	// A month with no stored row starts with the previous month's recurring
	// envelopes. A stored month keeps exactly its own: whether an imported envelope
	// is the same one as last month's is not known, and guessing could duplicate it.
	for i, month := range months {
		if i > 0 && rows[month].ID == uuid.Nil {
			envelopesByMonth[month] = rollForward(envelopesByMonth[months[i-1]], month)
		}
	}
	filters, facets, err := PrepareFilters(ctx, p.store, spaceID, filterIDs, txnRows)
	if err != nil {
		return PlanView{}, err
	}
	matcher := envelopeMatcher(filters, facets)

	// Prior other spending for the projection, oldest first. Stored figures
	// seed it; the chain overwrites whatever it recomputes below.
	prior := priorOtherSpending(stored, target)

	seriesKinds := make(map[uuid.UUID]domain.SeriesKind, len(seriesRows))
	for _, series := range seriesRows {
		seriesKinds[series.ID] = series.Kind
	}
	links := seriesLinks(txnRows, seriesKinds)

	// Loaded separately from the plan's ledger, which excludes deleted rows, because
	// a skip is written as a deleted row. The Bills screen and the alert sweep use
	// the same query, so they agree on which occurrences are still owed.
	slotRows, err := p.store.ListTransactions(ctx, spaceID, store.TransactionQuery{
		HoldsASlot: true, IncludeDeleted: true, IncludeEstimates: true,
	})
	if err != nil {
		return PlanView{}, err
	}
	skipped := domain.SkippedSlots(store.SlotHolders(slotRows))

	// The same loader the Bills screen and the alert sweep read, so the three
	// agree on what this cycle costs and when it is due.
	billConnect, err := NewBills(p.store).BillConnectFor(ctx, spaceID, nil)
	if err != nil {
		return PlanView{}, err
	}

	inputs := make([]domain.MonthInputs, 0, len(months))
	billsByMonth := make(map[domain.Month][]PlanOccurrence, len(months))
	closed := map[domain.Month]domain.SpendingPlanMonth{}

	occurrencesByMonth := make(map[domain.Month][]domain.SeriesOccurrence, len(months))
	for _, month := range months {
		occurrences, bills := monthOccurrences(
			month, seriesRows, postings, links, skipped, billConnect, rows[month])
		occurrencesByMonth[month] = occurrences
		billsByMonth[month] = bills
	}

	for _, month := range months {
		row := rows[month]
		occurrences, bills := occurrencesByMonth[month], billsByMonth[month]

		monthInputs := domain.MonthInputs{
			Month:             month,
			Postings:          postings,
			Occurrences:       occurrences,
			SeriesLinks:       links,
			Envelopes:         DomainEnvelopes(envelopesByMonth[month]),
			GoalContributions: contributions,
			ExcludedTxnIDs:    row.ExcludedByBucket(),
			Categories:        domainCategories,
			Overrides:         row.OverridesByBucket(bills),
			ResetOverwritten:  row.ResetByBucket(),
			IsClosedOut:       row.IsClosedOut,
			HasProjection:     true,
			Projection: domain.Projection{
				Type:               row.ProjectionType,
				Buffer:             row.ProjectionBuffer,
				WindowMonths:       row.ProjectionWindowMonths,
				PriorOtherSpending: prior[month],
				StartOn:            row.ProjectionStartOn,
				EndOn:              row.ProjectionEndOn,
			},
		}
		inputs = append(inputs, monthInputs)
		if row.IsClosedOut {
			closed[month] = row.FrozenMonth(envelopesByMonth[month])
		}
	}

	computed, err := domain.RecalculateChain(inputs, rows[start].Rollover, closed, matcher)
	if err != nil {
		return PlanView{}, err
	}

	results := make(map[domain.Month]domain.SpendingPlanMonth, len(computed))
	for _, month := range computed {
		results[month.Month] = month
	}
	return PlanView{
		AsOf:       asOf,
		Target:     target,
		Months:     months,
		Rows:       rows,
		Results:    results,
		Bills:      billsByMonth,
		Envelopes:  envelopesByMonth,
		Postings:   postingsByID,
		Categories: domainCategories,
		Filters:    filters,
	}, nil
}

// priorOtherSpending is each month's prior months' other spending, positive and
// oldest first, from what is already stored. The average and prior-month
// projections read it, as does a run-rate month that has not started yet.
func priorOtherSpending(stored map[domain.Month]*PlanMonthRow, target domain.Month) map[domain.Month][]domain.Money {
	ordered := SortedMonths(stored)
	out := map[domain.Month][]domain.Money{}
	for _, month := range ordered {
		if month.After(target) {
			continue
		}
		var before []domain.Money
		for _, earlier := range ordered {
			if !earlier.Before(month) {
				break
			}
			before = append(before, stored[earlier].Calc[FamSpent].Abs())
		}
		out[month] = before
	}
	if _, ok := out[target]; !ok {
		var before []domain.Money
		for _, earlier := range ordered {
			if earlier.Before(target) {
				before = append(before, stored[earlier].Calc[FamSpent].Abs())
			}
		}
		out[target] = before
	}
	return out
}

// latePaymentDays is how far past the chain's last month a payment can post and
// still fill a slot inside it: the widest "after" of any live series' match
// window, so this cannot disagree with how payments are linked to occurrences.
func latePaymentDays(seriesRows []PlanSeriesRow) int {
	widest := 0
	for _, series := range seriesRows {
		if _, after := domain.MatchWindow(series.DomainSeries().Recurrence); after > widest {
			widest = after
		}
	}
	return widest
}

// monthOccurrences expands every live series into the month's due-date slots
// and renders them as the Bills bucket's rows.
//
// An excluded occurrence is dropped from the inputs rather than filtered after:
// an unfulfilled one has no transaction, so the month's exclusion list (applied
// to postings) cannot reach it. Its row is still returned, flagged, so the UI can
// offer it back.
func monthOccurrences(
	month domain.Month,
	seriesRows []PlanSeriesRow,
	postings []domain.Posting,
	links []domain.SeriesLink,
	skipped map[domain.ID]map[domain.Date]bool,
	billConnect map[domain.ID][]domain.BillConnect,
	row *PlanMonthRow,
) ([]domain.SeriesOccurrence, []PlanOccurrence) {
	// A slot's exclusion is filed under the bucket it lands in, so income and
	// bills read different families.
	excluded := map[Family]map[uuid.UUID]bool{}
	for _, fam := range []Family{FamIncome, FamBills} {
		set := map[uuid.UUID]bool{}
		for _, id := range row.Excluded[fam] {
			set[id] = true
		}
		excluded[fam] = set
	}
	// Eligibility is the domain's, over the union of every bucket's
	// exclusions: a row dropped from one bucket must not reappear in another.
	dropped := row.AllExcluded()
	amountByTxn := map[domain.ID]domain.Money{}
	for _, posting := range postings {
		if domain.CountsTowardSpendingPlan(posting, dropped) {
			amountByTxn[posting.Txn.ID] = posting.Amount()
		}
	}

	var occurrences []domain.SeriesOccurrence
	var rows []PlanOccurrence
	for _, series := range seriesRows {
		bills := billConnect[domain.ID(series.ID.String())]
		for _, shown := range domain.OccurrenceSlots(
			series.DomainSeries(), month.FirstDay(), month.LastDay(), bills) {
			due := shown.DueOn
			slot := SlotExclusionID(series.ID, due)
			occurrence := series.occurrence(shown, bills)
			group, fam := GroupForKind(series.Kind)
			drop := excluded[fam]

			claimants := domain.MatchOccurrences(
				[]domain.SeriesOccurrence{occurrence}, links)[occurrence.Slot()]

			// The slot's identity is every transaction that claimed it, even an excluded
			// one, so the exclusion can be offered back. Only eligible postings contribute
			// the amount, in whichever month they posted (§5 rule 1).
			var posted []domain.Money
			var txnIDs []uuid.UUID
			for _, txnID := range claimants {
				key, err := store.ParseID(txnID)
				if err != nil {
					continue
				}
				txnIDs = append(txnIDs, key)
				if amount, ok := amountByTxn[txnID]; ok {
					posted = append(posted, amount)
				}
			}

			// An occurrence is either still expected or fulfilled, never both.
			// The posted rows win, because they are what actually happened.
			amount := occurrence.ExpectedAmount
			if len(claimants) > 0 {
				amount = domain.Total(posted...)
			}
			if series.Kind.NetsToZero() {
				amount = domain.Zero
			}

			id := fmt.Sprintf("%s:%s", series.ID, due)
			if len(txnIDs) > 0 {
				id = txnIDs[0].String()
			}
			isExcluded := drop[slot]
			for _, txnID := range txnIDs {
				if drop[txnID] {
					isExcluded = true
				}
			}

			bucket := domain.BucketBills
			if series.Kind == domain.SeriesIncome {
				bucket = domain.BucketIncome
			}
			// A skipped occurrence is one the household said is not happening. It goes
			// through the month's exclusion route: listed in the rows so the screen can
			// show it, left out of the figures.
			wasSkipped := skipped[domain.ID(series.ID.String())][due]
			rows = append(rows, PlanOccurrence{
				ID:          id,
				Group:       group,
				SeriesID:    series.ID,
				Name:        series.Label(),
				DueOn:       due,
				Amount:      amount.Round(),
				IsFulfilled: len(claimants) > 0,
				TxnIDs:      store.NonNil(txnIDs),
				IsExcluded:  isExcluded || wasSkipped,
				Bucket:      bucket,
				CategoryID:  series.CategoryID,
				Kind:        series.Kind,
			})
			if !drop[slot] && !wasSkipped {
				occurrences = append(occurrences, occurrence)
			}
		}
	}

	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].DueOn != rows[j].DueOn {
			return rows[i].DueOn.Before(rows[j].DueOn)
		}
		return rows[i].Name < rows[j].Name
	})
	return occurrences, rows
}

func seriesLinks(txnRows map[uuid.UUID]store.Transaction, kinds map[uuid.UUID]domain.SeriesKind) []domain.SeriesLink {
	links := make([]domain.SeriesLink, 0, len(txnRows))
	for _, row := range txnRows {
		if row.SeriesID == uuid.Nil {
			continue
		}
		// A projected row holds the slot on the Bills screen but fulfils nothing; the
		// amount stays the series'. The query already leaves estimates out, but a
		// claimant with no eligible amount would render the bill as zero.
		if row.EstimateStatus != "" {
			continue
		}
		// A row with no due date names its series but no occurrence; it is kept so
		// ComputeMonth can route it by Kind rather than into Other Spend.
		links = append(links, domain.SeriesLink{
			TxnID:    domain.ID(row.ID.String()),
			SeriesID: domain.ID(row.SeriesID.String()),
			DueOn:    row.SeriesDueOn,
			Kind:     kinds[row.SeriesID],
		})
	}
	sort.Slice(links, func(i, j int) bool { return links[i].TxnID < links[j].TxnID })
	return links
}

// envelopeMatcher evaluates an envelope's filter against a posting, through the
// same domain entry point the register uses. An envelope whose filter no
// longer exists matches nothing rather than failing the plan: it still has a
// target to reserve.
func envelopeMatcher(
	filters map[domain.ID]domain.Filter, facets map[domain.ID]domain.Facets,
) domain.EnvelopeMatcher {
	if len(filters) == 0 {
		return nil
	}
	// Per part, not per row: domain.MatchingParts, as the register and watchlist
	// use, so one matching split does not claim its siblings.
	return func(envelope domain.Envelope, part domain.Part) bool {
		filter, ok := filters[envelope.FilterID]
		if !ok {
			return false
		}
		posting := part.Posting
		for _, match := range domain.MatchingParts(
			filter, posting, facets[posting.Txn.ID], domain.DateEffective) {
			if domain.NewPart(posting, match, nil).Key() == part.Key() {
				return true
			}
		}
		return false
	}
}

// slotNamespace derives the exclusion id of an unfulfilled occurrence. The
// exclusion columns are uuid[] and a slot has no transaction, so series plus due
// date is hashed into a stable uuid.
var slotNamespace = uuid.MustParse("6f9619ff-8b86-d011-b42d-00c04fc964ff")

func SlotExclusionID(seriesID uuid.UUID, dueOn domain.Date) uuid.UUID {
	return uuid.NewSHA1(slotNamespace, []byte(seriesID.String()+":"+dueOn.String()))
}

// OpenMonth reads the month's stored row, materializing it if the space has
// never stored that month, and refuses a closed-out one.
//
// Materializing goes through the chain, never a bare insert, so a new month
// starts with the previous month's recurring envelopes.
func (p *Plan) OpenMonth(ctx context.Context, spaceID store.SpaceID, month domain.Month) (*PlanMonthRow, error) {
	row, err := p.readMonth(ctx, spaceID, month)
	if errors.Is(err, store.ErrNotFound) {
		view, computeErr := p.Compute(ctx, spaceID, month)
		if computeErr != nil {
			return nil, computeErr
		}
		if err := p.SaveResults(ctx, spaceID, view); err != nil {
			return nil, err
		}
		row, err = p.readMonth(ctx, spaceID, month)
	}
	if err != nil {
		return nil, err
	}
	if row.IsClosedOut {
		return nil, fmt.Errorf("%w: %s", ErrMonthClosedOut, month)
	}
	return row, nil
}

func SortedMonths(rows map[domain.Month]*PlanMonthRow) []domain.Month {
	months := make([]domain.Month, 0, len(rows))
	for month := range rows {
		months = append(months, month)
	}
	sort.Slice(months, func(i, j int) bool { return months[i].Before(months[j]) })
	return months
}

// --- Rows --------------------------------------------------------------------

// PlanMonthRow is the spending_plan_months row, in the fields this resource
// reads and writes.
type PlanMonthRow struct {
	ID       uuid.UUID
	Month    domain.Month
	Rollover domain.Money

	Calc     [FamilyCount]domain.Money
	TxnIDs   [FamilyCount][]uuid.UUID
	Excluded [FamilyCount][]uuid.UUID
	Over     [FamilyCount]domain.Money
	HasOver  [FamilyCount]bool
	Reset    [FamilyCount]bool

	SetAside     domain.Money
	TotalToSpend domain.Money
	LeftToSpend  domain.Money

	ProjectedOtherSpending domain.Money
	ProjectionType         domain.ProjectionType
	ProjectionStartOn      domain.Date
	ProjectionEndOn        domain.Date
	ProjectionBuffer       domain.Money
	ProjectionWindowMonths int

	IsClosedOut        bool
	ClosedOutAt        *time.Time
	ShowClosedOut      bool
	NewMonthViewed     bool
	HideExcludedFromUI bool
}

func NewPlanMonthRow(month domain.Month) *PlanMonthRow {
	return &PlanMonthRow{
		Month:                  month,
		ProjectionType:         domain.ProjectionRunRate,
		ProjectionWindowMonths: defaultProjectionWindow,
	}
}

func (p *PlanMonthRow) AllExcluded() map[domain.ID]bool {
	out := map[domain.ID]bool{}
	for fam := Family(0); fam < FamilyCount; fam++ {
		for _, id := range p.Excluded[fam] {
			out[domain.ID(id.String())] = true
		}
	}
	return out
}

func (p *PlanMonthRow) ExcludedByBucket() map[domain.BucketKey][]domain.ID {
	out := map[domain.BucketKey][]domain.ID{}
	for _, key := range domain.BucketOrder {
		fam, ok := BucketFamily(key)
		if !ok {
			continue
		}
		ids := make([]domain.ID, 0, len(p.Excluded[fam]))
		for _, id := range p.Excluded[fam] {
			ids = append(ids, domain.ID(id.String()))
		}
		out[key] = ids
	}
	return out
}

// OverridesByBucket folds the three bills families into the wire bucket's one
// override: an override on any family wins for that family and the others keep
// their computed subtotal (Bucket.Effective per family, summed). Ignoring the
// subscriptions and transfer columns would drop an imported override.
func (p *PlanMonthRow) OverridesByBucket(bills []PlanOccurrence) map[domain.BucketKey]domain.Money {
	out := map[domain.BucketKey]domain.Money{}
	for _, key := range domain.BucketOrder {
		fam, ok := BucketFamily(key)
		if !ok || key == domain.BucketBills {
			continue
		}
		if p.HasOver[fam] {
			out[key] = p.Over[fam]
		}
	}

	anyOverride := false
	var parts []domain.Money
	for _, group := range BillGroups {
		var amounts []domain.Money
		for _, bill := range bills {
			if bill.Group == group.Name && !bill.IsExcluded {
				amounts = append(amounts, bill.Amount)
			}
		}
		bucket := domain.Bucket{
			CalculatedAmount:     domain.Total(amounts...),
			OverwrittenAmount:    p.Over[group.Family],
			HasOverwrittenAmount: p.HasOver[group.Family],
			ResetOverwritten:     p.Reset[group.Family],
		}
		if bucket.HasOverwrittenAmount && !bucket.ResetOverwritten {
			anyOverride = true
		}
		parts = append(parts, bucket.Effective())
	}
	if anyOverride {
		out[domain.BucketBills] = domain.Total(parts...)
	}
	return out
}

func (p *PlanMonthRow) ResetByBucket() map[domain.BucketKey]bool {
	out := map[domain.BucketKey]bool{}
	for _, key := range domain.BucketOrder {
		if fam, ok := BucketFamily(key); ok {
			out[key] = p.Reset[fam]
		}
	}
	return out
}

// FrozenMonth rebuilds a closed month from what it closed with. RecalculateChain
// returns it untouched and carries its figure forward, so nothing here
// recomputes. The bills bucket is the sum of its three stored families.
func (p *PlanMonthRow) FrozenMonth(envelopes []EnvelopeRow) domain.SpendingPlanMonth {
	buckets := map[domain.BucketKey]domain.Bucket{}
	for _, key := range domain.BucketOrder {
		fam, ok := BucketFamily(key)
		if !ok {
			buckets[key] = domain.Bucket{Key: key, CalculatedAmount: p.Rollover, PostedAmount: p.Rollover}
			continue
		}
		calculated := p.Calc[fam]
		txnIDs := p.TxnIDs[fam]
		if key == domain.BucketBills {
			calculated = domain.Total(p.Calc[FamBills], p.Calc[FamSubscriptions], p.Calc[FamTransfer])
			txnIDs = append(append(append([]uuid.UUID{},
				p.TxnIDs[FamBills]...), p.TxnIDs[FamSubscriptions]...), p.TxnIDs[FamTransfer]...)
		}
		ids := make([]domain.ID, 0, len(txnIDs))
		for _, id := range txnIDs {
			ids = append(ids, domain.ID(id.String()))
		}
		buckets[key] = domain.Bucket{
			Key:              key,
			CalculatedAmount: calculated,
			// A closed month's stored figure is the sum of the ids it closed
			// with, so every part of it is posted by construction.
			PostedAmount:         calculated,
			ContributingTxnIDs:   ids,
			OverwrittenAmount:    p.Over[fam],
			HasOverwrittenAmount: p.HasOver[fam],
			ResetOverwritten:     p.Reset[fam],
		}
	}

	statuses := make([]domain.EnvelopeStatus, 0, len(envelopes))
	for _, envelope := range envelopes {
		// Parts stays nil: the month froze whole rows, so the list under it is drawn
		// from whole rows too rather than re-split now.
		txnIDs := make([]domain.ID, 0, len(envelope.TxnIDs))
		for _, id := range envelope.TxnIDs {
			txnIDs = append(txnIDs, domain.ID(id.String()))
		}
		statuses = append(statuses, domain.EnvelopeStatus{
			EnvelopeID: domain.ID(envelope.ID.String()),
			Name:       envelope.Name,
			Target:     envelope.DomainEnvelope().Target(),
			RolloverIn: envelope.Rollover,
			Spent:      envelope.CalculatedSpent,
			TxnIDs:     txnIDs,
		})
	}

	return domain.SpendingPlanMonth{
		Month:                   p.Month,
		Buckets:                 buckets,
		Envelopes:               statuses,
		ContestedEnvelopeTxnIDs: map[domain.ID][]domain.ID{},
		IsClosedOut:             true,
		HasProjection:           true,
		Projection: domain.Projection{
			Type:         p.ProjectionType,
			Buffer:       p.ProjectionBuffer,
			WindowMonths: p.ProjectionWindowMonths,
			StartOn:      p.ProjectionStartOn,
			EndOn:        p.ProjectionEndOn,
		},
	}
}

type EnvelopeRow struct {
	ID       uuid.UUID
	MonthID  uuid.UUID
	FilterID uuid.UUID
	Name     string

	TargetAmount         domain.Money
	OverwrittenTarget    domain.Money
	HasOverwrittenTarget bool
	CalculatedSpent      domain.Money
	Rollover             domain.Money
	RolloverCarried      bool
	AutoReleaseRollover  bool
	Recurring            bool
	// GroupID is recurring_group_id, uuid.Nil when the column is NULL.
	GroupID        uuid.UUID
	TxnIDs         []uuid.UUID
	ExcludedTxnIDs []uuid.UUID
	CreatedAt      time.Time
}

func (e EnvelopeRow) DomainEnvelope() domain.Envelope {
	envelope := domain.Envelope{
		ID:                      domain.ID(e.ID.String()),
		Name:                    e.Name,
		FilterID:                domain.ID(e.FilterID.String()),
		TargetAmount:            e.TargetAmount,
		OverwrittenTargetAmount: e.OverwrittenTarget,
		HasOverwrittenTarget:    e.HasOverwrittenTarget,
		RolloverIn:              e.Rollover,
		RolloverCarried:         e.RolloverCarried,
		AutoReleaseRollover:     e.AutoReleaseRollover,
		Recurring:               e.Recurring,
		CreatedAt:               e.CreatedAt,
	}
	if e.GroupID != uuid.Nil {
		envelope.GroupID = domain.ID(e.GroupID.String())
	}
	return envelope
}

func DomainEnvelopes(rows []EnvelopeRow) []domain.Envelope {
	out := make([]domain.Envelope, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.DomainEnvelope())
	}
	return out
}

// fromSeriesRow narrows the shared row into the domain-typed one this file
// works in. SeriesRow keeps the enum columns as strings for the matcher;
// domain.Recurrence wants the typed forms, which ToRecurrence alone builds.
func fromSeriesRow(row SeriesRow) PlanSeriesRow {
	return PlanSeriesRow{
		ID:                row.ID,
		AccountID:         row.AccountID,
		CategoryID:        row.CategoryID,
		Kind:              domain.SeriesKind(row.Kind),
		Description:       row.Description,
		DisplayName:       row.DisplayName,
		Amount:            row.Amount,
		Currency:          row.Currency,
		Recurrence:        ToRecurrence(row),
		StartOn:           row.StartOn,
		EndOn:             row.EndOn,
		NextDueOn:         row.NextDueOn,
		OverrideNextDueOn: row.OverrideNextDueOn,
		OverrideNextAmt:   row.OverrideNextAmount,
		HasOverrideAmt:    row.HasOverrideNextAmount,
		AutoAdjustDueOn:   row.AutoAdjustDueOn,
		IsActive:          row.IsActive,
		IsDeleted:         row.IsDeleted,
	}
}

type PlanSeriesRow struct {
	ID          uuid.UUID
	AccountID   uuid.UUID
	CategoryID  uuid.UUID
	Kind        domain.SeriesKind
	Description string
	DisplayName string
	Amount      domain.Money
	Currency    string

	Recurrence domain.Recurrence

	StartOn           domain.Date
	EndOn             domain.Date
	NextDueOn         domain.Date
	OverrideNextDueOn domain.Date
	OverrideNextAmt   domain.Money
	HasOverrideAmt    bool

	// The Bill Connect switch, so the plan expands occurrences exactly as the
	// Bills screen does.
	AutoAdjustDueOn bool

	IsActive  bool
	IsDeleted bool
}

func (s PlanSeriesRow) Label() string { return s.DomainSeries().Label() }

func (s PlanSeriesRow) DomainSeries() domain.Series {
	return domain.Series{
		ID:                    domain.ID(s.ID.String()),
		AccountID:             domain.ID(s.AccountID.String()),
		Kind:                  s.Kind,
		Description:           s.Description,
		DisplayName:           s.DisplayName,
		Amount:                s.Amount,
		Currency:              s.Currency,
		CategoryID:            domain.ID(s.CategoryID.String()),
		Recurrence:            s.Recurrence,
		StartOn:               s.StartOn,
		EndOn:                 s.EndOn,
		NextDueOn:             s.NextDueOn,
		OverrideNextDueOn:     s.OverrideNextDueOn,
		OverrideNextAmount:    s.OverrideNextAmt,
		HasOverrideNextAmount: s.HasOverrideAmt,
		AutoAdjustDueOn:       s.AutoAdjustDueOn,
		IsActive:              s.IsActive,
		IsDeleted:             s.IsDeleted,
	}
}

// occurrence is one slot on the day it is shown, at the amount
// domain.OccurrenceAmount gives the scheduled slot: the user's override, then
// the slot's linked bill, then the estimate.
func (s PlanSeriesRow) occurrence(
	slot domain.OccurrenceSlot, bills []domain.BillConnect,
) domain.SeriesOccurrence {
	expected := domain.OccurrenceAmount(s.DomainSeries(), slot.ScheduledOn, bills)
	return domain.SeriesOccurrence{
		SeriesID:       domain.ID(s.ID.String()),
		DueOn:          slot.DueOn,
		Kind:           s.Kind,
		ExpectedAmount: expected,
	}
}

// --- Persistence -------------------------------------------------------------
//
// Every statement filters on space_id and takes it as an argument.

func planMonthColumns() string {
	cols := []string{"id", "month", "calculated_rollover_amount"}
	for _, name := range FamilyNames {
		cols = append(cols,
			"calculated_"+name+"_amount",
			name+"_txn_ids",
			"excluded_"+name+"_txn_ids",
			"overwritten_"+name+"_amount",
			"reset_overwritten_"+name)
	}
	return strings.Join(append(cols,
		"set_aside", "total_to_spend_amount", "left_to_spend_amount",
		"projected_other_spending", "projection_type", "projection_start_date",
		"projection_end_date", "projection_buffer", "projection_window_months",
		"is_closed_out", "closed_out_at", "show_closed_out", "new_month_viewed",
		"hide_excluded_from_ui",
	), ", ")
}

func scanPlanMonth(row rowScanner) (*PlanMonthRow, error) {
	out := &PlanMonthRow{}
	var (
		monthOn      time.Time
		rollover     dbconv.Number
		calc         [FamilyCount]dbconv.Number
		over         [FamilyCount]dbconv.Number
		setAside     dbconv.Number
		totalToSpend dbconv.Number
		leftToSpend  dbconv.Number
		projected    dbconv.Number
		buffer       dbconv.Number
		startOn      *time.Time
		endOn        *time.Time
	)

	dest := []any{&out.ID, &monthOn, &rollover}
	for fam := Family(0); fam < FamilyCount; fam++ {
		dest = append(dest, &calc[fam], &out.TxnIDs[fam], &out.Excluded[fam], &over[fam], &out.Reset[fam])
	}
	dest = append(dest,
		&setAside, &totalToSpend, &leftToSpend,
		&projected, &out.ProjectionType, &startOn, &endOn, &buffer, &out.ProjectionWindowMonths,
		&out.IsClosedOut, &out.ClosedOutAt, &out.ShowClosedOut, &out.NewMonthViewed,
		&out.HideExcludedFromUI)
	if err := row.Scan(dest...); err != nil {
		return nil, err
	}

	out.Month = domain.MonthOf(domain.DateOf(monthOn))
	var err error
	read := func(dst *domain.Money, n dbconv.Number, column string) {
		if err == nil {
			*dst, err = dbconv.ReadMoney(n, "spending_plan_months."+column)
		}
	}
	read(&out.Rollover, rollover, "calculated_rollover_amount")
	for fam := Family(0); fam < FamilyCount; fam++ {
		read(&out.Calc[fam], calc[fam], "calculated_"+FamilyNames[fam]+"_amount")
		if err == nil {
			out.Over[fam], out.HasOver[fam], err = dbconv.ReadNullMoney(
				over[fam], "spending_plan_months.overwritten_"+FamilyNames[fam]+"_amount")
		}
	}
	read(&out.SetAside, setAside, "set_aside")
	read(&out.TotalToSpend, totalToSpend, "total_to_spend_amount")
	read(&out.LeftToSpend, leftToSpend, "left_to_spend_amount")
	read(&out.ProjectedOtherSpending, projected, "projected_other_spending")
	read(&out.ProjectionBuffer, buffer, "projection_buffer")
	if err != nil {
		return nil, err
	}
	out.ProjectionStartOn = dbconv.ReadNullDate(startOn)
	out.ProjectionEndOn = dbconv.ReadNullDate(endOn)
	if out.ProjectionType == "" {
		out.ProjectionType = domain.ProjectionRunRate
	}
	if out.ProjectionWindowMonths < 1 {
		out.ProjectionWindowMonths = defaultProjectionWindow
	}
	return out, nil
}

func (p *Plan) listMonths(ctx context.Context, spaceID store.SpaceID) (map[domain.Month]*PlanMonthRow, error) {
	rows, err := p.conn().Query(ctx,
		`SELECT `+planMonthColumns()+` FROM spending_plan_months WHERE space_id = $1 ORDER BY month`,
		spaceID.UUID())
	if err != nil {
		return nil, fmt.Errorf("service: list spending plan months: %w", err)
	}
	defer rows.Close()

	out := map[domain.Month]*PlanMonthRow{}
	for rows.Next() {
		row, err := scanPlanMonth(rows)
		if err != nil {
			return nil, fmt.Errorf("service: list spending plan months: %w", err)
		}
		out[row.Month] = row
	}
	return out, rows.Err()
}

func (p *Plan) readMonth(ctx context.Context, spaceID store.SpaceID, month domain.Month) (*PlanMonthRow, error) {
	row, err := scanPlanMonth(p.conn().QueryRow(ctx,
		`SELECT `+planMonthColumns()+` FROM spending_plan_months WHERE space_id = $1 AND month = $2`,
		spaceID.UUID(), month.FirstDay()))
	if errors.Is(err, sqlitedb.ErrNoRows) {
		return nil, fmt.Errorf("service: read spending plan month %s: %w", month, store.ErrNotFound)
	}
	if err != nil {
		return nil, fmt.Errorf("service: read spending plan month: %w", err)
	}
	return row, nil
}

// insertMonth creates a month's row and returns its id, or the id of the row a
// concurrent request created first (two first reads of a month both find no row).
func insertMonth(ctx context.Context, conn dbConn, spaceID store.SpaceID, row *PlanMonthRow) (uuid.UUID, error) {
	if _, err := conn.Exec(ctx, `
		INSERT INTO spending_plan_months (id, space_id, month, projection_type, projection_window_months)
		VALUES ($1, $2, $3, $4, $5)
		ON CONFLICT (space_id, month) DO NOTHING`,
		uuid.New(), spaceID.UUID(), row.Month.FirstDay(),
		string(row.ProjectionType), row.ProjectionWindowMonths); err != nil {
		return uuid.Nil, fmt.Errorf("service: create spending plan month: %w", err)
	}
	var id uuid.UUID
	if err := conn.QueryRow(ctx,
		`SELECT id FROM spending_plan_months WHERE space_id = $1 AND month = $2`,
		spaceID.UUID(), row.Month.FirstDay()).Scan(&id); err != nil {
		return uuid.Nil, fmt.Errorf("service: create spending plan month: %w", err)
	}
	return id, nil
}

// SaveMonthUserState writes the fields a person edited and nothing the engine
// computed; SaveResults writes the recalculation over the whole chain.
func (p *Plan) SaveMonthUserState(ctx context.Context, spaceID store.SpaceID, row *PlanMonthRow) error {
	set := []string{"updated_at = now()"}
	args := []any{spaceID.UUID(), row.ID}
	add := func(clause string, value any) {
		args = append(args, value)
		set = append(set, fmt.Sprintf("%s = $%d", clause, len(args)))
	}
	for fam := Family(0); fam < FamilyCount; fam++ {
		add("excluded_"+FamilyNames[fam]+"_txn_ids", store.NonNil(row.Excluded[fam]))
		add("overwritten_"+FamilyNames[fam]+"_amount", dbconv.NullMoney(row.Over[fam], row.HasOver[fam]))
		add("reset_overwritten_"+FamilyNames[fam], row.Reset[fam])
	}
	add("projection_type", string(row.ProjectionType))
	add("projection_window_months", row.ProjectionWindowMonths)
	add("projection_buffer", dbconv.Money(row.ProjectionBuffer))

	_, err := p.conn().Exec(ctx,
		`UPDATE spending_plan_months SET `+strings.Join(set, ", ")+` WHERE space_id = $1 AND id = $2`,
		args...)
	if err != nil {
		return fmt.Errorf("service: update spending plan month: %w", err)
	}
	return nil
}

// SaveResults materializes every open month the chain recomputed, in one
// transaction so no mix of old and new chains is left behind. It is safe to run
// concurrently: a month and its carried envelopes are inserted only if absent,
// and the carried envelopes' ids are derived.
//
// Closed months are skipped; their stored figures are the answer.
func (p *Plan) SaveResults(ctx context.Context, spaceID store.SpaceID, view PlanView) error {
	return p.inTx(ctx, func(tx *store.Store) error {
		for _, month := range view.Months {
			if err := saveMonth(ctx, tx.Conn(), spaceID, view, month); err != nil {
				return err
			}
		}
		return nil
	})
}

func saveMonth(ctx context.Context, conn dbConn, spaceID store.SpaceID, view PlanView, month domain.Month) error {
	row := view.Rows[month]
	computed, ok := view.Results[month]
	if !ok || computed.IsClosedOut {
		return nil
	}
	if row.ID == uuid.Nil {
		id, err := insertMonth(ctx, conn, spaceID, row)
		if err != nil {
			return err
		}
		row.ID = id
	}
	set := []string{"updated_at = now()"}
	args := []any{spaceID.UUID(), row.ID}
	add := func(clause string, value any) {
		args = append(args, value)
		set = append(set, fmt.Sprintf("%s = $%d", clause, len(args)))
	}

	for _, group := range BillGroups {
		var amounts []domain.Money
		for _, bill := range view.Bills[month] {
			if bill.Group == group.Name && !bill.IsExcluded {
				amounts = append(amounts, bill.Amount)
			}
		}
		add("calculated_"+FamilyNames[group.Family]+"_amount", dbconv.Money(domain.Total(amounts...)))
	}
	for _, key := range domain.BucketOrder {
		fam, ok := BucketFamily(key)
		if !ok || key == domain.BucketBills {
			continue
		}
		bucket := computed.Bucket(key)
		add("calculated_"+FamilyNames[fam]+"_amount", dbconv.Money(bucket.CalculatedAmount))
		add(FamilyNames[fam]+"_txn_ids", uuidArray(bucket.ContributingTxnIDs))
	}
	add("bills_txn_ids", uuidArray(computed.Bucket(domain.BucketBills).ContributingTxnIDs))
	add("calculated_rollover_amount", dbconv.Money(computed.Bucket(domain.BucketRollover).Effective()))
	add("left_to_spend_amount", dbconv.Money(computed.LeftThisMonth()))
	add("total_to_spend_amount", dbconv.Money(domain.Total(
		computed.Bucket(domain.BucketIncome).Effective(),
		computed.Bucket(domain.BucketRollover).Effective())))
	add("projected_other_spending", dbconv.Money(domain.ProjectedOtherSpending(computed, view.AsOf)))

	_, err := conn.Exec(ctx,
		`UPDATE spending_plan_months SET `+strings.Join(set, ", ")+` WHERE space_id = $1 AND id = $2`,
		args...)
	if err != nil {
		return fmt.Errorf("service: materialize spending plan month: %w", err)
	}

	byID := map[domain.ID]domain.EnvelopeStatus{}
	for _, status := range computed.Envelopes {
		byID[status.EnvelopeID] = status
	}
	envelopes := view.Envelopes[month]
	for i := range envelopes {
		envelope := &envelopes[i]
		if envelope.MonthID == uuid.Nil {
			envelope.MonthID = row.ID
			if err := insertCarriedEnvelope(ctx, conn, spaceID, *envelope); err != nil {
				return err
			}
		}
		status, ok := byID[domain.ID(envelope.ID.String())]
		if !ok {
			continue
		}
		// The rollover is written only while it is still the engine's carry: a figure
		// the user set between this chain's read and this write is theirs.
		_, err := conn.Exec(ctx, `
			UPDATE envelopes SET calculated_spent_amount = $3, txn_ids = $4,
				rollover_amount = CASE WHEN rollover_is_carried THEN $5 ELSE rollover_amount END,
				updated_at = now()
			WHERE space_id = $1 AND id = $2`,
			spaceID.UUID(), envelope.ID, dbconv.Money(status.Spent), uuidArray(status.TxnIDs),
			dbconv.Money(status.RolloverIn))
		if err != nil {
			return fmt.Errorf("service: materialize envelope: %w", err)
		}
	}
	return nil
}

// rollForward is the envelopes a month with no stored row starts with: the
// previous month's recurring ones, through domain.RollForward, with a
// placeholder rollover until RecalculateChain carries the figure in. Ids derive
// from group and month, so concurrent materializations insert the same rows.
func rollForward(prior []EnvelopeRow, month domain.Month) []EnvelopeRow {
	var out []EnvelopeRow
	for _, row := range prior {
		next, ok := domain.RollForward(row.DomainEnvelope(), domain.EnvelopeStatus{})
		if !ok {
			continue
		}
		group, err := store.ParseID(next.GroupID)
		if err != nil {
			continue
		}
		out = append(out, EnvelopeRow{
			ID:                  uuid.NewSHA1(envelopeNamespace, []byte(group.String()+":"+month.String())),
			FilterID:            row.FilterID,
			GroupID:             group,
			Name:                next.Name,
			TargetAmount:        next.TargetAmount,
			Rollover:            next.RolloverIn,
			RolloverCarried:     next.RolloverCarried,
			AutoReleaseRollover: next.AutoReleaseRollover,
			Recurring:           next.Recurring,
			TxnIDs:              []uuid.UUID{},
			ExcludedTxnIDs:      []uuid.UUID{},
			CreatedAt:           next.CreatedAt,
		})
	}
	return out
}

var envelopeNamespace = uuid.MustParse("8a4f3c6e-2b1d-4e7a-9c5f-1d2e3f4a5b6c")

// insertCarriedEnvelope writes a rolled-forward envelope unless a concurrent
// materialization already has. created_at is the predecessor's, because it is
// the tie-break between two envelopes claiming one transaction and must not
// change from month to month.
func insertCarriedEnvelope(ctx context.Context, conn dbConn, spaceID store.SpaceID, envelope EnvelopeRow) error {
	_, err := conn.Exec(ctx, `
		INSERT INTO envelopes (id, space_id, spending_plan_month_id, filter_id, recurring_group_id, name,
			target_amount, rollover_amount, rollover_is_carried, auto_release_rollover, recurring, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)
		ON CONFLICT (id) DO NOTHING`,
		envelope.ID, spaceID.UUID(), envelope.MonthID, envelope.FilterID, dbconv.NullUUID(envelope.GroupID),
		envelope.Name, dbconv.Money(envelope.TargetAmount), dbconv.Money(envelope.Rollover),
		envelope.RolloverCarried, envelope.AutoReleaseRollover, envelope.Recurring, envelope.CreatedAt)
	if err != nil {
		return fmt.Errorf("service: carry envelope forward: %w", err)
	}
	return nil
}

const envelopeColumns = `id, spending_plan_month_id, filter_id, name, target_amount,
	overwritten_target_amount, calculated_spent_amount, rollover_amount, auto_release_rollover,
	recurring, txn_ids, excluded_txn_ids, created_at, recurring_group_id, rollover_is_carried`

func scanEnvelope(row rowScanner) (EnvelopeRow, error) {
	var (
		out       EnvelopeRow
		target    dbconv.Number
		overTarge dbconv.Number
		spent     dbconv.Number
		rollover  dbconv.Number
		group     *uuid.UUID
	)
	err := row.Scan(&out.ID, &out.MonthID, &out.FilterID, &out.Name, &target,
		&overTarge, &spent, &rollover, &out.AutoReleaseRollover,
		&out.Recurring, &out.TxnIDs, &out.ExcludedTxnIDs, &out.CreatedAt, &group, &out.RolloverCarried)
	if err != nil {
		return EnvelopeRow{}, err
	}
	out.GroupID = dbconv.ReadNullUUID(group)
	if out.TargetAmount, err = dbconv.ReadMoney(target, "envelopes.target_amount"); err != nil {
		return EnvelopeRow{}, err
	}
	if out.OverwrittenTarget, out.HasOverwrittenTarget, err = dbconv.ReadNullMoney(
		overTarge, "envelopes.overwritten_target_amount"); err != nil {
		return EnvelopeRow{}, err
	}
	if out.CalculatedSpent, err = dbconv.ReadMoney(spent, "envelopes.calculated_spent_amount"); err != nil {
		return EnvelopeRow{}, err
	}
	if out.Rollover, err = dbconv.ReadMoney(rollover, "envelopes.rollover_amount"); err != nil {
		return EnvelopeRow{}, err
	}
	return out, nil
}

func (p *Plan) ListEnvelopes(ctx context.Context, spaceID store.SpaceID, monthID uuid.UUID) ([]EnvelopeRow, error) {
	if monthID == uuid.Nil {
		return nil, nil
	}
	rows, err := p.conn().Query(ctx,
		`SELECT `+envelopeColumns+` FROM envelopes
		 WHERE space_id = $1 AND spending_plan_month_id = $2
		 ORDER BY created_at, id`,
		spaceID.UUID(), monthID)
	if err != nil {
		return nil, fmt.Errorf("service: list envelopes: %w", err)
	}
	defer rows.Close()

	out := []EnvelopeRow{}
	for rows.Next() {
		envelope, err := scanEnvelope(rows)
		if err != nil {
			return nil, fmt.Errorf("service: list envelopes: %w", err)
		}
		out = append(out, envelope)
	}
	return out, rows.Err()
}

func (p *Plan) LoadEnvelope(ctx context.Context, spaceID store.SpaceID, monthID, id uuid.UUID) (EnvelopeRow, error) {
	envelope, err := scanEnvelope(p.conn().QueryRow(ctx,
		`SELECT `+envelopeColumns+` FROM envelopes
		 WHERE space_id = $1 AND spending_plan_month_id = $2 AND id = $3`,
		spaceID.UUID(), monthID, id))
	if err != nil {
		if errors.Is(err, sqlitedb.ErrNoRows) {
			return EnvelopeRow{}, store.ErrNotFound
		}
		return EnvelopeRow{}, fmt.Errorf("service: read envelope: %w", err)
	}
	return envelope, nil
}

func (p *Plan) InsertEnvelope(ctx context.Context, spaceID store.SpaceID, envelope EnvelopeRow) error {
	_, err := p.conn().Exec(ctx, `
		INSERT INTO envelopes (id, space_id, spending_plan_month_id, filter_id, name,
			target_amount, rollover_amount, auto_release_rollover, recurring)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9)`,
		envelope.ID, spaceID.UUID(), envelope.MonthID, envelope.FilterID, envelope.Name,
		dbconv.Money(envelope.TargetAmount), dbconv.Money(envelope.Rollover),
		envelope.AutoReleaseRollover, envelope.Recurring)
	if err != nil {
		return fmt.Errorf("service: create envelope: %w", err)
	}
	return nil
}

func (p *Plan) UpdateEnvelope(ctx context.Context, spaceID store.SpaceID, envelope EnvelopeRow) error {
	_, err := p.conn().Exec(ctx, `
		UPDATE envelopes SET name = $3, target_amount = $4, overwritten_target_amount = $5,
			rollover_amount = $6, auto_release_rollover = $7, recurring = $8,
			rollover_is_carried = $9, updated_at = now()
		WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), envelope.ID, envelope.Name, dbconv.Money(envelope.TargetAmount),
		dbconv.NullMoney(envelope.OverwrittenTarget, envelope.HasOverwrittenTarget),
		dbconv.Money(envelope.Rollover), envelope.AutoReleaseRollover, envelope.Recurring,
		envelope.RolloverCarried)
	if err != nil {
		return fmt.Errorf("service: update envelope: %w", err)
	}
	return nil
}

// RenameEnvelopeGroup gives every month's copy of one envelope the same name. A
// recurring envelope's months already share one filter, so a per-month name
// would make one dialog rename September but recategorize all of it. A one-month
// envelope has a single row.
func (p *Plan) RenameEnvelopeGroup(
	ctx context.Context, spaceID store.SpaceID, filterID uuid.UUID, name string,
) error {
	_, err := p.conn().Exec(ctx, `
		UPDATE envelopes SET name = $3, updated_at = now()
		 WHERE space_id = $1 AND filter_id = $2 AND name IS DISTINCT FROM $3`,
		spaceID.UUID(), filterID, name)
	if err != nil {
		return fmt.Errorf("service: rename envelope group: %w", err)
	}
	return nil
}

// rowScanner is what *sqlitedb.Row and *sqlitedb.Rows have in common, so one scan function
// serves both the single-row and the list query for a table.
type rowScanner interface {
	Scan(dest ...any) error
}

// LoadPostings joins a transaction query to its accounts and categories, and
// also returns the rows (response bodies need fields a Posting lacks).
//
// Refund links are applied here, at the bottom of the read path, so every screen
// that reads postings treats a linked credit the same way.
func LoadPostings(ctx context.Context, db *store.Store, spaceID store.SpaceID, q store.TransactionQuery) ([]domain.Posting, map[uuid.UUID]store.Transaction, error) {
	rows, err := db.ListTransactions(ctx, spaceID, q)
	if err != nil {
		return nil, nil, err
	}
	byID := make(map[uuid.UUID]store.Transaction, len(rows))
	for _, row := range rows {
		byID[row.ID] = row
	}
	if len(rows) == 0 {
		return nil, byID, nil
	}

	// Deleted and closed accounts included: store.BuildPostings errors on a
	// transaction whose account it was not given.
	accounts, err := db.ListAccounts(ctx, spaceID, store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, nil, err
	}
	categories, err := db.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, nil, err
	}
	postings, err := store.BuildPostings(rows, accounts, categories)
	if err != nil {
		return nil, nil, err
	}
	postings, err = db.RefileRefunds(ctx, spaceID, postings, categories)
	if err != nil {
		return nil, nil, err
	}
	return postings, byID, nil
}

// activeSeries is every live series, read through the same matcher the /series
// resource uses so the plan and the Bills & Income screen agree.
func (p *Plan) activeSeries(ctx context.Context, spaceID store.SpaceID) ([]PlanSeriesRow, error) {
	loaded, err := NewSeriesMatcher(p.store).LoadSeriesRows(ctx,
		`SELECT `+seriesColumns+` FROM series
		  WHERE space_id = $1 AND NOT is_deleted AND is_active
		  ORDER BY COALESCE(next_due_on, start_on), id`, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	out := make([]PlanSeriesRow, 0, len(loaded))
	for _, row := range loaded {
		out = append(out, fromSeriesRow(row))
	}
	return out, nil
}

// goalContributions is every goal's join rows, signed as a transaction is:
// negative for money moved into the goal, which domain.ComputeMonth sums straight
// into the Goals bucket. A contribution whose row was deleted has no amount.
func (p *Plan) goalContributions(
	ctx context.Context, spaceID store.SpaceID, ledger map[uuid.UUID]store.Transaction,
) ([]domain.GoalContribution, error) {
	rows, err := p.conn().Query(ctx,
		`SELECT id, txn_ids, withdrawal_txn_ids, spending_txn_ids, is_taken_from_plan, closed_on
		   FROM goals
		  WHERE space_id = $1 AND is_deleted = false
		  ORDER BY created_at, id`, spaceID.UUID())
	if err != nil {
		return nil, fmt.Errorf("service: list goals: %w", err)
	}
	defer rows.Close()

	var goals []store.GoalLinks
	for rows.Next() {
		var (
			one      store.GoalLinks
			closedOn *time.Time
		)
		if err := rows.Scan(&one.GoalID, &one.TxnIDs, &one.WithdrawalTxnIDs,
			&one.SpendingTxnIDs, &one.IsTakenFromPlan, &closedOn); err != nil {
			return nil, fmt.Errorf("service: list goals: %w", err)
		}
		one.ClosedOn = dbconv.ReadNullDate(closedOn)
		goals = append(goals, one)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("service: list goals: %w", err)
	}

	var out []domain.GoalContribution
	for _, goal := range goals {
		out = append(out, store.GoalContributions(goal, store.LedgerLookup(ledger))...)
	}
	return out, nil
}
