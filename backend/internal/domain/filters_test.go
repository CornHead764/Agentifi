package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

var (
	filterGroceries = Category{ID: "cat-groceries", Name: "Groceries", Kind: CategoryExpense}
	filterHousehold = Category{ID: "cat-household", Name: "Household", Kind: CategoryExpense}
	filterAuto      = Category{ID: "cat-auto", Name: "Auto & Transport", Kind: CategoryExpense}
)

func filterPosting(amount string) Posting {
	return Posting{
		Txn: Transaction{
			ID:            "txn-1",
			AccountID:     "acct-1",
			Date:          NewDate(2026, time.August, 15),
			Amount:        MustFromString(amount),
			StatementName: "SQ *COFFEE 1234",
			Payee:         "Coffee",
			CategoryID:    filterGroceries.ID,
			Source:        SourceSync,
			Currency:      "USD",
		},
		Account:     Account{ID: "acct-1", Name: "Checking 1", Kind: KindCash, Currency: "USD"},
		Category:    filterGroceries,
		HasCategory: true,
	}
}

// filterSplitPosting is a $200 receipt split between groceries and household.
func filterSplitPosting(primary string) Posting {
	p := filterPosting("-200.00")
	p.Txn.Splits = []Split{
		{ID: "s-1", Amount: MustFromString("-50.00"), CategoryID: filterGroceries.ID},
		{
			ID: "s-2", Amount: MustFromString("-150.00"),
			CategoryID: filterHousehold.ID, Memo: "soap",
		},
	}
	if primary != "" {
		p.Txn.AmountPrimary, p.Txn.HasAmountPrimary = MustFromString(primary), true
	}
	return p
}

func filterUncategorized(p Posting) Posting {
	p.Txn.CategoryID = ""
	p.Category, p.HasCategory = Category{}, false
	return p
}

func filterOf(items ...FilterItem) Filter {
	return Filter{ID: "flt-1", Items: items}
}

func filterByCategory(categoryIDs ...ID) Filter {
	values := make([]string, 0, len(categoryIDs))
	for _, id := range categoryIDs {
		values = append(values, string(id))
	}
	return filterOf(FilterItem{Field: FieldCategory, Values: values})
}

func filterMatches(f Filter, p Posting) bool {
	return Matches(f, p, Facets{}, DateEffective)
}

// matchedAmount is the sum of the parts MatchingParts hands a watchlist.
func matchedAmount(f Filter, p Posting, facets Facets, mode DateMode) Money {
	return Sum(MatchingParts(f, p, facets, mode), func(m Match) Money { return m.Amount })
}

func TestAFilterWithNoItemsMatchesEveryRow(t *testing.T) {
	require.True(t, filterMatches(filterOf(), filterPosting("-25.00")))
}

func TestValuesWithinOneFacetAreAnOr(t *testing.T) {
	wide := filterByCategory(filterGroceries.ID, filterHousehold.ID)
	require.True(t, filterMatches(wide, filterPosting("-25.00")))
}

func TestDifferentFacetsAreAnAnd(t *testing.T) {
	narrow := filterOf(
		FilterItem{Field: FieldCategory, Values: []string{string(filterGroceries.ID)}},
		FilterItem{Field: FieldAccount, Values: []string{"acct-other"}},
	)
	require.False(t, filterMatches(narrow, filterPosting("-25.00")))
}

func TestANegatedItemExcludesAllOfItsValues(t *testing.T) {
	without := filterOf(FilterItem{
		Field:   FieldCategory,
		Values:  []string{string(filterGroceries.ID), string(filterHousehold.ID)},
		Negated: true,
	})
	require.False(t, filterMatches(without, filterPosting("-25.00")))

	elsewhere := filterPosting("-25.00")
	elsewhere.Txn.CategoryID, elsewhere.Category = filterAuto.ID, filterAuto
	require.True(t, filterMatches(without, elsewhere))
}

