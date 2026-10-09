package domain

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The power bill: monthly on the 5th, the next slot standing on 5 September.
func billTestSeries() Series {
	return Series{
		ID:          "ser-power",
		AccountID:   "acct-1",
		Description: "CITY POWER BILLPAY",
		Amount:      MustFromString("-120.00"),
		Recurrence:  EveryMonth(5),
		StartOn:     NewDate(2026, time.January, 5),
		NextDueOn:   NewDate(2026, time.September, 5),
		Currency:    "USD",
		IsActive:    true,
	}
}

func TestBillersCatalogueIsWellFormed(t *testing.T) {
	seen := map[BillerID]bool{}
	for _, biller := range Billers {
		require.NotEmpty(t, biller.ID)
		require.False(t, seen[biller.ID], "duplicate biller id %s", biller.ID)
		seen[biller.ID] = true

		require.NotEmpty(t, biller.Name, "%s has no display name", biller.ID)
		if biller.Generic {
			require.Empty(t, biller.Home, biller.ID)
			require.Equal(t, AccessEmail, biller.Access, biller.ID)
		} else {
			require.True(t, strings.HasPrefix(biller.Home, "https://"), "%s: %q", biller.ID, biller.Home)
			require.NotContains(t, biller.Home, " ", "%s: %q", biller.ID, biller.Home)
		}

		switch biller.Access {
		case AccessAPI, AccessBrowser, AccessEmail:
		default:
			t.Fatalf("%s has no known access path: %q", biller.ID, biller.Access)
		}

		found, ok := BillerByID(biller.ID)
		require.True(t, ok)
		require.Equal(t, biller.Name, found.Name)
	}
	_, ok := BillerByID("not-a-biller")
	require.False(t, ok)
}

func TestBillClaimsSlotOnlyWithinMatchWindow(t *testing.T) {
	// A monthly series' window is three days before the slot and five after.
	monthly := EveryMonth(5)
	slot := NewDate(2026, time.September, 5)
	claims := func(year int, month time.Month, day int) bool {
		return BillClaimsSlot(monthly, slot, NewDate(year, month, day))
	}
	require.True(t, claims(2026, time.September, 5))
	require.True(t, claims(2026, time.September, 2))
	require.True(t, claims(2026, time.September, 10))
	require.False(t, claims(2026, time.September, 1))
	require.False(t, claims(2026, time.September, 11))
	// Three weeks out is next month's bill, not this slot's.
	require.False(t, claims(2026, time.September, 26))
	require.False(t, BillClaimsSlot(monthly, slot, Date{}))

	// A weekly series' window is tighter, two days each way, so a bill can
	// never be claimed by the neighbouring occurrence.
	weekly := EveryXDays(7)
	require.True(t, BillClaimsSlot(weekly, slot, NewDate(2026, time.September, 7)))
	require.False(t, BillClaimsSlot(weekly, slot, NewDate(2026, time.September, 8)))
	require.True(t, BillClaimsSlot(weekly, slot, NewDate(2026, time.September, 3)))
	require.False(t, BillClaimsSlot(weekly, slot, NewDate(2026, time.September, 2)))
}

func TestBillPaymentDatesReachTheFarEdgeOfEitherSlotTheBillCouldClaim(t *testing.T) {
	// Monthly: three days before a slot and five after. A bill due on the
	// 10th claims a slot on the 5th at the earliest, paid as early as the 2nd,
	// and a slot on the 13th at the latest, paid as late as the 18th.
	from, to := BillPaymentDates(EveryMonth(5), NewDate(2026, time.September, 10))
	require.Equal(t, NewDate(2026, time.September, 2), from)
	require.Equal(t, NewDate(2026, time.September, 18), to)

	// Weekly: two days each way.
	from, to = BillPaymentDates(EveryXDays(7), NewDate(2026, time.September, 10))
	require.Equal(t, NewDate(2026, time.September, 6), from)
	require.Equal(t, NewDate(2026, time.September, 14), to)
}

