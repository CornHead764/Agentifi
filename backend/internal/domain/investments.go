package domain

import (
	"fmt"
	"slices"

	"github.com/shopspring/decimal"
)

// Investments. A holding without lots has an unknown cost basis, not zero:
// market value less zero would show the whole position as gain. Unknown is a
// Has… flag all the way up, and the portfolio sets IsCostBasisIncomplete.
// TWR measures the investments (flows removed); IRR measures the investor
// (money-weighted). Both are a toggle on one chart.

var (
	irrDaysInYear = decimal.NewFromInt(365)
	// irrMinRate: (1 + rate) must stay positive to have a logarithm.
	irrMinRate = decimal.RequireFromString("-0.999999")
	// rateDecimals is the precision a rate is reported at; rates are not money.
	rateDecimals int32 = 6
	// irrBrackets are scanned for a sign change before bisection; a root
	// outside them is reported as none.
	irrBrackets = []string{
		"-0.999999", "-0.99", "-0.9", "-0.5", "-0.1", "0", "0.1",
		"0.5", "1", "10", "100", "1000", "10000", "1000000",
	}
)

// irrPrecision is the working precision for the solver's fractional powers,
// which shopspring computes approximately via ln and exp. Results are
// quantized to rateDecimals and never stored.
const irrPrecision int32 = 24

type Security struct {
	ID     ID
	Symbol string
	Name   string
}

// Lot is one acquisition of a security. A lot without a purchase price makes
// the whole holding's basis unknown.
type Lot struct {
	Shares          Rate
	CostPerShare    Rate
	HasCostPerShare bool
	AcquiredOn      Date
}

func (l Lot) Cost() (Money, bool) {
	if !l.HasCostPerShare {
		return Zero, false
	}
	return FromDecimal(l.Shares.Mul(l.CostPerShare)), true
}

// Quote is the latest price and the prior close. Without a prior close the day
// change is unknown, not zero.
type Quote struct {
	SecurityID    ID
	Price         Rate
	PriorClose    Rate
	HasPriorClose bool
	AsOf          Date
}

// Holding is a position in one security inside one account.
type Holding struct {
	ID         ID
	AccountID  ID
	SecurityID ID
	Shares     Rate
	Lots       []Lot
	// MarketValue is the provider's valuation, the only figure for a security
	// with no public quote (an employer plan's fund). A quote always wins.
	MarketValue    Money
	HasMarketValue bool
}

// HoldingValuation is one row of the Portfolio table. Absent figures render as
// a dash; no caller may substitute zero.
type HoldingValuation struct {
	Holding      Holding
	MarketValue  Money
	CostBasis    Money
	HasCostBasis bool
	DayChange    Money
	HasDayChange bool
	// IsUnquoted: valued from the provider's figure, so no price or day change.
	IsUnquoted bool
}

func (v HoldingValuation) TotalGain() (Money, bool) {
	if !v.HasCostBasis {
		return Zero, false
	}
	return v.MarketValue.Sub(v.CostBasis).Round(), true
}

func (v HoldingValuation) TotalGainPct() (Rate, bool) {
	if !v.HasCostBasis {
		return decimal.Zero, false
	}
	return Percent(v.MarketValue.Sub(v.CostBasis), v.CostBasis)
}

func (v HoldingValuation) DayChangePct() (Rate, bool) {
	return DayChangePct(v.DayChange, v.HasDayChange, v.MarketValue)
}

// PortfolioTotals is the Portfolio header. With some bases missing, cost basis
// and gain cover only holdings that have one: a partial basis subtracted from
// the full market value would report the missing positions as profit.
type PortfolioTotals struct {
	MarketValue  Money
	CostBasis    Money
	TotalGain    Money
	HasCostBasis bool
	DayChange    Money
	HasDayChange bool

	IsCostBasisIncomplete bool
	IsDayChangeIncomplete bool
}

