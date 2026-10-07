package domain

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// Every expected figure comes from the closed-form future value of an
// ordinary annuity, evaluated at sixty digits, not from the implementation:
//
//	FV = P·(1+i)^n + PMT·((1+i)^n − 1)/i     i = annual/12, n = 12·years

func retirementPlan(mutate ...func(*RetirementPlan)) RetirementPlan {
	plan := RetirementPlan{
		StartYear:           2026,
		CurrentAge:          35,
		RetirementAge:       65,
		CurrentBalance:      MustFromString("200000.00"),
		MonthlyContribution: MustFromString("1250.00"),
		AnnualReturn:        decimal.RequireFromString("0.07"),
		AnnualInflation:     decimal.RequireFromString("0.025"),
		WithdrawalRate:      decimal.RequireFromString("0.04"),
	}
	for _, apply := range mutate {
		apply(&plan)
	}
	return plan
}

func mustProject(t *testing.T, plan RetirementPlan) RetirementProjection {
	t.Helper()
	projection, err := ProjectRetirement(plan)
	require.NoError(t, err)
	return projection
}

// --- Compounding -------------------------------------------------------------

func TestTheSeriesOpensAtTodaysBalance(t *testing.T) {
	// Year zero is the position as it stands.
	projection := mustProject(t, retirementPlan())

	first := projection.Years[0]
	require.Equal(t, 2026, first.Year)
	require.Equal(t, 35, first.Age)
	require.Equal(t, "200000.00", first.Balance.String())
	require.Equal(t, "200000.00", first.BalanceInTodaysDollars.String())
	require.Equal(t, "0.00", first.Contributed.String())
	require.Equal(t, "0.00", first.Growth.String())
	require.Len(t, projection.Years, 31)
}

func TestEachYearCompoundsMonthlyAtOneTwelfthOfTheReturn(t *testing.T) {
	// 12% a year is 1% a month, which makes the closed form checkable without
	// a calculator: 10,000·1.01^12 + 100·(1.01^12 − 1)/0.01 = 12,536.50.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 40
		p.RetirementAge = 43
		p.CurrentBalance = MustFromString("10000.00")
		p.MonthlyContribution = MustFromString("100.00")
		p.AnnualReturn = decimal.RequireFromString("0.12")
	}))

	balances := []string{"10000.00", "12536.50", "15394.69", "18615.38"}
	for year, expected := range balances {
		require.Equal(t, expected, projection.Years[year].Balance.String(),
			"balance at year %d", year)
	}
	require.Equal(t, "18615.38", projection.BalanceAtRetirement.String())
}

func TestContributionsLandAfterTheMonthsGrowth(t *testing.T) {
	// An ordinary annuity: one month at 12% on a single 100.00 payment is
	// 100.00, not 101.00.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 40
		p.RetirementAge = 41
		p.CurrentBalance = Zero
		p.MonthlyContribution = MustFromString("100.00")
		p.AnnualReturn = decimal.RequireFromString("0.12")
	}))

	// 100·(1.01^12 − 1)/0.01 = 1,268.25. Crediting the contributions first
	// would report 1,280.93.
	require.Equal(t, "1268.25", projection.BalanceAtRetirement.String())
	require.Equal(t, "1200.00", projection.TotalContributed.String())
	require.Equal(t, "68.25", projection.TotalGrowth.String())
}

func TestTheRunningBalanceIsRoundedOncePerYearNotEveryMonth(t *testing.T) {
	// Thirty years of 1,250.00 a month at 7% is 3,148,263.24 by the closed
	// form; rounding the carried balance monthly lands on 3,148,262.75.
	projection := mustProject(t, retirementPlan())
	require.Equal(t, "3148263.24", projection.BalanceAtRetirement.String())
	require.NotEqual(t, "3148262.75", projection.BalanceAtRetirement.String())
}

func TestTheThreePartsOfTheBalanceAddUpEveryYear(t *testing.T) {
	// Contributed and Growth reconcile at every point, not only at the end.
	plan := retirementPlan()
	for _, year := range mustProject(t, plan).Years {
		require.Equal(t,
			year.Balance.String(),
			Total(plan.CurrentBalance, year.Contributed, year.Growth).String(),
			"year %d", year.Year)
	}
}

func TestContributionsAreCountedNotAccumulated(t *testing.T) {
	projection := mustProject(t, retirementPlan())
	require.Equal(t, "450000.00", projection.TotalContributed.String())
	require.Equal(t, "2498263.24", projection.TotalGrowth.String())
}

