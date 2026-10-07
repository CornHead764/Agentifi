package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func flowToday() Date { return NewDate(2026, time.September, 1) }

func flowTestSeries() Series {
	return Series{
		ID:          "ser-rent",
		AccountID:   "acct-1",
		Description: "ACME PROPERTY MGMT RENT",
		Amount:      MustFromString("-1500.00"),
		Recurrence:  EveryMonth(5),
		StartOn:     NewDate(2026, time.January, 5),
		NextDueOn:   NewDate(2026, time.September, 5),
		Currency:    "USD",
		IsActive:    true,
	}
}

func flowTestPaycheck() Series {
	return Series{
		ID:          "ser-pay",
		AccountID:   "acct-1",
		Description: "ACME CORP DES:PAYROLL",
		Amount:      MustFromString("2500.00"),
		Recurrence:  EveryXDays(14),
		StartOn:     NewDate(2026, time.January, 2),
		NextDueOn:   NewDate(2026, time.September, 11),
		Currency:    "USD",
		IsActive:    true,
	}
}

func flowTestOccurrence(amount string, on Date, seriesID ID) Occurrence {
	return Occurrence{SeriesID: seriesID, AccountID: "acct-1", DueOn: on, Amount: MustFromString(amount)}
}

// flowTestBill is the biller's word on the rent series' next slot: the
// scheduled date, so the bill claims it, and no autopay.
func flowTestBill(amount string) []BillConnect {
	return []BillConnect{{DueOn: NewDate(2026, time.September, 5), Amount: MustFromString(amount)}}
}

func TestAnOccurrenceUsesTheSeriesAmountByDefault(t *testing.T) {
	found := OccurrenceAmount(flowTestSeries(), NewDate(2026, time.September, 5), nil)
	require.Equal(t, "-1500.00", found.String())
}

func TestAOneOffOverrideAppliesToTheNextOccurrence(t *testing.T) {
	series := flowTestSeries()
	series.OverrideNextAmount = MustFromString("-1650.00")
	series.HasOverrideNextAmount = true
	found := OccurrenceAmount(series, NewDate(2026, time.September, 5), nil)
	require.Equal(t, "-1650.00", found.String())
}

func TestTheOverrideDoesNotLeakIntoLaterOccurrences(t *testing.T) {
	// One unusually large power bill must not project the user broke by March.
	series := flowTestSeries()
	series.OverrideNextAmount = MustFromString("-1650.00")
	series.HasOverrideNextAmount = true
	found := OccurrenceAmount(series, NewDate(2026, time.October, 5), nil)
	require.Equal(t, "-1500.00", found.String())
}

func TestALinkedSeriesTakesTheBillConnectFigure(t *testing.T) {
	found := OccurrenceAmount(flowTestSeries(), NewDate(2026, time.September, 5), flowTestBill("-1550.00"))
	require.Equal(t, "-1550.00", found.String())
}

func TestALinkedSeriesWithNoEBillYetFallsBackToItsEstimate(t *testing.T) {
	found := OccurrenceAmount(flowTestSeries(), NewDate(2026, time.September, 5), nil)
	require.Equal(t, "-1500.00", found.String())
}

func TestTheUsersOverrideOutranksBillConnect(t *testing.T) {
	series := flowTestSeries()
	series.OverrideNextAmount = MustFromString("-1650.00")
	series.HasOverrideNextAmount = true
	found := OccurrenceAmount(series, NewDate(2026, time.September, 5), flowTestBill("-1550.00"))
	require.Equal(t, "-1650.00", found.String())
}

func TestAProviderPaidBillStillSetsItsSlotsAmount(t *testing.T) {
	bills := flowTestBill("-1550.00")
	bills[0].Paid = true
	found := OccurrenceAmount(flowTestSeries(), NewDate(2026, time.September, 5), bills)
	require.Equal(t, "-1550.00", found.String())
}

func TestEverySeriesIsExpandedAcrossTheHorizonInDateOrder(t *testing.T) {
	found := ExpectedOccurrences(
		[]Series{flowTestSeries(), flowTestPaycheck()},
		flowToday(), NewDate(2026, time.October, 6), nil, nil, nil, nil,
	)
	type row struct {
		seriesID ID
		dueOn    Date
	}
	rows := make([]row, 0, len(found))
	for _, one := range found {
		rows = append(rows, row{one.SeriesID, one.DueOn})
	}
	require.Equal(t, []row{
		{"ser-rent", NewDate(2026, time.September, 5)},
		{"ser-pay", NewDate(2026, time.September, 11)},
		{"ser-pay", NewDate(2026, time.September, 25)},
		{"ser-rent", NewDate(2026, time.October, 5)},
	}, rows)
}

