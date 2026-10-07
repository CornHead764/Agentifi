package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Bills & income: the series, their occurrences, and the cash-flow projection
// (calculations.md §8 and §9).
//
//   - `description` is matched and never rendered; `display_name` is rendered
//     and never matched. Every response carries `label`.
//   - Occurrences are expanded from the rule, never counted from a table: an
//     every-14-days series falls 26 times in most years and 27 in some.
//   - The cash-flow account selector filters one expansion, so the lines and
//     the total cannot disagree.

func init() {
	Register(Resource{Prefix: "/series", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listSeries)
		rt.Write(http.MethodPost, "/", createSeries)
		rt.Read(http.MethodGet, "/suggested", listSuggestedSeries)
		rt.Read(http.MethodGet, "/suggested/for/{transaction_id}", suggestSeriesForTransaction)
		rt.Write(http.MethodPost, "/suggested/{signature}/dismiss", dismissSuggestion)
		rt.Write(http.MethodDelete, "/suggested/{signature}/dismiss", restoreSuggestion)
		rt.Read(http.MethodGet, "/refunds", listRefunds)
		rt.Read(http.MethodGet, "/{series_id}", readSeries)
		rt.Read(http.MethodGet, "/{series_id}/history", seriesHistory)
		rt.Write(http.MethodPatch, "/{series_id}", updateSeries)
		rt.Write(http.MethodDelete, "/{series_id}", deleteSeries)
	}})

	Register(Resource{Prefix: "/occurrences", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listOccurrences)
		rt.Write(http.MethodPost, "/accept", acceptOccurrence)
		rt.Write(http.MethodPost, "/skip", skipOccurrence)
	}})

	Register(Resource{Prefix: "/cash-flow", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", readCashFlow)
	}})
}

// defaultHorizonDays is the "Next 30 days" the Overview tab opens on, used
// only when a request leaves an end open.
const defaultHorizonDays = 30

// maxHorizonDays bounds an expansion.
const maxHorizonDays = 1830

// skippedEstimate marks the tombstone row that records a skipped occurrence:
// a soft-deleted zero-amount row in the series slot (space_id, series_id,
// series_due_on), which stops the occurrence being projected again.
const skippedEstimate = store.SkippedEstimate

// --- Wire shapes -------------------------------------------------------------

// RecurrenceResponse is the RRULE as the frequency dropdown round-trips it.
type RecurrenceResponse struct {
	// Alias names the dropdown entry: EVERY_MONTH, TWICE_A_MONTH,
	// MULTIPLE_FIXED, EVERY_X_DAYS… It labels the rule; it never replaces it.
	Alias     domain.RecurrenceAlias `json:"alias"`
	Frequency domain.Frequency       `json:"frequency"`
	Interval  int                    `json:"interval"`
	// ByMonthDay is an array because "the 1st and the 15th" is one series.
	ByMonthDay []int    `json:"by_month_day"`
	ByDay      []string `json:"by_day"`
	// ByMonth is the months (1–12) the rule is active in; empty is every
	// month.
	ByMonth []int `json:"by_month"`
}

type SeriesResponse struct {
	ID         uuid.UUID  `json:"id"`
	AccountID  uuid.UUID  `json:"account_id"`
	CategoryID *uuid.UUID `json:"category_id"`
	Kind       string     `json:"kind"`

	// Description is the matching input and is never a label. DisplayName is
	// the label and is never matched. Label is what a display path reads.
	Description string  `json:"description"`
	DisplayName *string `json:"display_name"`
	Label       string  `json:"label"`

	Amount     domain.Money       `json:"amount"`
	Currency   string             `json:"currency"`
	Recurrence RecurrenceResponse `json:"recurrence"`

	StartOn   Date  `json:"start_on"`
	EndOn     *Date `json:"end_on"`
	NextDueOn *Date `json:"next_due_on"`
	// DueOn is the next occurrence's real date, one-off override applied.
	DueOn Date `json:"due_on"`

	OverrideNextDueOn  *Date         `json:"override_next_due_on"`
	OverrideNextAmount *domain.Money `json:"override_next_amount"`
	AutoAdjustDueOn    bool          `json:"auto_adjust_due_on"`
	ReminderDays       int           `json:"reminder_days"`

	MatchCriteria  string        `json:"match_criteria"`
	MatchAmountMin *domain.Money `json:"match_amount_min"`
	MatchAmountMax *domain.Money `json:"match_amount_max"`

	// The transaction template stamped onto each accepted occurrence.
	TagIDs []uuid.UUID   `json:"tag_ids"`
	Splits []SeriesSplit `json:"splits"`

	IsActive bool `json:"is_active"`

	// AnnualizedAmount is the amount times the occurrences the rule actually
	// has in the year.
	AnnualizedAmount   domain.Money `json:"annualized_amount"`
	OccurrencesPerYear int          `json:"occurrences_per_year"`
}

type RecurrenceWrite struct {
	Alias      domain.RecurrenceAlias `json:"alias"`
	Frequency  domain.Frequency       `json:"frequency"`
	Interval   *int                   `json:"interval"`
	ByMonthDay []int                  `json:"by_month_day"`
	ByDay      []string               `json:"by_day"`
	ByMonth    []int                  `json:"by_month"`
}

type SeriesCreate struct {
	AccountID  uuid.UUID  `json:"account_id"`
	CategoryID *uuid.UUID `json:"category_id"`
	Kind       string     `json:"kind"`

	Description string  `json:"description"`
	DisplayName *string `json:"display_name"`

	Amount     domain.Money    `json:"amount"`
	Currency   *string         `json:"currency"`
	Recurrence RecurrenceWrite `json:"recurrence"`

	StartOn Date  `json:"start_on"`
	EndOn   *Date `json:"end_on"`

	AutoAdjustDueOn bool `json:"auto_adjust_due_on"`
	ReminderDays    *int `json:"reminder_days"`

	MatchCriteria  *string       `json:"match_criteria"`
	MatchAmountMin *domain.Money `json:"match_amount_min"`
	MatchAmountMax *domain.Money `json:"match_amount_max"`

	TagIDs []uuid.UUID   `json:"tag_ids"`
	Splits []SeriesSplit `json:"splits"`

	IsActive *bool `json:"is_active"`
}

// SeriesUpdate is a partial edit. Renaming through display_name must not touch
// description, which is why they are separate fields here as well.
type SeriesUpdate struct {
	AccountID   Opt[uuid.UUID]       `json:"account_id"`
	CategoryID  Opt[uuid.UUID]       `json:"category_id"`
	Kind        Opt[string]          `json:"kind"`
	Description Opt[string]          `json:"description"`
	DisplayName Opt[string]          `json:"display_name"`
	Amount      Opt[domain.Money]    `json:"amount"`
	Currency    Opt[string]          `json:"currency"`
	Recurrence  Opt[RecurrenceWrite] `json:"recurrence"`

	StartOn Opt[Date] `json:"start_on"`
	EndOn   Opt[Date] `json:"end_on"`

	OverrideNextDueOn  Opt[Date]         `json:"override_next_due_on"`
	OverrideNextAmount Opt[domain.Money] `json:"override_next_amount"`
	AutoAdjustDueOn    Opt[bool]         `json:"auto_adjust_due_on"`
	ReminderDays       Opt[int]          `json:"reminder_days"`

	MatchCriteria  Opt[string]       `json:"match_criteria"`
	MatchAmountMin Opt[domain.Money] `json:"match_amount_min"`
	MatchAmountMax Opt[domain.Money] `json:"match_amount_max"`

	// Plain pointers rather than Opt: an absent list leaves the template
	// alone and an empty one clears it, so neither needs a third state.
	TagIDs *[]uuid.UUID   `json:"tag_ids"`
	Splits *[]SeriesSplit `json:"splits"`

	IsActive Opt[bool] `json:"is_active"`
}

// OccurrenceResponse is one expected instance of a series, or a pay-manually
// reminder: one statement, with no series and no account.
type OccurrenceResponse struct {
	SeriesID   *uuid.UUID `json:"series_id"`
	AccountID  *uuid.UUID `json:"account_id"`
	CategoryID *uuid.UUID `json:"category_id"`
	Kind       string     `json:"kind"`
	// Label is the display name. Nothing matches against it.
	Label  string       `json:"label"`
	DueOn  Date         `json:"due_on"`
	Amount domain.Money `json:"amount"`
	// PaysOn is the day the money actually leaves, for an autopaying bill;
	// null when unknown. Only the projection reads it.
	PaysOn *Date `json:"pays_on"`
	// Status is upcoming, past_due, paid or skipped.
	Status string `json:"status"`
	// TransactionID is the charge that fulfilled this slot, when one has.
	TransactionID *uuid.UUID `json:"transaction_id"`
	// Bill is the statement these figures came from, on the one slot it
	// speaks about. Null on a slot no bill speaks about: a bill is one cycle.
	Bill *OccurrenceBill `json:"bill"`
	// BillLink is the provider this series follows, on every occurrence, bill
	// or no bill, and the provider of a pay-manually reminder. Null for an
	// unlinked series.
	BillLink *OccurrenceBillLink `json:"bill_link"`
}

// OccurrenceBillLink is the provider behind a linked series and where it
// stands. `health` is ok, not_connected, needs_sign_in, challenge or failed.
// `autopay` false says the bill is paid by hand.
type OccurrenceBillLink struct {
	ConnectionID    uuid.UUID `json:"connection_id"`
	Biller          string    `json:"biller"`
	ConnectionLabel string    `json:"connection_label"`
	SubaccountLabel string    `json:"subaccount_label"`
	Health          string    `json:"health"`
	Autopay         bool      `json:"autopay"`
}

