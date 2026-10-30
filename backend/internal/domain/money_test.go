package domain

import (
	"encoding/json"
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestParsingAcceptsTheStringANumericColumnSerializesTo(t *testing.T) {
	m, err := FromString("1900.00")
	require.NoError(t, err)
	require.Equal(t, "1900.00", m.String())
}

func TestParsingRejectsNonsense(t *testing.T) {
	_, err := FromString("not money")
	require.ErrorContains(t, err, "cannot parse")
}

func TestTheZeroValueIsAUsableZero(t *testing.T) {
	var m Money
	require.True(t, m.IsZero())
	require.Equal(t, "0.00", m.String())
}

func TestRoundingIsHalfAwayFromZeroInBothDirections(t *testing.T) {
	require.Equal(t, "0.01", MustFromString("0.005").Round().String())
	require.Equal(t, "-0.01", MustFromString("-0.005").Round().String())
}

func TestTotalRoundsOnceAtTheEndNotPerItem(t *testing.T) {
	// Three thirds of a cent round to nothing individually and to a whole cent
	// together. Rounding intermediates loses it.
	third := MustFromString("0.004")
	require.Equal(t, "0.01", Total(third, third, third).String())

	perItem := third.Round().Add(third.Round()).Add(third.Round())
	require.Equal(t, "0.00", perItem.String())
}

func TestTotalOfNothingIsZeroNotAnError(t *testing.T) {
	require.Equal(t, "0.00", Total().String())
}

func TestSumOverASlice(t *testing.T) {
	rows := []Money{MustFromString("-25.00"), MustFromString("-75.50")}
	got := Sum(rows, func(m Money) Money { return m })
	require.Equal(t, "-100.50", got.String())
}

func TestRatioByZeroReportsNotOkRatherThanZero(t *testing.T) {
	_, ok := Ratio(MustFromString("5"), Zero)
	require.False(t, ok, "no prior period and no change are different facts")
}

func TestPercentRoundsToTwoPlaces(t *testing.T) {
	got, ok := Percent(MustFromString("1"), MustFromString("3"))
	require.True(t, ok)
	require.Equal(t, "33.33", got.String())
}

func TestSignConventionExpensesNegativeIncomePositiveZeroNeither(t *testing.T) {
	require.True(t, MustFromString("-1").IsExpense())
	require.True(t, MustFromString("1").IsIncome())
	require.False(t, Zero.IsExpense())
	require.False(t, Zero.IsIncome())
}

func TestAbsIsWhatTheUiShowsForSpend(t *testing.T) {
	require.Equal(t, "42.50", MustFromString("-42.50").Abs().String())
}

func TestScaleDoesNotRoundSoAggregatesDoNotDrift(t *testing.T) {
	rate := decimal.RequireFromString("0.8333")
	// 8.333, not 8.3330: shopspring normalizes trailing zeros. Only the
	// rendering of an unrounded intermediate differs, and none is displayed.
	require.Equal(t, "8.333", MustFromString("10").Scale(rate).Decimal().String())
}

func TestJsonEmitsAStringNeverANumber(t *testing.T) {
	body, err := json.Marshal(struct {
		Amount Money `json:"amount"`
	}{MustFromString("1900")})
	require.NoError(t, err)
	require.JSONEq(t, `{"amount":"1900.00"}`, string(body))
}

func TestJsonRefusesANumberBecauseThePrecisionIsAlreadyGone(t *testing.T) {
	var row struct {
		Amount Money `json:"amount"`
	}
	err := json.Unmarshal([]byte(`{"amount":19.99}`), &row)
	require.ErrorContains(t, err, "expected a JSON string")
	var refused *AmountJSONError
	require.ErrorAs(t, err, &refused)
	require.True(t, refused.NotString)
	require.Equal(t, "19.99", string(refused.Raw))
}

func TestJsonRefusesTextThatIsNotAnAmountInItsOwnWords(t *testing.T) {
	var row struct {
		Amount Money `json:"amount"`
	}
	err := json.Unmarshal([]byte(`{"amount":"$1,000"}`), &row)
	var refused *AmountJSONError
	require.ErrorAs(t, err, &refused)
	require.Equal(t, `"$1,000" is not an amount such as "1250.00"`, refused.Reason())
	require.NotContains(t, err.Error(), "can't convert")
}

func TestJsonRoundTripsThroughTheStringForm(t *testing.T) {
	var row struct {
		Amount Money `json:"amount"`
	}
	require.NoError(t, json.Unmarshal([]byte(`{"amount":"-123.45"}`), &row))
	require.Equal(t, "-123.45", row.Amount.String())
}

// A numeric column serialized as "1900.00" concatenates under a string `+`;
// Money adds.
func TestTheConcatenationTrapIsNotRepresentable(t *testing.T) {
	a, b := MustFromString("1900.00"), MustFromString("1900.00")
	require.Equal(t, "3800.00", a.Add(b).String())
}

func TestDivIntKeepsThePerDayFigureInsideTheType(t *testing.T) {
	got, ok := MustFromString("310.00").DivInt(31)
	require.True(t, ok)
	require.Equal(t, "10.00", got.String())
}

func TestDivIntByZeroReportsNotOk(t *testing.T) {
	_, ok := MustFromString("310.00").DivInt(0)
	require.False(t, ok, "a month with no days remaining has no per-day figure")
}

func TestDivRateByZeroReportsNotOk(t *testing.T) {
	_, ok := MustFromString("10").DivRate(decimal.Zero)
	require.False(t, ok)
}

func TestDisplayPayeeFallsBackToTheBanksWording(t *testing.T) {
	require.Equal(t, "Coffee", Transaction{Payee: "Coffee", StatementName: "SQ *COFFEE"}.DisplayPayee())
	require.Equal(t, "SQ *COFFEE", Transaction{StatementName: "SQ *COFFEE"}.DisplayPayee())
}

func TestMatchNameReadsTheBanksWordingAndFallsBackToThePayee(t *testing.T) {
	require.Equal(t, "SQ *COFFEE", Transaction{Payee: "Coffee", StatementName: "SQ *COFFEE"}.MatchName())
	require.Equal(t, "Coffee", Transaction{Payee: "Coffee"}.MatchName())
}

// --- Scale (calculations.md §1) -----------------------------------------------

func TestAnAmountPrintsAtItsCurrencysScale(t *testing.T) {
	require.Equal(t, "-1.234", FromMinor(-1234, 3).String(), "a dinar has three places")
	require.Equal(t, "1500", FromMinor(1500, 0).String(), "a yen has none")
	require.Equal(t, "12.30", FromMinor(1230, 2).String())
	require.Equal(t, "0.000", FromMinor(0, 3).String(), "zero keeps its currency's places")

	body, err := json.Marshal(struct {
		Amount Money `json:"amount"`
	}{FromMinor(-1234, 3)})
	require.NoError(t, err)
	require.JSONEq(t, `{"amount":"-1.234"}`, string(body))
}

func TestRoundingIsAtTheAmountsOwnScale(t *testing.T) {
	dinar := FromMinor(1, 3)
	third, ok := dinar.MulInt(1000).DivInt(3)
	require.True(t, ok)
	require.Equal(t, "0.333", third.Round().String(), "a third of a dinar keeps its fils")
	require.Equal(t, "-0.001", FromMinor(-1, 3).Scale(decimal.RequireFromString("0.5")).Round().String(),
		"half a fil rounds away from zero")
	yen := FromMinor(1, 0).Scale(decimal.RequireFromString("2.5"))
	require.Equal(t, "3", yen.Round().String(), "2.5 yen rounds away from zero")
	require.Equal(t, "0.001", dinar.MinorUnit().String())
	require.Equal(t, "1", FromMinor(7, 0).MinorUnit().String())
}

func TestAnAmountWithNoScaleTakesTheScaleItMeets(t *testing.T) {
	sum := Zero.Add(FromMinor(1234, 3)).Add(MustFromString("1"))
	require.Equal(t, "2.234", sum.String())
	places, ok := sum.Places()
	require.True(t, ok)
	require.Equal(t, 3, places)
	require.Equal(t, "2.234", Total(MustFromString("1"), FromMinor(1234, 3)).String())
}

func TestCombiningTwoScalesIsRefusedNotCoerced(t *testing.T) {
	dinars, dollars := FromMinor(1234, 3), FromMinor(1234, 2)
	for name, combine := range map[string]func(){
		"add":     func() { dinars.Add(dollars) },
		"sub":     func() { dinars.Sub(dollars) },
		"compare": func() { dinars.LessThan(dollars) },
		"total":   func() { Total(dinars, dollars) },
	} {
		t.Run(name, func(t *testing.T) {
			defer func() {
				var mixed *MixedScaleError
				require.ErrorAs(t, recover().(error), &mixed)
				require.Equal(t, 3, mixed.Left)
				require.Equal(t, 2, mixed.Right)
			}()
			combine()
		})
	}
	require.False(t, dinars.SameScale(dollars))
	require.True(t, dinars.SameScale(MustFromString("1")))
}

func TestAConversionCarriesTheTargetsScale(t *testing.T) {
	// 1.234 KWD at 3.25 USD per KWD is 4.0105 USD, 4.01 once rounded.
	dollars := FromMinor(1234, 3).Convert(decimal.RequireFromString("3.25"), 2).Round()
	require.Equal(t, "4.01", dollars.String())
	require.Equal(t, "5.01", dollars.Add(FromMinor(100, 2)).String())
	// 1500 JPY at 0.0065 USD per JPY is 9.75 USD.
	require.Equal(t, "9.75", FromMinor(1500, 0).Convert(decimal.RequireFromString("0.0065"), 2).Round().String())
	// Face value restates the figure without a rate: approximate by intent.
	require.Equal(t, "1500.00", FromMinor(1500, 0).FaceValue(2).String())
}

func TestGivingAnAmountAScaleRefusesDigitsItCannotHold(t *testing.T) {
	kept, err := MustFromString("1.230").AtPlaces(2)
	require.NoError(t, err)
	require.Equal(t, "1.23", kept.String())
	_, err = MustFromString("1.2345").AtPlaces(3)
	require.Error(t, err, "a fourth dinar decimal is not rounded away")
	_, err = MustFromString("0.5").AtPlaces(0)
	require.Error(t, err, "half a yen is not an amount")
	_, err = FromMinor(1, 3).AtPlaces(2)
	var mixed *MixedScaleError
	require.ErrorAs(t, err, &mixed, "a dinar amount is not relabelled as dollars")
}

func TestTrailingZerosAreNotDigitsTheScaleCannotHold(t *testing.T) {
	for _, tc := range []struct {
		text   string
		places int
		want   string
	}{
		{"1.000", 2, "1.00"},
		{"1500.00", 0, "1500"},
		{"-12.340", 2, "-12.34"},
		{"1.2340", 3, "1.234"},
	} {
		got, err := MustFromString(tc.text).AtPlaces(tc.places)
		require.NoError(t, err, tc.text)
		require.Equal(t, tc.want, got.String())
	}
}

// The client holds money exactly to about ±900.7 billion, so nothing beyond
// ±900 billion is stored for it to drift on.
func TestAnAmountBeyondNineHundredBillionIsOutOfRange(t *testing.T) {
	require.NoError(t, MustFromString("900000000000").CheckRange())
	require.NoError(t, MustFromString("-900000000000.00").CheckRange())
	require.ErrorIs(t, MustFromString("900000000000.01").CheckRange(), ErrAmountOutOfRange)
	_, err := MustFromString("-1000000000000").AtPlaces(0)
	require.ErrorIs(t, err, ErrAmountOutOfRange)
}

func TestSplittingADinarChargeIsExactToTheFil(t *testing.T) {
	// 10.000 KWD over three equal items: 3.334, 3.333, 3.333.
	parts := SplitByWeight(FromMinor(-10000, 3), []Money{MustFromString("1"), MustFromString("1"), MustFromString("1")})
	require.Equal(t, "-10.000", Total(parts...).String())
	require.Equal(t, "-3.334", parts[0].String())
	require.Equal(t, "-3.333", parts[1].String())
	// 1000 JPY over the same three: 334, 333, 333.
	yen := SplitByWeight(FromMinor(1000, 0), []Money{MustFromString("1"), MustFromString("1"), MustFromString("1")})
	require.Equal(t, "334", yen[0].String())
	require.Equal(t, "333", yen[2].String())
}
