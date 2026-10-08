package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/shopspring/decimal"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
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
// The credit score has no method: there is no bureau feed, and a fabricated
// score is worse than an honest absence.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewPlanningServiceHandler(planningService{env}, opts...)
	})
}

type planningService struct{ env *Env }

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

func (s planningService) GetRetirementProjection(
	ctx context.Context, req *agentifiv1.GetRetirementProjectionRequest,
) (*agentifiv1.GetRetirementProjectionResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	plan := domain.RetirementPlan{StartYear: env.now().Year()}

	var err error
	if plan.CurrentAge, err = ageParam(req.CurrentAge, "current_age", defaultCurrentAge); err != nil {
		return nil, err
	}
	if plan.RetirementAge, err = ageParam(req.RetirementAge, "retirement_age", defaultRetirementAge); err != nil {
		return nil, err
	}
	if plan.AnnualReturn, err = rateParam(req.GetAnnualReturn(), "annual_return",
		defaultAnnualReturn, minAnnualReturn, maxAnnualReturn); err != nil {
		return nil, err
	}
	if plan.AnnualInflation, err = rateParam(req.GetAnnualInflation(), "annual_inflation",
		defaultAnnualInflation, minAnnualReturn, maxAnnualInflation); err != nil {
		return nil, err
	}
	if plan.WithdrawalRate, err = rateParam(req.GetWithdrawalRate(), "withdrawal_rate",
		defaultWithdrawalRate, decimal.Zero, maxWithdrawalRate); err != nil {
		return nil, err
	}

	if plan.LifeExpectancy, err = ageParam(req.LifeExpectancy, "life_expectancy", defaultLifeExpectancy); err != nil {
		return nil, err
	}
	if plan.LifeExpectancy != 0 && plan.LifeExpectancy < plan.RetirementAge {
		return nil, errInvalid("out_of_range", []string{"query", "life_expectancy"},
			"life_expectancy cannot be before retirement_age")
	}
	if plan.PreRetirementTaxRate, err = rateParam(req.GetPreRetirementTaxRate(), "pre_retirement_tax_rate",
		decimal.Zero, decimal.Zero, maxTaxRate); err != nil {
		return nil, err
	}
	if plan.PostRetirementTaxRate, err = rateParam(req.GetPostRetirementTaxRate(), "post_retirement_tax_rate",
		decimal.Zero, decimal.Zero, maxTaxRate); err != nil {
		return nil, err
	}
	if plan.ReturnSpread, err = rateParam(req.GetReturnSpread(), "return_spread",
		defaultReturnSpread, decimal.Zero, maxReturnSpread); err != nil {
		return nil, err
	}

	if plan.AnnualLivingExpenses, err = nonNegativeMoneyParam(
		req.GetAnnualLivingExpenses(), "annual_living_expenses"); err != nil {
		return nil, err
	}
	if plan.AnnualRetirementIncome, err = nonNegativeMoneyParam(
		req.GetAnnualRetirementIncome(), "annual_retirement_income"); err != nil {
		return nil, err
	}
	if plan.MonthlyContribution, err = nonNegativeMoneyParam(
		req.GetMonthlyContribution(), "monthly_contribution"); err != nil {
		return nil, err
	}
	if plan.TargetAnnualIncome, plan.HasTarget, err = moneyParam(
		req.GetTargetAnnualIncome(), "target_annual_income"); err != nil {
		return nil, err
	}

	// Seeded from the accounts and overridable.
	balance, given, err := moneyParam(req.GetCurrentBalance(), "current_balance")
	if err != nil {
		return nil, err
	}
	if !given {
		if balance, err = investedBalance(ctx, env, sp); err != nil {
			return nil, err
		}
	}
	plan.CurrentBalance = balance

	// Advanced mode reuses every shared assumption from the basic plan and
	// swaps the walk.
	var advanced *agentifiv1.AdvancedRetirementAssumptions
	var projection domain.RetirementProjection
	if strings.EqualFold(req.GetMode(), "advanced") {
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
		if aPlan.PostRetirementReturn, err = rateParam(req.GetPostRetirementReturn(), "post_retirement_return",
			defaultPostReturn, minAnnualReturn, maxAnnualReturn); err != nil {
			return nil, err
		}
		if aPlan.ContributionGrowth, err = rateParam(req.GetContributionGrowth(), "contribution_growth",
			decimal.Zero, decimal.Zero, maxAnnualReturn); err != nil {
			return nil, err
		}
		taxableContribution, _, err := moneyParam(req.GetAnnualTaxableContribution(), "annual_taxable_contribution")
		if err != nil {
			return nil, err
		}
		deferredContribution, _, err := moneyParam(req.GetAnnualDeferredContribution(), "annual_deferred_contribution")
		if err != nil {
			return nil, err
		}
		if taxableContribution.IsNegative() || deferredContribution.IsNegative() {
			return nil, errInvalid("out_of_range", []string{"query"},
				"a contribution cannot be negative")
		}
		aPlan.AnnualTaxableContribution = taxableContribution
		aPlan.AnnualDeferredContribution = deferredContribution

		taxable, taxableGiven, err := moneyParam(req.GetTaxableBalance(), "taxable_balance")
		if err != nil {
			return nil, err
		}
		deferred, deferredGiven, err := moneyParam(req.GetDeferredBalance(), "deferred_balance")
		if err != nil {
			return nil, err
		}
		fromAccounts := !taxableGiven && !deferredGiven
		if fromAccounts {
			if taxable, deferred, err = investedBalanceSplit(ctx, env, sp); err != nil {
				return nil, err
			}
		}
		aPlan.TaxableBalance, aPlan.DeferredBalance = taxable, deferred

		if projection, err = domain.ProjectAdvancedRetirement(aPlan); err != nil {
			return nil, errInvalid("out_of_range", []string{"query"}, "%s", err)
		}
		plan = projection.Plan
		advanced = &agentifiv1.AdvancedRetirementAssumptions{
			TaxableBalance:             moneyProto(aPlan.TaxableBalance.Round()),
			DeferredBalance:            moneyProto(aPlan.DeferredBalance.Round()),
			IsBalanceFromAccounts:      fromAccounts,
			AnnualTaxableContribution:  moneyProto(aPlan.AnnualTaxableContribution.Round()),
			AnnualDeferredContribution: moneyProto(aPlan.AnnualDeferredContribution.Round()),
			ContributionGrowth:         aPlan.ContributionGrowth.String(),
			PostRetirementReturn:       aPlan.PostRetirementReturn.String(),
		}
	} else if projection, err = domain.ProjectRetirement(plan); err != nil {
		// The bounds above should catch everything the domain refuses, so
		// this is a 422 naming the problem, not a 500.
		return nil, errInvalid("out_of_range", []string{"query"}, "%s", err)
	}

	out := &agentifiv1.GetRetirementProjectionResponse{
		Assumptions: &agentifiv1.RetirementAssumptions{
			StartYear:              int32(plan.StartYear),
			CurrentAge:             int32(plan.CurrentAge),
			RetirementAge:          int32(plan.RetirementAge),
			CurrentBalance:         moneyProto(plan.CurrentBalance.Round()),
			IsBalanceFromAccounts:  !given,
			MonthlyContribution:    moneyProto(plan.MonthlyContribution.Round()),
			AnnualReturn:           plan.AnnualReturn.String(),
			AnnualInflation:        plan.AnnualInflation.String(),
			WithdrawalRate:         plan.WithdrawalRate.String(),
			TargetAnnualIncome:     nullableMoneyProto(plan.TargetAnnualIncome, plan.HasTarget),
			LifeExpectancy:         int32(plan.LifeExpectancy),
			AnnualLivingExpenses:   moneyProto(plan.AnnualLivingExpenses.Round()),
			AnnualRetirementIncome: moneyProto(plan.AnnualRetirementIncome.Round()),
			PreRetirementTaxRate:   plan.PreRetirementTaxRate.String(),
			PostRetirementTaxRate:  plan.PostRetirementTaxRate.String(),
			ReturnSpread:           plan.ReturnSpread.String(),
			Advanced:               advanced,
		},
		Years:                              make([]*agentifiv1.RetirementYear, 0, len(projection.Years)),
		YearsToRetirement:                  int32(projection.YearsToRetirement),
		BalanceAtRetirement:                moneyProto(projection.BalanceAtRetirement),
		BalanceAtRetirementInTodaysDollars: moneyProto(projection.BalanceAtRetirementInTodaysDollars),
		TotalContributed:                   moneyProto(projection.TotalContributed),
		TotalGrowth:                        moneyProto(projection.TotalGrowth),
		AnnualIncome:                       moneyProto(projection.AnnualIncome),
		AnnualIncomeInTodaysDollars:        moneyProto(projection.AnnualIncomeInTodaysDollars),
	}
	for _, year := range projection.Years {
		out.Years = append(out.Years, &agentifiv1.RetirementYear{
			Year:                       int32(year.Year),
			Age:                        int32(year.Age),
			Balance:                    moneyProto(year.Balance),
			BalanceInTodaysDollars:     moneyProto(year.BalanceInTodaysDollars),
			Contributed:                moneyProto(year.Contributed),
			Drawn:                      moneyProto(year.Drawn),
			Growth:                     moneyProto(year.Growth),
			HighBalance:                moneyProto(year.HighBalance),
			LowBalance:                 moneyProto(year.LowBalance),
			HighBalanceInTodaysDollars: moneyProto(year.HighBalanceInTodaysDollars),
			LowBalanceInTodaysDollars:  moneyProto(year.LowBalanceInTodaysDollars),
		})
	}
	if projection.RunsOut {
		out.RunsOutAtAge = proto.Int32(int32(projection.RunsOutAtAge))
	}
	// No target is no verdict: false would read as a plan that fails.
	if plan.HasTarget {
		out.MeetsTarget = proto.Bool(projection.MeetsTarget)
		out.Shortfall = nullableMoneyProto(projection.Shortfall, true)
	}
	return out, nil
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

// ageParam reads an age parameter, bounded to a human lifespan.
func ageParam(value *int32, key string, fallback int) (int, error) {
	if value == nil {
		return fallback, nil
	}
	if *value < 0 || *value > 120 {
		return 0, errInvalid("out_of_range", []string{"query", key},
			"%s must be between %d and %d", key, 0, 120)
	}
	return int(*value), nil
}

// rateParam reads a rate parameter as a fraction (0.07, never 7), bounded so a
// percentage sent in the wrong unit is refused.
func rateParam(raw, key string, fallback, low, high decimal.Decimal) (domain.Rate, error) {
	raw = strings.TrimSpace(raw)
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

// moneyParam reads an amount parameter, reporting whether it was sent.
func moneyParam(value *agentifiv1.NullableMoney, key string) (domain.Money, bool, error) {
	if value == nil {
		return domain.Zero, false, nil
	}
	amount, err := moneyFrom(value, "query", key)
	return amount, err == nil, err
}

func nonNegativeMoneyParam(value *agentifiv1.NullableMoney, key string) (domain.Money, error) {
	amount, _, err := moneyParam(value, key)
	if err != nil {
		return domain.Zero, err
	}
	if amount.IsNegative() {
		return domain.Zero, errInvalid("out_of_range", []string{"query", key}, "%s cannot be negative", key)
	}
	return amount, nil
}

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