func TestAnUncategorizedRowMatchesOnlyAFilterThatAsksForIt(t *testing.T) {
	row := filterUncategorized(filterPosting("-25.00"))
	require.False(t, filterMatches(filterByCategory(filterGroceries.ID), row))

	asking := filterOf(FilterItem{Field: FieldIsUncategorized, State: true, HasState: true})
	require.True(t, filterMatches(asking, row))
}

func TestUnreviewedAndUncategorizedTakesInUndeterminedRowsAndNoFiledOnes(t *testing.T) {
	unfiled := filterOf(
		FilterItem{Field: FieldIsReviewed, State: false, HasState: true},
		FilterItem{Field: FieldIsUncategorized, State: true, HasState: true},
	)

	filed := filterPosting("1500.00")
	require.False(t, Matches(unfiled, filed, Facets{}, DateEffective))

	open := filterUncategorized(filterPosting("-25.00"))
	require.True(t, Matches(unfiled, open, Facets{HasCategorySuggestion: true}, DateEffective))
	require.True(t, Matches(unfiled, open, Facets{CategoryChecked: true}, DateEffective))

	open.Txn.IsReviewed = true
	require.False(t, Matches(unfiled, open, Facets{}, DateEffective))
}

func TestAPairedTransferLegIsNeitherUncategorizedNorUndetermined(t *testing.T) {
	// A card's autopay arrives as one leg of a pair, with no category by
	// design. It is not waiting for one, so neither facet may offer it.
	leg := filterUncategorized(filterPosting("-250.00"))
	leg.Txn.TransferPairID = "pair-1"
	checked := Facets{CategoryChecked: true}

	uncategorized := filterOf(FilterItem{Field: FieldIsUncategorized, State: true, HasState: true})
	undetermined := filterOf(
		FilterItem{Field: FieldIsCategoryUndetermined, State: true, HasState: true})
	require.False(t, Matches(uncategorized, leg, checked, DateEffective))
	require.False(t, Matches(undetermined, leg, checked, DateEffective))
	require.False(t, leg.Txn.IsUncategorized())

	settled := filterOf(FilterItem{Field: FieldIsUncategorized, State: false, HasState: true})
	require.True(t, Matches(settled, leg, checked, DateEffective))

	leg.Txn.TransferPairID = ""
	require.True(t, Matches(uncategorized, leg, checked, DateEffective))
	require.True(t, Matches(undetermined, leg, checked, DateEffective))
}

func TestThePayeeFacetReadsTheEditableNameCaseInsensitively(t *testing.T) {
	payees := filterOf(FilterItem{Field: FieldPayee, Values: []string{"coffee"}})
	require.True(t, filterMatches(payees, filterPosting("-25.00")))
}

func TestARowNobodyHasRenamedFallsBackToTheStatementName(t *testing.T) {
	unnamed := filterPosting("-25.00")
	unnamed.Txn.Payee = ""
	payees := filterOf(FilterItem{Field: FieldPayee, Values: []string{"SQ *COFFEE 1234"}})
	require.True(t, filterMatches(payees, unnamed))
}

func TestSearchStillFindsARowByWhatTheBankCalledIt(t *testing.T) {
	// A renamed row stays findable by the bank's name.
	renamed := filterPosting("-25.00")
	require.True(t, filterMatches(filterOf(FilterItem{
		Field: FieldText, Text: "sq *coffee",
	}), renamed))
}

func TestSearchReadsSplitMemos(t *testing.T) {
	soap := filterOf(FilterItem{Field: FieldText, Text: "soap"})
	require.True(t, filterMatches(soap, filterSplitPosting("")))
}

func TestABlankSearchNarrowsRatherThanMatchingEverything(t *testing.T) {
	// An empty search box means no item, not an item that matches all.
	require.False(t, filterMatches(
		filterOf(FilterItem{Field: FieldText, Text: "  "}), filterPosting("-25.00")))
}

