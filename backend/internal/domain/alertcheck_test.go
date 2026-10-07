package domain_test

import (
	"strings"
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Every test here is about a boundary or a sign: the two ways an alert fires
// for everything or never fires at all.

var alertDay = domain.Date{Year: 2026, Month: 8, Day: 22}

func amountRule(amount string) domain.AlertRuleView {
	return domain.AlertRuleView{
		Enabled: true, Amount: domain.MustFromString(amount), HasAmount: true,
	}
}

func countRule(count int) domain.AlertRuleView {
	return domain.AlertRuleView{Enabled: true, Count: count, HasCount: true}
}

func txn(id string, amount string, overrides ...func(*domain.AlertTransaction)) domain.AlertTransaction {
	one := domain.AlertTransaction{
		ID: domain.ID(id), AccountID: "acct", AccountName: "Everyday Checking",
		On: alertDay, Amount: domain.MustFromString(amount), Payee: "Safeway",
	}
	for _, apply := range overrides {
		apply(&one)
	}
	return one
}

func TestALargeTransactionIsJudgedOnItsMagnitudeNotItsSign(t *testing.T) {
	// Expenses are stored negative; comparing the signed amount would make
	// every expense small.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: amountRule("500.00"),
		},
		Transactions: []domain.AlertTransaction{txn("t1", "-600.00")},
	})
	require.Len(t, hits, 1)
	require.Equal(t, domain.AlertLargeTransaction, hits[0].Type)
	require.Contains(t, hits[0].Title, "$600.00")
}

func TestTheThresholdIsInclusiveAndTheOneBelowItIsNot(t *testing.T) {
	// "At or above this amount": exactly the threshold fires, a cent under
	// does not.
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: amountRule("500.00"),
		},
		Transactions: []domain.AlertTransaction{
			txn("exact", "-500.00"), txn("under", "-499.99"),
		},
	}
	hits := domain.EvaluateAlerts(in)
	require.Len(t, hits, 1)
	require.Equal(t, "large_transaction:exact", hits[0].DedupeKey)
}

func TestADepositDoesNotFireTheLargeTransactionAlert(t *testing.T) {
	// A large expense is not a deposit.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: amountRule("500.00"),
			domain.AlertLargeDeposit:     amountRule("500.00"),
		},
		Transactions: []domain.AlertTransaction{
			txn("spend", "-900.00"), txn("paid", "2400.00"),
		},
	})
	require.Len(t, hits, 2)
	byType := map[domain.AlertType]domain.AlertHit{}
	for _, hit := range hits {
		byType[hit.Type] = hit
	}
	require.Equal(t, "large_transaction:spend", byType[domain.AlertLargeTransaction].DedupeKey)
	require.Equal(t, "large_deposit:paid", byType[domain.AlertLargeDeposit].DedupeKey)
}

func TestARevaluationFiresNeitherTheLargeTransactionNorTheLargeDepositAlert(t *testing.T) {
	// A market-value adjustment moves net worth; nobody spent or received it.
	revaluation := func(one *domain.AlertTransaction) {
		one.Bookkeeping = true
		one.Payee = "Revaluation"
	}
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: amountRule("500.00"),
			domain.AlertLargeDeposit:     amountRule("500.00"),
		},
		Transactions: []domain.AlertTransaction{
			txn("down", "-3200.00", revaluation),
			txn("up", "4100.00", revaluation),
			txn("spend", "-900.00"),
		},
	})
	require.Len(t, hits, 1)
	require.Equal(t, "large_transaction:spend", hits[0].DedupeKey)
}

func TestOnlyABankFeeFiresTheBankFeeAlert(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertBankFee: amountRule("5.00"),
		},
		Transactions: []domain.AlertTransaction{
			txn("fee", "-12.00", func(one *domain.AlertTransaction) { one.IsBankFee = true }),
			txn("shopping", "-400.00"),
		},
	})
	require.Len(t, hits, 1)
	require.Equal(t, "bank_fee:fee", hits[0].DedupeKey)
}

func TestAPausedAlertDecidesNothing(t *testing.T) {
	// Pause stops the decision, not just the delivery.
	rule := amountRule("500.00")
	rule.Paused = true
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:        alertDay,
		Rules:        map[domain.AlertType]domain.AlertRuleView{domain.AlertLargeTransaction: rule},
		Transactions: []domain.AlertTransaction{txn("t1", "-9000.00")},
	})
	require.Empty(t, hits)
}

