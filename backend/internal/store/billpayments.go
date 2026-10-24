package store

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// heldBillSlot is the slot a bank row holds of a reminder linked to a billed
// account, with what decides which of the account's bills claim it.
type heldBillSlot struct {
	slot         domain.Date
	recurrence   domain.Recurrence
	subaccountID uuid.UUID
}

// billSlotOf is the slot the row holds, or false when it holds none, its
// series has no bill, or it is a forecast or a deleted row.
func (s *Store) billSlotOf(
	ctx context.Context, spaceID SpaceID, txnID uuid.UUID,
) (heldBillSlot, bool, error) {
	var (
		slotOn     string
		alias      string
		frequency  string
		interval   int
		byMonthDay jsonArray[int]
		byDay      jsonArray[string]
		held       heldBillSlot
	)
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return held, false, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return held, false, err
	}
	err = db.Read().QueryRowContext(ctx, `
		SELECT t.series_due_on, s.alias, s.frequency, s."interval", s.by_month_day, s.by_day, l.subaccount_id
		FROM transactions t
		JOIN series s ON s.id = t.series_id
		JOIN series_bill_links l ON l.series_id = t.series_id
		WHERE t.id = ? AND t.series_due_on IS NOT NULL AND `+MoneyMovedOn("t"), txnID).
		Scan(&slotOn, &alias, &frequency, &interval, &byMonthDay, &byDay, &held.subaccountID)
	if err != nil {
		err = wrap("store: bill slot of transaction", err)
		if errors.Is(err, ErrNotFound) {
			return heldBillSlot{}, false, nil
		}
		return heldBillSlot{}, false, err
	}
	days := make([]domain.Weekday, 0, len(byDay))
	for _, code := range byDay {
		days = append(days, domain.Weekday(code))
	}
	held.recurrence = domain.Recurrence{
		Alias:      domain.RecurrenceAlias(alias),
		Frequency:  domain.Frequency(frequency),
		Interval:   interval,
		ByMonthDay: byMonthDay,
		ByDay:      days,
	}
	held.slot, err = domain.ParseDate(slotOn)
	return held, err == nil, err
}

// SettledBill is a bill whose slot a bank row holds, and the connection it is
// filed under.
type SettledBill struct {
	Bill       Bill
	Connection BillConnection
}

// BillsSettledByTransaction is every open or paid bill that claims the slot
// the row holds (domain.BillClaimsSlot, the rule that files a statement on
// it), or, for a row that holds no slot, every bill the provider's payment
// paired with the row settled (domain.StatementsPaidBy). Either is answered
// whether or not the provider gave a statement document.
func (s *Store) BillsSettledByTransaction(
	ctx context.Context, spaceID SpaceID, txnID uuid.UUID,
) ([]SettledBill, error) {
	held, ok, err := s.billSlotOf(ctx, spaceID, txnID)
	if err != nil {
		return nil, err
	}
	if !ok {
		return s.billsSettledByPayment(ctx, spaceID, txnID)
	}
	before, after := domain.MatchWindow(held.recurrence)
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	bills, err := sqliteQueryAll(ctx, db.Read(), "store: bills settled by transaction", billScanner(spaceID),
		`SELECT `+billColumns+` FROM bills b
		  WHERE b.subaccount_id = ? AND b.status IN ('open', 'paid')
		    AND b.due_on BETWEEN ? AND ? ORDER BY b.due_on, b.invoice`,
		held.subaccountID, held.slot.AddDays(-before).String(), held.slot.AddDays(after).String())
	if err != nil || len(bills) == 0 {
		return nil, err
	}
	subaccount, err := s.GetBillSubaccount(ctx, spaceID, held.subaccountID)
	if err != nil {
		return nil, err
	}
	connection, err := s.GetBillConnection(ctx, spaceID, subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	var out []SettledBill
	for _, bill := range bills {
		if domain.BillClaimsSlot(held.recurrence, held.slot, bill.DueOn) {
			out = append(out, SettledBill{Bill: bill, Connection: connection})
		}
	}
	return out, nil
}

func (s *Store) billsSettledByPayment(ctx context.Context, spaceID SpaceID, txnID uuid.UUID) ([]SettledBill, error) {
	bills, subaccountID, ok, err := s.billsPaidBy(ctx, spaceID, txnID)
	if err != nil || !ok || len(bills) == 0 {
		return nil, err
	}
	subaccount, err := s.GetBillSubaccount(ctx, spaceID, subaccountID)
	if err != nil {
		return nil, err
	}
	connection, err := s.GetBillConnection(ctx, spaceID, subaccount.ConnectionID)
	if err != nil {
		return nil, err
	}
	out := make([]SettledBill, 0, len(bills))
	for _, bill := range bills {
		out = append(out, SettledBill{Bill: bill, Connection: connection})
	}
	return out, nil
}

// SeriesHeldSlots is the slot dates the series' bank rows hold, oldest first:
// never a forecast or a deleted row.
func (s *Store) SeriesHeldSlots(ctx context.Context, spaceID SpaceID, seriesID uuid.UUID) ([]domain.Date, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: series held slots", func(row scanner) (domain.Date, error) {
		var on string
		if err := row.Scan(&on); err != nil {
			return domain.Date{}, err
		}
		return domain.ParseDate(on)
	}, `SELECT DISTINCT series_due_on FROM transactions WHERE series_id = ? AND series_due_on IS NOT NULL AND `+MoneyMoved+` ORDER BY series_due_on`, seriesID)
}
