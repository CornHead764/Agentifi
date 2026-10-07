package billers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestAnAmountIsReadAsTheProviderWroteItWhateverCarriedIt(t *testing.T) {
	for _, tc := range []struct {
		name string
		in   any
		want string
	}{
		{"a string", "$1,234.56", "1234.56"},
		{"a credit in parentheses", "(12.00)", "-12.00"},
		{"a JSON number kept as text", json.Number("120.00"), "120.00"},
		{"a page's number, already a float", 120.00, "120.00"},
		{"a whole number", 87, "87.00"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := Amount(tc.in)
			require.True(t, ok)
			require.Equal(t, tc.want, got.String())
		})
	}
}

func TestAMillFigureIsRoundedHalfUpNotByItsBinaryRepresentation(t *testing.T) {
	// 1.005 has no exact float64; %.2f of it prints 1.00.
	got, ok := Amount("1.005")
	require.True(t, ok)
	require.Equal(t, "1.01", got.String())

	fromJSON, ok := Amount(json.Number("123.455"))
	require.True(t, ok)
	require.Equal(t, "123.46", fromJSON.String())
}

func TestAnAmountThatIsNotOneIsRefusedRatherThanReadAsZero(t *testing.T) {
	for _, in := range []any{nil, "", true, "see statement", "$10 - $20", "12-34"} {
		_, ok := Amount(in)
		require.Falsef(t, ok, "%#v", in)
	}
}

func TestAJSONBodyKeepsItsNumbersAsText(t *testing.T) {
	body, ok := decodeBody([]byte(`{"totalAmountDue": 123.455}`))
	require.True(t, ok)
	require.IsType(t, json.Number(""), body["totalAmountDue"])
}

func TestOnlyAPositiveFigureIsOwed(t *testing.T) {
	require.True(t, Owes(domain.MustFromString("0.01")))
	require.False(t, Owes(domain.Zero))
	require.False(t, Owes(domain.MustFromString("-5.00")))
}

func TestAnErieBillIsSettledOnlyWhenItsPaymentsCoverItExactly(t *testing.T) {
	owed := domain.MustFromString("120.00")
	paid := []eriePayment{
		{on: "2026-08-01", amount: domain.MustFromString("60.00")},
		{on: "2026-08-15", amount: domain.MustFromString("59.99")},
	}
	require.False(t, erieSettled(paid, "2026-08-01", owed), "a cent short is not paid")

	paid[1].amount = domain.MustFromString("60.00")
	require.True(t, erieSettled(paid, "2026-08-01", owed))

	require.False(t, erieSettled(paid, "2026-08-02", owed), "a payment before the bill's day is not its")
}
