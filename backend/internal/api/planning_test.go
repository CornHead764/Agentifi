package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Planning tools, end to end.
//
// The seeded portfolio is buildPortfolioLedger's: AAA and BBB worth 1,000.00
// each inside a brokerage whose balance is 10,000.00, plus a 4,000.00 IRA with
// no positions. Its total value is 14,000.00 — the 2,000.00 of stock, the
// 8,000.00 of brokerage cash the positions do not account for, and the IRA —
// and that is the figure the projection has to open at. A starting balance
// that summed holdings alone would open at 2,000.00 and disagree with the
// Investments page about the same accounts.
//
// The compounded figures below come from the closed form, the same way
// retirement_test.go's do:
//
//	FV = P·(1+i)^n + PMT·((1+i)^n − 1)/i     i = annual/12, n = 12·years

func retirementPlan(l *ledger, query string) map[string]any {
	l.t.Helper()
	path := "/planning/retirement"
	if query != "" {
		path += "?" + query
	}
	return l.alex.get(path).requireStatus(http.StatusOK).json()
}

func TestTheProjectionOpensAtThePortfolioTotalTheInvestmentsPageShows(t *testing.T) {
	l := buildPortfolioLedger(t)

	page := holdingsPage(l, "")
	totals := page["totals"].(map[string]any)
	require.Equal(t, "14000.00", totals["total_value"])

	body := retirementPlan(l, "")
	assumptions := body["assumptions"].(map[string]any)
	require.Equal(t, totals["total_value"], assumptions["current_balance"],
		"the two pages must not disagree about the same accounts")
	require.Equal(t, true, assumptions["is_balance_from_accounts"])
}

func TestEveryAssumptionComesBackWithTheAnswer(t *testing.T) {
	// A projection whose return rate is not on screen is a number nobody can
	// check, so the response carries the figures it was computed from —
	// defaults included.
	body := retirementPlan(buildPortfolioLedger(t), "")
	assumptions := body["assumptions"].(map[string]any)

	require.Equal(t, float64(35), assumptions["current_age"])
	require.Equal(t, float64(65), assumptions["retirement_age"])
	require.Equal(t, "0.07", assumptions["annual_return"])
	require.Equal(t, "0.025", assumptions["annual_inflation"])
	require.Equal(t, "0.04", assumptions["withdrawal_rate"])
	require.Equal(t, "0.00", assumptions["monthly_contribution"])
	require.Nil(t, assumptions["target_annual_income"])
}

func TestTheSeriesRunsFromTodayToTheLifeExpectancy(t *testing.T) {
	body := retirementPlan(buildPortfolioLedger(t), "")
	years := body["years"].([]any)

	// The default life expectancy of 85 carries the chart past retirement,
	// the way the reference product draws it: fifty years plus today.
	require.Len(t, years, 51)
	require.Equal(t, float64(30), body["years_to_retirement"])

	first := years[0].(map[string]any)
	require.Equal(t, float64(35), first["age"])
	require.Equal(t, "14000.00", first["balance"])
	require.Equal(t, "0.00", first["growth"])

	// 14,000·(1 + 0.07/12)^12 = 15,012.06.
	require.Equal(t, "15012.06", years[1].(map[string]any)["balance"])

	// The headline figures still belong to the retirement year, not the end
	// of the chart.
	atRetirement := years[30].(map[string]any)
	require.Equal(t, float64(65), atRetirement["age"])
	require.Equal(t, body["balance_at_retirement"], atRetirement["balance"])

	last := years[len(years)-1].(map[string]any)
	require.Equal(t, float64(85), last["age"])

	// Asking for no drawdown ends the chart at retirement.
	trimmed := retirementPlan(buildPortfolioLedger(t), "life_expectancy=65")
	require.Len(t, trimmed["years"].([]any), 31)
}

func TestTheHeadlineFiguresCompoundTheStatedAssumptions(t *testing.T) {
	// 14,000 at 7% for thirty years is 113,630.96; 4% of it is 4,545.24, and
	// 2.5% inflation over the same thirty years makes that 2,166.91 of today's
	// money.
	body := retirementPlan(buildPortfolioLedger(t), "")

	require.Equal(t, "113630.96", body["balance_at_retirement"])
	require.Equal(t, "54172.73", body["balance_at_retirement_in_todays_dollars"])
	require.Equal(t, "0.00", body["total_contributed"])
	require.Equal(t, "99630.96", body["total_growth"])
	require.Equal(t, "4545.24", body["annual_income"])
	require.Equal(t, "2166.91", body["annual_income_in_todays_dollars"])
}

