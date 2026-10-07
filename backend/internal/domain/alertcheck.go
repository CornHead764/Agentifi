package domain

import (
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/shopspring/decimal"
)

// Deciding that an alert has fired: the caller gathers the facts and this
// decides. Every hit carries a dedupe key, so a fact re-read on every sweep
// notifies once.

// AlertRuleView is one person's settings for one alert, without delivery
// channels.
type AlertRuleView struct {
	Enabled bool
	Paused  bool

	Amount    Money
	HasAmount bool
	Count     int
	HasCount  bool
	// Percent is percent units, not a fraction: 10 means 10%.
	Percent    Rate
	HasPercent bool
}

func (r AlertRuleView) fires() bool { return r.Enabled && !r.Paused }

type AlertTransaction struct {
	ID          ID
	AccountID   ID
	AccountName string
	On          Date
	// Amount keeps the register's sign convention: an expense is negative.
	Amount        Money
	Payee         string
	IsBankFee     bool
	Uncategorized bool
	// Bookkeeping marks a row no one transacted: an asset revaluation or an
	// opening balance (Source.IsCashFlow).
	Bookkeeping bool
}

type AlertAccount struct {
	ID   ID
	Name string
	Kind AccountKind
	// Balance is stored-sign: a card's debt is negative.
	Balance Money
}

type AlertBill struct {
	ID     ID
	Name   string
	DueOn  Date
	Amount Money
}

type AlertWatchlist struct {
	ID     ID
	Name   string
	Spent  Money
	Target Money
}

type AlertInputs struct {
	Today Date
	Rules map[AlertType]AlertRuleView

	// Transactions are the rows new since the last sweep; the whole ledger
	// would re-fire every alert that ever applied.
	Transactions []AlertTransaction
	// Pendings is every charge still pending, at any age: a pending row
	// becomes interesting by staying, so it is not narrowed to that window.
	Pendings []AlertTransaction
	Accounts []AlertAccount
	// Bills are those due inside the coming week.
	Bills      []AlertBill
	Watchlists []AlertWatchlist
	// Expected is everything due over the projection window, income included,
	// or a projected balance predicts a crisis every month.
	Expected []Occurrence
	// Series is every active series, for alerts that read a schedule.
	Series             []Series
	UncategorizedCount int

	// ThisMonth and LastMonth spend are magnitudes.
	ThisMonthSpend  Money
	LastMonthSpend  Money
	ThisMonthIncome Money
	// ExpectedIncome is what the income series predict for the month: the
	// denominator of the bills-to-income share.
	ExpectedIncome Money

	// Fulfilled are recent rows the ledger has matched to a series.
	Fulfilled []AlertFulfilment
	// OverdueRefunds are refunds whose expected day has passed.
	OverdueRefunds []AlertRefund
	Envelopes      []AlertEnvelope
	// Goals are the savings goals not yet complete.
	Goals []AlertGoal
	// AvailableToSpend is the plan's LeftThisMonth. HasAvailableToSpend is
	// false when there is no plan, which is not a plan that says zero.
	AvailableToSpend    Money
	HasAvailableToSpend bool
}

type AlertFulfilment struct {
	TxnID    ID
	SeriesID ID
	Name     string
	// Kind separates a paid bill from a wage arriving: two alerts, not one.
	Kind   SeriesKind
	On     Date
	Amount Money
}

type AlertRefund struct {
	SeriesID   ID
	Name       string
	ExpectedOn Date
	Amount     Money
}

type AlertEnvelope struct {
	ID     ID
	Name   string
	Spent  Money
	Target Money
}

// AlertGoal is one savings goal's standing this month. Needed is the goal
// card's "monthly needed"; an open-ended goal has none (HasNeeded false) and
// is never reminded about.
type AlertGoal struct {
	ID          ID
	Name        string
	Needed      Money
	HasNeeded   bool
	Contributed Money
	IsComplete  bool
}

type AlertHit struct {
	Type  AlertType
	Title string
	Body  string
	URL   string
	// DedupeKey is the alert plus its subject (a transaction id, or a goal
	// and month), so a repeat sweep writes nothing.
	DedupeKey string
	// Ongoing says the hit reports a state named by DedupeKey: it fires once
	// when the state begins and is withdrawn when it ends.
	Ongoing bool
}

