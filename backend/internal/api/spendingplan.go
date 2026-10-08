package api

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The spending plan: one materialized month, and every edit that can be made
// to it. The arithmetic is internal/domain's; anything here that looks like a
// calculation is a bug.
//
//   - Every mutation answers with the whole recalculated month, since a
//     rollover edit cascades forward and an envelope target moves a bucket.
//   - `as_of` rides on the payload: per-day and the run rate divide by a day
//     count, and a client clock would be off by a month at 00:05 on the 1st.
//   - Bills is one bucket whose rows name their group (bill, subscription,
//     transfer), so the three subtotals and the one line share rows.
//   - An unfulfilled occurrence is identified by its slot, `{series_id}:
//     {due_on}` on the wire and a stable derived UUID (slotExclusionID) in
//     the uuid[] exclusion columns.
//   - A closed month is frozen: RecalculateChain returns it as stored, and
//     every mutation here refuses it.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSpendingPlanServiceHandler(spendingPlanService{env}, opts...)
	})
}

type spendingPlanService struct{ env *Env }

// EnvelopeFilterScope marks the filters this resource creates, so the filter
// list can tell an envelope's membership rule from a saved search.
const EnvelopeFilterScope = "envelope"

// defaultProjectionWindow is how many months average_n_months averages until
// the user says otherwise.
const defaultProjectionWindow = 3

// maxProjectionWindow bounds the average window, and so the chain query.
const maxProjectionWindow = 24

// --- The engine, and what this file owns --------------------------------------

// The plan is computed in internal/service, where the alert sweep also reaches
// it; this file is the HTTP half. The aliases keep the engine's vocabulary
// without a conversion to keep in step.

type (
	planMonthRow  = service.PlanMonthRow
	envelopeRow   = service.EnvelopeRow
	planView      = service.PlanView
	occurrenceRow = service.PlanOccurrence
)

var (
	billGroups      = service.BillGroups
	bucketFamily    = service.BucketFamily
	slotExclusionID = service.SlotExclusionID
)

// plan is the engine, with this environment's clock.
func plan(env *Env) *service.Plan { return planOn(env, env.DB) }

// planOn is the engine over st, for a handler writing inside a transaction.
func planOn(env *Env, st *store.Store) *service.Plan {
	engine := service.NewPlan(st)
	engine.Now = env.now
	return engine
}

func parseBucketKey(raw string) (domain.BucketKey, error) {
	key := domain.BucketKey(raw)
	if _, ok := bucketFamily(key); ok {
		return key, nil
	}
	// Rollover is a bucket on the wire but it is carried in, not computed from
	// rows: it has nothing to override and nothing to exclude.
	return "", errNotFound("Bucket")
}

// --- Wire types --------------------------------------------------------------

type BucketResponse struct {
	Key domain.BucketKey `json:"key"`
	// CalculatedAmount is what the engine computed; EffectiveAmount is what
	// the plan actually adds up, which is the override when one is set.
	CalculatedAmount domain.Money `json:"calculated_amount"`
	// PostedAmount is the part of CalculatedAmount that a transaction backs;
	// it differs only in Income and Bills, by occurrences still expected.
	PostedAmount      domain.Money  `json:"posted_amount"`
	EffectiveAmount   domain.Money  `json:"effective_amount"`
	OverwrittenAmount *domain.Money `json:"overwritten_amount"`
	// ContributingTxnIDs is the audit trail: the exact rows behind the figure.
	ContributingTxnIDs []string `json:"contributing_txn_ids"`
	// ExcludedEntryIDs are the rows dropped for this month only. A slot with
	// no transaction appears here as `{series_id}:{due_on}`.
	ExcludedEntryIDs []string `json:"excluded_entry_ids"`
	// Contributing and Excluded are those same two id lists, named, so a
	// panel can draw a bucket without fetching the month's transactions.
	Contributing []PlanEntry `json:"contributing"`
	Excluded     []PlanEntry `json:"excluded"`
}

// The status vocabulary, shared by the plan, the reminders and a series'
// history. `received` and `paid` are one state (money moved), worded for
// income and for everything else. A charge linked off-schedule is `paid` with
// SeriesHistoryRow.OffSchedule set.
const (
	entryReceived = "received"
	entryPaid     = "paid"
	entryPastDue  = "past_due"
	entryUpcoming = "upcoming"
	entrySkipped  = "skipped"
)

// fulfilledStatus is what "the money moved" is called for this kind of series.
func fulfilledStatus(kind domain.SeriesKind) string {
	if kind == domain.SeriesIncome {
		return entryReceived
	}
	return entryPaid
}

// PlanEntry is one row behind a bucket's figure: a posted transaction, or an
// occurrence nothing has posted against yet, whose identity is the slot. Hence
// ID is a string and TxnID is nullable.
type PlanEntry struct {
	ID           string       `json:"id"`
	TxnID        *string      `json:"txn_id"`
	SeriesID     *string      `json:"series_id"`
	Name         string       `json:"name"`
	DueOn        Date         `json:"due_on"`
	Status       string       `json:"status"`
	CategoryName *string      `json:"category_name"`
	Amount       domain.Money `json:"amount"`
	// AccountID is the account the row is posted in. Null on an unposted
	// slot.
	AccountID *string `json:"account_id"`
	// IsSplit marks a row listed as one share of itself rather than whole. Not
	// derivable from the list: an envelope may take only one part of a row.
	IsSplit bool `json:"is_split"`
	// Group is the bills bucket's three families, or the goals bucket's rows.
	// Null where the bucket is one undivided list.
	Group      *string `json:"group"`
	IsTransfer bool    `json:"is_transfer"`
	// IsPadding marks a padding income row (domain.Transaction.IsPadding),
	// which a panel folds into one line per name and category. The bucket
	// counts it like any other row.
	IsPadding bool `json:"is_padding"`
}

// OtherSpendSlice is one bubble of the packed-circle chart: one category's
// share of the Other Spend bucket. Actuals only, positive, largest first.
//
// Two levels: the category group, with its leaf categories as Children.
//
// TxnIDs are the rows the bucket counted, carried here so a second query
// cannot disagree with the figure printed above.
type OtherSpendSlice struct {
	CategoryID   *string      `json:"category_id"`
	CategoryName string       `json:"category_name"`
	Spent        domain.Money `json:"spent"`
	TxnIDs       []string     `json:"txn_ids"`
	// Children is the level below, largest first. Empty on a leaf and on a
	// group whose only member is itself.
	Children []OtherSpendSlice `json:"children"`
}

// CategoryRef is one category an envelope claims, for the chips beside its name.
type CategoryRef struct {
	ID   uuid.UUID `json:"id"`
	Name string    `json:"name"`
}