// OccurrenceBill is the statement behind an occurrence. Status is open or
// paid: paid is the provider's word, which settles no slot by itself.
type OccurrenceBill struct {
	ID         uuid.UUID    `json:"id"`
	AmountDue  domain.Money `json:"amount_due"`
	DueOn      Date         `json:"due_on"`
	Status     string       `json:"status"`
	Source     string       `json:"source"`
	FetchedAt  time.Time    `json:"fetched_at"`
	DocumentID *uuid.UUID   `json:"document_id"`
}

// SeriesHistoryRow is one month's slot of a series beside what actually
// happened in it: the charge that paid it, or the fact that nothing did.
type SeriesHistoryRow struct {
	DueOn Date `json:"due_on"`
	// Expected is what the series said the slot would cost.
	Expected domain.Money `json:"expected"`
	// Status is upcoming, past_due, skipped, or paid (received, for income).
	Status string `json:"status"`
	// OffSchedule marks a charge linked to the series that fills no slot the
	// rule lands on.
	OffSchedule bool                      `json:"off_schedule"`
	Transaction *SeriesHistoryTransaction `json:"transaction"`
}

type SeriesHistoryTransaction struct {
	ID            uuid.UUID    `json:"id"`
	AccountID     uuid.UUID    `json:"account_id"`
	AccountName   string       `json:"account_name"`
	Date          Date         `json:"date"`
	Amount        domain.Money `json:"amount"`
	Payee         string       `json:"payee"`
	StatementName string       `json:"statement_name"`
	CategoryID    *uuid.UUID   `json:"category_id"`
}

// SeriesHistory is a series' occurrences over a run of months, newest first.
type SeriesHistory struct {
	Series SeriesResponse     `json:"series"`
	From   string             `json:"from"`
	To     string             `json:"to"`
	Rows   []SeriesHistoryRow `json:"rows"`
	// PaidCount and AveragePaid summarise the rows a charge stands behind;
	// AveragePaid is null when there are none.
	PaidCount   int           `json:"paid_count"`
	AveragePaid *domain.Money `json:"average_paid"`
}

// OccurrenceSummary is the Overview tab's three figures.
type OccurrenceSummary struct {
	Income   domain.Money `json:"income"`
	Expenses domain.Money `json:"expenses"`
	Net      domain.Money `json:"net"`
	Count    int          `json:"count"`
	PastDue  int          `json:"past_due"`
}

type OccurrenceList struct {
	Window  WindowResponse       `json:"window"`
	Items   []OccurrenceResponse `json:"items"`
	Summary OccurrenceSummary    `json:"summary"`
}

type OccurrenceRef struct {
	SeriesID uuid.UUID `json:"series_id"`
	DueOn    Date      `json:"due_on"`
}

// OccurrenceAccept records that an occurrence was paid. Amount and date
// default to the occurrence's own.
type OccurrenceAccept struct {
	SeriesID uuid.UUID     `json:"series_id"`
	DueOn    Date          `json:"due_on"`
	Amount   *domain.Money `json:"amount"`
	Date     *Date         `json:"date"`
	Payee    *string       `json:"payee"`
	Notes    *string       `json:"notes"`
}

type CashFlowPointResponse struct {
	On      Date         `json:"on"`
	Balance domain.Money `json:"balance"`
}

// CashFlowLine is one account's line on the chart.
type CashFlowLine struct {
	AccountID       uuid.UUID               `json:"account_id"`
	Name            string                  `json:"name"`
	StartingBalance domain.Money            `json:"starting_balance"`
	Points          []CashFlowPointResponse `json:"points"`
	Lowest          *CashFlowPointResponse  `json:"lowest"`
	// FirstBelow is the low-balance warning: this projection crossing the
	// threshold, never a second calculation.
	FirstBelow *CashFlowPointResponse `json:"first_below"`
}

type CashFlowResponse struct {
	Window WindowResponse `json:"window"`
	// Threshold is the low-balance line the warning was computed against.
	Threshold domain.Money            `json:"threshold"`
	Accounts  []CashFlowLine          `json:"accounts"`
	Combined  []CashFlowPointResponse `json:"combined"`
	// Occurrences are the markers drawn on the lines, from the same expansion
	// the lines were projected from.
	Occurrences []OccurrenceResponse `json:"occurrences"`
}

// SuggestionResponse is a series the history implies but nobody has created.
type SuggestionResponse struct {
	Signature   string     `json:"signature"`
	AccountID   uuid.UUID  `json:"account_id"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Kind        string     `json:"kind"`
	Description string     `json:"description"`
	DisplayName string     `json:"display_name"`
	Label       string     `json:"label"`

	Amount     domain.Money       `json:"amount"`
	Currency   string             `json:"currency"`
	Recurrence RecurrenceResponse `json:"recurrence"`
	StartOn    Date               `json:"start_on"`

	Occurrences int     `json:"occurrences"`
	FirstSeen   Date    `json:"first_seen"`
	LastSeen    Date    `json:"last_seen"`
	Confidence  float64 `json:"confidence"`

	MatchCriteria  string        `json:"match_criteria"`
	MatchAmountMin *domain.Money `json:"match_amount_min"`
	MatchAmountMax *domain.Money `json:"match_amount_max"`

	TransactionIDs []uuid.UUID `json:"transaction_ids"`
}

// SeriesSplit is one line of the split a generated occurrence is carved into,
// the same shape a transaction's splits use. The amounts are written against
// the series' own amount and scaled to what an occurrence actually cost.
type SeriesSplit struct {
	Amount     domain.Money `json:"amount"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Memo       string       `json:"memo"`
	TagIDs     []uuid.UUID  `json:"tag_ids"`
}

// RefundResponse is one expected or completed refund.
type RefundResponse struct {
	Series SeriesResponse `json:"series"`
	// ExpectedOn is the refund's due date; SettledOn and TransactionID are set
	// once a credit has landed in the slot.
	ExpectedOn    Date       `json:"expected_on"`
	SettledOn     *Date      `json:"settled_on"`
	TransactionID *uuid.UUID `json:"transaction_id"`
}

type RefundList struct {
	Expected  []RefundResponse `json:"expected"`
	Completed []RefundResponse `json:"completed"`
}

// --- Series CRUD -------------------------------------------------------------

func listSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	filter, err := seriesFilterFromRequest(r)
	if err != nil {
		return err
	}
	rows, err := querySeries(r.Context(), env, sp, filter)
	if err != nil {
		return err
	}
	today := domain.DateOf(env.now())
	out := make([]SeriesResponse, 0, len(rows))
	for _, row := range rows {
		out = append(out, seriesResponse(row, today))
	}
	return writeJSON(w, http.StatusOK, out)
}

// seriesHistory is one series month by month: each slot the rule landed on,
// the charge that filled it, and any linked charge that fills no slot.
//
// ?to=YYYY-MM is the last month shown, this month by default; ?months= is how
// many months back, twelve by default.
func seriesHistory(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveSeries(r, env, sp)
	if err != nil {
		return err
	}
	today := domain.DateOf(env.now())
	last, given, err := queryMonth(r, "to")
	if err != nil {
		return err
	}
	if !given {
		last = domain.MonthOf(today)
	}
	months, err := queryInt(r, "months", 12, 1, 60)
	if err != nil {
		return err
	}
	first := last.Shift(1 - months)
	start, end := first.FirstDay(), last.LastDay()

	rows, err := env.DB.ListTransactions(r.Context(), sp.ID(),
		store.TransactionQuery{IncludeDeleted: true, IncludeEstimates: true, SeriesID: row.ID})
	if err != nil {
		return err
	}
	accounts, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return err
	}
	names := make(map[uuid.UUID]string, len(accounts))
	for _, account := range accounts {
		names[account.ID] = account.Name
	}
	bySlot := map[slotKey]store.Transaction{}
	for _, txn := range rows {
		if !txn.SeriesDueOn.IsZero() {
			holdSlot(bySlot, slotKey{txn.SeriesID, txn.SeriesDueOn}, txn)
		}
	}
	describe := func(txn store.Transaction) *SeriesHistoryTransaction {
		return &SeriesHistoryTransaction{
			ID: txn.ID, AccountID: txn.AccountID, AccountName: names[txn.AccountID],
			Date: Date(txn.Date), Amount: txn.Amount, Payee: txn.Payee,
			StatementName: txn.StatementName, CategoryID: pgconv.NullUUID(txn.CategoryID),
		}
	}

	out := SeriesHistory{
		Series: seriesResponse(row, today), From: first.String(), To: last.String(),
		Rows: []SeriesHistoryRow{},
	}
	slotted := map[uuid.UUID]bool{}
	paid := []domain.Money{}
	bills, err := seriesBills(r.Context(), env, sp, []domain.ID{domain.ID(row.ID.String())})
	if err != nil {
		return err
	}
	links, err := seriesLinks(r.Context(), env, sp, []domain.ID{domain.ID(row.ID.String())})
	if err != nil {
		return err
	}
	for _, one := range domain.ExpectedOccurrences([]domain.Series{service.ToDomainSeries(row)},
		start, end, nil, billConnects(bills), nil, nil) {
		item := occurrenceResponse(row, one, bySlot, today, bills, links)
		entry := SeriesHistoryRow{DueOn: item.DueOn, Expected: one.Amount, Status: item.Status}
		if item.TransactionID != nil {
			charge := bySlot[slotKey{row.ID, one.ScheduledOn}]
			entry.Transaction = describe(charge)
			// Same-day bills are rows of one slot, which one charge pays.
			if !slotted[charge.ID] && (item.Status == entryPaid || item.Status == entryReceived) {
				paid = append(paid, charge.Amount)
			}
			slotted[charge.ID] = true
		}
		out.Rows = append(out.Rows, entry)
	}
	for _, txn := range rows {
		if slotted[txn.ID] || txn.IsDeleted || txn.EstimateStatus != "" ||
			txn.Date.Before(start) || txn.Date.After(end) {
			continue
		}
		out.Rows = append(out.Rows, SeriesHistoryRow{
			DueOn: Date(txn.Date), Expected: row.Amount,
			Status: fulfilledStatus(domain.SeriesKind(row.Kind)), OffSchedule: true, Transaction: describe(txn),
		})
		paid = append(paid, txn.Amount)
	}
	sort.SliceStable(out.Rows, func(i, j int) bool {
		return domain.Date(out.Rows[j].DueOn).Before(domain.Date(out.Rows[i].DueOn))
	})
	out.PaidCount = len(paid)
	if mean, ok := domain.Total(paid...).DivInt(len(paid)); ok {
		rounded := mean.Round()
		out.AveragePaid = &rounded
	}
	return writeJSON(w, http.StatusOK, out)
}

func readSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveSeries(r, env, sp)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, seriesResponse(row, domain.DateOf(env.now())))
}

func createSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body SeriesCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Description) == "" {
		return errInvalid("missing", []string{"body", "description"},
			"description is required; it is what matching compares")
	}
	account, err := requireAccount(r.Context(), env, sp, body.AccountID)
	if err != nil {
		return err
	}
	categoryID := store.Deref(body.CategoryID, uuid.Nil)
	if err := checkCategory(r.Context(), env, sp, categoryID); err != nil {
		return err
	}
	kind, err := checkSeriesKind(body.Kind)
	if err != nil {
		return err
	}
	recurrence, err := buildRecurrence(body.Recurrence)
	if err != nil {
		return err
	}
	startOn, err := seriesStart(recurrence, domain.Date(body.StartOn), dateOrZero(body.EndOn))
	if err != nil {
		return err
	}
	criteria, err := checkMatchCriteria(store.Deref(body.MatchCriteria, string(domain.CriteriaAuto)),
		body.MatchAmountMin, body.MatchAmountMax)
	if err != nil {
		return err
	}

	tagIDs, splits, err := checkSeriesTemplate(
		r.Context(), env, sp, body.Amount, body.TagIDs, body.Splits)
	if err != nil {
		return err
	}
	encodedSplits, err := encodeSeriesSplits(splits)
	if err != nil {
		return err
	}

	write := store.SeriesWrite{
		AccountID:       account.ID,
		CategoryID:      categoryID,
		Kind:            string(kind),
		Description:     body.Description,
		DisplayName:     store.Deref(body.DisplayName, ""),
		Amount:          body.Amount,
		Currency:        store.Deref(body.Currency, account.Currency),
		Recurrence:      recurrence,
		StartOn:         startOn,
		EndOn:           dateOrZero(body.EndOn),
		AutoAdjustDueOn: body.AutoAdjustDueOn,
		ReminderDays:    store.Deref(body.ReminderDays, 3),
		MatchCriteria:   criteria,
		TemplateTagIDs:  store.NonNil(tagIDs),
		TemplateSplits:  encodedSplits,
		IsActive:        store.Deref(body.IsActive, true),
	}
	if body.MatchAmountMin != nil {
		write.MatchAmountMin, write.HasMatchMin = *body.MatchAmountMin, true
	}
	if body.MatchAmountMax != nil {
		write.MatchAmountMax, write.HasMatchMax = *body.MatchAmountMax, true
	}
	if err := env.DB.CreateSeries(r.Context(), sp.ID(), &write); err != nil {
		return err
	}

	row, err := service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), write.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, seriesResponse(row, domain.DateOf(env.now())))
}

func updateSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveSeries(r, env, sp)
	if err != nil {
		return err
	}
	var body SeriesUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	if body.AccountID.Cleared() {
		return errConflict("account_id cannot be cleared")
	}
	if body.AccountID.Present() {
		account, err := requireAccount(r.Context(), env, sp, body.AccountID.Value)
		if err != nil {
			return err
		}
		row.AccountID = account.ID
	}
	if body.CategoryID.Set {
		if err := checkCategory(r.Context(), env, sp, valueOrNil(body.CategoryID)); err != nil {
			return err
		}
		applyNullable(body.CategoryID, &row.CategoryID)
	}
	if body.Kind.Present() {
		kind, err := checkSeriesKind(body.Kind.Value)
		if err != nil {
			return err
		}
		row.Kind = string(kind)
	}
	// description and display_name move independently: renaming a series for
	// the eye must not stop it matching the bank's wording.
	if err := applyRequired("description", body.Description, &row.Description); err != nil {
		return err
	}
	applyNullable(body.DisplayName, &row.DisplayName)
	if err := applyRequired("amount", body.Amount, &row.Amount); err != nil {
		return err
	}
	if err := applyRequired("currency", body.Currency, &row.Currency); err != nil {
		return err
	}
	startBefore := row.StartOn
	if err := applyRequired("start_on", body.StartOn, (*Date)(&row.StartOn)); err != nil {
		return err
	}
	applyNullable(body.EndOn, (*Date)(&row.EndOn))
	applyNullable(body.OverrideNextDueOn, (*Date)(&row.OverrideNextDueOn))
	applyNullableMoney(body.OverrideNextAmount, &row.OverrideNextAmount, &row.HasOverrideNextAmount)
	if err := applyRequired("auto_adjust_due_on", body.AutoAdjustDueOn, &row.AutoAdjustDueOn); err != nil {
		return err
	}
	if err := applyRequired("is_active", body.IsActive, &row.IsActive); err != nil {
		return err
	}
	applyNullableMoney(body.MatchAmountMin, &row.MatchAmountMin, &row.HasMatchMin)
	applyNullableMoney(body.MatchAmountMax, &row.MatchAmountMax, &row.HasMatchMax)
	if body.MatchCriteria.Present() {
		row.MatchCriteria = body.MatchCriteria.Value
	}
	if _, err := checkMatchCriteria(row.MatchCriteria,
		store.PtrIf(row.MatchAmountMin, row.HasMatchMin),
		store.PtrIf(row.MatchAmountMax, row.HasMatchMax)); err != nil {
		return err
	}

	recurrenceBefore := service.ToRecurrence(row)
	recurrence := recurrenceBefore
	if body.Recurrence.Present() {
		built, err := buildRecurrence(body.Recurrence.Value)
		if err != nil {
			return err
		}
		recurrence = built
	}
	if row.StartOn, err = seriesStart(recurrence, row.StartOn, row.EndOn); err != nil {
		return err
	}
	scheduleMoved := row.StartOn != startBefore || !sameRecurrence(recurrenceBefore, recurrence)
	reminderDays := 0
	if body.ReminderDays.Present() {
		reminderDays = body.ReminderDays.Value
	}

	// Validated against the amount this request may have just changed.
	tagIDs, splits := row.TemplateTagIDs, decodeSeriesSplits(row.TemplateSplits)
	if body.TagIDs != nil || body.Splits != nil {
		if body.TagIDs != nil {
			tagIDs = *body.TagIDs
		}
		if body.Splits != nil {
			splits = *body.Splits
		}
		tagIDs, splits, err = checkSeriesTemplate(r.Context(), env, sp, row.Amount, tagIDs, splits)
		if err != nil {
			return err
		}
	}
	encodedSplits, err := encodeSeriesSplits(splits)
	if err != nil {
		return err
	}

	write := store.SeriesWrite{
		ID:                    row.ID,
		AccountID:             row.AccountID,
		CategoryID:            row.CategoryID,
		Kind:                  row.Kind,
		Description:           row.Description,
		DisplayName:           row.DisplayName,
		Amount:                row.Amount,
		Currency:              row.Currency,
		Recurrence:            recurrence,
		StartOn:               row.StartOn,
		EndOn:                 row.EndOn,
		OverrideNextDueOn:     row.OverrideNextDueOn,
		OverrideNextAmount:    row.OverrideNextAmount,
		HasOverrideNextAmount: row.HasOverrideNextAmount,
		AutoAdjustDueOn:       row.AutoAdjustDueOn,
		ReminderDays:          reminderDays,
		MatchCriteria:         row.MatchCriteria,
		MatchAmountMin:        row.MatchAmountMin,
		HasMatchMin:           row.HasMatchMin,
		MatchAmountMax:        row.MatchAmountMax,
		HasMatchMax:           row.HasMatchMax,
		TemplateTagIDs:        store.NonNil(tagIDs),
		TemplateSplits:        encodedSplits,
		IsActive:              row.IsActive,
	}
	if scheduleMoved {
		write.NextDueOn = repointedSchedule(row.NextDueOn, startBefore, domain.Series{
			Recurrence: recurrence, StartOn: row.StartOn, EndOn: row.EndOn,
		})
		write.SetNextDueOn = true
	}
	if err := env.DB.UpdateSeries(r.Context(), sp.ID(), write, body.ReminderDays.Present()); err != nil {
		return err
	}

	updated, err := service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), row.ID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, seriesResponse(updated, domain.DateOf(env.now())))
}