func (t PortfolioTotals) DayChangePct() (Rate, bool) {
	return DayChangePct(t.DayChange, t.HasDayChange, t.MarketValue)
}

func MarketValue(holding Holding, quote Quote) Money {
	return FromDecimal(holding.Shares.Mul(quote.Price)).Round()
}

// CostBasis reports false when any part is unknown: no lots, a lot with no
// price, or lots covering fewer shares than the position holds.
func CostBasis(holding Holding) (Money, bool) {
	if len(holding.Lots) == 0 {
		return Zero, false
	}
	costs := make([]Money, 0, len(holding.Lots))
	shares := decimal.Zero
	for _, lot := range holding.Lots {
		cost, ok := lot.Cost()
		if !ok {
			return Zero, false
		}
		costs = append(costs, cost)
		shares = shares.Add(lot.Shares)
	}
	if !shares.Equal(holding.Shares) {
		return Zero, false
	}
	return Total(costs...), true
}

func DayChange(holding Holding, quote Quote) (Money, bool) {
	if !quote.HasPriorClose {
		return Zero, false
	}
	return FromDecimal(holding.Shares.Mul(quote.Price.Sub(quote.PriorClose))).Round(), true
}

// DayChangePct divides by yesterday's value (today's less the change); the
// post-move figure would understate gains and overstate losses.
func DayChangePct(change Money, hasChange bool, value Money) (Rate, bool) {
	if !hasChange {
		return decimal.Zero, false
	}
	return Percent(change, value.Sub(change))
}

func ValueHolding(holding Holding, quote Quote) HoldingValuation {
	basis, hasBasis := CostBasis(holding)
	change, hasChange := DayChange(holding, quote)
	return HoldingValuation{
		Holding:      holding,
		MarketValue:  MarketValue(holding, quote),
		CostBasis:    basis,
		HasCostBasis: hasBasis,
		DayChange:    change,
		HasDayChange: hasChange,
	}
}

// ValueUnquoted values a position at the provider's figure: no price or day
// change, but the lot-based cost basis still stands.
func ValueUnquoted(holding Holding) HoldingValuation {
	basis, hasBasis := CostBasis(holding)
	return HoldingValuation{
		Holding:      holding,
		MarketValue:  holding.MarketValue.Round(),
		CostBasis:    basis,
		HasCostBasis: hasBasis,
		IsUnquoted:   true,
	}
}

// ValueHoldings values every holding: by quote, else by the provider's market
// value (plan funds have no public price), else an error — skipping the row
// would quietly shrink the portfolio.
func ValueHoldings(holdings []Holding, quotes map[ID]Quote) ([]HoldingValuation, error) {
	valuations := make([]HoldingValuation, 0, len(holdings))
	for _, holding := range holdings {
		quote, ok := quotes[holding.SecurityID]
		switch {
		case ok:
			valuations = append(valuations, ValueHolding(holding, quote))
		case holding.HasMarketValue:
			valuations = append(valuations, ValueUnquoted(holding))
		default:
			return nil, fmt.Errorf(
				"investments: holding %s has neither a quote nor a market value for %s",
				holding.ID, holding.SecurityID)
		}
	}
	return valuations, nil
}

// CashOutsideHoldings is each account's balance less the market value of the
// positions filed under it: the account side a holdings total adds back
// (calculations.md §4, "Holdings double-counting"). A cash-only account keeps
// its whole balance; positions under an account not in balances add nothing.
func CashOutsideHoldings(balances map[ID]Money, valuations []HoldingValuation) map[ID]Money {
	cash := make(map[ID]Money, len(balances))
	for account, balance := range balances {
		cash[account] = balance
	}
	for _, valuation := range valuations {
		account := valuation.Holding.AccountID
		if balance, ok := cash[account]; ok {
			cash[account] = balance.Sub(valuation.MarketValue)
		}
	}
	return cash
}