// BillRow is one occurrence inside the folded Bills bucket.
//
// Group is bill, subscription or transfer. The transfer group subtotals to
// zero on purpose: the money was counted when the purchase posted.
type BillRow struct {
	// ID is the transaction id once something has posted against the slot, and
	// the composite `{series_id}:{due_on}` while the occurrence is still
	// expected. Both forms are accepted by the exclusion endpoints.
	ID          string       `json:"id"`
	Group       string       `json:"group"`
	SeriesID    uuid.UUID    `json:"series_id"`
	Name        string       `json:"name"`
	DueOn       Date         `json:"due_on"`
	Amount      domain.Money `json:"amount"`
	IsFulfilled bool         `json:"is_fulfilled"`
	TxnIDs      []uuid.UUID  `json:"txn_ids"`
	IsExcluded  bool         `json:"is_excluded"`
}

type BillSubtotal struct {
	Group  string       `json:"group"`
	Amount domain.Money `json:"amount"`
}

type EnvelopeResponse struct {
	ID       uuid.UUID `json:"id"`
	Name     string    `json:"name"`
	FilterID uuid.UUID `json:"filter_id"`
	// Categories is what the filter selects, resolved to names. The membership
	// rule stays in the filter (ground rule 3).
	Categories []CategoryRef `json:"categories"`

	TargetAmount            domain.Money  `json:"target_amount"`
	OverwrittenTargetAmount *domain.Money `json:"overwritten_target_amount"`
	// Target is the figure the month reserves: the override when set.
	Target     domain.Money `json:"target"`
	RolloverIn domain.Money `json:"rollover_amount"`
	// Spent is netted, not the sum of magnitudes: a refund inside an envelope
	// gives the money back. calculations.md §13 records that departure from §5.
	Spent     domain.Money `json:"spent"`
	Budget    domain.Money `json:"budget"`
	Available domain.Money `json:"available"`
	// PctUsed has no upper bound; BarPct stops at 100.
	PctUsed             domain.Rate          `json:"pct_used"`
	BarPct              domain.Rate          `json:"bar_pct"`
	State               domain.EnvelopeState `json:"state"`
	Recurring           bool                 `json:"recurring"`
	AutoReleaseRollover bool                 `json:"auto_release_rollover"`
	TxnIDs              []uuid.UUID          `json:"txn_ids"`
	// Entries is what Spent is made of, one row per part charged, so the list
	// adds up to Spent; whole rows from TxnIDs do not.
	Entries []PlanEntry `json:"entries"`
}

type ProjectionResponse struct {
	Type         domain.ProjectionType `json:"type"`
	WindowMonths int                   `json:"window_months"`
	Buffer       domain.Money          `json:"buffer"`
	StartOn      *Date                 `json:"start_on"`
	EndOn        *Date                 `json:"end_on"`
}

// SpendingPlanMonth is one month of the plan as the client renders it.
type SpendingPlanMonth struct {
	Month string `json:"month"`
	// AsOf is the server's today. The client must use it for per-day figures,
	// not its own clock.
	AsOf        Date  `json:"as_of"`
	IsClosedOut bool  `json:"is_closed_out"`
	ClosedOutAt *Date `json:"closed_out_at"`

	Buckets       []BucketResponse `json:"buckets"`
	Bills         []BillRow        `json:"bills"`
	BillSubtotals []BillSubtotal   `json:"bill_subtotals"`

	Envelopes []EnvelopeResponse `json:"envelopes"`
	// ContestedTxnIDs maps a transaction to the envelopes that matched it and
	// lost the creation-order tie-break, so a figure the user expected in
	// another envelope can be explained.
	ContestedTxnIDs map[string][]string `json:"contested_txn_ids"`

	LeftThisMonth domain.Money  `json:"left_this_month"`
	PerDay        *domain.Money `json:"per_day"`
	DaysRemaining int           `json:"days_remaining"`
	// MonthResult and its per-day rate are the headline without the rollover,
	// the figure the rail leads with; left_this_month is the two added.
	MonthResult       domain.Money  `json:"month_result"`
	MonthResultPerDay *domain.Money `json:"month_result_per_day"`
	// DaysElapsed is the run rate's divisor, for the words beside it.
	DaysElapsed int `json:"days_elapsed"`

	Projection ProjectionResponse `json:"projection"`
	// OtherSpendByCategory is the Other Spend bucket broken out for the chart.
	OtherSpendByCategory   []OtherSpendSlice `json:"other_spend_by_category"`
	OtherSpendToDate       domain.Money      `json:"other_spend_to_date"`
	ProjectedOtherSpending domain.Money      `json:"projected_other_spending"`
	ProjectedLeft          domain.Money      `json:"projected_left"`
	ProjectedMonthResult   domain.Money      `json:"projected_month_result"`
}

// --- Handlers ----------------------------------------------------------------

func (s spendingPlanService) GetSpendingPlanMonth(
	ctx context.Context, req *agentifiv1.GetSpendingPlanMonthRequest,
) (*agentifiv1.GetSpendingPlanMonthResponse, error) {
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, s.env, spaceFrom(ctx), month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetSpendingPlanMonthResponse{Month: out}, nil
}

// UpdateSpendingPlanBucket sets or clears a bucket's user override, for this
// month only. Clearing restores the computed figure.
func (s spendingPlanService) UpdateSpendingPlanBucket(
	ctx context.Context, req *agentifiv1.UpdateSpendingPlanBucketRequest,
) (*agentifiv1.UpdateSpendingPlanBucketResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	key, err := parseBucketKey(req.GetBucket())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	overwritten, err := optMoneyOf(mask, "overwritten_amount", req.OverwrittenAmount)
	if err != nil {
		return nil, err
	}
	if !overwritten.Set {
		return nil, errInvalid("missing", []string{"body", "overwritten_amount"},
			"overwritten_amount is required; send null to clear the override")
	}

	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	fam, _ := bucketFamily(key)
	if overwritten.Null {
		row.Over[fam], row.HasOver[fam] = domain.Zero, false
	} else {
		row.Over[fam], row.HasOver[fam] = overwritten.Value.Round(), true
	}
	// An explicit new value supersedes reset_overwritten.
	row.Reset[fam] = false
	if err := plan(env).SaveMonthUserState(ctx, sp.ID(), row); err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSpendingPlanBucketResponse{Month: out}, nil
}

func (s spendingPlanService) ExcludeSpendingPlanEntry(
	ctx context.Context, req *agentifiv1.ExcludeSpendingPlanEntryRequest,
) (*agentifiv1.ExcludeSpendingPlanEntryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	key, err := parseBucketKey(req.GetBucket())
	if err != nil {
		return nil, err
	}
	entry, err := parseEntryID(req.GetEntryId())
	if err != nil {
		return nil, err
	}

	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	fam, _ := bucketFamily(key)
	row.Excluded[fam] = addID(row.Excluded[fam], entry.storageID())
	if err := plan(env).SaveMonthUserState(ctx, sp.ID(), row); err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ExcludeSpendingPlanEntryResponse{Month: out}, nil
}