// --- The zero-contribution case ----------------------------------------------

func TestABalanceWithNoContributionsJustCompounds(t *testing.T) {
	// 25,000·(1 + 0.06/12)^12 = 26,541.95, and ten years of it is 45,484.92.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 55
		p.RetirementAge = 65
		p.CurrentBalance = MustFromString("25000.00")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.RequireFromString("0.06")
	}))

	require.Equal(t, "26541.95", projection.Years[1].Balance.String())
	require.Equal(t, "45484.92", projection.BalanceAtRetirement.String())
	require.Equal(t, "0.00", projection.TotalContributed.String())
	require.Equal(t, "20484.92", projection.TotalGrowth.String())
}

func TestNoReturnAndNoContributionLeavesTheBalanceWhereItWas(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentBalance = MustFromString("50000.00")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.Zero
	}))
	require.Equal(t, "50000.00", projection.BalanceAtRetirement.String())
	require.Equal(t, "0.00", projection.TotalGrowth.String())
}

// --- A retirement age already reached ----------------------------------------

func TestARetirementAgeAlreadyReachedProjectsNothing(t *testing.T) {
	// Not a refusal: the balance as it stands, with no year of growth.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 71
		p.RetirementAge = 65
	}))

	require.Equal(t, 0, projection.YearsToRetirement)
	require.Len(t, projection.Years, 1)
	require.Equal(t, 71, projection.Years[0].Age)
	require.Equal(t, "200000.00", projection.BalanceAtRetirement.String())
	require.Equal(t, "200000.00", projection.BalanceAtRetirementInTodaysDollars.String())
	require.Equal(t, "0.00", projection.TotalContributed.String())
	// 4% of what is there today; nothing projected, so nothing to deflate.
	require.Equal(t, "8000.00", projection.AnnualIncome.String())
	require.Equal(t, "8000.00", projection.AnnualIncomeInTodaysDollars.String())
}

func TestRetiringThisYearIsTheSameAsAlreadyRetired(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 65
		p.RetirementAge = 65
	}))
	require.Equal(t, 0, projection.YearsToRetirement)
	require.Equal(t, "200000.00", projection.BalanceAtRetirement.String())
}

// --- Inflation, income and the target ----------------------------------------

func TestTodaysDollarsDeflateByTheAssumedInflation(t *testing.T) {
	// 3,148,263.24 / 1.025^30 = 1,500,911.47.
	projection := mustProject(t, retirementPlan())
	require.Equal(t, "1500911.47", projection.BalanceAtRetirementInTodaysDollars.String())
}

func TestNoInflationLeavesTodaysDollarsAlone(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.AnnualInflation = decimal.Zero
	}))
	require.Equal(t,
		projection.BalanceAtRetirement.String(),
		projection.BalanceAtRetirementInTodaysDollars.String())
}

func TestTheIncomeIsTheWithdrawalRateOnTheBalanceAtRetirement(t *testing.T) {
	projection := mustProject(t, retirementPlan())
	require.Equal(t, "125930.53", projection.AnnualIncome.String())
	require.Equal(t, "60036.46", projection.AnnualIncomeInTodaysDollars.String())
}

func TestTheTargetIsJudgedInTodaysDollars(t *testing.T) {
	// 60,036.46 in today's money clears a 60,000.00 target and falls 8,661.66
	// short of 68,698.12; the nominal 125,930.53 would clear both.
	met := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.HasTarget = true
		p.TargetAnnualIncome = MustFromString("60000.00")
	}))
	require.True(t, met.MeetsTarget)
	require.Equal(t, "0.00", met.Shortfall.String(), "a met target is not a negative shortfall")

	missed := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.HasTarget = true
		p.TargetAnnualIncome = MustFromString("68698.12")
	}))
	require.False(t, missed.MeetsTarget)
	require.Equal(t, "8661.66", missed.Shortfall.String())
}

func TestATargetExactlyMetCounts(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.HasTarget = true
		p.TargetAnnualIncome = MustFromString("60036.46")
	}))
	require.True(t, projection.MeetsTarget)
	require.Equal(t, "0.00", projection.Shortfall.String())
}

func TestNoTargetMeansNoVerdict(t *testing.T) {
	projection := mustProject(t, retirementPlan())
	require.False(t, projection.MeetsTarget)
	require.Equal(t, "0.00", projection.Shortfall.String())
}

// --- No float enters the arithmetic ------------------------------------------

