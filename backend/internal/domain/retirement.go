package domain

// The retirement projection: arithmetic over the household's own stated
// assumptions, with no market data. Conventions the numbers depend on:
//
//   - The return is nominal and compounds monthly at one twelfth of it (not
//     the twelfth root, which differs by real money over decades).
//   - Contributions land at the end of each month, after that month's growth.
//   - Inflation is a deflator, not a drag on the return: every year carries
//     its nominal balance and the same balance in today's dollars.
//
// The running balance keeps sub-cent precision and is rounded to the cent only
// for the reported yearly figure: rounding it monthly drifts by dollars over
// decades, and never rounding it grows the decimal without bound.

import (
	"errors"
	"fmt"

	"github.com/shopspring/decimal"
)

const (
	// monthlyRateDecimals is fixed rather than left to decimal's global
	// DivisionPrecision, which another package's init could change.
	monthlyRateDecimals int32 = 20

	// workingDecimals is the sub-cent precision carried between months.
	workingDecimals int32 = 10

	oldestAge = 120
)

// ErrRetirementPlan is every refusal this file makes, so a caller can tell a
// bad assumption from another failure.
var ErrRetirementPlan = errors.New("retirement plan")

// RetirementPlan is every input to the projection. No field has a default
// here: the API layer picks them and returns them with the answer.
type RetirementPlan struct {
	StartYear     int
	CurrentAge    int
	RetirementAge int

	// CurrentBalance may be negative (a margin account).
	CurrentBalance      Money
	MonthlyContribution Money

	// AnnualReturn and AnnualInflation are fractions: 0.07 is 7%.
	AnnualReturn    Rate
	AnnualInflation Rate

	// WithdrawalRate is the fraction drawn in the first year of retirement.
	WithdrawalRate Rate

	// TargetAnnualIncome is in today's dollars and compared against the
	// deflated projected income, never the nominal one. HasTarget false is
	// not a target of zero.
	TargetAnnualIncome Money
	HasTarget          bool

	// LifeExpectancy extends the projection past retirement with the
	// drawdown; zero ends it at retirement.
	LifeExpectancy int

	// AnnualLivingExpenses and AnnualRetirementIncome shape the drawdown:
	// each retirement year draws expenses minus income, stated in today's
	// dollars and inflated to the year. A surplus is not reinvested.
	AnnualLivingExpenses   Money
	AnnualRetirementIncome Money

	// PreRetirementTaxRate and PostRetirementTaxRate discount growth to
	// return × (1 − rate) in the phase each governs; zero leaves it whole.
	PreRetirementTaxRate  Rate
	PostRetirementTaxRate Rate

	// ReturnSpread is the width of the estimate band: high and low walks at
	// return ± spread. The default lives in the API layer.
	ReturnSpread Rate
}

// RetirementYear is one point of the balance-by-year series, rounded to the
// cent.
type RetirementYear struct {
	Year int
	Age  int
	// Balance is the end of that year, in that year's dollars; the first
	// entry is today.
	Balance                Money
	BalanceInTodaysDollars Money
	// Balance = start + Contributed + Growth − Drawn, by construction.
	Contributed Money
	Drawn       Money
	Growth      Money
	// HighBalance and LowBalance are the estimate band; they equal Balance
	// when the spread is zero.
	HighBalance                Money
	LowBalance                 Money
	HighBalanceInTodaysDollars Money
	LowBalanceInTodaysDollars  Money
}

// RetirementProjection is the series and the headline answers from one walk,
// so the chart and the headline cannot disagree.
type RetirementProjection struct {
	Plan  RetirementPlan
	Years []RetirementYear

	// YearsToRetirement is zero once the retirement age has been reached.
	YearsToRetirement int

	BalanceAtRetirement                Money
	BalanceAtRetirementInTodaysDollars Money
	TotalContributed                   Money
	TotalGrowth                        Money

	// AnnualIncome is the withdrawal rate applied to the balance at
	// retirement, in that year's dollars.
	AnnualIncome                Money
	AnnualIncomeInTodaysDollars Money

	// MeetsTarget is false when no target was stated. Shortfall is in today's
	// dollars and zero when the target is met or absent.
	MeetsTarget bool
	Shortfall   Money

	// RunsOut reports the expected walk reaching zero during retirement while
	// a draw was being taken; the band gets no verdict of its own.
	RunsOut      bool
	RunsOutAtAge int
}

