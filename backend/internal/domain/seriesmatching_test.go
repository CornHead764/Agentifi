package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	rentText    = "ACME PROPERTY MGMT RENT"
	payrollText = "ACME CORP DES:PAYROLL"
)

func matchTestSeries() Series {
	return Series{
		ID:          "ser-rent",
		AccountID:   "acct-1",
		Description: rentText,
		Amount:      MustFromString("-1500.00"),
		Recurrence:  EveryMonth(1),
		StartOn:     NewDate(2026, time.January, 1),
		NextDueOn:   NewDate(2026, time.September, 1),
		Currency:    "USD",
		IsActive:    true,
	}
}

func matchTestContext() MatchContext {
	return MatchContext{Series: matchTestSeries(), Tolerance: ExactAmount()}
}

func matchTestCharge(amount string, on Date) Transaction {
	return Transaction{
		ID:            "txn-1",
		AccountID:     "acct-1",
		Date:          on,
		Amount:        MustFromString(amount),
		StatementName: rentText,
		Payee:         "Rent",
		Currency:      "USD",
		Source:        SourceSync,
	}
}

func matchTestRentCharge() Transaction {
	return matchTestCharge("-1500.00", NewDate(2026, time.September, 1))
}

func matchTestPlaceholder() MatchPlaceholder {
	return MatchPlaceholder{
		ID:       "ph-1",
		SeriesID: "ser-rent",
		DueOn:    NewDate(2026, time.September, 1),
		Amount:   MustFromString("-1500.00"),
	}
}

func TestIdenticalWordingScoresOne(t *testing.T) {
	require.Equal(t, 1.0, TokenSimilarity(rentText, rentText))
}

func TestUnrelatedWordingScoresZero(t *testing.T) {
	require.Equal(t, 0.0, TokenSimilarity("Paycheck", payrollText))
}

func TestOverlapIsMeasuredAgainstTheLongerDescription(t *testing.T) {
	require.InDelta(t, 2.0/5.0, TokenSimilarity("STREAMSVC COM", "STREAMSVC COM 800 555 0100"), 1e-12)
}

func TestMissingWordingNeverScores(t *testing.T) {
	require.Equal(t, 0.0, TokenSimilarity("", rentText))
	require.Equal(t, 0.0, TokenSimilarity(rentText, ""))
}

func TestAChargeMatchingOnEverySignalIsACandidate(t *testing.T) {
	require.True(t, IsCandidate(matchTestContext(), matchTestRentCharge(), NewDate(2026, time.September, 1)))
}

