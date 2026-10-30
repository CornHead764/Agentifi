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
		rows, err := household.Read().QueryContext(ctx, "SELECT payee, statement_name, date, amount FROM transactions ORDER BY payee, statement_name, date, amount")
		require.NoError(t, err)
		for rows.Next() {
			var p, s, d string
			var amount int64
			require.NoError(t, rows.Scan(&p, &s, &d, &amount))
			ledger = append(ledger, fmt.Sprintf("%q:%q:%s:%d;", p, s, d, amount))
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