// ProjectRetirement walks the plan year by year. The error is for an
// assumption with no arithmetic, not a plan that falls short.
//
// Up to the retirement age the balance compounds at the pre-retirement taxed
// return with contributions at each month's end. Past it (only with a life
// expectancy) contributions stop and each month draws a twelfth of the
// inflated annual draw. The balance may go negative: when it runs out is the
// answer. Low, expected and high walks share one loop.
func ProjectRetirement(plan RetirementPlan) (RetirementProjection, error) {
	if err := plan.validate(); err != nil {
		return RetirementProjection{}, err
	}

	years := plan.RetirementAge - plan.CurrentAge
	if years < 0 {
		// Already retired is a state the screen must show, so clamp.
		years = 0
	}
	horizon := years
	if plan.LifeExpectancy > 0 {
		if extended := plan.LifeExpectancy - plan.CurrentAge; extended > horizon {
			horizon = extended
		}
	}

	one := decimal.NewFromInt(1)
	twelve := decimal.NewFromInt(12)
	monthlyRate := func(taxRate Rate, spread decimal.Decimal) decimal.Decimal {
		return plan.AnnualReturn.Add(spread).Mul(one.Sub(taxRate)).
			DivRound(twelve, monthlyRateDecimals)
	}
	inflationFactor := one.Add(plan.AnnualInflation)

	annualDraw := plan.AnnualLivingExpenses.Sub(plan.AnnualRetirementIncome)
	if annualDraw.IsNegative() {
		annualDraw = Zero
	}
	monthlyDrawToday := annualDraw.Scale(one.DivRound(twelve, monthlyRateDecimals))

	spreads := [3]decimal.Decimal{plan.ReturnSpread.Neg(), decimal.Zero, plan.ReturnSpread}
	var balances [3]Money
	for i := range balances {
		balances[i] = plan.CurrentBalance
	}

	projection := RetirementProjection{Plan: plan, YearsToRetirement: years}
	deflator := one
	drawn := Zero

	var balanceAtRetirement Money
	var deflatorAtRetirement Rate

	for year := 0; year <= horizon; year++ {
		if year > 0 {
			retired := year > years
			deflator = deflator.Mul(inflationFactor)
			taxRate := plan.PreRetirementTaxRate
			if retired {
				taxRate = plan.PostRetirementTaxRate
			}
			// The draw is in today's dollars, inflated to year N.
			monthlyDraw := Zero
			if retired {
				monthlyDraw = monthlyDrawToday.Scale(deflator)
			}
			for month := 0; month < 12; month++ {
				for i := range balances {
					factor := one.Add(monthlyRate(taxRate, spreads[i]))
					balances[i] = balances[i].Scale(factor)
					if !retired {
						balances[i] = balances[i].Add(plan.MonthlyContribution)
					} else {
						balances[i] = balances[i].Sub(monthlyDraw)
					}
					balances[i] = FromDecimal(balances[i].Decimal().Round(workingDecimals))
				}
				if retired {
					drawn = drawn.Add(monthlyDraw)
				}
			}
		}

		contributedYears := year
		if contributedYears > years {
			contributedYears = years
		}
		// Multiplied rather than accumulated, so no intermediate rounding.
		contributed := plan.MonthlyContribution.Scale(decimal.NewFromInt(int64(contributedYears * 12)))
		expected := balances[1]
		age := plan.CurrentAge + year
		// Growth is the residual of the rounded figures, so the identity
		// holds to the cent.
		growth := expected.Round().Sub(plan.CurrentBalance.Round()).
			Sub(contributed.Round()).Add(drawn.Round())
		projection.Years = append(projection.Years, RetirementYear{
			Year:                       plan.StartYear + year,
			Age:                        age,
			Balance:                    expected.Round(),
			BalanceInTodaysDollars:     inTodaysDollars(expected, deflator),
			Contributed:                contributed.Round(),
			Drawn:                      drawn.Round(),
			Growth:                     growth,
			HighBalance:                balances[2].Round(),
			LowBalance:                 balances[0].Round(),
			HighBalanceInTodaysDollars: inTodaysDollars(balances[2], deflator),
			LowBalanceInTodaysDollars:  inTodaysDollars(balances[0], deflator),
		})

		if year == years {
			balanceAtRetirement = expected
			deflatorAtRetirement = deflator
		}
		if year > years && !projection.RunsOut && annualDraw.IsPositive() && !expected.IsPositive() {
			projection.RunsOut = true
			projection.RunsOutAtAge = age
		}
	}

	atRetirement := projection.Years[years]
	income := balanceAtRetirement.Scale(plan.WithdrawalRate)
	projection.BalanceAtRetirement = atRetirement.Balance
	projection.BalanceAtRetirementInTodaysDollars = atRetirement.BalanceInTodaysDollars
	projection.TotalContributed = atRetirement.Contributed
	projection.TotalGrowth = atRetirement.Growth
	projection.AnnualIncome = income.Round()
	projection.AnnualIncomeInTodaysDollars = inTodaysDollars(income, deflatorAtRetirement)

	if plan.HasTarget {
		short := plan.TargetAnnualIncome.Sub(projection.AnnualIncomeInTodaysDollars)
		projection.MeetsTarget = !short.IsPositive()
		if short.IsPositive() {
			projection.Shortfall = short.Round()
		}
	}
	return projection, nil
}

