package domain

import (
	"slices"
	"time"
)

// A reminder suggested from a billed account's statements: what a series for
// those bills would look like, read out of their due dates and amounts, with
// the bank rows that paid them supplying the account and the wording to match.

// SeasonGapDays is the gap between two bills that reads as an off season
// rather than a late bill: more than two months.
const SeasonGapDays = 62

// minBillDates is how many due dates a suggestion needs; two make one gap,
// which is no rhythm.
const minBillDates = 3

// BillHistoryEntry is one statement of the billed account. Amount is what the
// provider states, a magnitude.
type BillHistoryEntry struct {
	DueOn  Date
	Amount Money
	Status BillStatus
}

// BillPaymentCandidate is a bank row that may have paid one of the bills.
// Amount is signed as the ledger holds it; SeriesID is set when the row
// already belongs to a series.
type BillPaymentCandidate struct {
	ID            ID
	AccountID     ID
	CategoryID    ID
	SeriesID      ID
	On            Date
	Amount        Money
	StatementName string
}

// BillReminderSuggestion is the series the bills describe.
type BillReminderSuggestion struct {
	Recurrence Recurrence
	// StartOn is the slot of the earliest open bill, so the reminder shows the
	// bill already waiting; with none open, the next slot from today.
	StartOn Date
	// Amount is the median of what each due date asked for, negative: a bill
	// is money going out.
	Amount       Money
	Tolerance    AmountTolerance
	AmountVaries bool
	// Confident is false when the dates fit no rhythm cleanly and the schedule
	// is a best guess the person should check.
	Confident bool
	DueDates  int
	FirstDue  Date
	LastDue   Date

	// From the bank rows that paid the bills; empty when none were found.
	AccountID     ID
	CategoryID    ID
	Description   string
	PaymentIDs    []ID
	PaidBySeries  ID
	PaymentsFound int
}

// billDay is every invoice due on one day, which is one occurrence: two lawn
// visits invoiced together are paid together.
type billDay struct {
	on       Date
	total    Money
	invoices []Money
	open     bool
}

// SuggestBillReminder reads a reminder out of a billed account's statements.
// False when there are too few due dates or they keep no rhythm.
//
// The rhythm is the cadence of the gaps between due dates
// (ClassifyCadence). When that fails and the widest gap is an off season
// (seasonOf), the rhythm is read from the gaps inside the season and the
// rule is limited to the months outside it. A season whose visits are about
// monthly but not evenly spaced is offered as monthly and not Confident, as
// is a year-round history of about-monthly bills that wander too much to
// classify.
func SuggestBillReminder(
	bills []BillHistoryEntry, payments []BillPaymentCandidate, today Date,
) (BillReminderSuggestion, bool) {
	days := billDays(bills)
	if len(days) < minBillDates {
		return BillReminderSuggestion{}, false
	}
	dates := make([]Date, len(days))
	totals := make([]Money, len(days))
	for i, day := range days {
		dates[i], totals[i] = day.on, day.total
	}

	recurrence, confident, ok := billRhythm(dates)
	if !ok {
		return BillReminderSuggestion{}, false
	}
	tolerance := SuggestTolerance(totals)
	out := BillReminderSuggestion{
		Recurrence:   recurrence,
		Amount:       MedianAmount(totals).Neg(),
		Tolerance:    tolerance,
		AmountVaries: tolerance.Criteria != CriteriaExact,
		Confident:    confident,
		DueDates:     len(days),
		FirstDue:     dates[0],
		LastDue:      dates[len(dates)-1],
	}
	out.StartOn = billReminderStart(recurrence, days, today)
	readPayments(&out, days, payments)
	return out, true
}

func billDays(bills []BillHistoryEntry) []billDay {
	byDay := map[Date]*billDay{}
	var order []Date
	for _, bill := range bills {
		if bill.Status == BillSuperseded || bill.DueOn.IsZero() {
			continue
		}
		day, seen := byDay[bill.DueOn]
		if !seen {
			day = &billDay{on: bill.DueOn, total: Zero}
			byDay[bill.DueOn] = day
			order = append(order, bill.DueOn)
		}
		amount := bill.Amount.Abs()
		day.total = day.total.Add(amount)
		day.invoices = append(day.invoices, amount)
		day.open = day.open || bill.Status == BillOpen
	}
	slices.SortFunc(order, dateOrder)
	out := make([]billDay, 0, len(order))
	for _, on := range order {
		out = append(out, *byDay[on])
	}
	return out
}