func TestSlotBillIsTheNearestBillInTheSlotsWindow(t *testing.T) {
	series := billTestSeries()
	early := BillConnect{DueOn: NewDate(2026, time.September, 3), Amount: MustFromString("-61.00")}
	late := BillConnect{DueOn: NewDate(2026, time.September, 7), Amount: MustFromString("-62.00")}
	near := BillConnect{DueOn: NewDate(2026, time.September, 6), Amount: MustFromString("-63.00")}
	october := BillConnect{DueOn: NewDate(2026, time.October, 6), Amount: MustFromString("-64.00")}

	found, ok := SlotBill(series, NewDate(2026, time.September, 5), []BillConnect{early, late, near, october})
	require.True(t, ok)
	require.Equal(t, near.DueOn, found.DueOn)

	// Two bills as near: the later one is the newer word on the cycle.
	found, ok = SlotBill(series, NewDate(2026, time.September, 5), []BillConnect{late, early})
	require.True(t, ok)
	require.Equal(t, late.DueOn, found.DueOn)

	found, ok = SlotBill(series, NewDate(2026, time.October, 5), []BillConnect{early, late, near, october})
	require.True(t, ok)
	require.Equal(t, october.DueOn, found.DueOn)

	_, ok = SlotBill(series, NewDate(2026, time.November, 5), []BillConnect{early, late, near, october})
	require.False(t, ok)
}

func TestOccurrenceAmountPrefersUserOverrideOverBill(t *testing.T) {
	// The adjust switch on and a bill in hand: the typed amount still wins,
	// at the bill's own due date.
	series := billTestSeries()
	series.AutoAdjustDueOn = true
	series.OverrideNextAmount = MustFromString("-95.00")
	series.HasOverrideNextAmount = true
	slot := NewDate(2026, time.September, 5)

	bills := []BillConnect{{DueOn: NewDate(2026, time.September, 8), Amount: MustFromString("-131.00")}}
	require.Equal(t, NewDate(2026, time.September, 8), OccurrenceDueOn(series, slot, bills))
	require.Equal(t, "-95.00", OccurrenceAmount(series, slot, bills).String())

	// Without the typed amount the bill's figure is what the slot costs.
	series.HasOverrideNextAmount = false
	require.Equal(t, "-131.00", OccurrenceAmount(series, slot, bills).String())

	// A bill outside the window speaks about a later cycle and changes neither.
	late := []BillConnect{{DueOn: NewDate(2026, time.September, 26), Amount: MustFromString("-131.00")}}
	require.Equal(t, slot, OccurrenceDueOn(series, slot, late))
	require.Equal(t, "-120.00", OccurrenceAmount(series, slot, late).String())
}

func TestALinkedBillSetsTheAmountWithTheDueDateSwitchOff(t *testing.T) {
	// The due-date switch decides only the day: the provider's figure is the
	// cycle's cost either way.
	series := billTestSeries()
	slot := NewDate(2026, time.September, 5)
	bills := []BillConnect{{DueOn: NewDate(2026, time.September, 8), Amount: MustFromString("-131.00")}}

	require.Equal(t, "-131.00", OccurrenceAmount(series, slot, bills).String())
	require.Equal(t, slot, OccurrenceDueOn(series, slot, bills))
}

func TestOccurrenceSlotsMoveEachSlotToItsOpenBill(t *testing.T) {
	series := billTestSeries()
	series.AutoAdjustDueOn = true
	bills := []BillConnect{{DueOn: NewDate(2026, time.September, 8), Amount: MustFromString("-131.00")}}

	found := OccurrenceSlots(series, NewDate(2026, time.August, 1), NewDate(2026, time.October, 31), bills)
	require.Equal(t, []OccurrenceSlot{
		{ScheduledOn: NewDate(2026, time.August, 5), DueOn: NewDate(2026, time.August, 5)},
		{ScheduledOn: NewDate(2026, time.September, 5), DueOn: NewDate(2026, time.September, 8)},
		{ScheduledOn: NewDate(2026, time.October, 5), DueOn: NewDate(2026, time.October, 5)},
	}, found)

	// The projection's expansion must not project the bill twice either.
	occurrences := ExpectedOccurrences(
		[]Series{series},
		NewDate(2026, time.September, 1), NewDate(2026, time.September, 30),
		nil, map[ID][]BillConnect{"ser-power": bills}, nil, nil,
	)
	require.Len(t, occurrences, 1)
	require.Equal(t, NewDate(2026, time.September, 8), occurrences[0].DueOn)

	// A slot shown past the window's end leaves it, and one shown inside the
	// window from a scheduled date just outside it joins.
	require.Empty(t, OccurrenceSlots(series, NewDate(2026, time.September, 1), NewDate(2026, time.September, 7), bills))
	require.Equal(t, []OccurrenceSlot{
		{ScheduledOn: NewDate(2026, time.September, 5), DueOn: NewDate(2026, time.September, 8)},
	}, OccurrenceSlots(series, NewDate(2026, time.September, 6), NewDate(2026, time.September, 9), bills))

	// A series that did not ask to be auto-adjusted keeps its schedule.
	series.AutoAdjustDueOn = false
	require.Equal(t, []OccurrenceSlot{
		{ScheduledOn: NewDate(2026, time.August, 5), DueOn: NewDate(2026, time.August, 5)},
		{ScheduledOn: NewDate(2026, time.September, 5), DueOn: NewDate(2026, time.September, 5)},
		{ScheduledOn: NewDate(2026, time.October, 5), DueOn: NewDate(2026, time.October, 5)},
	}, OccurrenceSlots(series, NewDate(2026, time.August, 1), NewDate(2026, time.October, 31), bills))
}

