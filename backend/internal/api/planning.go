package api

import (
	"context"
	"net/http"
	"strings"

	"github.com/shopspring/decimal"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Planning tools: the retirement projection. The arithmetic is
// domain.ProjectRetirement's; this file assembles its inputs.
//
//   - Every assumption is a parameter with its default here, and the response
//     echoes whichever figure was used, so the screen can state it.
//   - The starting balance is portfolioValue's total, the one the Investments
//     page shows (calculations.md §4), never re-derived from holdings alone.
//
// The credit score has no endpoint: there is no bureau feed, and a fabricated
// score is worse than an honest absence.

func init() {
	Register(Resource{Prefix: "/planning", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/retirement", readRetirement)
	}})
}

// The assumptions when the caller states none: conventional figures, not
// derived ones. The ages are placeholders, since nothing records a date of
// birth, so the screen makes them editable.
const (
	defaultCurrentAge     = 35
	defaultRetirementAge  = 65
	defaultLifeExpectancy = 85
)

var (
	defaultAnnualReturn    = decimal.RequireFromString("0.07")
	defaultAnnualInflation = decimal.RequireFromString("0.025")
	defaultWithdrawalRate  = decimal.RequireFromString("0.04")

	// Advanced mode's post-retirement return: more conservative than the
	// working-years default, because the portfolio is no longer being fed.
	defaultPostReturn = decimal.RequireFromString("0.04")

	// The estimate band's width, ±2 points of return, stated in the response
	// so the band is checkable. The tax rates default to zero.
	defaultReturnSpread = decimal.RequireFromString("0.02")

	// The bounds a rate parameter is accepted within: narrow enough that a
	// percentage sent as 7 instead of 0.07 is refused rather than projected.
	maxAnnualReturn    = decimal.RequireFromString("0.5")
	maxAnnualInflation = decimal.RequireFromString("0.5")
	maxWithdrawalRate  = decimal.RequireFromString("0.25")
	minAnnualReturn    = decimal.RequireFromString("-0.5")
	maxTaxRate         = decimal.RequireFromString("0.99")
	maxReturnSpread    = decimal.RequireFromString("0.25")
)

// RetirementAssumptions is every input the projection used, echoed back. Rates
// are fractions: 0.07 is 7%.
type RetirementAssumptions struct {
	StartYear     int `json:"start_year"`
	CurrentAge    int `json:"current_age"`
	RetirementAge int `json:"retirement_age"`

	CurrentBalance domain.Money `json:"current_balance"`
	// IsBalanceFromAccounts is false when the caller overrode the figure; the
	// screen labels the two cases differently.
	IsBalanceFromAccounts bool         `json:"is_balance_from_accounts"`
	MonthlyContribution   domain.Money `json:"monthly_contribution"`

	AnnualReturn    domain.Rate `json:"annual_return"`
	AnnualInflation domain.Rate `json:"annual_inflation"`
	WithdrawalRate  domain.Rate `json:"withdrawal_rate"`

	// TargetAnnualIncome is null when none was stated, which is not a target
	// of zero. It is in today's dollars, like the verdict drawn against it.
	TargetAnnualIncome *domain.Money `json:"target_annual_income"`

	// The drawdown's shape: the chart runs to LifeExpectancy, and each
	// retirement year draws living expenses minus retirement income, both in
	// today's dollars. The tax rates discount the return in the phase each
	// governs, and ReturnSpread is the ± band the high and low estimates walk.
	LifeExpectancy         int          `json:"life_expectancy"`
	AnnualLivingExpenses   domain.Money `json:"annual_living_expenses"`
	AnnualRetirementIncome domain.Money `json:"annual_retirement_income"`
	PreRetirementTaxRate   domain.Rate  `json:"pre_retirement_tax_rate"`
	PostRetirementTaxRate  domain.Rate  `json:"post_retirement_tax_rate"`
	ReturnSpread           domain.Rate  `json:"return_spread"`

	// Advanced carries the tax-split inputs, and is null for a basic
	// projection: absence is the mode, not a set of zeroes.
	Advanced *AdvancedAssumptions `json:"advanced"`
}

