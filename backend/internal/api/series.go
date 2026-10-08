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

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSeriesServiceHandler(seriesService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewOccurrenceServiceHandler(occurrenceService{env}, opts...)
	})
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewCashFlowServiceHandler(cashFlowService{env}, opts...)
	})
}

type (
	seriesService     struct{ env *Env }
	occurrenceService struct{ env *Env }
	cashFlowService   struct{ env *Env }
)

// defaultHorizonDays is the "Next 30 days" the Overview tab opens on, used
// only when a request leaves an end open.
const defaultHorizonDays = 30

// maxHorizonDays bounds an expansion.
const maxHorizonDays = 1830

// skippedEstimate marks the tombstone row that records a skipped occurrence:
// a soft-deleted zero-amount row in the series slot (space_id, series_id,
// series_due_on), which stops the occurrence being projected again.
const skippedEstimate = store.SkippedEstimate

// --- Shapes other routes share -----------------------------------------------

// RecurrenceResponse is the RRULE as the REST routes that embed one (the bill
// reminders) write it.
type RecurrenceResponse struct {
	Alias      domain.RecurrenceAlias `json:"alias"`
	Frequency  domain.Frequency       `json:"frequency"`
	Interval   int                    `json:"interval"`
	ByMonthDay []int                  `json:"by_month_day"`
	ByDay      []string               `json:"by_day"`
	ByMonth    []int                  `json:"by_month"`
}

// RecurrenceWrite is a rule as a request writes it.
type RecurrenceWrite struct {
	Alias      domain.RecurrenceAlias
	Frequency  domain.Frequency
	Interval   *int
	ByMonthDay []int
	ByDay      []string
	ByMonth    []int
}

// CashFlowPointResponse is one day of a balance line, as the REST routes that
// draw one (an account's balance history) write it.
type CashFlowPointResponse struct {
	On      Date         `json:"on"`
	Balance domain.Money `json:"balance"`
}

// SeriesSplit is one line of the split a generated occurrence is carved into,
// as the template column stores it, the same shape a transaction's splits
// use. The amounts are written against the series' own amount and scaled to
// what an occurrence actually cost.
type SeriesSplit struct {
	Amount     domain.Money `json:"amount"`
	CategoryID *uuid.UUID   `json:"category_id"`
	Memo       string       `json:"memo"`
	TagIDs     []uuid.UUID  `json:"tag_ids"`
}

// --- Series CRUD -------------------------------------------------------------

func (s seriesService) ListSeries(
	ctx context.Context, req *agentifiv1.ListSeriesRequest,
) (*agentifiv1.ListSeriesResponse, error) {
	filter, err := seriesFilterOf(req.GetAccountId(), req.GetKind(), req.GetIsActive(), req.GetSearch())
	if err != nil {
		return nil, err
	}
	rows, err := querySeries(ctx, s.env, spaceFrom(ctx), filter)
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(s.env.now())
	out := &agentifiv1.ListSeriesResponse{Series: make([]*agentifiv1.Series, 0, len(rows))}
	for _, row := range rows {
		out.Series = append(out.Series, seriesProto(row, today))
	}
	return out, nil
}

