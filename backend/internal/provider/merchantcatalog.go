package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
)

// Item-number lookups for receipt lines; each answer is kept for good in
// store.CatalogItem, so this is asked once per number.

type CatalogEntry struct {
	Title     string
	Brand     string
	Size      string
	Category  string
	ImageURL  string
	URL       string
	Price     domain.Money
	HasPrice  bool
	Source    string
	SourceRef string
	// Raw is kept so an unmapped field can be read later without asking again.
	Raw json.RawMessage
}

type MerchantCatalog interface {
	// Lookup's err is a failure to ask, and says nothing about the number.
	Lookup(ctx context.Context, merchant domain.MerchantID, sku string) (CatalogEntry, bool, error)
}

var ErrNoCatalog = errors.New("provider: no catalog for this merchant")

// CostcoCatalog reads Instacart's sameday.costco.com, which (unlike
// costco.com) answers a plain server request; records carry the item number
// back as the retailer reference code, so a hit is verified. The API is
// persisted-query GraphQL: the hashes break when Instacart ships a new build,
// answering PersistedQueryNotFound.
type CostcoCatalog struct {
	BaseURL string
	Client  *http.Client
	// The warehouse sets the price only; the catalog is the same everywhere.
	// The listing still answers only for a warehouse, so the operator names
	// one (config.CostcoStore) and the catalogue is not built without it.
	ShopID, ZoneID, PostalCode string

	mu    sync.Mutex
	token string
}

var (
	costcoGuestUserHash = "60933aab54d6b3f2add1fec396506dae6ef9b6a1891effbdaf39db129e787446"
	costcoSearchHash    = "bc1d0bf4c510947cd08f43304bbc4bb3bb85d8dfcfaf4f06f230cb7e1a30adfd"
	costcoItemsHash     = "0569d5997f69659900bde2d8b65943a3968b7d4a52e7851d564f7f35d4596f85"
)

const (
	costcoCatalogSource  = "costco-sameday"
	costcoCatalogHome    = "https://sameday.costco.com"
	costcoCatalogAgent   = "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/140.0.0.0 Safari/537.36"
	costcoCatalogResults = 8
)

func NewCostcoCatalog() *CostcoCatalog {
	return &CostcoCatalog{BaseURL: costcoCatalogHome}
}

func (c *CostcoCatalog) client() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *CostcoCatalog) base() string {
	if c.BaseURL == "" {
		return costcoCatalogHome
	}
	return strings.TrimRight(c.BaseURL, "/")
}

func (c *CostcoCatalog) Lookup(ctx context.Context, merchant domain.MerchantID, sku string) (CatalogEntry, bool, error) {
	if merchant != domain.MerchantCostco {
		return CatalogEntry{}, false, ErrNoCatalog
	}
	sku = strings.TrimSpace(sku)
	if sku == "" {
		return CatalogEntry{}, false, nil
	}
	ids, err := c.search(ctx, sku)
	if err != nil {
		return CatalogEntry{}, false, err
	}
	if len(ids) == 0 {
		return CatalogEntry{}, false, nil
	}
	items, err := c.items(ctx, ids)
	if err != nil {
		return CatalogEntry{}, false, err
	}
	for _, item := range items {
		if sameItemNumber(item.reference(), sku) {
			return item.entry(), true, nil
		}
	}
	return CatalogEntry{}, false, nil
}

// sameItemNumber ignores leading zeros, which the listing drops.
func sameItemNumber(a, b string) bool {
	trim := func(s string) string {
		s = strings.TrimLeft(strings.TrimSpace(s), "0")
		if s == "" {
			return "0"
		}
		return s
	}
	return a != "" && trim(a) == trim(b)
}

func (c *CostcoCatalog) search(ctx context.Context, query string) ([]string, error) {
	variables := map[string]any{
		"query": query, "shopId": c.ShopID, "postalCode": c.PostalCode, "zoneId": c.ZoneID,
		"pageViewId": uuid.NewString(), "first": costcoCatalogResults,
	}
	var out struct {
		Data struct {
			Placements struct {
				Placements []struct {
					Content struct {
						ItemIDs []string `json:"itemIds"`
					} `json:"content"`
				} `json:"placements"`
			} `json:"searchResultsPlacements"`
		} `json:"data"`
	}
	if err := c.query(ctx, "SearchResultsPlacements", costcoSearchHash, variables, &out); err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var ids []string
	for _, placement := range out.Data.Placements.Placements {
		for _, id := range placement.Content.ItemIDs {
			if !seen[id] && len(ids) < costcoCatalogResults {
				seen[id] = true
				ids = append(ids, id)
			}
		}
	}
	return ids, nil
}

type costcoItem struct {
	ID          string `json:"id"`
	ProductID   string `json:"productId"`
	Name        string `json:"name"`
	Size        string `json:"size"`
	BrandName   string `json:"brandName"`
	ViewSection struct {
		Reference string `json:"retailerReferenceCodeString"`
		Image     struct {
			URL string `json:"url"`
		} `json:"itemImage"`
		Tracking struct {
			Category string `json:"product_category_name"`
		} `json:"trackingProperties"`
	} `json:"viewSection"`
	Price struct {
		ViewSection struct {
			PriceString string `json:"priceString"`
		} `json:"viewSection"`
	} `json:"price"`
	raw json.RawMessage
}