func TestADisabledAlertDecidesNothing(t *testing.T) {
	rule := amountRule("500.00")
	rule.Enabled = false
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:        alertDay,
		Rules:        map[domain.AlertType]domain.AlertRuleView{domain.AlertLargeTransaction: rule},
		Transactions: []domain.AlertTransaction{txn("t1", "-9000.00")},
	})
	require.Empty(t, hits)
}

func TestAThresholdlessRuleFiresNothingRatherThanEverything(t *testing.T) {
	// A rule with no amount is silent rather than comparing against zero.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: {Enabled: true},
		},
		Transactions: []domain.AlertTransaction{txn("t1", "-4.00")},
	})
	require.Empty(t, hits)
}

func TestALowBalanceReadsCashAccountsOnly(t *testing.T) {
	// A card at -$400 is not a bank account about to bounce.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLowBankBalance: amountRule("200.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Everyday Checking", Kind: domain.KindCash,
				Balance: domain.MustFromString("150.00")},
			{ID: "a2", Name: "Apple Card", Kind: domain.KindCreditCard,
				Balance: domain.MustFromString("-400.00")},
			{ID: "a3", Name: "Rainy Day Fund", Kind: domain.KindCash,
				Balance: domain.MustFromString("21471.19")},
		},
	})
	require.Len(t, hits, 1)
	require.Equal(t, "low_bank_balance:a1", hits[0].DedupeKey)
	require.True(t, hits[0].Ongoing)
}

func TestAHighCardBalanceReadsTheDebtsMagnitude(t *testing.T) {
	// Debt is stored negative; comparing the stored figure would fire when a
	// card is paid off.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertHighCardBalance: amountRule("5000.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "c1", Name: "Alex's Visa", Kind: domain.KindCreditCard,
				Balance: domain.MustFromString("-6543.21")},
			{ID: "c2", Name: "Apple Card", Kind: domain.KindCreditCard,
				Balance: domain.MustFromString("-174.00")},
		},
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "$6,543.21")
	require.Equal(t, "high_card_balance:c1", hits[0].DedupeKey)
	require.True(t, hits[0].Ongoing)
}

func TestAPendingChargeIsReportedOnlyOnceItIsOlderThanTheThreshold(t *testing.T) {
	// A young pending row is ordinary; the threshold is the whole judgement.
	fresh := txn("fresh", "-40.00", func(one *domain.AlertTransaction) {
		one.On = alertDay.AddDays(-13)
	})
	stale := txn("stale", "-1234.50", func(one *domain.AlertTransaction) {
		one.On = alertDay.AddDays(-14)
		one.Payee = "Fuel Stop"
	})
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:    alertDay,
		Rules:    map[domain.AlertType]domain.AlertRuleView{domain.AlertStalePending: countRule(14)},
		Pendings: []domain.AlertTransaction{fresh, stale},
	})

	require.Len(t, hits, 1, "a pending row younger than the threshold was reported")
	require.Equal(t, domain.AlertStalePending, hits[0].Type)
	require.Contains(t, hits[0].Title, "Fuel Stop")
	require.Contains(t, hits[0].Title, "14 days")
	require.Contains(t, hits[0].Body, "Everyday Checking")
	require.True(t, strings.HasPrefix(hits[0].Body, "$1,234.50 on "),
		"the amount is written as money, as the other alert bodies write it: %q", hits[0].Body)
}

func TestAStalePendingIsReportedOncePerRowNotOncePerDay(t *testing.T) {
	// Keyed on the row, not the day, or it repeats every morning.
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertStalePending: countRule(14)},
		Pendings: []domain.AlertTransaction{txn("p1", "-1.00", func(one *domain.AlertTransaction) {
			one.On = alertDay.AddDays(-20)
		})},
	}
	first := domain.EvaluateAlerts(in)
	in.Today = alertDay.AddDays(1)
	later := domain.EvaluateAlerts(in)

	require.Len(t, first, 1)
	require.Len(t, later, 1)
	require.Equal(t, first[0].DedupeKey, later[0].DedupeKey,
		"the same stuck charge would be announced again tomorrow")
}

func TestAPendingRowIsNotJudgedByTheTransactionWindow(t *testing.T) {
	// The other transaction alerts read a seven-day window a stale pending
	// is always older than, so it has its own input.
	stale := txn("old", "-478.00", func(one *domain.AlertTransaction) {
		one.On = alertDay.AddDays(-60)
	})
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:    alertDay,
		Rules:    map[domain.AlertType]domain.AlertRuleView{domain.AlertStalePending: countRule(14)},
		Pendings: []domain.AlertTransaction{stale},
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "60 days")
}

