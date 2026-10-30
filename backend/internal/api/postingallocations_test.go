package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestPostingLoadersAgree(t *testing.T) {
	t.Parallel()
	// Expected value: both loaders must return the same postings for one seeded household.
	st := storetest.New(t)
	cipher, err := store.NewCipher("seed-development-only-not-a-secret")
	require.NoError(t, err)
	st = st.WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Households = 1
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), st, opts))
	spaces, err := st.ListSpaces(t.Context())
	require.NoError(t, err)
	sp := auth.SpaceContext{Space: spaces[0]}
	accounts, err := st.ListAccounts(t.Context(), sp.ID(), store.AccountQuery{IncludeClosed: true})
	require.NoError(t, err)
	accounts[0].ExcludedFromReports = true
	accounts[0].ExcludedFromSpendingPlan = true
	require.NoError(t, st.UpdateAccount(t.Context(), sp.ID(), &accounts[0]))
	categories, err := st.ListCategories(t.Context(), sp.ID(), true)
	require.NoError(t, err)
	for i := range categories {
		categories[i].ExcludedFromReports = true
		categories[i].ExcludedFromSpendingPlan = true
		require.NoError(t, st.UpdateCategory(t.Context(), sp.ID(), &categories[i]))
	}
	ids := make([]uuid.UUID, len(accounts))
	for i, account := range accounts {
		ids[i] = account.ID
	}
	query := store.TransactionQuery{AccountIDs: ids}
	legacy, _, err := service.LoadPostings(t.Context(), st, sp.ID(), query)
	require.NoError(t, err)
	current, err := st.LoadPostings(t.Context(), sp.ID(), query)
	require.NoError(t, err)
	require.Equal(t, legacy, current)
	coverage := map[string]bool{}
	grouped := make(map[uuid.UUID][]domain.Posting)
	for _, posting := range legacy {
		id, err := store.ParseID(posting.Txn.AccountID)
		require.NoError(t, err)
		grouped[id] = append(grouped[id], posting)
		if posting.Txn.IsPending {
			coverage["pending"] = true
		}
		if posting.Txn.TransferPairID != "" {
			coverage["transfer"] = true
		}
		if posting.Txn.HasAmountPrimary {
			coverage["fx"] = true
		}
		if len(posting.Txn.Splits) > 0 {
			coverage["split"] = true
		}
		if posting.Txn.ExcludedFromReports && posting.Txn.ExcludedFromSpendingPlan {
			coverage["transaction exclusions"] = true
		}
		if posting.Account.ExcludedFromReports && posting.Account.ExcludedFromSpendingPlan {
			coverage["account exclusions"] = true
		}
		if posting.HasCategory && posting.Category.ExcludedFromReports && posting.Category.ExcludedFromSpendingPlan {
			coverage["category exclusions"] = true
		}
	}
	for _, feature := range []string{"pending", "transfer", "fx", "split", "transaction exclusions", "account exclusions", "category exclusions"} {
		require.True(t, coverage[feature], feature)
	}
	links, err := st.ListRefundLinks(t.Context(), sp.ID())
	require.NoError(t, err)
	require.NotEmpty(t, links)

	env := NewEnv(testConfig(), st)
	env.Now = func() time.Time { return opts.Today }
	gotGrouped, err := postingsByAccount(t.Context(), env, sp, accounts)
	require.NoError(t, err)
	require.Equal(t, grouped, gotGrouped)
	require.Greater(t, len(gotGrouped), 1)
	for id, rows := range gotGrouped {
		require.Equal(t, len(rows), cap(rows), "account %s's rows must be capped, or an append overwrites the next account's", id)
	}
	window := Window{From: domain.NewDate(2026, time.June, 1), HasFrom: true,
		To: domain.NewDate(2026, time.June, 30), HasTo: true, Mode: domain.DatePosted}
	series, err := newBalanceSeries(t.Context(), env, sp, accounts, window, "day")
	require.NoError(t, err)
	series.postings = grouped
	series.cached = map[domain.Date]map[domain.ID]domain.Money{}
	converter, err := newBalanceConverter(t.Context(), env, sp, accounts, series.end)
	require.NoError(t, err)
	domainAccounts := store.DomainAccounts(accounts)
	point := func(on domain.Date) NetWorthPoint {
		balances := converter.apply(series.on(on))
		return netWorthPoint(domain.NetWorthAt(domainAccounts, balances, on),
			netWorthByKind(domainAccounts, balances), domain.EquityAt(domainAccounts, balances))
	}
	request := httptest.NewRequest(http.MethodGet, "/net-worth?from=2026-06-01&to=2026-06-30&granularity=day", nil)
	recorder := httptest.NewRecorder()
	require.NoError(t, readNetWorth(env, recorder, request, sp))
	require.Equal(t, http.StatusOK, recorder.Code)
	var response NetWorthResponse
	require.NoError(t, json.Unmarshal(recorder.Body.Bytes(), &response))
	equalJSON := func(expected, actual any) {
		t.Helper()
		want, err := json.Marshal(expected)
		require.NoError(t, err)
		got, err := json.Marshal(actual)
		require.NoError(t, err)
		require.JSONEq(t, string(want), string(got))
	}
	require.Len(t, response.Points, len(series.samples))
	for i, on := range series.samples {
		equalJSON(point(on), response.Points[i])
	}
	equalJSON(point(series.start), response.Start)
	equalJSON(point(series.end), response.End)
	startBalances := converter.apply(series.on(series.start))
	endBalances := converter.apply(series.on(series.end))
	equalJSON(netWorthGroups(accounts, startBalances, endBalances), response.Groups)
	start := domain.NetWorthAt(domainAccounts, startBalances, series.start)
	end := domain.NetWorthAt(domainAccounts, endBalances, series.end)
	require.True(t, domain.NetWorthChange(start, end).Equal(response.Change))
	changePct, hasChangePct := domain.NetWorthChangePct(start, end)
	equalJSON(store.PtrIf(changePct, hasChangePct), response.ChangePct)
	ratio, hasRatio := end.DebtToAsset()
	equalJSON(store.PtrIf(ratio, hasRatio), response.DebtToAsset)
}
