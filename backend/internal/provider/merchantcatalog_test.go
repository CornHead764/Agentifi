package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A stand-in for Instacart's storefront: a guest session, a search that
// answers item ids, and the items behind them.
func costcoListing(t *testing.T) (*CostcoCatalog, *atomic.Int32, *atomic.Int32) {
	t.Helper()
	var sessions, refusals atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Contains(t, r.UserAgent(), "Chrome", "a browser's own name, or the site drops the connection")
		w.Header().Set("Content-Type", "application/json")
		if r.Method == http.MethodPost {
			sessions.Add(1)
			_, _ = w.Write([]byte(`{"data":{"createGuestUser":{"token":"guest-` + string(rune('0'+sessions.Load())) + `"}}}`))
			return
		}
		cookie, err := r.Cookie("__Host-instacart_sid")
		require.NoError(t, err)
		if cookie.Value == "guest-1" && refusals.Load() == 0 {
			// The first session is stale: refused once, renewed, never again.
			refusals.Add(1)
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		var variables map[string]any
		require.NoError(t, json.Unmarshal([]byte(r.URL.Query().Get("variables")), &variables))
		switch r.URL.Query().Get("operationName") {
		case "SearchResultsPlacements":
			switch variables["query"] {
			case "12345", "0012345":
				_, _ = w.Write([]byte(`{"data":{"searchResultsPlacements":{"placements":[
				  {"content":{}},
				  {"content":{"itemIds":["items_359-90000001","items_359-90000002"]}}]}}}`))
			case "9999999":
				_, _ = w.Write([]byte(`{"data":{"searchResultsPlacements":{"placements":[
				  {"content":{"itemIds":["items_359-90000001"]}}]}}}`))
			case "broken":
				_, _ = w.Write([]byte(`{"errors":[{"message":"PersistedQueryNotFound"}]}`))
			default:
				_, _ = w.Write([]byte(`{"data":{"searchResultsPlacements":{"placements":[]}}}`))
			}
		case "Items":
			_, _ = w.Write([]byte(`{"data":{"items":[
			  {"id":"items_359-90000001","productId":"90000001","name":"Example Lemon Chicken, 32 oz","size":"32 oz",
			   "brandName":"example farms","viewSection":{"retailerReferenceCodeString":"1000001",
			   "itemImage":{"url":"https://img.example/lemon.jpg"},"trackingProperties":{"product_category_name":"Other Prepared Chicken"}},
			   "price":{"viewSection":{"priceString":"$17.00"}}},
			  {"id":"items_359-90000002","productId":"90000002","name":"Kirkland Signature Roast Chicken","size":"each",
			   "brandName":"kirkland signature","viewSection":{"retailerReferenceCodeString":"12345",
			   "itemImage":{"url":"https://img.example/chicken.jpg"},"trackingProperties":{"product_category_name":"Deli"}},
			   "price":{"viewSection":{"priceString":"$5.49"}}}]}}`))
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(server.Close)
	return &CostcoCatalog{BaseURL: server.URL, ShopID: "0000", ZoneID: "0", PostalCode: "00000"}, &sessions, &refusals
}

func TestACostcoItemsPriceIsReadAsMoneyOrNotAtAll(t *testing.T) {
	for _, tc := range []struct {
		priceString string
		want        string
		hasPrice    bool
	}{
		{"$5.49", "5.49", true},
		{"$1,234.50", "1234.50", true},
		// Bad thousands grouping is refused rather than read with the comma
		// dropped, the same as any other amount of text.
		{"$1,2.00", "", false},
		{"", "", false},
	} {
		item := costcoItem{Name: "Example"}
		item.Price.ViewSection.PriceString = tc.priceString
		entry := item.entry()
		require.Equal(t, tc.hasPrice, entry.HasPrice, tc.priceString)
		if tc.hasPrice {
			require.Equal(t, tc.want, entry.Price.String(), tc.priceString)
		}
	}
}

func TestTheCostcoCatalogFindsAnItemByItsNumberAndVerifiesTheHit(t *testing.T) {
	catalog, sessions, _ := costcoListing(t)
	ctx := context.Background()

	entry, found, err := catalog.Lookup(ctx, domain.MerchantCostco, "12345")
	require.NoError(t, err)
	require.True(t, found)
	require.Equal(t, "Kirkland Signature Roast Chicken", entry.Title)
	require.Equal(t, "kirkland signature", entry.Brand)
	require.Equal(t, "each", entry.Size)
	require.Equal(t, "Deli", entry.Category)
	require.Equal(t, "https://img.example/chicken.jpg", entry.ImageURL)
	require.Equal(t, "https://sameday.costco.com/store/costco/products/90000002", entry.URL)
	require.True(t, entry.HasPrice)
	require.Equal(t, "5.49", entry.Price.String())
	require.Equal(t, "costco-sameday", entry.Source)
	require.Equal(t, "items_359-90000002", entry.SourceRef)
	require.True(t, strings.Contains(string(entry.Raw), `"retailerReferenceCodeString":"12345"`), "the listing's own record is kept")
	// The stale first session was renewed once and then kept.
	require.EqualValues(t, 2, sessions.Load())

	// A search that returns other items, none carrying the number, is a miss —
	// the hit is verified by the reference code, never taken from the ranking.
	_, found, err = catalog.Lookup(ctx, domain.MerchantCostco, "9999999")
	require.NoError(t, err)
	require.False(t, found)
	require.EqualValues(t, 2, sessions.Load(), "the kept session serves every later lookup")

	// Nothing at all is a miss, not an error.
	_, found, err = catalog.Lookup(ctx, domain.MerchantCostco, "1")
	require.NoError(t, err)
	require.False(t, found)

	// A number the receipt printed with leading zeros is the same number.
	_, found, err = catalog.Lookup(ctx, domain.MerchantCostco, "0012345")
	require.NoError(t, err)
	require.True(t, found, "search answers what it answers; the reference code is compared as a number")

	// A refused persisted query is a failure to ask, and says so.
	_, _, err = catalog.Lookup(ctx, domain.MerchantCostco, "broken")
	require.ErrorContains(t, err, "PersistedQueryNotFound")

	_, _, err = catalog.Lookup(ctx, domain.MerchantAmazon, "B000")
	require.ErrorIs(t, err, ErrNoCatalog)
}