// deleteSeries soft-deletes. The charges it already matched keep their
// series_id, so a deleted series that is restored still owns its history.
func deleteSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveSeries(r, env, sp)
	if err != nil {
		return err
	}
	if err := env.DB.DeleteSeries(r.Context(), sp.ID(), row.ID); err != nil {
		return err
	}
	return writeNoContent(w)
}

// dismissSuggestion records that the household does not want this proposal.
// Idempotent. The signature digests the account, direction, currency, kind and
// matched wording, so the dismissal survives another charge joining the group.
func dismissSuggestion(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	signature, err := suggestionSignature(r)
	if err != nil {
		return err
	}
	if err := env.DB.DismissSuggestion(r.Context(), sp.ID(), signature); err != nil {
		return err
	}
	return writeNoContent(w)
}

// restoreSuggestion undoes a dismissal, so the sweep may propose it again.
func restoreSuggestion(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	signature, err := suggestionSignature(r)
	if err != nil {
		return err
	}
	restored, err := env.DB.RestoreSuggestion(r.Context(), sp.ID(), signature)
	if err != nil {
		return err
	}
	if !restored {
		return errNotFound("Dismissed suggestion")
	}
	return writeNoContent(w)
}

// suggestionSignature reads the path parameter and checks it is a hex SHA-256,
// keeping arbitrary text out of a column only ever compared against digests.
func suggestionSignature(r *http.Request) (string, error) {
	raw := strings.TrimSpace(chi.URLParam(r, "signature"))
	if len(raw) != 64 {
		return "", errInvalid("invalid", []string{"path", "signature"},
			"a suggestion signature is a 64-character hex digest")
	}
	for _, c := range raw {
		if !strings.ContainsRune("0123456789abcdef", c) {
			return "", errInvalid("invalid", []string{"path", "signature"},
				"a suggestion signature is a 64-character hex digest")
		}
	}
	return raw, nil
}

func listSuggestedSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	limit, err := queryInt(r, "limit", 0, 1, 200)
	if err != nil {
		return err
	}
	// Without this the sweep re-proposes a dismissed pattern on every read.
	dismissed, err := env.DB.ListDismissedSuggestions(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	skip := make(map[string]bool, len(dismissed))
	for _, signature := range dismissed {
		skip[signature] = true
	}

	// ?dismissed=true lists the dismissed patterns still present. A dismissal
	// is a signature, so the sweep has to run to find them.
	showDismissed, _, err := queryBool(r, "dismissed")
	if err != nil {
		return err
	}
	query := service.SuggestionQuery{Today: domain.DateOf(env.now()), Limit: limit}
	if showDismissed {
		query.Limit = 0
	} else {
		query.Dismissed = skip
	}
	found, err := service.NewSuggestions(env.DB).GetSuggestions(r.Context(), sp.ID(), query)
	if err != nil {
		return err
	}

	out := make([]SuggestionResponse, 0, len(found))
	for _, one := range found {
		if showDismissed && !skip[one.Signature] {
			continue
		}
		out = append(out, suggestionResponse(one))
		if showDismissed && limit > 0 && len(out) >= limit {
			break
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func suggestionResponse(one service.RecurringSuggestion) SuggestionResponse {
	low, high, bounded := one.Tolerance.Bounds(one.Amount, nil)
	return SuggestionResponse{
		Signature:      one.Signature,
		AccountID:      one.AccountID,
		CategoryID:     pgconv.NullUUID(one.CategoryID),
		Kind:           string(one.Kind),
		Description:    one.Description,
		DisplayName:    one.DisplayName,
		Label:          domain.Series{DisplayName: one.DisplayName, Description: one.Description}.Label(),
		Amount:         one.Amount,
		Currency:       one.Currency,
		Recurrence:     recurrenceResponse(one.Recurrence),
		StartOn:        Date(one.StartOn),
		Occurrences:    one.Occurrences,
		FirstSeen:      Date(one.FirstSeen),
		LastSeen:       Date(one.LastSeen),
		Confidence:     one.Confidence,
		MatchCriteria:  string(one.Tolerance.Criteria),
		MatchAmountMin: store.PtrIf(low, bounded),
		MatchAmountMax: store.PtrIf(high, bounded),
		TransactionIDs: store.NonNil(one.TransactionIDs),
	}
}

// suggestSeriesForTransaction answers "what would a series for this row look
// like", so *Create a series* opens on a cadence read out of the history.
// A row already in a series, or with no recognisable wording, is a 404; the
// client then seeds its editor from the transaction alone.
func suggestSeriesForTransaction(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "transaction_id", "Transaction")
	if err != nil {
		return err
	}
	one, ok, err := service.NewSuggestions(env.DB).SuggestForTransaction(
		r.Context(), sp.ID(), id, domain.DateOf(env.now()))
	if err != nil {
		return err
	}
	if !ok {
		return errNotFound("Suggestion")
	}
	return writeJSON(w, http.StatusOK, suggestionResponse(one))
}

// listRefunds splits the refund series into the tab's two sections. Completed
// means a charge has landed in the slot; expected means none has.
func listRefunds(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := querySeries(r.Context(), env, sp, seriesFilter{Kind: string(domain.SeriesRefund)})
	if err != nil {
		return err
	}
	_, _, bySlot, err := loadSeriesSlots(r.Context(), env, sp, domain.DateOf(env.now()))
	if err != nil {
		return err
	}

	today := domain.DateOf(env.now())
	out := RefundList{Expected: []RefundResponse{}, Completed: []RefundResponse{}}
	for _, row := range rows {
		series := service.ToDomainSeries(row)
		dueOn := series.DueOn()
		refund := RefundResponse{Series: seriesResponse(row, today), ExpectedOn: Date(dueOn)}
		// The slot, not the day the override moved it to: the credit was filed
		// under the date the rule landed on.
		if charge, settled := bySlot[slotKey{row.ID, series.ScheduledDueOn()}]; settled && !charge.IsDeleted {
			date := Date(charge.Date)
			refund.SettledOn, refund.TransactionID = &date, &charge.ID
			out.Completed = append(out.Completed, refund)
			continue
		}
		out.Expected = append(out.Expected, refund)
	}
	return writeJSON(w, http.StatusOK, out)
}

// --- Occurrences -------------------------------------------------------------

func listOccurrences(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	start, end := horizon(window, domain.DateOf(env.now()))

	filter, err := seriesFilterFromRequest(r)
	if err != nil {
		return err
	}
	rows, err := querySeries(r.Context(), env, sp, filter)
	if err != nil {
		return err
	}
	// Settled rather than claimed: a bill the provider has already written a
	// forecast row for is still upcoming.
	_, settled, bySlot, err := loadSeriesSlots(r.Context(), env, sp, domain.DateOf(env.now()))
	if err != nil {
		return err
	}

	includeFulfilled, _, err := queryBool(r, "include_fulfilled")
	if err != nil {
		return err
	}
	expected := settled
	if includeFulfilled {
		expected = nil
	}

	series := make([]domain.Series, 0, len(rows))
	meta := make(map[domain.ID]service.SeriesRow, len(rows))
	for _, row := range rows {
		one := service.ToDomainSeries(row)
		series = append(series, one)
		meta[one.ID] = row
	}

	bills, err := seriesBills(r.Context(), env, sp, seriesIDs(series))
	if err != nil {
		return err
	}
	links, err := seriesLinks(r.Context(), env, sp, seriesIDs(series))
	if err != nil {
		return err
	}
	manual, err := payManually(r.Context(), env, sp, filter)
	if err != nil {
		return err
	}

	today := domain.DateOf(env.now())
	items := make([]OccurrenceResponse, 0)
	summary := OccurrenceSummary{}
	occurrences := domain.ExpectedOccurrences(
		series, start, end, expected, billConnects(bills), service.ManualBills(manual), nil)
	byBill := manualByBill(manual)
	for _, one := range occurrences {
		item := manualOccurrenceResponse(one, byBill, today)
		if one.SeriesID != "" {
			item = occurrenceResponse(meta[one.SeriesID], one, bySlot, today, bills, links)
		}
		items = append(items, item)
		if item.Status == entryPastDue {
			summary.PastDue++
		}
	}

	totals := domain.SummarizeOccurrences(occurrences)
	summary.Income = totals.Income
	summary.Expenses = totals.Expenses
	summary.Net = totals.Net
	summary.Count = len(items)

	return writeJSON(w, http.StatusOK, OccurrenceList{
		Window:  windowResponse(window),
		Items:   items,
		Summary: summary,
	})
}

// reminderDaysBack is how far before today a reminder can still be past due,
// matching the register's Reminders strip.
const reminderDaysBack = 60

// reminder is one occurrence still to be paid or received. Series is the zero
// value on a pay-manually reminder.
type reminder struct {
	domain.Occurrence
	Series domain.Series
	Label  string
	Status string
}

// loadReminders is what the Reminders strip lists, for [from, to]: the
// occurrences that are past_due or upcoming, oldest first. A slot a provider
// forecast row holds is still a reminder; a paid or skipped one is not.
func loadReminders(
	ctx context.Context, env *Env, sp auth.SpaceContext, from, to domain.Date,
) ([]reminder, error) {
	rows, err := querySeries(ctx, env, sp, seriesFilter{})
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(env.now())
	_, settled, bySlot, err := loadSeriesSlots(ctx, env, sp, today)
	if err != nil {
		return nil, err
	}
	series := make([]domain.Series, 0, len(rows))
	byID := make(map[domain.ID]domain.Series, len(rows))
	meta := make(map[domain.ID]service.SeriesRow, len(rows))
	for _, row := range rows {
		one := service.ToDomainSeries(row)
		series = append(series, one)
		byID[one.ID] = one
		meta[one.ID] = row
	}
	bills, err := seriesBills(ctx, env, sp, seriesIDs(series))
	if err != nil {
		return nil, err
	}
	manual, err := payManually(ctx, env, sp, seriesFilter{})
	if err != nil {
		return nil, err
	}

	out := make([]reminder, 0)
	byBill := manualByBill(manual)
	for _, one := range domain.ExpectedOccurrences(
		series, from, to, settled, billConnects(bills), service.ManualBills(manual), nil) {
		item := manualOccurrenceResponse(one, byBill, today)
		if one.SeriesID != "" {
			item = occurrenceResponse(meta[one.SeriesID], one, bySlot, today, bills, nil)
		}
		if item.Status != entryPastDue && item.Status != entryUpcoming {
			continue
		}
		out = append(out, reminder{Occurrence: one, Series: byID[one.SeriesID], Label: item.Label, Status: item.Status})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DueOn.Before(out[j].DueOn) })
	return out, nil
}

// acceptOccurrence records the charge that pays an occurrence and advances the
// schedule pointer past it. Back-filling an older occurrence leaves the
// pointer alone, or a payment still to come would be skipped.
func acceptOccurrence(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body OccurrenceAccept
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	row, series, slot, err := occurrenceTarget(r.Context(), env, sp, body.SeriesID, domain.Date(body.DueOn))
	if err != nil {
		return err
	}

	// One charge per slot, so a double click or a retried POST cannot move
	// the balance and the plan twice.
	settled, taken, err := env.DB.SeriesSlotSettled(r.Context(), sp.ID(), row.ID, slot)
	if err != nil {
		return err
	}
	if taken {
		return errSlotTaken(settled)
	}

	bills, err := seriesBillConnects(r.Context(), env, sp, series.ID)
	if err != nil {
		return err
	}
	amount := domain.OccurrenceAmount(series, slot, bills)
	if body.Amount != nil {
		amount = *body.Amount
	}

	charge := &store.Transaction{
		AccountID:     row.AccountID,
		Date:          dateOr(body.Date, domain.Date(body.DueOn)),
		Amount:        amount,
		Currency:      row.Currency,
		StatementName: row.Description,
		Payee:         store.Deref(body.Payee, service.ToDomainSeries(row).Label()),
		Notes:         store.Deref(body.Notes, ""),
		CategoryID:    row.CategoryID,
		Source:        domain.SourceManual,
		SeriesID:      row.ID,
		SeriesDueOn:   slot,
		AcceptedOn:    domain.DateOf(env.now()),
		IsReviewed:    true,
		TagIDs:        row.TemplateTagIDs,
		Splits: storeSplits(
			scaleSeriesSplits(decodeSeriesSplits(row.TemplateSplits), row.Amount, amount)),
	}
	if err := env.DB.CreateTransaction(r.Context(), sp.ID(), charge); err != nil {
		return err
	}
	if err := service.NewSeriesMatcher(env.DB).AdvancePast(r.Context(), sp.ID(), row, slot); err != nil {
		return err
	}
	if err := recomputeRunningBalances(r.Context(), env, sp, charge.AccountID); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, transactionResponse(*charge))
}