func TestAnOccurrenceAPostedChargeAlreadyPaidIsNotStillExpected(t *testing.T) {
	// It is already in the balance the projection starts from; counting it
	// again charges the user their rent twice.
	fulfilled := map[ID]map[Date]bool{
		"ser-rent": {NewDate(2026, time.September, 5): true},
	}
	found := ExpectedOccurrences(
		[]Series{flowTestSeries()},
		flowToday(), NewDate(2026, time.October, 31), fulfilled, nil, nil, nil,
	)
	require.Len(t, found, 1)
	require.Equal(t, NewDate(2026, time.October, 5), found[0].DueOn)
}

func TestTheAccountSelectorIsAFilterOverTheSameExpansion(t *testing.T) {
	other := flowTestSeries()
	other.ID = "ser-other"
	other.AccountID = "acct-2"

	found := ExpectedOccurrences(
		[]Series{flowTestSeries(), other},
		flowToday(), NewDate(2026, time.September, 30), nil, nil, nil, map[ID]bool{"acct-1": true},
	)
	require.Len(t, found, 1)
	require.Equal(t, ID("ser-rent"), found[0].SeriesID)
}

func TestBillConnectFiguresAreAppliedPerSeries(t *testing.T) {
	found := ExpectedOccurrences(
		[]Series{flowTestSeries(), flowTestPaycheck()},
		flowToday(), NewDate(2026, time.September, 30), nil,
		map[ID][]BillConnect{"ser-rent": flowTestBill("-1550.00")}, nil, nil,
	)
	require.Len(t, found, 3)
	require.Equal(t, ID("ser-rent"), found[0].SeriesID)
	require.Equal(t, "-1550.00", found[0].Amount.String())
	require.Equal(t, "2500.00", found[1].Amount.String())
	require.Equal(t, "2500.00", found[2].Amount.String())
}

func balanceOn(current Money, occurrences []Occurrence, day Date) Money {
	return ProjectBalances(current, occurrences, day, day)[0].Balance
}

func TestTheBalanceOnADayIsTodayPlusEverythingDueByThen(t *testing.T) {
	occurrences := []Occurrence{
		flowTestOccurrence("-1500.00", NewDate(2026, time.September, 5), "ser-rent"),
		flowTestOccurrence("2500.00", NewDate(2026, time.September, 11), "ser-pay"),
	}
	current := MustFromString("3000.00")
	require.Equal(t, "3000.00", balanceOn(current, occurrences, NewDate(2026, time.September, 4)).String())
	require.Equal(t, "1500.00", balanceOn(current, occurrences, NewDate(2026, time.September, 5)).String())
	require.Equal(t, "4000.00", balanceOn(current, occurrences, NewDate(2026, time.September, 30)).String())
}

func TestTheProjectionStaysExactToTheCent(t *testing.T) {
	found := balanceOn(
		MustFromString("100.005"),
		[]Occurrence{flowTestOccurrence("-0.015", NewDate(2026, time.September, 2), "ser-rent")},
		NewDate(2026, time.September, 30),
	)
	require.Equal(t, "99.99", found.String())
}

func TestTheProjectionReturnsOnePointPerDayCarryingTheBalanceForward(t *testing.T) {
	points := ProjectBalances(
		MustFromString("3000.00"),
		[]Occurrence{flowTestOccurrence("-1500.00", NewDate(2026, time.September, 5), "ser-rent")},
		flowToday(), NewDate(2026, time.September, 7),
	)
	balances := make([]string, 0, len(points))
	for _, point := range points {
		balances = append(balances, point.Balance.String())
	}
	require.Equal(t, []string{
		"3000.00", "3000.00", "3000.00", "3000.00", "1500.00", "1500.00", "1500.00",
	}, balances)
	require.Equal(t, flowToday(), points[0].On)
}

func TestAnOccurrenceBeforeTheWindowIsFoldedIntoTheOpeningPoint(t *testing.T) {
	points := ProjectBalances(
		MustFromString("3000.00"),
		[]Occurrence{flowTestOccurrence("-1500.00", NewDate(2026, time.August, 25), "ser-rent")},
		flowToday(), NewDate(2026, time.September, 2),
	)
	require.Equal(t, "1500.00", points[0].Balance.String())
}

func TestOneLinePerAccountComesFromOneExpansion(t *testing.T) {
	other := flowTestSeries()
	other.ID = "ser-other"
	other.AccountID = "acct-2"
	other.Amount = MustFromString("-40.00")

	lines := ProjectAccounts(
		map[ID]Money{"acct-1": MustFromString("3000.00"), "acct-2": MustFromString("500.00")},
		[]Series{flowTestSeries(), other},
		flowToday(), NewDate(2026, time.September, 30), nil, nil,
	)
	require.Equal(t, "1500.00", lines["acct-1"][len(lines["acct-1"])-1].Balance.String())
	require.Equal(t, "460.00", lines["acct-2"][len(lines["acct-2"])-1].Balance.String())
}