// AdvancedAssumptions is Advanced mode's own input set, echoed back.
type AdvancedAssumptions struct {
	// The same portfolio total the basic mode opens on, split by the tax
	// treatment of each account. Overridable; the flag says whether the
	// figures are still the accounts' own.
	TaxableBalance        domain.Money `json:"taxable_balance"`
	DeferredBalance       domain.Money `json:"deferred_balance"`
	IsBalanceFromAccounts bool         `json:"is_balance_from_accounts"`

	AnnualTaxableContribution  domain.Money `json:"annual_taxable_contribution"`
	AnnualDeferredContribution domain.Money `json:"annual_deferred_contribution"`
	ContributionGrowth         domain.Rate  `json:"contribution_growth"`

	PostRetirementReturn domain.Rate `json:"post_retirement_return"`
}

// RetirementYear is one point of the balance-by-year chart.
type RetirementYear struct {
	Year                   int          `json:"year"`
	Age                    int          `json:"age"`
	Balance                domain.Money `json:"balance"`
	BalanceInTodaysDollars domain.Money `json:"balance_in_todays_dollars"`
	Contributed            domain.Money `json:"contributed"`
	Drawn                  domain.Money `json:"drawn"`
	Growth                 domain.Money `json:"growth"`

	HighBalance                domain.Money `json:"high_balance"`
	LowBalance                 domain.Money `json:"low_balance"`
	HighBalanceInTodaysDollars domain.Money `json:"high_balance_in_todays_dollars"`
	LowBalanceInTodaysDollars  domain.Money `json:"low_balance_in_todays_dollars"`
}

// RetirementResponse is the whole panel: the assumptions, the series and the
// headline answers from one walk of the plan.
type RetirementResponse struct {
	Assumptions RetirementAssumptions `json:"assumptions"`
	Years       []RetirementYear      `json:"years"`

	YearsToRetirement                  int          `json:"years_to_retirement"`
	BalanceAtRetirement                domain.Money `json:"balance_at_retirement"`
	BalanceAtRetirementInTodaysDollars domain.Money `json:"balance_at_retirement_in_todays_dollars"`
	TotalContributed                   domain.Money `json:"total_contributed"`
	TotalGrowth                        domain.Money `json:"total_growth"`

	AnnualIncome                domain.Money `json:"annual_income"`
	AnnualIncomeInTodaysDollars domain.Money `json:"annual_income_in_todays_dollars"`

	// MeetsTarget and Shortfall are null when no target was stated: there is
	// no verdict to render, and false would read as a plan that fails.
	MeetsTarget *bool         `json:"meets_target"`
	Shortfall   *domain.Money `json:"shortfall"`

	// RunsOutAtAge is null while the expected walk stays above zero through
	// the drawdown; set, it is the first year-end age the money had run out.
	RunsOutAtAge *int `json:"runs_out_at_age"`
}