func TestEveryLinkedSlotTakesItsOwnBillPastDueIncluded(t *testing.T) {
	// A utility reminder on the 3rd whose pointer was never advanced: the
	// August and September slots are past due, October is to come. The
	// provider reports August and September paid and October open, each a
	// few days after the reminder's own day.
	series := Series{
		ID:              "ser-gas",
		AccountID:       "acct-1",
		Description:     "METRO GAS AUTOPAY",
		Amount:          MustFromString("-150.00"),
		Recurrence:      EveryMonth(3),
		StartOn:         NewDate(2026, time.January, 3),
		NextDueOn:       NewDate(2026, time.August, 3),
		AutoAdjustDueOn: true,
		Currency:        "USD",
		IsActive:        true,
	}
	bills := []BillConnect{
		{DueOn: NewDate(2026, time.July, 6), Amount: MustFromString("-88.00"), Paid: true},
		{DueOn: NewDate(2026, time.August, 6), Amount: MustFromString("-97.00"), Paid: true},
		{DueOn: NewDate(2026, time.September, 7), Amount: MustFromString("-105.00"), Paid: true},
		{DueOn: NewDate(2026, time.October, 7), Amount: MustFromString("-112.00"),
			AutopayOn: NewDate(2026, time.October, 2)},
	}

	found := ExpectedOccurrences(
		[]Series{series},
		NewDate(2026, time.August, 1), NewDate(2026, time.October, 31),
		nil, map[ID][]BillConnect{"ser-gas": bills}, nil, nil,
	)
	require.Len(t, found, 3)

	// A paid bill gives its slot the cycle's real cost but neither moves it
	// nor dates a payment: the provider's word is not a bank charge, and the
	// slot stays owed until one matches.
	require.Equal(t, NewDate(2026, time.August, 3), found[0].DueOn)
	require.Equal(t, NewDate(2026, time.August, 3), found[0].ScheduledOn)
	require.Equal(t, "-97.00", found[0].Amount.String())
	require.True(t, found[0].PaysOn.IsZero())

	require.Equal(t, NewDate(2026, time.September, 3), found[1].DueOn)
	require.Equal(t, "-105.00", found[1].Amount.String())
	require.True(t, found[1].PaysOn.IsZero())

	// The open bill moves its slot to its due date and says when it pays.
	require.Equal(t, NewDate(2026, time.October, 7), found[2].DueOn)
	require.Equal(t, NewDate(2026, time.October, 3), found[2].ScheduledOn)
	require.Equal(t, "-112.00", found[2].Amount.String())
	require.Equal(t, NewDate(2026, time.October, 2), found[2].PaysOn)

	// November has no bill yet: the estimate.
	november := ExpectedOccurrences(
		[]Series{series},
		NewDate(2026, time.November, 1), NewDate(2026, time.November, 30),
		nil, map[ID][]BillConnect{"ser-gas": bills}, nil, nil,
	)
	require.Len(t, november, 1)
	require.Equal(t, "-150.00", november[0].Amount.String())
}