func TestUncategorizedIsOneStandingBacklogRatherThanOnePerTransaction(t *testing.T) {
	// A key carrying the count would fire again on each import.
	in := domain.AlertInputs{
		Today:              alertDay,
		Rules:              map[domain.AlertType]domain.AlertRuleView{domain.AlertUncategorized: countRule(5)},
		UncategorizedCount: 73,
	}
	hits := domain.EvaluateAlerts(in)
	require.Len(t, hits, 1)
	require.Equal(t, "uncategorized", hits[0].DedupeKey)
	require.True(t, hits[0].Ongoing)
	require.Contains(t, hits[0].Title, "73")

	in.UncategorizedCount = 4
	require.Empty(t, domain.EvaluateAlerts(in), "under the count is silence")
}

func TestAWatchlistFiresWithinItsMarginAndOverIt(t *testing.T) {
	// "Within 10% of target or over": 90% fires, 89% does not, and past the
	// target says over.
	rule := domain.AlertRuleView{
		Enabled: true, Percent: decimal.RequireFromString("10"), HasPercent: true,
	}
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertWatchlistNearingTarget: rule},
		Watchlists: []domain.AlertWatchlist{
			{ID: "w1", Name: "Groceries", Spent: domain.MustFromString("900.00"),
				Target: domain.MustFromString("1000.00")},
			{ID: "w2", Name: "Fuel", Spent: domain.MustFromString("889.99"),
				Target: domain.MustFromString("1000.00")},
			{ID: "w3", Name: "Dining", Spent: domain.MustFromString("1200.00"),
				Target: domain.MustFromString("1000.00")},
		},
	})
	require.Len(t, hits, 2)
	require.Contains(t, hits[0].Title, "Groceries is near")
	require.Contains(t, hits[1].Title, "Dining is over")
}

func TestUpcomingBillsTotalsWhatIsDue(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertUpcomingBills: countRule(2)},
		Bills: []domain.AlertBill{
			{ID: "b1", Name: "Mortgage", DueOn: alertDay.AddDays(2),
				Amount: domain.MustFromString("-1800.00")},
			{ID: "b2", Name: "Phone", DueOn: alertDay.AddDays(5),
				Amount: domain.MustFromString("-54.00")},
		},
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "2 bills")
	require.Contains(t, hits[0].Body, "$1,854.00")
}

func TestAnAlertNobodyHasARuleForIsNotEvaluated(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:        alertDay,
		Rules:        map[domain.AlertType]domain.AlertRuleView{},
		Transactions: []domain.AlertTransaction{txn("t1", "-9000.00")},
	})
	require.Empty(t, hits)
}

// decided is every alert EvaluateAlerts has a rule for. Not read off
// AlertDefinition.Evaluated, which is about firing end to end.
var decided = []domain.AlertType{
	domain.AlertLargeTransaction, domain.AlertLargeDeposit, domain.AlertBankFee,
	domain.AlertUncategorized, domain.AlertLowBankBalance, domain.AlertHighCardBalance,
	domain.AlertUpcomingBills, domain.AlertWatchlistNearingTarget,
	domain.AlertProjectedLowBalance, domain.AlertBillsToIncomePct,
	domain.AlertExtraPaycheckMonth, domain.AlertMonthlySummary, domain.AlertSpendingUpdate,
	domain.AlertBillPaid, domain.AlertIncomeReceived, domain.AlertRefundStatus,
	domain.AlertPlannedExpenseNearing, domain.AlertGoalContribution,
	domain.AlertAvailableToSpendLow, domain.AlertStalePending,
}

// unfed is the subset of decided whose inputs service.alerts.gather does not
// supply: an alert the sweep cannot feed must not be offered as if it worked.
var unfed = []domain.AlertType{}

// raisedByAConnector is the alerts nothing here decides: the bills bridge
// fires them when a pull parks a challenge or stops, and the merchant
// connector when an order pull stops at a sign-in.
var raisedByAConnector = map[domain.AlertType]bool{
	domain.AlertAmazonSignIn:          true,
	domain.AlertCostcoSignIn:          true,
	domain.AlertBillChallenge:         true,
	domain.AlertBillPullFailed:        true,
	domain.AlertMerchantPullFailed:    true,
	domain.AlertEmailConnectionFailed: true,
	domain.AlertBackupFailed:          true,
}

func init() {
	fed := map[domain.AlertType]bool{}
	for _, one := range decided {
		fed[one] = true
	}
	for _, one := range unfed {
		if !fed[one] {
			panic("unfed names " + string(one) + ", which no evaluator decides")
		}
		delete(fed, one)
	}
}

