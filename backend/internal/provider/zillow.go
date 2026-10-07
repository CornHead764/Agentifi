package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// The Zestimate, read from the property's own page. Zillow's address search
// (/homes/<address>_rb/) redirects an address it can place to that property's
// /homedetails/ page and anything else to a results page, so the redirect is
// the match. The page carries the property as Next.js data.

const (
	zillowOrigin   = "https://www.zillow.com"
	zillowDocument = "/"
	// The property page is about a megabyte; the cap only bounds a runaway.
	zillowPageLimit = 16 << 20
)

// errZillowBlocked is the site declining a lookup, which it does to some
// and not to the next from a fresh browser context; the asset stays due and
// is retried later.
var errZillowBlocked = errors.New("zillow: the lookup was declined")

type ZillowProperty struct {
	Browser ValuationBrowser
}

func (z *ZillowProperty) Name() string         { return "zillow" }
func (z *ZillowProperty) AssetTypes() []string { return []string{AssetTypeRealEstate} }
func (z *ZillowProperty) IsConfigured() bool   { return z.Browser != nil }

func (z *ZillowProperty) Estimate(ctx context.Context, subject ValuationSubject) (*ValuationQuote, error) {
	slug := zillowSlug(subject.Address)
	if slug == "" {
		return nil, nil
	}
	ctx, cancel := context.WithTimeout(ctx, lookupTimeout)
	defer cancel()

	page, err := z.lookup(ctx, slug)
	if errors.Is(err, errZillowBlocked) {
		page, err = z.lookup(ctx, slug)
	}
	if errors.Is(err, errZillowBlocked) {
		return nil, fmt.Errorf("zillow: refused twice: %w", ErrValuationRateLimited)
	}
	if err != nil {
		return nil, err
	}
	return zillowQuote(page, subject.Address)
}

type zillowPage struct {
	// URL is where the address search ended.
	URL  *url.URL
	HTML []byte
}

func (z *ZillowProperty) lookup(ctx context.Context, slug string) (zillowPage, error) {
	surface, err := z.Browser(zillowOrigin, zillowDocument)
	if err != nil {
		return zillowPage{}, err
	}
	defer closeSurface(surface)
	return zillowSearch(ctx, lookupFetcher(surface, zillowOrigin, zillowDocument), slug)
}

func zillowSearch(ctx context.Context, fetcher browser.Fetcher, slug string) (zillowPage, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		zillowOrigin+"/homes/"+slug+"_rb/", nil)
	if err != nil {
		return zillowPage{}, fmt.Errorf("zillow: request: %w", err)
	}
	req.Header.Set("Accept", "text/html")
	resp, err := httpx.Read(fetcher, req, zillowPageLimit)
	if err != nil {
		return zillowPage{}, fmt.Errorf("zillow: the address search failed: %w", err)
	}

	if resp.Status == http.StatusForbidden || resp.Status == http.StatusTooManyRequests {
		return zillowPage{}, errZillowBlocked
	}
	if resp.Status >= 400 {
		return zillowPage{}, fmt.Errorf("zillow: the address search answered %d", resp.Status)
	}
	return zillowPage{URL: resp.URL, HTML: resp.Body}, nil
}

// zillowSlug is the address as Zillow's own search box writes it into the
// path: commas dropped, words joined by hyphens.
func zillowSlug(address string) string {
	words := strings.Fields(strings.ReplaceAll(address, ",", " "))
	for i, word := range words {
		words[i] = url.PathEscape(word)
	}
	return strings.Join(words, "-")
}