func TestAutopayOnFromRuleNeverAfterDue(t *testing.T) {
	due := NewDate(2026, time.October, 26)
	bill := BillConnect{DueOn: due, Amount: MustFromString("-120.00")}

	require.Equal(t, NewDate(2026, time.October, 21),
		AutopayOn(bill, AutopayRule{Kind: AutopayDaysBeforeDue, Days: 5}))
	require.Equal(t, due, AutopayOn(bill, AutopayRule{Kind: AutopayDaysBeforeDue}))

	require.Equal(t, due, AutopayOn(bill, AutopayRule{Kind: AutopayOnDueDate}))

	// day_of_month: this month's when it has not passed, else the previous.
	require.Equal(t, NewDate(2026, time.October, 21),
		AutopayOn(bill, AutopayRule{Kind: AutopayDayOfMonth, DayOfMonth: 21}))
	require.Equal(t, NewDate(2026, time.October, 26),
		AutopayOn(bill, AutopayRule{Kind: AutopayDayOfMonth, DayOfMonth: 26}))
	require.Equal(t, NewDate(2026, time.September, 28),
		AutopayOn(bill, AutopayRule{Kind: AutopayDayOfMonth, DayOfMonth: 28}))
	// The 31st of September does not exist; the wrap lands on its last day.
	require.Equal(t, NewDate(2026, time.September, 30),
		AutopayOn(bill, AutopayRule{Kind: AutopayDayOfMonth, DayOfMonth: 31}))

	// No rule and nothing stated is no answer, not a guess at the due date.
	require.True(t, AutopayOn(bill, AutopayRule{Kind: AutopayNone}).IsZero())
	require.True(t, AutopayOn(BillConnect{}, AutopayRule{Kind: AutopayOnDueDate}).IsZero())

	// The provider's own stated date outranks the household's rule.
	stated := bill
	stated.AutopayOn = NewDate(2026, time.October, 11)
	require.Equal(t, NewDate(2026, time.October, 11),
		AutopayOn(stated, AutopayRule{Kind: AutopayDaysBeforeDue, Days: 5}))

	// Nothing comes back later than the due date, whoever said it.
	later := bill
	later.AutopayOn = NewDate(2026, time.October, 29)
	require.Equal(t, due, AutopayOn(later, AutopayRule{Kind: AutopayNone}))
}

func TestSupersededBillsKeepsTheLatest(t *testing.T) {
	bills := []Bill{
		{ID: "bill-jul", SubaccountID: "sub-electric", DueOn: NewDate(2026, time.July, 15),
			AmountDue: MustFromString("110.00"), Status: BillOpen},
		{ID: "bill-sep", SubaccountID: "sub-electric", DueOn: NewDate(2026, time.September, 15),
			AmountDue: MustFromString("120.00"), Status: BillOpen},
		{ID: "bill-aug", SubaccountID: "sub-electric", DueOn: NewDate(2026, time.August, 15),
			AmountDue: MustFromString("126.00"), Status: BillOpen},
		// A later bill the provider already calls paid supersedes nothing: it
		// is not one of the open ones the question is about.
		{ID: "bill-oct", SubaccountID: "sub-electric", DueOn: NewDate(2026, time.October, 15),
			AmountDue: MustFromString("141.00"), Status: BillPaid},
		// Another subaccount is another ledger.
		{ID: "bill-gas", SubaccountID: "sub-gas", DueOn: NewDate(2026, time.August, 20),
			AmountDue: MustFromString("42.00"), Status: BillOpen},
	}

	found := SupersededBills(bills)
	ids := make([]ID, 0, len(found))
	for _, one := range found {
		ids = append(ids, one.ID)
	}
	require.Equal(t, []ID{"bill-jul", "bill-aug"}, ids)

	// One open bill on a subaccount is never superseded by itself.
	require.Empty(t, SupersededBills(bills[4:]))
	require.Empty(t, SupersededBills(nil))
}

func TestSupersededBillsKeepsEveryInvoiceOfTheLatestDay(t *testing.T) {
	// Two invoices due the same day are two bills; only the older day goes.
	bills := []Bill{
		{ID: "inv-b", SubaccountID: "sub-lawn", DueOn: NewDate(2026, time.May, 12),
			AmountDue: MustFromString("55.00"), Status: BillOpen},
		{ID: "inv-a", SubaccountID: "sub-lawn", DueOn: NewDate(2026, time.May, 12),
			AmountDue: MustFromString("35.00"), Status: BillOpen},
		{ID: "inv-old", SubaccountID: "sub-lawn", DueOn: NewDate(2026, time.April, 7),
			AmountDue: MustFromString("55.00"), Status: BillOpen},
	}
	found := SupersededBills(bills)
	require.Len(t, found, 1)
	require.Equal(t, ID("inv-old"), found[0].ID)
}

