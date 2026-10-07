package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var valToday = NewDate(2026, time.August, 21)

func TestMileageIsProjectedForwardFromTheLastReading(t *testing.T) {
	readOn := NewDate(2025, time.August, 21)
	// A year at 12,000 a year, so the reading is a year older than it looks.
	require.Equal(t, 42_000, ProjectedMileage(30_000, readOn, 12_000, true, valToday))
}

func TestAMileageWithNoAnnualRateIsSentAsRead(t *testing.T) {
	readOn := NewDate(2024, time.January, 1)
	// No rate is not a rate of zero (a car in storage).
	require.Equal(t, 30_000, ProjectedMileage(30_000, readOn, 0, false, valToday))
	require.Equal(t, 30_000, ProjectedMileage(30_000, readOn, 0, true, valToday))
}

func TestAnOdometerIsNeverProjectedBackwards(t *testing.T) {
	readOn := NewDate(2027, time.January, 1)
	require.Equal(t, 30_000, ProjectedMileage(30_000, readOn, 12_000, true, valToday))
}

func TestAnUndatedReadingIsNotAged(t *testing.T) {
	require.Equal(t, 30_000, ProjectedMileage(30_000, Date{}, 12_000, true, valToday))
}

func TestARevaluationIsTheDifferenceAndNothingElse(t *testing.T) {
	delta, moved := RevaluationAmount(MustFromString("24000.00"), MustFromString("21500.00"))
	require.True(t, moved)
	require.Equal(t, "-2500.00", delta.String())
}

func TestAnEstimateThatAgreesWritesNoRow(t *testing.T) {
	_, moved := RevaluationAmount(MustFromString("24000.00"), MustFromString("24000.00"))
	require.False(t, moved, "a zero adjustment would give the account a row per valuation run")
}

// revaluedAsset is an imported asset: its balance is a provider figure of
// 20,000 with no rows behind it.
func revaluedAsset() Account {
	return Account{
		ID: "acct-car", Name: "Car 1", Kind: KindAsset, Currency: "USD",
		ProviderBalance: MustFromString("20000.00"), HasProviderBalance: true,
		AddedOn: NewDate(2026, time.July, 1),
	}
}

// applyRevaluation writes what RevaluationOf says, as the service does.
func applyRevaluation(
	t *testing.T, account *Account, postings []Posting, estimate string, on Date, id ID,
) []Posting {
	t.Helper()
	revaluation, moved := RevaluationOf(*account, postings, MustFromString(estimate))
	require.True(t, moved)
	if revaluation.MovesAnchor {
		account.ProviderBalance = revaluation.Anchor
	}
	return append(postings, Posting{Account: *account, Txn: Transaction{
		ID: id, AccountID: account.ID, Date: on, Amount: revaluation.Adjustment,
		Source: SourceBalanceAdjustment, Currency: "USD",
	}})
}

func TestRevaluingAnAssetWithAProviderBalanceMovesItsAnchor(t *testing.T) {
	account := revaluedAsset()
	first, second := NewDate(2026, time.August, 10), NewDate(2026, time.August, 20)

	postings := applyRevaluation(t, &account, nil, "17500.00", first, "r1")
	require.Equal(t, "-2500.00", postings[0].Txn.Amount.String())
	postings = applyRevaluation(t, &account, postings, "18250.00", second, "r2")
	// The second row is measured from the first estimate, not from the import.
	require.Equal(t, "750.00", postings[1].Txn.Amount.String())

	require.Equal(t, "18250.00", AccountBalance(account, postings).String())
	require.Equal(t, "20000.00",
		LedgerBalanceAsOf(account, postings, first.AddDays(-1), DatePosted).String())
	require.Equal(t, "17500.00",
		LedgerBalanceAsOf(account, postings, second.AddDays(-1), DatePosted).String())
	require.Equal(t, "18250.00", LedgerBalanceAsOf(account, postings, second, DatePosted).String())
}

