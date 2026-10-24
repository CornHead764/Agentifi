package store

import (
	"context"
	"encoding/json"
	"sort"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The merchant catalog: what an item number means. Keyed by merchant and
// number with no space, because an item number means the same in every
// household and outlives the account that bought it. Written by
// service.Merchants.EnrichCatalog, read beside every order item.

// A catalog row's status.
const (
	CatalogFound   = "found"
	CatalogMissing = "missing"
)

// CatalogItem is one number's meaning, or the record that it had none when
// last asked.
type CatalogItem struct {
	Merchant      domain.MerchantID
	SKU           string
	Status        string
	Title         string
	Brand         string
	Size          string
	Category      string
	ImageURL      string
	URL           string
	Price         domain.Money
	HasPrice      bool
	PriceCurrency string
	Source        string
	SourceRef     string
	Raw           json.RawMessage
	LookedUpAt    time.Time
	FoundAt       *time.Time
}

// UpsertCatalogItem writes what a lookup learned. The catalog only learns: a
// lookup that found nothing records the ask and keeps what an earlier one
// found.
func (s *Store) UpsertCatalogItem(ctx context.Context, one *CatalogItem) error {
	db, err := s.server()
	if err != nil {
		return err
	}
	if one.LookedUpAt.IsZero() {
		one.LookedUpAt = fromUnixMillis(db.Now())
	}
	var price, currency any
	if one.HasPrice {
		code := one.PriceCurrency
		if code == "" {
			code = "USD" // Existing catalogue providers quote US storefront prices.
		}
		minor, err := toMinor(one.Price.Decimal(), code)
		if err != nil {
			return err
		}
		price, currency = minor, code
	}
	var raw any
	if len(one.Raw) > 0 {
		raw = string(one.Raw)
	}
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO merchant_catalog
		     (merchant, sku, status, title, brand, size, category, image_url, url, price, price_currency, source, source_ref,
		      raw, looked_up_at, found_at, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT (merchant, sku) DO UPDATE SET
		     status        = CASE WHEN EXCLUDED.status = 'found' THEN 'found' ELSE merchant_catalog.status END,
		     title         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.title ELSE merchant_catalog.title END,
		     brand         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.brand ELSE merchant_catalog.brand END,
		     size          = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.size ELSE merchant_catalog.size END,
		     category      = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.category ELSE merchant_catalog.category END,
		     image_url     = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.image_url ELSE merchant_catalog.image_url END,
		     url           = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.url ELSE merchant_catalog.url END,
		     price         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.price ELSE merchant_catalog.price END,
		     price_currency = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.price_currency ELSE merchant_catalog.price_currency END,
		     source        = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.source ELSE merchant_catalog.source END,
		     source_ref    = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.source_ref ELSE merchant_catalog.source_ref END,
		     raw           = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.raw ELSE merchant_catalog.raw END,
		     found_at      = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.found_at ELSE merchant_catalog.found_at END,
		     looked_up_at  = EXCLUDED.looked_up_at,
		     updated_at    = EXCLUDED.updated_at`,
		string(one.Merchant), one.SKU, one.Status, one.Title, one.Brand, one.Size, one.Category, one.ImageURL,
		one.URL, price, currency, one.Source, one.SourceRef, raw,
		unixMillis(one.LookedUpAt), nullableMillis(one.FoundAt), db.Now(), db.Now())
	return wrap("store: upsert catalog item", err)
}

// CatalogSKUsToLookUp is every number on a merchant's orders, in any space,
// that the catalog has never been asked about — or was asked about, found
// nothing, and has not been asked since retryBefore. Newest purchases first.
func (s *Store) CatalogSKUsToLookUp(
	ctx context.Context, merchant domain.MerchantID, retryBefore time.Time, limit int,
) ([]string, error) {
	db, err := s.server()
	if err != nil {
		return nil, err
	}
	known, err := sqliteQueryAll(ctx, db.Read(), "store: catalog known numbers", scanValue[string],
		`SELECT sku FROM merchant_catalog WHERE merchant = ? AND NOT (status = 'missing' AND looked_up_at < ?)`,
		string(merchant), unixMillis(retryBefore))
	if err != nil {
		return nil, err
	}
	excluded := make(map[string]bool, len(known))
	for _, sku := range known {
		excluded[sku] = true
	}
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, err
	}
	type purchase struct{ sku, date string }
	newest := make(map[string]string)
	for _, id := range ids {
		household, err := s.household(ctx, id.UUID())
		if err != nil {
			return nil, err
		}
		rows, err := sqliteQueryAll(ctx, household.Read(), "store: catalog household purchases", func(row scanner) (purchase, error) {
			var p purchase
			err := row.Scan(&p.sku, &p.date)
			return p, err
		}, `SELECT i.sku, MAX(o.ordered_on) FROM merchant_order_items i JOIN merchant_orders o ON o.id = i.order_id
		    WHERE o.merchant = ? AND i.sku <> '' GROUP BY i.sku`, string(merchant))
		if err != nil {
			return nil, err
		}
		for _, p := range rows {
			if !excluded[p.sku] && p.date > newest[p.sku] {
				newest[p.sku] = p.date
			}
		}
	}
	out := make([]string, 0, len(newest))
	for sku := range newest {
		out = append(out, sku)
	}
	sort.Slice(out, func(i, j int) bool {
		if newest[out[i]] == newest[out[j]] {
			return out[i] < out[j]
		}
		return newest[out[i]] > newest[out[j]]
	})
	if limit >= 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}