// inTodaysDollars divides by the accumulated inflation factor. validate makes
// a zero deflator impossible; if one arises the amount is returned undeflated.
func inTodaysDollars(amount Money, deflator Rate) Money {
	deflated, ok := amount.DivRate(deflator)
	if !ok {
		return amount.Round()
	}
	return deflated.Round()
}

func (p RetirementPlan) validate() error {
	if p.CurrentAge < 0 || p.CurrentAge > oldestAge {
		return fmt.Errorf("%w: current age %d is not between 0 and %d",
			ErrRetirementPlan, p.CurrentAge, oldestAge)
	}
	if p.RetirementAge < 0 || p.RetirementAge > oldestAge {
		return fmt.Errorf("%w: retirement age %d is not between 0 and %d",
			ErrRetirementPlan, p.RetirementAge, oldestAge)
	}
	if p.MonthlyContribution.IsNegative() {
		return fmt.Errorf("%w: the monthly contribution cannot be negative", ErrRetirementPlan)
	}
	if p.WithdrawalRate.IsNegative() {
		return fmt.Errorf("%w: the withdrawal rate cannot be negative", ErrRetirementPlan)
	}
	// −100% or worse compounds to nothing and divides by nothing.
	minusOne := decimal.NewFromInt(-1)
	if !p.AnnualReturn.GreaterThan(minusOne) {
		return fmt.Errorf("%w: the annual return must be greater than -100%%", ErrRetirementPlan)
	}
	if !p.AnnualInflation.GreaterThan(minusOne) {
		return fmt.Errorf("%w: the annual inflation must be greater than -100%%", ErrRetirementPlan)
	}
	if p.LifeExpectancy != 0 {
		if p.LifeExpectancy < 0 || p.LifeExpectancy > oldestAge {
			return fmt.Errorf("%w: life expectancy %d is not between 0 and %d",
				ErrRetirementPlan, p.LifeExpectancy, oldestAge)
		}
		if p.LifeExpectancy < p.RetirementAge {
			return fmt.Errorf("%w: a life expectancy of %d is before the retirement age of %d",
				ErrRetirementPlan, p.LifeExpectancy, p.RetirementAge)
		}
	}
	if p.AnnualLivingExpenses.IsNegative() {
		return fmt.Errorf("%w: annual living expenses cannot be negative", ErrRetirementPlan)
	}
	if p.AnnualRetirementIncome.IsNegative() {
		return fmt.Errorf("%w: annual retirement income cannot be negative", ErrRetirementPlan)
	}
	zero, one := decimal.Zero, decimal.NewFromInt(1)
	if p.PreRetirementTaxRate.LessThan(zero) || !p.PreRetirementTaxRate.LessThan(one) {
		return fmt.Errorf("%w: the pre-retirement tax rate must be at least 0%% and below 100%%",
			ErrRetirementPlan)
	}
	if p.PostRetirementTaxRate.LessThan(zero) || !p.PostRetirementTaxRate.LessThan(one) {
		return fmt.Errorf("%w: the post-retirement tax rate must be at least 0%% and below 100%%",
			ErrRetirementPlan)
	}
	if p.ReturnSpread.IsNegative() {
		return fmt.Errorf("%w: the return spread cannot be negative", ErrRetirementPlan)
	}
	if !p.AnnualReturn.Sub(p.ReturnSpread).GreaterThan(minusOne) {
		return fmt.Errorf("%w: the low estimate's return must be greater than -100%%", ErrRetirementPlan)
	}
	return nil
}