func TestTheUnbuiltAlertsDecideNothingYet(t *testing.T) {
	// Every alert not decided here must stay silent even when handed inputs
	// that would satisfy it, or the settings page offers dead switches.
	rules := map[domain.AlertType]domain.AlertRuleView{}
	for _, one := range domain.AlertCatalog {
		rules[one.Type] = domain.AlertRuleView{
			Enabled: true,
			Amount:  domain.MustFromString("0.01"), HasAmount: true,
			Count: 1, HasCount: true,
			Percent: decimal.RequireFromString("100"), HasPercent: true,
		}
	}
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay, Rules: rules,
		// One row of each sign: spend alerts and the deposit alert read
		// opposite halves of the ledger.
		Transactions: []domain.AlertTransaction{
			txn("t1", "-9000.00", func(one *domain.AlertTransaction) { one.IsBankFee = true }),
			txn("t2", "2400.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Checking", Kind: domain.KindCash, Balance: domain.Zero},
			{ID: "c1", Name: "Card", Kind: domain.KindCreditCard,
				Balance: domain.MustFromString("-1.00")},
		},
		Bills: []domain.AlertBill{{ID: "b1", Name: "Rent", DueOn: alertDay,
			Amount: domain.MustFromString("-10.00")}},
		Watchlists: []domain.AlertWatchlist{{ID: "w1", Name: "Food",
			Spent: domain.MustFromString("1.00"), Target: domain.MustFromString("2.00")}},
		UncategorizedCount: 3,
	})

	fired := map[domain.AlertType]bool{}
	for _, hit := range hits {
		fired[hit.Type] = true
	}
	wanted := map[domain.AlertType]bool{}
	for _, one := range decided {
		wanted[one] = true
	}
	unfedType := map[domain.AlertType]bool{}
	for _, one := range unfed {
		unfedType[one] = true
	}
	for _, one := range domain.AlertCatalog {
		// Only the one direction: some decided alerts are exclusive in time
		// (monthly summary, spending update), so not all fire at once.
		if !wanted[one.Type] {
			require.False(t, fired[one.Type],
				"%q fired, and nothing is supposed to decide it", one.Type)
		}
		fires := (wanted[one.Type] && !unfedType[one.Type]) || raisedByAConnector[one.Type]
		require.Equal(t, fires, one.Evaluated,
			"%q: the catalogue says Evaluated=%v and the evaluator-and-sweep say %v",
			one.Type, one.Evaluated, fires)
	}
}

func TestAnAmountInAnAlertReadsLikeTheRegisters(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLargeTransaction: amountRule("500.00"),
		},
		Transactions: []domain.AlertTransaction{
			txn("small", "-600.00"), txn("big", "-1234567.89"),
		},
	})
	require.Len(t, hits, 2)
	// The whole title: "$600.00" is contained in "-$600.00" too.
	require.Equal(t, "$600.00 at Safeway", hits[0].Title)
	require.Equal(t, "$1,234,567.89 at Safeway", hits[1].Title)
}

func percentRule(percent string) domain.AlertRuleView {
	return domain.AlertRuleView{
		Enabled: true, Percent: decimal.RequireFromString(percent), HasPercent: true,
	}
}

func TestAProjectedLowBalanceWarnsBeforeItHappens(t *testing.T) {
	// £900 today with £1,000 of rent due next week is worth hearing now.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertProjectedLowBalance: amountRule("500.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Everyday Checking", Kind: domain.KindCash,
				Balance: amt("900.00")},
		},
		Expected: []domain.Occurrence{
			{SeriesID: "rent", AccountID: "a1", DueOn: alertDay.AddDays(6),
				Amount: amt("-1000.00")},
		},
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "Everyday Checking is heading for")
	require.Contains(t, hits[0].Body, "$900.00 today")
}

func TestAnAccountAlreadyBelowTheLineIsNotWarnedTwice(t *testing.T) {
	// The low-balance alert already covers it.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertProjectedLowBalance: amountRule("500.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Everyday Checking", Kind: domain.KindCash, Balance: amt("100.00")},
		},
		Expected: []domain.Occurrence{
			{SeriesID: "rent", AccountID: "a1", DueOn: alertDay.AddDays(6),
				Amount: amt("-50.00")},
		},
	})
	require.Empty(t, hits)
}