func TestATagOnTheTransactionMatches(t *testing.T) {
	tagged := filterPosting("-25.00")
	tagged.Txn.TagIDs = []ID{"tag-holiday"}
	require.True(t, filterMatches(
		filterOf(FilterItem{Field: FieldTag, Values: []string{"tag-holiday"}}), tagged))
}

func TestATagOnOneSplitMatches(t *testing.T) {
	tagged := filterPosting("-200.00")
	tagged.Txn.Splits = []Split{
		{ID: "s-1", Amount: MustFromString("-200.00"), TagIDs: []ID{"tag-holiday"}},
	}
	require.True(t, filterMatches(
		filterOf(FilterItem{Field: FieldTag, Values: []string{"tag-holiday"}}), tagged))
}

func TestTheAccountFacetMatchesById(t *testing.T) {
	accounts := filterOf(FilterItem{Field: FieldAccount, Values: []string{"acct-savings"}})
	savings := filterPosting("-25.00")
	savings.Account.ID, savings.Txn.AccountID = "acct-savings", "acct-savings"

	require.True(t, filterMatches(accounts, savings))
	require.False(t, filterMatches(accounts, filterPosting("-25.00")))
}

func TestAnUnknownFacetNarrowsRatherThanWidens(t *testing.T) {
	// A caller that cannot supply flags must not have every row pass.
	flagged := filterOf(FilterItem{Field: FieldFlag, Values: []string{"red"}})
	require.False(t, filterMatches(flagged, filterPosting("-25.00")))
	require.True(t, Matches(flagged, filterPosting("-25.00"),
		Facets{UserFlag: "red"}, DateEffective))
}

func TestTheAdvancedRadioPairsReadTheRowsOwnFlags(t *testing.T) {
	reviewed := filterOf(FilterItem{Field: FieldIsReviewed, State: true, HasState: true})

	yes := filterPosting("-25.00")
	yes.Txn.IsReviewed = true
	require.True(t, filterMatches(reviewed, yes))
	require.False(t, filterMatches(reviewed, filterPosting("-25.00")))
}

func TestAWaitingSuggestionIsAFacetBothWaysRound(t *testing.T) {
	suggested := filterOf(FilterItem{Field: FieldHasCategorySuggestion, State: true, HasState: true})
	without := filterOf(FilterItem{Field: FieldHasCategorySuggestion, State: false, HasState: true})
	row := filterPosting("-25.00")

	require.True(t, Matches(suggested, row, Facets{HasCategorySuggestion: true}, DateEffective))
	require.False(t, Matches(suggested, row, Facets{}, DateEffective))
	require.True(t, Matches(without, row, Facets{}, DateEffective))
	require.False(t, Matches(without, row, Facets{HasCategorySuggestion: true}, DateEffective))
	require.NoError(t, suggested.Validate())
}

func TestAnAttachmentIsAFacetBothWaysRound(t *testing.T) {
	attached := filterOf(FilterItem{Field: FieldHasAttachment, State: true, HasState: true})
	bare := filterOf(FilterItem{Field: FieldHasAttachment, State: false, HasState: true})
	row := filterPosting("-25.00")

	require.True(t, Matches(attached, row, Facets{HasAttachment: true}, DateEffective))
	require.False(t, Matches(attached, row, Facets{}, DateEffective))
	require.True(t, Matches(bare, row, Facets{}, DateEffective))
	require.False(t, Matches(bare, row, Facets{HasAttachment: true}, DateEffective))
	require.NoError(t, attached.Validate())
}