// skipOccurrence marks a slot as handled without a charge.
func skipOccurrence(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body OccurrenceRef
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	row, _, slot, err := occurrenceTarget(r.Context(), env, sp, body.SeriesID, domain.Date(body.DueOn))
	if err != nil {
		return err
	}

	// One claim per slot: two tombstones would read as two skips.
	settled, taken, err := env.DB.SeriesSlotSettled(r.Context(), sp.ID(), row.ID, slot)
	if err != nil {
		return err
	}
	if taken {
		return errSlotTaken(settled)
	}

	tombstone := &store.Transaction{
		AccountID:      row.AccountID,
		Date:           domain.Date(body.DueOn),
		Amount:         domain.Zero,
		Currency:       row.Currency,
		StatementName:  row.Description,
		Payee:          service.ToDomainSeries(row).Label(),
		Source:         domain.SourceManual,
		SeriesID:       row.ID,
		SeriesDueOn:    slot,
		EstimateStatus: skippedEstimate,
		IsDeleted:      true,
	}
	if err := env.DB.CreateTransaction(r.Context(), sp.ID(), tombstone); err != nil {
		return err
	}
	if err := service.NewSeriesMatcher(env.DB).AdvancePast(r.Context(), sp.ID(), row, slot); err != nil {
		return err
	}
	return writeNoContent(w)
}

// --- Cash flow ---------------------------------------------------------------

func readCashFlow(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	window, err := WindowFromRequest(r)
	if err != nil {
		return err
	}
	start, end := horizon(window, domain.DateOf(env.now()))

	threshold, _, err := queryMoney(r, "threshold")
	if err != nil {
		return err
	}

	wanted, err := queryAccountFilter(r)
	if err != nil {
		return err
	}
	var accounts []store.Account
	if !wanted.selectsNothing() {
		accounts, err = env.DB.ListAccounts(r.Context(), sp.ID(), wanted.narrow(store.AccountQuery{}))
		if err != nil {
			return err
		}
	}
	postings, err := postingsByAccount(r.Context(), env, sp, accounts)
	if err != nil {
		return err
	}

	// The balance the day before the window opens, not the account's ledger
	// total: rows dated inside the window (an early acceptance, a provider
	// forecast) are what the projection lays out day by day, so starting from
	// a figure that contains them counts them twice.
	//
	// The ledger's figure, not the history's: an account linked today has no
	// history yesterday.
	today := domain.DateOf(env.now())
	opening := start.AddDays(-1)
	balances := make(map[domain.ID]domain.Money, len(accounts))
	names := make(map[domain.ID]store.Account, len(accounts))
	for _, account := range accounts {
		id := domain.ID(account.ID.String())
		balances[id] = domain.LedgerBalanceAsOf(
			store.DomainAccount(account), postings[account.ID], opening, domain.DatePosted)
		names[id] = account
	}

	rows, err := querySeries(r.Context(), env, sp, seriesFilter{ActiveOnly: true})
	if err != nil {
		return err
	}
	// Settled, matching the balance above: a slot whose only row is dated in
	// the future still has to be projected.
	_, settled, bySlot, err := loadSeriesSlots(r.Context(), env, sp, today)
	if err != nil {
		return err
	}
	series := make([]domain.Series, 0, len(rows))
	meta := make(map[domain.ID]service.SeriesRow, len(rows))
	for _, row := range rows {
		one := service.ToDomainSeries(row)
		series = append(series, one)
		meta[one.ID] = row
	}

	bills, err := seriesBills(r.Context(), env, sp, seriesIDs(series))
	if err != nil {
		return err
	}

	// One expansion, filtered per account, so a line cannot disagree with the
	// total.
	projections := domain.ProjectAccounts(
		balances, series, start, end, settled, billConnects(bills))

	lines := make([]CashFlowLine, 0, len(projections))
	combined := map[domain.Date]domain.Money{}
	for id, projection := range projections {
		account := names[id]
		points := make([]CashFlowPointResponse, 0, len(projection))
		for _, point := range projection {
			points = append(points, CashFlowPointResponse{On: Date(point.On), Balance: point.Balance})
			combined[point.On] = combined[point.On].Add(point.Balance)
		}
		line := CashFlowLine{
			AccountID:       account.ID,
			Name:            account.Name,
			StartingBalance: balances[id],
			Points:          points,
		}
		if lowest, found := projection.Lowest(); found {
			line.Lowest = &CashFlowPointResponse{On: Date(lowest.On), Balance: lowest.Balance}
		}
		if below, found := projection.FirstBelow(threshold); found {
			line.FirstBelow = &CashFlowPointResponse{On: Date(below.On), Balance: below.Balance}
		}
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].Name < lines[j].Name })

	total := make([]CashFlowPointResponse, 0, len(combined))
	for _, on := range domain.DateRange(start, end) {
		if balance, present := combined[on]; present {
			total = append(total, CashFlowPointResponse{On: Date(on), Balance: balance.Round()})
		}
	}

	onlyWanted := map[domain.ID]bool{}
	for id := range balances {
		onlyWanted[id] = true
	}
	markers := make([]OccurrenceResponse, 0)
	for _, one := range domain.ExpectedOccurrences(
		series, start, end, settled, billConnects(bills), nil, onlyWanted) {
		markers = append(markers, occurrenceResponse(meta[one.SeriesID], one, bySlot, today, bills, nil))
	}

	return writeJSON(w, http.StatusOK, CashFlowResponse{
		Window:      windowResponse(window),
		Threshold:   threshold,
		Accounts:    lines,
		Combined:    total,
		Occurrences: markers,
	})
}

// --- Orchestration -----------------------------------------------------------

type seriesFilter struct {
	AccountID  uuid.UUID
	Kind       string
	ActiveOnly bool
	Search     string
}