func TestIncomeDueIsCountedInTheProjection(t *testing.T) {
	// A projection counting only what leaves would predict a crisis before
	// every payday.
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertProjectedLowBalance: amountRule("500.00"),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Everyday Checking", Kind: domain.KindCash, Balance: amt("900.00")},
		},
		Expected: []domain.Occurrence{
			{SeriesID: "pay", AccountID: "a1", DueOn: alertDay.AddDays(2),
				Amount: amt("2400.00")},
			{SeriesID: "rent", AccountID: "a1", DueOn: alertDay.AddDays(6),
				Amount: amt("-1000.00")},
		},
	})
	require.Empty(t, hits, "the wage arrives before the rent leaves")
}

func TestBillsAreReportedAsAShareOfExpectedIncome(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertBillsToIncomePct: percentRule("50"),
		},
		Bills: []domain.AlertBill{
			{ID: "b1", Name: "Rent", DueOn: alertDay, Amount: amt("-1800.00")},
		},
		ExpectedIncome: amt("2400.00"),
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "75%")
	require.Contains(t, hits[0].Body, "$1,800.00")
}

func TestABillsShareUnderTheThresholdSaysNothing(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertBillsToIncomePct: percentRule("50"),
		},
		Bills:          []domain.AlertBill{{ID: "b1", Name: "Phone", Amount: amt("-54.00")}},
		ExpectedIncome: amt("2400.00"),
	})
	require.Empty(t, hits)
}

func paydays(recurrence domain.Recurrence, startOn domain.Date) []domain.AlertHit {
	return domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertExtraPaycheckMonth: {Enabled: true}},
		Series: []domain.Series{{
			ID: "pay", AccountID: "a1", Kind: domain.SeriesIncome, Amount: amt("1200.00"),
			Recurrence: recurrence, StartOn: startOn, IsActive: true,
		}},
	})
}

func TestAThirdPaydayInOneMonthIsWorthKnowing(t *testing.T) {
	// A fortnightly wage: August paydays 1, 15, 29.
	hits := paydays(domain.EveryXDays(14), domain.Date{Year: 2026, Month: 8, Day: 1})

	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "extra payday")
	require.Contains(t, hits[0].Body, "3 times")
}

func TestAPaydayAlreadyPaidStillCountsTowardTheMonth(t *testing.T) {
	// The 22nd, with two paydays behind it: the month still holds three.
	require.Len(t, paydays(domain.EveryXDays(14), domain.Date{Year: 2026, Month: 7, Day: 18}), 1)
}

func TestMoneyExpectedBackIsNotAPayday(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertExtraPaycheckMonth: {Enabled: true}},
		Series: []domain.Series{{
			ID: "refund", AccountID: "a1", Kind: domain.SeriesRefund, Amount: amt("1200.00"),
			Recurrence: domain.EveryXDays(14), StartOn: domain.Date{Year: 2026, Month: 8, Day: 1}, IsActive: true,
		}},
	})

	require.Empty(t, hits)
}

func TestTwoPaydaysInAMonthIsJustAMonth(t *testing.T) {
	require.Empty(t, paydays(domain.EveryXDays(14), domain.Date{Year: 2026, Month: 8, Day: 8}))
}

func TestAWeeklyWageIsNotAnExtraPaydayEveryMonth(t *testing.T) {
	// Four Fridays is the usual month for a weekly wage.
	require.Empty(t, paydays(domain.EveryWeek(), domain.Date{Year: 2026, Month: 1, Day: 2}))
}

func TestTheMonthlySummaryArrivesOnlyOnceTheMonthIsOver(t *testing.T) {
	// A summary of a month still running is not a summary.
	rules := map[domain.AlertType]domain.AlertRuleView{domain.AlertMonthlySummary: {Enabled: true}}

	mid := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay, Rules: rules, LastMonthSpend: amt("2100.00"),
	})
	require.Empty(t, mid, "the 22nd is not the time to summarise last month")

	first := domain.EvaluateAlerts(domain.AlertInputs{
		Today:          domain.Date{Year: 2026, Month: 9, Day: 2},
		Rules:          rules,
		LastMonthSpend: amt("2100.00"),
	})
	require.Len(t, first, 1)
	require.Contains(t, first[0].Title, "2026-08")
	require.Contains(t, first[0].Title, "$2,100.00")
	require.Equal(t, "monthly_summary:2026-08", first[0].DedupeKey, "once for the month it covers")
}

func TestTheSpendingComparisonWaitsUntilThereIsSomethingToCompare(t *testing.T) {
	// Two days against a whole month says nothing.
	rules := map[domain.AlertType]domain.AlertRuleView{domain.AlertSpendingUpdate: {Enabled: true}}

	early := domain.EvaluateAlerts(domain.AlertInputs{
		Today:          domain.Date{Year: 2026, Month: 8, Day: 2},
		Rules:          rules,
		ThisMonthSpend: amt("100.00"), LastMonthSpend: amt("2100.00"),
	})
	require.Empty(t, early)

	later := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay, Rules: rules,
		ThisMonthSpend: amt("2400.00"), LastMonthSpend: amt("2100.00"),
	})
	require.Len(t, later, 1)
	require.Contains(t, later[0].Title, "$300.00 more than")
}