func TestAnAmountBeyondFloat64SurvivesTheProjection(t *testing.T) {
	// 1,234,567,890,123,456.78 exceeds a float64 mantissa (nearest double
	// ...456.8), so a float anywhere would lose the cents.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 60
		p.RetirementAge = 65
		p.CurrentBalance = MustFromString("1234567890123456.78")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.Zero
		p.AnnualInflation = decimal.Zero
	}))
	require.Equal(t, "1234567890123456.78", projection.BalanceAtRetirement.String())
}

func TestATenthPlusATwentiethIsExact(t *testing.T) {
	// 0.1 + 0.05 is 0.15000000000000002 in floating point; this must be 0.25.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 40
		p.RetirementAge = 41
		p.CurrentBalance = MustFromString("0.10")
		p.MonthlyContribution = MustFromString("0.0125")
		p.AnnualReturn = decimal.Zero
		p.AnnualInflation = decimal.Zero
	}))
	require.Equal(t, "0.25", projection.BalanceAtRetirement.String())
	require.Equal(t, "0.15", projection.TotalContributed.String())
}

// --- Refusals ----------------------------------------------------------------

func TestAPlanWithAnImpossibleAssumptionIsRefused(t *testing.T) {
	cases := map[string]func(*RetirementPlan){
		"negative age":           func(p *RetirementPlan) { p.CurrentAge = -1 },
		"age past a lifetime":    func(p *RetirementPlan) { p.CurrentAge = 200 },
		"retirement past a life": func(p *RetirementPlan) { p.RetirementAge = 500 },
		"negative contribution": func(p *RetirementPlan) {
			p.MonthlyContribution = MustFromString("-100.00")
		},
		"negative withdrawal": func(p *RetirementPlan) {
			p.WithdrawalRate = decimal.RequireFromString("-0.04")
		},
		"a return of everything": func(p *RetirementPlan) {
			p.AnnualReturn = decimal.RequireFromString("-1")
		},
		"inflation of everything": func(p *RetirementPlan) {
			p.AnnualInflation = decimal.RequireFromString("-1.5")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			_, err := ProjectRetirement(retirementPlan(mutate))
			require.ErrorIs(t, err, ErrRetirementPlan)
		})
	}
}

func TestANegativeBalanceIsProjectedNotRefused(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 60
		p.RetirementAge = 61
		p.CurrentBalance = MustFromString("-1000.00")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.RequireFromString("0.12")
	}))
	// −1,000·1.01^12 = −1,126.83: a debt compounds the same way.
	require.Equal(t, "-1126.83", projection.BalanceAtRetirement.String())
}

// --- The drawdown ------------------------------------------------------------

// Zero return and inflation make the drawdown pure subtraction.
func drawdownPlan(mutate ...func(*RetirementPlan)) RetirementPlan {
	return retirementPlan(append([]func(*RetirementPlan){func(p *RetirementPlan) {
		p.CurrentAge = 60
		p.RetirementAge = 60
		p.LifeExpectancy = 62
		p.CurrentBalance = MustFromString("100000.00")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.Zero
		p.AnnualInflation = decimal.Zero
		p.AnnualLivingExpenses = MustFromString("12000.00")
	}}, mutate...)...)
}

func TestRetirementDrawsExpensesMinusIncomeEachYear(t *testing.T) {
	projection := mustProject(t, drawdownPlan(func(p *RetirementPlan) {
		p.AnnualRetirementIncome = MustFromString("4000.00")
	}))
	require.Len(t, projection.Years, 3)
	require.Equal(t, "100000.00", projection.Years[0].Balance.String())
	require.Equal(t, "92000.00", projection.Years[1].Balance.String())
	require.Equal(t, "84000.00", projection.Years[2].Balance.String())
	require.Equal(t, "16000.00", projection.Years[2].Drawn.String())
}

func TestTheFourPartsOfTheBalanceAddUpThroughRetirement(t *testing.T) {
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 55
		p.RetirementAge = 60
		p.LifeExpectancy = 70
		p.AnnualLivingExpenses = MustFromString("50000.00")
		p.AnnualRetirementIncome = MustFromString("20000.00")
	}))
	for _, year := range projection.Years {
		sum := projection.Plan.CurrentBalance.Round().
			Add(year.Contributed).Add(year.Growth).Sub(year.Drawn)
		require.Equal(t, year.Balance.String(), sum.String(),
			"start + contributed + growth − drawn broke at age %d", year.Age)
	}
}