func (i costcoItem) reference() string { return i.ViewSection.Reference }

func (i costcoItem) entry() CatalogEntry {
	out := CatalogEntry{
		Title: strings.TrimSpace(i.Name), Brand: strings.TrimSpace(i.BrandName), Size: strings.TrimSpace(i.Size),
		Category: strings.TrimSpace(i.ViewSection.Tracking.Category), ImageURL: i.ViewSection.Image.URL,
		Source: costcoCatalogSource, SourceRef: i.ID, Raw: i.raw,
	}
	if i.ProductID != "" {
		out.URL = costcoCatalogHome + "/store/costco/products/" + url.PathEscape(i.ProductID)
	}
	if amount, ok := domain.ParseMoneyText(i.Price.ViewSection.PriceString); ok {
		out.Price, out.HasPrice = amount, true
	}
	return out
}

func (c *CostcoCatalog) items(ctx context.Context, ids []string) ([]costcoItem, error) {
	variables := map[string]any{"ids": ids, "shopId": c.ShopID, "zoneId": c.ZoneID, "postalCode": c.PostalCode}
	var out struct {
		Data struct {
			Items []json.RawMessage `json:"items"`
		} `json:"data"`
	}
	if err := c.query(ctx, "Items", costcoItemsHash, variables, &out); err != nil {
		return nil, err
	}
	items := make([]costcoItem, 0, len(out.Data.Items))
	for _, raw := range out.Data.Items {
		var item costcoItem
		if err := json.Unmarshal(raw, &item); err != nil {
			continue
		}
		item.raw = raw
		items = append(items, item)
	}
	return items, nil
}

// query takes a new guest session once if the held one is refused.
func (c *CostcoCatalog) query(ctx context.Context, operation, hash string, variables map[string]any, out any) error {
	token, err := c.session(ctx, false)
	if err != nil {
		return err
	}
	res, err := httpx.RetryOnceOnAuth(ctx,
		func(ctx context.Context) (httpx.Response, error) {
			return c.get(ctx, operation, hash, variables, token)
		},
		func(ctx context.Context) (err error) {
			token, err = c.session(ctx, true)
			return err
		})
	if err != nil {
		return err
	}
	if res.Refused() {
		return fmt.Errorf("provider: the Costco catalog refused a fresh guest session for %s", operation)
	}
	if !res.OK() {
		return fmt.Errorf("provider: the Costco catalog answered %s: %w", operation, res.Err())
	}
	var envelope struct {
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(res.Body, &envelope); err != nil {
		return fmt.Errorf("provider: the Costco catalog did not answer JSON to %s: %s", operation, res.Excerpt())
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("provider: the Costco catalog refused %s: %s", operation, envelope.Errors[0].Message)
	}
	return json.Unmarshal(res.Body, out)
}

func (c *CostcoCatalog) get(ctx context.Context, operation, hash string, variables map[string]any, token string) (httpx.Response, error) {
	vars, _ := json.Marshal(variables)
	ext, _ := json.Marshal(map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": hash}})
	params := url.Values{"operationName": {operation}, "variables": {string(vars)}, "extensions": {string(ext)}}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.base()+"/graphql?"+params.Encode(), nil)
	if err != nil {
		return httpx.Response{}, err
	}
	req.Header.Set("User-Agent", costcoCatalogAgent)
	req.Header.Set("Accept", "application/json")
	req.AddCookie(&http.Cookie{Name: "__Host-instacart_sid", Value: token})
	res, err := httpx.Read(c.client(), req, 4<<20)
	if err != nil {
		return res, fmt.Errorf("provider: the Costco catalog could not be reached: %w", err)
	}
	return res, nil
}

func (c *CostcoCatalog) session(ctx context.Context, renew bool) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.token != "" && !renew {
		return c.token, nil
	}
	body, _ := json.Marshal(map[string]any{
		"operationName": "HomepagePbiCreateGuestUserWithPostalCodeMutation",
		"variables":     map[string]any{"postalCode": ""},
		"extensions":    map[string]any{"persistedQuery": map[string]any{"version": 1, "sha256Hash": costcoGuestUserHash}},
	})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base()+"/graphql", strings.NewReader(string(body)))
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", costcoCatalogAgent)
	req.Header.Set("Content-Type", "application/json")
	res, err := httpx.Read(c.client(), req, 1<<20)
	if err != nil {
		return "", fmt.Errorf("provider: the Costco catalog could not be reached: %w", err)
	}
	var out struct {
		Data struct {
			Guest struct {
				Token string `json:"token"`
			} `json:"createGuestUser"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res.Body, &out); err != nil || out.Data.Guest.Token == "" {
		return "", fmt.Errorf("provider: the Costco catalog gave no guest session (HTTP %d): %s", res.Status, res.Excerpt())
	}
	c.token = out.Data.Guest.Token
	return c.token, nil
}