// plainRule is a ThresholdNone alert's configuration.
func plainRule() domain.AlertRuleView { return domain.AlertRuleView{Enabled: true} }

func fulfilment(kind domain.SeriesKind, name, amount string) domain.AlertFulfilment {
	return domain.AlertFulfilment{
		TxnID: domain.ID("txn-" + name), SeriesID: domain.ID("series-" + name),
		Name: name, Kind: kind, On: alertDay, Amount: domain.MustFromString(amount),
	}
}

// A paid bill and arrived income must not report each other's rows.
func TestABillPaidAndIncomeReceivedReadOppositeHalvesOfTheSchedule(t *testing.T) {
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertBillPaid:       plainRule(),
			domain.AlertIncomeReceived: plainRule(),
		},
		Fulfilled: []domain.AlertFulfilment{
			fulfilment(domain.SeriesBill, "Mortgage", "-1800.00"),
			fulfilment(domain.SeriesIncome, "Payroll", "3200.00"),
		},
	}
	hits := domain.EvaluateAlerts(in)
	require.Len(t, hits, 2)

	byType := map[domain.AlertType]domain.AlertHit{}
	for _, hit := range hits {
		byType[hit.Type] = hit
	}
	// The magnitude: "Mortgage paid — -$1,800.00" reads as money coming back.
	require.Equal(t, "Mortgage paid — $1,800.00", byType[domain.AlertBillPaid].Title)
	require.Equal(t, "Payroll arrived — $3,200.00", byType[domain.AlertIncomeReceived].Title)
}

// The allowlist: transfers, card payments and refunds are not bills paid.
func TestOnlyBillsAndSubscriptionsCountAsABillPaid(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertBillPaid: plainRule()},
		Fulfilled: []domain.AlertFulfilment{
			fulfilment(domain.SeriesTransfer, "To Savings", "-500.00"),
			fulfilment(domain.SeriesCreditCardPayment, "Card Payment", "-300.00"),
			fulfilment("refund", "Returned Jacket", "89.00"),
			fulfilment(domain.SeriesSubscription, "Streaming", "-12.00"),
		},
	})
	require.Len(t, hits, 1)
	require.Contains(t, hits[0].Title, "Streaming paid")
}

// Each fulfilment dedupes on its own transaction: a second sweep writes
// nothing while two payments of the same bill both land.
func TestEachFulfilmentDedupesOnItsOwnTransaction(t *testing.T) {
	first := fulfilment(domain.SeriesBill, "Water", "-40.00")
	second := first
	second.TxnID = "txn-water-2"

	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today:     alertDay,
		Rules:     map[domain.AlertType]domain.AlertRuleView{domain.AlertBillPaid: plainRule()},
		Fulfilled: []domain.AlertFulfilment{first, second},
	})
	require.Len(t, hits, 2)
	require.NotEqual(t, hits[0].DedupeKey, hits[1].DedupeKey)
}

// The key carries the expected day, so two late refunds both report and a
// second sweep repeats neither.
func TestAnOverdueRefundReportsThePendingAmountAndTheDayItWasDue(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertRefundStatus: plainRule()},
		OverdueRefunds: []domain.AlertRefund{
			{SeriesID: "r1", Name: "Returned jacket", ExpectedOn: alertDay.AddDays(-5),
				Amount: domain.MustFromString("89.00")},
			{SeriesID: "r1", Name: "Returned jacket", ExpectedOn: alertDay.AddDays(-12),
				Amount: domain.MustFromString("120.00")},
		},
	})
	require.Len(t, hits, 2)
	require.Equal(t, "Returned jacket has not arrived", hits[0].Title)
	require.Contains(t, hits[0].Body, "$89.00")
	require.Contains(t, hits[0].Body, alertDay.AddDays(-5).String())
	require.NotEqual(t, hits[0].DedupeKey, hits[1].DedupeKey)
}

func goal(name, needed, contributed string, overrides ...func(*domain.AlertGoal)) domain.AlertGoal {
	one := domain.AlertGoal{
		ID: domain.ID("goal-" + name), Name: name,
		Needed: domain.MustFromString(needed), HasNeeded: true,
		Contributed: domain.MustFromString(contributed),
	}
	for _, apply := range overrides {
		apply(&one)
	}
	return one
}