func NewPortfolioTotals(valuations []HoldingValuation) PortfolioTotals {
	totals := PortfolioTotals{}
	basis, gains, change := Zero, Zero, Zero
	withBasis, withChange := 0, 0

	for _, row := range valuations {
		totals.MarketValue = totals.MarketValue.Add(row.MarketValue)
		if row.HasCostBasis {
			withBasis++
			basis = basis.Add(row.CostBasis)
			gains = gains.Add(row.MarketValue.Sub(row.CostBasis))
		}
		if row.HasDayChange {
			withChange++
			change = change.Add(row.DayChange)
		}
	}

	totals.MarketValue = totals.MarketValue.Round()
	totals.HasCostBasis = withBasis > 0
	totals.CostBasis = basis.Round()
	totals.TotalGain = gains.Round()
	totals.HasDayChange = withChange > 0
	totals.DayChange = change.Round()
	totals.IsCostBasisIncomplete = withBasis != len(valuations)
	totals.IsDayChangeIncomplete = withChange != len(valuations)
	if !totals.HasCostBasis {
		totals.CostBasis, totals.TotalGain = Zero, Zero
	}
	if !totals.HasDayChange {
		totals.DayChange = Zero
	}
	return totals
}

// ValuePoint is the portfolio's value on one day, after that day's flows.
type ValuePoint struct {
	On    Date
	Value Money
}

// CashFlow is money crossing the account boundary, positive in. A reinvested
// dividend is not one: counting it would make earnings look like deposits.
type CashFlow struct {
	On     Date
	Amount Money
}

// TimeWeightedReturn chain-links sub-period returns between external flows,
// each stripped of its flow. False with fewer than two points. A sub-period
// starting at zero contributes a factor of 1: no capital was at risk.
func TimeWeightedReturn(points []ValuePoint, flows []CashFlow) (Rate, bool) {
	ordered := slices.Clone(points)
	slices.SortFunc(ordered, func(a, b ValuePoint) int { return dateOrder(a.On, b.On) })
	if len(ordered) < 2 {
		return decimal.Zero, false
	}

	growth := decimal.NewFromInt(1)
	for i := 0; i+1 < len(ordered); i++ {
		start, end := ordered[i], ordered[i+1]
		flow := Zero
		for _, move := range flows {
			if move.On.After(start.On) && move.On.NotAfter(end.On) {
				flow = flow.Add(move.Amount)
			}
		}
		if start.Value.IsZero() {
			continue
		}
		period, ok := Ratio(end.Value.Sub(flow), start.Value)
		if !ok {
			continue
		}
		growth = growth.Mul(period)
	}
	return growth.Sub(decimal.NewFromInt(1)).Round(rateDecimals), true
}

// InternalRateOfReturn is the annualized money-weighted return; false when
// there is no root. Ending is separate so flows keep one sign convention
// (money into the portfolio). Newton, then bisection: no answer beats a wrong
// root.
func InternalRateOfReturn(flows []CashFlow, ending ValuePoint, starting *ValuePoint) (Rate, bool) {
	series, ok := irrSeries(flows, ending, starting)
	if !ok {
		return decimal.Zero, false
	}

	scale := decimal.Zero
	for _, term := range series {
		scale = scale.Add(term.amount.Abs())
	}
	tolerance := scale.Mul(decimal.RequireFromString("1e-9"))
	if floor := decimal.RequireFromString("1e-9"); tolerance.LessThan(floor) {
		tolerance = floor
	}

	rate, found := irrNewton(series, decimal.RequireFromString("0.1"), tolerance, 64)
	if !found {
		rate, found = irrBisect(series, tolerance, 200)
	}
	if !found {
		return decimal.Zero, false
	}
	return rate.Round(rateDecimals), true
}

// irrTerm is one investor-side amount, dated in years from the window start.
type irrTerm struct {
	years  decimal.Decimal
	amount decimal.Decimal
}