// SweptConditions are the alerts the sweep reports as Ongoing; a sweep that no
// longer finds one withdraws it. Connector alerts are Ongoing too, but only the
// connector can say its trouble is over.
var SweptConditions = []AlertType{
	AlertUncategorized, AlertLowBankBalance, AlertHighCardBalance,
	AlertProjectedLowBalance, AlertWatchlistNearingTarget,
}

// EvaluateAlerts decides which alerts have fired, in catalog order. An alert
// with no rule is not evaluated; callers pass rules resolved from the
// catalog's defaults.
func EvaluateAlerts(in AlertInputs) []AlertHit {
	var hits []AlertHit
	for _, definition := range AlertCatalog {
		rule, held := in.Rules[definition.Type]
		if !held || !rule.fires() {
			continue
		}
		hits = append(hits, evaluateOne(definition.Type, rule, in)...)
	}
	return hits
}

func evaluateOne(alert AlertType, rule AlertRuleView, in AlertInputs) []AlertHit {
	switch alert {
	case AlertLargeTransaction:
		return largeTransactions(rule, in)
	case AlertLargeDeposit:
		return largeDeposits(rule, in)
	case AlertBankFee:
		return bankFees(rule, in)
	case AlertUncategorized:
		return uncategorized(rule, in)
	case AlertStalePending:
		return stalePendings(rule, in)
	case AlertLowBankBalance:
		return lowBankBalances(rule, in)
	case AlertHighCardBalance:
		return highCardBalances(rule, in)
	case AlertUpcomingBills:
		return upcomingBills(rule, in)
	case AlertWatchlistNearingTarget:
		return watchlistsNearingTarget(rule, in)
	case AlertProjectedLowBalance:
		return projectedLowBalances(rule, in)
	case AlertBillsToIncomePct:
		return billsToIncome(rule, in)
	case AlertExtraPaycheckMonth:
		return extraPaycheckMonth(rule, in)
	case AlertMonthlySummary:
		return monthlySummary(rule, in)
	case AlertSpendingUpdate:
		return spendingUpdate(rule, in)
	case AlertBillPaid:
		return fulfilments(rule, in, AlertBillPaid)
	case AlertIncomeReceived:
		return fulfilments(rule, in, AlertIncomeReceived)
	case AlertRefundStatus:
		return refundStatus(rule, in)
	case AlertPlannedExpenseNearing:
		return plannedExpensesNearing(rule, in)
	case AlertGoalContribution:
		return goalReminders(rule, in)
	case AlertAvailableToSpendLow:
		return availableToSpendLow(rule, in)
	default:
		// Catalogued but not decided yet; AlertDefinition.Evaluated says so.
		return nil
	}
}

func largeTransactions(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount {
		return nil
	}
	var hits []AlertHit
	for _, txn := range in.Transactions {
		// Magnitude, or every expense is small.
		if txn.Bookkeeping || !txn.Amount.IsExpense() || txn.Amount.Abs().LessThan(rule.Amount) {
			continue
		}
		hits = append(hits, AlertHit{
			Type:      AlertLargeTransaction,
			Title:     fmt.Sprintf("%s at %s", moneyText(txn.Amount.Abs()), payeeOr(txn)),
			Body:      fmt.Sprintf("A transaction of %s on %s.", moneyText(txn.Amount.Abs()), txn.AccountName),
			URL:       "/transactions",
			DedupeKey: key("large_transaction", string(txn.ID)),
		})
	}
	return hits
}

func largeDeposits(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount {
		return nil
	}
	var hits []AlertHit
	for _, txn := range in.Transactions {
		if txn.Bookkeeping || !txn.Amount.IsIncome() || txn.Amount.LessThan(rule.Amount) {
			continue
		}
		hits = append(hits, AlertHit{
			Type:      AlertLargeDeposit,
			Title:     fmt.Sprintf("%s into %s", moneyText(txn.Amount), txn.AccountName),
			Body:      fmt.Sprintf("A deposit from %s.", payeeOr(txn)),
			URL:       "/transactions",
			DedupeKey: key("large_deposit", string(txn.ID)),
		})
	}
	return hits
}

