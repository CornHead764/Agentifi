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