func (s spendingPlanService) IncludeSpendingPlanEntry(
	ctx context.Context, req *agentifiv1.IncludeSpendingPlanEntryRequest,
) (*agentifiv1.IncludeSpendingPlanEntryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	key, err := parseBucketKey(req.GetBucket())
	if err != nil {
		return nil, err
	}
	entry, err := parseEntryID(req.GetEntryId())
	if err != nil {
		return nil, err
	}

	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	fam, _ := bucketFamily(key)
	aliases, err := exclusionAliases(ctx, env, sp, entry)
	if err != nil {
		return nil, err
	}
	kept, found := removeIDs(row.Excluded[fam], aliases)
	if !found {
		return nil, errNotFound("Exclusion")
	}
	row.Excluded[fam] = kept
	if err := plan(env).SaveMonthUserState(ctx, sp.ID(), row); err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.IncludeSpendingPlanEntryResponse{Month: out}, nil
}

// CreateSpendingPlanEnvelope adds a planned-spend item to one month. The
// envelope gets a filter of its own (ground rule 3).
func (s spendingPlanService) CreateSpendingPlanEnvelope(
	ctx context.Context, req *agentifiv1.CreateSpendingPlanEnvelopeRequest,
) (*agentifiv1.CreateSpendingPlanEnvelopeResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	name := req.GetName()
	if strings.TrimSpace(name) == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	target, err := moneyOrZero(req.GetTargetAmount(), "target_amount")
	if err != nil {
		return nil, err
	}
	categoryIDs, err := bodyIDsField("category_ids", req.GetCategoryIds())
	if err != nil {
		return nil, err
	}
	// A filter with no items matches everything: the whole ledger would land
	// in one envelope.
	if len(categoryIDs) == 0 {
		return nil, errInvalid("missing", []string{"body", "category_ids"},
			"category_ids must name at least one category")
	}
	for _, id := range categoryIDs {
		if err := checkCategory(ctx, env, sp, id); err != nil {
			return nil, err
		}
	}

	// One transaction: there is no envelope delete, so a filter whose envelope
	// insert failed would be an orphan.
	err = env.DB.InTx(ctx, func(tx *store.Store) error {
		engine := planOn(env, tx)
		row, err := engine.OpenMonth(ctx, sp.ID(), month)
		if err != nil {
			return err
		}
		filter := &store.Filter{
			Name:  name,
			Scope: EnvelopeFilterScope,
			Items: []store.FilterItem{{
				Field:    string(domain.FieldCategory),
				Operator: string(domain.OpIn),
				ValueIDs: categoryIDs,
			}},
		}
		if err := tx.CreateFilter(ctx, sp.ID(), filter); err != nil {
			return err
		}
		return engine.InsertEnvelope(ctx, sp.ID(), envelopeRow{
			ID:           uuid.New(),
			MonthID:      row.ID,
			FilterID:     filter.ID,
			Name:         name,
			TargetAmount: target.Round(),
			Recurring:    req.Recurring == nil || req.GetRecurring(),
			// "Roll over unused funds" on means do not auto-release it.
			AutoReleaseRollover: req.Rollover != nil && !req.GetRollover(),
		})
	})
	if err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateSpendingPlanEnvelopeResponse{Month: out}, nil
}

