package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Suggested series: the pattern analysis, then the sweep over a real ledger.

var (
	suggestionAccountID = uuid.UUID{1}
	suggestionToday     = on(2026, time.April, 1)
)

type rowOption func(*Row)

func rowPayee(payee string) rowOption {
	return func(r *Row) { r.Payee = payee }
}

func rowStatementName(name string) rowOption {
	return func(r *Row) { r.StatementName = name }
}

func ledgerRow(day domain.Date, amount string, opts ...rowOption) Row {
	row := Row{
		ID:            uuid.New(),
		AccountID:     suggestionAccountID,
		StatementName: "ACME UTILITIES 4471",
		Currency:      "USD",
		Amount:        domain.MustFromString(amount),
		On:            day,
	}
	for _, opt := range opts {
		opt(&row)
	}
	return row
}

func TestAnAccentedStatementNameGroupsWithItsPlainSpelling(t *testing.T) {
	require.Equal(t, DescriptionKey("CAFE LUMIERE 0417"), DescriptionKey("CAFÉ LUMIÈRE 0418"))
}

func TestTheDisplayNameStopsAtTheVaryingTail(t *testing.T) {
	require.Equal(t, "ACME UTILITIES", DisplayNameFor(ledgerRow(on(2026, time.March, 5), "-84.00")))
	require.Equal(t, "Acme Water",
		DisplayNameFor(ledgerRow(on(2026, time.March, 5), "-84.00", rowPayee("Acme Water"))))
}

func TestARefundDoesNotJoinTheBillItReverses(t *testing.T) {
	// Mixed directions would read as a cadence twice as fast.
	out, ok := GroupKeyOf(ledgerRow(on(2026, time.March, 5), "-84.00"))
	require.True(t, ok)
	back, ok := GroupKeyOf(ledgerRow(on(2026, time.March, 6), "84.00"))
	require.True(t, ok)
	require.NotEqual(t, out, back)
}

func TestARowWithNoRecognisableWordingCannotBeGrouped(t *testing.T) {
	_, ok := GroupKeyOf(ledgerRow(on(2026, time.March, 5), "-84.00", rowStatementName("99 4471")))
	require.False(t, ok)
}

func monthlyGroup() (GroupKey, []Row) {
	rows := []Row{
		ledgerRow(on(2026, time.January, 5), "-84.00"),
		ledgerRow(on(2026, time.February, 5), "-84.00"),
		ledgerRow(on(2026, time.March, 5), "-84.00"),
	}
	key, _ := GroupKeyOf(rows[0])
	return key, rows
}

func TestThreeMonthlyChargesBecomeASuggestion(t *testing.T) {
	key, rows := monthlyGroup()
	suggestion, ok := Build(key, rows, suggestionToday, true)
	require.True(t, ok)

	require.Equal(t, domain.EveryMonth(5), suggestion.Recurrence)
	require.Equal(t, domain.SeriesBill, suggestion.Kind)
	require.Equal(t, "-84.00", suggestion.Amount.String())
	require.Equal(t, domain.CriteriaExact, suggestion.Tolerance.Criteria)
	// The name a person reads and the text a matcher compares stay separate.
	require.Equal(t, "ACME UTILITIES", suggestion.DisplayName)
	require.Equal(t, "ACME UTILITIES 4471", suggestion.Description)
}

func TestASuggestionStartsInTheFuture(t *testing.T) {
	key, rows := monthlyGroup()
	suggestion, ok := Build(key, rows, suggestionToday, true)
	require.True(t, ok)
	require.Equal(t, on(2026, time.April, 5), suggestion.StartOn)
}

func TestAnAmountThatMovesGetsTheAutoBand(t *testing.T) {
	key, rows := monthlyGroup()
	rows[len(rows)-1] = ledgerRow(on(2026, time.March, 5), "-91.00")
	suggestion, ok := Build(key, rows, suggestionToday, true)
	require.True(t, ok)
	require.Equal(t, domain.CriteriaAuto, suggestion.Tolerance.Criteria)
}

