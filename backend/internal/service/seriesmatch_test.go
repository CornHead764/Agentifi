package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The persistence half of series matching. The algorithm is tested in domain.

func seriesFixture(mods ...func(*SeriesRow)) SeriesRow {
	row := SeriesRow{
		ID:            uuid.UUID{1},
		AccountID:     uuid.UUID{2},
		Description:   "STREAMSVC.COM",
		Amount:        domain.MustFromString("-15.00"),
		StartOn:       on(2026, time.January, 15),
		MatchCriteria: string(domain.CriteriaExact),
		Alias:         string(domain.AliasEveryMonth),
		Frequency:     string(domain.FreqMonthly),
		Interval:      1,
		ByMonthDay:    []int{15},
	}
	for _, mod := range mods {
		mod(&row)
	}
	return row
}

func TestTheStoredRRuleFieldsBecomeARecurrence(t *testing.T) {
	recurrence := ToRecurrence(seriesFixture())
	require.Equal(t, domain.AliasEveryMonth, recurrence.Alias)
	require.Equal(t, domain.FreqMonthly, recurrence.Frequency)
	require.Equal(t, []int{15}, recurrence.ByMonthDay)
}

func TestMatchCriteriaMapToTheFourBands(t *testing.T) {
	require.Equal(t, domain.CriteriaExact, ToTolerance(seriesFixture()).Criteria)
	require.Equal(t, domain.CriteriaAny, ToTolerance(seriesFixture(func(s *SeriesRow) {
		s.MatchCriteria = string(domain.CriteriaAny)
	})).Criteria)

	banded := ToTolerance(seriesFixture(func(s *SeriesRow) {
		s.MatchCriteria = string(domain.CriteriaRange)
		s.MatchAmountMin, s.HasMatchMin = domain.MustFromString("-20"), true
		s.MatchAmountMax, s.HasMatchMax = domain.MustFromString("-10"), true
	}))
	expected, err := domain.BetweenAmounts(
		domain.MustFromString("-20"), domain.MustFromString("-10"))
	require.NoError(t, err)
	require.Equal(t, expected, banded)
}

func TestARangeMissingABoundFallsBackToAutoNotToExact(t *testing.T) {
	// Stored data that cannot be honoured must widen, never narrow.
	tolerance := ToTolerance(seriesFixture(func(s *SeriesRow) {
		s.MatchCriteria = string(domain.CriteriaRange)
	}))
	require.Equal(t, domain.CriteriaAuto, tolerance.Criteria)
}

func TestThePointerDoesNotMoveOnABackfill(t *testing.T) {
	// Advancing here silently skips the payment the series is still waiting for.
	decision := domain.MatchDecision{
		Outcome:      domain.OutcomeBackfill,
		SeriesID:     "s",
		ChargeID:     "c",
		OccurrenceOn: on(2026, time.March, 15),
	}
	require.Equal(t, PointerUpdate{}, PointerFields(decision, seriesFixture()))
}

func TestRunningOutOfOccurrencesDeactivatesTheSeries(t *testing.T) {
	decision := domain.MatchDecision{
		Outcome:      domain.OutcomeStampSeries,
		SeriesID:     "s",
		ChargeID:     "c",
		OccurrenceOn: on(2026, time.March, 15),
	}
	update := PointerFields(decision, seriesFixture())
	require.True(t, update.Applies)
	require.True(t, update.NextDueOn.IsZero())
	require.True(t, update.Deactivate)
}

func TestAFulfilledOneOffOverrideIsCleared(t *testing.T) {
	// Left set, it drags the next occurrence onto the overridden date too.
	decision := domain.MatchDecision{
		Outcome:          domain.OutcomeStampSeries,
		SeriesID:         "s",
		ChargeID:         "c",
		OccurrenceOn:     on(2026, time.March, 18),
		AdvancePointerTo: on(2026, time.April, 15),
	}
	update := PointerFields(decision, seriesFixture(func(s *SeriesRow) {
		s.OverrideNextDueOn = on(2026, time.March, 18)
	}))
	require.Equal(t, on(2026, time.April, 15), update.NextDueOn)
	require.False(t, update.Deactivate)
	require.True(t, update.ClearOverride)
}

