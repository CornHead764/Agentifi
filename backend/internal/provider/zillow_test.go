package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
)

// An invented property page: the Next.js data with the query cache inside a
// string, as Zillow's own page carries it.
func zillowPropertyPage(t *testing.T, property map[string]any) string {
	t.Helper()
	cache, err := json.Marshal(map[string]any{
		`ViewShowcasePriorityQuery{"zpid":90000001}`: map[string]any{"property": property},
	})
	require.NoError(t, err)
	next, err := json.Marshal(map[string]any{
		"props": map[string]any{"pageProps": map[string]any{
			"componentProps": map[string]any{"gdpClientCache": string(cache)},
		}},
	})
	require.NoError(t, err)
	return `<!doctype html><html><head><title>42 Example Way</title></head><body>` +
		`<script id="__NEXT_DATA__" type="application/json">` + string(next) + `</script></body></html>`
}

func inventedProperty(overrides map[string]any) map[string]any {
	property := map[string]any{
		"zpid":            json.Number("90000001"),
		"zestimate":       json.Number("412300"),
		"rentZestimate":   json.Number("2150"),
		"currency":        "USD",
		"livingAreaValue": json.Number("1640"),
		"homeStatus":      "OTHER",
		"price":           nil,
		"address": map[string]any{
			"streetAddress": "42 Example Way", "city": "Sampleton", "state": "ZZ", "zipcode": "00000",
		},
		"resoFacts": map[string]any{"taxAssessedValue": json.Number("301000")},
	}
	for key, value := range overrides {
		property[key] = value
	}
	return property
}

// zillowSite answers the address search by redirecting to the property page,
// which serves page, or answers blocked times with the site's refusal first.
func zillowSite(t *testing.T, page string, blocked int) (*fakeSite, *[]string) {
	t.Helper()
	var searched []string
	mux := http.NewServeMux()
	mux.HandleFunc("/homes/", func(w http.ResponseWriter, r *http.Request) {
		searched = append(searched, r.URL.EscapedPath())
		if blocked > 0 {
			blocked--
			w.WriteHeader(http.StatusForbidden)
			fmt.Fprint(w, "<html><title>Access to this page has been denied</title></html>")
			return
		}
		http.Redirect(w, r, "/homedetails/42-Example-Way-Sampleton-ZZ-00000/90000001_zpid/", http.StatusMovedPermanently)
	})
	mux.HandleFunc("/homedetails/", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, page)
	})
	return &fakeSite{handler: mux}, &searched
}

func TestTheZestimateIsReadFromThePropertyPage(t *testing.T) {
	site, searched := zillowSite(t, zillowPropertyPage(t, inventedProperty(nil)), 0)
	zillow := &ZillowProperty{Browser: site.open}

	quote, err := zillow.Estimate(context.Background(), ValuationSubject{
		AssetType: AssetTypeRealEstate, Address: "42 Example Way, Sampleton, ZZ",
	})
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Equal(t, "412300.00", quote.Value.String())
	require.Equal(t, "USD", quote.Currency)
	require.Equal(t, PricedAs{Address: "42 Example Way, Sampleton, ZZ 00000"}, quote.Priced)
	require.Equal(t, json.Number("301000"), quote.Detail["tax_assessed_value"])
	require.Equal(t, json.Number("90000001"), quote.Detail["zpid"])
	require.Equal(t, []string{"/homes/42-Example-Way-Sampleton-ZZ_rb/"}, *searched)
	require.Equal(t, []string{"https://www.zillow.com/"}, site.origins)
	require.Equal(t, 1, site.closed, "the lookup's browser context is closed")
}

func TestAListingWithoutAZestimateYieldsNothing(t *testing.T) {
	// A for-sale listing's price and the tax assessment are not market value.
	page := zillowPropertyPage(t, inventedProperty(map[string]any{
		"zestimate": nil, "homeStatus": "FOR_SALE", "price": json.Number("450000"),
	}))
	site, _ := zillowSite(t, page, 0)

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "42 Example Way, Sampleton, ZZ"})
	require.NoError(t, err)
	require.Nil(t, quote)
}

func TestAnAddressZillowCannotPlaceYieldsNothing(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/homes/", func(w http.ResponseWriter, r *http.Request) {
		// A results page carries listings, and Next.js data of its own.
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, zillowPropertyPage(t, inventedProperty(nil)))
	})
	site := &fakeSite{handler: mux}

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "Example Way, Sampleton"})
	require.NoError(t, err)
	require.Nil(t, quote)
}

func TestAPropertyWithAnotherStreetNumberYieldsNothing(t *testing.T) {
	site, _ := zillowSite(t, zillowPropertyPage(t, inventedProperty(nil)), 0)

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "44 Example Way, Sampleton, ZZ"})
	require.NoError(t, err)
	require.Nil(t, quote, "a neighbour's Zestimate is not this house's value")
}

func TestAPropertyPageWithoutItsDataYieldsNothing(t *testing.T) {
	site, _ := zillowSite(t, "<html><body>a page Zillow changed</body></html>", 0)

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "42 Example Way, Sampleton, ZZ"})
	require.NoError(t, err)
	require.Nil(t, quote)
}

func TestARefusedLookupIsTriedOnceMoreInAFreshContext(t *testing.T) {
	site, searched := zillowSite(t, zillowPropertyPage(t, inventedProperty(nil)), 1)

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "42 Example Way, Sampleton, ZZ"})
	require.NoError(t, err)
	require.NotNil(t, quote)
	require.Len(t, *searched, 2)
	require.Equal(t, 2, site.opened, "the second try opens a context of its own")
	require.Equal(t, 2, site.closed)
}

func TestALookupRefusedTwiceIsRateLimited(t *testing.T) {
	site, searched := zillowSite(t, zillowPropertyPage(t, inventedProperty(nil)), 2)

	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "42 Example Way, Sampleton, ZZ"})
	require.ErrorIs(t, err, ErrValuationRateLimited)
	require.ErrorIs(t, err, ErrRateLimited)
	require.Nil(t, quote)
	require.Len(t, *searched, 2, "tried twice, not more")
	require.Equal(t, 2, site.closed)
}

func TestNoAddressOpensNoBrowser(t *testing.T) {
	site := &fakeSite{handler: http.NotFoundHandler()}
	quote, err := (&ZillowProperty{Browser: site.open}).Estimate(context.Background(),
		ValuationSubject{Address: "  , "})
	require.NoError(t, err)
	require.Nil(t, quote)
	require.Zero(t, site.opened)
}

func TestTheSearchSlugIsTheAddressWithoutCommas(t *testing.T) {
	require.Equal(t, "42-Example-Way-Sampleton-ZZ-00000", zillowSlug(" 42 Example Way,  Sampleton, ZZ 00000 "))
	require.Equal(t, "7-Sample-Ct-Unit-%233", zillowSlug("7 Sample Ct Unit #3"))
	require.Equal(t, "", zillowSlug(" , "))
}

func TestTheStreetNumberIsTheAddressesLeadingDigits(t *testing.T) {
	require.Equal(t, "42", leadingNumber(" 42 Example Way"))
	require.Equal(t, "1200", leadingNumber("1200B Sample Rd"))
	require.Equal(t, "", leadingNumber("Example Way"))
}