func bankFees(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount {
		return nil
	}
	var hits []AlertHit
	for _, txn := range in.Transactions {
		if !txn.IsBankFee || txn.Amount.Abs().LessThan(rule.Amount) {
			continue
		}
		hits = append(hits, AlertHit{
			Type:      AlertBankFee,
			Title:     fmt.Sprintf("%s fee on %s", moneyText(txn.Amount.Abs()), txn.AccountName),
			Body:      fmt.Sprintf("Charged by %s.", payeeOr(txn)),
			URL:       "/transactions",
			DedupeKey: key("bank_fee", string(txn.ID)),
		})
	}
	return hits
}

func uncategorized(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasCount || in.UncategorizedCount < rule.Count {
		return nil
	}
	return []AlertHit{{
		Type:  AlertUncategorized,
		Title: fmt.Sprintf("%d transactions need a category", in.UncategorizedCount),
		Body:  "The spending plan and reports need them categorized.",
		URL:   "/transactions",
		// The backlog, not the count, or every import re-fires it.
		DedupeKey: key("uncategorized"), Ongoing: true,
	}}
}

// stalePendings reports a charge pending longer than a bank takes to post one.
// A synced pending is retired when the feed stops listing it, but an imported
// or hand-entered one never is, and it counts in balances that add pendings.
// It reports rather than removes: the row may be a real purchase that settled
// at another figure unmatched.
func stalePendings(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasCount || rule.Count <= 0 {
		return nil
	}
	var hits []AlertHit
	for _, txn := range in.Pendings {
		age := DaysBetween(txn.On, in.Today)
		if age < rule.Count {
			continue
		}
		named := strings.TrimSpace(txn.Payee)
		if named == "" {
			named = "A charge"
		}
		hits = append(hits, AlertHit{
			Type:  AlertStalePending,
			Title: fmt.Sprintf("%s has been pending %d days", named, age),
			Body: fmt.Sprintf(
				"%s on %s. Likely settled at another amount or released; check the account.",
				moneyText(txn.Amount.Abs()), txn.AccountName),
			URL: "/transactions",
			// The row, not the row and day, or it repeats every morning.
			DedupeKey: key("stale_pending", string(txn.ID)),
		})
	}
	return hits
}

func lowBankBalances(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount {
		return nil
	}
	var hits []AlertHit
	for _, account := range in.Accounts {
		if account.Kind != KindCash || !account.Balance.LessThan(rule.Amount) {
			continue
		}
		hits = append(hits, AlertHit{
			Type:      AlertLowBankBalance,
			Title:     fmt.Sprintf("%s is down to %s", account.Name, moneyText(account.Balance)),
			Body:      fmt.Sprintf("Below the %s you asked to be told about.", moneyText(rule.Amount)),
			URL:       "/transactions",
			DedupeKey: key("low_bank_balance", string(account.ID)), Ongoing: true,
		})
	}
	return hits
}

func highCardBalances(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount {
		return nil
	}
	var hits []AlertHit
	for _, account := range in.Accounts {
		// A card's debt is stored negative.
		owed := account.Balance.Abs()
		if account.Kind != KindCreditCard || !account.Balance.IsNegative() ||
			!owed.GreaterThan(rule.Amount) {
			continue
		}
		hits = append(hits, AlertHit{
			Type:      AlertHighCardBalance,
			Title:     fmt.Sprintf("%s is at %s", account.Name, moneyText(owed)),
			Body:      fmt.Sprintf("Above the %s you asked to be told about.", moneyText(rule.Amount)),
			URL:       "/transactions",
			DedupeKey: key("high_card_balance", string(account.ID)), Ongoing: true,
		})
	}
	return hits
}

func upcomingBills(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasCount || len(in.Bills) < rule.Count {
		return nil
	}
	total := Zero
	for _, bill := range in.Bills {
		total = total.Add(bill.Amount.Abs())
	}
	return []AlertHit{{
		Type:  AlertUpcomingBills,
		Title: fmt.Sprintf("%s due in the next week", plural(len(in.Bills), "bill")),
		Body:  fmt.Sprintf("%s in total.", moneyText(total)),
		URL:   "/upcoming",
		// Once a week: there are bills on nearly every day.
		DedupeKey: key("upcoming_bills", weekOf(in.Today).String()),
	}}
}