func TestTheLowBalanceWarningIsTheProjectionCrossingAThreshold(t *testing.T) {
	points := ProjectBalances(
		MustFromString("1600.00"),
		[]Occurrence{flowTestOccurrence("-1500.00", NewDate(2026, time.September, 5), "ser-rent")},
		flowToday(), NewDate(2026, time.September, 10),
	)
	crossing, ok := points.FirstBelow(MustFromString("200.00"))
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 5), crossing.On)

	_, ok = points.FirstBelow(Zero)
	require.False(t, ok)
}

func TestAProjectionThatNeverDipsRaisesNoWarning(t *testing.T) {
	points := ProjectBalances(MustFromString("1000.00"), nil, flowToday(), NewDate(2026, time.September, 10))
	_, ok := points.FirstBelow(Zero)
	require.False(t, ok)
}

func TestTheLowestPointIsTheWorstDayNotTheLastOne(t *testing.T) {
	points := ProjectBalances(
		MustFromString("3000.00"),
		[]Occurrence{
			flowTestOccurrence("-2900.00", NewDate(2026, time.September, 5), "ser-rent"),
			flowTestOccurrence("2500.00", NewDate(2026, time.September, 8), "ser-pay"),
		},
		flowToday(), NewDate(2026, time.September, 10),
	)
	lowest, ok := points.Lowest()
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 5), lowest.On)
	require.Equal(t, "100.00", lowest.Balance.String())
}

func TestAnEmptyProjectionHasNoLowestPoint(t *testing.T) {
	_, ok := BalanceProjection(nil).Lowest()
	require.False(t, ok)
}

// sameDayBills is a lawn-care provider's two invoices for one billed account,
// both due on the rent series' next slot, the second autopaying early.
func sameDayBills() []BillConnect {
	due := NewDate(2026, time.September, 5)
	return []BillConnect{
		{ID: "bill-mow", DueOn: due, Amount: MustFromString("-58.00")},
		{ID: "bill-feed", DueOn: due, Amount: MustFromString("-71.50"), AutopayOn: NewDate(2026, time.September, 3)},
	}
}

func TestSlotBillsAreEveryBillDueOnTheNearestDay(t *testing.T) {
	bills := append(sameDayBills(),
		BillConnect{ID: "bill-old", DueOn: NewDate(2026, time.September, 2), Amount: MustFromString("-10.00")})
	found := SlotBills(flowTestSeries(), NewDate(2026, time.September, 5), bills)
	require.Len(t, found, 2)
	require.Equal(t, []ID{"bill-mow", "bill-feed"}, []ID{found[0].ID, found[1].ID})

	require.Empty(t, SlotBills(flowTestSeries(), NewDate(2026, time.November, 5), bills))
}

func TestASlotWithTwoSameDayBillsCostsBoth(t *testing.T) {
	found := OccurrenceAmount(flowTestSeries(), NewDate(2026, time.September, 5), sameDayBills())
	require.True(t, found.Equal(MustFromString("-129.50")), found.String())
}

func TestTwoSameDayBillsAreListedAsTwoOccurrencesOfOneSlot(t *testing.T) {
	series := flowTestSeries()
	slot := NewDate(2026, time.September, 5)
	bills := map[ID][]BillConnect{series.ID: sameDayBills()}

	found := ExpectedOccurrences([]Series{series}, slot, slot, nil, bills, nil, nil)
	require.Len(t, found, 2)
	require.Equal(t, ID("bill-mow"), found[0].BillID)
	require.True(t, found[0].Amount.Equal(MustFromString("-58.00")))
	require.True(t, found[0].PaysOn.IsZero())
	require.Equal(t, ID("bill-feed"), found[1].BillID)
	require.True(t, found[1].Amount.Equal(MustFromString("-71.50")))
	require.Equal(t, NewDate(2026, time.September, 3), found[1].PaysOn)
	for _, one := range found {
		require.Equal(t, slot, one.ScheduledOn)
		require.Equal(t, slot, one.DueOn)
	}

	// A charge filed under the slot pays the cycle, both bills with it.
	paid := map[ID]map[Date]bool{series.ID: {slot: true}}
	require.Empty(t, ExpectedOccurrences([]Series{series}, slot, slot, paid, bills, nil, nil))
}

func TestAnOverrideStandsForBothSameDayBills(t *testing.T) {
	series := flowTestSeries()
	series.HasOverrideNextAmount = true
	series.OverrideNextAmount = MustFromString("-120.00")
	slot := NewDate(2026, time.September, 5)

	bills := map[ID][]BillConnect{series.ID: sameDayBills()}
	found := ExpectedOccurrences([]Series{series}, slot, slot, nil, bills, nil, nil)
	require.Len(t, found, 1)
	require.True(t, found[0].Amount.Equal(MustFromString("-120.00")))
}
