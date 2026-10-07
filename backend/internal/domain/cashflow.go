package domain

import (
	"slices"
	"strings"
)

// Cash flow projection, per account, forward from today:
//
//	balance(d) = current_balance + Σ expected occurrences with due_date ≤ d
//
// Not a statistical forecast: the balance plus the bills and paychecks the
// user has told us about. Low balance is this projection crossing a threshold,
// never a second calculation, so the notification cannot disagree with the
// chart.

// CashFlowPoint is one account's projected balance at the end of one day.
type CashFlowPoint struct {
	On      Date
	Balance Money
}

// BalanceProjection is one account's line on the cash-flow chart, a point per
// day.
type BalanceProjection []CashFlowPoint

// OccurrenceAmount is what the occurrence in this scheduled slot is expected
// to cost: the user's one-off override, then the linked bill that speaks about
// the slot, then the series estimate. The user's override beats the biller
// deliberately, and applies to the next slot only, or one large power bill
// would be projected into every future month. A bill speaks about its own
// cycle only, for the same reason. Two bills due the same day both speak
// about the slot, and it costs their sum (SlotBills).
func OccurrenceAmount(series Series, slot Date, bills []BillConnect) Money {
	if overridesAmount(series, slot) {
		return series.OverrideNextAmount
	}
	if speaking := SlotBills(series, slot, bills); len(speaking) > 0 {
		total := Zero
		for _, bill := range speaking {
			total = total.Add(bill.Amount)
		}
		return total
	}
	return series.Amount
}

func overridesAmount(series Series, slot Date) bool {
	return slot == series.ScheduledDueOn() && series.HasOverrideNextAmount
}

// OccurrenceDueOn is the day the occurrence in this scheduled slot is shown:
// the user's override of the next slot, then an open bill's due date for an
// auto_adjust_due_on series, then the slot itself.
func OccurrenceDueOn(series Series, slot Date, bills []BillConnect) Date {
	if slot == series.ScheduledDueOn() && !series.OverrideNextDueOn.IsZero() {
		return series.OverrideNextDueOn
	}
	if bill, found := SlotBill(series, slot, bills); found && series.AutoAdjustDueOn && !bill.Paid {
		return bill.DueOn
	}
	return slot
}

// OccurrencePaysOn is the day the money for this slot leaves when an open bill
// says so, else zero.
func OccurrencePaysOn(series Series, slot Date, bills []BillConnect) Date {
	if bill, found := SlotBill(series, slot, bills); found && !bill.Paid {
		return bill.AutopayOn
	}
	return Date{}
}

