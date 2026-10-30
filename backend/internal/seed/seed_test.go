package seed_test

import (
	"context"
	"fmt"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
	"sort"
	"strings"
	"testing"
)

func seeded(t *testing.T, n int64) *store.Store {
	t.Helper()
	cipher, err := store.NewCipher("seed-test-only-not-a-secret")
	require.NoError(t, err)
	db := storetest.New(t).WithCipher(cipher)
	o := seed.DefaultOptions()
	o.Seed = n
	o.Transactions = 48
	o.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), db, o))
	return db
}
func TestEveryTableIsPopulated(t *testing.T) {
	db := seeded(t, 1)
	counts, err := seed.Counts(t.Context(), db)
	require.NoError(t, err)
	require.NotEmpty(t, counts)
	for name, count := range counts {
		require.Positive(t, count, "table %s is unexpectedly empty (no intentionally empty tables)", name)
	}
}
func fingerprint(t *testing.T, db *store.Store) string {
	t.Helper()
	ctx := context.Background()
	counts, err := seed.Counts(ctx, db)
	require.NoError(t, err)
	keys := make([]string, 0, len(counts))
	for k := range counts {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for _, k := range keys {
		fmt.Fprintf(&b, "%s=%d;", k, counts[k])
	}
	spaces, err := db.ListSpaces(ctx)
	require.NoError(t, err)
	// Seeded household IDs are deliberately random. Sort the ledger content
	// across all files rather than including those IDs in the fingerprint.
	var ledger []string
	for _, space := range spaces {
		household, err := db.HouseholdDB(ctx, space.ID)
		require.NoError(t, err)
		rows, err := household.Read().QueryContext(ctx, "SELECT t.payee, t.statement_name, t.date, t.amount, COALESCE(c.name, '') FROM transactions t LEFT JOIN categories c ON c.id = t.category_id ORDER BY t.payee, t.statement_name, t.date, t.amount, COALESCE(c.name, '')")
		require.NoError(t, err)
		for rows.Next() {
			var p, s, d, category string
			var amount int64
			require.NoError(t, rows.Scan(&p, &s, &d, &amount, &category))
			ledger = append(ledger, fmt.Sprintf("%q:%q:%s:%d:%q;", p, s, d, amount, category))
		}
		require.NoError(t, rows.Err())
		require.NoError(t, rows.Close())
	}
	sort.Strings(ledger)
	for _, entry := range ledger {
		b.WriteString(entry)
	}
	return b.String()
}
func TestDeterminism(t *testing.T) {
	a := fingerprint(t, seeded(t, 42))
	b := fingerprint(t, seeded(t, 42))
	c := fingerprint(t, seeded(t, 43))
	require.Equal(t, a, b)
	require.NotEqual(t, a, c)
}

func TestAppend(t *testing.T) {
	db := seeded(t, 42)
	o := seed.DefaultOptions()
	o.Seed = 42
	o.Transactions = 48
	o.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), db, o))
	counts, err := seed.Counts(t.Context(), db)
	require.NoError(t, err)
	require.EqualValues(t, 6, counts["households"])
	require.EqualValues(t, 2, counts["passkeys"])
	require.EqualValues(t, 288, counts["transactions"])
}

func TestSeedMerchantCategories(t *testing.T) {
	db := storetest.New(t)
	cipher, err := store.NewCipher("seed-test-only-not-a-secret")
	require.NoError(t, err)
	db = db.WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Households = 1
	opts.Transactions = 4000
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), db, opts))
	spaces, err := db.ListSpaces(t.Context())
	require.NoError(t, err)
	household, err := db.HouseholdDB(t.Context(), spaces[0].ID)
	require.NoError(t, err)
	rows, err := household.Read().QueryContext(t.Context(), `
 SELECT t.payee, COALESCE(c.name, ''), t.amount FROM transactions t
 LEFT JOIN categories c ON c.id = t.category_id
 WHERE t.statement_name = t.payee || ' / SEED' AND t.series_id IS NULL`)
	require.NoError(t, err)
	homes := map[string]string{
		"Café du Marché": "Restaurants & cafés", "CAFÉ DU MARCHÉ": "Restaurants & cafés",
		"Straße Markt": "Groceries", "STRASSE MARKT": "Groceries",
		"İstanbul Bakery": "Groceries", "istanbul bakery": "Groceries",
		"Lunch 🍜": "Restaurants & cafés", "City Water": "Utilities", "Amazon": "Shopping",
		"Rent": "Housing", "": "Living expenses",
	}
	totals, exceptions, unfiled := map[string]int{}, map[string]int{}, map[string]int{}
	payroll := 0
	for rows.Next() {
		var payee, category string
		var amount int64
		require.NoError(t, rows.Scan(&payee, &category, &amount))
		if payee == "Payroll" {
			require.Equal(t, "Salary", category)
			require.Positive(t, amount)
			payroll++
			continue
		}
		home, ok := homes[payee]
		require.True(t, ok, "unknown merchant %q", payee)
		totals[payee]++
		if category == "" {
			unfiled[payee]++
		} else if category != home {
			exceptions[payee]++
		}
	}
	require.NoError(t, rows.Err())
	require.NoError(t, rows.Close())
	require.Positive(t, payroll)
	total, exceptional, uncategorized := 0, 0, 0
	for payee := range homes {
		require.Greater(t, totals[payee], 100, "merchant %q", payee)
		require.Less(t, float64(exceptions[payee]+unfiled[payee])/float64(totals[payee]), 0.15, "merchant %q", payee)
		total += totals[payee]
		exceptional += exceptions[payee]
		uncategorized += unfiled[payee]
	}
	require.InDelta(t, 0.05, float64(exceptional)/float64(total), 0.02)
	require.InDelta(t, 0.02, float64(uncategorized)/float64(total), 0.01)
	// Named edge fixtures retain their special amounts, dates and split/transfer links.
	var count int
	for _, fixture := range []struct {
		query string
		count int
	}{
		{`SELECT count(*) FROM transactions WHERE payee = 'Leap day 2024' AND date = '2024-02-29'`, 1},
		{`SELECT count(*) FROM transactions WHERE payee = 'Numeric limit windfall' AND amount = 999999999999998 AND excluded_from_reports = 1 AND excluded_from_spending_plan = 1`, 1},
		{`SELECT count(*) FROM transactions WHERE payee = 'Transfer to savings' AND transfer_pair_id IS NOT NULL`, 2},
		{`SELECT count(*) FROM transaction_splits s JOIN transactions t ON t.id = s.transaction_id WHERE t.payee = 'Exact split groceries'`, 2},
	} {
		require.NoError(t, household.Read().QueryRowContext(t.Context(), fixture.query).Scan(&count))
		require.Equal(t, fixture.count, count, fixture.query)
	}
}