// GetSeriesHistory is one series month by month: each slot the rule landed
// on, the charge that filled it, and any linked charge that fills no slot.
func (s seriesService) GetSeriesHistory(
	ctx context.Context, req *agentifiv1.GetSeriesHistoryRequest,
) (*agentifiv1.GetSeriesHistoryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := liveSeries(ctx, env, sp, req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(env.now())
	last, given, err := monthParameter("to", req.GetTo())
	if err != nil {
		return nil, err
	}
	if !given {
		last = domain.MonthOf(today)
	}
	months, err := boundedParameter("months", req.Months, 12, 1, 60)
	if err != nil {
		return nil, err
	}
	first := last.Shift(1 - months)
	start, end := first.FirstDay(), last.LastDay()

	rows, err := env.DB.ListTransactions(ctx, sp.ID(),
		store.TransactionQuery{IncludeDeleted: true, IncludeEstimates: true, SeriesID: row.ID})
	if err != nil {
		return nil, err
	}
	accounts, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
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
	describe := func(txn store.Transaction) *agentifiv1.SeriesHistoryTransaction {
		return &agentifiv1.SeriesHistoryTransaction{
			Id: txn.ID.String(), AccountId: txn.AccountID.String(), AccountName: names[txn.AccountID],
			Date: txn.Date.String(), Amount: moneyProto(txn.Amount), Payee: txn.Payee,
			StatementName: txn.StatementName, CategoryId: protoOptID(txn.CategoryID),
		}
	}

	out := &agentifiv1.GetSeriesHistoryResponse{
		Series: seriesProto(row, today), From: first.String(), To: last.String(),
		Rows: []*agentifiv1.SeriesHistoryRow{},
	}
	slotted := map[uuid.UUID]bool{}
	paid := []domain.Money{}
	bills, err := seriesBills(ctx, env, sp, []domain.ID{domain.ID(row.ID.String())})
	if err != nil {
		return nil, err
	}
	links, err := seriesLinks(ctx, env, sp, []domain.ID{domain.ID(row.ID.String())})
	if err != nil {
		return nil, err
	}
	for _, one := range domain.ExpectedOccurrences([]domain.Series{service.ToDomainSeries(row)},
		start, end, nil, billConnects(bills), nil, nil) {
		item := occurrenceProto(row, one, bySlot, today, bills, links)
		entry := &agentifiv1.SeriesHistoryRow{DueOn: item.GetDueOn(), Expected: moneyProto(one.Amount), Status: item.GetStatus()}
		if item.TransactionId != nil {
			charge := bySlot[slotKey{row.ID, one.ScheduledOn}]
			entry.Transaction = describe(charge)
			// Same-day bills are rows of one slot, which one charge pays.
			if !slotted[charge.ID] && (item.GetStatus() == entryPaid || item.GetStatus() == entryReceived) {
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
		out.Rows = append(out.Rows, &agentifiv1.SeriesHistoryRow{
			DueOn: txn.Date.String(), Expected: moneyProto(row.Amount),
			Status: fulfilledStatus(domain.SeriesKind(row.Kind)), OffSchedule: true, Transaction: describe(txn),
		})
		paid = append(paid, txn.Amount)
	}
	// "YYYY-MM-DD" sorts as the days do.
	sort.SliceStable(out.Rows, func(i, j int) bool { return out.Rows[j].GetDueOn() < out.Rows[i].GetDueOn() })
	out.PaidCount = int32(len(paid))
	mean, hasMean := domain.Total(paid...).DivInt(len(paid))
	out.AveragePaid = nullableMoneyProto(mean.Round(), hasMean)
	return out, nil
}

func (s seriesService) GetSeries(
	ctx context.Context, req *agentifiv1.GetSeriesRequest,
) (*agentifiv1.GetSeriesResponse, error) {
	row, err := liveSeries(ctx, s.env, spaceFrom(ctx), req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetSeriesResponse{Series: seriesProto(row, domain.DateOf(s.env.now()))}, nil
}

func (s seriesService) CreateSeries(
	ctx context.Context, req *agentifiv1.CreateSeriesRequest,
) (*agentifiv1.CreateSeriesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if strings.TrimSpace(req.GetDescription()) == "" {
		return nil, errInvalid("missing", []string{"body", "description"},
			"description is required; it is what matching compares")
	}
	accountID, err := bodyIDField("account_id", req.GetAccountId())
	if err != nil {
		return nil, err
	}
	account, err := requireAccount(ctx, env, sp, accountID)
	if err != nil {
		return nil, err
	}
	categoryID, err := bodyIDField("category_id", req.GetCategoryId())
	if err != nil {
		return nil, err
	}
	if err := checkCategory(ctx, env, sp, categoryID); err != nil {
		return nil, err
	}
	kind, err := checkSeriesKind(req.GetKind())
	if err != nil {
		return nil, err
	}
	recurrence, err := buildRecurrence(recurrenceWriteOf(req.GetRecurrence()))
	if err != nil {
		return nil, err
	}
	requestedStart, err := dateField(req.GetStartOn(), "body", "start_on")
	if err != nil {
		return nil, err
	}
	endOn, err := dateField(req.GetEndOn(), "body", "end_on")
	if err != nil {
		return nil, err
	}
	startOn, err := seriesStart(recurrence, requestedStart, endOn)
	if err != nil {
		return nil, err
	}
	amount, err := moneyOrZero(req.GetAmount(), "amount")
	if err != nil {
		return nil, err
	}
	low, err := moneyOrNil(req.GetMatchAmountMin(), "match_amount_min")
	if err != nil {
		return nil, err
	}
	high, err := moneyOrNil(req.GetMatchAmountMax(), "match_amount_max")
	if err != nil {
		return nil, err
	}
	criteria, err := checkMatchCriteria(store.Deref(req.MatchCriteria, string(domain.CriteriaAuto)), low, high)
	if err != nil {
		return nil, err
	}

	tagIDs, err := bodyIDsField("tag_ids", req.GetTagIds())
	if err != nil {
		return nil, err
	}
	splits, err := seriesSplitsOf(req.GetSplits())
	if err != nil {
		return nil, err
	}
	tagIDs, splits, err = checkSeriesTemplate(ctx, env, sp, amount, tagIDs, splits)
	if err != nil {
		return nil, err
	}
	encodedSplits, err := encodeSeriesSplits(splits)
	if err != nil {
		return nil, err
	}

	reminderDays := 3
	if req.ReminderDays != nil {
		reminderDays = int(req.GetReminderDays())
	}
	write := store.SeriesWrite{
		AccountID:       account.ID,
		CategoryID:      categoryID,
		Kind:            string(kind),
		Description:     req.GetDescription(),
		DisplayName:     req.GetDisplayName(),
		Amount:          amount,
		Currency:        store.Deref(req.Currency, account.Currency),
		Recurrence:      recurrence,
		StartOn:         startOn,
		EndOn:           endOn,
		AutoAdjustDueOn: req.GetAutoAdjustDueOn(),
		ReminderDays:    reminderDays,
		MatchCriteria:   criteria,
		TemplateTagIDs:  store.NonNil(tagIDs),
		TemplateSplits:  encodedSplits,
		IsActive:        store.Deref(req.IsActive, true),
	}
	if low != nil {
		write.MatchAmountMin, write.HasMatchMin = *low, true
	}
	if high != nil {
		write.MatchAmountMax, write.HasMatchMax = *high, true
	}
	if err := env.DB.CreateSeries(ctx, sp.ID(), &write); err != nil {
		return nil, err
	}

	row, err := service.NewSeriesMatcher(env.DB).GetSeries(ctx, sp.ID(), write.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateSeriesResponse{Series: seriesProto(row, domain.DateOf(env.now()))}, nil
}

func (s seriesService) UpdateSeries(
	ctx context.Context, req *agentifiv1.UpdateSeriesRequest,
) (*agentifiv1.UpdateSeriesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	row, err := liveSeries(ctx, env, sp, req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}

	accountID := optOf(mask, "account_id", req.AccountId)
	if accountID.Cleared() {
		return nil, errConflict("account_id cannot be cleared")
	}
	if accountID.Present() {
		id, err := bodyIDField("account_id", accountID.Value)
		if err != nil {
			return nil, err
		}
		account, err := requireAccount(ctx, env, sp, id)
		if err != nil {
			return nil, err
		}
		row.AccountID = account.ID
	}
	if categoryID := optOf(mask, "category_id", req.CategoryId); categoryID.Set {
		id, err := bodyIDField("category_id", categoryID.Value)
		if err != nil {
			return nil, err
		}
		if err := checkCategory(ctx, env, sp, id); err != nil {
			return nil, err
		}
		row.CategoryID = id
	}
	if kind := optOf(mask, "kind", req.Kind); kind.Present() {
		checked, err := checkSeriesKind(kind.Value)
		if err != nil {
			return nil, err
		}
		row.Kind = string(checked)
	}
	// description and display_name move independently: renaming a series for
	// the eye must not stop it matching the bank's wording.
	if err := applyRequired("description", optOf(mask, "description", req.Description), &row.Description); err != nil {
		return nil, err
	}
	applyNullable(optOf(mask, "display_name", req.DisplayName), &row.DisplayName)
	amount, err := optMoneyOf(mask, "amount", req.Amount)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("amount", amount, &row.Amount); err != nil {
		return nil, err
	}
	if err := applyRequired("currency", optOf(mask, "currency", req.Currency), &row.Currency); err != nil {
		return nil, err
	}
	startBefore := row.StartOn
	startOn, err := optDateOf(mask, "start_on", req.StartOn)
	if err != nil {
		return nil, err
	}
	if err := applyRequired("start_on", startOn, &row.StartOn); err != nil {
		return nil, err
	}
	endOn, err := optDateOf(mask, "end_on", req.EndOn)
	if err != nil {
		return nil, err
	}
	applyNullable(endOn, &row.EndOn)
	overrideOn, err := optDateOf(mask, "override_next_due_on", req.OverrideNextDueOn)
	if err != nil {
		return nil, err
	}
	applyNullable(overrideOn, &row.OverrideNextDueOn)
	overrideAmount, err := optMoneyOf(mask, "override_next_amount", req.OverrideNextAmount)
	if err != nil {
		return nil, err
	}
	applyNullableMoney(overrideAmount, &row.OverrideNextAmount, &row.HasOverrideNextAmount)
	if err := applyRequired("auto_adjust_due_on",
		optOf(mask, "auto_adjust_due_on", req.AutoAdjustDueOn), &row.AutoAdjustDueOn); err != nil {
		return nil, err
	}
	if err := applyRequired("is_active", optOf(mask, "is_active", req.IsActive), &row.IsActive); err != nil {
		return nil, err
	}
	low, err := optMoneyOf(mask, "match_amount_min", req.MatchAmountMin)
	if err != nil {
		return nil, err
	}
	applyNullableMoney(low, &row.MatchAmountMin, &row.HasMatchMin)
	high, err := optMoneyOf(mask, "match_amount_max", req.MatchAmountMax)
	if err != nil {
		return nil, err
	}
	applyNullableMoney(high, &row.MatchAmountMax, &row.HasMatchMax)
	if criteria := optOf(mask, "match_criteria", req.MatchCriteria); criteria.Present() {
		row.MatchCriteria = criteria.Value
	}
	if _, err := checkMatchCriteria(row.MatchCriteria,
		store.PtrIf(row.MatchAmountMin, row.HasMatchMin),
		store.PtrIf(row.MatchAmountMax, row.HasMatchMax)); err != nil {
		return nil, err
	}

	recurrenceBefore := service.ToRecurrence(row)
	recurrence := recurrenceBefore
	if mask["recurrence"] && req.Recurrence != nil {
		built, err := buildRecurrence(recurrenceWriteOf(req.GetRecurrence()))
		if err != nil {
			return nil, err
		}
		recurrence = built
	}
	if row.StartOn, err = seriesStart(recurrence, row.StartOn, row.EndOn); err != nil {
		return nil, err
	}
	scheduleMoved := row.StartOn != startBefore || !sameRecurrence(recurrenceBefore, recurrence)
	reminderDays := optOf(mask, "reminder_days", req.ReminderDays)

	// Validated against the amount this request may have just changed.
	tagIDs, splits := row.TemplateTagIDs, decodeSeriesSplits(row.TemplateSplits)
	if mask["tag_ids"] || mask["splits"] {
		if mask["tag_ids"] {
			if tagIDs, err = bodyIDsField("tag_ids", req.GetTagIds()); err != nil {
				return nil, err
			}
		}
		if mask["splits"] {
			if splits, err = seriesSplitsOf(req.GetSplits()); err != nil {
				return nil, err
			}
		}
		tagIDs, splits, err = checkSeriesTemplate(ctx, env, sp, row.Amount, tagIDs, splits)
		if err != nil {
			return nil, err
		}
	}
	encodedSplits, err := encodeSeriesSplits(splits)
	if err != nil {
		return nil, err
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
		ReminderDays:          int(reminderDays.Value),
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
	if err := env.DB.UpdateSeries(ctx, sp.ID(), write, reminderDays.Present()); err != nil {
		return nil, err
	}

	updated, err := service.NewSeriesMatcher(env.DB).GetSeries(ctx, sp.ID(), row.ID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSeriesResponse{Series: seriesProto(updated, domain.DateOf(env.now()))}, nil
}

// DeleteSeries soft-deletes. The charges it already matched keep their
// series_id, so a deleted series that is restored still owns its history.
func (s seriesService) DeleteSeries(
	ctx context.Context, req *agentifiv1.DeleteSeriesRequest,
) (*agentifiv1.DeleteSeriesResponse, error) {
	sp := spaceFrom(ctx)
	row, err := liveSeries(ctx, s.env, sp, req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteSeries(ctx, sp.ID(), row.ID); err != nil {
		return nil, err
	}
	return &agentifiv1.DeleteSeriesResponse{}, nil
}

// DismissSeriesSuggestion records that the household does not want this
// proposal. Idempotent. The signature digests the account, direction,
// currency, kind and matched wording, so the dismissal survives another charge
// joining the group.
func (s seriesService) DismissSeriesSuggestion(
	ctx context.Context, req *agentifiv1.DismissSeriesSuggestionRequest,
) (*agentifiv1.DismissSeriesSuggestionResponse, error) {
	signature, err := suggestionSignature(req.GetSignature())
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DismissSuggestion(ctx, spaceFrom(ctx).ID(), signature); err != nil {
		return nil, err
	}
	return &agentifiv1.DismissSeriesSuggestionResponse{}, nil
}

// RestoreSeriesSuggestion undoes a dismissal, so the sweep may propose it
// again.
func (s seriesService) RestoreSeriesSuggestion(
	ctx context.Context, req *agentifiv1.RestoreSeriesSuggestionRequest,
) (*agentifiv1.RestoreSeriesSuggestionResponse, error) {
	signature, err := suggestionSignature(req.GetSignature())
	if err != nil {
		return nil, err
	}
	restored, err := s.env.DB.RestoreSuggestion(ctx, spaceFrom(ctx).ID(), signature)
	if err != nil {
		return nil, err
	}
	if !restored {
		return nil, errNotFound("Dismissed suggestion")
	}
	return &agentifiv1.RestoreSeriesSuggestionResponse{}, nil
}

// suggestionSignature checks the signature is a hex SHA-256, keeping arbitrary
// text out of a column only ever compared against digests.
func suggestionSignature(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
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

func (s seriesService) ListSeriesSuggestions(
	ctx context.Context, req *agentifiv1.ListSeriesSuggestionsRequest,
) (*agentifiv1.ListSeriesSuggestionsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	limit, err := boundedParameter("limit", req.Limit, 0, 1, 200)
	if err != nil {
		return nil, err
	}
	// Without this the sweep re-proposes a dismissed pattern on every read.
	dismissed, err := env.DB.ListDismissedSuggestions(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	skip := make(map[string]bool, len(dismissed))
	for _, signature := range dismissed {
		skip[signature] = true
	}

	// dismissed lists the dismissed patterns still present. A dismissal is a
	// signature, so the sweep has to run to find them.
	showDismissed := req.GetDismissed()
	query := service.SuggestionQuery{Today: domain.DateOf(env.now()), Limit: limit}
	if showDismissed {
		query.Limit = 0
	} else {
		query.Dismissed = skip
	}
	found, err := service.NewSuggestions(env.DB).GetSuggestions(ctx, sp.ID(), query)
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.ListSeriesSuggestionsResponse{Suggestions: make([]*agentifiv1.SeriesSuggestion, 0, len(found))}
	for _, one := range found {
		if showDismissed && !skip[one.Signature] {
			continue
		}
		out.Suggestions = append(out.Suggestions, seriesSuggestionProto(one))
		if showDismissed && limit > 0 && len(out.Suggestions) >= limit {
			break
		}
	}
	return out, nil
}

func seriesSuggestionProto(one service.RecurringSuggestion) *agentifiv1.SeriesSuggestion {
	low, high, bounded := one.Tolerance.Bounds(one.Amount, nil)
	return &agentifiv1.SeriesSuggestion{
		Signature:      one.Signature,
		AccountId:      one.AccountID.String(),
		CategoryId:     protoOptID(one.CategoryID),
		Kind:           string(one.Kind),
		Description:    one.Description,
		DisplayName:    one.DisplayName,
		Label:          domain.Series{DisplayName: one.DisplayName, Description: one.Description}.Label(),
		Amount:         moneyProto(one.Amount),
		Currency:       one.Currency,
		Recurrence:     recurrenceProto(one.Recurrence),
		StartOn:        one.StartOn.String(),
		Occurrences:    int32(one.Occurrences),
		FirstSeen:      one.FirstSeen.String(),
		LastSeen:       one.LastSeen.String(),
		Confidence:     one.Confidence,
		MatchCriteria:  string(one.Tolerance.Criteria),
		MatchAmountMin: nullableMoneyProto(low, bounded),
		MatchAmountMax: nullableMoneyProto(high, bounded),
		TransactionIds: protoIDs(one.TransactionIDs),
	}
}

// GetSeriesSuggestionForTransaction answers "what would a series for this row
// look like", so *Create a series* opens on a cadence read out of the history.
// A row already in a series, or with no recognisable wording, is a 404; the
// client then seeds its editor from the transaction alone.
func (s seriesService) GetSeriesSuggestionForTransaction(
	ctx context.Context, req *agentifiv1.GetSeriesSuggestionForTransactionRequest,
) (*agentifiv1.GetSeriesSuggestionForTransactionResponse, error) {
	id, err := idFrom(req.GetTransactionId(), "Transaction")
	if err != nil {
		return nil, err
	}
	one, ok, err := service.NewSuggestions(s.env.DB).SuggestForTransaction(
		ctx, spaceFrom(ctx).ID(), id, domain.DateOf(s.env.now()))
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, errNotFound("Suggestion")
	}
	return &agentifiv1.GetSeriesSuggestionForTransactionResponse{Suggestion: seriesSuggestionProto(one)}, nil
}

// ListRefunds splits the refund series into the tab's two sections. Completed
// means a charge has landed in the slot; expected means none has.
func (s seriesService) ListRefunds(
	ctx context.Context, _ *agentifiv1.ListRefundsRequest,
) (*agentifiv1.ListRefundsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	rows, err := querySeries(ctx, env, sp, seriesFilter{Kind: string(domain.SeriesRefund)})
	if err != nil {
		return nil, err
	}
	_, _, bySlot, err := loadSeriesSlots(ctx, env, sp, domain.DateOf(env.now()))
	if err != nil {
		return nil, err
	}

	today := domain.DateOf(env.now())
	out := &agentifiv1.ListRefundsResponse{Expected: []*agentifiv1.Refund{}, Completed: []*agentifiv1.Refund{}}
	for _, row := range rows {
		series := service.ToDomainSeries(row)
		refund := &agentifiv1.Refund{Series: seriesProto(row, today), ExpectedOn: series.DueOn().String()}
		// The slot, not the day the override moved it to: the credit was filed
		// under the date the rule landed on.
		if charge, settled := bySlot[slotKey{row.ID, series.ScheduledDueOn()}]; settled && !charge.IsDeleted {
			refund.SettledOn = proto.String(charge.Date.String())
			refund.TransactionId = proto.String(charge.ID.String())
			out.Completed = append(out.Completed, refund)
			continue
		}
		out.Expected = append(out.Expected, refund)
	}
	return out, nil
}

// --- Occurrences -------------------------------------------------------------

func (s occurrenceService) ListOccurrences(
	ctx context.Context, req *agentifiv1.ListOccurrencesRequest,
) (*agentifiv1.ListOccurrencesResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	start, end := horizon(window, domain.DateOf(env.now()))

	filter, err := seriesFilterOf(req.GetAccountId(), req.GetKind(), req.GetIsActive(), req.GetSearch())
	if err != nil {
		return nil, err
	}
	rows, err := querySeries(ctx, env, sp, filter)
	if err != nil {
		return nil, err
	}
	// Settled rather than claimed: a bill the provider has already written a
	// forecast row for is still upcoming.
	_, settled, bySlot, err := loadSeriesSlots(ctx, env, sp, domain.DateOf(env.now()))
	if err != nil {
		return nil, err
	}
	expected := settled
	if req.GetIncludeFulfilled() {
		expected = nil
	}

	series := make([]domain.Series, 0, len(rows))
	meta := make(map[domain.ID]service.SeriesRow, len(rows))
	for _, row := range rows {
		one := service.ToDomainSeries(row)
		series = append(series, one)
		meta[one.ID] = row
	}

	bills, err := seriesBills(ctx, env, sp, seriesIDs(series))
	if err != nil {
		return nil, err
	}
	links, err := seriesLinks(ctx, env, sp, seriesIDs(series))
	if err != nil {
		return nil, err
	}
	manual, err := payManually(ctx, env, sp, filter)
	if err != nil {
		return nil, err
	}

	today := domain.DateOf(env.now())
	items := make([]*agentifiv1.Occurrence, 0)
	pastDue := 0
	occurrences := domain.ExpectedOccurrences(
		series, start, end, expected, billConnects(bills), service.ManualBills(manual), nil)
	byBill := manualByBill(manual)
	for _, one := range occurrences {
		item := manualOccurrenceProto(one, byBill, today)
		if one.SeriesID != "" {
			item = occurrenceProto(meta[one.SeriesID], one, bySlot, today, bills, links)
		}
		items = append(items, item)
		if item.GetStatus() == entryPastDue {
			pastDue++
		}
	}

	totals := domain.SummarizeOccurrences(occurrences)
	return &agentifiv1.ListOccurrencesResponse{
		Window: windowProto(window),
		Items:  items,
		Summary: &agentifiv1.OccurrenceSummary{
			Income:   moneyProto(totals.Income),
			Expenses: moneyProto(totals.Expenses),
			Net:      moneyProto(totals.Net),
			Count:    int32(len(items)),
			PastDue:  int32(pastDue),
		},
	}, nil
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
		item := manualOccurrenceProto(one, byBill, today)
		if one.SeriesID != "" {
			item = occurrenceProto(meta[one.SeriesID], one, bySlot, today, bills, nil)
		}
		if item.GetStatus() != entryPastDue && item.GetStatus() != entryUpcoming {
			continue
		}
		out = append(out, reminder{Occurrence: one, Series: byID[one.SeriesID], Label: item.GetLabel(), Status: item.GetStatus()})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].DueOn.Before(out[j].DueOn) })
	return out, nil
}

// AcceptOccurrence records the charge that pays an occurrence and advances the
// schedule pointer past it. Back-filling an older occurrence leaves the
// pointer alone, or a payment still to come would be skipped.
func (s occurrenceService) AcceptOccurrence(
	ctx context.Context, req *agentifiv1.AcceptOccurrenceRequest,
) (*agentifiv1.AcceptOccurrenceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	seriesID, err := bodyIDField("series_id", req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	dueOn, err := dateField(req.GetDueOn(), "body", "due_on")
	if err != nil {
		return nil, err
	}
	paidOn, err := dateField(req.GetDate(), "body", "date")
	if err != nil {
		return nil, err
	}
	row, series, slot, err := occurrenceTarget(ctx, env, sp, seriesID, dueOn)
	if err != nil {
		return nil, err
	}

	// One charge per slot, so a double click or a retried POST cannot move
	// the balance and the plan twice.
	settled, taken, err := env.DB.SeriesSlotSettled(ctx, sp.ID(), row.ID, slot)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, errSlotTaken(settled)
	}

	bills, err := seriesBillConnects(ctx, env, sp, series.ID)
	if err != nil {
		return nil, err
	}
	amount := domain.OccurrenceAmount(series, slot, bills)
	if req.Amount != nil {
		if amount, err = moneyFrom(req.GetAmount(), "body", "amount"); err != nil {
			return nil, err
		}
	}
	if req.Date == nil {
		paidOn = dueOn
	}

	charge := &store.Transaction{
		AccountID:     row.AccountID,
		Date:          paidOn,
		Amount:        amount,
		Currency:      row.Currency,
		StatementName: row.Description,
		Payee:         store.Deref(req.Payee, service.ToDomainSeries(row).Label()),
		Notes:         req.GetNotes(),
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
	if err := env.DB.CreateTransaction(ctx, sp.ID(), charge); err != nil {
		return nil, err
	}
	if err := service.NewSeriesMatcher(env.DB).AdvancePast(ctx, sp.ID(), row, slot); err != nil {
		return nil, err
	}
	if err := recomputeRunningBalances(ctx, env, sp, charge.AccountID); err != nil {
		return nil, err
	}
	return &agentifiv1.AcceptOccurrenceResponse{Transaction: transactionProto(transactionResponse(*charge))}, nil
}

// SkipOccurrence marks a slot as handled without a charge.
func (s occurrenceService) SkipOccurrence(
	ctx context.Context, req *agentifiv1.SkipOccurrenceRequest,
) (*agentifiv1.SkipOccurrenceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	seriesID, err := bodyIDField("series_id", req.GetSeriesId())
	if err != nil {
		return nil, err
	}
	dueOn, err := dateField(req.GetDueOn(), "body", "due_on")
	if err != nil {
		return nil, err
	}
	row, _, slot, err := occurrenceTarget(ctx, env, sp, seriesID, dueOn)
	if err != nil {
		return nil, err
	}

	// One claim per slot: two tombstones would read as two skips.
	settled, taken, err := env.DB.SeriesSlotSettled(ctx, sp.ID(), row.ID, slot)
	if err != nil {
		return nil, err
	}
	if taken {
		return nil, errSlotTaken(settled)
	}

	tombstone := &store.Transaction{
		AccountID:      row.AccountID,
		Date:           dueOn,
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
	if err := env.DB.CreateTransaction(ctx, sp.ID(), tombstone); err != nil {
		return nil, err
	}
	if err := service.NewSeriesMatcher(env.DB).AdvancePast(ctx, sp.ID(), row, slot); err != nil {
		return nil, err
	}
	return &agentifiv1.SkipOccurrenceResponse{}, nil
}

// --- Cash flow ---------------------------------------------------------------

func (s cashFlowService) GetCashFlow(
	ctx context.Context, req *agentifiv1.GetCashFlowRequest,
) (*agentifiv1.GetCashFlowResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	window, err := windowOf(req.GetFrom(), req.GetTo(), req.GetDateField())
	if err != nil {
		return nil, err
	}
	start, end := horizon(window, domain.DateOf(env.now()))

	threshold := domain.Zero
	if req.Threshold != nil {
		if threshold, err = moneyFrom(req.GetThreshold(), "query", "threshold"); err != nil {
			return nil, err
		}
	}

	wanted, err := cashFlowAccounts(req.GetAccountId())
	if err != nil {
		return nil, err
	}
	var accounts []store.Account
	if !wanted.selectsNothing() {
		accounts, err = env.DB.ListAccounts(ctx, sp.ID(), wanted.narrow(store.AccountQuery{}))
		if err != nil {
			return nil, err
		}
	}
	postings, err := postingsByAccount(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
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

	rows, err := querySeries(ctx, env, sp, seriesFilter{ActiveOnly: true})
	if err != nil {
		return nil, err
	}
	// Settled, matching the balance above: a slot whose only row is dated in
	// the future still has to be projected.
	_, settled, bySlot, err := loadSeriesSlots(ctx, env, sp, today)
	if err != nil {
		return nil, err
	}
	series := make([]domain.Series, 0, len(rows))
	meta := make(map[domain.ID]service.SeriesRow, len(rows))
	for _, row := range rows {
		one := service.ToDomainSeries(row)
		series = append(series, one)
		meta[one.ID] = row
	}

	bills, err := seriesBills(ctx, env, sp, seriesIDs(series))
	if err != nil {
		return nil, err
	}

	// One expansion, filtered per account, so a line cannot disagree with the
	// total.
	projections := domain.ProjectAccounts(
		balances, series, start, end, settled, billConnects(bills))

	lines := make([]*agentifiv1.CashFlowLine, 0, len(projections))
	combined := map[domain.Date]domain.Money{}
	for id, projection := range projections {
		account := names[id]
		points := make([]*agentifiv1.CashFlowPoint, 0, len(projection))
		for _, point := range projection {
			points = append(points, cashFlowPoint(point.On, point.Balance))
			combined[point.On] = combined[point.On].Add(point.Balance)
		}
		line := &agentifiv1.CashFlowLine{
			AccountId:       account.ID.String(),
			Name:            account.Name,
			StartingBalance: moneyProto(balances[id]),
			Points:          points,
		}
		if lowest, found := projection.Lowest(); found {
			line.Lowest = cashFlowPoint(lowest.On, lowest.Balance)
		}
		if below, found := projection.FirstBelow(threshold); found {
			line.FirstBelow = cashFlowPoint(below.On, below.Balance)
		}
		lines = append(lines, line)
	}
	sort.Slice(lines, func(i, j int) bool { return lines[i].GetName() < lines[j].GetName() })

	total := make([]*agentifiv1.CashFlowPoint, 0, len(combined))
	for _, on := range domain.DateRange(start, end) {
		if balance, present := combined[on]; present {
			total = append(total, cashFlowPoint(on, balance.Round()))
		}
	}

	onlyWanted := map[domain.ID]bool{}
	for id := range balances {
		onlyWanted[id] = true
	}
	markers := make([]*agentifiv1.Occurrence, 0)
	for _, one := range domain.ExpectedOccurrences(
		series, start, end, settled, billConnects(bills), nil, onlyWanted) {
		markers = append(markers, occurrenceProto(meta[one.SeriesID], one, bySlot, today, bills, nil))
	}

	return &agentifiv1.GetCashFlowResponse{
		Window:      windowProto(window),
		Threshold:   moneyProto(threshold),
		Accounts:    lines,
		Combined:    total,
		Occurrences: markers,
	}, nil
}

func cashFlowPoint(on domain.Date, balance domain.Money) *agentifiv1.CashFlowPoint {
	return &agentifiv1.CashFlowPoint{On: on.String(), Balance: moneyProto(balance)}
}

// cashFlowAccounts is the account_id selection: unset is every account, an
// empty set none.
func cashFlowAccounts(set *agentifiv1.IdSet) (accountFilter, error) {
	if set == nil {
		return accountFilter{}, nil
	}
	ids := make([]uuid.UUID, 0, len(set.GetIds()))
	for _, raw := range set.GetIds() {
		id, err := uuid.Parse(strings.TrimSpace(raw))
		if err != nil {
			return accountFilter{}, errInvalid("uuid_parsing", []string{"query", "account_id"},
				"account_id must be a uuid")
		}
		ids = append(ids, id)
	}
	return accountFilter{IDs: ids, Given: true}, nil
}

// --- Orchestration -----------------------------------------------------------

type seriesFilter struct {
	AccountID  uuid.UUID
	Kind       string
	ActiveOnly bool
	Search     string
}

func seriesFilterOf(accountID, kind string, activeOnly bool, search string) (seriesFilter, error) {
	out := seriesFilter{
		Search:     strings.TrimSpace(search),
		Kind:       strings.TrimSpace(kind),
		ActiveOnly: activeOnly,
	}
	if raw := strings.TrimSpace(accountID); raw != "" {
		id, err := uuid.Parse(raw)
		if err != nil {
			return seriesFilter{}, errInvalid("uuid_parsing", []string{"query", "account_id"},
				"account_id must be a uuid")
		}
		out.AccountID = id
	}
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

func liveSeries(ctx context.Context, env *Env, sp auth.SpaceContext, rawID string) (service.SeriesRow, error) {
	id, err := idFrom(rawID, "Series")
	if err != nil {
		return service.SeriesRow{}, err
	}
	row, err := service.NewSeriesMatcher(env.DB).GetSeries(ctx, sp.ID(), id)
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

func occurrenceProto(
	row service.SeriesRow, one domain.Occurrence,
	bySlot map[slotKey]store.Transaction, today domain.Date,
	bills map[domain.ID][]service.SeriesBill, links map[domain.ID]service.SeriesBillLinkInfo,
) *agentifiv1.Occurrence {
	series := service.ToDomainSeries(row)
	out := &agentifiv1.Occurrence{
		SeriesId:   proto.String(row.ID.String()),
		AccountId:  proto.String(row.AccountID.String()),
		CategoryId: protoOptID(row.CategoryID),
		Kind:       row.Kind,
		Label:      series.Label(),
		DueOn:      one.DueOn.String(),
		Amount:     moneyProto(one.Amount),
		PaysOn:     protoOptDate(one.PaysOn),
		Status:     occurrenceStatus(domain.SeriesKind(row.Kind), false, one.DueOn, today),
	}
	// Which bill speaks about the slot is the domain's decision
	// (domain.SlotBills), named on the occurrence.
	for _, bill := range bills[domain.ID(row.ID.String())] {
		if one.BillID == "" || bill.Connect.ID != one.BillID {
			continue
		}
		out.Bill = occurrenceBillProto(bill.Bill)
		break
	}
	if link, linked := links[domain.ID(row.ID.String())]; linked {
		out.BillLink = &agentifiv1.OccurrenceBillLink{
			ConnectionId: link.ConnectionID.String(), Biller: string(link.Biller),
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
			out.TransactionId = proto.String(charge.ID.String())
		default:
			out.Status = occurrenceStatus(domain.SeriesKind(row.Kind), true, one.DueOn, today)
			out.TransactionId = proto.String(charge.ID.String())
		}
	}
	return out
}

func occurrenceBillProto(bill store.Bill) *agentifiv1.OccurrenceBill {
	return &agentifiv1.OccurrenceBill{
		Id: bill.ID.String(), AmountDue: moneyProto(bill.AmountDue), DueOn: bill.DueOn.String(),
		Status: string(bill.Status), Source: bill.Source,
		FetchedAt: timestamppb.New(bill.FetchedAt), DocumentId: protoOptID(bill.DocumentID),
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

// manualOccurrenceProto is a pay-manually reminder as an occurrence: the
// provider is its label and its link, the statement its bill.
func manualOccurrenceProto(
	one domain.Occurrence, manual map[domain.ID]service.ManualBill, today domain.Date,
) *agentifiv1.Occurrence {
	held := manual[one.BillID]
	return &agentifiv1.Occurrence{
		Kind:   string(domain.SeriesBill),
		Label:  held.Connection.DisplayName(),
		DueOn:  one.DueOn.String(),
		Amount: moneyProto(one.Amount),
		Status: occurrenceStatus(domain.SeriesBill, false, one.DueOn, today),
		Bill:   occurrenceBillProto(held.Bill),
		BillLink: &agentifiv1.OccurrenceBillLink{
			ConnectionId: held.Connection.ID.String(), Biller: string(held.Connection.Biller),
			ConnectionLabel: held.Connection.DisplayName(), SubaccountLabel: held.Subaccount.Label,
			Health: string(service.LinkHealth(held.Connection)),
		},
	}
}

func seriesProto(row service.SeriesRow, today domain.Date) *agentifiv1.Series {
	series := service.ToDomainSeries(row)
	recurrence := series.Recurrence
	perYear := domain.OccurrencesPerYear(recurrence, series.StartOn, today.Year, series.EndOn)
	return &agentifiv1.Series{
		Id:                 row.ID.String(),
		AccountId:          row.AccountID.String(),
		CategoryId:         protoOptID(row.CategoryID),
		Kind:               row.Kind,
		Description:        row.Description,
		DisplayName:        dbconv.NullText(row.DisplayName),
		Label:              series.Label(),
		Amount:             moneyProto(row.Amount),
		Currency:           row.Currency,
		Recurrence:         recurrenceProto(recurrence),
		StartOn:            row.StartOn.String(),
		EndOn:              protoOptDate(row.EndOn),
		NextDueOn:          protoOptDate(row.NextDueOn),
		DueOn:              series.DueOn().String(),
		OverrideNextDueOn:  protoOptDate(row.OverrideNextDueOn),
		OverrideNextAmount: nullableMoneyProto(row.OverrideNextAmount, row.HasOverrideNextAmount),
		AutoAdjustDueOn:    row.AutoAdjustDueOn,
		ReminderDays:       int32(row.ReminderDays),
		MatchCriteria:      row.MatchCriteria,
		MatchAmountMin:     nullableMoneyProto(row.MatchAmountMin, row.HasMatchMin),
		MatchAmountMax:     nullableMoneyProto(row.MatchAmountMax, row.HasMatchMax),
		TagIds:             protoIDs(row.TemplateTagIDs),
		Splits:             seriesSplitsProto(decodeSeriesSplits(row.TemplateSplits)),
		IsActive:           row.IsActive,
		AnnualizedAmount: moneyProto(domain.AnnualizedAmount(
			row.Amount, recurrence, series.StartOn, today.Year, series.EndOn).Round()),
		OccurrencesPerYear: int32(perYear),
	}
}

func recurrenceProto(r domain.Recurrence) *agentifiv1.SeriesRecurrence {
	out := &agentifiv1.SeriesRecurrence{
		Alias:      string(r.Alias),
		Frequency:  string(r.Frequency),
		Interval:   int32(r.Interval),
		ByMonthDay: make([]int32, 0, len(r.ByMonthDay)),
		ByDay:      make([]string, 0, len(r.ByDay)),
		ByMonth:    make([]int32, 0, len(r.ByMonth)),
	}
	for _, day := range r.ByMonthDay {
		out.ByMonthDay = append(out.ByMonthDay, int32(day))
	}
	for _, day := range r.ByDay {
		out.ByDay = append(out.ByDay, string(day))
	}
	for _, month := range r.ByMonth {
		out.ByMonth = append(out.ByMonth, int32(month))
	}
	return out
}

func recurrenceWriteOf(write *agentifiv1.SeriesRecurrenceWrite) RecurrenceWrite {
	out := RecurrenceWrite{
		Alias:     domain.RecurrenceAlias(write.GetAlias()),
		Frequency: domain.Frequency(write.GetFrequency()),
		ByDay:     write.GetByDay(),
	}
	if write.Interval != nil {
		interval := int(write.GetInterval())
		out.Interval = &interval
	}
	for _, day := range write.GetByMonthDay() {
		out.ByMonthDay = append(out.ByMonthDay, int(day))
	}
	for _, month := range write.GetByMonth() {
		out.ByMonth = append(out.ByMonth, int(month))
	}
	return out
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

// --- The procedure wire ------------------------------------------------------

// protoOptDate is an optional date field: unset for the zero date.
func protoOptDate(d domain.Date) *string {
	if d.IsZero() {
		return nil
	}
	return proto.String(d.String())
}

// protoOptID is an optional id field: unset for the zero id.
func protoOptID(id uuid.UUID) *string {
	if id == uuid.Nil {
		return nil
	}
	return proto.String(id.String())
}

func protoIDs(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// bodyIDField reads an id a request names in its body. Empty is the zero id,
// which names nothing.
func bodyIDField(name, raw string) (uuid.UUID, error) {
	if raw == "" {
		return uuid.Nil, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalid("uuid_parsing", []string{"body", name}, "%s must be a uuid", name)
	}
	return id, nil
}

func bodyIDsField(name string, raw []string) ([]uuid.UUID, error) {
	out := make([]uuid.UUID, 0, len(raw))
	for _, one := range raw {
		id, err := uuid.Parse(one)
		if err != nil {
			return nil, errInvalid("uuid_parsing", []string{"body", name}, "%s must be a list of uuids", name)
		}
		out = append(out, id)
	}
	return out, nil
}

// moneyOrZero reads a body amount whose absence is zero.
func moneyOrZero(m *agentifiv1.Money, name string) (domain.Money, error) {
	if m == nil {
		return domain.Zero, nil
	}
	return moneyFrom(m, "body", name)
}

// moneyOrNil reads a body amount whose absence is no amount.
func moneyOrNil(m *agentifiv1.NullableMoney, name string) (*domain.Money, error) {
	if m == nil {
		return nil, nil
	}
	amount, err := moneyFrom(m, "body", name)
	if err != nil {
		return nil, err
	}
	return &amount, nil
}

// monthParameter reads a "YYYY-MM" parameter. Empty is absent.
func monthParameter(name, raw string) (domain.Month, bool, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return domain.Month{}, false, nil
	}
	parsed, err := parseMonth(raw)
	if err != nil {
		return domain.Month{}, false, errInvalid("date_parsing", []string{"query", name}, "%s", err)
	}
	return parsed, true, nil
}

// boundedParameter reads a whole-number parameter, fallback when unset and
// refused outside low..high rather than clamped.
func boundedParameter(name string, value *int32, fallback, low, high int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if got := int(*value); got >= low && got <= high {
		return got, nil
	}
	return 0, errInvalid("out_of_range", []string{"query", name},
		"%s must be between %d and %d", name, low, high)
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

func seriesSplitsProto(splits []SeriesSplit) []*agentifiv1.SeriesSplit {
	out := make([]*agentifiv1.SeriesSplit, 0, len(splits))
	for _, one := range splits {
		out = append(out, &agentifiv1.SeriesSplit{
			Amount:     moneyProto(one.Amount),
			CategoryId: protoOptID(store.Deref(one.CategoryID, uuid.Nil)),
			Memo:       one.Memo,
			TagIds:     protoIDs(one.TagIDs),
		})
	}
	return out
}

func seriesSplitsOf(splits []*agentifiv1.SeriesSplit) ([]SeriesSplit, error) {
	out := make([]SeriesSplit, 0, len(splits))
	for _, one := range splits {
		amount, err := moneyOrZero(one.GetAmount(), "splits")
		if err != nil {
			return nil, err
		}
		categoryID, err := bodyIDField("splits", one.GetCategoryId())
		if err != nil {
			return nil, err
		}
		tagIDs, err := bodyIDsField("splits", one.GetTagIds())
		if err != nil {
			return nil, err
		}
		split := SeriesSplit{Amount: amount, Memo: one.GetMemo(), TagIDs: tagIDs}
		if one.CategoryId != nil {
			split.CategoryID = &categoryID
		}
		out = append(out, split)
	}
	return out, nil
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