func TestAnUpgradedPlaceholderTakesTheBankFactsAndKeepsTheUserEdits(t *testing.T) {
	charge := store.Transaction{
		ID:            uuid.UUID{9},
		AccountID:     uuid.UUID{2},
		Date:          on(2026, time.March, 17),
		Amount:        domain.MustFromString("-16.00"),
		StatementName: "STREAMSVC.COM 800-555-0100",
		ExternalID:    "ext-1",
		Source:        domain.SourceSync,
	}
	decision := domain.MatchDecision{
		Outcome:        domain.OutcomeUpgradePlaceholder,
		SeriesID:       "s",
		ChargeID:       "9",
		OccurrenceOn:   on(2026, time.March, 15),
		PlaceholderID:  "8",
		AdoptAmount:    domain.MustFromString("-16.00"),
		HasAdoptAmount: true,
	}
	fields := AbsorbFrom(charge, decision)
	require.Equal(t, "STREAMSVC.COM 800-555-0100", fields.StatementName)
	require.Equal(t, "-16.00", fields.Amount.String())
	require.True(t, fields.HasAmount)
	require.Equal(t, on(2026, time.March, 15), fields.SeriesDueOn)
	// The payee, category and notes are not fields of this struct, so nothing
	// can copy them.
}

func TestWordingIsLearnedOnceAndOnlyWhenItIsNew(t *testing.T) {
	series := seriesFixture(func(s *SeriesRow) {
		s.LearnedDescriptions = []string{"STREAMSVC.COM 800"}
	})
	_, learned := LearnedDescriptionsAfter(series,
		store.Transaction{StatementName: "STREAMSVC.COM 800"})
	require.False(t, learned)

	_, learned = LearnedDescriptionsAfter(series, store.Transaction{StatementName: "STREAMSVC.COM"})
	require.False(t, learned)

	wordings, learned := LearnedDescriptionsAfter(series,
		store.Transaction{StatementName: "STRM DIGITAL"})
	require.True(t, learned)
	require.Equal(t, []string{"STREAMSVC.COM 800", "STRM DIGITAL"}, wordings)
}

// matchOne runs the matcher over one charge, as a settle pass of one row does.
func matchOne(t *testing.T, spaceID store.SpaceID, charge store.Transaction) (MatchOutcome, bool, error) {
	t.Helper()
	outcomes, err := NewSeriesMatcher(db(t)).MatchAll(t.Context(), spaceID, []store.Transaction{charge})
	if err != nil || len(outcomes) == 0 {
		return MatchOutcome{}, false, err
	}
	return outcomes[0], true, nil
}

func loadSeries(t *testing.T, spaceID store.SpaceID, id uuid.UUID) SeriesRow {
	t.Helper()
	row, err := NewSeriesMatcher(db(t)).GetSeries(t.Context(), spaceID, id)
	require.NoError(t, err)
	return row
}

func TestAChargeWithNoPlaceholderIsStampedAndMovesThePointer(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account)
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
		withStatementName("STREAMSVC.COM"))

	outcome, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.OutcomeStampSeries, outcome.Outcome)
	require.Equal(t, charge.ID, outcome.SurvivingID)

	stored := reload(t, spaceID, charge.ID)
	require.Equal(t, series.ID, stored.SeriesID)
	require.Equal(t, on(2026, time.March, 15), stored.SeriesDueOn)
	require.Equal(t, on(2026, time.April, 15), loadSeries(t, spaceID, series.ID).NextDueOn)
}

func TestTwoMonthsOfOneBillInOneBatchFillTwoSlots(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account)
	march := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
		withStatementName("STREAMSVC.COM"))
	april := newTransaction(t, spaceID, account, on(2026, time.April, 16), "-15.00",
		withStatementName("STREAMSVC.COM"))

	outcomes, err := NewSeriesMatcher(db(t)).MatchAll(t.Context(), spaceID,
		[]store.Transaction{*march, *april})
	require.NoError(t, err)
	require.Len(t, outcomes, 2)

	require.Equal(t, on(2026, time.March, 15), reload(t, spaceID, march.ID).SeriesDueOn)
	require.Equal(t, on(2026, time.April, 15), reload(t, spaceID, april.ID).SeriesDueOn)
	require.Equal(t, on(2026, time.May, 15), loadSeries(t, spaceID, series.ID).NextDueOn)
}

