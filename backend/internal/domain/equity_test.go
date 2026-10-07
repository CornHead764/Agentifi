package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestEquityIsTheAssetLessWhatIsSecuredOnIt(t *testing.T) {
	house := MustFromString("420000.00")
	// Debt is stored negative; subtracting the stored balance would answer
	// 704,000.
	mortgage := MustFromString("-284000.00")

	require.Equal(t, "136000.00", Equity(house, []Money{mortgage}).String())
}

func TestEquityCountsEveryLoanSecuredOnTheAsset(t *testing.T) {
	// One house, a first mortgage and a HELOC: why the link is stored on the
	// loan side.
	house := MustFromString("420000.00")
	loans := []Money{MustFromString("-284000.00"), MustFromString("-35000.00")}

	require.Equal(t, "101000.00", Equity(house, loans).String())
}

func TestAnUnfinancedAssetIsAllEquity(t *testing.T) {
	car := MustFromString("18400.00")
	require.Equal(t, "18400.00", Equity(car, nil).String())
}

func TestEquityGoesNegativeWhenTheAssetIsUnderwater(t *testing.T) {
	// Not clamped at zero: net worth must agree with the sum of its
	// accounts.
	car := MustFromString("18400.00")
	loan := MustFromString("-22150.00")

	require.Equal(t, "-3750.00", Equity(car, []Money{loan}).String())
}

func TestALoanStoredPositiveIsStillCountedAsDebt(t *testing.T) {
	// A hand-entered loan can arrive positive; adding it would inflate the
	// asset.
	house := MustFromString("420000.00")

	require.Equal(t, "136000.00", Equity(house, []Money{MustFromString("284000.00")}).String())
}

func TestLoanToValueIsTheFinancedShare(t *testing.T) {
	house := MustFromString("420000.00")
	ratio, ok := LoanToValue(house, []Money{MustFromString("-284000.00")})

	require.True(t, ok)
	// A plain ratio, not percent units.
	require.Equal(t, "0.6762", ratio.Round(4).String())
}

func TestAnAssetWorthNothingHasNoLoanToValue(t *testing.T) {
	// Zero would read as "nothing is owed" on a house with a mortgage on it.
	_, ok := LoanToValue(Zero, []Money{MustFromString("-284000.00")})
	require.False(t, ok)
}

func equityAsset(id ID) Account {
	return Account{ID: id, Name: string(id), Kind: KindAsset, Currency: "USD", IncludeInNetWorth: true}
}

func equityLoan(id, securedBy ID) Account {
	return Account{
		ID: id, Name: string(id), Kind: KindLoan, Currency: "USD",
		IncludeInNetWorth: true, SecuredByAccountID: securedBy,
	}
}

func TestEquityAtIsTheAssetLessTheLoanSecuredOnIt(t *testing.T) {
	house := equityAsset("a-house")
	mortgage := equityLoan("a-mortgage", house.ID)

	got := EquityAt([]Account{house, mortgage}, netFigures(map[ID]string{
		"a-house": "420000.00", "a-mortgage": "-284000.00",
	}))
	require.Equal(t, "136000.00", got.String())
}

func TestEquityAtCountsEveryLoanSecuredOnTheSameAsset(t *testing.T) {
	house := equityAsset("a-house")
	first := equityLoan("a-mortgage", house.ID)
	heloc := equityLoan("a-heloc", house.ID)

	got := EquityAt([]Account{house, first, heloc}, netFigures(map[ID]string{
		"a-house": "420000.00", "a-mortgage": "-284000.00", "a-heloc": "-35000.00",
	}))
	require.Equal(t, "101000.00", got.String())
}

func TestEquityAtLeavesAnUnsecuredLoanOutOfTheSum(t *testing.T) {
	// A car loan with no SecuredByAccountID is ordinary debt, not equity.
	house := equityAsset("a-house")
	carLoan := Account{
		ID: "a-car-loan", Name: "a-car-loan", Kind: KindLoan,
		Currency: "USD", IncludeInNetWorth: true,
	}

	got := EquityAt([]Account{house, carLoan}, netFigures(map[ID]string{
		"a-house": "420000.00", "a-car-loan": "-12000.00",
	}))
	require.Equal(t, "420000.00", got.String())
}

func TestEquityAtLeavesOutAnAssetExcludedFromNetWorth(t *testing.T) {
	house := equityAsset("a-house")
	excluded := equityAsset("a-boat")
	excluded.IncludeInNetWorth = false

	got := EquityAt([]Account{house, excluded}, netFigures(map[ID]string{
		"a-house": "420000.00", "a-boat": "18000.00",
	}))
	require.Equal(t, "420000.00", got.String())
}

func TestEquityAtWithNoAssetsIsZero(t *testing.T) {
	got := EquityAt(nil, netFigures(map[ID]string{}))
	require.Equal(t, "0.00", got.String())
}
