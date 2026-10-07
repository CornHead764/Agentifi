package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// The valuers are scrapers that run in Camoufox, so a deployment prices
// houses and cars exactly when CAMOUFOX_URL is set; there is nothing else to
// configure.

func TestWithoutCamoufoxNothingCanBePriced(t *testing.T) {
	l := buildLedger(t)
	l.env.Cfg.Browser.CamoufoxURL = ""

	sources := l.alex.get("/accounts/valuation-sources").requireStatus(http.StatusOK).json()
	require.ElementsMatch(t, []any{"real_estate", "vehicle"}, sources["asset_types"])
	require.Empty(t, sources["configured"])
}

func TestALookupWithoutCamoufoxSaysSo(t *testing.T) {
	l := buildLedger(t)
	l.env.Cfg.Browser.CamoufoxURL = ""
	id := valuedCar(l)

	refused := l.alex.post("/accounts/"+id+"/revalue", nil).
		requireStatus(http.StatusServiceUnavailable).json()
	require.Contains(t, refused["detail"], "CAMOUFOX_URL")

	run := l.alex.post("/accounts/revalue?force=1", nil).requireStatus(http.StatusOK).json()
	results := run["results"].([]any)
	require.Len(t, results, 1)
	require.Contains(t, results[0].(map[string]any)["skipped"], "CAMOUFOX_URL")
	require.NotContains(t, results[0].(map[string]any)["skipped"], "no valuation source")
}

func TestWithCamoufoxHousesAndCarsArePriced(t *testing.T) {
	l := buildLedger(t)
	// Set before the engine is first built; nothing connects until a lookup.
	l.env.Cfg.Browser.CamoufoxURL = "ws://camoufox.invalid:9333/test-path"

	sources := l.alex.get("/accounts/valuation-sources").requireStatus(http.StatusOK).json()
	require.ElementsMatch(t, []any{"real_estate", "vehicle"}, sources["configured"])
}

func TestThereIsNoValuationCredentialToAdminister(t *testing.T) {
	admin := newClient(t).as(makeAdmin(t))
	require.Equal(t, http.StatusNotFound, admin.get("/admin/valuation-source").Code)
}