// irrSeries turns flows into investor-side amounts: contributions negative,
// the ending value positive. False when they never change sign — no rate
// exists, and that is not an error.
func irrSeries(flows []CashFlow, ending ValuePoint, starting *ValuePoint) ([]irrTerm, bool) {
	type dated struct {
		on     Date
		amount decimal.Decimal
	}
	rows := make([]dated, 0, len(flows)+2)
	for _, flow := range flows {
		rows = append(rows, dated{flow.On, flow.Amount.Decimal().Neg()})
	}
	if starting != nil && !starting.Value.IsZero() {
		rows = append(rows, dated{starting.On, starting.Value.Decimal().Neg()})
	}
	rows = append(rows, dated{ending.On, ending.Value.Decimal()})

	positive, negative := false, false
	for _, row := range rows {
		positive = positive || row.amount.IsPositive()
		negative = negative || row.amount.IsNegative()
	}
	if !positive || !negative {
		return nil, false
	}

	origin := rows[0].on
	if starting != nil {
		origin = starting.On
	} else {
		for _, row := range rows {
			if row.on.Before(origin) {
				origin = row.on
			}
		}
	}

	series := make([]irrTerm, 0, len(rows))
	for _, row := range rows {
		years := decimal.NewFromInt(int64(DaysBetween(origin, row.on))).Div(irrDaysInYear)
		series = append(series, irrTerm{years: years, amount: row.amount})
	}
	return series, true
}

// irrCompound is (1 + rate) ** years. A non-positive base is a caller bug.
func irrCompound(rate, years decimal.Decimal) (decimal.Decimal, error) {
	base := decimal.NewFromInt(1).Add(rate)
	if !base.IsPositive() {
		return decimal.Zero, fmt.Errorf("investments: rate %s is outside the solver's domain", rate)
	}
	if years.IsZero() {
		return decimal.NewFromInt(1), nil
	}
	return base.PowWithPrecision(years, irrPrecision)
}

func irrNPV(rate decimal.Decimal, series []irrTerm) (decimal.Decimal, error) {
	sum := decimal.Zero
	for _, term := range series {
		discount, err := irrCompound(rate, term.years)
		if err != nil {
			return decimal.Zero, err
		}
		if discount.IsZero() {
			return decimal.Zero, fmt.Errorf("investments: rate %s discounts to zero", rate)
		}
		sum = sum.Add(term.amount.Div(discount))
	}
	return sum, nil
}

func irrNPVSlope(rate decimal.Decimal, series []irrTerm) (decimal.Decimal, error) {
	sum := decimal.Zero
	for _, term := range series {
		discount, err := irrCompound(rate, term.years)
		if err != nil {
			return decimal.Zero, err
		}
		divisor := discount.Mul(decimal.NewFromInt(1).Add(rate))
		if divisor.IsZero() {
			return decimal.Zero, fmt.Errorf("investments: rate %s has no slope", rate)
		}
		sum = sum.Sub(term.years.Mul(term.amount).Div(divisor))
	}
	return sum, nil
}

// irrNewton gives up (to bisection) on leaving the domain, a flat slope or the
// iteration budget, rather than "converging" wherever it stands.
func irrNewton(series []irrTerm, guess, tolerance decimal.Decimal, iterations int) (decimal.Decimal, bool) {
	rate := guess
	for range iterations {
		value, err := irrNPV(rate, series)
		if err != nil {
			return decimal.Zero, false
		}
		if value.Abs().LessThanOrEqual(tolerance) {
			return rate, true
		}
		slope, err := irrNPVSlope(rate, series)
		if err != nil || slope.IsZero() {
			return decimal.Zero, false
		}
		rate = rate.Sub(value.Div(slope))
		if rate.LessThanOrEqual(irrMinRate) {
			return decimal.Zero, false
		}
	}
	return decimal.Zero, false
}