func TestAMonthlyContributionIsCompoundedWithTheBalance(t *testing.T) {
	body := retirementPlan(buildPortfolioLedger(t), "monthly_contribution=500.00")
	require.Equal(t, "723616.46", body["balance_at_retirement"])
	require.Equal(t, "180000.00", body["total_contributed"])
	require.Equal(t, "28944.66", body["annual_income"])
}

func TestAnOverriddenBalanceSaysItIsNotFromTheAccounts(t *testing.T) {
	// "What you have" and "what you typed" are different claims, and the
	// screen labels them differently.
	body := retirementPlan(buildPortfolioLedger(t), "current_balance=50000.00")
	assumptions := body["assumptions"].(map[string]any)
	require.Equal(t, "50000.00", assumptions["current_balance"])
	require.Equal(t, false, assumptions["is_balance_from_accounts"])
}

func TestTheTargetVerdictIsAbsentUntilATargetIsStated(t *testing.T) {
	// Null rather than false: no target means no verdict, and false would read
	// as a plan that fails.
	body := retirementPlan(buildPortfolioLedger(t), "")
	require.Nil(t, body["meets_target"])
	require.Nil(t, body["shortfall"])
}

func TestTheTargetIsJudgedAgainstIncomeInTodaysDollars(t *testing.T) {
	l := buildPortfolioLedger(t)

	// 2,166.91 in today's money clears a 2,000.00 target and falls 833.09
	// short of a 3,000.00 one. Judging the nominal 4,545.24 against either
	// would call both a success.
	met := retirementPlan(l, "target_annual_income=2000.00")
	require.Equal(t, true, met["meets_target"])
	require.Equal(t, "0.00", met["shortfall"])

	missed := retirementPlan(l, "target_annual_income=3000.00")
	require.Equal(t, false, missed["meets_target"])
	require.Equal(t, "833.09", missed["shortfall"])
}

func TestARetirementAgeAlreadyReachedIsProjectedNotRefused(t *testing.T) {
	body := retirementPlan(buildPortfolioLedger(t),
		"current_age=70&retirement_age=65&life_expectancy=70")
	require.Equal(t, float64(0), body["years_to_retirement"])
	require.Len(t, body["years"].([]any), 1)
	require.Equal(t, "14000.00", body["balance_at_retirement"])
}

