package domain

import "fmt"

// One account's cash flow: its balance history and its own-rhythm estimate.
// Both share the projection's balance (cashflow.go), so a chart drawing all
// three cannot put the account in two places on one day.

// BalanceHistory is the account's end-of-day balance from `from` through
// `through`, both included, oldest first. Each point is exactly
// BalanceAsOf(account, postings, day, DatePosted). Posted date, not effective
// date: a card's effective date is its statement's due date, which would hold
// a charge off the line for a month.
//
// The line begins at HistoryStart when that is later than `from`: before it
// the account has no point, not a point of zero.
func BalanceHistory(account Account, postings []Posting, from, through Date) BalanceProjection {
	if start := HistoryStart(account, postings); !start.IsZero() && from.Before(start) {
		from = start
	}
	if through.Before(from) {
		return nil
	}
	moves := func(p Posting) bool {
		if account.HasProviderBalance {
			return CountsTowardBalance(p)
		}
		return IsSettled(p)
	}
	byDay := map[Date]Money{}
	for _, posting := range postings {
		if !moves(posting) {
			continue
		}
		on := posting.Txn.ReportingDate(DatePosted)
		if on.After(from) && on.NotAfter(through) {
			byDay[on] = byDay[on].Add(posting.NativeAmount())
		}
	}

	running := BalanceAsOf(account, postings, from, DatePosted)
	days := DateRange(from, through)
	out := make(BalanceProjection, 0, len(days))
	for _, on := range days {
		if on.After(from) {
			running = running.Add(byDay[on])
		}
		out = append(out, CashFlowPoint{On: on, Balance: running.Round()})
	}
	return out
}

// AccountForecastHistoryMonths is the most complete months the estimate
// averages over.
const AccountForecastHistoryMonths = 12

// AccountForecastMonths is how many months the estimate runs forward,
// starting with the current month.
const AccountForecastMonths = 6

// AccountCashFlows is every settled row of the account, signed, by posted
// date. Transfers stay in: for one account a sweep to savings moves the
// balance as surely as rent does. The reports and spending-plan exclusion
// flags do not apply: a row excluded from reports still left the bank.
func AccountCashFlows(postings []Posting) []CashFlow {
	out := make([]CashFlow, 0, len(postings))
	for _, posting := range postings {
		if IsSettled(posting) {
			out = append(out, CashFlow{
				On: posting.Txn.ReportingDate(DatePosted), Amount: posting.NativeAmount(),
			})
		}
	}
	return out
}

// EstimateAccountCashFlow is the average month of the account's recent
// history, repeated for the next six.
//
// It reads up to twelve complete months before today's. A history that begins
// inside them is read from where it begins, and a first month that starts
// after the 1st (a bank backfill usually does) is dropped, since averaging a
// partial month understates everything. Empty months inside the span count as
// zero. In and out are averaged separately and rounded once.
//
// When that partial month is the only one, it is read anyway and the
// narrative says so. False when there is no row before today's month: the
// current month alone would read as a trend.
func EstimateAccountCashFlow(flows []CashFlow, today Date) (CashFlowForecast, bool) {
	months := FullMonthsBefore(today, AccountForecastHistoryMonths)
	first, last := months[0], months[len(months)-1]

	totals := map[Month]CashFlowMonth{}
	var earliest Date
	for _, flow := range flows {
		month := MonthOf(flow.On)
		if month.After(last) {
			continue
		}
		if earliest.IsZero() || flow.On.Before(earliest) {
			earliest = flow.On
		}
		if month.Before(first) {
			continue
		}
		row := totals[month]
		if flow.Amount.IsNegative() {
			row.Out = row.Out.Add(flow.Amount.Abs())
		} else {
			row.In = row.In.Add(flow.Amount)
		}
		totals[month] = row
	}
	if earliest.IsZero() {
		return CashFlowForecast{}, false
	}

	start, partialOnly := first, false
	if begins := MonthOf(earliest); !begins.Before(first) {
		start = begins
		if earliest.Day != 1 {
			if begins == last {
				partialOnly = true
			} else {
				start = begins.Next()
			}
		}
	}

	read := MonthCount(start, last) + 1
	moneyIn, moneyOut := Zero, Zero
	for _, month := range MonthsBetween(start, last) {
		moneyIn = moneyIn.Add(totals[month].In)
		moneyOut = moneyOut.Add(totals[month].Out)
	}
	averageIn, _ := moneyIn.DivInt(read)
	averageOut, _ := moneyOut.DivInt(read)
	averageIn, averageOut = averageIn.Round(), averageOut.Round()

	out := CashFlowForecast{Months: make([]CashFlowMonth, 0, AccountForecastMonths)}
	for offset := range AccountForecastMonths {
		out.Months = append(out.Months, CashFlowMonth{
			Month: MonthOf(today).Shift(offset), In: averageIn, Out: averageOut,
		})
	}
	span := fmt.Sprintf("the last %d complete months (%s through %s)", read, start, last)
	switch {
	case partialOnly:
		span = fmt.Sprintf("its first month (%s), which may not be a full one", last)
	case read == 1:
		span = fmt.Sprintf("its one complete month (%s)", last)
	}
	out.Narrative = fmt.Sprintf("This account's average month over %s: %s in and %s out, "+
		"transfers included.", span, averageIn, averageOut)
	return out, true
}
