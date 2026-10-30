package store

import (
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/stretchr/testify/require"
)

func TestCatalogMissingLookupPreservesFoundPrice(t *testing.T) {
	st := newSQLiteTest(t)
	ctx := t.Context()
	foundAt := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
	item := &CatalogItem{Merchant: domain.MerchantCostco, SKU: "123", Status: CatalogFound, Title: "Found", Price: domain.MustFromString("1.234"), PriceCurrency: "KWD", HasPrice: true, FoundAt: &foundAt}
	require.NoError(t, st.UpsertCatalogItem(ctx, item))
	require.False(t, item.LookedUpAt.IsZero())
	require.NoError(t, st.UpsertCatalogItem(ctx, &CatalogItem{Merchant: item.Merchant, SKU: item.SKU, Status: CatalogMissing}))
	db, err := st.server()
	require.NoError(t, err)
	var status, title, currency string
	var price, storedFoundAt int64
	require.NoError(t, db.Read().QueryRowContext(ctx, `SELECT status, title, price, price_currency, found_at FROM merchant_catalog WHERE merchant = ? AND sku = ?`, string(item.Merchant), item.SKU).Scan(&status, &title, &price, &currency, &storedFoundAt))
	require.Equal(t, CatalogFound, status)
	require.Equal(t, "Found", title)
	require.EqualValues(t, 1234, price)
	require.Equal(t, "KWD", currency)
	require.Equal(t, foundAt.UnixMilli(), storedFoundAt)
	item.Price = domain.MustFromString("1.2345")
	require.Error(t, st.UpsertCatalogItem(ctx, item))
}