func TestProjectBalancesStepsOnTheAutopayDate(t *testing.T) {
	// The power bill is due the 10th but autopays on the 5th, so the
	// projection steps on the 5th as the bank will.
	occurrences := []Occurrence{
		{SeriesID: "ser-pay", AccountID: "acct-1", DueOn: NewDate(2026, time.September, 8),
			Amount: MustFromString("500.00")},
		{SeriesID: "ser-power", AccountID: "acct-1", DueOn: NewDate(2026, time.September, 10),
			PaysOn: NewDate(2026, time.September, 5), Amount: MustFromString("-150.00")},
		{SeriesID: "ser-rent", AccountID: "acct-1", DueOn: NewDate(2026, time.September, 3),
			Amount: MustFromString("-200.00")},
	}
	points := ProjectBalances(
		MustFromString("1000.00"), occurrences,
		NewDate(2026, time.September, 1), NewDate(2026, time.September, 12),
	)

	expected := []string{
		"1000.00", // Sep 1
		"1000.00", // Sep 2
		"800.00",  // Sep 3, rent
		"800.00",  // Sep 4
		"650.00",  // Sep 5, the power bill autopays five days before it is due
		"650.00",  // Sep 6
		"650.00",  // Sep 7
		"1150.00", // Sep 8, paycheck
		"1150.00", // Sep 9
		"1150.00", // Sep 10, the due date the bill already left on
		"1150.00", // Sep 11
		"1150.00", // Sep 12
	}
	require.Len(t, points, len(expected))
	for index, want := range expected {
		on := NewDate(2026, time.September, index+1)
		require.Equal(t, on, points[index].On)
		require.Equal(t, want, points[index].Balance.String(), "on %s", on)
	}

	// The occurrence still says it is due on the 10th; only the projection
	// asks when the money moves.
	require.Equal(t, NewDate(2026, time.September, 10), occurrences[1].DueOn)
	require.Equal(t, NewDate(2026, time.September, 5), occurrences[1].MovesOn())
	require.Equal(t, NewDate(2026, time.September, 3), occurrences[2].MovesOn())
}

func TestTheBillsPaymentDateReachesTheOccurrence(t *testing.T) {
	series := billTestSeries()
	bill := BillConnect{
		DueOn:     NewDate(2026, time.September, 5),
		Amount:    MustFromString("-131.00"),
		AutopayOn: NewDate(2026, time.August, 31),
	}
	found := ExpectedOccurrences(
		[]Series{series},
		NewDate(2026, time.August, 25), NewDate(2026, time.October, 31),
		nil, map[ID][]BillConnect{"ser-power": {bill}}, nil, nil,
	)
	require.Len(t, found, 2)
	require.Equal(t, NewDate(2026, time.September, 5), found[0].DueOn)
	require.Equal(t, NewDate(2026, time.August, 31), found[0].PaysOn)
	require.Equal(t, "-131.00", found[0].Amount.String())
	// A bill speaks about one cycle: October's slot carries neither figure.
	require.Equal(t, NewDate(2026, time.October, 5), found[1].DueOn)
	require.True(t, found[1].PaysOn.IsZero())
	require.Equal(t, "-120.00", found[1].Amount.String())
}