func seriesFilterFromRequest(r *http.Request) (seriesFilter, error) {
	out := seriesFilter{
		Search: strings.TrimSpace(r.URL.Query().Get("search")),
		Kind:   strings.TrimSpace(r.URL.Query().Get("kind")),
	}
	id, given, err := queryUUID(r, "account_id")
	if err != nil {
		return seriesFilter{}, err
	}
	if given {
		out.AccountID = id
	}
	active, given, err := queryBool(r, "is_active")
	if err != nil {
		return seriesFilter{}, err
	}
	out.ActiveOnly = given && active
	return out, nil
}

func querySeries(
	ctx context.Context, env *Env, sp auth.SpaceContext, filter seriesFilter,
) ([]service.SeriesRow, error) {
	sql, args := store.SeriesListSQL(sp.ID(), store.SeriesQuery{
		AccountID:  filter.AccountID,
		Kind:       filter.Kind,
		ActiveOnly: filter.ActiveOnly,
	})
	rows, err := service.NewSeriesMatcher(env.DB).LoadSeriesRows(ctx, sql, args...)
	if err != nil {
		return nil, err
	}
	if filter.Search == "" {
		return rows, nil
	}
	needle := strings.ToLower(filter.Search)
	kept := rows[:0]
	for _, row := range rows {
		if strings.Contains(strings.ToLower(row.Description), needle) ||
			strings.Contains(strings.ToLower(row.DisplayName), needle) {
			kept = append(kept, row)
		}
	}
	return kept, nil
}

func liveSeries(r *http.Request, env *Env, sp auth.SpaceContext) (service.SeriesRow, error) {
	id, err := pathUUID(r, "series_id", "Series")
	if err != nil {
		return service.SeriesRow{}, err
	}
	row, err := service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), id)
	if err != nil {
		if isNotFound(err) || strings.Contains(err.Error(), "no rows") {
			return service.SeriesRow{}, errNotFound("Series")
		}
		return service.SeriesRow{}, err
	}
	if row.IsDeleted {
		return service.SeriesRow{}, errNotFound("Series")
	}
	return row, nil
}

// slotKey is one occurrence of one series — the slot, not just the series, so
// two payments in one month cannot both claim the same due date.
type slotKey struct {
	SeriesID uuid.UUID
	DueOn    domain.Date
}

// loadSeriesSlots is every occurrence a row has claimed, in two senses.
//
// Deleted rows are included because a skip is a deleted row in the slot.
//
// claimed is every slot with a row in it, and is what a projection must use:
// the amount is already in the ledger, whether posted, accepted early, or a
// provider's forecast, so projecting it too would charge twice.
//
// settled is the smaller set the Bills screen hides: a posted payment or a
// skip. A row dated in the future or a forecast settles nothing; treating one
// as settled marks next month's bill paid. The rule is domain.SettledSlots,
// shared with the alert sweep.
func loadSeriesSlots(
	ctx context.Context, env *Env, sp auth.SpaceContext, today domain.Date,
) (claimed, settled map[domain.ID]map[domain.Date]bool, bySlot map[slotKey]store.Transaction, err error) {
	rows, err := env.DB.ListTransactions(ctx, sp.ID(), store.TransactionQuery{
		HoldsASlot: true, IncludeDeleted: true, IncludeEstimates: true,
	})
	if err != nil {
		return nil, nil, nil, err
	}
	claimed, settled = domain.SettledSlots(store.SlotHolders(rows), today)
	bySlot = map[slotKey]store.Transaction{}
	for _, row := range rows {
		holdSlot(bySlot, slotKey{row.SeriesID, row.SeriesDueOn}, row)
	}
	return claimed, settled, bySlot, nil
}

// holdSlot keeps the best of the rows that hold one occurrence (a forecast and
// the charge that filled it can share a slot), so the answer does not depend
// on query order.
func holdSlot(bySlot map[slotKey]store.Transaction, key slotKey, row store.Transaction) {
	if held, seen := bySlot[key]; !seen || slotRank(row) > slotRank(held) {
		bySlot[key] = row
	}
}

// slotRank orders the rows that can hold one occurrence, best answer highest:
// a charge, then a skip tombstone, then a forecast.
func slotRank(row store.Transaction) int {
	switch {
	case store.CountsTowardBalance(row):
		return 2
	case row.IsDeleted:
		return 1
	default:
		return 0
	}
}

// occurrenceTarget resolves the series a slot names, refusing one that is not
// in this space and one whose rule never fires on that day. slot is the
// schedule date, not the (possibly moved) date the client was shown.
func occurrenceTarget(
	ctx context.Context, env *Env, sp auth.SpaceContext, seriesID uuid.UUID, dueOn domain.Date,
) (service.SeriesRow, domain.Series, domain.Date, error) {
	row, err := service.NewSeriesMatcher(env.DB).GetSeries(ctx, sp.ID(), seriesID)
	if err != nil {
		if isNotFound(err) || strings.Contains(err.Error(), "no rows") {
			return service.SeriesRow{}, domain.Series{}, domain.Date{}, errNotFound("Series")
		}
		return service.SeriesRow{}, domain.Series{}, domain.Date{}, err
	}
	if row.IsDeleted {
		return service.SeriesRow{}, domain.Series{}, domain.Date{}, errNotFound("Series")
	}
	series := service.ToDomainSeries(row)
	bills, err := seriesBillConnects(ctx, env, sp, series.ID)
	if err != nil {
		return service.SeriesRow{}, domain.Series{}, domain.Date{}, err
	}
	for _, slot := range domain.OccurrenceSlots(series, dueOn, dueOn, bills) {
		if slot.DueOn == dueOn {
			return row, series, slot.ScheduledOn, nil
		}
	}
	return service.SeriesRow{}, domain.Series{}, domain.Date{},
		errConflict("%s has no occurrence due on %s", series.Label(), dueOn)
}

// repointedSchedule is where the pointer stands after the rule or the start
// date changed: the first slot of the new rule on or after where it stood.
//
// A pointer left on an old-rule date is a slot nobody can see, and the
// reminder reads unpaid for ever. A series that has never fired starts over
// from its new start.
func repointedSchedule(pointer, startBefore domain.Date, series domain.Series) domain.Date {
	from := series.StartOn
	fired := !pointer.IsZero() && pointer != startBefore
	if fired && pointer.After(from) {
		from = pointer
	}
	next, found := domain.NextOccurrenceAfter(series.Recurrence, series.StartOn, from.AddDays(-1), series.EndOn)
	if !found {
		return series.StartOn
	}
	return next
}

func sameRecurrence(a, b domain.Recurrence) bool {
	return a.Alias == b.Alias && a.Frequency == b.Frequency && a.Interval == b.Interval &&
		slices.Equal(a.ByMonthDay, b.ByMonthDay) && slices.Equal(a.ByDay, b.ByDay) &&
		slices.Equal(a.ByMonth, b.ByMonth)
}

// horizon resolves the two ends the expansion needs from the request's window.
// An open end becomes today, or today plus defaultHorizonDays; the response
// still echoes the window that was asked for.
func horizon(window Window, today domain.Date) (start, end domain.Date) {
	start = today
	if window.HasFrom {
		start = window.From
	}
	if window.HasTo {
		end = window.To
	} else {
		end = start.AddDays(defaultHorizonDays)
	}
	if days := len(domain.DateRange(start, end)); days > maxHorizonDays {
		end = start.AddDays(maxHorizonDays - 1)
	}
	return start, end
}

// seriesBills is the one loader every occurrence reader on this resource
// calls. An empty list of ids means every linked series in the space.
func seriesBills(
	ctx context.Context, env *Env, sp auth.SpaceContext, ids []domain.ID,
) (map[domain.ID][]service.SeriesBill, error) {
	return service.NewBills(env.DB).SeriesBills(ctx, sp.ID(), ids)
}

func seriesLinks(
	ctx context.Context, env *Env, sp auth.SpaceContext, ids []domain.ID,
) (map[domain.ID]service.SeriesBillLinkInfo, error) {
	return service.NewBills(env.DB).SeriesLinks(ctx, sp.ID(), ids)
}

func seriesBillConnects(
	ctx context.Context, env *Env, sp auth.SpaceContext, seriesID domain.ID,
) ([]domain.BillConnect, error) {
	bills, err := seriesBills(ctx, env, sp, []domain.ID{seriesID})
	if err != nil {
		return nil, err
	}
	return service.SeriesBillConnects(bills[seriesID]), nil
}

func billConnects(bills map[domain.ID][]service.SeriesBill) map[domain.ID][]domain.BillConnect {
	if len(bills) == 0 {
		return nil
	}
	out := make(map[domain.ID][]domain.BillConnect, len(bills))
	for id, linked := range bills {
		out[id] = service.SeriesBillConnects(linked)
	}
	return out
}

func seriesIDs(series []domain.Series) []domain.ID {
	out := make([]domain.ID, 0, len(series))
	for _, one := range series {
		out = append(out, one.ID)
	}
	return out
}