func TestRetirementIncomeAboveExpensesDrawsNothing(t *testing.T) {
	projection := mustProject(t, drawdownPlan(func(p *RetirementPlan) {
		p.AnnualRetirementIncome = MustFromString("20000.00")
	}))
	require.Equal(t, "100000.00", projection.Years[2].Balance.String(),
		"a surplus was invested or drawn; it should do neither")
	require.Equal(t, "0.00", projection.Years[2].Drawn.String())
}

func TestTheDrawIsInflatedToTheYearItLandsIn(t *testing.T) {
	projection := mustProject(t, drawdownPlan(func(p *RetirementPlan) {
		p.AnnualInflation = decimal.RequireFromString("0.10")
	}))
	// Year one draws 12,000 × 1.1 = 13,200; year two 12,000 × 1.21 = 14,520.
	require.Equal(t, "86800.00", projection.Years[1].Balance.String())
	require.Equal(t, "72280.00", projection.Years[2].Balance.String())
}

func TestTheMoneyRunningOutIsNamedWithItsAge(t *testing.T) {
	projection := mustProject(t, drawdownPlan(func(p *RetirementPlan) {
		p.LifeExpectancy = 80
		p.AnnualLivingExpenses = MustFromString("30000.00")
	}))
	require.True(t, projection.RunsOut)
	// 100,000 at 30,000 a year: negative during the fourth year, at age 64.
	require.Equal(t, 64, projection.RunsOutAtAge)
}

// --- Tax rates ---------------------------------------------------------------

func TestTheTaxRateDiscountsTheReturnItGoverns(t *testing.T) {
	// 12% at a 50% tax rate compounds at 0.5% a month: 10,000 × 1.005¹² is
	// 10,616.78.
	projection := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.CurrentAge = 64
		p.RetirementAge = 65
		p.CurrentBalance = MustFromString("10000.00")
		p.MonthlyContribution = Zero
		p.AnnualReturn = decimal.RequireFromString("0.12")
		p.PreRetirementTaxRate = decimal.RequireFromString("0.50")
	}))
	require.Equal(t, "10616.78", projection.Years[1].Balance.String())
}

// --- The estimate band -------------------------------------------------------

func TestTheHighWalkIsTheExpectedWalkAtTheWiderReturn(t *testing.T) {
	spread := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.ReturnSpread = decimal.RequireFromString("0.02")
	}))
	widened := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.AnnualReturn = decimal.RequireFromString("0.09")
	}))
	narrowed := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.AnnualReturn = decimal.RequireFromString("0.05")
	}))
	for i, year := range spread.Years {
		require.Equal(t, widened.Years[i].Balance.String(), year.HighBalance.String())
		require.Equal(t, narrowed.Years[i].Balance.String(), year.LowBalance.String())
	}
}

func TestAZeroSpreadCollapsesTheBandOntoTheLine(t *testing.T) {
	projection := mustProject(t, retirementPlan())
	final := projection.Years[len(projection.Years)-1]
	require.Equal(t, final.Balance.String(), final.HighBalance.String())
	require.Equal(t, final.Balance.String(), final.LowBalance.String())
}

// --- Refusals ----------------------------------------------------------------

func TestALifeExpectancyBeforeRetirementIsRefused(t *testing.T) {
	_, err := ProjectRetirement(retirementPlan(func(p *RetirementPlan) {
		p.LifeExpectancy = 60
	}))
	require.ErrorIs(t, err, ErrRetirementPlan)
}

func TestNoLifeExpectancyEndsTheChartAtRetirement(t *testing.T) {
	projection := mustProject(t, retirementPlan())
	require.Len(t, projection.Years, 31)
	require.Equal(t, 65, projection.Years[len(projection.Years)-1].Age)
	require.False(t, projection.RunsOut)
}

// --- The Advanced planner ----------------------------------------------------

func advancedPlan(mutate ...func(*AdvancedRetirementPlan)) AdvancedRetirementPlan {
	plan := AdvancedRetirementPlan{
		StartYear:            2026,
		CurrentAge:           64,
		RetirementAge:        65,
		TaxableBalance:       MustFromString("10000.00"),
		DeferredBalance:      MustFromString("10000.00"),
		PreRetirementReturn:  decimal.RequireFromString("0.12"),
		PostRetirementReturn: decimal.RequireFromString("0.12"),
	}
	for _, apply := range mutate {
		apply(&plan)
	}
	return plan
}

func mustProjectAdvanced(t *testing.T, plan AdvancedRetirementPlan) RetirementProjection {
	t.Helper()
	projection, err := ProjectAdvancedRetirement(plan)
	require.NoError(t, err)
	return projection
}

