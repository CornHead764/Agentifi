package domain

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// The estimated cash-flow forecast. The projection in cashflow.go is only
// arithmetic over known bills; here a model is shown the last twelve months as
// a numeric series and its answer for the next six is parsed.
//
//   - The series carries dates and amounts and nothing else: no payee,
//     account, category or note is sent to the model.
//   - An answer that does not fit is refused, never repaired, and the caller
//     falls back to the arithmetic.
//   - No float, at any point: numbers decode as json.Number into FromString.

// CashFlowDay is one day of the series a forecast run is shown. In and Out
// are non-negative magnitudes; a day nothing happened on is absent.
type CashFlowDay struct {
	On  Date
	In  Money
	Out Money
}

func (d CashFlowDay) Net() Money { return d.In.Sub(d.Out) }

// CashFlowMonth is also the shape the model answers in.
type CashFlowMonth struct {
	Month Month
	In    Money
	Out   Money
}

func (m CashFlowMonth) Net() Money { return m.In.Sub(m.Out) }

// SummarizeCashFlowDays folds signed amounts into one row per day, oldest
// first, split into two magnitudes: a single net hides a month that earned and
// spent twice as much as usual.
func SummarizeCashFlowDays(flows []CashFlow) []CashFlowDay {
	totals := map[Date]CashFlowDay{}
	order := make([]Date, 0, len(flows))
	for _, flow := range flows {
		day, seen := totals[flow.On]
		if !seen {
			day.On = flow.On
			order = append(order, flow.On)
		}
		if flow.Amount.IsNegative() {
			day.Out = day.Out.Add(flow.Amount.Abs())
		} else {
			day.In = day.In.Add(flow.Amount)
		}
		totals[flow.On] = day
	}
	sortDates(order)
	out := make([]CashFlowDay, 0, len(order))
	for _, on := range order {
		day := totals[on]
		out = append(out, CashFlowDay{On: on, In: day.In.Round(), Out: day.Out.Round()})
	}
	return out
}

// SummarizeCashFlowMonths rolls the same flows up per month, oldest first.
// Empty months are present: a gap reads to a model as a month it was not told
// about.
func SummarizeCashFlowMonths(flows []CashFlow) []CashFlowMonth {
	if len(flows) == 0 {
		return nil
	}
	totals := map[Month]CashFlowMonth{}
	first, last := MonthOf(flows[0].On), MonthOf(flows[0].On)
	for _, flow := range flows {
		month := MonthOf(flow.On)
		if month.Before(first) {
			first = month
		}
		if month.After(last) {
			last = month
		}
		row := totals[month]
		row.Month = month
		if flow.Amount.IsNegative() {
			row.Out = row.Out.Add(flow.Amount.Abs())
		} else {
			row.In = row.In.Add(flow.Amount)
		}
		totals[month] = row
	}
	out := make([]CashFlowMonth, 0, MonthCount(first, last)+1)
	for _, month := range MonthsBetween(first, last) {
		row := totals[month]
		out = append(out, CashFlowMonth{Month: month, In: row.In.Round(), Out: row.Out.Round()})
	}
	return out
}

// sortDates is an insertion sort because the input is nearly sorted already.
func sortDates(dates []Date) {
	for i := 1; i < len(dates); i++ {
		for j := i; j > 0 && dates[j].Before(dates[j-1]); j-- {
			dates[j], dates[j-1] = dates[j-1], dates[j]
		}
	}
}

// CashFlowForecast is a model's answer, parsed and checked.
type CashFlowForecast struct {
	// Months are oldest first and contiguous.
	Months    []CashFlowMonth
	Narrative string
}

func (f CashFlowForecast) Horizon() Date {
	if len(f.Months) == 0 {
		return Date{}
	}
	return f.Months[len(f.Months)-1].Month.LastDay()
}

func (f CashFlowForecast) Net() Money {
	total := Zero
	for _, month := range f.Months {
		total = total.Add(month.Net())
	}
	return total.Round()
}

// maxForecastMonths is generous next to the six asked for, so a long answer is
// refused for being wrong rather than for being long.
const maxForecastMonths = 18