func TestASplitTakesItsRowsAttachment(t *testing.T) {
	split := filterSplitPosting("")
	attached := filterOf(FilterItem{Field: FieldHasAttachment, State: true, HasState: true})
	withFile := Facets{HasAttachment: true}

	require.True(t, Matches(attached, split, withFile, DateEffective))
	require.Nil(t, PartialParts(attached, split, withFile, DateEffective), "every split has it")
	require.False(t, Matches(attached, split, Facets{}, DateEffective))

	groceries := filterOf(
		FilterItem{Field: FieldHasAttachment, State: true, HasState: true},
		FilterItem{Field: FieldCategory, Values: []string{string(filterGroceries.ID)}},
	)
	parts := PartialParts(groceries, split, withFile, DateEffective)
	require.Len(t, parts, 1)
	require.Equal(t, ID("s-1"), parts[0].Split.ID)
}

func TestUnreviewedAndSuggestedNarrowTogether(t *testing.T) {
	// The register's "Unreviewed, with a suggestion" quick filter: two items in
	// one group, so both have to hold.
	both := filterOf(
		FilterItem{Field: FieldIsReviewed, State: false, HasState: true},
		FilterItem{Field: FieldHasCategorySuggestion, State: true, HasState: true},
	)
	reviewed := filterPosting("-25.00")
	reviewed.Txn.IsReviewed = true
	waiting := Facets{HasCategorySuggestion: true}

	require.True(t, Matches(both, filterPosting("-25.00"), waiting, DateEffective))
	require.False(t, Matches(both, reviewed, waiting, DateEffective))
	require.False(t, Matches(both, filterPosting("-25.00"), Facets{}, DateEffective))
}

func TestARuleCannotMatchOnAVerdictOrASuggestion(t *testing.T) {
	require.True(t, RuleCannotMatch(FieldIsBillOrSubscription))
	require.True(t, RuleCannotMatch(FieldHasCategorySuggestion))
	require.True(t, RuleCannotMatch(FieldHasAttachment))
	require.False(t, RuleCannotMatch(FieldIsReviewed))
}

func TestTheTwoExclusionFlagsAreSeparateFacets(t *testing.T) {
	reports := filterOf(FilterItem{
		Field: FieldIsExcludedFromReports, State: true, HasState: true,
	})
	plan := filterOf(FilterItem{
		Field: FieldIsExcludedFromSpendingPlan, State: true, HasState: true,
	})

	row := filterPosting("-25.00")
	row.Txn.ExcludedFromReports = true
	require.True(t, filterMatches(reports, row))
	require.False(t, filterMatches(plan, row))
}

func TestTheAmountRangeIsInclusiveAndReadsMagnitudes(t *testing.T) {
	band := filterOf(FilterItem{
		Field:      FieldAmount,
		Minimum:    MustFromString("25"),
		HasMinimum: true,
		Maximum:    MustFromString("100"),
		HasMaximum: true,
	})
	require.True(t, filterMatches(band, filterPosting("-25.00")))
	require.True(t, filterMatches(band, filterPosting("-100.00")))
	require.False(t, filterMatches(band, filterPosting("-100.02")))
}

func TestAnOpenEndedRangeOnlyBoundsTheEndItWasGiven(t *testing.T) {
	over := filterOf(FilterItem{
		Field: FieldAmount, Minimum: MustFromString("500"), HasMinimum: true,
	})
	require.True(t, filterMatches(over, filterPosting("-900.00")))
	require.False(t, filterMatches(over, filterPosting("-499.99")))
}

func TestTheDateRangeReadsWhicheverDateTheCallerNames(t *testing.T) {
	charge := filterPosting("-25.00")
	charge.Txn.EffectiveDate = NewDate(2026, time.September, 5)
	september := filterOf(FilterItem{
		Field: FieldDate,
		Start: NewDate(2026, time.September, 1),
		End:   NewDate(2026, time.September, 30),
	})

	require.True(t, Matches(september, charge, Facets{}, DateEffective))
	require.False(t, Matches(september, charge, Facets{}, DatePosted))
}

func TestOnlyTheMatchingAllocationOfASplitRowCounts(t *testing.T) {
	// The whole receipt is $200; a groceries watchlist may only see $50.
	row := filterSplitPosting("")
	require.Equal(t, "-50.00", matchedAmount(
		filterByCategory(filterGroceries.ID), row, Facets{}, DateEffective).String())
	require.Equal(t, "-150.00", matchedAmount(
		filterByCategory(filterHousehold.ID), row, Facets{}, DateEffective).String())
}