func TestAPlaceholderTheBatchAlreadyUpgradedIsNotUpgradedAgain(t *testing.T) {
	// The first charge absorbs the placeholder. A second charge in the same
	// batch reading the placeholders as they were before would absorb into it
	// too, overwriting the first charge's bank facts and retiring a real row.
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account,
		seriesCriteria(domain.CriteriaAuto), seriesDescription("STREAMSVC COM DIGITAL"))
	placeholder := newTransaction(t, spaceID, account, on(2026, time.March, 15), "-15.00",
		withStatementName("STREAMSVC COM DIGITAL"), withSeries(series.ID, on(2026, time.March, 15)),
		withEstimate("estimated"))
	first := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-16.00",
		withStatementName("STREAMSVC COM DIGITAL"), withExternalID("ext-1"))
	second := newTransaction(t, spaceID, account, on(2026, time.March, 17), "-16.00",
		withStatementName("STREAMSVC COM DIGITAL"), withExternalID("ext-2"))

	_, err := NewSeriesMatcher(db(t)).MatchAll(t.Context(), spaceID,
		[]store.Transaction{*first, *second})
	require.NoError(t, err)

	require.Equal(t, "ext-1", reload(t, spaceID, placeholder.ID).ExternalID)
	require.False(t, reload(t, spaceID, second.ID).IsDeleted)
}

func TestAChargeUpgradesThePlaceholderInPlace(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	category := newCategory(t, spaceID, "Entertainment", uuid.Nil)
	series := newSeries(t, spaceID, account,
		seriesCriteria(domain.CriteriaAuto), seriesDescription("STREAMSVC COM DIGITAL"))
	placeholder := newTransaction(t, spaceID, account, on(2026, time.March, 15), "-15.00",
		withStatementName("STREAMSVC COM DIGITAL"), withPayee("Example Streaming"),
		withCategory(category.ID), withSeries(series.ID, on(2026, time.March, 15)),
		withEstimate("estimated"))
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 17), "-16.00",
		withStatementName("STREAMSVC COM DIGITAL 8005550100"), withExternalID("ext-1"))

	outcome, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.OutcomeUpgradePlaceholder, outcome.Outcome)
	require.Equal(t, placeholder.ID, outcome.SurvivingID)
	require.Equal(t, charge.ID, outcome.RetiredID)

	// The placeholder keeps its id, its category and its payee, and takes the
	// bank's amount, wording, date and external id.
	survivor := reload(t, spaceID, placeholder.ID)
	require.Equal(t, "-16.00", survivor.Amount.String())
	require.Equal(t, "STREAMSVC COM DIGITAL 8005550100", survivor.StatementName)
	require.Equal(t, "ext-1", survivor.ExternalID)
	require.Equal(t, on(2026, time.March, 17), survivor.Date)
	require.Equal(t, "", survivor.EstimateStatus)
	require.Equal(t, category.ID, survivor.CategoryID)
	require.Equal(t, "Example Streaming", survivor.Payee)

	// The duplicate is gone from every view, and its external id moved to the
	// survivor: the bank's id must name exactly one row or a re-import stores
	// the charge twice.
	retired := reload(t, spaceID, charge.ID)
	require.True(t, retired.IsDeleted)
	require.Equal(t, "", retired.ExternalID)

	stored := loadSeries(t, spaceID, series.ID)
	require.Equal(t, on(2026, time.April, 15), stored.NextDueOn)
	require.Contains(t, stored.LearnedDescriptions, "STREAMSVC COM DIGITAL 8005550100")
}