func TestTwoOccurrencesAreNotAPatternUnlessTheyAreYearly(t *testing.T) {
	key, rows := monthlyGroup()
	_, ok := Build(key, rows[:2], suggestionToday, true)
	require.False(t, ok)

	yearly := []Row{
		ledgerRow(on(2024, time.March, 5), "-84.00"),
		ledgerRow(on(2025, time.March, 5), "-84.00"),
	}
	yearlyKey, _ := GroupKeyOf(yearly[0])
	_, ok = Build(yearlyKey, yearly, on(2025, time.April, 1), true)
	require.True(t, ok)
}

func TestTwoChargesOnOneDayAreNotAZeroDayCadence(t *testing.T) {
	rows := []Row{
		ledgerRow(on(2026, time.March, 5), "-84.00"),
		ledgerRow(on(2026, time.March, 5), "-84.00"),
	}
	key, _ := GroupKeyOf(rows[0])
	_, ok := Build(key, rows, suggestionToday, true)
	require.False(t, ok)
}

func TestAPatternThatStoppedIsNoLongerAProposal(t *testing.T) {
	key, rows := monthlyGroup()
	suggestion, ok := Build(key, rows, suggestionToday, true)
	require.True(t, ok)
	require.False(t, HasGoneQuiet(suggestion, suggestionToday))
	require.True(t, HasGoneQuiet(suggestion, on(2026, time.September, 1)))
}

func TestAnExistingSeriesWithTheSameWordingCoversThePattern(t *testing.T) {
	key, rows := monthlyGroup()
	suggestion, _ := Build(key, rows, suggestionToday, true)
	sameWording := []ExistingSeries{{
		AccountID:   suggestionAccountID,
		Amount:      domain.MustFromString("-99.00"),
		Description: "ACME UTILITIES 4471",
		PeriodDays:  30.44,
	}}
	require.True(t, CoveredByExisting(suggestion, sameWording))
}

func TestAnExistingSeriesNamedDifferentlyIsCaughtByTheAmount(t *testing.T) {
	// The arm that catches a series the user called "Water" for a payee the
	// bank calls "ACME UTILITIES".
	key, rows := monthlyGroup()
	suggestion, _ := Build(key, rows, suggestionToday, true)
	renamed := []ExistingSeries{{
		AccountID:   suggestionAccountID,
		Amount:      domain.MustFromString("-84.00"),
		Description: "Water",
		PeriodDays:  30.44,
	}}
	require.True(t, CoveredByExisting(suggestion, renamed))

	// Same amount, different rhythm: two unrelated bills.
	weekly := []ExistingSeries{{
		AccountID:   suggestionAccountID,
		Amount:      domain.MustFromString("-84.00"),
		Description: "Water",
		PeriodDays:  7.0,
	}}
	require.False(t, CoveredByExisting(suggestion, weekly))
}

func TestOneCounterpartySplitAcrossTwoGroupsIsShownOnce(t *testing.T) {
	key, rows := monthlyGroup()
	payeeKeyed := make([]Row, 0, len(rows))
	for _, row := range rows {
		payeeKeyed = append(payeeKeyed, ledgerRow(row.On, "-84.00", rowPayee("Acme Utilities")))
	}
	payeeKey, _ := GroupKeyOf(payeeKeyed[0])

	first, _ := Build(key, rows, suggestionToday, true)
	second, _ := Build(payeeKey, payeeKeyed, suggestionToday, true)
	require.Len(t, Rank([]RecurringSuggestion{first, second}, 20), 1)
}

func seedMonthlyBill(
	t *testing.T, spaceID store.SpaceID, account *store.Account, opts ...txnOption,
) {
	t.Helper()
	for _, day := range []domain.Date{
		on(2026, time.January, 5), on(2026, time.February, 5), on(2026, time.March, 5),
	} {
		all := append([]txnOption{withStatementName("ACME UTILITIES 4471")}, opts...)
		newTransaction(t, spaceID, account, day, "-84.00", all...)
	}
}

