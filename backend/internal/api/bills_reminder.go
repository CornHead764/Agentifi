package api

import (
	"context"
	"strings"

	"github.com/google/uuid"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// paymentSearchDays is how far either side of the bills the bank rows are
// read; the domain keeps only those within a charge's reach of a bill.
const paymentSearchDays = 31

// SuggestBillReminder reads a reminder out of a billed account's statements
// and the bank rows that paid them. A billed account that already keeps a
// reminder current is a conflict; one whose bills keep no rhythm is a 404.
func (s billService) SuggestBillReminder(
	ctx context.Context, req *agentifiv1.SuggestBillReminderRequest,
) (*agentifiv1.SuggestBillReminderResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	subaccount, err := billSubaccountOf(ctx, env, sp, req.GetSubaccountId())
	if err != nil {
		return nil, err
	}
	links, err := env.DB.ListSeriesBillLinks(ctx, sp.ID(), nil)
	if err != nil {
		return nil, err
	}
	for _, link := range links {
		if link.SubaccountID == subaccount.ID {
			return nil, errConflict("%s already keeps a reminder current", subaccount.Label)
		}
	}
	connection, err := env.DB.GetBillConnection(ctx, sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	siblings, err := env.DB.ListBillSubaccounts(ctx, sp.ID(), connection.ID)
	if err != nil {
		return nil, err
	}
	rows, err := env.DB.ListBills(ctx, sp.ID(), subaccount.ID)
	if err != nil {
		return nil, err
	}

	bills := make([]domain.BillHistoryEntry, 0, len(rows))
	var amounts []domain.Money
	var first, last domain.Date
	for _, row := range rows {
		bills = append(bills, domain.BillHistoryEntry{
			DueOn: row.DueOn, Amount: row.AmountDue, Status: row.Status,
		})
		amounts = append(amounts, row.AmountDue)
		if first.IsZero() || row.DueOn.Before(first) {
			first = row.DueOn
		}
		if row.DueOn.After(last) {
			last = row.DueOn
		}
	}
	amounts = append(amounts, dayTotals(bills)...)
	var payments []domain.BillPaymentCandidate
	if len(rows) > 0 {
		payments, err = env.DB.BillPaymentCandidates(ctx, sp.ID(),
			first.AddDays(-paymentSearchDays), last.AddDays(paymentSearchDays), amounts)
		if err != nil {
			return nil, err
		}
	}

	found, ok := domain.SuggestBillReminder(bills, payments, domain.DateOf(env.now()))
	if !ok {
		return nil, errNotFound("Suggestion")
	}

	name := connection.DisplayName()
	if len(siblings) > 1 {
		name += " · " + subaccount.Label
	}
	description := found.Description
	if description == "" {
		description = name
	}
	low, high, bounded := found.Tolerance.Bounds(found.Amount, nil)
	out := &agentifiv1.BillReminderSuggestion{
		SubaccountId:   subaccount.ID.String(),
		ConnectionId:   connection.ID.String(),
		AccountId:      billNullableParsedID(found.AccountID),
		CategoryId:     billNullableParsedID(found.CategoryID),
		Kind:           string(domain.SeriesBill),
		Description:    description,
		DisplayName:    name,
		Amount:         moneyProto(found.Amount),
		AmountVaries:   found.AmountVaries,
		MatchCriteria:  string(found.Tolerance.Criteria),
		MatchAmountMin: nullableMoneyProto(low, bounded),
		MatchAmountMax: nullableMoneyProto(high, bounded),
		Recurrence:     billRecurrenceProto(found.Recurrence),
		StartOn:        found.StartOn.String(),
		Confident:      found.Confident,
		DueDates:       int32(found.DueDates),
		FirstDue:       found.FirstDue.String(),
		LastDue:        found.LastDue.String(),
		PaymentIds:     []string{},
	}
	for _, id := range found.PaymentIDs {
		if parsed := parsedID(id); parsed != nil {
			out.PaymentIds = append(out.PaymentIds, parsed.String())
		}
	}
	if series := parsedID(found.PaidBySeries); series != nil {
		row, err := service.NewSeriesMatcher(env.DB).GetSeries(ctx, sp.ID(), *series)
		switch {
		case err == nil:
			if row.IsActive && !row.IsDeleted {
				out.PaidBySeriesId = proto.String(series.String())
			}
		case !isNotFound(err) && !strings.Contains(err.Error(), "no rows"):
			return nil, err
		}
	}
	return &agentifiv1.SuggestBillReminderResponse{Suggestion: out}, nil
}

// dayTotals is what each due date asked for in all, since one payment can
// settle two invoices due the same day.
func dayTotals(bills []domain.BillHistoryEntry) []domain.Money {
	totals := map[domain.Date]domain.Money{}
	for _, bill := range bills {
		totals[bill.DueOn] = totals[bill.DueOn].Add(bill.Amount)
	}
	out := make([]domain.Money, 0, len(totals))
	for _, total := range totals {
		out = append(out, total)
	}
	return out
}

func parsedID(id domain.ID) *uuid.UUID {
	if id == "" {
		return nil
	}
	parsed, err := store.ParseID(id)
	if err != nil {
		return nil
	}
	return &parsed
}

func billNullableParsedID(id domain.ID) *string {
	if parsed := parsedID(id); parsed != nil {
		return proto.String(parsed.String())
	}
	return nil
}

// billRecurrenceProto is a rule in the series editor's shape. The lists are
// never null.
func billRecurrenceProto(r domain.Recurrence) *agentifiv1.BillReminderRecurrence {
	out := &agentifiv1.BillReminderRecurrence{
		Alias: string(r.Alias), Frequency: string(r.Frequency), Interval: int32(r.Interval),
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
