package domain

import (
	"testing"
	"time"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

// invTestLot builds a lot; an empty cost per share is one whose purchase price
// never arrived.
func invTestLot(shares, costPerShare string) Lot {
	lot := Lot{Shares: decimal.RequireFromString(shares), AcquiredOn: NewDate(2024, time.January, 15)}
	if costPerShare != "" {
		lot.CostPerShare = decimal.RequireFromString(costPerShare)
		lot.HasCostPerShare = true
	}
	return lot
}

func invTestHolding(shares string, lots ...Lot) Holding {
	return Holding{
		ID:         "hold-1",
		AccountID:  "acct-brokerage",
		SecurityID: "sec-acme",
		Shares:     decimal.RequireFromString(shares),
		Lots:       lots,
	}
}

// invTestQuote builds a quote; an empty prior close is a security with no full
// session on file yet.
func invTestQuote(price, priorClose string) Quote {
	quote := Quote{
		SecurityID: "sec-acme",
		Price:      decimal.RequireFromString(price),
		AsOf:       NewDate(2026, time.August, 21),
	}
	if priorClose != "" {
		quote.PriorClose = decimal.RequireFromString(priorClose)
		quote.HasPriorClose = true
	}
	return quote
}

func invTestKnownHolding() Holding { return invTestHolding("10", invTestLot("10", "100.00")) }

// invTestUnknownHolding is a position imported without its lots.
func invTestUnknownHolding() Holding {
	holding := invTestHolding("5")
	holding.ID = "hold-2"
	holding.SecurityID = "sec-other"
	return holding
}

func invTestFlow(amount string, on Date) CashFlow {
	return CashFlow{On: on, Amount: MustFromString(amount)}
}

func TestMarketValueIsSharesAtTheLatestPrice(t *testing.T) {
	require.Equal(t, "1500.00", MarketValue(invTestHolding("10"), invTestQuote("150.00", "140.00")).String())
}

func TestFractionalSharesRoundOnceAtTheEnd(t *testing.T) {
	require.Equal(t, "50.00", MarketValue(invTestHolding("0.333333"), invTestQuote("150.00", "140.00")).String())
}

func TestTheCostBasisSumsTheLotsWhenEveryOneIsKnown(t *testing.T) {
	position := invTestHolding("10", invTestLot("4", "100.00"), invTestLot("6", "120.00"))
	basis, ok := CostBasis(position)
	require.True(t, ok)
	require.Equal(t, "1120.00", basis.String())
}

func TestAHoldingWithNoLotsHasAnUnknownBasisNotABasisOfZero(t *testing.T) {
	_, ok := CostBasis(invTestHolding("10"))
	require.False(t, ok)
}

func TestOneLotWithoutAPurchasePricePoisonsTheWholeBasis(t *testing.T) {
	position := invTestHolding("10", invTestLot("4", "100.00"), invTestLot("6", ""))
	_, ok := CostBasis(position)
	require.False(t, ok)
}

func TestLotsThatDoNotAccountForEveryShareAreUnknown(t *testing.T) {
	_, ok := CostBasis(invTestHolding("10", invTestLot("4", "100.00")))
	require.False(t, ok)
}

func TestTotalGainIsMarketValueLessWhatThePositionCost(t *testing.T) {
	gain, ok := ValueHolding(invTestHolding("10", invTestLot("10", "100.00")), invTestQuote("150.00", "140.00")).TotalGain()
	require.True(t, ok)
	require.Equal(t, "500.00", gain.String())
}

func TestAnUnknownBasisGivesNoGainRatherThanTheWholePosition(t *testing.T) {
	// market_value − 0 would report the entire position as profit.
	_, ok := ValueHolding(invTestHolding("10"), invTestQuote("150.00", "140.00")).TotalGain()
	require.False(t, ok)
}

func TestTheDayChangeIsSharesTimesTheMoveFromThePriorClose(t *testing.T) {
	change, ok := DayChange(invTestHolding("10"), invTestQuote("150.00", "140.00"))
	require.True(t, ok)
	require.Equal(t, "100.00", change.String())
}

func TestNoPriorCloseMeansTheMoveIsUnknownNotZero(t *testing.T) {
	_, ok := DayChange(invTestHolding("10"), invTestQuote("150.00", ""))
	require.False(t, ok)
}

func TestTheDayChangePercentageIsOfYesterdaysValue(t *testing.T) {
	// $100 on a position worth $1,400 at yesterday's close, not $1,500.
	quote := invTestQuote("150.00", "140.00")
	value := MarketValue(invTestHolding("10"), quote)
	change, ok := DayChange(invTestHolding("10"), quote)
	require.True(t, ok)

	pct, ok := DayChangePct(change, true, value)
	require.True(t, ok)
	require.Equal(t, "7.14", pct.String())
}

func TestAnUnknownMoveHasNoPercentage(t *testing.T) {
	_, ok := DayChangePct(Zero, false, MustFromString("1500.00"))
	require.False(t, ok)
}

func TestAValuationRowCarriesItsOwnFigures(t *testing.T) {
	row := ValueHolding(invTestKnownHolding(), invTestQuote("150.00", "140.00"))
	require.Equal(t, "1500.00", row.MarketValue.String())
	require.True(t, row.HasCostBasis)
	require.Equal(t, "1000.00", row.CostBasis.String())

	gain, ok := row.TotalGain()
	require.True(t, ok)
	require.Equal(t, "500.00", gain.String())

	pct, ok := row.TotalGainPct()
	require.True(t, ok)
	require.Equal(t, "50", pct.String())

	require.True(t, row.HasDayChange)
	require.Equal(t, "100.00", row.DayChange.String())
}

func TestARowWithoutLotsRendersDashesForTheCostColumns(t *testing.T) {
	row := ValueHolding(invTestHolding("10"), invTestQuote("150.00", "140.00"))
	require.False(t, row.HasCostBasis)
	_, ok := row.TotalGain()
	require.False(t, ok)
	_, ok = row.TotalGainPct()
	require.False(t, ok)
}

func TestAHoldingWithNoQuoteAndNoValueFailsRatherThanShrinkingThePortfolio(t *testing.T) {
	_, err := ValueHoldings([]Holding{invTestHolding("10")}, map[ID]Quote{})
	require.ErrorContains(t, err, "neither a quote nor a market value")
}

func TestCashOutsideHoldingsIsTheBalanceLessItsPositions(t *testing.T) {
	// A brokerage account at 1,000.00 holding positions of 600.00 and 300.00
	// has 100.00 of cash beside them; a cash-only account keeps its whole
	// 250.00; a position under no listed account adds nothing.
	held := func(account ID, value string) HoldingValuation {
		return HoldingValuation{Holding: Holding{AccountID: account}, MarketValue: MustFromString(value)}
	}
	cash := CashOutsideHoldings(
		map[ID]Money{"brokerage": MustFromString("1000.00"), "cash-only": MustFromString("250.00")},
		[]HoldingValuation{held("brokerage", "600.00"), held("brokerage", "300.00"), held("elsewhere", "75.00")},
	)
	require.Len(t, cash, 2)
	require.Equal(t, "100.00", cash["brokerage"].String())
	require.Equal(t, "250.00", cash["cash-only"].String())
}

func TestAnUnquotedHoldingIsValuedAtWhatTheProviderSaysItIsWorth(t *testing.T) {
	// An employer plan's fund has no public price.
	holding := invTestHolding("10")
	holding.MarketValue, holding.HasMarketValue = MustFromString("45678.90"), true

	rows, err := ValueHoldings([]Holding{holding}, map[ID]Quote{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "45678.90", rows[0].MarketValue.String())
	require.True(t, rows[0].IsUnquoted)
	require.False(t, rows[0].HasDayChange)
}

func TestAQuoteBeatsAStoredMarketValue(t *testing.T) {
	// Shares times today's price is the fresher figure whenever there is one.
	holding := invTestHolding("10")
	holding.MarketValue, holding.HasMarketValue = MustFromString("1.00"), true

	rows, err := ValueHoldings([]Holding{holding},
		map[ID]Quote{holding.SecurityID: invTestQuote("150.00", "140.00")})
	require.NoError(t, err)
	require.Equal(t, "1500.00", rows[0].MarketValue.String())
	require.False(t, rows[0].IsUnquoted)
}

func invTestMixedTotals() PortfolioTotals {
	quote := invTestQuote("150.00", "140.00")
	return NewPortfolioTotals([]HoldingValuation{
		ValueHolding(invTestKnownHolding(), quote),
		ValueHolding(invTestUnknownHolding(), quote),
	})
}

func TestPortfolioTotalsAddUpWhenEveryBasisIsKnown(t *testing.T) {
	totals := NewPortfolioTotals([]HoldingValuation{
		ValueHolding(invTestKnownHolding(), invTestQuote("150.00", "140.00")),
	})
	require.Equal(t, "1500.00", totals.MarketValue.String())
	require.True(t, totals.HasCostBasis)
	require.Equal(t, "1000.00", totals.CostBasis.String())
	require.Equal(t, "500.00", totals.TotalGain.String())
	require.False(t, totals.IsCostBasisIncomplete)
}

func TestAMissingBasisFlagsTheTotalIncomplete(t *testing.T) {
	require.True(t, invTestMixedTotals().IsCostBasisIncomplete)
}

func TestTheGainCoversOnlyTheHoldingsWithAKnownBasis(t *testing.T) {
	// market_value − partial_basis would report the unpriced position as pure
	// profit: $2,250 − $1,000 = $1,250 instead of $500.
	totals := invTestMixedTotals()
	require.Equal(t, "2250.00", totals.MarketValue.String())
	require.Equal(t, "1000.00", totals.CostBasis.String())
	require.Equal(t, "500.00", totals.TotalGain.String())
}

func TestAPortfolioWithNoKnownBasisAtAllHasNone(t *testing.T) {
	totals := NewPortfolioTotals([]HoldingValuation{
		ValueHolding(invTestUnknownHolding(), invTestQuote("150.00", "140.00")),
	})
	require.False(t, totals.HasCostBasis)
	require.True(t, totals.IsCostBasisIncomplete)
}

func TestAMissingPriorCloseFlagsTheDayChangeTheSameWay(t *testing.T) {
	totals := NewPortfolioTotals([]HoldingValuation{
		ValueHolding(invTestKnownHolding(), invTestQuote("150.00", "140.00")),
		ValueHolding(invTestUnknownHolding(), invTestQuote("150.00", "")),
	})
	require.True(t, totals.HasDayChange)
	require.Equal(t, "100.00", totals.DayChange.String())
	require.True(t, totals.IsDayChangeIncomplete)
}

func TestAllocationIsEachSecuritysShareOfMarketValue(t *testing.T) {
	quote := invTestQuote("150.00", "140.00")
	shares, ok := Allocation([]HoldingValuation{
		ValueHolding(invTestKnownHolding(), quote),
		ValueHolding(invTestUnknownHolding(), quote),
	})
	require.True(t, ok)
	expected := decimal.RequireFromString("1500.00").Div(decimal.RequireFromString("2250.00"))
	require.True(t, shares["sec-acme"].Equal(expected))
}

func TestGrowthWithNoFlowsIsThePlainReturn(t *testing.T) {
	points := []ValuePoint{
		{NewDate(2026, time.January, 1), MustFromString("1000")},
		{NewDate(2026, time.December, 31), MustFromString("1100")},
	}
	rate, ok := TimeWeightedReturn(points, nil)
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestADepositIsNotAReturn(t *testing.T) {
	// The account ends 21% up on its starting value, but $100 of that was paid
	// in. Chain-linking at the flow leaves the true 10%.
	points := []ValuePoint{
		{NewDate(2026, time.January, 1), MustFromString("1000")},
		{NewDate(2026, time.July, 1), MustFromString("1100")},
		{NewDate(2026, time.December, 31), MustFromString("1210")},
	}
	rate, ok := TimeWeightedReturn(points, []CashFlow{invTestFlow("100", NewDate(2026, time.July, 1))})
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestAWithdrawalDoesNotReadAsALoss(t *testing.T) {
	points := []ValuePoint{
		{NewDate(2026, time.January, 1), MustFromString("1000")},
		{NewDate(2026, time.July, 1), MustFromString("900")},
		{NewDate(2026, time.December, 31), MustFromString("990")},
	}
	rate, ok := TimeWeightedReturn(points, []CashFlow{invTestFlow("-100", NewDate(2026, time.July, 1))})
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestASubPeriodThatStartsEmptyContributesNoReturn(t *testing.T) {
	points := []ValuePoint{
		{NewDate(2026, time.January, 1), Zero},
		{NewDate(2026, time.July, 1), MustFromString("1000")},
		{NewDate(2026, time.December, 31), MustFromString("1100")},
	}
	rate, ok := TimeWeightedReturn(points, []CashFlow{invTestFlow("1000", NewDate(2026, time.July, 1))})
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestFewerThanTwoPointsIsNoWindowAtAll(t *testing.T) {
	_, ok := TimeWeightedReturn(nil, nil)
	require.False(t, ok)

	_, ok = TimeWeightedReturn([]ValuePoint{{NewDate(2026, time.January, 1), MustFromString("10")}}, nil)
	require.False(t, ok)
}

func TestASingleContributionReturningTenPercentOverAYear(t *testing.T) {
	rate, ok := InternalRateOfReturn(
		[]CashFlow{invTestFlow("1000", NewDate(2025, time.January, 1))},
		ValuePoint{NewDate(2026, time.January, 1), MustFromString("1100")},
		nil,
	)
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestALossSolvesToANegativeRate(t *testing.T) {
	rate, ok := InternalRateOfReturn(
		[]CashFlow{invTestFlow("1000", NewDate(2025, time.January, 1))},
		ValuePoint{NewDate(2026, time.January, 1), MustFromString("900")},
		nil,
	)
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("-0.100000")), "got %s", rate)
}

func TestContributionsAreWeightedByHowLongTheyWereInvested(t *testing.T) {
	// $1,000 for two years and $1,000 for one, both at 10%, ends at $2,310.
	rate, ok := InternalRateOfReturn(
		[]CashFlow{
			invTestFlow("1000", NewDate(2025, time.January, 1)),
			invTestFlow("1000", NewDate(2026, time.January, 1)),
		},
		ValuePoint{NewDate(2027, time.January, 1), MustFromString("2310")},
		nil,
	)
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestAStartingValueIsTreatedAsAContributionOnItsOwnDate(t *testing.T) {
	rate, ok := InternalRateOfReturn(
		nil,
		ValuePoint{NewDate(2026, time.January, 1), MustFromString("1100")},
		&ValuePoint{NewDate(2025, time.January, 1), MustFromString("1000")},
	)
	require.True(t, ok)
	require.True(t, rate.Equal(decimal.RequireFromString("0.100000")), "got %s", rate)
}

func TestASeriesThatNeverChangesSignHasNoRate(t *testing.T) {
	_, ok := InternalRateOfReturn(
		[]CashFlow{invTestFlow("1000", NewDate(2025, time.January, 1))},
		ValuePoint{NewDate(2026, time.January, 1), Zero},
		nil,
	)
	require.False(t, ok)
}

func TestARootBeyondTheSearchBracketIsReportedAsNoneNotAsAWrongRoot(t *testing.T) {
	// A ten-million-fold gain in a day is past anything the solver brackets.
	_, ok := InternalRateOfReturn(
		[]CashFlow{invTestFlow("100", NewDate(2026, time.August, 20))},
		ValuePoint{NewDate(2026, time.August, 21), MustFromString("1000000000")},
		nil,
	)
	require.False(t, ok)
}

func TestTheRateReadsAsAPercentageForTheChart(t *testing.T) {
	rate, ok := InternalRateOfReturn(
		[]CashFlow{invTestFlow("1000", NewDate(2025, time.January, 1))},
		ValuePoint{NewDate(2026, time.January, 1), MustFromString("1100")},
		nil,
	)
	pct, ok := RatePct(rate, ok)
	require.True(t, ok)
	require.True(t, pct.Equal(decimal.RequireFromString("10.00")), "got %s", pct)

	_, ok = RatePct(decimal.Zero, false)
	require.False(t, ok)
}

func TestAllocationSumsASecurityHeldInMoreThanOneAccount(t *testing.T) {
	// One security in a taxable account and an IRA is one slice; last-wins
	// would show only the IRA's shares.
	quote := invTestQuote("150.00", "140.00")

	taxable := invTestKnownHolding()
	ira := invTestKnownHolding()
	ira.ID = "hold-ira"
	ira.AccountID = "acct-ira"

	other := invTestUnknownHolding()

	shares, ok := Allocation([]HoldingValuation{
		ValueHolding(taxable, quote),
		ValueHolding(ira, quote),
		ValueHolding(other, quote),
	})
	require.True(t, ok)
	require.Len(t, shares, 2)

	// $3,000 of sec-acme against a $3,750 portfolio, not $1,500 of it.
	require.True(t, shares["sec-acme"].Equal(decimal.RequireFromString("0.8")), "got %s", shares["sec-acme"])
	require.True(t, shares["sec-other"].Equal(decimal.RequireFromString("0.2")), "got %s", shares["sec-other"])
}

func TestAllocationGroupsByWhateverTheCallerAsksFor(t *testing.T) {
	// Two accounts, one of them holding both securities. By account the
	// portfolio is $1,500 taxable and $2,250 in the IRA against $3,750; by
	// security it is $3,000 of sec-acme and $750 of sec-other.
	quote := invTestQuote("150.00", "140.00")

	taxable := invTestKnownHolding()
	ira := invTestKnownHolding()
	ira.ID = "hold-ira"
	ira.AccountID = "acct-ira"
	other := invTestUnknownHolding()
	other.AccountID = "acct-ira"

	byAccount, ok := AllocationBy([]HoldingValuation{
		ValueHolding(taxable, quote),
		ValueHolding(ira, quote),
		ValueHolding(other, quote),
	}, func(v HoldingValuation) ID { return v.Holding.AccountID })
	require.True(t, ok)
	require.Len(t, byAccount, 2)
	require.True(t, byAccount["acct-brokerage"].Equal(decimal.RequireFromString("0.4")),
		"got %s", byAccount["acct-brokerage"])
	require.True(t, byAccount["acct-ira"].Equal(decimal.RequireFromString("0.6")),
		"got %s", byAccount["acct-ira"])
}

func TestAPortfolioWorthNothingHasNoGroupsToAllocate(t *testing.T) {
	_, ok := AllocationBy(nil, func(v HoldingValuation) ID { return v.Holding.AccountID })
	require.False(t, ok)
}

func TestAPriceSeriesReportsItsMoveAndTheFractionOfWhereItStarted(t *testing.T) {
	// 40.00 to 46.00 is +6.00, 0.15 of the open. Points are out of order on
	// purpose.
	change, pct, hasPct, ok := PriceChange([]PricePoint{
		{NewDate(2026, time.March, 3), decimal.RequireFromString("42.50")},
		{NewDate(2026, time.March, 1), decimal.RequireFromString("40.00")},
		{NewDate(2026, time.March, 5), decimal.RequireFromString("46.00")},
	})
	require.True(t, ok)
	require.True(t, hasPct)
	require.True(t, change.Equal(decimal.RequireFromString("6")), "got %s", change)
	require.True(t, pct.Equal(decimal.RequireFromString("0.15")), "got %s", pct)
}

func TestOneCloseIsNoMove(t *testing.T) {
	_, _, _, ok := PriceChange([]PricePoint{
		{NewDate(2026, time.March, 1), decimal.RequireFromString("40.00")},
	})
	require.False(t, ok)
}

func TestASeriesOpeningAtNothingHasAMoveButNoPercentage(t *testing.T) {
	change, _, hasPct, ok := PriceChange([]PricePoint{
		{NewDate(2026, time.March, 1), decimal.Zero},
		{NewDate(2026, time.March, 2), decimal.RequireFromString("12.00")},
	})
	require.True(t, ok)
	require.False(t, hasPct)
	require.True(t, change.Equal(decimal.RequireFromString("12")))
}