func readRetirement(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	plan := domain.RetirementPlan{StartYear: env.now().Year()}

	var err error
	if plan.CurrentAge, err = queryInt(r, "current_age", defaultCurrentAge, 0, 120); err != nil {
		return err
	}
	plan.RetirementAge, err = queryInt(r, "retirement_age", defaultRetirementAge, 0, 120)
	if err != nil {
		return err
	}
	if plan.AnnualReturn, err = queryRate(r, "annual_return",
		defaultAnnualReturn, minAnnualReturn, maxAnnualReturn); err != nil {
		return err
	}
	if plan.AnnualInflation, err = queryRate(r, "annual_inflation",
		defaultAnnualInflation, minAnnualReturn, maxAnnualInflation); err != nil {
		return err
	}
	if plan.WithdrawalRate, err = queryRate(r, "withdrawal_rate",
		defaultWithdrawalRate, decimal.Zero, maxWithdrawalRate); err != nil {
		return err
	}

	if plan.LifeExpectancy, err = queryInt(r, "life_expectancy",
		defaultLifeExpectancy, 0, 120); err != nil {
		return err
	}
	if plan.LifeExpectancy != 0 && plan.LifeExpectancy < plan.RetirementAge {
		return errInvalid("out_of_range", []string{"query", "life_expectancy"},
			"life_expectancy cannot be before retirement_age")
	}
	if plan.PreRetirementTaxRate, err = queryRate(r, "pre_retirement_tax_rate",
		decimal.Zero, decimal.Zero, maxTaxRate); err != nil {
		return err
	}
	if plan.PostRetirementTaxRate, err = queryRate(r, "post_retirement_tax_rate",
		decimal.Zero, decimal.Zero, maxTaxRate); err != nil {
		return err
	}
	if plan.ReturnSpread, err = queryRate(r, "return_spread",
		defaultReturnSpread, decimal.Zero, maxReturnSpread); err != nil {
		return err
	}

	expenses, _, err := queryMoney(r, "annual_living_expenses")
	if err != nil {
		return err
	}
	if expenses.IsNegative() {
		return errInvalid("out_of_range", []string{"query", "annual_living_expenses"},
			"annual_living_expenses cannot be negative")
	}
	plan.AnnualLivingExpenses = expenses

	retirementIncome, _, err := queryMoney(r, "annual_retirement_income")
	if err != nil {
		return err
	}
	if retirementIncome.IsNegative() {
		return errInvalid("out_of_range", []string{"query", "annual_retirement_income"},
			"annual_retirement_income cannot be negative")
	}
	plan.AnnualRetirementIncome = retirementIncome

	contribution, _, err := queryMoney(r, "monthly_contribution")
	if err != nil {
		return err
	}
	if contribution.IsNegative() {
		return errInvalid("out_of_range", []string{"query", "monthly_contribution"},
			"monthly_contribution cannot be negative")
	}
	plan.MonthlyContribution = contribution

	target, hasTarget, err := queryMoney(r, "target_annual_income")
	if err != nil {
		return err
	}
	plan.TargetAnnualIncome, plan.HasTarget = target, hasTarget

	// Seeded from the accounts and overridable.
	balance, given, err := queryMoney(r, "current_balance")
	if err != nil {
		return err
	}
	if !given {
		if balance, err = investedBalance(r.Context(), env, sp); err != nil {
			return err
		}
	}
	plan.CurrentBalance = balance

	// Advanced mode reuses every shared assumption from the basic plan and
	// swaps the walk.
	var advanced *AdvancedAssumptions
	var projection domain.RetirementProjection
	if strings.EqualFold(r.URL.Query().Get("mode"), "advanced") {
		aPlan := domain.AdvancedRetirementPlan{
			StartYear:              plan.StartYear,
			CurrentAge:             plan.CurrentAge,
			RetirementAge:          plan.RetirementAge,
			LifeExpectancy:         plan.LifeExpectancy,
			AnnualLivingExpenses:   plan.AnnualLivingExpenses,
			AnnualRetirementIncome: plan.AnnualRetirementIncome,
			AnnualInflation:        plan.AnnualInflation,
			PreRetirementReturn:    plan.AnnualReturn,
			PreRetirementTaxRate:   plan.PreRetirementTaxRate,
			PostRetirementTaxRate:  plan.PostRetirementTaxRate,
			ReturnSpread:           plan.ReturnSpread,
		}
		if aPlan.PostRetirementReturn, err = queryRate(r, "post_retirement_return",
			defaultPostReturn, minAnnualReturn, maxAnnualReturn); err != nil {
			return err
		}
		if aPlan.ContributionGrowth, err = queryRate(r, "contribution_growth",
			decimal.Zero, decimal.Zero, maxAnnualReturn); err != nil {
			return err
		}
		taxableContribution, _, err := queryMoney(r, "annual_taxable_contribution")
		if err != nil {
			return err
		}
		deferredContribution, _, err := queryMoney(r, "annual_deferred_contribution")
		if err != nil {
			return err
		}
		if taxableContribution.IsNegative() || deferredContribution.IsNegative() {
			return errInvalid("out_of_range", []string{"query"},
				"a contribution cannot be negative")
		}
		aPlan.AnnualTaxableContribution = taxableContribution
		aPlan.AnnualDeferredContribution = deferredContribution

		taxable, taxableGiven, err := queryMoney(r, "taxable_balance")
		if err != nil {
			return err
		}
		deferred, deferredGiven, err := queryMoney(r, "deferred_balance")
		if err != nil {
			return err
		}
		fromAccounts := !taxableGiven && !deferredGiven
		if fromAccounts {
			if taxable, deferred, err = investedBalanceSplit(r.Context(), env, sp); err != nil {
				return err
			}
		}
		aPlan.TaxableBalance, aPlan.DeferredBalance = taxable, deferred

		if projection, err = domain.ProjectAdvancedRetirement(aPlan); err != nil {
			return errInvalid("out_of_range", []string{"query"}, "%s", err)
		}
		plan = projection.Plan
		advanced = &AdvancedAssumptions{
			TaxableBalance:             aPlan.TaxableBalance.Round(),
			DeferredBalance:            aPlan.DeferredBalance.Round(),
			IsBalanceFromAccounts:      fromAccounts,
			AnnualTaxableContribution:  aPlan.AnnualTaxableContribution.Round(),
			AnnualDeferredContribution: aPlan.AnnualDeferredContribution.Round(),
			ContributionGrowth:         aPlan.ContributionGrowth,
			PostRetirementReturn:       aPlan.PostRetirementReturn,
		}
	} else if projection, err = domain.ProjectRetirement(plan); err != nil {
		// The bounds above should catch everything the domain refuses, so
		// this is a 422 naming the problem, not a 500.
		return errInvalid("out_of_range", []string{"query"}, "%s", err)
	}

	response := RetirementResponse{
		Assumptions: RetirementAssumptions{
			StartYear:              plan.StartYear,
			CurrentAge:             plan.CurrentAge,
			RetirementAge:          plan.RetirementAge,
			CurrentBalance:         plan.CurrentBalance.Round(),
			IsBalanceFromAccounts:  !given,
			MonthlyContribution:    plan.MonthlyContribution.Round(),
			AnnualReturn:           plan.AnnualReturn,
			AnnualInflation:        plan.AnnualInflation,
			WithdrawalRate:         plan.WithdrawalRate,
			TargetAnnualIncome:     store.PtrIf(plan.TargetAnnualIncome, plan.HasTarget),
			LifeExpectancy:         plan.LifeExpectancy,
			AnnualLivingExpenses:   plan.AnnualLivingExpenses.Round(),
			AnnualRetirementIncome: plan.AnnualRetirementIncome.Round(),
			PreRetirementTaxRate:   plan.PreRetirementTaxRate,
			PostRetirementTaxRate:  plan.PostRetirementTaxRate,
			ReturnSpread:           plan.ReturnSpread,
			Advanced:               advanced,
		},
		Years:                              make([]RetirementYear, 0, len(projection.Years)),
		YearsToRetirement:                  projection.YearsToRetirement,
		BalanceAtRetirement:                projection.BalanceAtRetirement,
		BalanceAtRetirementInTodaysDollars: projection.BalanceAtRetirementInTodaysDollars,
		TotalContributed:                   projection.TotalContributed,
		TotalGrowth:                        projection.TotalGrowth,
		AnnualIncome:                       projection.AnnualIncome,
		AnnualIncomeInTodaysDollars:        projection.AnnualIncomeInTodaysDollars,
	}
	for _, year := range projection.Years {
		response.Years = append(response.Years, RetirementYear{
			Year:                       year.Year,
			Age:                        year.Age,
			Balance:                    year.Balance,
			BalanceInTodaysDollars:     year.BalanceInTodaysDollars,
			Contributed:                year.Contributed,
			Drawn:                      year.Drawn,
			Growth:                     year.Growth,
			HighBalance:                year.HighBalance,
			LowBalance:                 year.LowBalance,
			HighBalanceInTodaysDollars: year.HighBalanceInTodaysDollars,
			LowBalanceInTodaysDollars:  year.LowBalanceInTodaysDollars,
		})
	}
	if projection.RunsOut {
		age := projection.RunsOutAtAge
		response.RunsOutAtAge = &age
	}
	if plan.HasTarget {
		met := projection.MeetsTarget
		shortfall := projection.Shortfall
		response.MeetsTarget = &met
		response.Shortfall = &shortfall
	}
	return writeJSON(w, http.StatusOK, response)
}