func (s spendingPlanService) UpdateSpendingPlanEnvelope(
	ctx context.Context, req *agentifiv1.UpdateSpendingPlanEnvelopeRequest,
) (*agentifiv1.UpdateSpendingPlanEnvelopeResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetEnvelopeId(), "Envelope")
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	overwritten, err := optMoneyOf(mask, "overwritten_target_amount", req.OverwrittenTargetAmount)
	if err != nil {
		return nil, err
	}
	rollover, err := optMoneyOf(mask, "rollover_amount", req.RolloverAmount)
	if err != nil {
		return nil, err
	}
	var categoryIDs []uuid.UUID
	if mask["category_ids"] {
		if categoryIDs, err = bodyIDsField("category_ids", req.GetCategoryIds()); err != nil {
			return nil, err
		}
	}

	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	envelope, err := plan(env).LoadEnvelope(ctx, sp.ID(), row.ID, id)
	if err != nil {
		return nil, err
	}

	if overwritten.Set {
		if overwritten.Null {
			envelope.OverwrittenTarget, envelope.HasOverwrittenTarget = domain.Zero, false
		} else {
			envelope.OverwrittenTarget = overwritten.Value.Round()
			envelope.HasOverwrittenTarget = true
		}
	}
	// Setting the carried amount directly is the *change rollover amount*
	// operation.
	if err := applyRequired("rollover_amount", rollover, &envelope.Rollover); err != nil {
		return nil, err
	}
	if rollover.Set {
		set := domain.SetRollover(envelope.DomainEnvelope(), envelope.Rollover)
		envelope.Rollover, envelope.RolloverCarried = set.RolloverIn, set.RolloverCarried
	}
	if err := applyRequired("auto_release_rollover",
		optOf(mask, "auto_release_rollover", req.AutoReleaseRollover), &envelope.AutoReleaseRollover); err != nil {
		return nil, err
	}
	// name and recurring are what the envelope is: null leaves them alone.
	if recurring := optOf(mask, "recurring", req.Recurring); recurring.Present() {
		envelope.Recurring = recurring.Value
	}
	renamed := optOf(mask, "name", req.Name)
	if renamed.Present() {
		name := strings.TrimSpace(renamed.Value)
		if name == "" {
			return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
		}
		envelope.Name = name
	}
	err = env.DB.InTx(ctx, func(tx *store.Store) error {
		if mask["category_ids"] {
			if err := repointEnvelope(ctx, env, tx, sp, envelope, categoryIDs); err != nil {
				return err
			}
		}
		engine := planOn(env, tx)
		if err := engine.UpdateEnvelope(ctx, sp.ID(), envelope); err != nil {
			return err
		}
		// A recurring envelope is one filter and a row per month, so the name
		// follows the same rows the categories do.
		if renamed.Present() {
			return engine.RenameEnvelopeGroup(ctx, sp.ID(), envelope.FilterID, envelope.Name)
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSpendingPlanEnvelopeResponse{Month: out}, nil
}

// repointEnvelope rewrites the categories an envelope claims, in place on its
// existing filter, so anything else pointed at that filter follows it.
//
// Widening can make a transaction contested (oldest envelope wins); the losers
// come back in the month's contested ids.
func repointEnvelope(
	ctx context.Context, env *Env, st *store.Store, sp auth.SpaceContext, envelope envelopeRow, ids []uuid.UUID,
) error {
	// Same rule as creation: an empty filter matches everything.
	if len(ids) == 0 {
		return errInvalid("missing", []string{"body", "category_ids"},
			"category_ids must name at least one category")
	}
	for _, id := range ids {
		if err := checkCategory(ctx, env, sp, id); err != nil {
			return err
		}
	}

	filter, err := st.GetFilter(ctx, sp.ID(), envelope.FilterID)
	if err != nil {
		return err
	}
	// Replace-all: items this screen cannot show would be a membership rule
	// nobody can see or correct.
	filter.Name = envelope.Name
	filter.Items = []store.FilterItem{{
		Field:    string(domain.FieldCategory),
		Operator: string(domain.OpIn),
		ValueIDs: ids,
	}}
	return st.ReplaceFilterItems(ctx, sp.ID(), &filter)
}

// ReleaseSpendingPlanEnvelope is the *release unspent funds* operation. The
// carried funds already sit in the plan's rollover bucket (planned_spend
// reserves targets only), so nothing is added anywhere, or the same dollar
// would count twice.
func (s spendingPlanService) ReleaseSpendingPlanEnvelope(
	ctx context.Context, req *agentifiv1.ReleaseSpendingPlanEnvelopeRequest,
) (*agentifiv1.ReleaseSpendingPlanEnvelopeResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetEnvelopeId(), "Envelope")
	if err != nil {
		return nil, err
	}
	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	envelope, err := plan(env).LoadEnvelope(ctx, sp.ID(), row.ID, id)
	if err != nil {
		return nil, err
	}
	released, _ := domain.ReleaseRollover(envelope.DomainEnvelope())
	envelope.Rollover, envelope.RolloverCarried = released.RolloverIn, released.RolloverCarried
	if err := plan(env).UpdateEnvelope(ctx, sp.ID(), envelope); err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ReleaseSpendingPlanEnvelopeResponse{Month: out}, nil
}

func (s spendingPlanService) ReleaseAllSpendingPlanEnvelopes(
	ctx context.Context, req *agentifiv1.ReleaseAllSpendingPlanEnvelopesRequest,
) (*agentifiv1.ReleaseAllSpendingPlanEnvelopesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	envelopes, err := plan(env).ListEnvelopes(ctx, sp.ID(), row.ID)
	if err != nil {
		return nil, err
	}
	err = env.DB.InTx(ctx, func(tx *store.Store) error {
		engine := planOn(env, tx)
		for _, envelope := range envelopes {
			released, _ := domain.ReleaseRollover(envelope.DomainEnvelope())
			envelope.Rollover, envelope.RolloverCarried = released.RolloverIn, released.RolloverCarried
			if err := engine.UpdateEnvelope(ctx, sp.ID(), envelope); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.ReleaseAllSpendingPlanEnvelopesResponse{Month: out}, nil
}

// UpdateSpendingPlanProjection records how this month's other spending is
// carried forward. The method is stored, not just its result, so the
// projection stays explainable.
func (s spendingPlanService) UpdateSpendingPlanProjection(
	ctx context.Context, req *agentifiv1.UpdateSpendingPlanProjectionRequest,
) (*agentifiv1.UpdateSpendingPlanProjectionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	month, err := monthOf(req.GetMonth())
	if err != nil {
		return nil, err
	}
	projection := domain.ProjectionType(req.GetType())
	switch projection {
	case domain.ProjectionRunRate, domain.ProjectionPriorMonth, domain.ProjectionAverageNMonths:
	default:
		return nil, errInvalid("enum", []string{"body", "type"},
			"type must be run_rate, prior_month or average_n_months, got %q", projection)
	}
	window := defaultProjectionWindow
	if req.WindowMonths != nil {
		window = int(req.GetWindowMonths())
		if window < 1 || window > maxProjectionWindow {
			return nil, errInvalid("out_of_range", []string{"body", "window_months"},
				"window_months must be between 1 and %d", maxProjectionWindow)
		}
	}
	buffer, err := moneyOrNil(req.GetBuffer(), "buffer")
	if err != nil {
		return nil, err
	}

	row, err := plan(env).OpenMonth(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	row.ProjectionType = projection
	row.ProjectionWindowMonths = window
	if buffer != nil {
		row.ProjectionBuffer = buffer.Round()
	}
	if err := plan(env).SaveMonthUserState(ctx, sp.ID(), row); err != nil {
		return nil, err
	}
	out, err := respondWithMonth(ctx, env, sp, month)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSpendingPlanProjectionResponse{Month: out}, nil
}

// --- Orchestration -----------------------------------------------------------

func respondWithMonth(
	ctx context.Context, env *Env, sp auth.SpaceContext, month domain.Month,
) (*agentifiv1.SpendingPlanMonth, error) {
	view, err := plan(env).Compute(ctx, sp.ID(), month)
	if err != nil {
		return nil, err
	}
	// A viewer's read must not write, and materializing is a write.
	if sp.CanWrite() {
		if err := plan(env).SaveResults(ctx, sp.ID(), view); err != nil {
			return nil, err
		}
	}
	return monthProto(monthResponse(view)), nil
}

// --- Response ----------------------------------------------------------------

func monthResponse(view planView) SpendingPlanMonth {
	month := view.Results[view.Target]
	row := view.Rows[view.Target]
	bills := view.Bills[view.Target]

	buckets := make([]BucketResponse, 0, len(domain.BucketOrder))
	for _, key := range domain.BucketOrder {
		buckets = append(buckets, bucketResponse(key, month.Bucket(key), row, view))
	}

	subtotals := make([]BillSubtotal, 0, len(billGroups))
	for _, group := range billGroups {
		var amounts []domain.Money
		for _, bill := range bills {
			if bill.Group == group.Name && !bill.IsExcluded {
				amounts = append(amounts, bill.Amount)
			}
		}
		subtotals = append(subtotals, BillSubtotal{Group: group.Name, Amount: domain.Total(amounts...)})
	}

	envelopes := make([]EnvelopeResponse, 0, len(month.Envelopes))
	stored := map[domain.ID]envelopeRow{}
	for _, row := range view.Envelopes[view.Target] {
		stored[domain.ID(row.ID.String())] = row
	}
	for _, status := range month.Envelopes {
		envelopes = append(envelopes, envelopeResponse(status, stored[status.EnvelopeID], view))
	}

	contested := map[string][]string{}
	for txnID, envelopeIDs := range month.ContestedEnvelopeTxnIDs {
		ids := make([]string, 0, len(envelopeIDs))
		for _, id := range envelopeIDs {
			ids = append(ids, string(id))
		}
		contested[string(txnID)] = ids
	}

	response := SpendingPlanMonth{
		Month:           view.Target.String(),
		AsOf:            Date(view.AsOf),
		IsClosedOut:     month.IsClosedOut,
		ClosedOutAt:     nullableDate(dbconv.ReadNullDate(row.ClosedOutAt)),
		Buckets:         buckets,
		Bills:           billList(bills),
		BillSubtotals:   subtotals,
		Envelopes:       envelopes,
		ContestedTxnIDs: contested,
		LeftThisMonth:   month.LeftThisMonth(),
		Projection: ProjectionResponse{
			Type:         row.ProjectionType,
			WindowMonths: row.ProjectionWindowMonths,
			Buffer:       row.ProjectionBuffer,
			StartOn:      nullableDate(row.ProjectionStartOn),
			EndOn:        nullableDate(row.ProjectionEndOn),
		},
		OtherSpendByCategory:   otherSpendByCategory(month.Bucket(domain.BucketOtherSpend), month, view),
		OtherSpendToDate:       month.OtherSpendToDate(),
		ProjectedOtherSpending: domain.ProjectedOtherSpending(month, view.AsOf),
		ProjectedLeft:          domain.ProjectedLeft(month, view.AsOf),
		ProjectedMonthResult:   domain.ProjectedMonthResult(month, view.AsOf),
		MonthResult:            month.MonthResult(),
		DaysElapsed:            month.DaysElapsed(view.AsOf),
	}
	if perDay, ok := month.MonthResultPerDay(view.AsOf); ok {
		response.MonthResultPerDay = &perDay
	}
	if perDay, ok := month.PerDay(view.AsOf); ok {
		response.PerDay = &perDay
		response.DaysRemaining = domain.DaysRemainingInMonth(view.AsOf)
		if view.AsOf.Before(view.Target.FirstDay()) {
			response.DaysRemaining = view.Target.Days()
		}
	}
	return response
}

func bucketResponse(key domain.BucketKey, bucket domain.Bucket, row *planMonthRow, view planView) BucketResponse {
	out := BucketResponse{
		Key:                key,
		CalculatedAmount:   bucket.CalculatedAmount,
		PostedAmount:       bucket.PostedAmount,
		EffectiveAmount:    bucket.Effective(),
		ContributingTxnIDs: stringList(bucket.ContributingTxnIDs),
		ExcludedEntryIDs:   []string{},
	}
	out.Contributing, out.Excluded = bucketEntries(key, bucket, row, view)
	fam, ok := bucketFamily(key)
	if !ok {
		return out
	}
	if row.HasOver[fam] && !row.Reset[fam] {
		amount := row.Over[fam]
		out.OverwrittenAmount = &amount
	}

	// A stored slot exclusion is rendered back as its composite, the only form
	// the client can act on.
	slots := map[uuid.UUID]string{}
	for _, bill := range view.Bills[view.Target] {
		if strings.Contains(bill.ID, ":") {
			slots[slotExclusionID(bill.SeriesID, domain.Date(bill.DueOn))] = bill.ID
		}
	}
	for _, id := range row.Excluded[fam] {
		if composite, isSlot := slots[id]; isSlot {
			out.ExcludedEntryIDs = append(out.ExcludedEntryIDs, composite)
			continue
		}
		out.ExcludedEntryIDs = append(out.ExcludedEntryIDs, id.String())
	}
	return out
}

func envelopeResponse(status domain.EnvelopeStatus, row envelopeRow, view planView) EnvelopeResponse {
	txnIDs := make([]uuid.UUID, 0, len(status.TxnIDs))
	for _, id := range status.TxnIDs {
		if key, err := store.ParseID(id); err == nil {
			txnIDs = append(txnIDs, key)
		}
	}
	return EnvelopeResponse{
		ID:                      row.ID,
		Name:                    status.Name,
		FilterID:                row.FilterID,
		Categories:              envelopeCategories(view.Filters[domain.ID(row.FilterID.String())], view.Categories),
		TargetAmount:            row.TargetAmount,
		OverwrittenTargetAmount: store.PtrIf(row.OverwrittenTarget, row.HasOverwrittenTarget),
		Target:                  status.Target,
		RolloverIn:              status.RolloverIn,
		Spent:                   status.Spent,
		Budget:                  status.Budget(),
		Available:               status.Available(),
		PctUsed:                 status.PctUsed(),
		BarPct:                  status.BarPct(),
		State:                   status.State(),
		Recurring:               row.Recurring,
		AutoReleaseRollover:     row.AutoReleaseRollover,
		TxnIDs:                  store.NonNil(txnIDs),
		Entries:                 envelopeEntries(status, view),
	}
}

// envelopeEntries names the rows behind one envelope's spent figure, from the
// parts the engine actually charged it, never a second query by its filter
// (series and goal claims, exclusions and the tie-break all apply first).
//
// A closed month froze transaction ids rather than parts, so it is listed
// row by row, each split row as its splits.
func envelopeEntries(status domain.EnvelopeStatus, view planView) []PlanEntry {
	parts := status.Parts
	if len(parts) == 0 {
		parts = frozenParts(status.TxnIDs, view)
	}
	out := make([]PlanEntry, 0, len(parts))
	for _, part := range parts {
		if entry, ok := partEntry(part, domain.BucketPlannedSpend, view); ok {
			out = append(out, entry)
		}
	}
	sortPlanEntries(out)
	return out
}

// partEntry names one part of one posted row, with the part's own share and
// category (for a split, not its parent's).
func partEntry(part domain.SpendPart, key domain.BucketKey, view planView) (PlanEntry, bool) {
	txnID, err := store.ParseID(part.TxnID)
	if err != nil {
		return PlanEntry{}, false
	}
	entry, ok := postingEntry(txnID, key, view)
	if !ok {
		return PlanEntry{}, false
	}
	entry.ID = string(part.Key())
	entry.Amount = part.Amount.Round()
	entry.IsSplit = part.SplitID != ""
	entry.CategoryName = nil
	if part.HasCategory {
		name := categoryName(view, part.CategoryID, "")
		if name != "" {
			entry.CategoryName = &name
		}
	}
	return entry, true
}

// billList is the wire shape of the occurrences the bills bucket claimed.
func billList(rows []occurrenceRow) []BillRow {
	out := make([]BillRow, 0, len(rows))
	for _, row := range rows {
		if row.Bucket == domain.BucketBills {
			out = append(out, billRow(row))
		}
	}
	return out
}

func billRow(row occurrenceRow) BillRow {
	return BillRow{
		ID:          row.ID,
		Group:       row.Group,
		SeriesID:    row.SeriesID,
		Name:        row.Name,
		DueOn:       Date(row.DueOn),
		Amount:      row.Amount,
		IsFulfilled: row.IsFulfilled,
		TxnIDs:      store.NonNil(row.TxnIDs),
		IsExcluded:  row.IsExcluded,
	}
}

// bucketEntries names the rows behind one bucket's figure: the occurrences
// first (an unposted bill has no transaction to look up), then posted rows,
// ordered by sortPlanEntries.
func bucketEntries(
	key domain.BucketKey,
	bucket domain.Bucket,
	row *planMonthRow,
	view planView,
) (contributing, excluded []PlanEntry) {
	contributing, excluded = []PlanEntry{}, []PlanEntry{}

	// Claimed slots, so a posted bill is not listed twice.
	claimed := map[uuid.UUID]bool{}
	for _, occurrence := range view.Bills[view.Target] {
		if occurrence.Bucket != key {
			continue
		}
		for _, id := range occurrence.TxnIDs {
			claimed[id] = true
		}
		entry := occurrenceEntry(occurrence, view)
		if occurrence.IsExcluded {
			excluded = append(excluded, entry)
			continue
		}
		contributing = append(contributing, entry)
	}

	for _, id := range bucket.ContributingTxnIDs {
		txnID, err := store.ParseID(id)
		if err != nil || claimed[txnID] {
			continue
		}
		if entry, ok := postingEntry(txnID, key, view); ok {
			contributing = append(contributing, entry)
		}
	}

	// A bucket with no family carries no per-month exclusion list of its own.
	if fam, ok := bucketFamily(key); ok {
		for _, id := range row.Excluded[fam] {
			if claimed[id] {
				continue
			}
			if entry, ok := postingEntry(id, key, view); ok {
				excluded = append(excluded, entry)
			}
		}
	}
	sortPlanEntries(contributing)
	sortPlanEntries(excluded)
	return contributing, excluded
}

// sortPlanEntries puts one bucket's rows in the order money moves: earliest
// DueOn first (an occurrence's due date, or a posted row's effective date),
// then name and id so equal days are stable across requests. Applied once
// where the list is assembled, since the two halves arrive in different
// orders.
func sortPlanEntries(entries []PlanEntry) {
	sort.Slice(entries, func(i, j int) bool {
		a, b := entries[i], entries[j]
		if domain.Date(a.DueOn) != domain.Date(b.DueOn) {
			return domain.Date(a.DueOn).Before(domain.Date(b.DueOn))
		}
		if a.Name != b.Name {
			return a.Name < b.Name
		}
		return a.ID < b.ID
	})
}

func occurrenceEntry(row occurrenceRow, view planView) PlanEntry {
	seriesID := row.SeriesID.String()
	entry := PlanEntry{
		ID:         row.ID,
		SeriesID:   &seriesID,
		Name:       row.Name,
		DueOn:      Date(row.DueOn),
		Status:     occurrenceStatus(row.Kind, row.IsFulfilled, row.DueOn, view.AsOf),
		Amount:     row.Amount,
		IsTransfer: row.Kind.NetsToZero(),
	}
	if len(row.TxnIDs) > 0 {
		txnID := row.TxnIDs[0].String()
		entry.TxnID = &txnID
	}
	if category, ok := view.Categories[domain.ID(row.CategoryID.String())]; ok {
		entry.CategoryName = &category.Name
	}
	// Income is one undivided list; the bills bucket is the folded three.
	if row.Bucket == domain.BucketBills {
		group := row.Group
		entry.Group = &group
	}
	return entry
}

// postingEntry names one posted row. Absent means the id names a transaction
// outside the window the plan loaded.
func postingEntry(txnID uuid.UUID, key domain.BucketKey, view planView) (PlanEntry, bool) {
	posting, ok := view.Postings[domain.ID(txnID.String())]
	if !ok {
		return PlanEntry{}, false
	}
	id := txnID.String()
	accountID := string(posting.Account.ID)
	// Money that moved reads received or paid, series or not.
	status := entryPaid
	if key == domain.BucketIncome {
		status = entryReceived
	}
	entry := PlanEntry{
		ID:         id,
		TxnID:      &id,
		Name:       posting.Txn.DisplayPayee(),
		DueOn:      Date(posting.Txn.ReportingDate(domain.DateEffective)),
		Status:     status,
		Amount:     posting.Amount().Round(),
		AccountID:  &accountID,
		IsTransfer: posting.IsTransfer(),
		IsPadding:  posting.Txn.IsPadding(),
	}
	if names := rowCategoryNames(posting.Txn, view); names != "" {
		entry.CategoryName = &names
	}
	if key == domain.BucketGoals {
		group := "goal"
		entry.Group = &group
	}
	return entry, true
}

// rowCategoryNames is what a whole row is filed under, its splits' categories
// for a split row; empty when nothing is.
func rowCategoryNames(txn domain.Transaction, view planView) string {
	var names []string
	for _, id := range txn.CategoryIDs() {
		if name := categoryName(view, id, ""); name != "" && !slices.Contains(names, name) {
			names = append(names, name)
		}
	}
	return strings.Join(names, ", ")
}

// occurrenceStatus is the chip the row carries, wherever it is drawn. Income
// has no overdue state: a paycheck not yet arrived is upcoming.
func occurrenceStatus(kind domain.SeriesKind, isFulfilled bool, dueOn, asOf domain.Date) string {
	switch {
	case isFulfilled:
		return fulfilledStatus(kind)
	case kind != domain.SeriesIncome && dueOn.Before(asOf):
		return entryPastDue
	default:
		return entryUpcoming
	}
}

// otherSpendByCategory splits the Other Spend bucket for the chart, positive
// and largest first, grouped one level deep, from the bucket's own rows so
// the chart cannot disagree with the total.
//
// A leaf whose category has no parent is its own group, with no children.
func otherSpendByCategory(bucket domain.Bucket, month domain.SpendingPlanMonth, view planView) []OtherSpendSlice {
	type node struct {
		slice    *OtherSpendSlice
		children map[string]*OtherSpendSlice
		order    []string
	}
	var order []string
	groups := map[string]*node{}

	for _, part := range otherSpendEntries(bucket, month, view) {
		id := part.TxnID
		leafKey, leafName := "", "Uncategorized"
		groupKey, groupName := "", "Uncategorized"
		if part.HasCategory {
			leafKey = string(part.CategoryID)
			leafName = categoryName(view, part.CategoryID, "Uncategorized")
			groupKey, groupName = leafKey, leafName
			if parent := part.ParentID; parent != "" {
				groupKey = string(parent)
				groupName = categoryName(view, parent, leafName)
			}
		}

		group, seen := groups[groupKey]
		if !seen {
			group = &node{slice: newSlice(groupKey, groupName), children: map[string]*OtherSpendSlice{}}
			groups[groupKey] = group
			order = append(order, groupKey)
		}
		amount := part.Amount.Abs()
		group.slice.Spent = group.slice.Spent.Add(amount)
		group.slice.TxnIDs = append(group.slice.TxnIDs, string(id))

		// Only a leaf that is not its own group becomes a child. A group of
		// one would expand into a copy of itself.
		if leafKey == groupKey {
			continue
		}
		child, known := group.children[leafKey]
		if !known {
			child = newSlice(leafKey, leafName)
			group.children[leafKey] = child
			group.order = append(group.order, leafKey)
		}
		child.Spent = child.Spent.Add(amount)
		child.TxnIDs = append(child.TxnIDs, string(id))
	}

	out := make([]OtherSpendSlice, 0, len(order))
	for _, key := range order {
		group := groups[key]
		group.slice.Spent = group.slice.Spent.Round()
		for _, childKey := range group.order {
			child := group.children[childKey]
			child.Spent = child.Spent.Round()
			group.slice.Children = append(group.slice.Children, *child)
		}
		sortSlices(group.slice.Children)
		out = append(out, *group.slice)
	}
	sortSlices(out)
	return out
}

// otherSpendEntries is the bucket a part at a time. An open month computes
// parts, so a split row goes to the categories its splits name; a closed month
// is walked from the rows it froze.
func otherSpendEntries(
	bucket domain.Bucket, month domain.SpendingPlanMonth, view planView,
) []domain.SpendPart {
	if len(month.OtherSpendParts) > 0 {
		return month.OtherSpendParts
	}
	return frozenParts(bucket.ContributingTxnIDs, view)
}

// frozenParts is the fallback for a month that froze transaction ids and not
// parts. A split row is its splits, each under its own category: the parent
// carries none, and as one part it would be drawn as Uncategorized.
func frozenParts(ids []domain.ID, view planView) []domain.SpendPart {
	out := make([]domain.SpendPart, 0, len(ids))
	for _, id := range ids {
		posting, ok := view.Postings[id]
		if !ok {
			continue
		}
		for _, part := range domain.PartsOf(posting, view.Categories) {
			out = append(out, domain.SpendPartOf(part))
		}
	}
	return out
}

// newSlice starts an empty bubble. The id is a pointer because "uncategorized"
// is a real slice.
func newSlice(key, name string) *OtherSpendSlice {
	slice := &OtherSpendSlice{CategoryName: name, TxnIDs: []string{}, Children: []OtherSpendSlice{}}
	if key != "" {
		id := key
		slice.CategoryID = &id
	}
	return slice
}

func sortSlices(slices []OtherSpendSlice) {
	sort.SliceStable(slices, func(i, j int) bool {
		return slices[j].Spent.LessThan(slices[i].Spent)
	})
}

// categoryName resolves a parent id to its name, falling back to the child's
// own name rather than a blank label.
func categoryName(view planView, id domain.ID, fallback string) string {
	if category, ok := view.Categories[id]; ok && category.Name != "" {
		return category.Name
	}
	return fallback
}

func envelopeCategories(filter domain.Filter, categories map[domain.ID]domain.Category) []CategoryRef {
	out := []CategoryRef{}
	for _, item := range filter.Items {
		if item.Field != domain.FieldCategory {
			continue
		}
		for _, value := range item.Values {
			if id, err := uuid.Parse(value); err == nil {
				out = append(out, CategoryRef{ID: id, Name: categories[domain.ID(id.String())].Name})
			}
		}
	}
	return out
}

func stringList(ids []domain.ID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, string(id))
	}
	return out
}

// --- Entry ids ---------------------------------------------------------------

// entryID is one thing a bucket can exclude: a posted transaction, or a
// due-date slot nothing has posted against yet.
type entryID struct {
	TxnID    uuid.UUID
	SeriesID uuid.UUID
	DueOn    domain.Date
	IsSlot   bool
}

func (e entryID) storageID() uuid.UUID {
	if e.IsSlot {
		return slotExclusionID(e.SeriesID, e.DueOn)
	}
	return e.TxnID
}

// parseEntryID accepts both forms the Bills rows carry.
func parseEntryID(raw string) (entryID, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return entryID{}, errInvalid("missing", []string{"body", "entry_id"}, "entry_id is required")
	}
	seriesRaw, dueRaw, isComposite := strings.Cut(raw, ":")
	if !isComposite {
		id, err := uuid.Parse(raw)
		if err != nil {
			return entryID{}, errInvalid("uuid_parsing", []string{"body", "entry_id"},
				"entry_id must be a uuid or {series_id}:{due_on}")
		}
		return entryID{TxnID: id}, nil
	}

	seriesID, err := uuid.Parse(seriesRaw)
	if err != nil {
		return entryID{}, errInvalid("uuid_parsing", []string{"body", "entry_id"},
			"the series half of entry_id must be a uuid")
	}
	dueOn, err := parseDate(dueRaw)
	if err != nil {
		return entryID{}, errInvalid("date_parsing", []string{"body", "entry_id"}, "%s", err)
	}
	return entryID{SeriesID: seriesID, DueOn: dueOn, IsSlot: true}, nil
}

func addID(ids []uuid.UUID, id uuid.UUID) []uuid.UUID {
	for _, existing := range ids {
		if existing == id {
			return ids
		}
	}
	return append(ids, id)
}

func removeIDs(ids []uuid.UUID, drop []uuid.UUID) ([]uuid.UUID, bool) {
	gone := make(map[uuid.UUID]bool, len(drop))
	for _, id := range drop {
		gone[id] = true
	}
	out := make([]uuid.UUID, 0, len(ids))
	found := false
	for _, existing := range ids {
		if gone[existing] {
			found = true
			continue
		}
		out = append(out, existing)
	}
	return out, found
}

// exclusionAliases is every id the occurrence behind an entry may be stored
// under, so Include releases it whichever one Exclude wrote: the slot's derived
// uuid before anything posted, then the charge's id, or the first of two
// claimants.
func exclusionAliases(ctx context.Context, env *Env, sp auth.SpaceContext, entry entryID) ([]uuid.UUID, error) {
	out := []uuid.UUID{entry.storageID()}

	seriesID, dueOn := entry.SeriesID, entry.DueOn
	if !entry.IsSlot {
		row, err := env.DB.GetTransaction(ctx, sp.ID(), entry.TxnID)
		if err != nil {
			// A posting with no row left is still an exclusion worth clearing
			// under its own id.
			if isNotFound(err) {
				return out, nil
			}
			return nil, err
		}
		seriesID, dueOn = row.SeriesID, row.SeriesDueOn
	}
	if seriesID == uuid.Nil || dueOn.IsZero() {
		return out, nil
	}
	out = addID(out, slotExclusionID(seriesID, dueOn))

	// Deleted and estimate rows included: a deleted duplicate can be the one
	// carrying the exclusion.
	rows, err := env.DB.ListTransactions(ctx, sp.ID(), store.TransactionQuery{
		SeriesID: seriesID, HoldsASlot: true, IncludeDeleted: true, IncludeEstimates: true,
	})
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		if row.SeriesDueOn == dueOn {
			out = addID(out, row.ID)
		}
	}
	return out, nil
}

// --- The procedure wire ------------------------------------------------------

// monthOf reads the month a request names, "YYYY-MM".
func monthOf(raw string) (domain.Month, error) {
	month, err := parseMonth(raw)
	if err != nil {
		return domain.Month{}, errInvalid("date_parsing", []string{"path", "month"}, "%s", err)
	}
	return month, nil
}

// monthProto is the month monthResponse renders, which the assistant reads
// too, as the procedures answer it.
func monthProto(m SpendingPlanMonth) *agentifiv1.SpendingPlanMonth {
	contested := &structpb.Struct{Fields: make(map[string]*structpb.Value, len(m.ContestedTxnIDs))}
	for txnID, envelopeIDs := range m.ContestedTxnIDs {
		values := make([]*structpb.Value, 0, len(envelopeIDs))
		for _, id := range envelopeIDs {
			values = append(values, structpb.NewStringValue(id))
		}
		contested.Fields[txnID] = structpb.NewListValue(&structpb.ListValue{Values: values})
	}
	out := &agentifiv1.SpendingPlanMonth{
		Month:             m.Month,
		AsOf:              domain.Date(m.AsOf).String(),
		IsClosedOut:       m.IsClosedOut,
		ClosedOutAt:       datePtrProto(m.ClosedOutAt),
		Buckets:           make([]*agentifiv1.SpendingPlanBucket, 0, len(m.Buckets)),
		Bills:             make([]*agentifiv1.SpendingPlanBill, 0, len(m.Bills)),
		BillSubtotals:     make([]*agentifiv1.SpendingPlanBillSubtotal, 0, len(m.BillSubtotals)),
		Envelopes:         make([]*agentifiv1.SpendingPlanEnvelope, 0, len(m.Envelopes)),
		ContestedTxnIds:   contested,
		LeftThisMonth:     moneyProto(m.LeftThisMonth),
		PerDay:            moneyPtrProto(m.PerDay),
		DaysRemaining:     int32(m.DaysRemaining),
		MonthResult:       moneyProto(m.MonthResult),
		MonthResultPerDay: moneyPtrProto(m.MonthResultPerDay),
		DaysElapsed:       int32(m.DaysElapsed),
		Projection: &agentifiv1.SpendingPlanProjection{
			Type:         string(m.Projection.Type),
			WindowMonths: int32(m.Projection.WindowMonths),
			Buffer:       moneyProto(m.Projection.Buffer),
			StartOn:      datePtrProto(m.Projection.StartOn),
			EndOn:        datePtrProto(m.Projection.EndOn),
		},
		OtherSpendByCategory:   otherSpendProto(m.OtherSpendByCategory),
		OtherSpendToDate:       moneyProto(m.OtherSpendToDate),
		ProjectedOtherSpending: moneyProto(m.ProjectedOtherSpending),
		ProjectedLeft:          moneyProto(m.ProjectedLeft),
		ProjectedMonthResult:   moneyProto(m.ProjectedMonthResult),
	}
	for _, bucket := range m.Buckets {
		out.Buckets = append(out.Buckets, &agentifiv1.SpendingPlanBucket{
			Key:                string(bucket.Key),
			CalculatedAmount:   moneyProto(bucket.CalculatedAmount),
			PostedAmount:       moneyProto(bucket.PostedAmount),
			EffectiveAmount:    moneyProto(bucket.EffectiveAmount),
			OverwrittenAmount:  moneyPtrProto(bucket.OverwrittenAmount),
			ContributingTxnIds: bucket.ContributingTxnIDs,
			ExcludedEntryIds:   bucket.ExcludedEntryIDs,
			Contributing:       planEntriesProto(bucket.Contributing),
			Excluded:           planEntriesProto(bucket.Excluded),
		})
	}
	for _, bill := range m.Bills {
		out.Bills = append(out.Bills, &agentifiv1.SpendingPlanBill{
			Id: bill.ID, Group: bill.Group, SeriesId: bill.SeriesID.String(), Name: bill.Name,
			DueOn: domain.Date(bill.DueOn).String(), Amount: moneyProto(bill.Amount),
			IsFulfilled: bill.IsFulfilled, TxnIds: uuidStrings(bill.TxnIDs), IsExcluded: bill.IsExcluded,
		})
	}
	for _, subtotal := range m.BillSubtotals {
		out.BillSubtotals = append(out.BillSubtotals, &agentifiv1.SpendingPlanBillSubtotal{
			Group: subtotal.Group, Amount: moneyProto(subtotal.Amount),
		})
	}
	for _, envelope := range m.Envelopes {
		categories := make([]*agentifiv1.SpendingPlanEnvelopeCategory, 0, len(envelope.Categories))
		for _, category := range envelope.Categories {
			categories = append(categories, &agentifiv1.SpendingPlanEnvelopeCategory{
				Id: category.ID.String(), Name: category.Name,
			})
		}
		out.Envelopes = append(out.Envelopes, &agentifiv1.SpendingPlanEnvelope{
			Id:                      envelope.ID.String(),
			Name:                    envelope.Name,
			FilterId:                envelope.FilterID.String(),
			Categories:              categories,
			TargetAmount:            moneyProto(envelope.TargetAmount),
			OverwrittenTargetAmount: moneyPtrProto(envelope.OverwrittenTargetAmount),
			Target:                  moneyProto(envelope.Target),
			RolloverAmount:          moneyProto(envelope.RolloverIn),
			Spent:                   moneyProto(envelope.Spent),
			Budget:                  moneyProto(envelope.Budget),
			Available:               moneyProto(envelope.Available),
			PctUsed:                 envelope.PctUsed.String(),
			BarPct:                  envelope.BarPct.String(),
			State:                   string(envelope.State),
			Recurring:               envelope.Recurring,
			AutoReleaseRollover:     envelope.AutoReleaseRollover,
			TxnIds:                  uuidStrings(envelope.TxnIDs),
			Entries:                 planEntriesProto(envelope.Entries),
		})
	}
	return out
}

func planEntriesProto(entries []PlanEntry) []*agentifiv1.SpendingPlanEntry {
	out := make([]*agentifiv1.SpendingPlanEntry, 0, len(entries))
	for _, entry := range entries {
		out = append(out, &agentifiv1.SpendingPlanEntry{
			Id:           entry.ID,
			TxnId:        entry.TxnID,
			SeriesId:     entry.SeriesID,
			Name:         entry.Name,
			DueOn:        domain.Date(entry.DueOn).String(),
			Status:       entry.Status,
			CategoryName: entry.CategoryName,
			Amount:       moneyProto(entry.Amount),
			AccountId:    entry.AccountID,
			IsSplit:      entry.IsSplit,
			Group:        entry.Group,
			IsTransfer:   entry.IsTransfer,
			IsPadding:    entry.IsPadding,
		})
	}
	return out
}

func otherSpendProto(slices []OtherSpendSlice) []*agentifiv1.SpendingPlanOtherSpendSlice {
	out := make([]*agentifiv1.SpendingPlanOtherSpendSlice, 0, len(slices))
	for _, slice := range slices {
		out = append(out, &agentifiv1.SpendingPlanOtherSpendSlice{
			CategoryId:   slice.CategoryID,
			CategoryName: slice.CategoryName,
			Spent:        moneyProto(slice.Spent),
			TxnIds:       slice.TxnIDs,
			Children:     otherSpendProto(slice.Children),
		})
	}
	return out
}