func gapsBetween(dates []Date) []int {
	out := make([]int, 0, len(dates))
	for i := 1; i < len(dates); i++ {
		out = append(out, DaysBetween(dates[i-1], dates[i]))
	}
	return out
}

// billRhythm is the rule the due dates keep, and whether they keep it
// cleanly.
func billRhythm(dates []Date) (Recurrence, bool, bool) {
	gaps := gapsBetween(dates)
	day := medianDay(dates)
	if name, _, ok := ClassifyCadence(gaps); ok {
		return cadenceRule(name, day, dates), true, true
	}

	if months, ok := seasonOf(dates, gaps); ok {
		var inSeason []int
		for _, gap := range gaps {
			if gap <= SeasonGapDays {
				inSeason = append(inSeason, gap)
			}
		}
		rule, confident, ok := seasonRule(inSeason, day, dates)
		if !ok {
			return Recurrence{}, false, false
		}
		rule.ByMonth = months
		return rule, confident, true
	}

	if aboutMonthly(gaps) {
		return EveryMonth(day), false, true
	}
	return Recurrence{}, false, false
}

// seasonRule is the rhythm inside the season, which can only be one a season
// can hold: weekly, fortnightly or monthly.
func seasonRule(gaps []int, day int, dates []Date) (Recurrence, bool, bool) {
	if len(gaps) == 0 {
		return Recurrence{}, false, false
	}
	if name, _, ok := ClassifyCadence(gaps); ok {
		switch name {
		case "weekly", "biweekly", "monthly":
			return cadenceRule(name, day, dates), true, true
		}
		return Recurrence{}, false, false
	}
	if aboutMonthly(gaps) {
		return EveryMonth(day), false, true
	}
	return Recurrence{}, false, false
}

// aboutMonthly is a median gap between three weeks and two months: visits a
// person would call monthly, though too uneven for the cadence test.
func aboutMonthly(gaps []int) bool {
	middle := medianInts(gaps)
	return middle >= 20 && middle <= SeasonGapDays
}

// cadenceRule is RecurrenceFor with the bills' usual due day for a monthly or
// quarterly rhythm, rather than the last bill's day.
func cadenceRule(name string, day int, dates []Date) Recurrence {
	switch name {
	case "monthly":
		return EveryMonth(day)
	case "quarterly":
		return EveryQuarter(day)
	}
	return RecurrenceFor(name, dates[len(dates)-1])
}

func medianDay(dates []Date) int {
	days := make([]int, len(dates))
	for i, on := range dates {
		days[i] = on.Day
	}
	slices.Sort(days)
	return days[len(days)/2]
}

// seasonOf is the active months, when the widest gap between bills is an off
// season: longer than SeasonGapDays, spanning two or more whole months, and no
// bill in any year falling in one of them. Bills from April to October with a
// winter gap give April to October, though visits six weeks apart leave June
// or September without a bill in some years. A gap of one whole month is a
// late bill, not a season.
func seasonOf(dates []Date, gaps []int) ([]time.Month, bool) {
	widest := 0
	for i, gap := range gaps {
		if gap > gaps[widest] {
			widest = i
		}
	}
	if gaps[widest] <= SeasonGapDays {
		return nil, false
	}
	off := map[time.Month]bool{}
	for month := MonthOf(dates[widest]).Next(); month.Before(MonthOf(dates[widest+1])); month = month.Next() {
		off[month.Month] = true
	}
	if len(off) < 2 || len(off) == 12 {
		return nil, false
	}
	for _, on := range dates {
		if off[on.Month] {
			return nil, false
		}
	}
	var active []time.Month
	for month := time.January; month <= time.December; month++ {
		if !off[month] {
			active = append(active, month)
		}
	}
	return active, true
}

