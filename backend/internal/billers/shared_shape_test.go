package billers

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestAShapeIsKeysAndTypesAndNoValues(t *testing.T) {
	answer := map[string]any{
		"isAutoPay":   true,
		"currentDate": "9/19/2026",
		"plan":        "Monthly (Plan Z)",
		"amount":      json.Number("12.34"),
		"rows":        []any{map[string]any{"dueOn": "2026-10-15", "paid": nil}},
		"empty":       []any{},
		"Q0000098765": map[string]any{"amount": 1.5},
	}

	require.Equal(t,
		"{<id>: {amount: number}, amount: number, currentDate: date, empty: [], isAutoPay: bool, "+
			"plan: string, rows: [1 × {dueOn: date, paid: null}]}",
		Shape(answer))
	require.Equal(t, "[2 × {a: number}]", Shape([]map[string]any{{"a": 1.0}, {"a": 2.0}}))
	require.Equal(t, "null", Shape(nil))
}

func TestAnAspNetJsonDateIsTheDayItNames(t *testing.T) {
	// Midnight in the eastern US, with and without its offset,
	// and as a JSON string escapes it.
	require.Equal(t, "2026-09-15", ISODate("/Date(1789444800000)/"))
	require.Equal(t, "2026-09-15", ISODate("/Date(1789444800000-0400)/"))
	require.Equal(t, "2026-09-15", ISODate(`\/Date(1789444800000)\/`))
	require.Equal(t, "", ISODate("/Date(soon)/"))
}