// ParseCashFlowForecast reads a model's answer. Models wrap JSON in fences or
// prose, so the outermost braces are taken; that is the only forgiveness:
//
//	{"months": [{"month": "2026-10", "money_in": "8450.00",
//	             "money_out": "7310.00"}],
//	 "narrative": "..."}
//
// money_in and money_out are non-negative magnitudes. An optional "net" is
// checked, not trusted: if it disagrees, which figure is wrong is unknowable.
func ParseCashFlowForecast(raw string) (CashFlowForecast, error) {
	body, err := jsonObjectIn(raw)
	if err != nil {
		return CashFlowForecast{}, err
	}
	var answer struct {
		Months []struct {
			Month    string `json:"month"`
			MoneyIn  any    `json:"money_in"`
			MoneyOut any    `json:"money_out"`
			Net      any    `json:"net"`
		} `json:"months"`
		Narrative string `json:"narrative"`
	}
	decoder := json.NewDecoder(strings.NewReader(body))
	// An unquoted figure must never pass through a float64.
	decoder.UseNumber()
	if err := decoder.Decode(&answer); err != nil {
		return CashFlowForecast{}, fmt.Errorf("the forecast is not readable JSON: %w", err)
	}
	if len(answer.Months) == 0 {
		return CashFlowForecast{}, fmt.Errorf("the forecast lists no months")
	}
	if len(answer.Months) > maxForecastMonths {
		return CashFlowForecast{}, fmt.Errorf("the forecast lists %d months, more than the %d "+
			"a forecast may cover", len(answer.Months), maxForecastMonths)
	}

	out := CashFlowForecast{
		Months:    make([]CashFlowMonth, 0, len(answer.Months)),
		Narrative: strings.TrimSpace(answer.Narrative),
	}
	for index, row := range answer.Months {
		month, err := ParseForecastMonth(row.Month)
		if err != nil {
			return CashFlowForecast{}, err
		}
		if index > 0 {
			previous := out.Months[index-1].Month
			if month != previous.Next() {
				return CashFlowForecast{}, fmt.Errorf(
					"the forecast jumps from %s to %s; the months have to run one after "+
						"another", previous, month)
			}
		}
		moneyIn, err := parseForecastMoney(row.MoneyIn, "money_in", month)
		if err != nil {
			return CashFlowForecast{}, err
		}
		moneyOut, err := parseForecastMoney(row.MoneyOut, "money_out", month)
		if err != nil {
			return CashFlowForecast{}, err
		}
		if moneyIn.IsNegative() || moneyOut.IsNegative() {
			return CashFlowForecast{}, fmt.Errorf("%s has a negative figure; money_in and "+
				"money_out are both amounts, and which way they run is which field they are in",
				month)
		}
		one := CashFlowMonth{Month: month, In: moneyIn.Round(), Out: moneyOut.Round()}
		if row.Net != nil {
			claimed, err := parseForecastMoney(row.Net, "net", month)
			if err != nil {
				return CashFlowForecast{}, err
			}
			if claimed.Round().Sub(one.Net()).Abs().GreaterThan(FromCents(1)) {
				return CashFlowForecast{}, fmt.Errorf(
					"%s says net %s, but money_in minus money_out is %s", month,
					claimed.Round(), one.Net())
			}
		}
		out.Months = append(out.Months, one)
	}
	if out.Narrative == "" {
		return CashFlowForecast{}, fmt.Errorf("the forecast has no narrative: a six-month " +
			"figure nobody can interrogate is worse than no figure")
	}
	return out, nil
}

// ParseForecastMonth reads a "YYYY-MM" month.
func ParseForecastMonth(text string) (Month, error) {
	month, ok := ParseMonth(strings.TrimSpace(text))
	if !ok {
		return Month{}, fmt.Errorf("%q is not a month: a month is written 2026-10", text)
	}
	return month, nil
}

// parseForecastMoney accepts a quoted amount or a bare number
// (MoneyFromJSONValue). Anything else is refused, floats included.
func parseForecastMoney(raw any, field string, month Month) (Money, error) {
	amount, err := MoneyFromJSONValue(raw)
	switch {
	case errors.Is(err, ErrNoAmount):
		return Zero, fmt.Errorf("%s has no %s", month, field)
	case err != nil:
		return Zero, fmt.Errorf("%s has a %s of %v, which is not an amount", month, field, raw)
	}
	return amount, nil
}

func jsonObjectIn(raw string) (string, error) {
	opening := strings.Index(raw, "{")
	closing := strings.LastIndex(raw, "}")
	if opening < 0 || closing < opening {
		return "", fmt.Errorf("the forecast is not JSON: no object was found in the answer")
	}
	return raw[opening : closing+1], nil
}