func TestARateInTheWrongUnitIsRefused(t *testing.T) {
	// 7 instead of 0.07 is the one mistake a projection cannot survive: it
	// compounds to a figure that looks like a rendering bug.
	l := buildPortfolioLedger(t)
	l.alex.get("/planning/retirement?annual_return=7").
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/planning/retirement?withdrawal_rate=4").
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/planning/retirement?annual_return=seven").
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAnAmountThatIsNotAnAmountIsRefused(t *testing.T) {
	l := buildPortfolioLedger(t)
	l.alex.get("/planning/retirement?current_balance=lots").
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/planning/retirement?monthly_contribution=-100.00").
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.get("/planning/retirement?current_age=999").
		requireStatus(http.StatusUnprocessableEntity)
}

func TestEveryAmountCrossesTheWireAsAString(t *testing.T) {
	// Trap 1: a JSON number here would hand the client a float64 and lose the
	// guarantee at the last possible moment.
	body := retirementPlan(buildPortfolioLedger(t), "monthly_contribution=500.00")
	for _, field := range []string{
		"balance_at_retirement", "balance_at_retirement_in_todays_dollars",
		"total_contributed", "total_growth", "annual_income",
		"annual_income_in_todays_dollars",
	} {
		require.IsType(t, "", body[field], "%s is not a string", field)
	}
	year := body["years"].([]any)[0].(map[string]any)
	for _, field := range []string{"balance", "balance_in_todays_dollars", "contributed", "growth"} {
		require.IsType(t, "", year[field], "years[].%s is not a string", field)
	}
}

func TestAnotherHouseholdGetsItsOwnProjection(t *testing.T) {
	// The starting balance is somebody's portfolio; Bob's space has none, so
	// his projection opens at zero rather than at this household's 14,000.00.
	l := buildPortfolioLedger(t)
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	body := bob.get("/planning/retirement").requireStatus(http.StatusOK).json()
	require.Equal(t, "0.00", body["assumptions"].(map[string]any)["current_balance"])
}

func TestTheDrawdownAssumptionsComeBackAndTheRunOutIsNamed(t *testing.T) {
	// 14,000 drawing 12,000 a year from 66 on, with no growth and no
	// inflation, is gone during the second year of retirement.
	body := retirementPlan(buildPortfolioLedger(t),
		"annual_return=0&annual_inflation=0&life_expectancy=85"+
			"&annual_living_expenses=12000.00&pre_retirement_tax_rate=0.22"+
			"&post_retirement_tax_rate=0.10&return_spread=0.03"+
			"&current_age=65&retirement_age=65")

	assumptions := body["assumptions"].(map[string]any)
	require.Equal(t, float64(85), assumptions["life_expectancy"])
	require.Equal(t, "12000.00", assumptions["annual_living_expenses"])
	require.Equal(t, "0.00", assumptions["annual_retirement_income"])
	require.Equal(t, "0.22", assumptions["pre_retirement_tax_rate"])
	require.Equal(t, "0.1", assumptions["post_retirement_tax_rate"])
	require.Equal(t, "0.03", assumptions["return_spread"])

	require.Equal(t, float64(67), *jsonNumber(t, body["runs_out_at_age"]))
}

// jsonNumber unwraps a nullable number field, failing the test on null.
func jsonNumber(t *testing.T, value any) *float64 {
	t.Helper()
	require.NotNil(t, value)
	number, ok := value.(float64)
	require.True(t, ok, "expected a number, got %T", value)
	return &number
}

func TestALifeExpectancyBeforeTheRetirementAgeIsRefusedAtTheDoor(t *testing.T) {
	l := buildPortfolioLedger(t)
	l.alex.get("/planning/retirement?retirement_age=70&life_expectancy=65").
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAdvancedModeSplitsThePortfolioByTaxTreatmentAndTheHalvesSum(t *testing.T) {
	l := buildPortfolioLedger(t)

	body := retirementPlan(l, "mode=advanced")
	assumptions := body["assumptions"].(map[string]any)
	advanced := assumptions["advanced"].(map[string]any)

	// The two halves are the same money the basic mode opens on, carved by
	// the tax treatment of the account each dollar sits in.
	taxable := advanced["taxable_balance"].(string)
	deferred := advanced["deferred_balance"].(string)
	require.Equal(t, "14000.00", assumptions["current_balance"],
		"the sticker total must still be the portfolio's own figure")
	sum := domain.MustFromString(taxable).Add(domain.MustFromString(deferred))
	require.Equal(t, "14000.00", sum.Round().String(),
		"taxable %s + deferred %s must equal the basic mode's balance", taxable, deferred)
	require.Equal(t, true, advanced["is_balance_from_accounts"])

	// No target inputs exist in advanced mode, so no verdict is rendered.
	require.Nil(t, body["meets_target"])
}

func TestAdvancedModeEchoesItsInputsAndWalksThem(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := retirementPlan(l,
		"mode=advanced&taxable_balance=10000.00&deferred_balance=10000.00"+
			"&annual_return=0.12&post_retirement_return=0.12"+
			"&pre_retirement_tax_rate=0.50&current_age=64&retirement_age=65"+
			"&life_expectancy=65&annual_inflation=0&return_spread=0")

	advanced := body["assumptions"].(map[string]any)["advanced"].(map[string]any)
	require.Equal(t, "10000.00", advanced["taxable_balance"])
	require.Equal(t, false, advanced["is_balance_from_accounts"])
	require.Equal(t, "0.12", advanced["post_retirement_return"])

	// The taxable half compounds at 0.5% a month, the deferred at 1%:
	// 10,616.78 + 11,268.25. The same closed forms the domain test holds.
	require.Equal(t, "21885.03", body["balance_at_retirement"])
}
