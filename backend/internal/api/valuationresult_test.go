package api

import (
	"encoding/json"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
)

func pricedAsJSON(t *testing.T, result service.ValuationResult) map[string]any {
	t.Helper()
	raw, err := json.Marshal(valuationRunResponse([]service.ValuationResult{result}))
	require.NoError(t, err)
	var decoded struct {
		Results []map[string]any `json:"results"`
	}
	require.NoError(t, json.Unmarshal(raw, &decoded))
	require.Len(t, decoded.Results, 1)
	priced, _ := decoded.Results[0]["priced_as"].(map[string]any)
	return priced
}

func TestARevaluationSaysWhatCarItPriced(t *testing.T) {
	priced := pricedAsJSON(t, service.ValuationResult{
		AccountID: uuid.New(), Name: "Car 1", Source: "kbb",
		Estimate: domain.MustFromString("17500.00"), HasEstimate: true,
		Priced: &provider.PricedAs{
			Year: "2019", Make: "Examplemotors", Model: "Roadster",
			Mileage: 41000, HasMileage: true, TypicalMileage: true,
			Low: domain.MustFromString("16100"), High: domain.MustFromString("18900"), HasRange: true,
		},
	})
	require.Equal(t, map[string]any{
		"year": "2019", "make": "Examplemotors", "model": "Roadster", "trim": nil,
		"mileage": float64(41000), "typical_mileage": true, "address": nil,
		// Trap 1: amounts cross the wire as strings.
		"low": "16100.00", "high": "18900.00",
	}, priced)
}

func TestARevaluationSaysWhatHouseItMatched(t *testing.T) {
	priced := pricedAsJSON(t, service.ValuationResult{
		AccountID: uuid.New(), Name: "House 1", Source: "zillow",
		Estimate: domain.MustFromString("412300.00"), HasEstimate: true,
		Priced: &provider.PricedAs{Address: "42 Example Way, Sampleton, ZZ 00000"},
	})
	require.Equal(t, "42 Example Way, Sampleton, ZZ 00000", priced["address"])
	require.Nil(t, priced["mileage"])
	require.Nil(t, priced["low"])
	require.Nil(t, priced["high"])
}

func TestASkippedRevaluationPricedNothing(t *testing.T) {
	priced := pricedAsJSON(t, service.ValuationResult{
		AccountID: uuid.New(), Name: "Car 2", Skipped: "the source had no estimate for this asset",
	})
	require.Nil(t, priced)
}