// CheckCashFlowForecast weighs the answer against the series it was given. It
// catches what small models do: a slipped decimal place, which passes every
// structural check, and an answer of all zeroes. The bound is deliberately
// loose, not statistical.
func CheckCashFlowForecast(forecast CashFlowForecast, history []CashFlowMonth) error {
	total := Zero
	for _, month := range forecast.Months {
		total = total.Add(month.In).Add(month.Out)
	}
	if total.IsZero() {
		return fmt.Errorf("the forecast is zero in every month, which is not a forecast")
	}
	if len(history) == 0 {
		return nil
	}
	widest := Zero
	for _, month := range history {
		if month.In.GreaterThan(widest) {
			widest = month.In
		}
		if month.Out.GreaterThan(widest) {
			widest = month.Out
		}
	}
	ceiling := widest.MulInt(forecastPlausibleMultiple)
	for _, month := range forecast.Months {
		if month.In.GreaterThan(ceiling) || month.Out.GreaterThan(ceiling) {
			return fmt.Errorf("%s forecasts %s in and %s out, against a busiest month of %s in "+
				"the last year: that is not this household's rhythm", month.Month, month.In,
				month.Out, widest)
		}
	}
	return nil
}

const forecastPlausibleMultiple = 3

// CashFlowForecastCoversFrom refuses a forecast that starts somewhere other
// than the month of from or the one after it.
func CashFlowForecastCoversFrom(forecast CashFlowForecast, from Date) error {
	if len(forecast.Months) == 0 {
		return fmt.Errorf("the forecast lists no months")
	}
	first, this := forecast.Months[0].Month, MonthOf(from)
	if first != this && first != this.Next() {
		return fmt.Errorf("the forecast starts at %s; it was asked for the six months from %s",
			first, this)
	}
	return nil
}

// CashFlowForecastWindow is the estimate over one of the windows the projection
// card already offers — 30, 60, 90 or 180 days.
type CashFlowForecastWindow struct {
	Days    int
	Through Date
	In      Money
	Out     Money
	// Covered is how many days of the window the forecast reaches, so an
	// incomplete estimate is not shown as a whole one.
	Covered int
}

func (w CashFlowForecastWindow) Net() Money { return w.In.Sub(w.Out).Round() }

func (w CashFlowForecastWindow) IsComplete() bool { return w.Covered >= w.Days+1 }

// BucketCashFlowForecast breaks monthly figures into the day windows the UI
// offers, prorating the months the window opens and closes inside.
//
// A window runs from `from` through `from` plus days, both ends included, as
// the projection card asks for. A month is spread evenly across its days: the
// model's figure carries no shape inside the month.
func BucketCashFlowForecast(
	forecast CashFlowForecast, from Date, horizons []int,
) []CashFlowForecastWindow {
	out := make([]CashFlowForecastWindow, 0, len(horizons))
	for _, days := range horizons {
		if days < 0 {
			continue
		}
		through := from.AddDays(days)
		window := CashFlowForecastWindow{Days: days, Through: through}
		for _, month := range forecast.Months {
			overlap := monthDaysWithin(month.Month, from, through)
			if overlap == 0 {
				continue
			}
			window.Covered += overlap
			window.In = window.In.Add(prorateMonth(month.In, overlap, month.Month.Days()))
			window.Out = window.Out.Add(prorateMonth(month.Out, overlap, month.Month.Days()))
		}
		window.In, window.Out = window.In.Round(), window.Out.Round()
		out = append(out, window)
	}
	return out
}

// prorateMonth is left unrounded; the caller rounds the window's sum once.
func prorateMonth(amount Money, days, monthDays int) Money {
	perDay, ok := amount.DivInt(monthDays)
	if !ok {
		return Zero
	}
	return perDay.MulInt(days)
}

func monthDaysWithin(month Month, from, through Date) int {
	start, end := month.FirstDay(), month.LastDay()
	if start.Before(from) {
		start = from
	}
	if end.After(through) {
		end = through
	}
	if end.Before(start) {
		return 0
	}
	return DaysBetween(start, end) + 1
}

// CashFlowForecastCheck is the estimate set beside the arithmetic for the same
// window. The estimate never replaces the projection: the UI draws the
// arithmetic and shows the estimate beside it.
type CashFlowForecastCheck struct {
	// Estimated is the opening balance plus the estimate's movement.
	Estimated Money
	// Scheduled is the deterministic projection's balance on the same day.
	Scheduled Money
	// Difference is Estimated minus Scheduled: negative when the estimate is
	// the gloomier.
	Difference Money
}

func ReconcileCashFlowForecast(opening, scheduled Money, window CashFlowForecastWindow) CashFlowForecastCheck {
	estimated := opening.Add(window.Net()).Round()
	return CashFlowForecastCheck{
		Estimated:  estimated,
		Scheduled:  scheduled.Round(),
		Difference: estimated.Sub(scheduled.Round()).Round(),
	}
}
