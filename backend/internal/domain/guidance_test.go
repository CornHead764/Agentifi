package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fuelStop is a convenience store where the amount decides whether the charge
// was lunch or a tank of fuel.
func fuelStop(amount string) Posting {
	return Posting{
		Txn: Transaction{
			ID:            "txn-kt",
			AccountID:     "acct-1",
			Date:          NewDate(2026, time.September, 12),
			Amount:        MustFromString(amount),
			StatementName: "FUEL STOP #0631 SPRINGFIELD",
			Payee:         "Fuel Stop",
			Source:        SourceSync,
			Currency:      "USD",
		},
		Account: Account{ID: "acct-1", Name: "Everyday Checking", Kind: KindCash, Currency: "USD"},
	}
}

func noteAbout(field FilterField, keyword string) Guidance {
	return Guidance{
		ID: "gd-1", Name: "Fuel Stop", Instruction: "Under $20 is Fast Food; over is Gas & Fuel.",
		Filter: Filter{Items: []FilterItem{
			{Field: field, Operator: OpContains, Values: []string{keyword}},
		}},
	}
}

func TestANoteWithNoConditionsIsAboutNoRowAtAll(t *testing.T) {
	// Unlike the register, an empty filter here matches nothing. The API
	// refuses to save one; this second door has to fail closed.
	note := Guidance{ID: "gd-1", Name: "Always", Instruction: "Prefer the narrower category."}
	require.False(t, note.Applies(fuelStop("-12.00"), Facets{}))
	require.False(t, note.Applies(filterPosting("-25.00"), Facets{}))
	require.Empty(t, SelectGuidance([]Guidance{note}, fuelStop("-12.00"), Facets{}))
}

func TestANoteMatchesTheStatementNameWithoutRegardToCase(t *testing.T) {
	note := noteAbout(FieldStatementName, "fuel stop")
	require.True(t, note.Applies(fuelStop("-12.00"), Facets{}))
	require.False(t, note.Applies(filterPosting("-25.00"), Facets{}))
}

func TestANoteMayMatchTheCleanedPayeeInstead(t *testing.T) {
	// Ground rule 5: a note can match the bank's wording or the rewritten
	// payee.
	note := noteAbout(FieldPayee, "FUEL")
	require.True(t, note.Applies(fuelStop("-12.00"), Facets{}))

	renamed := fuelStop("-12.00")
	renamed.Txn.Payee = "Gas Mart"
	require.False(t, note.Applies(renamed, Facets{}))
}

func TestSelectGuidanceKeepsTheHouseholdsOrder(t *testing.T) {
	first := noteAbout(FieldStatementName, "fuel stop")
	first.ID, first.Name = "gd-first", "First"
	second := noteAbout(FieldStatementName, "fuel")
	second.ID, second.Name = "gd-second", "Second"
	elsewhere := noteAbout(FieldStatementName, "costco")

	picked := SelectGuidance([]Guidance{first, second, elsewhere}, fuelStop("-12.00"), Facets{})
	require.Len(t, picked, 2)
	require.Equal(t, "First", picked[0].Name)
	require.Equal(t, "Second", picked[1].Name)
}

func TestANoteCanBeNarrowedToAnAmount(t *testing.T) {
	note := Guidance{
		ID: "gd-2", Name: "Small Fuel Stop runs", Instruction: "Fast Food.",
		Filter: Filter{Items: []FilterItem{
			{Field: FieldStatementName, Operator: OpContains, Values: []string{"fuel stop"}},
			{
				Field: FieldAmount, Operator: OpLessThan,
				Maximum: MustFromString("20.00"), HasMaximum: true,
				State: false, HasState: true,
			},
		}},
	}
	require.True(t, note.Applies(fuelStop("-12.00"), Facets{}))
	require.False(t, note.Applies(fuelStop("-48.00"), Facets{}))
}

func TestDescribeFilterSaysWhatANoteIsAbout(t *testing.T) {
	require.Equal(t, "nothing", DescribeFilter(Filter{}, nil))

	require.Equal(t, `the statement name contains "fuel stop"`,
		DescribeFilter(noteAbout(FieldStatementName, "fuel stop").Filter, nil))

	amounts := Filter{Items: []FilterItem{
		{Field: FieldPayee, Operator: OpContains, Values: []string{"Fuel"}},
		{
			Field: FieldAmount, Operator: OpGreaterThan,
			Minimum: MustFromString("20.00"), HasMinimum: true, State: false, HasState: true,
		},
	}}
	require.Equal(t, `the payee contains "Fuel" and money out is more than 20.00`,
		DescribeFilter(amounts, nil))
}

func TestDescribeFilterReadsGroupsAsAlternatives(t *testing.T) {
	// Items in one group are AND'd, groups are OR'd.
	filter := Filter{Items: []FilterItem{
		{Field: FieldStatementName, Operator: OpContains, Values: []string{"fuel stop"}},
		{
			Field: FieldStatementName, Operator: OpContains,
			Values: []string{"fuelstop"}, GroupIndex: 1,
		},
	}}
	require.Equal(t,
		`the statement name contains "fuel stop", or the statement name contains "fuelstop"`,
		DescribeFilter(filter, nil))
}