func TestASplitRowMatchingNothingContributesNothing(t *testing.T) {
	row := filterSplitPosting("")
	require.False(t, filterMatches(filterByCategory(filterAuto.ID), row))
	require.Equal(t, "0.00", matchedAmount(
		filterByCategory(filterAuto.ID), row, Facets{}, DateEffective).String())
}

func TestAnUnsplitRowContributesItsWholeAmount(t *testing.T) {
	require.Equal(t, "-25.00", matchedAmount(
		filterByCategory(filterGroceries.ID),
		filterPosting("-25.00"), Facets{}, DateEffective).String())
}

func TestASplitsShareIsScaledByTheRowsOwnConversion(t *testing.T) {
	// Splits are stored native; every aggregate reads primary.
	row := filterSplitPosting("-400.00")
	require.Equal(t, "-100.00", matchedAmount(
		filterByCategory(filterGroceries.ID), row, Facets{}, DateEffective).String())
	require.Equal(t, "-400.00", matchedAmount(
		filterByCategory(filterGroceries.ID, filterHousehold.ID),
		row, Facets{}, DateEffective).String())
}

func TestSelectKeepsTheOrderItWasGiven(t *testing.T) {
	first := filterPosting("-25.00")
	first.Txn.ID = "t-1"
	second := filterPosting("-25.00")
	second.Txn.ID, second.Txn.CategoryID, second.Category = "t-2", filterAuto.ID, filterAuto

	selected := Select(filterByCategory(filterGroceries.ID),
		[]Posting{first, second}, nil, DateEffective)
	require.Len(t, selected, 1)
	require.Equal(t, ID("t-1"), selected[0].Txn.ID)
}

func TestFlagsAreLookedUpPerTransaction(t *testing.T) {
	flagged := filterOf(FilterItem{Field: FieldFlag, Values: []string{"red"}})
	plain, red := filterPosting("-25.00"), filterPosting("-25.00")
	plain.Txn.ID, red.Txn.ID = "t-1", "t-2"

	selected := Select(flagged, []Posting{plain, red},
		map[ID]Facets{"t-2": {UserFlag: "red"}}, DateEffective)
	require.Len(t, selected, 1)
	require.Equal(t, ID("t-2"), selected[0].Txn.ID)
}

// The rules builder's + button: two groups must widen what a filter catches,
// or every multi-group rule silently matches nothing.
func filterCoffeeOrBigGroceryRun() Filter {
	return Filter{
		ID: "f-groups",
		Items: []FilterItem{
			{Field: FieldPayee, Values: []string{"Coffee"}},
			{Field: FieldCategory, Values: []string{string(filterGroceries.ID)}, GroupIndex: 1},
			{Field: FieldAmount, Minimum: MustFromString("100"), HasMinimum: true, GroupIndex: 1},
		},
	}
}

func TestARowMatchingOnlyTheFirstGroupMatches(t *testing.T) {
	row := filterUncategorized(filterPosting("-4.75"))
	require.True(t, filterMatches(filterCoffeeOrBigGroceryRun(), row))
}

func TestARowMatchingEveryItemOfTheSecondGroupMatches(t *testing.T) {
	row := filterPosting("-180.00")
	row.Txn.Payee = "Fresh Market"
	require.True(t, filterMatches(filterCoffeeOrBigGroceryRun(), row))
}

func TestARowMatchingOnlyPartOfTheSecondGroupDoesNot(t *testing.T) {
	// Groceries, but only $12 — the group's own items are still an AND.
	row := filterPosting("-12.00")
	row.Txn.Payee = "Fresh Market"
	require.False(t, filterMatches(filterCoffeeOrBigGroceryRun(), row))
}

