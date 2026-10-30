package portcheck

import (
	"strings"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
)

func TestMain(m *testing.M) { storetest.Main(m, "portcheck") }
func TestSeededSnapshotsAndTransactionMutation(t *testing.T) {
	cipher, err := store.NewCipher("portcheck-test-secret")
	require.NoError(t, err)
	db := storetest.Empty(t).WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Transactions = 48
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), db, opts))
	cfg := &config.Config{SecretKey: "portcheck-test-secret"}
	date, err := time.Parse(time.DateOnly, seed.DefaultToday)
	require.NoError(t, err)
	before, err := Capture(t.Context(), db, cfg, date)
	require.NoError(t, err)
	for _, requests := range before.Households {
		for month := 0; month < 24; month++ {
			start := time.Date(date.Year(), date.Month(), 1, 12, 0, 0, 0, time.UTC).AddDate(0, -23+month, 0)
			key := "GET /reports/monthly-summary?month=" + start.Format("2006-01")
			require.Equal(t, 200, requests[key].Status, key)
			body, ok := requests[key].Body.(map[string]any)
			require.True(t, ok, key)
			require.Equal(t, start.Format("2006-01"), body["month"], key)
		}
	}
	again, err := Capture(t.Context(), db, cfg, date)
	require.NoError(t, err)
	require.Empty(t, Diff(before, again))
	spaces, err := db.ListSpaces(t.Context())
	require.NoError(t, err)
	transactions, err := db.ListTransactions(t.Context(), spaces[0].ID, store.TransactionQuery{})
	require.NoError(t, err)
	require.NotEmpty(t, transactions)
	var changed bool
	for _, tx := range transactions {
		if tx.EstimateStatus != "" || tx.IsDeleted {
			continue
		}
		tx.Amount = tx.Amount.Add(domain.MustFromString("17.23"))
		if tx.HasAmountPrimary {
			tx.AmountPrimary = tx.AmountPrimary.Add(domain.MustFromString("17.23"))
		}
		require.NoError(t, db.UpdateTransaction(t.Context(), spaces[0].ID, &tx))
		changed = true
		break
	}
	require.True(t, changed)
	after, err := Capture(t.Context(), db, cfg, date)
	require.NoError(t, err)
	differences := Diff(before, after)
	require.NotEmpty(t, differences)
	totals := false
	for _, d := range differences {
		require.True(t, strings.HasPrefix(d.Path, "/households/"+spaces[0].ID.String()+"/"), d.Path)
		if strings.Contains(d.Path, "transactions?") && (strings.Contains(d.Path, "total") || strings.Contains(d.Path, "running_balance")) {
			totals = true
		}
	}
	require.True(t, totals, "mutation must affect a register balance or total")
}

func TestDiffReportsMissingRequestsAndNumericPrecision(t *testing.T) {
	a := &Snapshot{FormatVersion: 1, Households: map[string]map[string]Response{"home": {"GET /a": {200, map[string]any{"amount": "9007199254740993"}}}}}
	b := &Snapshot{FormatVersion: 1, Households: map[string]map[string]Response{"home": {"GET /b": {200, nil}}}}
	differences := Diff(a, b)
	require.Len(t, differences, 2)
	require.True(t, differences[0].AfterMissing)
	require.True(t, differences[1].BeforeMissing)
}

func TestDiscoveryUsesCollectionEntitiesNotNestedIDs(t *testing.T) {
	responses := map[string]Response{
		"GET /filters": {200, []any{map[string]any{
			"id": "filter", "items": []any{map[string]any{"id": "clause"}},
		}}},
		"GET /email/messages": {200, []any{map[string]any{
			"id": "log-row", "message_id": "<external@example.invalid>",
		}}},
	}
	require.Equal(t, []string{"filter"}, discover(responses, "/filters/{filter_id}", "filter_id"))
	require.Equal(t, []string{"log-row"}, discover(responses, "/email/messages/{message_id}/text", "message_id"))
}