// AdvancedRetirementPlan is the reference product's Advanced mode: balances
// split into taxable and tax-deferred, two escalating contribution streams, and
// a return per phase. Conventions:
//
//   - Taxable growth is taxed as it accrues (return × (1 − rate)); the
//     deferred bucket compounds at the full return and is taxed on the way out.
//   - Contributions escalate at each year boundary: year N contributes the
//     annual amount × (1 + growth)^(N−1), monthly at each month's end.
//   - The drawdown spends the taxable bucket first; after that a dollar of
//     spending costs 1 ⁄ (1 − post-retirement tax) from the deferred bucket.
//   - The chart's balance is the sticker total, taxable plus deferred, so the
//     two modes' charts agree about the same accounts.
type AdvancedRetirementPlan struct {
	StartYear      int
	CurrentAge     int
	RetirementAge  int
	LifeExpectancy int

	TaxableBalance  Money
	DeferredBalance Money

	AnnualTaxableContribution  Money
	AnnualDeferredContribution Money
	// ContributionGrowth is the annual escalation of both streams.
	ContributionGrowth Rate

	AnnualLivingExpenses   Money
	AnnualRetirementIncome Money
	AnnualInflation        Rate

	PreRetirementReturn   Rate
	PostRetirementReturn  Rate
	PreRetirementTaxRate  Rate
	PostRetirementTaxRate Rate

	ReturnSpread Rate
}

// basicShape adapts the plan onto the basic validator, so the two modes agree
// on what is a valid plan.
func (p AdvancedRetirementPlan) basicShape() RetirementPlan {
	return RetirementPlan{
		StartYear:              p.StartYear,
		CurrentAge:             p.CurrentAge,
		RetirementAge:          p.RetirementAge,
		LifeExpectancy:         p.LifeExpectancy,
		CurrentBalance:         p.TaxableBalance.Add(p.DeferredBalance),
		AnnualReturn:           p.PreRetirementReturn,
		AnnualInflation:        p.AnnualInflation,
		AnnualLivingExpenses:   p.AnnualLivingExpenses,
		AnnualRetirementIncome: p.AnnualRetirementIncome,
		PreRetirementTaxRate:   p.PreRetirementTaxRate,
		PostRetirementTaxRate:  p.PostRetirementTaxRate,
		ReturnSpread:           p.ReturnSpread,
	}
}

func (p AdvancedRetirementPlan) validate() error {
	if err := p.basicShape().validate(); err != nil {
		return err
	}
	minusOne := decimal.NewFromInt(-1)
	if !p.PostRetirementReturn.GreaterThan(minusOne) {
		return fmt.Errorf("%w: the post-retirement return must be greater than -100%%", ErrRetirementPlan)
	}
	if !p.PostRetirementReturn.Sub(p.ReturnSpread).GreaterThan(minusOne) {
		return fmt.Errorf("%w: the low estimate's post-retirement return must be greater than -100%%",
			ErrRetirementPlan)
	}
	if p.AnnualTaxableContribution.IsNegative() || p.AnnualDeferredContribution.IsNegative() {
		return fmt.Errorf("%w: a contribution cannot be negative", ErrRetirementPlan)
	}
	if !p.ContributionGrowth.GreaterThan(decimal.NewFromInt(-1)) {
		return fmt.Errorf("%w: the contribution growth must be greater than -100%%", ErrRetirementPlan)
	}
	return nil
}