func TestARowMatchingNeitherGroupDoesNot(t *testing.T) {
	row := filterUncategorized(filterPosting("-40.00"))
	row.Txn.Payee = "Shell"
	require.False(t, filterMatches(filterCoffeeOrBigGroceryRun(), row))
}

func TestAnUngroupedFilterIsStillAPlainConjunction(t *testing.T) {
	both := Filter{
		ID: "f-plain",
		Items: []FilterItem{
			{Field: FieldPayee, Values: []string{"Coffee"}},
			{Field: FieldCategory, Values: []string{string(filterGroceries.ID)}},
		},
	}
	require.True(t, filterMatches(both, filterPosting("-4.75")))
	require.False(t, filterMatches(both, filterUncategorized(filterPosting("-4.75"))))
}

func TestThePayeeFacetIsAnExactTestUnderTheDefaultOperator(t *testing.T) {
	// Whole payee names: picking "Coffee" must not select "Coffee Grinder
	// Repair".
	exact := filterOf(FilterItem{Field: FieldPayee, Values: []string{"Coff"}})
	require.False(t, filterMatches(exact, filterPosting("-25.00")))
}

func TestKeywordChipsUnderContainsMustAllBePresent(t *testing.T) {
	// Keyword chips must all be present; alternatives are groups.
	both := filterOf(FilterItem{
		Field:    FieldStatementName,
		Operator: OpContains,
		Values:   []string{"sq", "coffee"},
	})
	require.True(t, filterMatches(both, filterPosting("-25.00")))

	missing := filterOf(FilterItem{
		Field:    FieldStatementName,
		Operator: OpContains,
		Values:   []string{"sq", "hardware"},
	})
	require.False(t, filterMatches(missing, filterPosting("-25.00")))
}

func TestIsExactlyDoesNotFireOnASubstring(t *testing.T) {
	item := FilterItem{Field: FieldStatementName, Operator: OpIsExactly, Values: []string{"SQ"}}
	require.False(t, filterMatches(filterOf(item), filterPosting("-25.00")))

	item.Values = []string{"sq *coffee 1234"}
	require.True(t, filterMatches(filterOf(item), filterPosting("-25.00")))
}

func TestABlankKeywordChipFailsRatherThanHandingOverTheWholeLedger(t *testing.T) {
	blank := filterOf(FilterItem{
		Field: FieldStatementName, Operator: OpContains, Values: []string{"  "},
	})
	require.False(t, filterMatches(blank, filterPosting("-25.00")))
}

func TestStatementNameMatchingIgnoresTheRename(t *testing.T) {
	// A payee rule stops firing once its own rename lands, which is why the
	// builder recommends the statement name.
	renamed := filterPosting("-25.00")
	renamed.Txn.Payee = "Coffee"

	statement := filterOf(FilterItem{
		Field: FieldStatementName, Operator: OpContains, Values: []string{"sq *coffee"},
	})
	payee := filterOf(FilterItem{
		Field: FieldPayee, Operator: OpContains, Values: []string{"sq *coffee"},
	})
	require.True(t, filterMatches(statement, renamed))
	require.False(t, filterMatches(payee, renamed))
}

func TestAccentsDoNotStopARuleFiring(t *testing.T) {
	row := filterPosting("-25.00")
	row.Txn.StatementName = "CAFÉ MOMÜS"

	item := FilterItem{
		Field: FieldStatementName, Operator: OpContains, Values: []string{"cafe momus"},
	}
	require.True(t, filterMatches(filterOf(item), row))
}

