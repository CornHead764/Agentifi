package importer

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Money never goes through float64: encoding/json decodes a JSON number into
// float64 unless told otherwise, which fails silently as a cent of drift.
// These tests keep ParseExport's UseNumber in place.

func TestParseExportKeepsEveryNumberAsItsLiteralCharacters(t *testing.T) {
	export, err := ParseExport([]byte(fixtureJSON))
	require.NoError(t, err)

	records, err := export.Records(fixtureDataset, "transactionStore")
	require.NoError(t, err)
	amount := records["x1"]["amount"]

	number, ok := amount.(json.Number)
	require.Truef(t, ok, "amount arrived as %T; the decoder is not using UseNumber, "+
		"and every amount in the import has already been through a float", amount)
	require.Equal(t, "-25.5", number.String())
}

func TestAJSONNumberReachesMoneyThroughItsStringAndNotThroughAFloat(t *testing.T) {
	// 0.1 + 0.2 in binary floating point is 0.30000000000000004. Parsed from
	// the literal characters it is exactly what was written.
	r := newRecord(map[string]any{"amount": json.Number("0.30000000000000004")})
	require.Equal(t, "0.30000000000000004", r.money("amount").Decimal().String())
	require.Nil(t, r.Err())
}

func TestAFloat64InAnAmountFieldIsARefusalAndNotARoundedNumber(t *testing.T) {
	// A float64 means something decoded the export without UseNumber, and the
	// precision is already gone.
	r := newRecord(map[string]any{"amount": 25.5})
	r.money("amount")
	require.NotNil(t, r.Err())
	require.Contains(t, r.Err().Message, "UseNumber")
}

func TestEveryAmountTheFixtureCarriesSurvivesExactly(t *testing.T) {
	out := mapped(t, complete(t))
	expected := map[string]string{
		"SQ *COFFEE 1234":            "-25.50",
		"TRANSFER TO SAVINGS":        "-100.00",
		"TRANSFER FROM CHECKING":     "100.00",
		"SUPERMARKET":                "-100.00",
		"STREAMSVC.COM 800-555-0100": "-15.00",
		"PLUMBER":                    "-220.00",
	}
	for statement, amount := range expected {
		require.Equal(t, amount, txnByStatement(t, out, statement).Amount.String(), statement)
	}
	require.Equal(t, "1234.56", out.Months[0].LeftToSpend.String())
	require.Equal(t, "57.50", out.Envelopes[0].RolloverAmount.String())
}

func TestAnIntegerAmountIsExactRatherThanAFloatRoundTrip(t *testing.T) {
	r := newRecord(map[string]any{"amount": json.Number("15000")})
	require.Equal(t, "15000.00", r.money("amount").String())
	require.Nil(t, r.Err())
}

func TestABooleanIsNeverReadAsTheNumberOne(t *testing.T) {
	r := newRecord(map[string]any{"amount": true})
	r.money("amount")
	require.NotNil(t, r.Err())
	require.Contains(t, r.Err().Message, "boolean")
}

func TestAValueThatIsNotAnAmountIsRefusedRatherThanZeroed(t *testing.T) {
	r := newRecord(map[string]any{"amount": "about twenty dollars"})
	require.True(t, r.money("amount").IsZero())
	require.NotNil(t, r.Err())
	require.Contains(t, r.Err().Message, "not an amount")
}

func TestAMissingRequiredAmountIsNamedRatherThanDefaultedToZero(t *testing.T) {
	r := newRecord(map[string]any{})
	r.money("amount")
	require.NotNil(t, r.Err())
	require.Equal(t, "amount", r.Err().Field)

	// A bucket with a documented default is different, and says so by taking
	// one explicitly rather than by falling through the same reader.
	fresh := newRecord(map[string]any{})
	require.True(t, fresh.moneyOr("calculatedIncomeAmount", domain.Zero).IsZero())
	require.Nil(t, fresh.Err())
}

func TestAnAbsentOverrideIsNotAnOverrideOfZero(t *testing.T) {
	r := newRecord(map[string]any{"overwrittenBillsAmount": nil})
	_, present := r.optMoney("overwrittenBillsAmount")
	require.False(t, present)

	set := newRecord(map[string]any{"overwrittenBillsAmount": json.Number("0")})
	amount, present := set.optMoney("overwrittenBillsAmount")
	require.True(t, present)
	require.Equal(t, "0.00", amount.String())
}

func TestASharePriceKeepsItsPlacesBecauseItIsNotMoney(t *testing.T) {
	r := newRecord(map[string]any{"averageCost": json.Number("190.4761904762")})
	rate, present := r.optRate("averageCost")
	require.True(t, present)
	require.Equal(t, "190.4761904762", rate.String())
}
