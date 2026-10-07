package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
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
	Merchant   domain.MerchantID
	SKU        string
	Status     string
	Title      string
	Brand      string
	Size       string
	Category   string
	ImageURL   string
	URL        string
	Price      domain.Money
	HasPrice   bool
	Source     string
	SourceRef  string
	Raw        json.RawMessage
	LookedUpAt time.Time
	FoundAt    *time.Time
}

// UpsertCatalogItem writes what a lookup learned. The catalog only learns: a
// lookup that found nothing records the ask and keeps what an earlier one
// found.
func (s *Store) UpsertCatalogItem(ctx context.Context, one *CatalogItem) error {
	if one.LookedUpAt.IsZero() {
		one.LookedUpAt = time.Now().UTC()
	}
	var raw any
	if len(one.Raw) > 0 {
		raw = string(one.Raw)
	}
	_, err := s.db.Exec(ctx,
		`INSERT INTO merchant_catalog
		     (merchant, sku, status, title, brand, size, category, image_url, url, price, source, source_ref,
		      raw, looked_up_at, found_at)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15)
		 ON CONFLICT (merchant, sku) DO UPDATE SET
		     status        = CASE WHEN EXCLUDED.status = 'found' THEN 'found' ELSE merchant_catalog.status END,
		     title         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.title ELSE merchant_catalog.title END,
		     brand         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.brand ELSE merchant_catalog.brand END,
		     size          = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.size ELSE merchant_catalog.size END,
		     category      = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.category ELSE merchant_catalog.category END,
		     image_url     = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.image_url ELSE merchant_catalog.image_url END,
		     url           = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.url ELSE merchant_catalog.url END,
		     price         = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.price ELSE merchant_catalog.price END,
		     source        = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.source ELSE merchant_catalog.source END,
		     source_ref    = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.source_ref ELSE merchant_catalog.source_ref END,
		     raw           = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.raw ELSE merchant_catalog.raw END,
		     found_at      = CASE WHEN EXCLUDED.status = 'found' THEN EXCLUDED.found_at ELSE merchant_catalog.found_at END,
		     looked_up_at  = EXCLUDED.looked_up_at,
		     updated_at    = now()`,
		string(one.Merchant), one.SKU, one.Status, one.Title, one.Brand, one.Size, one.Category, one.ImageURL,
		one.URL, pgconv.NullMoney(one.Price, one.HasPrice), one.Source, one.SourceRef, raw,
		one.LookedUpAt, one.FoundAt)
	return wrap("store: upsert catalog item", err)
}

// CatalogSKUsToLookUp is every number on a merchant's orders, in any space,
// that the catalog has never been asked about — or was asked about, found
// nothing, and has not been asked since retryBefore. Newest purchases first.
func (s *Store) CatalogSKUsToLookUp(
	ctx context.Context, merchant domain.MerchantID, retryBefore time.Time, limit int,
) ([]string, error) {
	return queryAll(ctx, s.db, "store: catalog numbers to look up", scanValue[string],
		`SELECT sku FROM (
		   SELECT DISTINCT ON (i.sku) i.sku, o.ordered_on
		     FROM merchant_order_items i
		     JOIN merchant_orders o ON o.id = i.order_id
		     LEFT JOIN merchant_catalog c ON c.merchant = o.merchant AND c.sku = i.sku
		    WHERE o.merchant = $1 AND i.sku <> ''
		      AND (c.sku IS NULL OR (c.status = 'missing' AND c.looked_up_at < $2))
		    ORDER BY i.sku, o.ordered_on DESC
		 ) wanted
		 ORDER BY ordered_on DESC, sku
		 LIMIT $3`,
		string(merchant), retryBefore, limit)
}