func TestAChargeInAnotherAccountIsNeverTheSameSeries(t *testing.T) {
	charge := matchTestRentCharge()
	charge.AccountID = "acct-2"
	require.False(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestARefundDoesNotMatchTheBillItReverses(t *testing.T) {
	charge := matchTestCharge("1500.00", NewDate(2026, time.September, 1))
	require.False(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestAChargeInAnotherCurrencyIsNotTheSameCharge(t *testing.T) {
	charge := matchTestRentCharge()
	charge.Currency = "EUR"
	require.False(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestADeletedChargeIsNotACandidate(t *testing.T) {
	charge := matchTestRentCharge()
	charge.IsDeleted = true
	require.False(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestAMonthlyOccurrenceAcceptsThreeDaysEarlyToFiveLate(t *testing.T) {
	cases := []struct {
		name    string
		on      Date
		wantHit bool
	}{
		{"three days early", NewDate(2026, time.August, 29), true},
		{"on the day", NewDate(2026, time.September, 1), true},
		{"five days late", NewDate(2026, time.September, 6), true},
		{"four days early is outside the window", NewDate(2026, time.August, 28), false},
		{"six days late is outside the window", NewDate(2026, time.September, 7), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			charge := matchTestCharge("-1500.00", tc.on)
			require.Equal(t, tc.wantHit, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
		})
	}
}

func TestAWeeklySeriesUsesATighterWindowSoNeighbouringWeeksCannotClaimIt(t *testing.T) {
	context := matchTestContext()
	context.Series.Recurrence = EveryWeek()
	context.Series.StartOn = NewDate(2026, time.August, 3)

	inside := matchTestCharge("-1500.00", NewDate(2026, time.August, 5))
	outside := matchTestCharge("-1500.00", NewDate(2026, time.August, 6))
	require.True(t, IsCandidate(context, inside, NewDate(2026, time.August, 3)))
	require.False(t, IsCandidate(context, outside, NewDate(2026, time.August, 3)))
}

func TestWordingThatSharesNothingIsRejectedHoweverRightTheAmount(t *testing.T) {
	charge := matchTestRentCharge()
	charge.StatementName = "STARBUCKS 4491"
	charge.Payee = "Coffee"
	require.False(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestEitherOfTheChargesTwoNamesMayBeTheOneThatResembles(t *testing.T) {
	charge := matchTestRentCharge()
	charge.StatementName = "ACH DEBIT 88213"
	charge.Payee = rentText
	require.True(t, IsCandidate(matchTestContext(), charge, NewDate(2026, time.September, 1)))
}

func TestExactAcceptsOnlyTheSeriesAmount(t *testing.T) {
	band := ExactAmount()
	require.True(t, band.Accepts(MustFromString("-1500.00"), MustFromString("-1500.00"), nil))
	require.False(t, band.Accepts(MustFromString("-1500.00"), MustFromString("-1500.01"), nil))
}

func TestAnyAcceptsWhateverPosted(t *testing.T) {
	band := AnyAmount()
	require.True(t, band.Accepts(MustFromString("-1500.00"), MustFromString("-3.00"), nil))
	_, _, bounded := band.Bounds(MustFromString("-1500.00"), nil)
	require.False(t, bounded)
}

func TestRangeAcceptsInsideTheBandTheUserTyped(t *testing.T) {
	band, err := BetweenAmounts(MustFromString("-200.00"), MustFromString("-80.00"))
	require.NoError(t, err)
	require.True(t, band.Accepts(MustFromString("-120.00"), MustFromString("-190.00"), nil))
	require.False(t, band.Accepts(MustFromString("-120.00"), MustFromString("-210.00"), nil))
}

func TestAnInvertedRangeIsRejectedAtConstruction(t *testing.T) {
	_, err := BetweenAmounts(MustFromString("-80.00"), MustFromString("-200.00"))
	require.Error(t, err)
}

func TestAutoWidensFromWhatTheSeriesHasActuallyCharged(t *testing.T) {
	band := AutoAmount()
	observed := []Money{MustFromString("-100.00"), MustFromString("-140.00")}
	expected := MustFromString("-100.00")
	require.False(t, band.Accepts(expected, MustFromString("-140.00"), nil))
	require.True(t, band.Accepts(expected, MustFromString("-140.00"), observed))
	require.True(t, band.Accepts(expected, MustFromString("-149.00"), observed))
	require.False(t, band.Accepts(expected, MustFromString("-160.00"), observed))
}

func TestAutoWithNoHistoryIsTheEstimatePadded(t *testing.T) {
	band := AutoAmount()
	require.True(t, band.Accepts(MustFromString("-100.00"), MustFromString("-108.00"), nil))
	require.False(t, band.Accepts(MustFromString("-100.00"), MustFromString("-112.00"), nil))
}

func TestAMeteredUtilityMatchesOnlyOnceItsToleranceIsWidened(t *testing.T) {
	strict := matchTestContext()
	strict.Series.Amount = MustFromString("-120.00")

	widened := matchTestContext()
	widened.Series.Amount = MustFromString("-120.00")
	band, err := BetweenAmounts(MustFromString("-200.00"), MustFromString("-80.00"))
	require.NoError(t, err)
	widened.Tolerance = band

	powerBill := matchTestCharge("-183.00", NewDate(2026, time.September, 1))
	require.False(t, IsCandidate(strict, powerBill, NewDate(2026, time.September, 1)))
	require.True(t, IsCandidate(widened, powerBill, NewDate(2026, time.September, 1)))
}

func matchTestPaycheckContext() MatchContext {
	context := matchTestContext()
	context.Series.ID = "ser-pay"
	context.Series.Description = "Paycheck"
	context.Series.Amount = MustFromString("2500.00")
	context.Series.Recurrence = EveryXDays(14)
	context.Series.StartOn = NewDate(2026, time.January, 2)
	context.Series.NextDueOn = NewDate(2026, time.September, 4)
	return context
}

func matchTestDeposit() Transaction {
	deposit := matchTestCharge("2500.00", NewDate(2026, time.September, 4))
	deposit.StatementName = payrollText
	deposit.Payee = "Pay"
	return deposit
}

func TestAPaycheckDoesNotMatchTheBanksPhrasingUntilItHasBeenTaught(t *testing.T) {
	// "Paycheck" shares no token with "ACME CORP DES:PAYROLL", so the first
	// link has to be made by hand.
	require.False(t, IsCandidate(matchTestPaycheckContext(), matchTestDeposit(), NewDate(2026, time.September, 4)))
}

func TestOnceAChargeIsLinkedItsWordingMatchesTheNextOneOnItsOwn(t *testing.T) {
	taught := matchTestPaycheckContext()
	taught.LearnedDescriptions = []string{payrollText}
	require.True(t, IsCandidate(taught, matchTestDeposit(), NewDate(2026, time.September, 4)))
}

func TestRenamingASeriesDoesNotStopItMatching(t *testing.T) {
	renamed := matchTestContext()
	renamed.Series.DisplayName = "Rent — new place"
	require.Equal(t, "Rent — new place", renamed.Series.Label())
	require.True(t, IsCandidate(renamed, matchTestRentCharge(), NewDate(2026, time.September, 1)))
}

func TestTheDisplayNameIsNeverMatchedAgainst(t *testing.T) {
	disguised := matchTestContext()
	disguised.Series.Description = "Paycheck"
	disguised.Series.DisplayName = rentText
	require.False(t, IsCandidate(disguised, matchTestRentCharge(), NewDate(2026, time.September, 1)))
}

func TestAChargeBetweenTwoOccurrencesTakesTheNearerOne(t *testing.T) {
	twice := matchTestContext()
	twice.Series.Recurrence = TwiceAMonth(1, 15)

	found, ok := MatchingOccurrence(twice, matchTestCharge("-1500.00", NewDate(2026, time.September, 14)))
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 15), found)
}

func TestAChargeThatFindsAPlaceholderUpgradesItAndAdvancesThePointer(t *testing.T) {
	decision, ok := Decide(matchTestRentCharge(), []MatchContext{matchTestContext()}, []MatchPlaceholder{matchTestPlaceholder()})
	require.True(t, ok)
	require.Equal(t, OutcomeUpgradePlaceholder, decision.Outcome)
	require.Equal(t, ID("ph-1"), decision.PlaceholderID)
	require.Equal(t, NewDate(2026, time.September, 1), decision.OccurrenceOn)
	require.Equal(t, NewDate(2026, time.October, 1), decision.AdvancePointerTo)
}

func TestAChargeThatFindsOnlyTheSeriesIsStampedAndAdvancesIt(t *testing.T) {
	decision, ok := Decide(matchTestRentCharge(), []MatchContext{matchTestContext()}, nil)
	require.True(t, ok)
	require.Equal(t, OutcomeStampSeries, decision.Outcome)
	require.Empty(t, decision.PlaceholderID)
	require.Equal(t, NewDate(2026, time.October, 1), decision.AdvancePointerTo)
}

func TestBackFillingAnOldOccurrenceLeavesThePointerAlone(t *testing.T) {
	// Moving it would silently skip the September payment still to come.
	charge := matchTestCharge("-1500.00", NewDate(2026, time.July, 1))
	decision, ok := Decide(charge, []MatchContext{matchTestContext()}, nil)
	require.True(t, ok)
	require.Equal(t, OutcomeBackfill, decision.Outcome)
	require.Equal(t, NewDate(2026, time.July, 1), decision.OccurrenceOn)
	require.True(t, decision.AdvancePointerTo.IsZero())
}

func TestAChargeThatPostsEarlyStillAdvancesPastTheOccurrenceItPaid(t *testing.T) {
	// Advancing only past the posting date would leave the pointer on an
	// occurrence already paid, and generation would write it again.
	charge := matchTestCharge("-1500.00", NewDate(2026, time.August, 30))
	decision, ok := Decide(charge, []MatchContext{matchTestContext()}, nil)
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 1), decision.OccurrenceOn)
	require.Equal(t, NewDate(2026, time.October, 1), decision.AdvancePointerTo)
}

func TestAnOccurrenceAChargeAlreadyPaysTakesNoSecondCharge(t *testing.T) {
	// The September rent is recorded; a second charge the same day is not it.
	paid := matchTestContext()
	paid.SettledOn = []Date{NewDate(2026, time.September, 1)}
	_, ok := Decide(matchTestRentCharge(), []MatchContext{paid}, nil)
	require.False(t, ok)
}

func TestASettledNeighbourDoesNotRefuseTheOccurrenceTheChargePays(t *testing.T) {
	paid := matchTestContext()
	paid.SettledOn = []Date{NewDate(2026, time.August, 1)}
	decision, ok := Decide(matchTestRentCharge(), []MatchContext{paid}, nil)
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.September, 1), decision.OccurrenceOn)
}

func TestAHandPickedOccurrenceIsDecidedAsTheMatcherWouldDecideIt(t *testing.T) {
	// A charge dated in July filed by hand under September pays September and
	// moves the pointer past it, as a September charge would.
	charge := matchTestCharge("-1400.00", NewDate(2026, time.July, 20))
	decision := DecideOccurrence(matchTestContext(), charge,
		NewDate(2026, time.September, 1), []MatchPlaceholder{matchTestPlaceholder()})
	require.Equal(t, OutcomeUpgradePlaceholder, decision.Outcome)
	require.Equal(t, ID("ph-1"), decision.PlaceholderID)
	require.Equal(t, MustFromString("-1400.00"), decision.AdoptAmount)
	require.Equal(t, NewDate(2026, time.October, 1), decision.AdvancePointerTo)
}

func TestAnUpgradedPlaceholderAdoptsWhatActuallyPosted(t *testing.T) {
	widened := matchTestContext()
	band, err := BetweenAmounts(MustFromString("-1600"), MustFromString("-1400"))
	require.NoError(t, err)
	widened.Tolerance = band

	charge := matchTestCharge("-1525.00", NewDate(2026, time.September, 1))
	decision, ok := Decide(charge, []MatchContext{widened}, []MatchPlaceholder{matchTestPlaceholder()})
	require.True(t, ok)
	require.True(t, decision.HasAdoptAmount)
	require.Equal(t, "-1525.00", decision.AdoptAmount.String())
}

func TestAPlaceholderThatWasAlreadyRightAdoptsNothing(t *testing.T) {
	decision, ok := Decide(matchTestRentCharge(), []MatchContext{matchTestContext()}, []MatchPlaceholder{matchTestPlaceholder()})
	require.True(t, ok)
	require.False(t, decision.HasAdoptAmount)
}

func TestTheLastOccurrenceOfAFiniteSeriesAdvancesToNothing(t *testing.T) {
	ending := matchTestContext()
	ending.Series.EndOn = NewDate(2026, time.September, 30)
	decision, ok := Decide(matchTestRentCharge(), []MatchContext{ending}, nil)
	require.True(t, ok)
	require.True(t, decision.AdvancePointerTo.IsZero())
}

func TestAnOrdinaryChargeMatchesNoSeries(t *testing.T) {
	coffee := matchTestRentCharge()
	coffee.StatementName = "STARBUCKS"
	coffee.Payee = "Coffee"
	_, ok := Decide(coffee, []MatchContext{matchTestContext()}, nil)
	require.False(t, ok)
}

func TestAPausedOrDeletedSeriesTakesNoCharges(t *testing.T) {
	paused := matchTestContext()
	paused.Series.IsActive = false
	_, ok := Decide(matchTestRentCharge(), []MatchContext{paused}, nil)
	require.False(t, ok)

	deleted := matchTestContext()
	deleted.Series.IsDeleted = true
	_, ok = Decide(matchTestRentCharge(), []MatchContext{deleted}, nil)
	require.False(t, ok)
}

func TestTheBestScoringSeriesWinsWhenTwoCouldClaimTheCharge(t *testing.T) {
	exact := matchTestContext()
	exact.Series.ID = "ser-exact"

	looser := matchTestContext()
	looser.Series.ID = "ser-loose"
	looser.Series.Amount = MustFromString("-1450.00")
	band, err := BetweenAmounts(MustFromString("-1600"), MustFromString("-1400"))
	require.NoError(t, err)
	looser.Tolerance = band

	decision, ok := Decide(matchTestRentCharge(), []MatchContext{looser, exact}, nil)
	require.True(t, ok)
	require.Equal(t, ID("ser-exact"), decision.SeriesID)
}

func TestAPlaceholderForADifferentOccurrenceIsNotTheOneUpgraded(t *testing.T) {
	august := matchTestPlaceholder()
	august.ID = "ph-aug"
	august.DueOn = NewDate(2026, time.August, 1)

	decision, ok := Decide(matchTestRentCharge(), []MatchContext{matchTestContext()}, []MatchPlaceholder{august})
	require.True(t, ok)
	require.Equal(t, OutcomeStampSeries, decision.Outcome)
	require.Empty(t, decision.PlaceholderID)
}

func TestAnUnsetToleranceIsReadAsTheExactBand(t *testing.T) {
	// A series with no stored tolerance must not silently accept any amount.
	var unset AmountTolerance
	require.True(t, unset.Accepts(MustFromString("-1500.00"), MustFromString("-1500.00"), nil))
	require.False(t, unset.Accepts(MustFromString("-1500.00"), MustFromString("-1500.01"), nil))
}

const powerText = "NORTHWIND POWER AUTOPAY"

// matchTestPowerContext is a linked power reminder whose estimate is far from
// what the biller actually charges, with its pointer left on a past-due July
// slot. Every slot is on the 3rd; each bill is due a few days later, inside
// its slot's match window.
func matchTestPowerContext() MatchContext {
	return MatchContext{
		Series: Series{
			ID:          "ser-power",
			AccountID:   "acct-1",
			Description: powerText,
			Amount:      MustFromString("-300.00"),
			Recurrence:  EveryMonth(3),
			StartOn:     NewDate(2026, time.January, 3),
			NextDueOn:   NewDate(2026, time.July, 3),
			Currency:    "USD",
			IsActive:    true,
		},
		Tolerance: AutoAmount(),
		Bills: []BillConnect{
			{DueOn: NewDate(2026, time.July, 6), Amount: MustFromString("-85.00"), Paid: true},
			{DueOn: NewDate(2026, time.August, 6), Amount: MustFromString("-90.00"), Paid: true},
			{DueOn: NewDate(2026, time.September, 7), Amount: MustFromString("-105.00")},
		},
	}
}

func matchTestPowerCharge(amount string, on Date) Transaction {
	charge := matchTestCharge(amount, on)
	charge.StatementName, charge.Payee = powerText, "Power"
	return charge
}

func TestAChargeIsWeighedAgainstTheBillItsSlotShows(t *testing.T) {
	context := matchTestPowerContext()
	require.Equal(t, "-85.00", context.Expected(NewDate(2026, time.July, 3)).String())
	require.Equal(t, "-90.00", context.Expected(NewDate(2026, time.August, 3)).String())
	require.Equal(t, "-300.00", context.Expected(NewDate(2026, time.October, 3)).String(),
		"a slot with no bill is still weighed against the estimate")
}

func TestAChargeForTheBilledAmountSettlesThePastDueSlotItBelongsTo(t *testing.T) {
	// The estimate's auto band is 270.00 to 330.00; the charge is the July
	// bill's figure, nowhere near it.
	charge := matchTestPowerCharge("-85.00", NewDate(2026, time.July, 7))

	decision, ok := Decide(charge, []MatchContext{matchTestPowerContext()}, nil)
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.July, 3), decision.OccurrenceOn)
	require.Equal(t, OutcomeStampSeries, decision.Outcome)
	require.Equal(t, NewDate(2026, time.August, 3), decision.AdvancePointerTo)

	unlinked := matchTestPowerContext()
	unlinked.Bills = nil
	_, ok = Decide(charge, []MatchContext{unlinked}, nil)
	require.False(t, ok, "without the bill the estimate rejects it")
}

func TestEachSlotIsSettledByTheChargeForItsOwnBill(t *testing.T) {
	// July is paid; the August charge settles August, not the older slot
	// still waiting on July's bill.
	context := matchTestPowerContext()
	context.SettledOn = []Date{NewDate(2026, time.July, 3)}

	decision, ok := Decide(matchTestPowerCharge("-90.00", NewDate(2026, time.August, 6)),
		[]MatchContext{context}, nil)
	require.True(t, ok)
	require.Equal(t, NewDate(2026, time.August, 3), decision.OccurrenceOn)

	// September's figure is outside August's band: 90.00 padded by 10%.
	require.False(t, IsCandidate(context,
		matchTestPowerCharge("-105.00", NewDate(2026, time.August, 6)), NewDate(2026, time.August, 3)))
}

func TestAnExactSeriesMatchesItsBillToTheCent(t *testing.T) {
	context := matchTestPowerContext()
	context.Tolerance = ExactAmount()
	on := NewDate(2026, time.August, 3)
	require.True(t, IsCandidate(context, matchTestPowerCharge("-90.00", NewDate(2026, time.August, 5)), on))
	require.False(t, IsCandidate(context, matchTestPowerCharge("-300.00", NewDate(2026, time.August, 5)), on))
}

func TestTheUsersOverrideIsWhatThePointerSlotIsWeighedAgainst(t *testing.T) {
	// The reminder shows the override over July's bill, so the matcher does.
	context := matchTestPowerContext()
	context.Tolerance = ExactAmount()
	context.Series.OverrideNextAmount = MustFromString("-88.00")
	context.Series.HasOverrideNextAmount = true
	on := NewDate(2026, time.July, 3)
	require.True(t, IsCandidate(context, matchTestPowerCharge("-88.00", NewDate(2026, time.July, 6)), on))
	require.False(t, IsCandidate(context, matchTestPowerCharge("-85.00", NewDate(2026, time.July, 6)), on))
}
