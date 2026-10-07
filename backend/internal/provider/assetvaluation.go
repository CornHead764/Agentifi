package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// External estimates of a house (by address, from Zillow) or car (by VIN and
// mileage, from Kelley Blue Book). Neither has a public API, so both are read
// from the sites' own pages and endpoints, run in Camoufox, and both are
// absent without it. Every parse path yields "no estimate" rather than a
// different number, so a hollow response leaves the recorded value alone.

type ValuationSubject struct {
	AssetType string
	Address   string
	VIN       string
	// Mileage is negative when unknown, since zero miles is a real odometer
	// reading on a new car.
	Mileage int
}

type ValuationQuote struct {
	Value    domain.Money
	Currency string
	AsOf     domain.Date
	Priced   PricedAs
	// Detail is provider-specific extras; never load-bearing.
	Detail map[string]any
}

// PricedAs is what the source took the asset to be, so that a mistyped input
// shows in the result as well as in the number.
type PricedAs struct {
	// A vehicle, as the source decoded its VIN.
	Year, Make, Model, Trim string
	Mileage                 int
	HasMileage              bool
	// TypicalMileage is set when no odometer reading was given and the
	// source priced the car at its own typical mileage.
	TypicalMileage bool
	// Address is the house the source matched.
	Address string
	// Low and High are the source's range around the value.
	Low, High domain.Money
	HasRange  bool
}

type AssetValuationProvider interface {
	Name() string
	AssetTypes() []string
	IsConfigured() bool
	// Estimate answers nil, nil when the subject could not be priced, which
	// must never overwrite the recorded value.
	Estimate(ctx context.Context, subject ValuationSubject) (*ValuationQuote, error)
}

// ErrValuationRateLimited also covers a site declining the lookup; the
// asset stays due and is retried later.
var ErrValuationRateLimited = fmt.Errorf("asset valuation: %w", ErrRateLimited)

const (
	AssetTypeRealEstate = "real_estate"
	AssetTypeVehicle    = "vehicle"
)

// ValuationBrowser opens a Camoufox page at origin, sitting on document, in a
// browser context of its own; the surface's Close ends that context.
type ValuationBrowser func(origin, document string) (browser.FetchSurface, error)

// lookupTimeout bounds one lookup, page loads included, so a stalled site
// cannot hold the daily pass.
const lookupTimeout = 2 * time.Minute

// lookupFetcher makes calls from inside the surface's page. The caller closes
// the surface itself: a lookup may end before its first call.
func lookupFetcher(surface browser.FetchSurface, origin, document string) browser.Fetcher {
	return &browser.PageFetcher{
		Origin: origin, Document: document,
		Open: func() (browser.FetchSurface, error) { return surface, nil },
	}
}

func closeSurface(surface browser.FetchSurface) {
	if surface.Close != nil {
		_ = surface.Close()
	}
}

// valuationAmount treats a zero or negative figure as no estimate, and
// converts from the number's literal text, never through float64.
func valuationAmount(raw any) (domain.Money, bool) {
	value, err := domain.MoneyFromJSONValue(raw)
	if err != nil || !value.IsPositive() {
		return domain.Zero, false
	}
	return value, true
}

// detailNumber is an absent figure as nil: an empty json.Number marshals as 0.
func detailNumber(n json.Number) any {
	if n == "" {
		return nil
	}
	return n
}

// Valuers indexes the valuation providers by asset type. A nil browser means
// Camoufox is not configured, and then there are none.
func Valuers(open ValuationBrowser) map[string]AssetValuationProvider {
	out := map[string]AssetValuationProvider{}
	for _, p := range []AssetValuationProvider{
		&ZillowProperty{Browser: open},
		&KelleyBlueBook{Browser: open},
	} {
		if !p.IsConfigured() {
			continue
		}
		for _, assetType := range p.AssetTypes() {
			out[assetType] = p
		}
	}
	return out
}

// ValuationAssetTypes lists every externally valuable type, configured or not.
func ValuationAssetTypes() []string {
	return []string{AssetTypeRealEstate, AssetTypeVehicle}
}
