package provider

import (
	"context"
	"os"
	"strconv"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/config"
)

// The valuers against the real sites, through a real Camoufox. The inputs are
// somebody's own house and car, so they come from the environment and never
// from the repository:
//
//	CAMOUFOX_URL=ws://… PLAYWRIGHT_DRIVER_PATH=… \
//	AGENTIFI_LIVE_ZILLOW_ADDRESS='…' \
//	AGENTIFI_LIVE_KBB_VIN=… AGENTIFI_LIVE_KBB_MILEAGE=… \
//	go test ./internal/provider/ -run Live -v
//
// AGENTIFI_LIVE_KBB_MILEAGE may be left unset to price at KBB's typical
// mileage. What the sites answer is logged rather than asserted, since it moves.

func liveValuationBrowser(t *testing.T) ValuationBrowser {
	t.Helper()
	settings, err := config.LoadBrowser()
	require.NoError(t, err)
	if settings.CamoufoxURL == "" {
		t.Skip("set CAMOUFOX_URL to look values up through a real Camoufox")
	}
	engine := browser.NewEngine(settings)
	t.Cleanup(func() { _ = engine.Close() })
	return engine.OpenFirefoxFetchSurface
}

func TestLiveZillowPricesTheHouse(t *testing.T) {
	address := os.Getenv("AGENTIFI_LIVE_ZILLOW_ADDRESS")
	if address == "" {
		t.Skip("set AGENTIFI_LIVE_ZILLOW_ADDRESS to price a house")
	}
	zillow := &ZillowProperty{Browser: liveValuationBrowser(t)}

	quote, err := zillow.Estimate(context.Background(), ValuationSubject{
		AssetType: AssetTypeRealEstate, Address: address,
	})
	require.NoError(t, err)
	require.NotNil(t, quote, "zillow gave no estimate for the address")
	t.Logf("zestimate %s %s, detail %v", quote.Value, quote.Currency, quote.Detail)
}

func TestLiveKelleyBlueBookPricesTheCar(t *testing.T) {
	vin := os.Getenv("AGENTIFI_LIVE_KBB_VIN")
	if vin == "" {
		t.Skip("set AGENTIFI_LIVE_KBB_VIN to price a car")
	}
	mileage := -1
	if raw := os.Getenv("AGENTIFI_LIVE_KBB_MILEAGE"); raw != "" {
		var err error
		mileage, err = strconv.Atoi(raw)
		require.NoError(t, err, "AGENTIFI_LIVE_KBB_MILEAGE is a whole number of miles")
	}
	kbb := &KelleyBlueBook{Browser: liveValuationBrowser(t)}

	quote, err := kbb.Estimate(context.Background(), ValuationSubject{
		AssetType: AssetTypeVehicle, VIN: vin, Mileage: mileage,
	})
	require.NoError(t, err)
	require.NotNil(t, quote, "kbb gave no estimate for the VIN")
	t.Logf("private party (good) %s %s, detail %v", quote.Value, quote.Currency, quote.Detail)
}