func TestTheAmountComparisonsRunOnMagnitudes(t *testing.T) {
	// "Greater than 50" for an expense means bigger, not less negative.
	cases := []struct {
		name     string
		item     FilterItem
		amount   string
		expected bool
	}{
		{"greater than fires on a bigger expense", FilterItem{
			Field: FieldAmount, Operator: OpGreaterThan,
			Minimum: MustFromString("50"), HasMinimum: true,
		}, "-90.00", true},
		{"greater than does not fire on a smaller one", FilterItem{
			Field: FieldAmount, Operator: OpGreaterThan,
			Minimum: MustFromString("50"), HasMinimum: true,
		}, "-10.00", false},
		{"less than fires below the bound", FilterItem{
			Field: FieldAmount, Operator: OpLessThan,
			Maximum: MustFromString("50"), HasMaximum: true,
		}, "-10.00", true},
		{"equals fires on the exact magnitude", FilterItem{
			Field: FieldAmount, Operator: OpEquals,
			Minimum: MustFromString("25"), HasMinimum: true,
		}, "-25.00", true},
		{"equals does not fire on a near miss", FilterItem{
			Field: FieldAmount, Operator: OpEquals,
			Minimum: MustFromString("25"), HasMinimum: true,
		}, "-25.01", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.expected,
				filterMatches(filterOf(tc.item), filterPosting(tc.amount)))
		})
	}
}

func TestTheExpenseIncomeRadioRidesOnTheAmountItemsState(t *testing.T) {
	income := filterOf(FilterItem{Field: FieldAmount, State: true, HasState: true})
	expense := filterOf(FilterItem{Field: FieldAmount, State: false, HasState: true})
	either := filterOf(FilterItem{Field: FieldAmount})

	require.False(t, filterMatches(income, filterPosting("-25.00")))
	require.True(t, filterMatches(income, filterPosting("25.00")))
	require.True(t, filterMatches(expense, filterPosting("-25.00")))
	require.True(t, filterMatches(either, filterPosting("-25.00")))
	require.True(t, filterMatches(either, filterPosting("25.00")))
}

func TestAnUnhandledFieldFailsItsItemAndValidateSaysWhy(t *testing.T) {
	// The evaluator narrows silently; Validate is the loud check.
	unknown := filterOf(FilterItem{Field: "invented_by_a_newer_release"})
	require.False(t, filterMatches(unknown, filterPosting("-25.00")))
	require.ErrorContains(t, unknown.Validate(), "unhandled filter field")
	require.NoError(t, filterByCategory(filterGroceries.ID).Validate())
}

func matchesItem(field FilterField, patterns ...string) Filter {
	return filterOf(FilterItem{Field: field, Operator: OpMatches, Values: patterns})
}

func TestAPatternMatchesTheStatementNameCaseInsensitively(t *testing.T) {
	row := filterPosting("-4.75")
	require.True(t, filterMatches(matchesItem(FieldStatementName, `^SQ \*`), row))
	require.False(t, filterMatches(matchesItem(FieldStatementName, `^AMZN`), row))
}

func TestAnyOnePatternMatchingIsEnough(t *testing.T) {
	// Unlike keyword chips, any one pattern may match.
	row := filterPosting("-4.75")
	require.True(t, filterMatches(matchesItem(FieldStatementName, `^AMZN`, `COFFEE`), row))
}

func TestAPatternRunsAgainstTheRawTextSoAnAnchorHolds(t *testing.T) {
	row := filterPosting("-4.75")
	require.True(t, filterMatches(matchesItem(FieldStatementName, `1234$`), row))
	require.False(t, filterMatches(matchesItem(FieldStatementName, `^COFFEE`), row))
}

func TestAPatternReadsThePayeeWhenPointedAtIt(t *testing.T) {
	row := filterPosting("-4.75")
	require.True(t, filterMatches(matchesItem(FieldPayee, `^Coff`), row))
	require.False(t, filterMatches(matchesItem(FieldPayee, `^SQ`), row),
		"the payee is the clean name; only statement_name carries the bank's wording")
}

func TestAPatternThatWillNotCompileMatchesNothing(t *testing.T) {
	// One malformed rule must not stop the engine for every other row.
	require.False(t, filterMatches(matchesItem(FieldStatementName, `(unclosed`), filterPosting("-4.75")))
}

