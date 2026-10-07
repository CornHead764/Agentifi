package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func forecastFlows() []CashFlow {
	return []CashFlow{
		{On: NewDate(2026, time.August, 1), Amount: MustFromString("-1500.00")},
		{On: NewDate(2026, time.August, 1), Amount: MustFromString("-42.00")},
		{On: NewDate(2026, time.August, 14), Amount: MustFromString("2500.00")},
		{On: NewDate(2026, time.October, 2), Amount: MustFromString("-80.00")},
	}
}

func TestTheDailySeriesSplitsMoneyInFromMoneyOut(t *testing.T) {
	days := SummarizeCashFlowDays(forecastFlows())
	require.Len(t, days, 3)
	require.Equal(t, NewDate(2026, time.August, 1), days[0].On)
	require.Equal(t, "0.00", days[0].In.String())
	require.Equal(t, "1542.00", days[0].Out.String())
	require.Equal(t, "-1542.00", days[0].Net().String())
	require.Equal(t, "2500.00", days[1].In.String())
}

func TestTheDailySeriesIsOrderedOldestFirst(t *testing.T) {
	flows := []CashFlow{
		{On: NewDate(2026, time.August, 14), Amount: MustFromString("2500.00")},
		{On: NewDate(2026, time.August, 1), Amount: MustFromString("-42.00")},
		{On: NewDate(2026, time.July, 30), Amount: MustFromString("-10.00")},
	}
	days := SummarizeCashFlowDays(flows)
	require.Equal(t, NewDate(2026, time.July, 30), days[0].On)
	require.Equal(t, NewDate(2026, time.August, 1), days[1].On)
	require.Equal(t, NewDate(2026, time.August, 14), days[2].On)
}

func TestTheMonthlySeriesKeepsAMonthNothingHappenedIn(t *testing.T) {
	months := SummarizeCashFlowMonths(forecastFlows())
	require.Len(t, months, 3)
	require.Equal(t, "2026-08", months[0].Month.String())
	require.Equal(t, "2500.00", months[0].In.String())
	require.Equal(t, "1542.00", months[0].Out.String())
	require.Equal(t, "2026-09", months[1].Month.String())
	require.Equal(t, "0.00", months[1].In.String())
	require.Equal(t, "0.00", months[1].Out.String())
	require.Equal(t, "2026-10", months[2].Month.String())
	require.Equal(t, "-80.00", months[2].Net().String())
}

const forecastAnswer = `{
  "months": [
    {"month": "2026-10", "money_in": "8450.00", "money_out": "7310.00"},
    {"month": "2026-11", "money_in": 8450, "money_out": "9100.50"}
  ],
  "narrative": "Spending runs about 7,300 a month, with a November bump."
}`

func TestAForecastParsesQuotedAndBareAmounts(t *testing.T) {
	forecast, err := ParseCashFlowForecast(forecastAnswer)
	require.NoError(t, err)
	require.Len(t, forecast.Months, 2)
	require.Equal(t, "2026-10", forecast.Months[0].Month.String())
	require.Equal(t, "8450.00", forecast.Months[0].In.String())
	require.Equal(t, "1140.00", forecast.Months[0].Net().String())
	require.Equal(t, "8450.00", forecast.Months[1].In.String())
	require.Equal(t, "489.50", forecast.Net().String())
	require.Equal(t, NewDate(2026, time.November, 30), forecast.Horizon())
	require.Contains(t, forecast.Narrative, "7,300 a month")
}

func TestAForecastParsesInsideAFencedBlock(t *testing.T) {
	wrapped := "Sure — here is the forecast:\n```json\n" + forecastAnswer + "\n```\n"
	forecast, err := ParseCashFlowForecast(wrapped)
	require.NoError(t, err)
	require.Len(t, forecast.Months, 2)
}

func TestAForecastWithNoJSONAtAllIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast("I cannot forecast that.")
	require.ErrorContains(t, err, "no object was found")
}

func TestAForecastWithNoNarrativeIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(
		`{"months": [{"month": "2026-10", "money_in": "1", "money_out": "2"}]}`)
	require.ErrorContains(t, err, "no narrative")
}

func TestAForecastMissingAFigureIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(
		`{"months": [{"month": "2026-10", "money_in": "8450.00"}], "narrative": "x"}`)
	require.ErrorContains(t, err, "has no money_out")
}

func TestAForecastWithAMonthMissingFromTheMiddleIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "1", "money_out": "2"},
	    {"month": "2026-12", "money_in": "1", "money_out": "2"}], "narrative": "x"}`)
	require.ErrorContains(t, err, "jumps from 2026-10 to 2026-12")
}

func TestAForecastWhoseNetDisagreesWithItsOwnHalvesIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "8450.00", "money_out": "7310.00",
	     "net": "2140.00"}], "narrative": "x"}`)
	require.ErrorContains(t, err, "says net 2140.00")
}

func TestAForecastWhoseNetAgreesIsAccepted(t *testing.T) {
	forecast, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "8450.00", "money_out": "7310.00",
	     "net": "1140.00"}], "narrative": "x"}`)
	require.NoError(t, err)
	require.Equal(t, "1140.00", forecast.Net().String())
}

func TestAForecastWithANegativeFigureIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "8450.00", "money_out": "-7310.00"}],
	    "narrative": "x"}`)
	require.ErrorContains(t, err, "negative figure")
}

func TestAForecastMonthThatIsNotAMonthIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [
	    {"month": "next month", "money_in": "1", "money_out": "2"}], "narrative": "x"}`)
	require.ErrorContains(t, err, "is not a month")
}

func TestAForecastMoneyFigureThatIsNotAnAmountIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "about eight thousand", "money_out": "2"}],
	    "narrative": "x"}`)
	require.ErrorContains(t, err, "which is not an amount")
}

func TestAForecastAmountKeepsEveryCentOfWhatTheModelWrote(t *testing.T) {
	// There is no FromFloat in this package, and a bare 6120.40 must not
	// arrive as the nearest float64 to it.
	forecast, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": 6120.40, "money_out": "$1,000.05"}],
	    "narrative": "x"}`)
	require.NoError(t, err)
	require.Equal(t, "6120.40", forecast.Months[0].In.String())
	require.Equal(t, "1000.05", forecast.Months[0].Out.String())
}

func TestAForecastWithNoMonthsIsRefused(t *testing.T) {
	_, err := ParseCashFlowForecast(`{"months": [], "narrative": "x"}`)
	require.ErrorContains(t, err, "lists no months")
}

func TestAForecastStartingSomewhereElseIsRefused(t *testing.T) {
	forecast, err := ParseCashFlowForecast(forecastAnswer)
	require.NoError(t, err)
	require.NoError(t, CashFlowForecastCoversFrom(forecast, NewDate(2026, time.October, 13)))
	require.NoError(t, CashFlowForecastCoversFrom(forecast, NewDate(2026, time.September, 28)))
	require.ErrorContains(t, CashFlowForecastCoversFrom(forecast, NewDate(2026, time.July, 1)),
		"starts at 2026-10")
}

func forecastHistory() []CashFlowMonth {
	return []CashFlowMonth{
		{Month: NewMonth(2026, time.August), In: MustFromString("8400.00"),
			Out: MustFromString("7900.00")},
		{Month: NewMonth(2026, time.September), In: MustFromString("8500.00"),
			Out: MustFromString("8100.00")},
	}
}

func TestAForecastTenTimesTheHouseholdsRhythmIsRefused(t *testing.T) {
	// A slipped decimal place passes every structural check and would be drawn
	// on the chart.
	forecast, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "84500.00", "money_out": "79000.00"}],
	    "narrative": "x"}`)
	require.NoError(t, err)
	require.ErrorContains(t, CheckCashFlowForecast(forecast, forecastHistory()),
		"not this household's rhythm")
}

func TestAForecastOfAllZeroesIsRefused(t *testing.T) {
	forecast, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-10", "money_in": "0", "money_out": "0"}], "narrative": "x"}`)
	require.NoError(t, err)
	require.ErrorContains(t, CheckCashFlowForecast(forecast, forecastHistory()),
		"zero in every month")
}

func TestAForecastInTheHouseholdsOwnRangePasses(t *testing.T) {
	forecast, err := ParseCashFlowForecast(forecastAnswer)
	require.NoError(t, err)
	require.NoError(t, CheckCashFlowForecast(forecast, forecastHistory()))
}

func bucketForecast(t *testing.T) CashFlowForecast {
	t.Helper()
	forecast, err := ParseCashFlowForecast(`{"months": [
	    {"month": "2026-09", "money_in": "3000.00", "money_out": "6000.00"},
	    {"month": "2026-10", "money_in": "3100.00", "money_out": "3100.00"},
	    {"month": "2026-11", "money_in": "3000.00", "money_out": "3000.00"}],
	    "narrative": "x"}`)
	require.NoError(t, err)
	return forecast
}

func TestAWindowProratesTheMonthsItOpensAndClosesInside(t *testing.T) {
	// 2026-09-15 through 2026-10-15 inclusive: 16 days of September's 30 and
	// 15 of October's 31. September spends 200 a day, October 100.
	windows := BucketCashFlowForecast(bucketForecast(t), NewDate(2026, time.September, 15), []int{30})
	require.Len(t, windows, 1)
	require.Equal(t, 30, windows[0].Days)
	require.Equal(t, NewDate(2026, time.October, 15), windows[0].Through)
	require.Equal(t, "4700.00", windows[0].Out.String())
	require.Equal(t, "3100.00", windows[0].In.String())
	require.Equal(t, "-1600.00", windows[0].Net().String())
	require.Equal(t, 31, windows[0].Covered)
	require.True(t, windows[0].IsComplete())
}

func TestEveryWindowTheCardOffersIsBucketed(t *testing.T) {
	windows := BucketCashFlowForecast(bucketForecast(t), NewDate(2026, time.September, 15),
		[]int{30, 60, 90, 180})
	require.Len(t, windows, 4)
	require.Equal(t, []int{30, 60, 90, 180}, []int{windows[0].Days, windows[1].Days,
		windows[2].Days, windows[3].Days})
	// Each window covers at least as much as the one inside it.
	for index := 1; index < len(windows); index++ {
		require.GreaterOrEqual(t, windows[index].Out.Cmp(windows[index-1].Out), 0)
	}
}

func TestAWindowPastTheLastForecastMonthSaysSoRatherThanGuessing(t *testing.T) {
	windows := BucketCashFlowForecast(bucketForecast(t), NewDate(2026, time.September, 15), []int{180})
	require.Equal(t, 77, windows[0].Covered)
	require.False(t, windows[0].IsComplete())
}