func TestOnlyTheTaxableBucketPaysTaxAsItGrows(t *testing.T) {
	// 12% at a 50% tax rate: the taxable half compounds at 0.5% a month to
	// 10,616.78, the deferred half at 1% to 11,268.25; the total is the sum.
	projection := mustProjectAdvanced(t, advancedPlan(func(p *AdvancedRetirementPlan) {
		p.PreRetirementTaxRate = decimal.RequireFromString("0.50")
	}))
	require.Equal(t, "21885.03", projection.Years[1].Balance.String())
}

func TestContributionsEscalateAtEachYearBoundary(t *testing.T) {
	// Doubling growth, zero return: 1,200 the first year, 2,400 the second.
	projection := mustProjectAdvanced(t, advancedPlan(func(p *AdvancedRetirementPlan) {
		p.RetirementAge = 66
		p.TaxableBalance = Zero
		p.DeferredBalance = Zero
		p.AnnualTaxableContribution = MustFromString("1200.00")
		p.ContributionGrowth = decimal.RequireFromString("1.00")
		p.PreRetirementReturn = decimal.Zero
		p.PostRetirementReturn = decimal.Zero
	}))
	require.Equal(t, "1200.00", projection.Years[1].Balance.String())
	require.Equal(t, "3600.00", projection.Years[2].Balance.String())
	require.Equal(t, "3600.00", projection.Years[2].Contributed.String())
}

func TestTheDrawdownSpendsTaxableFirstThenGrossesUpTheDeferred(t *testing.T) {
	// 12,000 a year against 6,000 of taxable money: half from taxable, the
	// other half costs double from deferred at a 50% tax — 88,000 of 100,000.
	projection := mustProjectAdvanced(t, advancedPlan(func(p *AdvancedRetirementPlan) {
		p.CurrentAge = 65
		p.LifeExpectancy = 66
		p.TaxableBalance = MustFromString("6000.00")
		p.DeferredBalance = MustFromString("100000.00")
		p.AnnualLivingExpenses = MustFromString("12000.00")
		p.PreRetirementReturn = decimal.Zero
		p.PostRetirementReturn = decimal.Zero
		p.PostRetirementTaxRate = decimal.RequireFromString("0.50")
	}))
	require.Equal(t, "88000.00", projection.Years[1].Balance.String())
	require.Equal(t, "12000.00", projection.Years[1].Drawn.String())
}

func TestAnAllTaxablePlanMatchesTheBasicWalk(t *testing.T) {
	// One bucket, one return, no escalation: the two modes must agree.
	advanced := mustProjectAdvanced(t, advancedPlan(func(p *AdvancedRetirementPlan) {
		p.CurrentAge = 35
		p.RetirementAge = 65
		p.LifeExpectancy = 75
		p.TaxableBalance = MustFromString("200000.00")
		p.DeferredBalance = Zero
		p.AnnualTaxableContribution = MustFromString("15000.00")
		p.PreRetirementReturn = decimal.RequireFromString("0.07")
		p.PostRetirementReturn = decimal.RequireFromString("0.07")
		p.PreRetirementTaxRate = decimal.RequireFromString("0.22")
		p.PostRetirementTaxRate = decimal.RequireFromString("0.22")
		p.AnnualInflation = decimal.RequireFromString("0.025")
		p.AnnualLivingExpenses = MustFromString("50000.00")
		p.AnnualRetirementIncome = MustFromString("20000.00")
	}))
	basic := mustProject(t, retirementPlan(func(p *RetirementPlan) {
		p.MonthlyContribution = MustFromString("1250.00")
		p.LifeExpectancy = 75
		p.PreRetirementTaxRate = decimal.RequireFromString("0.22")
		p.PostRetirementTaxRate = decimal.RequireFromString("0.22")
		p.AnnualLivingExpenses = MustFromString("50000.00")
		p.AnnualRetirementIncome = MustFromString("20000.00")
	}))
	require.Equal(t, len(basic.Years), len(advanced.Years))
	for i := range basic.Years {
		require.Equal(t, basic.Years[i].Balance.String(), advanced.Years[i].Balance.String(),
			"the modes disagreed at age %d", basic.Years[i].Age)
	}
}

func TestANegativeAdvancedContributionIsRefused(t *testing.T) {
	_, err := ProjectAdvancedRetirement(advancedPlan(func(p *AdvancedRetirementPlan) {
		p.AnnualDeferredContribution = MustFromString("-1.00")
	}))
	require.ErrorIs(t, err, ErrRetirementPlan)
}