// ExpectedOccurrences lists every occurrence still expected in the window,
// ascending by date: each series' slots, and each pay-manually reminder
// (PayManually) due in it. Occurrences already fulfilled by a posted
// transaction are dropped: that charge is in the starting balance. A nil
// accountIDs means every account; the screen's account selector filters this
// same set so the lines cannot disagree with the total. A pay-manually
// reminder is paid from no account anyone named, so any account filter
// leaves it out.
func ExpectedOccurrences(
	series []Series,
	windowStart, windowEnd Date,
	fulfilled map[ID]map[Date]bool,
	bills map[ID][]BillConnect,
	manual []ManualBill,
	accountIDs map[ID]bool,
) []Occurrence {
	var out []Occurrence
	for _, one := range manual {
		if accountIDs != nil || one.DueOn.Before(windowStart) || one.DueOn.After(windowEnd) {
			continue
		}
		out = append(out, Occurrence{
			Kind: SeriesBill, DueOn: one.DueOn, ScheduledOn: one.DueOn, Amount: one.Amount,
			BillID: one.BillID,
		})
	}
	for _, one := range series {
		if accountIDs != nil && !accountIDs[one.AccountID] {
			continue
		}
		done := fulfilled[one.ID]
		linked := bills[one.ID]
		for _, slot := range OccurrenceSlots(one, windowStart, windowEnd, linked) {
			// Asked of the slot, not the displayed day: the charge that paid
			// a moved occurrence is filed under the schedule date.
			if done[slot.ScheduledOn] {
				continue
			}
			occurrence := Occurrence{
				SeriesID:    one.ID,
				AccountID:   one.AccountID,
				Kind:        one.Kind,
				DueOn:       slot.DueOn,
				ScheduledOn: slot.ScheduledOn,
				Amount:      OccurrenceAmount(one, slot.ScheduledOn, linked),
				PaysOn:      OccurrencePaysOn(one, slot.ScheduledOn, linked),
			}
			speaking := SlotBills(one, slot.ScheduledOn, linked)
			if len(speaking) > 0 {
				occurrence.BillID = speaking[0].ID
			}
			// Same-day bills are listed one occurrence each, on the one slot
			// they share, so their amounts still add up to OccurrenceAmount.
			// The user's override is one figure for the whole cycle.
			if len(speaking) < 2 || overridesAmount(one, slot.ScheduledOn) {
				out = append(out, occurrence)
				continue
			}
			for _, bill := range speaking {
				occurrence.BillID = bill.ID
				occurrence.Amount = bill.Amount
				occurrence.PaysOn = Date{}
				if !bill.Paid {
					occurrence.PaysOn = bill.AutopayOn
				}
				out = append(out, occurrence)
			}
		}
	}
	slices.SortStableFunc(out, func(a, b Occurrence) int {
		if order := dateOrder(a.DueOn, b.DueOn); order != 0 {
			return order
		}
		if order := strings.Compare(string(a.SeriesID), string(b.SeriesID)); order != 0 || a.SeriesID != "" {
			return order
		}
		// Pay-manually reminders, which have no series.
		return strings.Compare(string(a.BillID), string(b.BillID))
	})
	return out
}

// ProjectBalances returns a point per day across the window, the first day
// included. Occurrences dated before the window fold into the opening point
// rather than being dropped.
func ProjectBalances(currentBalance Money, occurrences []Occurrence, windowStart, windowEnd Date) BalanceProjection {
	ordered := slices.Clone(occurrences)
	slices.SortFunc(ordered, func(a, b Occurrence) int { return dateOrder(a.MovesOn(), b.MovesOn()) })
	running := currentBalance
	index := 0
	var points BalanceProjection
	for _, on := range DateRange(windowStart, windowEnd) {
		for index < len(ordered) && ordered[index].MovesOn().NotAfter(on) {
			running = running.Add(ordered[index].Amount)
			index++
		}
		points = append(points, CashFlowPoint{On: on, Balance: running.Round()})
	}
	return points
}

// ProjectAccounts draws one line per account from one expansion of the same
// series set.
func ProjectAccounts(
	balances map[ID]Money,
	series []Series,
	windowStart, windowEnd Date,
	fulfilled map[ID]map[Date]bool,
	bills map[ID][]BillConnect,
) map[ID]BalanceProjection {
	wanted := make(map[ID]bool, len(balances))
	for accountID := range balances {
		wanted[accountID] = true
	}
	occurrences := ExpectedOccurrences(series, windowStart, windowEnd, fulfilled, bills, nil, wanted)

	byAccount := make(map[ID][]Occurrence, len(balances))
	for _, one := range occurrences {
		byAccount[one.AccountID] = append(byAccount[one.AccountID], one)
	}
	out := make(map[ID]BalanceProjection, len(balances))
	for accountID, balance := range balances {
		out[accountID] = ProjectBalances(balance, byAccount[accountID], windowStart, windowEnd)
	}
	return out
}

// FirstBelow is the first day the projection drops under the threshold: the
// low-balance notification. Callers pass the points they display.
func (p BalanceProjection) FirstBelow(threshold Money) (CashFlowPoint, bool) {
	for _, point := range p {
		if point.Balance.LessThan(threshold) {
			return point, true
		}
	}
	return CashFlowPoint{}, false
}

// Lowest is the worst day in the window — the figure the summary card leads
// with.
func (p BalanceProjection) Lowest() (CashFlowPoint, bool) {
	var best CashFlowPoint
	found := false
	for _, point := range p {
		if !found || point.Balance.LessThan(best.Balance) {
			best, found = point, true
		}
	}
	return best, found
}