// investedBalance is the portfolio's total value as the Investments page shows
// it. portfolioValue owns adding the account cash back after filtering
// holdings out, so it is called rather than reimplemented.
func investedBalance(
	ctx context.Context, env *Env, sp auth.SpaceContext,
) (domain.Money, error) {
	rows, err := env.DB.ListHoldings(ctx, sp.ID(), nil)
	if err != nil {
		return domain.Zero, err
	}
	securities, err := env.DB.ListSecurities(ctx, sp.ID())
	if err != nil {
		return domain.Zero, err
	}

	quotes := make(map[domain.ID]domain.Quote, len(securities))
	for _, security := range securities {
		if !security.HasLastPrice {
			continue
		}
		id := domain.ID(security.ID.String())
		quotes[id] = domain.Quote{
			SecurityID:    id,
			Price:         security.LastPrice,
			PriorClose:    security.PriorClose,
			HasPriorClose: security.HasPriorClose,
		}
	}

	holdings := make([]domain.Holding, 0, len(rows))
	for _, row := range rows {
		holdings = append(holdings, store.DomainHolding(row))
	}
	// A missing quote is a failed fetch, not a position worth nothing; seeding
	// from a portfolio short one holding would look like a real balance.
	valuations, err := domain.ValueHoldings(holdings, quotes)
	if err != nil {
		return domain.Zero, errConflict("%s", err)
	}

	totals, err := portfolioValue(ctx, env, sp, accountFilter{}, valuations)
	if err != nil {
		return domain.Zero, err
	}
	return totals.TotalValue, nil
}