// billReminderStart is the slot the earliest open bill claims, so the
// reminder opens on the bill already waiting; with none open, or none
// claimed, the next slot after the last bill that is not yet past.
func billReminderStart(recurrence Recurrence, days []billDay, today Date) Date {
	last := days[len(days)-1].on
	for _, day := range days {
		if !day.open {
			continue
		}
		before, after := MatchWindow(recurrence)
		for _, slot := range ExpandOccurrences(recurrence, phaseAnchor(recurrence, days, day.on),
			day.on.AddDays(-after), day.on.AddDays(before), Date{}) {
			if BillClaimsSlot(recurrence, slot, day.on) {
				return slot
			}
		}
		break
	}
	return NextStart(recurrence, last, today)
}

// phaseAnchor is the start a rule is expanded from to find the slot near a
// bill: the latest bill before the bill's window, which keeps an
// every-14-days or weekly rule in step; a yearly rule is anchored on the bill
// itself, since its anchor names its day.
func phaseAnchor(recurrence Recurrence, days []billDay, due Date) Date {
	if recurrence.Frequency == FreqYearly {
		return due
	}
	_, after := MatchWindow(recurrence)
	anchor := days[0].on
	for _, day := range days {
		if day.on.Before(due.AddDays(-after)) {
			anchor = day.on
		}
	}
	if anchor.After(due.AddDays(-after)) {
		anchor = due.AddDays(-after)
	}
	return anchor
}

// readPayments finds the bank row that paid each due date: money out for the
// day's total or one of its invoices, inside the reach of a charge for the
// bill's slot (BillPaymentDates), the nearest first and each row once. The
// account the most of them left is the reminder's, and the latest of those
// rows names the wording to match and the category.
func readPayments(out *BillReminderSuggestion, days []billDay, payments []BillPaymentCandidate) {
	used := map[ID]bool{}
	var matched []BillPaymentCandidate
	for _, day := range days {
		from, to := BillPaymentDates(out.Recurrence, day.on)
		var best BillPaymentCandidate
		bestOff := -1
		for _, payment := range payments {
			if used[payment.ID] || payment.On.Before(from) || payment.On.After(to) {
				continue
			}
			if !paysBillDay(payment.Amount, day) {
				continue
			}
			off := DaysBetween(day.on, payment.On)
			if off < 0 {
				off = -off
			}
			if bestOff < 0 || off < bestOff {
				best, bestOff = payment, off
			}
		}
		if bestOff >= 0 {
			used[best.ID] = true
			matched = append(matched, best)
		}
	}
	if len(matched) == 0 {
		return
	}
	slices.SortFunc(matched, func(a, b BillPaymentCandidate) int { return dateOrder(a.On, b.On) })

	accounts := map[ID]int{}
	series := map[ID]int{}
	for _, payment := range matched {
		accounts[payment.AccountID]++
		if payment.SeriesID != "" {
			series[payment.SeriesID]++
		}
		out.PaymentIDs = append(out.PaymentIDs, payment.ID)
	}
	out.PaymentsFound = len(matched)
	out.AccountID = mostCommon(accounts, matched, func(p BillPaymentCandidate) ID { return p.AccountID })
	out.PaidBySeries = mostCommon(series, matched, func(p BillPaymentCandidate) ID { return p.SeriesID })
	for i := len(matched) - 1; i >= 0; i-- {
		payment := matched[i]
		if payment.AccountID != out.AccountID {
			continue
		}
		if out.Description == "" {
			out.Description = payment.StatementName
		}
		if out.CategoryID == "" {
			out.CategoryID = payment.CategoryID
		}
	}
}

func paysBillDay(amount Money, day billDay) bool {
	if !amount.IsNegative() {
		return false
	}
	paid := amount.Abs()
	if paid.Equal(day.total) {
		return true
	}
	for _, invoice := range day.invoices {
		if paid.Equal(invoice) {
			return true
		}
	}
	return false
}

// mostCommon is the most counted id, the later row's on a tie.
func mostCommon(
	counts map[ID]int, rows []BillPaymentCandidate, idOf func(BillPaymentCandidate) ID,
) ID {
	var best ID
	for _, row := range rows {
		id := idOf(row)
		if id != "" && counts[id] >= counts[best] {
			best = id
		}
	}
	return best
}