// The news is the shortfall: a goal that has had its month's money is silent
// and one a cent short is not.
func TestAGoalIsRemindedOnlyWhileItIsShortThisMonth(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertGoalContribution: plainRule()},
		Goals: []domain.AlertGoal{
			goal("Roof", "300.00", "100.00"),
			goal("Holiday", "200.00", "200.00"),
			goal("Car", "150.00", "149.98"),
		},
	})
	require.Len(t, hits, 2)
	require.Equal(t, "Roof needs $200.00 this month", hits[0].Title)
	require.Contains(t, hits[0].Body, "$100.00 of $300.00")
	require.Equal(t, "Car needs $0.02 this month", hits[1].Title)
}

// An open-ended goal implies no rate and a finished one needs nothing more.
func TestAGoalWithNoTargetDateAndAFinishedGoalAreBothSilent(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertGoalContribution: plainRule()},
		Goals: []domain.AlertGoal{
			goal("Someday", "0.00", "0.00", func(one *domain.AlertGoal) {
				one.HasNeeded = false
			}),
			goal("Done", "500.00", "0.00", func(one *domain.AlertGoal) {
				one.IsComplete = true
			}),
		},
	})
	require.Empty(t, hits)
}

// Once a month per goal.
func TestAGoalIsNudgedOncePerMonth(t *testing.T) {
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertGoalContribution: plainRule()},
		Goals: []domain.AlertGoal{goal("Roof", "300.00", "0.00")},
	}
	first := domain.EvaluateAlerts(in)
	in.Today = alertDay.AddDays(3)
	sameMonth := domain.EvaluateAlerts(in)
	in.Today = domain.Date{Year: 2026, Month: 9, Day: 1}
	nextMonth := domain.EvaluateAlerts(in)

	require.Equal(t, first[0].DedupeKey, sameMonth[0].DedupeKey)
	require.NotEqual(t, first[0].DedupeKey, nextMonth[0].DedupeKey)
	require.Contains(t, first[0].Body, "Nothing in yet this month")
}

func planRule(amount string) domain.AlertRuleView { return amountRule(amount) }

// "Falls to this or less": exactly the line fires, a cent above does not.
func TestAvailableToSpendFiresAtTheLineAndNotAboveIt(t *testing.T) {
	at := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertAvailableToSpendLow: planRule("10.00"),
		},
		AvailableToSpend: domain.MustFromString("10.00"), HasAvailableToSpend: true,
	})
	require.Len(t, at, 1)
	require.Equal(t, "$10.00 left to spend this month", at[0].Title)

	above := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertAvailableToSpendLow: planRule("10.00"),
		},
		AvailableToSpend: domain.MustFromString("10.01"), HasAvailableToSpend: true,
	})
	require.Empty(t, above)
}

// Overspent is a different sentence; the sign is the whole news.
func TestAnOverspentPlanSaysSoRatherThanQuotingANegative(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertAvailableToSpendLow: planRule("10.00"),
		},
		AvailableToSpend: domain.MustFromString("-40.00"), HasAvailableToSpend: true,
	})
	require.Len(t, hits, 1)
	require.Equal(t, "The spending plan is over by $40.00", hits[0].Title)
}

// A space with no plan to read is not a space with nothing left to spend.
func TestNoPlanAtAllIsNotAZeroBalance(t *testing.T) {
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertAvailableToSpendLow: planRule("10.00"),
		},
		HasAvailableToSpend: false,
	})
	require.Empty(t, hits)
}

// Once a month: the figure moves with every transaction.
func TestAvailableToSpendFiresOncePerMonth(t *testing.T) {
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertAvailableToSpendLow: planRule("10.00"),
		},
		AvailableToSpend: domain.MustFromString("5.00"), HasAvailableToSpend: true,
	}
	first := domain.EvaluateAlerts(in)
	in.AvailableToSpend = domain.MustFromString("4.00")
	same := domain.EvaluateAlerts(in)
	in.Today = domain.Date{Year: 2026, Month: 9, Day: 1}
	next := domain.EvaluateAlerts(in)

	require.Equal(t, first[0].DedupeKey, same[0].DedupeKey)
	require.NotEqual(t, first[0].DedupeKey, next[0].DedupeKey)
}