func watchlistsNearingTarget(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasPercent {
		return nil
	}
	var hits []AlertHit
	for _, watchlist := range in.Watchlists {
		if !watchlist.Target.IsPositive() {
			continue
		}
		// Headroom rather than a ratio, so rounding cannot put the boundary
		// on the wrong side of itself.
		headroom := watchlist.Target.Sub(watchlist.Spent)
		margin := watchlist.Target.Scale(percentAsRate(rule.Percent))
		if headroom.GreaterThan(margin) {
			continue
		}
		title := fmt.Sprintf("%s is near its %s target", watchlist.Name, moneyText(watchlist.Target))
		if watchlist.Spent.GreaterThan(watchlist.Target) {
			title = fmt.Sprintf("%s is over its %s target", watchlist.Name, moneyText(watchlist.Target))
		}
		hits = append(hits, AlertHit{
			Type:      AlertWatchlistNearingTarget,
			Title:     title,
			Body:      fmt.Sprintf("%s spent so far.", moneyText(watchlist.Spent)),
			URL:       "/watchlist",
			DedupeKey: key("watchlist_nearing_target", string(watchlist.ID)), Ongoing: true,
		})
	}
	return hits
}

// fulfilments reports a series occurrence the ledger has matched a row to, one
// alert per kind: a bill paid and a wage arriving are opposite news.
func fulfilments(rule AlertRuleView, in AlertInputs, which AlertType) []AlertHit {
	var hits []AlertHit
	income := which == AlertIncomeReceived
	for _, one := range in.Fulfilled {
		// An allowlist: transfers, card payments and refunds are not money
		// leaving, and a kind added later stays silent until assigned.
		if income {
			if one.Kind != SeriesIncome {
				continue
			}
		} else if one.Kind != SeriesBill && one.Kind != SeriesSubscription {
			continue
		}

		title := fmt.Sprintf("%s paid — %s", one.Name, moneyText(one.Amount.Abs()))
		if income {
			title = fmt.Sprintf("%s arrived — %s", one.Name, moneyText(one.Amount))
		}
		hits = append(hits, AlertHit{
			Type: which, Title: title,
			Body:      fmt.Sprintf("On %s.", one.On),
			URL:       "/upcoming",
			DedupeKey: key(string(which), string(one.TxnID)),
		})
	}
	return hits
}

// refundStatus reports only late refunds: an arrived one is already visible in
// the register.
func refundStatus(rule AlertRuleView, in AlertInputs) []AlertHit {
	var hits []AlertHit
	for _, one := range in.OverdueRefunds {
		hits = append(hits, AlertHit{
			Type:  AlertRefundStatus,
			Title: fmt.Sprintf("%s has not arrived", one.Name),
			Body: fmt.Sprintf("%s was expected on %s.",
				moneyText(one.Amount.Abs()), one.ExpectedOn),
			URL:       "/upcoming",
			DedupeKey: key("refund_status", string(one.SeriesID), one.ExpectedOn.String()),
		})
	}
	return hits
}

// plannedExpensesNearing warns as an envelope approaches its limit, by
// headroom as the watchlist alert does.
func plannedExpensesNearing(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasPercent {
		return nil
	}
	var hits []AlertHit
	for _, one := range in.Envelopes {
		if !one.Target.IsPositive() {
			continue
		}
		headroom := one.Target.Sub(one.Spent)
		if headroom.GreaterThan(one.Target.Scale(percentAsRate(rule.Percent))) {
			continue
		}
		title := fmt.Sprintf("%s is near its %s limit", one.Name, moneyText(one.Target))
		if one.Spent.GreaterThan(one.Target) {
			title = fmt.Sprintf("%s is over its %s limit", one.Name, moneyText(one.Target))
		}
		hits = append(hits, AlertHit{
			Type: AlertPlannedExpenseNearing, Title: title,
			Body:      fmt.Sprintf("%s spent so far this month.", moneyText(one.Spent)),
			URL:       "/spending-plan",
			DedupeKey: key("planned_expense_nearing", string(one.ID), MonthOf(in.Today).String()),
		})
	}
	return hits
}

// availableToSpendLow warns when the plan's LeftThisMonth falls to the
// threshold or less (a line at zero means the moment it reaches zero). Once per
// month, since the figure moves with every transaction.
func availableToSpendLow(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount || !in.HasAvailableToSpend {
		return nil
	}
	if in.AvailableToSpend.GreaterThan(rule.Amount) {
		return nil
	}

	left := in.AvailableToSpend
	title := fmt.Sprintf("%s left to spend this month", moneyText(left))
	if left.IsNegative() {
		title = fmt.Sprintf("The spending plan is over by %s", moneyText(left.Abs()))
	}
	return []AlertHit{{
		Type:      AlertAvailableToSpendLow,
		Title:     title,
		Body:      fmt.Sprintf("Your alert is set at %s.", moneyText(rule.Amount)),
		URL:       "/spending-plan",
		DedupeKey: key("available_to_spend_low", MonthOf(in.Today).String()),
	}}
}