func TestMovedOccurrenceIsSettledByItsScheduledSlot(t *testing.T) {
	// The matcher files a charge under the slot the rule landed on, while a
	// reader shows the occurrence on the day a bill or override moved it to.
	// The settled map must be asked with the slot, or a moved bill reads
	// unpaid after the charge posts.
	monthly := billTestSeries()
	monthly.Recurrence = EveryMonth(1)
	monthly.StartOn = NewDate(2026, time.January, 1)
	monthly.NextDueOn = NewDate(2026, time.September, 1)

	slot := NewDate(2026, time.September, 1)
	shownOn := NewDate(2026, time.September, 3)
	start, end := NewDate(2026, time.September, 1), NewDate(2026, time.September, 30)

	charge := Transaction{
		ID:            "txn-power",
		AccountID:     "acct-1",
		Date:          shownOn,
		Amount:        MustFromString("-120.00"),
		StatementName: "CITY POWER BILLPAY",
		Payee:         "City Power",
		Currency:      "USD",
		Source:        SourceSync,
	}

	withBill := monthly
	withBill.AutoAdjustDueOn = true
	withOverride := monthly
	withOverride.OverrideNextDueOn = shownOn

	moved := []struct {
		what   string
		series Series
		bills  map[ID][]BillConnect
	}{
		{
			what:   "a bill the series asked to be adjusted to",
			series: withBill,
			bills: map[ID][]BillConnect{
				"ser-power": {{DueOn: shownOn, Amount: MustFromString("-120.00")}},
			},
		},
		{
			what:   "the household's own one-off override",
			series: withOverride,
		},
	}

	for _, one := range moved {
		t.Run(one.what, func(t *testing.T) {
			expected := ExpectedOccurrences(
				[]Series{one.series}, start, end, nil, one.bills, nil, nil)
			require.Len(t, expected, 1)
			require.Equal(t, shownOn, expected[0].DueOn)
			require.Equal(t, slot, expected[0].ScheduledOn)

			// Posted on the moved day, filed under the slot regardless.
			filedUnder, ok := MatchingOccurrence(
				MatchContext{Series: one.series, Tolerance: ExactAmount()}, charge)
			require.True(t, ok)
			require.Equal(t, slot, filedUnder)

			settled := map[ID]map[Date]bool{"ser-power": {filedUnder: true}}
			require.Empty(t, ExpectedOccurrences(
				[]Series{one.series}, start, end, settled, one.bills, nil, nil))
		})
	}
}

func TestASecondFactorChoiceSaysWhetherItIsAnsweredUnattended(t *testing.T) {
	for _, tc := range []struct {
		factor SecondFactor
		key    bool
		valid  bool
		alone  bool
	}{
		{SecondFactorAny, true, true, false},
		{SecondFactorEmail, false, true, true},
		{SecondFactorTOTP, true, true, true},
		{SecondFactorTOTP, false, true, false},
		{"sms", true, false, false},
		{"carrier-pigeon", true, false, false},
	} {
		if got := tc.factor.Valid(); got != tc.valid {
			t.Errorf("%q.Valid() = %v", tc.factor, got)
		}
		if got := tc.factor.Unattended(tc.key); got != tc.alone {
			t.Errorf("%q.Unattended(%v) = %v", tc.factor, tc.key, got)
		}
	}
}

// A yearly premium on a monthly reminder: each bill is counted against the
// slots bank rows hold, through the same window that files its statement.
func TestTallyBillHistoryCountsSettledUnsettledAndStillToCome(t *testing.T) {
	bills := []BillOnFile{
		{DueOn: NewDate(2024, time.March, 21), HasStatement: true},
		{DueOn: NewDate(2025, time.March, 22)},
		// Nothing holds a slot near it, and it is past: unsettled.
		{DueOn: NewDate(2025, time.June, 2)},
		// Due after today: still to be paid, counted in neither.
		{DueOn: NewDate(2026, time.March, 19)},
	}
	held := []Date{
		NewDate(2024, time.March, 20),
		// The bill due 22 March is 2 days after this slot, inside 3 before / 5 after.
		NewDate(2025, time.March, 20),
		// A slot held 9 days before the June bill claims nothing.
		NewDate(2025, time.May, 24),
	}
	got := TallyBillHistory(EveryMonth(20), bills, held, NewDate(2025, time.October, 1))
	require.Equal(t, BillHistory{Settled: 2, WithStatement: 1, Unsettled: 1}, got)
}

func TestBillDueGapIsTheMedianOfDistinctDueDates(t *testing.T) {
	_, ok := BillDueGap([]Date{NewDate(2025, time.March, 1)})
	require.False(t, ok)

	gap, ok := BillDueGap([]Date{
		NewDate(2026, time.March, 19),
		NewDate(2024, time.March, 21),
		NewDate(2025, time.March, 20),
		NewDate(2025, time.March, 20),
	})
	require.True(t, ok)
	// 2024-03-21 to 2025-03-20 is 364 days; 2025-03-20 to 2026-03-19 is 364.
	require.Equal(t, 364, gap)
}

func TestBillCadenceDisagreesOnlyOutsideHalfToOneAndAHalfPeriods(t *testing.T) {
	require.True(t, BillCadenceDisagrees(EveryMonth(20), 364))
	require.False(t, BillCadenceDisagrees(EveryMonth(20), 28))
	require.False(t, BillCadenceDisagrees(EveryMonth(20), 31))
	require.False(t, BillCadenceDisagrees(EveryYear(), 364))
	require.True(t, BillCadenceDisagrees(EveryYear(), 31))
}