func TestAnEnvelopeNearingItsLimitAndOverItReadDifferently(t *testing.T) {
	rule := domain.AlertRuleView{
		Enabled: true, Percent: decimal.RequireFromString("10"), HasPercent: true,
	}
	hits := domain.EvaluateAlerts(domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{domain.AlertPlannedExpenseNearing: rule},
		Envelopes: []domain.AlertEnvelope{
			{ID: "e1", Name: "Hobbies", Spent: domain.MustFromString("95.00"),
				Target: domain.MustFromString("100.00")},
			{ID: "e2", Name: "Gaming", Spent: domain.MustFromString("50.00"),
				Target: domain.MustFromString("100.00")},
			{ID: "e3", Name: "Gifts", Spent: domain.MustFromString("140.00"),
				Target: domain.MustFromString("100.00")},
		},
	})
	require.Len(t, hits, 2)
	require.Equal(t, "Hobbies is near its $100.00 limit", hits[0].Title)
	require.Equal(t, "Gifts is over its $100.00 limit", hits[1].Title)
	require.Contains(t, hits[0].Body, "$95.00 spent so far")
}

func TestAStandingConditionKeepsOneKeyFromDayToDay(t *testing.T) {
	// The same states read on two days: a key carrying the day would notify
	// every morning about accounts that did not change.
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertLowBankBalance:         amountRule("200.00"),
			domain.AlertHighCardBalance:        amountRule("5000.00"),
			domain.AlertWatchlistNearingTarget: {Enabled: true, Percent: decimal.NewFromInt(10), HasPercent: true},
			domain.AlertUncategorized:          countRule(5),
		},
		Accounts: []domain.AlertAccount{
			{ID: "a1", Name: "Everyday Checking", Kind: domain.KindCash,
				Balance: domain.MustFromString("150.00")},
			{ID: "c1", Name: "Alex's Visa", Kind: domain.KindCreditCard,
				Balance: domain.MustFromString("-6543.21")},
		},
		Watchlists: []domain.AlertWatchlist{{ID: "w1", Name: "Dining out",
			Spent: domain.MustFromString("95.00"), Target: domain.MustFromString("100.00")}},
		UncategorizedCount: 9,
	}
	today := domain.EvaluateAlerts(in)
	in.Today = alertDay.AddDays(1)
	tomorrow := domain.EvaluateAlerts(in)

	require.Len(t, today, 4)
	require.Len(t, tomorrow, 4)
	for i := range today {
		require.True(t, today[i].Ongoing, "%s", today[i].Type)
		require.Contains(t, domain.SweptConditions, today[i].Type)
		require.Equal(t, today[i].DedupeKey, tomorrow[i].DedupeKey, "%s", today[i].Type)
	}
}

func TestUpcomingBillsAndTheSpendingUpdateArriveOnceAWeek(t *testing.T) {
	// 2026-08-22 is a Saturday: the week it falls in started Monday the 17th,
	// and the Monday after is a new week.
	in := domain.AlertInputs{
		Today: alertDay,
		Rules: map[domain.AlertType]domain.AlertRuleView{
			domain.AlertUpcomingBills:  countRule(1),
			domain.AlertSpendingUpdate: {Enabled: true},
		},
		Bills: []domain.AlertBill{{ID: "b1", Name: "Power", DueOn: alertDay.AddDays(2),
			Amount: domain.MustFromString("-80.00")}},
		ThisMonthSpend: domain.MustFromString("900.00"),
		LastMonthSpend: domain.MustFromString("1000.00"),
	}
	keys := func(day domain.Date) []string {
		in.Today = day
		var out []string
		for _, one := range domain.EvaluateAlerts(in) {
			require.False(t, one.Ongoing)
			out = append(out, one.DedupeKey)
		}
		return out
	}
	saturday := keys(alertDay)
	require.Equal(t, []string{"upcoming_bills:2026-08-17", "spending_update:2026-08-17"}, saturday)
	require.Equal(t, saturday, keys(alertDay.AddDays(1)), "Sunday is the same week")
	require.Equal(t, []string{"upcoming_bills:2026-08-24", "spending_update:2026-08-24"},
		keys(alertDay.AddDays(2)))
}

func TestAMailboxIsReportedOnlyOnceItHasStayedUnreadable(t *testing.T) {
	cases := []struct {
		name       string
		failures   int
		failingFor time.Duration
		tell       bool
	}{
		{"one failed poll, however long ago", 1, 6 * time.Hour, false},
		{"the next poll failing half an hour later", 2, 30 * time.Minute, false},
		{"Read now pressed three times in a minute", 3, time.Minute, false},
		{"two polls in a row across an hour", 2, time.Hour, true},
		{"a morning of failed polls", 12, 6 * time.Hour, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.tell, domain.MailboxOutageWorthTelling(c.failures, c.failingFor))
		})
	}
}
