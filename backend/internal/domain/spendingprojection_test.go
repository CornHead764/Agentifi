package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestProjectSpendingAddsWhatIsStillScheduled(t *testing.T) {
	money := MustFromString
	october := func(day int) Date { return NewDate(2026, time.October, day) }
	window := PeriodWindow(PeriodOf(october(3), GrainMonth), october(3))
	require.True(t, window.Partial)

	scope := ProjectionScope{
		Accounts: map[ID]Account{
			"checking": {ID: "checking"},
			"hidden":   {ID: "hidden", ExcludedFromReports: true},
		},
		Series: map[ID]Series{
			"pay":       {ID: "pay", CategoryID: "salary"},
			"rent":      {ID: "rent", CategoryID: "housing"},
			"private":   {ID: "private", CategoryID: "private"},
			"abroad":    {ID: "abroad", Currency: "EUR"},
			"streaming": {ID: "streaming", Currency: "USD"},
		},
		Categories: map[ID]Category{
			"salary":  {ID: "salary", Kind: CategoryIncome},
			"housing": {ID: "housing", Kind: CategoryExpense},
			"private": {ID: "private", Kind: CategoryExpense, ExcludedFromReports: true},
		},
		Currency: "USD",
	}
	on := func(series string, kind SeriesKind, account string, due Date, amount string) Occurrence {
		return Occurrence{
			SeriesID: ID(series), AccountID: ID(account), Kind: kind,
			DueOn: due, ScheduledOn: due, Amount: money(amount),
		}
	}
	gym := on("gym", SeriesBill, "checking", NewDate(2026, time.November, 2), "-40.00")
	gym.PaysOn = october(31)
	insurance := on("insurance", SeriesBill, "checking", october(29), "-90.00")
	insurance.PaysOn = NewDate(2026, time.November, 1)
	expected := []Occurrence{
		on("pay", SeriesIncome, "checking", october(30), "3100.00"),
		on("rent", SeriesBill, "checking", october(5), "-1400.00"),
		// Due today and not yet settled: still to come.
		on("streaming", SeriesSubscription, "checking", october(3), "-15.00"),
		// Past due is not the rest of the period.
		on("phone", SeriesBill, "checking", october(1), "-60.00"),
		on("card", SeriesCreditCardPayment, "checking", october(20), "-500.00"),
		// Due in November, paid on 31 October: this month's.
		gym,
		// Due in October, paid on 1 November: next month's.
		insurance,
		on("hidden-bill", SeriesBill, "hidden", october(10), "-200.00"),
		on("elsewhere", SeriesBill, "savings", october(12), "-300.00"),
		on("private", SeriesBill, "checking", october(14), "-80.00"),
		on("abroad", SeriesBill, "checking", october(15), "-25.00"),
		// An expected refund nets against spending.
		on("refund", SeriesRefund, "checking", october(18), "30.00"),
		on("pay", SeriesIncome, "checking", NewDate(2026, time.November, 13), "3100.00"),
	}

	actual := SummarizeSpending(money("1200.00"), money("-950.00"))
	require.Equal(t, RatingGood, actual.Rating)

	projection, ok := ProjectSpending(actual, window, expected, scope)
	require.True(t, ok)
	require.Equal(t, october(31), projection.End)
	require.Equal(t, 5, projection.Count)
	require.Equal(t, "3100.00", projection.ExpectedIncome.String())
	// −1,400.00 − 15.00 − 40.00 + 30.00
	require.Equal(t, "-1425.00", projection.ExpectedSpent.String())
	require.Equal(t, "4300.00", projection.Summary.Income.String())
	require.Equal(t, "-2375.00", projection.Summary.Spent.String())
	require.Equal(t, "1925.00", projection.Summary.Remaining.String())
	// 1,925 / 4,300 and 2,375 / 4,300
	require.Equal(t, "0.4477", projection.Summary.SavingsRate.Round(4).String())
	require.Equal(t, "0.5523", projection.Summary.SpendingRate.Round(4).String())
	require.Equal(t, RatingGreat, projection.Summary.Rating)
}

func TestProjectSpendingLeavesAWholePeriodAlone(t *testing.T) {
	september := PeriodWindow(PeriodOf(NewDate(2026, time.September, 10), GrainMonth), NewDate(2026, time.October, 3))
	require.False(t, september.Partial)
	pay := Occurrence{
		SeriesID: "pay", AccountID: "checking", Kind: SeriesIncome,
		DueOn: NewDate(2026, time.September, 30), Amount: MustFromString("3100.00"),
	}
	_, ok := ProjectSpending(SummarizeSpending(Zero, Zero), september, []Occurrence{pay},
		ProjectionScope{Accounts: map[ID]Account{"checking": {ID: "checking"}}})
	require.False(t, ok)
}

func TestProjectSpendingWithNothingScheduledIsTheActualFigures(t *testing.T) {
	today := NewDate(2026, time.October, 3)
	window := PeriodWindow(PeriodOf(today, GrainQuarter), today)
	actual := SummarizeSpending(MustFromString("500.00"), MustFromString("-200.00"))
	projection, ok := ProjectSpending(actual, window, nil, ProjectionScope{})
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.December, 31), projection.End)
	require.Zero(t, projection.Count)
	require.Equal(t, actual, projection.Summary)
}