func TestABlankPatternMatchesNothingRatherThanEverything(t *testing.T) {
	// An empty regex would match every string.
	require.False(t, filterMatches(matchesItem(FieldStatementName, "  "), filterPosting("-4.75")))
}

// Allocations and MatchingParts must agree about what a part is: a report
// reads one or the other depending on whether it has a filter.
func TestEveryPartOfARowIsScaledToPrimaryLikeAMatchedOneIs(t *testing.T) {
	// A €100 row that converted to $110, split 60/40 in its own currency.
	txn := Transaction{
		ID: "t-1", Amount: MustFromString("-100.00"),
		AmountPrimary: MustFromString("-110.00"), HasAmountPrimary: true,
		Splits: []Split{
			{ID: "s-1", Amount: MustFromString("-60.00"), CategoryID: "cat-air"},
			{ID: "s-2", Amount: MustFromString("-40.00"), CategoryID: "cat-hotel"},
		},
	}
	posting := Posting{Txn: txn}

	parts := Allocations(posting)

	require.Len(t, parts, 2)
	require.Equal(t, "-66.00", parts[0].Amount.String(), "60% of the converted total")
	require.Equal(t, "-44.00", parts[1].Amount.String())
	require.Equal(t, posting.Amount().String(),
		Total(parts[0].Amount, parts[1].Amount).String(), "the parts sum to the row")
}

func TestAnUnsplitRowIsOneWholeAllocation(t *testing.T) {
	posting := Posting{Txn: Transaction{ID: "t-1", Amount: MustFromString("-50.00")}}

	parts := Allocations(posting)

	require.Len(t, parts, 1)
	require.Nil(t, parts[0].Split)
	require.Equal(t, "-50.00", parts[0].Amount.String())
}

// One folding, shared by the filter and the suggestion detector.
func TestFoldingIsCaseAndAccentInsensitive(t *testing.T) {
	require.Equal(t, FoldName("CAFÉ MÜNCHEN"), FoldName("cafe munchen"))
	require.Equal(t, FoldName("Ångström"), FoldName("angstrom"))
	require.Equal(t, FoldName("ＡＭＡＺＯＮ"), FoldName("amazon"), "full-width folds too")
	require.NotEqual(t, FoldName("amazon"), FoldName("amazn"))
}

func TestAFilterKeepingOneSplitIsAPartialViewOfTheRow(t *testing.T) {
	row := filterSplitPosting("")
	parts := PartialParts(filterByCategory(filterGroceries.ID), row, Facets{}, DateEffective)

	require.Len(t, parts, 1)
	require.Equal(t, ID("s-1"), parts[0].Split.ID)
	require.Equal(t, "-50.00", MatchedAmount(row, parts).String())
	require.Equal(t, "-200.00", row.Amount().String())
}

func TestARowKeptWholeIsNotPartial(t *testing.T) {
	split := filterSplitPosting("")
	both := filterByCategory(filterGroceries.ID, filterHousehold.ID)
	require.Nil(t, PartialParts(both, split, Facets{}, DateEffective))

	// A row-level facet passes every split, so it never makes a partial row.
	account := filterOf(FilterItem{Field: FieldAccount, Values: []string{"acct-1"}})
	require.Nil(t, PartialParts(account, split, Facets{}, DateEffective))

	plain := filterPosting("-25.00")
	require.Nil(t, PartialParts(filterByCategory(filterGroceries.ID), plain, Facets{}, DateEffective))
	require.Equal(t, "-25.00", MatchedAmount(plain, nil).String())
}

func TestAPartialSplitIsCountedInThePrimaryCurrency(t *testing.T) {
	// 200 booked in another currency as 220: the groceries quarter is 55, not 50.
	row := filterSplitPosting("-220.00")
	parts := PartialParts(filterByCategory(filterGroceries.ID), row, Facets{}, DateEffective)
	require.Equal(t, "-55.00", MatchedAmount(row, parts).String())
}