// ProjectAdvancedRetirement walks the two buckets year by year into the basic
// RetirementProjection; withdrawal-rate income and the target verdict stay
// zero.
func ProjectAdvancedRetirement(plan AdvancedRetirementPlan) (RetirementProjection, error) {
	if err := plan.validate(); err != nil {
		return RetirementProjection{}, err
	}

	years := plan.RetirementAge - plan.CurrentAge
	if years < 0 {
		years = 0
	}
	horizon := years
	if plan.LifeExpectancy > 0 {
		if extended := plan.LifeExpectancy - plan.CurrentAge; extended > horizon {
			horizon = extended
		}
	}

	one := decimal.NewFromInt(1)
	twelve := decimal.NewFromInt(12)
	oneTwelfth := one.DivRound(twelve, monthlyRateDecimals)
	inflationFactor := one.Add(plan.AnnualInflation)
	escalation := one.Add(plan.ContributionGrowth)

	monthlyRate := func(annual Rate, taxRate Rate, spread decimal.Decimal) decimal.Decimal {
		return annual.Add(spread).Mul(one.Sub(taxRate)).DivRound(twelve, monthlyRateDecimals)
	}

	annualDraw := plan.AnnualLivingExpenses.Sub(plan.AnnualRetirementIncome)
	if annualDraw.IsNegative() {
		annualDraw = Zero
	}
	monthlyDrawToday := annualDraw.Scale(oneTwelfth)

	// grossUp is what leaves the deferred bucket per dollar of spending.
	grossUp := one.DivRound(one.Sub(plan.PostRetirementTaxRate), monthlyRateDecimals)

	spreads := [3]decimal.Decimal{plan.ReturnSpread.Neg(), decimal.Zero, plan.ReturnSpread}
	type buckets struct{ taxable, deferred Money }
	var walks [3]buckets
	for i := range walks {
		walks[i] = buckets{taxable: plan.TaxableBalance, deferred: plan.DeferredBalance}
	}

	start := plan.TaxableBalance.Add(plan.DeferredBalance)
	projection := RetirementProjection{
		Plan:              plan.basicShape(),
		YearsToRetirement: years,
	}
	deflator := one
	drawn := Zero
	contributed := Zero
	escalator := one

	for year := 0; year <= horizon; year++ {
		if year > 0 {
			retired := year > years
			deflator = deflator.Mul(inflationFactor)
			annualReturn, taxRate := plan.PreRetirementReturn, plan.PreRetirementTaxRate
			if retired {
				annualReturn, taxRate = plan.PostRetirementReturn, plan.PostRetirementTaxRate
			}
			monthlyDraw := Zero
			var monthlyTaxable, monthlyDeferred Money
			if retired {
				monthlyDraw = monthlyDrawToday.Scale(deflator)
			} else {
				monthlyTaxable = plan.AnnualTaxableContribution.Scale(escalator).Scale(oneTwelfth)
				monthlyDeferred = plan.AnnualDeferredContribution.Scale(escalator).Scale(oneTwelfth)
			}
			for month := 0; month < 12; month++ {
				for i := range walks {
					taxableFactor := one.Add(monthlyRate(annualReturn, taxRate, spreads[i]))
					deferredFactor := one.Add(monthlyRate(annualReturn, decimal.Zero, spreads[i]))
					walks[i].taxable = walks[i].taxable.Scale(taxableFactor)
					walks[i].deferred = walks[i].deferred.Scale(deferredFactor)
					if !retired {
						walks[i].taxable = walks[i].taxable.Add(monthlyTaxable)
						walks[i].deferred = walks[i].deferred.Add(monthlyDeferred)
					} else if monthlyDraw.IsPositive() {
						// Taxable first, the rest grossed up out of deferred,
						// which may go negative: the money ran out.
						fromTaxable := monthlyDraw
						if walks[i].taxable.LessThan(fromTaxable) {
							if walks[i].taxable.IsPositive() {
								fromTaxable = walks[i].taxable
							} else {
								fromTaxable = Zero
							}
						}
						walks[i].taxable = walks[i].taxable.Sub(fromTaxable)
						short := monthlyDraw.Sub(fromTaxable)
						if short.IsPositive() {
							walks[i].deferred = walks[i].deferred.Sub(short.Scale(grossUp))
						}
					}
					walks[i].taxable = FromDecimal(walks[i].taxable.Decimal().Round(workingDecimals))
					walks[i].deferred = FromDecimal(walks[i].deferred.Decimal().Round(workingDecimals))
				}
				if retired {
					drawn = drawn.Add(monthlyDraw)
				}
			}
			if !retired {
				contributed = contributed.
					Add(plan.AnnualTaxableContribution.Scale(escalator)).
					Add(plan.AnnualDeferredContribution.Scale(escalator))
				escalator = escalator.Mul(escalation)
			}
		}

		expected := walks[1].taxable.Add(walks[1].deferred)
		high := walks[2].taxable.Add(walks[2].deferred)
		low := walks[0].taxable.Add(walks[0].deferred)
		age := plan.CurrentAge + year
		growth := expected.Round().Sub(start.Round()).Sub(contributed.Round()).Add(drawn.Round())
		projection.Years = append(projection.Years, RetirementYear{
			Year:                       plan.StartYear + year,
			Age:                        age,
			Balance:                    expected.Round(),
			BalanceInTodaysDollars:     inTodaysDollars(expected, deflator),
			Contributed:                contributed.Round(),
			Drawn:                      drawn.Round(),
			Growth:                     growth,
			HighBalance:                high.Round(),
			LowBalance:                 low.Round(),
			HighBalanceInTodaysDollars: inTodaysDollars(high, deflator),
			LowBalanceInTodaysDollars:  inTodaysDollars(low, deflator),
		})

		if year > years && !projection.RunsOut && annualDraw.IsPositive() && !expected.IsPositive() {
			projection.RunsOut = true
			projection.RunsOutAtAge = age
		}
	}

	atRetirement := projection.Years[years]
	projection.BalanceAtRetirement = atRetirement.Balance
	projection.BalanceAtRetirementInTodaysDollars = atRetirement.BalanceInTodaysDollars
	projection.TotalContributed = atRetirement.Contributed
	projection.TotalGrowth = atRetirement.Growth
	return projection, nil
}