// goalReminders nudges a goal behind its own monthly rate: it reads the
// shortfall, so a goal that has had its month's money is silent. Once per goal
// per month.
func goalReminders(_ AlertRuleView, in AlertInputs) []AlertHit {
	var hits []AlertHit
	for _, one := range in.Goals {
		// No target date implies no rate; a finished goal needs nothing.
		if !one.HasNeeded || one.IsComplete || !one.Needed.IsPositive() {
			continue
		}
		short := one.Needed.Sub(one.Contributed)
		if !short.IsPositive() {
			continue
		}

		body := fmt.Sprintf("%s of %s so far this month.",
			moneyText(one.Contributed), moneyText(one.Needed))
		if !one.Contributed.IsPositive() {
			body = fmt.Sprintf("Nothing in yet this month; %s needed.",
				moneyText(one.Needed))
		}
		hits = append(hits, AlertHit{
			Type:      AlertGoalContribution,
			Title:     fmt.Sprintf("%s needs %s this month", one.Name, moneyText(short)),
			Body:      body,
			URL:       "/goals",
			DedupeKey: key("goal_contribution", string(one.ID), MonthOf(in.Today).String()),
		})
	}
	return hits
}

// ProjectionDays is how far ahead the projected-balance alert looks.
const ProjectionDays = 30

// projectedLowBalances warns before a balance falls, using the cash-flow
// chart's projection so the two agree on the day. An account already below the
// line is left to the low-balance alert.
func projectedLowBalances(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasAmount || len(in.Expected) == 0 {
		return nil
	}
	var hits []AlertHit
	for _, account := range in.Accounts {
		if account.Kind != KindCash || account.Balance.LessThan(rule.Amount) {
			continue
		}
		due := make([]Occurrence, 0, len(in.Expected))
		for _, one := range in.Expected {
			if one.AccountID == account.ID {
				due = append(due, one)
			}
		}
		if len(due) == 0 {
			continue
		}

		projection := ProjectBalances(account.Balance, due, in.Today,
			in.Today.AddDays(ProjectionDays))
		point, falls := projection.FirstBelow(rule.Amount)
		if !falls {
			continue
		}
		hits = append(hits, AlertHit{
			Type:  AlertProjectedLowBalance,
			Title: fmt.Sprintf("%s is heading for %s", account.Name, moneyText(point.Balance)),
			Body: fmt.Sprintf("On %s, after what is due. %s today.",
				point.On, moneyText(account.Balance)),
			URL:       "/upcoming",
			DedupeKey: key("projected_low_balance", string(account.ID)), Ongoing: true,
		})
	}
	return hits
}

// billsToIncome is what share of the month's expected income the bills take.
func billsToIncome(rule AlertRuleView, in AlertInputs) []AlertHit {
	if !rule.HasPercent || !in.ExpectedIncome.IsPositive() {
		return nil
	}
	bills := Zero
	for _, one := range in.Bills {
		bills = bills.Add(one.Amount.Abs())
	}
	share, ok := Percent(bills, in.ExpectedIncome)
	if !ok || share.LessThan(rule.Percent) {
		return nil
	}
	return []AlertHit{{
		Type: AlertBillsToIncomePct,
		Title: fmt.Sprintf("Bills are %s%% of this month's income",
			share.Round(0).String()),
		Body: fmt.Sprintf("%s of bills against %s expected.",
			moneyText(bills), moneyText(in.ExpectedIncome)),
		URL:       "/upcoming",
		DedupeKey: key("bills_to_income_pct", MonthOf(in.Today).String()),
	}}
}