// queryRate reads a rate parameter as a fraction (0.07, never 7), bounded so a
// percentage sent in the wrong unit is refused.
func queryRate(
	r *http.Request, key string, fallback, low, high decimal.Decimal,
) (domain.Rate, error) {
	raw := strings.TrimSpace(r.URL.Query().Get(key))
	if raw == "" {
		return fallback, nil
	}
	value, err := decimal.NewFromString(raw)
	if err != nil {
		return decimal.Zero, errInvalid("decimal_parsing", []string{"query", key},
			"%s must be a decimal fraction such as \"0.07\"", key)
	}
	if value.LessThan(low) || value.GreaterThan(high) {
		return decimal.Zero, errInvalid("out_of_range", []string{"query", key},
			"%s must be between %s and %s", key, low, high)
	}
	return value, nil
}

// queryMoney reads an amount parameter as a string, the way money crosses the
// wire everywhere else.
// deferredAccountTypes are the account types whose withdrawals are income.
// Roth accounts, HSAs and 529s hold already-taxed dollars, so they land in the
// taxable bucket.
var deferredAccountTypes = map[string]bool{
	"401k":       true,
	"403b":       true,
	"ira":        true,
	"sep_ira":    true,
	"simple_ira": true,
	"keogh":      true,
}

// investedBalanceSplit is investedBalance carved by tax treatment; the two
// halves sum to exactly the figure the basic mode opens on.
func investedBalanceSplit(
	ctx context.Context, env *Env, sp auth.SpaceContext,
) (taxable, deferred domain.Money, err error) {
	rows, err := env.DB.ListHoldings(ctx, sp.ID(), nil)
	if err != nil {
		return domain.Zero, domain.Zero, err
	}
	securities, err := env.DB.ListSecurities(ctx, sp.ID())
	if err != nil {
		return domain.Zero, domain.Zero, err
	}
	quotes := make(map[domain.ID]domain.Quote, len(securities))
	for _, security := range securities {
		if !security.HasLastPrice {
			continue
		}
		id := domain.ID(security.ID.String())
		quotes[id] = domain.Quote{
			SecurityID:    id,
			Price:         security.LastPrice,
			PriorClose:    security.PriorClose,
			HasPriorClose: security.HasPriorClose,
		}
	}
	holdings := make([]domain.Holding, 0, len(rows))
	for _, row := range rows {
		holdings = append(holdings, store.DomainHolding(row))
	}
	valuations, err := domain.ValueHoldings(holdings, quotes)
	if err != nil {
		return domain.Zero, domain.Zero, errConflict("%s", err)
	}

	accounts, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{})
	if err != nil {
		return domain.Zero, domain.Zero, err
	}
	typeOf := make(map[domain.ID]string, len(accounts))
	investment := make([]store.Account, 0, len(accounts))
	for _, account := range accounts {
		typeOf[domain.ID(account.ID.String())] = account.Type
		if account.Kind == domain.KindInvestment {
			investment = append(investment, account)
		}
	}

	add := func(accountType string, amount domain.Money) {
		if deferredAccountTypes[accountType] {
			deferred = deferred.Add(amount)
		} else {
			taxable = taxable.Add(amount)
		}
	}

	for _, valuation := range valuations {
		add(typeOf[valuation.Holding.AccountID], valuation.MarketValue)
	}

	postings, err := postingsByAccount(ctx, env, sp, investment)
	if err != nil {
		return domain.Zero, domain.Zero, err
	}
	for account, cash := range domain.CashOutsideHoldings(investmentBalances(investment, postings), valuations) {
		add(typeOf[account], cash)
	}
	return taxable, deferred, nil
}
