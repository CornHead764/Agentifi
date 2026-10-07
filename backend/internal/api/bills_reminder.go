package api

import (
	"net/http"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// paymentSearchDays is how far either side of the bills the bank rows are
// read; the domain keeps only those within a charge's reach of a bill.
const paymentSearchDays = 31

// BillReminderSuggestion is the reminder a billed account's statements
// describe, in the fields the series editor opens on. Nothing is written: the
// person confirms it, and saving creates the series and links it here.
type BillReminderSuggestion struct {
	SubaccountID uuid.UUID `json:"subaccount_id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	// AccountID is the account the bank rows that paid the bills left; null
	// when none were found.
	AccountID   *uuid.UUID `json:"account_id"`
	CategoryID  *uuid.UUID `json:"category_id"`
	Kind        string     `json:"kind"`
	Description string     `json:"description"`
	DisplayName string     `json:"display_name"`

	Amount         domain.Money  `json:"amount"`
	AmountVaries   bool          `json:"amount_varies"`
	MatchCriteria  string        `json:"match_criteria"`
	MatchAmountMin *domain.Money `json:"match_amount_min"`
	MatchAmountMax *domain.Money `json:"match_amount_max"`

	Recurrence RecurrenceResponse `json:"recurrence"`
	StartOn    Date               `json:"start_on"`
	// Confident is false when the due dates fit no rhythm cleanly.
	Confident bool `json:"confident"`
	DueDates  int  `json:"due_dates"`
	FirstDue  Date `json:"first_due"`
	LastDue   Date `json:"last_due"`

	PaymentIDs []uuid.UUID `json:"payment_ids"`
	// PaidBySeriesID is a live series the payments already belong to, which
	// the person may rather link than duplicate.
	PaidBySeriesID *uuid.UUID `json:"paid_by_series_id"`
}

// suggestBillReminder reads a reminder out of a billed account's statements
// and the bank rows that paid them. A billed account that already keeps a
// reminder current is a conflict; one whose bills keep no rhythm is a 404.
func suggestBillReminder(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	subaccount, err := billSubaccount(r, env, sp)
	if err != nil {
		return err
	}
	links, err := env.DB.ListSeriesBillLinks(r.Context(), sp.ID(), nil)
	if err != nil {
		return err
	}
	for _, link := range links {
		if link.SubaccountID == subaccount.ID {
			return errConflict("%s already keeps a reminder current", subaccount.Label)
		}
	}
	connection, err := env.DB.GetBillConnection(r.Context(), sp.ID(), subaccount.ConnectionID)
	if err != nil {
		return err
	}
	siblings, err := env.DB.ListBillSubaccounts(r.Context(), sp.ID(), connection.ID)
	if err != nil {
		return err
	}
	rows, err := env.DB.ListBills(r.Context(), sp.ID(), subaccount.ID)
	if err != nil {
		return err
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
		payments, err = env.DB.BillPaymentCandidates(r.Context(), sp.ID(),
			first.AddDays(-paymentSearchDays), last.AddDays(paymentSearchDays), amounts)
		if err != nil {
			return err
		}
	}

	found, ok := domain.SuggestBillReminder(bills, payments, domain.DateOf(env.now()))
	if !ok {
		return errNotFound("Suggestion")
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
	out := BillReminderSuggestion{
		SubaccountID:   subaccount.ID,
		ConnectionID:   connection.ID,
		AccountID:      parsedID(found.AccountID),
		CategoryID:     parsedID(found.CategoryID),
		Kind:           string(domain.SeriesBill),
		Description:    description,
		DisplayName:    name,
		Amount:         found.Amount,
		AmountVaries:   found.AmountVaries,
		MatchCriteria:  string(found.Tolerance.Criteria),
		MatchAmountMin: store.PtrIf(low, bounded),
		MatchAmountMax: store.PtrIf(high, bounded),
		Recurrence:     recurrenceResponse(found.Recurrence),
		StartOn:        Date(found.StartOn),
		Confident:      found.Confident,
		DueDates:       found.DueDates,
		FirstDue:       Date(found.FirstDue),
		LastDue:        Date(found.LastDue),
		PaymentIDs:     []uuid.UUID{},
	}
	for _, id := range found.PaymentIDs {
		if parsed := parsedID(id); parsed != nil {
			out.PaymentIDs = append(out.PaymentIDs, *parsed)
		}
	}
	if series := parsedID(found.PaidBySeries); series != nil {
		row, err := service.NewSeriesMatcher(env.DB).GetSeries(r.Context(), sp.ID(), *series)
		switch {
		case err == nil:
			if row.IsActive && !row.IsDeleted {
				out.PaidBySeriesID = series
			}
		case !isNotFound(err) && !strings.Contains(err.Error(), "no rows"):
			return err
		}
	}
	return writeJSON(w, http.StatusOK, out)
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