func TestRevaluingAManualAssetWritesOnlyTheRow(t *testing.T) {
	account := revaluedAsset()
	account.ProviderBalance, account.HasProviderBalance = Zero, false
	account.OpeningBalance = MustFromString("20000.00")

	revaluation, moved := RevaluationOf(account, nil, MustFromString("17500.00"))
	require.True(t, moved)
	require.Equal(t, "-2500.00", revaluation.Adjustment.String())
	require.False(t, revaluation.MovesAnchor, "a manual asset's rows are its balance")
}

func TestAnEstimateThatMatchesTheAnchorMovesNothing(t *testing.T) {
	_, moved := RevaluationOf(revaluedAsset(), nil, MustFromString("20000.00"))
	require.False(t, moved)
}

func TestAnAssetNeverValuedIsAlwaysDue(t *testing.T) {
	require.True(t, StaleValuation(Date{}, valToday, 30))
}

func TestAnAssetValuedTodayIsNotDue(t *testing.T) {
	require.False(t, StaleValuation(valToday, valToday, 30))
	require.False(t, StaleValuation(valToday.AddDays(-29), valToday, 30))
	require.True(t, StaleValuation(valToday.AddDays(-30), valToday, 30))
}

func TestAValueHistoryBecomesTheDifferencesThatReproduceIt(t *testing.T) {
	opening := MustFromString("300000.00")
	points := []ValuePoint{
		{On: NewDate(2024, time.January, 1), Value: MustFromString("310000.00")},
		{On: NewDate(2025, time.January, 1), Value: MustFromString("325000.00")},
		{On: NewDate(2026, time.January, 1), Value: MustFromString("318000.00")},
	}

	rows := ValuationAdjustments(opening, nil, points)
	require.Len(t, rows, 3)
	// Each row corrects the running balance, so the second is +15,000 rather
	// than the +25,000 a per-point calculation against the opening would give.
	require.Equal(t, "10000.00", rows[0].Amount.String())
	require.Equal(t, "15000.00", rows[1].Amount.String())
	require.Equal(t, "-7000.00", rows[2].Amount.String())
}

func TestTheHistoryLandsOnTopOfWhatTheLedgerAlreadyHas(t *testing.T) {
	opening := MustFromString("300000.00")
	existing := []DatedAmount{
		{On: NewDate(2023, time.June, 1), Amount: MustFromString("5000.00")},
	}
	points := []ValuePoint{{On: NewDate(2024, time.January, 1), Value: MustFromString("310000.00")}}

	rows := ValuationAdjustments(opening, existing, points)
	require.Len(t, rows, 1)
	require.Equal(t, "5000.00", rows[0].Amount.String())
}

func TestARowDatedAfterThePointDoesNotCountTowardsIt(t *testing.T) {
	opening := MustFromString("300000.00")
	existing := []DatedAmount{
		{On: NewDate(2025, time.June, 1), Amount: MustFromString("5000.00")},
	}
	points := []ValuePoint{{On: NewDate(2024, time.January, 1), Value: MustFromString("310000.00")}}

	rows := ValuationAdjustments(opening, existing, points)
	require.Equal(t, "10000.00", rows[0].Amount.String())
}

func TestReimportingTheSameHistoryWritesNothing(t *testing.T) {
	opening := MustFromString("300000.00")
	points := []ValuePoint{
		{On: NewDate(2024, time.January, 1), Value: MustFromString("310000.00")},
		{On: NewDate(2025, time.January, 1), Value: MustFromString("325000.00")},
	}
	first := ValuationAdjustments(opening, nil, points)

	already := make([]DatedAmount, len(first))
	copy(already, first)
	require.Empty(t, ValuationAdjustments(opening, already, points),
		"a second import of the same file must not double the history")
}

func TestPointsOutOfOrderAreReadInDateOrder(t *testing.T) {
	opening := MustFromString("300000.00")
	points := []ValuePoint{
		{On: NewDate(2025, time.January, 1), Value: MustFromString("325000.00")},
		{On: NewDate(2024, time.January, 1), Value: MustFromString("310000.00")},
	}
	rows := ValuationAdjustments(opening, nil, points)
	require.Equal(t, NewDate(2024, time.January, 1), rows[0].On)
	require.Equal(t, "10000.00", rows[0].Amount.String())
	require.Equal(t, "15000.00", rows[1].Amount.String())
}