func occurrenceResponse(
	row service.SeriesRow, one domain.Occurrence,
	bySlot map[slotKey]store.Transaction, today domain.Date,
	bills map[domain.ID][]service.SeriesBill, links map[domain.ID]service.SeriesBillLinkInfo,
) OccurrenceResponse {
	series := service.ToDomainSeries(row)
	out := OccurrenceResponse{
		SeriesID:   &row.ID,
		AccountID:  &row.AccountID,
		CategoryID: pgconv.NullUUID(row.CategoryID),
		Kind:       row.Kind,
		Label:      series.Label(),
		DueOn:      Date(one.DueOn),
		Amount:     one.Amount,
		Status:     occurrenceStatus(domain.SeriesKind(row.Kind), false, one.DueOn, today),
	}
	if !one.PaysOn.IsZero() {
		paysOn := Date(one.PaysOn)
		out.PaysOn = &paysOn
	}
	// Which bill speaks about the slot is the domain's decision
	// (domain.SlotBills), named on the occurrence.
	for _, bill := range bills[domain.ID(row.ID.String())] {
		if one.BillID == "" || bill.Connect.ID != one.BillID {
			continue
		}
		out.Bill = occurrenceBill(bill.Bill)
		break
	}
	if link, linked := links[domain.ID(row.ID.String())]; linked {
		out.BillLink = &OccurrenceBillLink{
			ConnectionID: link.ConnectionID, Biller: string(link.Biller),
			ConnectionLabel: link.ConnectionLabel, SubaccountLabel: link.SubaccountLabel,
			Health: string(link.Health), Autopay: link.Autopay,
		}
	}
	if charge, claimed := bySlot[slotKey{row.ID, one.ScheduledOn}]; claimed {
		switch {
		case charge.EstimateStatus == skippedEstimate || charge.IsDeleted:
			out.Status = entrySkipped
		case charge.EstimateStatus != "" || charge.Date.After(today):
			// A provider forecast, not a payment: the slot keeps upcoming or
			// past_due.
			out.TransactionID = &charge.ID
		default:
			out.Status = occurrenceStatus(domain.SeriesKind(row.Kind), true, one.DueOn, today)
			out.TransactionID = &charge.ID
		}
	}
	return out
}

func occurrenceBill(bill store.Bill) *OccurrenceBill {
	return &OccurrenceBill{
		ID: bill.ID, AmountDue: bill.AmountDue, DueOn: Date(bill.DueOn),
		Status: string(bill.Status), Source: bill.Source,
		FetchedAt: bill.FetchedAt, DocumentID: pgconv.NullUUID(bill.DocumentID),
	}
}

// payManually is the pay-manually reminders an occurrence listing filtered
// this way holds: none under an account filter, since none is paid from an
// account anyone named, nor under a kind other than bill, and under a search
// those whose provider or billed account holds it.
func payManually(
	ctx context.Context, env *Env, sp auth.SpaceContext, filter seriesFilter,
) ([]service.ManualBill, error) {
	if filter.AccountID != uuid.Nil || (filter.Kind != "" && filter.Kind != string(domain.SeriesBill)) {
		return nil, nil
	}
	manual, err := service.NewBills(env.DB).PayManually(ctx, sp.ID())
	if err != nil || filter.Search == "" {
		return manual, err
	}
	needle := strings.ToLower(filter.Search)
	var kept []service.ManualBill
	for _, one := range manual {
		if strings.Contains(strings.ToLower(one.Connection.DisplayName()), needle) ||
			strings.Contains(strings.ToLower(one.Subaccount.Label), needle) {
			kept = append(kept, one)
		}
	}
	return kept, nil
}

func manualByBill(manual []service.ManualBill) map[domain.ID]service.ManualBill {
	out := make(map[domain.ID]service.ManualBill, len(manual))
	for _, one := range manual {
		out[one.BillID] = one
	}
	return out
}

// manualOccurrenceResponse is a pay-manually reminder as an occurrence: the
// provider is its label and its link, the statement its bill.
func manualOccurrenceResponse(
	one domain.Occurrence, manual map[domain.ID]service.ManualBill, today domain.Date,
) OccurrenceResponse {
	held := manual[one.BillID]
	return OccurrenceResponse{
		Kind:   string(domain.SeriesBill),
		Label:  held.Connection.DisplayName(),
		DueOn:  Date(one.DueOn),
		Amount: one.Amount,
		Status: occurrenceStatus(domain.SeriesBill, false, one.DueOn, today),
		Bill:   occurrenceBill(held.Bill),
		BillLink: &OccurrenceBillLink{
			ConnectionID: held.Connection.ID, Biller: string(held.Connection.Biller),
			ConnectionLabel: held.Connection.DisplayName(), SubaccountLabel: held.Subaccount.Label,
			Health: string(service.LinkHealth(held.Connection)),
		},
	}
}

func seriesResponse(row service.SeriesRow, today domain.Date) SeriesResponse {
	series := service.ToDomainSeries(row)
	recurrence := series.Recurrence
	perYear := domain.OccurrencesPerYear(recurrence, series.StartOn, today.Year, series.EndOn)
	return SeriesResponse{
		ID:                 row.ID,
		AccountID:          row.AccountID,
		CategoryID:         pgconv.NullUUID(row.CategoryID),
		Kind:               row.Kind,
		Description:        row.Description,
		DisplayName:        pgconv.NullText(row.DisplayName),
		Label:              series.Label(),
		Amount:             row.Amount,
		Currency:           row.Currency,
		Recurrence:         recurrenceResponse(recurrence),
		StartOn:            Date(row.StartOn),
		EndOn:              nullableDate(row.EndOn),
		NextDueOn:          nullableDate(row.NextDueOn),
		DueOn:              Date(series.DueOn()),
		OverrideNextDueOn:  nullableDate(row.OverrideNextDueOn),
		OverrideNextAmount: store.PtrIf(row.OverrideNextAmount, row.HasOverrideNextAmount),
		AutoAdjustDueOn:    row.AutoAdjustDueOn,
		ReminderDays:       row.ReminderDays,
		MatchCriteria:      row.MatchCriteria,
		MatchAmountMin:     store.PtrIf(row.MatchAmountMin, row.HasMatchMin),
		MatchAmountMax:     store.PtrIf(row.MatchAmountMax, row.HasMatchMax),
		TagIDs:             store.NonNil(row.TemplateTagIDs),
		Splits:             decodeSeriesSplits(row.TemplateSplits),
		IsActive:           row.IsActive,
		AnnualizedAmount: domain.AnnualizedAmount(
			row.Amount, recurrence, series.StartOn, today.Year, series.EndOn).Round(),
		OccurrencesPerYear: perYear,
	}
}

func recurrenceResponse(r domain.Recurrence) RecurrenceResponse {
	days := r.ByMonthDay
	if days == nil {
		days = []int{}
	}
	byDay := make([]string, 0, len(r.ByDay))
	for _, day := range r.ByDay {
		byDay = append(byDay, string(day))
	}
	months := make([]int, 0, len(r.ByMonth))
	for _, month := range r.ByMonth {
		months = append(months, int(month))
	}
	return RecurrenceResponse{
		Alias:      r.Alias,
		Frequency:  r.Frequency,
		Interval:   r.Interval,
		ByMonthDay: days,
		ByDay:      byDay,
		ByMonth:    months,
	}
}

// --- Validation --------------------------------------------------------------

func buildRecurrence(write RecurrenceWrite) (domain.Recurrence, error) {
	weekdays, err := checkWeekdays(write.ByDay)
	if err != nil {
		return domain.Recurrence{}, err
	}
	byDay := make([]domain.Weekday, 0, len(weekdays))
	for _, code := range weekdays {
		byDay = append(byDay, domain.Weekday(code))
	}

	interval := store.Deref(write.Interval, 1)
	alias := write.Alias
	if alias == "" {
		alias = domain.AliasOneTime
		if write.Frequency != domain.FreqNone {
			alias = aliasFor(write.Frequency, interval, write.ByMonthDay)
		}
	}
	months := make([]time.Month, 0, len(write.ByMonth))
	for _, month := range write.ByMonth {
		months = append(months, time.Month(month))
	}
	built, err := domain.NewRecurrence(alias, write.Frequency, interval, write.ByMonthDay, byDay, months)
	if err != nil {
		return domain.Recurrence{}, errInvalid("recurrence_invalid", []string{"body", "recurrence"},
			"%s", err)
	}
	return built, nil
}

// seriesStart moves a start date that falls outside the active months to the
// rule's first occurrence, and refuses a rule that never falls in one.
func seriesStart(recurrence domain.Recurrence, startOn, endOn domain.Date) (domain.Date, error) {
	start, ok := domain.ActiveStart(recurrence, startOn, endOn)
	if !ok {
		return domain.Date{}, errInvalid("recurrence_invalid", []string{"body", "recurrence", "by_month"},
			"the rule never falls in any of the months it is active in")
	}
	return start, nil
}

// aliasFor names an unlabelled rule with the dropdown entry it corresponds to.
// Two month days is "twice a month" and more than two is "multiple fixed
// dates"; neither is a rule type of its own.
func aliasFor(frequency domain.Frequency, interval int, byMonthDay []int) domain.RecurrenceAlias {
	switch frequency {
	case domain.FreqDaily:
		return domain.AliasEveryXDays
	case domain.FreqWeekly:
		return domain.AliasEveryWeek
	case domain.FreqYearly:
		return domain.AliasEveryYear
	case domain.FreqMonthly:
		switch {
		case interval == 3:
			return domain.AliasEveryQuarter
		case len(byMonthDay) == 2:
			return domain.AliasTwiceAMonth
		case len(byMonthDay) > 2:
			return domain.AliasMultipleFixed
		default:
			return domain.AliasEveryMonth
		}
	default:
		return domain.AliasOneTime
	}
}

func checkWeekdays(codes []string) ([]string, error) {
	out := make([]string, 0, len(codes))
	for _, code := range codes {
		switch domain.Weekday(code) {
		case domain.WeekdayMO, domain.WeekdayTU, domain.WeekdayWE, domain.WeekdayTH,
			domain.WeekdayFR, domain.WeekdaySA, domain.WeekdaySU:
			out = append(out, code)
		default:
			return nil, errInvalid("enum", []string{"body", "recurrence", "by_day"},
				"%q is not a weekday code", code)
		}
	}
	return out, nil
}