// extraPaycheckMonth spots a month holding one more payday than the series'
// usual (ExtraOccurrenceMonths, not a fixed three, which a weekly wage always
// clears). Counted from the whole month's schedule, so a landed payday counts.
func extraPaycheckMonth(rule AlertRuleView, in AlertInputs) []AlertHit {
	month := MonthOf(in.Today)
	for _, series := range in.Series {
		if series.Kind != SeriesIncome {
			continue
		}
		if !slices.Contains(ExtraOccurrenceMonths(series.Recurrence, series.StartOn, month.Year, series.EndOn), month) {
			continue
		}
		times := OccurrencesByMonth(series.Recurrence, series.StartOn, month.Year, series.EndOn)[month]
		return []AlertHit{{
			Type:      AlertExtraPaycheckMonth,
			Title:     "An extra payday this month",
			Body:      fmt.Sprintf("One of your income series pays %d times in %s.", times, month),
			URL:       "/upcoming",
			DedupeKey: key("extra_paycheck_month", string(series.ID), month.String()),
		}}
	}
	return nil
}

// monthlySummary is the month just gone, sent in the first days of the next
// and keyed on the month it covers.
func monthlySummary(rule AlertRuleView, in AlertInputs) []AlertHit {
	if in.Today.Day > 3 || !in.LastMonthSpend.IsPositive() {
		return nil
	}
	last := MonthOf(in.Today).Shift(-1)
	return []AlertHit{{
		Type:      AlertMonthlySummary,
		Title:     fmt.Sprintf("%s: %s spent", last, moneyText(in.LastMonthSpend)),
		Body:      "Open Reports for where it went.",
		URL:       "/reports",
		DedupeKey: key("monthly_summary", last.String()),
	}}
}

// spendingUpdate compares this month with the last one at the same point.
func spendingUpdate(rule AlertRuleView, in AlertInputs) []AlertHit {
	// Not in the first days of a month: too little to compare.
	if in.Today.Day < 7 || !in.LastMonthSpend.IsPositive() {
		return nil
	}
	difference := in.ThisMonthSpend.Sub(in.LastMonthSpend)
	direction := "more than"
	if difference.IsNegative() {
		direction = "less than"
	}
	return []AlertHit{{
		Type: AlertSpendingUpdate,
		Title: fmt.Sprintf("%s %s last month so far",
			moneyText(difference.Abs()), direction),
		Body: fmt.Sprintf("%s this month against %s all of last month.",
			moneyText(in.ThisMonthSpend), moneyText(in.LastMonthSpend)),
		URL: "/reports",
		// Once a week: the comparison moves with every purchase.
		DedupeKey: key("spending_update", weekOf(in.Today).String()),
	}}
}

// A mailbox outage is worth telling once it has failed at least
// MailboxOutageFailures polls in a row over at least MailboxOutageAfter: one
// failed poll is usually the network, and quick manual retries are not an
// outage.
const (
	MailboxOutageFailures = 2
	MailboxOutageAfter    = time.Hour
)

// MailboxOutageWorthTelling decides from the failing streak so far.
func MailboxOutageWorthTelling(failures int, failingFor time.Duration) bool {
	return failures >= MailboxOutageFailures && failingFor >= MailboxOutageAfter
}

// weekOf is the Monday that starts the week holding a day.
func weekOf(day Date) Date {
	return day.AddDays(-((int(day.Time().Weekday()) + 6) % 7))
}

// percentAsRate turns percent units into the fraction Scale wants: 10 -> 0.1.
func percentAsRate(percent Rate) Rate {
	return percent.Div(decimal.NewFromInt(100))
}

// moneyText writes an amount the way the register does ("$6,543.21"), since
// an alert is read next to the figure it is about.
func moneyText(m Money) string {
	text := m.Abs().String()
	whole, fraction, _ := strings.Cut(text, ".")

	var grouped strings.Builder
	for i, digit := range whole {
		if i > 0 && (len(whole)-i)%3 == 0 {
			grouped.WriteByte(',')
		}
		grouped.WriteRune(digit)
	}

	sign := ""
	if m.IsNegative() {
		sign = "-"
	}
	return sign + "$" + grouped.String() + "." + fraction
}

func payeeOr(txn AlertTransaction) string {
	if txn.Payee != "" {
		return txn.Payee
	}
	return txn.AccountName
}

func plural(n int, word string) string {
	if n == 1 {
		return fmt.Sprintf("1 %s", word)
	}
	return fmt.Sprintf("%d %ss", n, word)
}

func key(parts ...string) string {
	out := ""
	for i, part := range parts {
		if i > 0 {
			out += ":"
		}
		out += part
	}
	return out
}