func TestDescribeFilterNamesAnAccountRatherThanItsID(t *testing.T) {
	filter := Filter{Items: []FilterItem{
		{Field: FieldAccount, Operator: OpIn, Values: []string{"acct-1"}},
	}}
	names := map[ID]string{"acct-1": "Everyday Checking"}
	require.Equal(t, `the account is one of "Everyday Checking"`, DescribeFilter(filter, names))
	require.Equal(t, `the account is one of "acct-1"`, DescribeFilter(filter, nil))
}

func TestDescribeFilterMentionsAConditionItCannotSpell(t *testing.T) {
	// An unknown field is named imprecisely rather than dropped, or the note
	// would read as applying more widely than it does.
	filter := Filter{Items: []FilterItem{{Field: FilterField("invented_next_march")}}}
	require.Equal(t, "invented_next_march matches", DescribeFilter(filter, nil))
}

func TestANoteCanTestAFacetTheRulesBuilderAlsoTests(t *testing.T) {
	// The facets come from the row as the rules engine supplies them, so a
	// flag condition narrows rather than silently never matching.
	note := Guidance{
		ID: "gd-3", Name: "Flagged rows", Instruction: "Leave these for a person.",
		Filter: Filter{Items: []FilterItem{
			{Field: FieldFlag, Operator: OpIn, Values: []string{"red"}},
		}},
	}
	require.True(t, note.Applies(fuelStop("-12.00"), Facets{UserFlag: "red"}))
	require.False(t, note.Applies(fuelStop("-12.00"), Facets{UserFlag: "blue"}))
	require.False(t, note.Applies(fuelStop("-12.00"), Facets{}))
}

func TestDescribeFilterSpellsTheYesNoFields(t *testing.T) {
	cases := map[string]FilterItem{
		"the row has been reviewed": {Field: FieldIsReviewed, HasState: true, State: true},
		"the row has not been reviewed": {
			Field: FieldIsReviewed, HasState: true, State: false,
		},
		"the row is still pending":  {Field: FieldIsPending, HasState: true, State: true},
		"the row has settled":       {Field: FieldIsPending, HasState: true, State: false},
		"the row counts in reports": {Field: FieldIsExcludedFromReports, HasState: true},
		"the row is a bill or subscription": {
			Field: FieldIsBillOrSubscription, HasState: true, State: true,
		},
		"the row has a file attached or a receipt filed on it": {
			Field: FieldHasAttachment, HasState: true, State: true,
		},
		"the row has no files attached": {
			Field: FieldHasAttachment, HasState: true, State: false,
		},
	}
	for expected, item := range cases {
		require.Equal(t, expected, DescribeFilter(Filter{Items: []FilterItem{item}}, nil))
	}
}

func TestDescribeFilterReadsANegatedYesNoFieldTheOtherWayRound(t *testing.T) {
	// Not "not is reviewed", which is ambiguous about which negation won.
	item := FilterItem{Field: FieldIsReviewed, HasState: true, State: true, Negated: true}
	require.Equal(t, "the row has not been reviewed",
		DescribeFilter(Filter{Items: []FilterItem{item}}, nil))
}

func TestDescribeFilterSpellsADateRangeAndAFlag(t *testing.T) {
	dates := FilterItem{
		Field: FieldDate, Operator: OpBetween,
		Start: NewDate(2026, time.January, 1), End: NewDate(2026, time.March, 31),
	}
	require.Equal(t, "the date is between 2026-01-01 and 2026-03-31",
		DescribeFilter(Filter{Items: []FilterItem{dates}}, nil))

	open := FilterItem{Field: FieldDate, Operator: OpBetween, Start: NewDate(2026, time.January, 1)}
	require.Equal(t, "the date is on or after 2026-01-01",
		DescribeFilter(Filter{Items: []FilterItem{open}}, nil))

	flag := FilterItem{Field: FieldFlag, Operator: OpIn, Values: []string{"flagged"}}
	require.Equal(t, `the flag is one of "flagged"`,
		DescribeFilter(Filter{Items: []FilterItem{flag}}, nil))
}

func TestANoteBoundedByDatesReadsTheEffectiveDate(t *testing.T) {
	// Posted on the last day of August, paid with the card's September
	// statement: a note about September is about this row.
	charge := fuelStop("-31.00")
	charge.Txn.Date = NewDate(2026, time.August, 31)
	charge.Txn.EffectiveDate = NewDate(2026, time.September, 25)
	september := Guidance{
		ID: "gd-3", Name: "September", Instruction: "Travel.",
		Filter: Filter{Items: []FilterItem{{
			Field: FieldDate, Start: NewDate(2026, time.September, 1), End: NewDate(2026, time.September, 30),
		}}},
	}
	august := september
	august.Filter = Filter{Items: []FilterItem{{
		Field: FieldDate, Start: NewDate(2026, time.August, 1), End: NewDate(2026, time.August, 31),
	}}}
	require.True(t, september.Applies(charge, Facets{}))
	require.False(t, august.Applies(charge, Facets{}))
}