func checkSeriesKind(kind string) (domain.SeriesKind, error) {
	switch domain.SeriesKind(kind) {
	case domain.SeriesIncome, domain.SeriesBill, domain.SeriesSubscription,
		domain.SeriesTransfer, domain.SeriesCreditCardPayment, domain.SeriesRefund:
		return domain.SeriesKind(kind), nil
	}
	return "", errInvalid("enum", []string{"body", "kind"}, "%q is not a series kind", kind)
}

func checkMatchCriteria(criteria string, low, high *domain.Money) (string, error) {
	switch domain.MatchCriteria(criteria) {
	case domain.CriteriaExact, domain.CriteriaAny, domain.CriteriaAuto:
		return criteria, nil
	case domain.CriteriaRange:
		if low == nil || high == nil {
			return "", errConflict("a limited range needs both match_amount_min and match_amount_max")
		}
		if _, err := domain.BetweenAmounts(*low, *high); err != nil {
			return "", errConflict("%s", err)
		}
		return criteria, nil
	}
	return "", errInvalid("enum", []string{"body", "match_criteria"},
		"%q is not a match criteria", criteria)
}

// --- Encoding helpers --------------------------------------------------------

func dateOr(d *Date, fallback domain.Date) domain.Date {
	if d == nil {
		return fallback
	}
	return domain.Date(*d)
}

// --- The transaction template ------------------------------------------------

// checkSeriesTemplate validates the tags and splits a series will stamp onto
// its occurrences. Splits must sum to the series' own amount, so accept-time
// scaling has something to be a proportion of.
func checkSeriesTemplate(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	amount domain.Money, tagIDs []uuid.UUID, splits []SeriesSplit,
) ([]uuid.UUID, []SeriesSplit, error) {
	tags, err := resolveTags(ctx, env, sp, tagIDs)
	if err != nil {
		return nil, nil, err
	}
	if len(splits) == 0 {
		return tags, nil, nil
	}
	// One line is not a split; it is the series' category said twice.
	if len(splits) < 2 {
		return nil, nil, errInvalid("too_short", []string{"body", "splits"},
			"a split needs at least two lines")
	}
	allocated := domain.Sum(splits, func(one SeriesSplit) domain.Money { return one.Amount })
	if !allocated.Equal(amount.Round()) {
		return nil, nil, errConflict("splits total %s but the series is %s", allocated, amount)
	}

	out := make([]SeriesSplit, 0, len(splits))
	for _, one := range splits {
		if err := checkCategory(ctx, env, sp, store.Deref(one.CategoryID, uuid.Nil)); err != nil {
			return nil, nil, err
		}
		lineTags, err := resolveTags(ctx, env, sp, one.TagIDs)
		if err != nil {
			return nil, nil, err
		}
		out = append(out, SeriesSplit{
			Amount:     one.Amount.Round(),
			CategoryID: one.CategoryID,
			Memo:       one.Memo,
			TagIDs:     store.NonNil(lineTags),
		})
	}
	return tags, out, nil
}

// encodeSeriesSplits is the template as the jsonb column holds it. An empty
// template is SQL NULL rather than `[]`, so "no split" reads the same whether
// it was never set or was cleared.
func encodeSeriesSplits(splits []SeriesSplit) ([]byte, error) {
	if len(splits) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(splits)
	if err != nil {
		return nil, fmt.Errorf("api: encode series splits: %w", err)
	}
	return raw, nil
}

// decodeSeriesSplits reads the column back. Unreadable JSON (the importer
// writes it too) is an empty template rather than a failed request.
func decodeSeriesSplits(raw []byte) []SeriesSplit {
	if len(raw) == 0 {
		return []SeriesSplit{}
	}
	var out []SeriesSplit
	if err := json.Unmarshal(raw, &out); err != nil {
		return []SeriesSplit{}
	}
	for i := range out {
		if out[i].TagIDs == nil {
			out[i].TagIDs = []uuid.UUID{}
		}
	}
	return out
}

// scaleSeriesSplits carves an accepted amount up the way the template says, so
// the splits sum to what it actually cost. The rounding remainder lands on the
// largest line; an equal amount is returned verbatim.
func scaleSeriesSplits(splits []SeriesSplit, from, to domain.Money) []SeriesSplit {
	if len(splits) == 0 || from.IsZero() || from.Equal(to) {
		return splits
	}
	out := make([]SeriesSplit, len(splits))
	copy(out, splits)

	running := domain.Zero
	largest := 0
	for i := range out {
		out[i].Amount = out[i].Amount.Scale(to.Decimal().Div(from.Decimal())).Round()
		running = running.Add(out[i].Amount)
		if out[i].Amount.Abs().GreaterThan(out[largest].Amount.Abs()) {
			largest = i
		}
	}
	if remainder := to.Round().Sub(running); !remainder.IsZero() {
		out[largest].Amount = out[largest].Amount.Add(remainder)
	}
	return out
}

// storeSplits is the template as the ledger's own split rows.
func storeSplits(splits []SeriesSplit) []store.Split {
	out := make([]store.Split, 0, len(splits))
	for position, one := range splits {
		out = append(out, store.Split{
			Position:   position,
			Amount:     one.Amount,
			CategoryID: store.Deref(one.CategoryID, uuid.Nil),
			Memo:       one.Memo,
			TagIDs:     one.TagIDs,
		})
	}
	return out
}

// --- Linking an existing charge to a series ----------------------------------

// SeriesLink names the series a posted row should belong to. The slot is
// optional: left out, the occurrence nearest the charge's own date claims it,
// which is what "Link to existing series" from a row menu means.
type SeriesLink struct {
	SeriesID uuid.UUID `json:"series_id"`
	DueOn    *Date     `json:"due_on"`
}

// nearestDueDate is the series occurrence closest to the charge's date,
// searched a year each way. The bool is false only for a series with no
// occurrences. Scheduled dates, as domain.MatchingOccurrence uses: an override
// moves where an occurrence is shown, never which one a charge was.
func nearestDueDate(series domain.Series, on domain.Date) (domain.Date, bool) {
	candidates := domain.ScheduledDueDates(series, on.AddDays(-366), on.AddDays(366))
	var best domain.Date
	bestOff := 0
	for _, candidate := range candidates {
		off := domain.DaysBetween(on, candidate)
		if off < 0 {
			off = -off
		}
		if best.IsZero() || off < bestOff {
			best, bestOff = candidate, off
		}
	}
	return best, !best.IsZero()
}

// errSlotTaken refuses a second charge on one occurrence, naming the first so
// the user can go and look at it.
func errSlotTaken(settled store.Transaction) error {
	return errConflict("%s on %s already records this occurrence",
		store.DomainTransaction(settled).DisplayPayee(), settled.Date)
}

// linkTransactionSeries files a posted row under an occurrence of an existing
// series, through the matcher's own decision and writes. The user is
// overriding the matcher, so no candidate gate runs.
func linkTransactionSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	var body SeriesLink
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.SeriesID == uuid.Nil {
		return errBadRequest("series_id names the series to link to")
	}

	var (
		seriesRow service.SeriesRow
		dueOn     domain.Date
	)
	if body.DueOn != nil {
		seriesRow, _, dueOn, err = occurrenceTarget(r.Context(), env, sp, body.SeriesID, domain.Date(*body.DueOn))
		if err != nil {
			return err
		}
	} else {
		seriesRow, err = service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), body.SeriesID)
		if err != nil {
			if isNotFound(err) || strings.Contains(err.Error(), "no rows") {
				return errNotFound("Series")
			}
			return err
		}
		if seriesRow.IsDeleted {
			return errNotFound("Series")
		}
		series := service.ToDomainSeries(seriesRow)
		var found bool
		if dueOn, found = nearestDueDate(series, row.Date); !found {
			return errConflict("%s has no occurrences to link to", series.Label())
		}
	}

	// One charge per slot. A forecast in the slot does not count (see
	// store.SeriesSlotSettled): the link upgrades it, as the matcher does.
	settled, taken, err := env.DB.SeriesSlotSettled(r.Context(), sp.ID(), seriesRow.ID, dueOn)
	if err != nil {
		return err
	}
	if taken && settled.ID != row.ID {
		return errSlotTaken(settled)
	}

	outcome, err := service.NewSeriesMatcher(env.DB).LinkByHand(r.Context(), sp.ID(), row, seriesRow, dueOn)
	if err != nil {
		return err
	}
	if outcome.RetiredID != uuid.Nil {
		if err := recomputeRunningBalances(r.Context(), env, sp, row.AccountID); err != nil {
			return err
		}
	}
	return respondWithTransaction(env, w, r, sp, outcome.SurvivingID, http.StatusOK)
}

// unlinkTransactionSeries releases the row from its occurrence. The series'
// pointer is left alone.
func unlinkTransactionSeries(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	row, err := liveTransaction(r, env, sp)
	if err != nil {
		return err
	}
	if row.SeriesID == uuid.Nil {
		return errConflict("this transaction is not linked to a series")
	}
	row.SeriesID = uuid.Nil
	row.SeriesDueOn = domain.Date{}
	if err := env.DB.UpdateTransaction(r.Context(), sp.ID(), &row); err != nil {
		return err
	}
	return writeOneTransaction(env, w, r, sp, row, http.StatusOK)
}