func TestRetiringAnUpgradedChargeReleasesItsTransferPartner(t *testing.T) {
	// The retired charge goes through the same delete as any other row, so a
	// pair it was half of does not leave the other leg holding a dead token.
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	other := newAccount(t, spaceID, "Savings")
	series := newSeries(t, spaceID, account,
		seriesCriteria(domain.CriteriaAuto), seriesDescription("STREAMSVC COM DIGITAL"))
	newTransaction(t, spaceID, account, on(2026, time.March, 15), "-15.00",
		withStatementName("STREAMSVC COM DIGITAL"), withSeries(series.ID, on(2026, time.March, 15)),
		withEstimate("estimated"))
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 17), "-16.00",
		withStatementName("STREAMSVC COM DIGITAL 8005550100"), withExternalID("ext-1"))
	partner := newTransaction(t, spaceID, other, on(2026, time.March, 17), "16.00")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET transfer_pair_id = $2 WHERE id IN (SELECT value FROM json_each($1))`,
		[]uuid.UUID{charge.ID, partner.ID}, uuid.New())
	require.NoError(t, err)

	outcome, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, charge.ID, outcome.RetiredID)

	require.Equal(t, uuid.Nil, reload(t, spaceID, partner.ID).TransferPairID)
}

func TestAnOldOccurrenceBackfillsWithoutMovingThePointer(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account, seriesNextDueOn(on(2026, time.May, 15)))
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
		withStatementName("STREAMSVC.COM"))

	outcome, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, domain.OutcomeBackfill, outcome.Outcome)
	require.Equal(t, on(2026, time.March, 15), reload(t, spaceID, charge.ID).SeriesDueOn)
	require.Equal(t, on(2026, time.May, 15), loadSeries(t, spaceID, series.ID).NextDueOn)
}

func TestAFulfilledOneOffOverrideIsClearedAgainstARealSeries(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account, seriesOverrideDueOn(on(2026, time.March, 18)))
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 18), "-15.00",
		withStatementName("STREAMSVC.COM"))

	_, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)

	stored := loadSeries(t, spaceID, series.ID)
	require.Equal(t, on(2026, time.April, 15), stored.NextDueOn)
	require.True(t, stored.OverrideNextDueOn.IsZero())
	require.False(t, stored.HasOverrideNextAmount)
}

func TestAnUnrelatedChargeMatchesNothing(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	newSeries(t, spaceID, account)
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
		withStatementName("CORNER STORE 4471"))

	_, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.False(t, ok)
}

// Which rows may fill a series slot: matchableCharge. An opening balance or a
// balance adjustment is not a payment of anything.

func TestABookkeepingRowIsNotMatchedToASeries(t *testing.T) {
	for _, source := range []domain.Source{
		domain.SourceOpeningBalance, domain.SourceBalanceAdjustment,
	} {
		t.Run(string(source), func(t *testing.T) {
			spaceID := newSpace(t)
			account := newAccount(t, spaceID, "Checking")
			newSeries(t, spaceID, account)
			// Wording and amount a real charge would match on, so only the
			// source can be what refuses it.
			charge := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
				withStatementName("STREAMSVC.COM"), withSource(source))

			_, ok, err := matchOne(t, spaceID, *charge)
			require.NoError(t, err)
			require.False(t, ok, "an asset revaluation was offered a recurring bill's slot")
		})
	}
}

func TestTheSameChargeMatchesWhenItsSourceIsRealMoney(t *testing.T) {
	// The other half, so the test above is about the source and not about the
	// fixture failing to match anything at all.
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	newSeries(t, spaceID, account)
	charge := newTransaction(t, spaceID, account, on(2026, time.March, 16), "-15.00",
		withStatementName("STREAMSVC.COM"), withSource(domain.SourceSync))

	_, ok, err := matchOne(t, spaceID, *charge)
	require.NoError(t, err)
	require.True(t, ok)
}

func TestBothDirectionsAgreeOnWhichRowsAreMatchable(t *testing.T) {
	// The property the shared rule exists to hold, stated over every source.
	for _, source := range domain.AllSources {
		row := store.Transaction{Source: source}
		require.Equal(t, source.IsCashFlow(), matchableCharge(row),
			"source %s", source)
	}
	require.False(t, matchableCharge(store.Transaction{
		Source: domain.SourceSync, EstimateStatus: store.ProjectedEstimate}))
	require.False(t, matchableCharge(store.Transaction{
		Source: domain.SourceSync, SeriesID: uuid.New()}))
}
