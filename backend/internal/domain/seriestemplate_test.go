package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// calculations.md §9, "A linked row takes the series' template". Expected
// values are worked from that rule by hand.

func templateForTest() SeriesTemplate {
	return SeriesTemplate{
		Payee:      "Water bill",
		CategoryID: "cat-utilities",
		TagIDs:     []ID{"tag-house"},
		Splits: []Split{
			{Amount: MustFromString("-60.00"), CategoryID: "cat-water", Memo: "water"},
			{Amount: MustFromString("-40.00"), CategoryID: "cat-sewer", Memo: "sewer", TagIDs: []ID{"tag-city"}},
		},
		Amount: MustFromString("-100.00"),
	}
}

func TestABareRowTakesTheWholeTemplate(t *testing.T) {
	row := Transaction{Amount: MustFromString("-110.00"), StatementName: "CITY WATER 0001"}
	fill := FillFromTemplate(row, templateForTest())

	require.Equal(t, "Water bill", fill.Payee)
	require.Equal(t, ID("cat-utilities"), fill.CategoryID)
	require.Equal(t, []ID{"tag-house"}, fill.TagIDs)
	// 60/40 of 110.00.
	require.Len(t, fill.Splits, 2)
	require.Equal(t, "-66.00", fill.Splits[0].Amount.String())
	require.Equal(t, ID("cat-water"), fill.Splits[0].CategoryID)
	require.Equal(t, "water", fill.Splits[0].Memo)
	require.Equal(t, "-44.00", fill.Splits[1].Amount.String())
	require.Equal(t, []ID{"tag-city"}, fill.Splits[1].TagIDs)
}

func TestTheTemplateFillsOnlyWhatTheRowLacks(t *testing.T) {
	row := Transaction{
		Amount: MustFromString("-110.00"), Payee: "City Water", CategoryID: "cat-rule",
		TagIDs: []ID{"tag-mine"},
	}
	require.True(t, FillFromTemplate(row, templateForTest()).IsEmpty())
}

func TestACategorizedRowTakesNoSplit(t *testing.T) {
	// A split would decide where the money is filed, over the rule's category.
	row := Transaction{Amount: MustFromString("-110.00"), CategoryID: "cat-rule"}
	fill := FillFromTemplate(row, templateForTest())
	require.Empty(t, fill.Splits)
	require.Empty(t, fill.CategoryID)
	require.Equal(t, "Water bill", fill.Payee)
	require.Equal(t, []ID{"tag-house"}, fill.TagIDs)
}

func TestARowWithItsOwnSplitKeepsIt(t *testing.T) {
	row := Transaction{
		Amount: MustFromString("-110.00"),
		Splits: []Split{
			{Amount: MustFromString("-100.00"), CategoryID: "cat-a"},
			{Amount: MustFromString("-10.00"), CategoryID: "cat-b"},
		},
	}
	fill := FillFromTemplate(row, templateForTest())
	require.Empty(t, fill.Splits)
	require.Empty(t, fill.CategoryID)
}

func TestTheRoundingRemainderLandsOnTheFirstLargestLine(t *testing.T) {
	// Three equal thirds of 90.00 carried to 100.00: 33.333... rounds to
	// 33.33 three times, 99.99, and the missing cent goes to the first.
	thirds := []Split{
		{Amount: MustFromString("-30.00")},
		{Amount: MustFromString("-30.00")},
		{Amount: MustFromString("-30.00")},
	}
	got := ScaleSplits(thirds, MustFromString("-90.00"), MustFromString("-100.00"))
	require.Equal(t, "-33.34", got[0].Amount.String())
	require.Equal(t, "-33.33", got[1].Amount.String())
	require.Equal(t, "-33.33", got[2].Amount.String())
}

func TestAnEqualAmountKeepsTheTemplatesLines(t *testing.T) {
	template := templateForTest()
	got := ScaleSplits(template.Splits, template.Amount, MustFromString("-100.00"))
	require.Equal(t, "-60.00", got[0].Amount.String())
	require.Equal(t, "-40.00", got[1].Amount.String())
}

func TestAZeroSeriesAmountGivesNoProportion(t *testing.T) {
	template := templateForTest()
	require.Nil(t, ScaleSplits(template.Splits, Zero, MustFromString("-100.00")))
}

// §9: the bank's charge of 16.00 taking over a row accepted at 15.00, split
// 9.00 / 6.00, carries the split to 9.60 / 6.40, each line keeping its
// category, memo and tags.
func TestATakeoverCarriesTheRowsOwnSplitToTheBanksAmount(t *testing.T) {
	splits := []Split{
		{Amount: MustFromString("-9.00"), CategoryID: "cat-water", Memo: "water"},
		{Amount: MustFromString("-6.00"), CategoryID: "cat-sewer", Memo: "sewer", TagIDs: []ID{"tag-house"}},
	}
	got := CarrySplits(splits, MustFromString("-15.00"), MustFromString("-16.00"))
	require.Equal(t, []Split{
		{Amount: MustFromString("-9.60"), CategoryID: "cat-water", Memo: "water"},
		{Amount: MustFromString("-6.40"), CategoryID: "cat-sewer", Memo: "sewer", TagIDs: []ID{"tag-house"}},
	}, got)
}

// §9: three 5.00 lines of 15.00 carried to 16.00 are 5.34, 5.33 and 5.33.
func TestACarriedSplitsRemainderLandsOnTheFirstLargestLine(t *testing.T) {
	fifths := []Split{
		{Amount: MustFromString("-5.00")}, {Amount: MustFromString("-5.00")}, {Amount: MustFromString("-5.00")},
	}
	got := CarrySplits(fifths, MustFromString("-15.00"), MustFromString("-16.00"))
	require.Equal(t, "-5.34", got[0].Amount.String())
	require.Equal(t, "-5.33", got[1].Amount.String())
	require.Equal(t, "-5.33", got[2].Amount.String())
}

// §9: a row of zero gives no proportion; its lines stay and the difference
// lands on the largest.
func TestASplitOfZeroTakesTheDifferenceOnItsLargestLine(t *testing.T) {
	splits := []Split{
		{Amount: MustFromString("-10.00"), CategoryID: "cat-a"},
		{Amount: MustFromString("10.00"), CategoryID: "cat-b"},
		{Amount: MustFromString("-20.00"), CategoryID: "cat-c"},
		{Amount: MustFromString("20.00"), CategoryID: "cat-d"},
	}
	got := CarrySplits(splits, Zero, MustFromString("-16.00"))
	require.Equal(t, "-10.00", got[0].Amount.String())
	require.Equal(t, "10.00", got[1].Amount.String())
	require.Equal(t, "-36.00", got[2].Amount.String())
	require.Equal(t, "20.00", got[3].Amount.String())
	require.Nil(t, CarrySplits(nil, Zero, MustFromString("-16.00")))
}