// irrBisect bisects over the first bracket whose ends disagree in sign.
func irrBisect(series []irrTerm, tolerance decimal.Decimal, iterations int) (decimal.Decimal, bool) {
	low, high, ok := irrBracket(series)
	if !ok {
		return decimal.Zero, false
	}
	lowValue, err := irrNPV(low, series)
	if err != nil {
		return decimal.Zero, false
	}
	width := decimal.New(1, -rateDecimals)
	two := decimal.NewFromInt(2)
	for range iterations {
		middle := low.Add(high).Div(two)
		value, err := irrNPV(middle, series)
		if err != nil {
			return decimal.Zero, false
		}
		if value.Abs().LessThanOrEqual(tolerance) || high.Sub(low).LessThan(width) {
			return middle, true
		}
		if value.IsNegative() == lowValue.IsNegative() {
			low, lowValue = middle, value
		} else {
			high = middle
		}
	}
	return decimal.Zero, false
}

func irrBracket(series []irrTerm) (low, high decimal.Decimal, ok bool) {
	previousRate := decimal.RequireFromString(irrBrackets[0])
	previousValue, err := irrNPV(previousRate, series)
	if err != nil {
		return decimal.Zero, decimal.Zero, false
	}
	for _, candidate := range irrBrackets[1:] {
		rate := decimal.RequireFromString(candidate)
		value, err := irrNPV(rate, series)
		if err != nil {
			return decimal.Zero, decimal.Zero, false
		}
		if value.IsNegative() != previousValue.IsNegative() {
			return previousRate, rate, true
		}
		previousRate, previousValue = rate, value
	}
	return decimal.Zero, decimal.Zero, false
}

// RatePct renders a rate for the chart's axis, which is in percent. It passes
// an absent rate straight through, so a chart never plots a zero it invented.
func RatePct(rate Rate, ok bool) (Rate, bool) {
	if !ok {
		return decimal.Zero, false
	}
	return rate.Mul(decimal.NewFromInt(100)).Round(2), true
}

// Allocation is each security's share of market value, summed across accounts.
func Allocation(valuations []HoldingValuation) (map[ID]Rate, bool) {
	return AllocationBy(valuations, func(v HoldingValuation) ID { return v.Holding.SecurityID })
}

// AllocationBy is each group's share of market value. Rows are summed per group,
// never last-wins: one security in two accounts is one security slice and two
// account slices. False for a portfolio worth nothing.
func AllocationBy[K comparable](
	valuations []HoldingValuation, key func(HoldingValuation) K,
) (map[K]Rate, bool) {
	portfolio := Zero
	byGroup := make(map[K]Money, len(valuations))
	for _, row := range valuations {
		portfolio = portfolio.Add(row.MarketValue)
		group := key(row)
		byGroup[group] = byGroup[group].Add(row.MarketValue)
	}
	portfolio = portfolio.Round()
	if portfolio.IsZero() {
		return nil, false
	}
	out := make(map[K]Rate, len(byGroup))
	for group, value := range byGroup {
		share, _ := Ratio(value.Round(), portfolio)
		out[group] = share
	}
	return out, true
}

// PricePoint is one security's close. A Rate, not Money: share prices are not
// rounded to the cent.
type PricePoint struct {
	On    Date
	Close Rate
}

// PriceChange is the last close less the first, and that as a fraction of the
// first. False for fewer than two points; no fraction when the series opens at
// zero. Points are sorted here because multi-source series arrive unordered.
func PriceChange(points []PricePoint) (change Rate, pct Rate, hasPct bool, ok bool) {
	ordered := slices.Clone(points)
	slices.SortFunc(ordered, func(a, b PricePoint) int { return dateOrder(a.On, b.On) })
	if len(ordered) < 2 {
		return decimal.Zero, decimal.Zero, false, false
	}
	first, last := ordered[0].Close, ordered[len(ordered)-1].Close
	change = last.Sub(first)
	if first.IsZero() {
		return change, decimal.Zero, false, true
	}
	return change, change.Div(first).Round(rateDecimals), true, true
}