func zillowQuote(page zillowPage, address string) (*ValuationQuote, error) {
	if page.URL == nil || !strings.HasPrefix(page.URL.Path, "/homedetails/") {
		slog.Info("zillow could not place the address on one property")
		return nil, nil
	}
	property, ok := zillowListingFrom(page.HTML)
	if !ok {
		slog.Warn("zillow's property page carried no property data", "page", page.URL.Path)
		return nil, nil
	}
	// Zillow resolves a close miss to a neighbour rather than failing.
	if want := leadingNumber(address); want != "" && leadingNumber(property.Address.StreetAddress) != want {
		slog.Info("zillow placed the address on a different house")
		return nil, nil
	}

	value, ok := valuationAmount(property.Zestimate)
	if !ok {
		// Never substitute the list price or tax assessment: they are not
		// market value.
		slog.Info("zillow property carries no zestimate", "home_status", property.HomeStatus)
		return nil, nil
	}
	currency := property.Currency
	if currency == "" {
		currency = "USD" // Zillow covers the US only.
	}
	return &ValuationQuote{
		Value:    value,
		Currency: currency,
		Priced:   PricedAs{Address: property.Address.String()},
		Detail: map[string]any{
			"zpid":               detailNumber(property.Zpid),
			"home_status":        property.HomeStatus,
			"rent_zestimate":     detailNumber(property.RentZestimate),
			"living_area":        detailNumber(property.LivingAreaValue),
			"tax_assessed_value": detailNumber(property.ResoFacts.TaxAssessedValue),
		},
	}, nil
}

type zillowListing struct {
	Zpid            json.Number   `json:"zpid"`
	Zestimate       json.Number   `json:"zestimate"`
	RentZestimate   json.Number   `json:"rentZestimate"`
	Currency        string        `json:"currency"`
	LivingAreaValue json.Number   `json:"livingAreaValue"`
	HomeStatus      string        `json:"homeStatus"`
	Address         zillowAddress `json:"address"`
	ResoFacts       struct {
		TaxAssessedValue json.Number `json:"taxAssessedValue"`
	} `json:"resoFacts"`
}

type zillowAddress struct {
	StreetAddress string `json:"streetAddress"`
	City          string `json:"city"`
	State         string `json:"state"`
	Zipcode       string `json:"zipcode"`
}

// String is the address as a letter would carry it, from the parts Zillow gave.
func (a zillowAddress) String() string {
	region := strings.TrimSpace(strings.TrimSpace(a.State) + " " + strings.TrimSpace(a.Zipcode))
	parts := make([]string, 0, 3)
	for _, part := range []string{a.StreetAddress, a.City, region} {
		if part = strings.TrimSpace(part); part != "" {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ", ")
}

var nextDataScript = regexp.MustCompile(`(?s)<script[^>]*\bid="__NEXT_DATA__"[^>]*>(.*?)</script>`)

// zillowListingFrom reads the property out of the page's Next.js data, whose
// gdpClientCache is itself JSON inside a string, keyed by the query that
// filled it.
func zillowListingFrom(html []byte) (zillowListing, bool) {
	match := nextDataScript.FindSubmatch(html)
	if match == nil {
		return zillowListing{}, false
	}
	var next struct {
		Props struct {
			PageProps struct {
				ComponentProps struct {
					GDPClientCache string `json:"gdpClientCache"`
				} `json:"componentProps"`
			} `json:"pageProps"`
		} `json:"props"`
	}
	if err := json.Unmarshal(match[1], &next); err != nil {
		return zillowListing{}, false
	}
	var cache map[string]struct {
		Property *zillowListing `json:"property"`
	}
	if err := json.Unmarshal([]byte(next.Props.PageProps.ComponentProps.GDPClientCache), &cache); err != nil {
		return zillowListing{}, false
	}
	keys := make([]string, 0, len(cache))
	for key := range cache {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	for _, key := range keys {
		if property := cache[key].Property; property != nil {
			return *property, true
		}
	}
	return zillowListing{}, false
}

// leadingNumber is the street number an address starts with, or "".
func leadingNumber(address string) string {
	address = strings.TrimSpace(address)
	end := 0
	for end < len(address) && address[end] >= '0' && address[end] <= '9' {
		end++
	}
	return address[:end]
}