func TestTheSweepFindsABillNobodyCreated(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account)

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, "ACME UTILITIES", found[0].DisplayName)
	require.Equal(t, domain.EveryMonth(5), found[0].Recurrence)
	require.Equal(t, 3, found[0].Occurrences)
	require.True(t, found[0].GeneratePlaceholders)
}

func TestASuggestionOnAConnectedAccountDoesNotMaterialize(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking", withConnection(newConnection(t, spaceID)))
	seedMonthlyBill(t, spaceID, account)

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.False(t, found[0].GeneratePlaceholders)
}

func TestADismissedSignatureStaysDismissed(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account)

	suggestions := NewSuggestions(db(t))
	found, err := suggestions.GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Len(t, found, 1)

	again, err := suggestions.GetSuggestions(t.Context(), spaceID, SuggestionQuery{
		Today:     suggestionToday,
		Dismissed: map[string]bool{found[0].Signature: true},
	})
	require.NoError(t, err)
	require.Empty(t, again)
}

func TestTheSignatureSurvivesANewChargeArriving(t *testing.T) {
	// Keyed to the group, so a fourth charge does not undo a dismissal.
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account)

	suggestions := NewSuggestions(db(t))
	before, err := suggestions.GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Len(t, before, 1)

	newTransaction(t, spaceID, account, on(2026, time.April, 5), "-84.00",
		withStatementName("ACME UTILITIES 4471"))

	after, err := suggestions.GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: on(2026, time.May, 1)})
	require.NoError(t, err)
	require.Len(t, after, 1)
	require.Equal(t, before[0].Signature, after[0].Signature)
}

func TestEverydayEatingIsNotABill(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	parent := newCategory(t, spaceID, "Food & Dining", uuid.Nil)
	lunch := newCategory(t, spaceID, "Weekday Spot", parent.ID)
	seedMonthlyBill(t, spaceID, account, withCategory(lunch.ID))

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestATransferLegIsNeverProposed(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account, withTransferPair(uuid.New()))

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestARowExcludedFromReportsIsNeverProposed(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account, func(txn *store.Transaction) {
		txn.ExcludedFromReports = true
	})

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestRowsAlreadyLinkedToASeriesAreTheAnswerNotTheQuestion(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	series := newSeries(t, spaceID, account, seriesDescription("ACME UTILITIES 4471"))
	seedMonthlyBill(t, spaceID, account, withSeries(series.ID, on(2026, time.March, 15)))

	found, err := NewSuggestions(db(t)).GetSuggestions(t.Context(), spaceID,
		SuggestionQuery{Today: suggestionToday})
	require.NoError(t, err)
	require.Empty(t, found)
}

func TestAskingAboutOneRowAnswersEvenWithNoPattern(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	txn := newTransaction(t, spaceID, account, on(2026, time.March, 5), "-84.00",
		withStatementName("ACME UTILITIES 4471"))

	suggestion, ok, err := NewSuggestions(db(t)).SuggestForTransaction(
		t.Context(), spaceID, txn.ID, suggestionToday)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 1, suggestion.Occurrences)
	require.Equal(t, 0.0, suggestion.Confidence)
	require.Equal(t, domain.EveryMonth(5), suggestion.Recurrence)
	require.Equal(t, on(2026, time.April, 5), suggestion.StartOn)
}

func TestAskingAboutARowWithHistoryUsesIt(t *testing.T) {
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	seedMonthlyBill(t, spaceID, account)
	latest := newTransaction(t, spaceID, account, on(2026, time.April, 5), "-84.00",
		withStatementName("ACME UTILITIES 4471"))

	suggestion, ok, err := NewSuggestions(db(t)).SuggestForTransaction(
		t.Context(), spaceID, latest.ID, on(2026, time.April, 20))
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, 4, suggestion.Occurrences)
	require.Greater(t, suggestion.Confidence, 0.5)
}
