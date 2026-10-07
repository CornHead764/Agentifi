package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// An estimate is looked up by an address, a VIN and an odometer; one taken
// for another of them says nothing about this asset, so editing any of them
// makes it due at once rather than after StaleAfterDays.

func valuedCar(l *ledger) string {
	l.t.Helper()
	id := newAccount(l, "Car 1", "asset", "vehicle", "18000.00")
	l.alex.patch("/accounts/"+id, map[string]any{
		"vehicle_vin": "TESTVIN0000000002", "vehicle_mileage": 40000,
		"vehicle_mileage_as_of": "2026-05-01", "vehicle_miles_per_year": 9000,
	}).requireStatus(http.StatusOK)

	account, err := l.env.DB.GetAccount(l.t.Context(),
		store.SpaceIDOf(l.id("space")), uuid.MustParse(id))
	require.NoError(l.t, err)
	valuedAt := time.Date(2026, time.June, 1, 12, 0, 0, 0, time.UTC)
	account.ValuationSource, account.ValuedAt = "test-valuer", &valuedAt
	require.NoError(l.t, l.env.DB.UpdateAccount(l.t.Context(), account.SpaceID, &account))
	return id
}

func TestEditingWhatAnEstimateIsLookedUpByMakesTheAssetDue(t *testing.T) {
	for field, value := range map[string]any{
		"vehicle_vin":            "TESTVIN0000000003",
		"vehicle_mileage":        41000,
		"vehicle_mileage_as_of":  "2026-06-01",
		"vehicle_miles_per_year": 12000,
		"property_address":       "42 Example Way, Sampleton, ZZ 00000",
	} {
		t.Run(field, func(t *testing.T) {
			l := buildLedger(t)
			id := valuedCar(l)
			body := l.alex.patch("/accounts/"+id, map[string]any{field: value}).
				requireStatus(http.StatusOK).json()
			require.Nil(t, body["valued_at"])
		})
	}
}

func TestEditingAnythingElseKeepsTheValuation(t *testing.T) {
	l := buildLedger(t)
	id := valuedCar(l)

	body := l.alex.patch("/accounts/"+id, map[string]any{
		"name": "Car 1 renamed", "vehicle_mileage": 40000,
	}).requireStatus(http.StatusOK).json()
	require.NotNil(t, body["valued_at"], "restating the same reading changes nothing")
	require.Equal(t, "test-valuer", body["valuation_source"])
}
