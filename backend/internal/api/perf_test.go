package api

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/stretchr/testify/require"
)

// BenchmarkHousehold includes SQLite reads and JSON serialization through the
// authenticated router. Fixture creation and the warm-up are outside the timer.
func BenchmarkHousehold(b *testing.B) {
	for _, size := range []int{2000, 20000} {
		b.Run(fmt.Sprintf("transactions=%d", size), func(b *testing.B) {
			st := storetest.New(b)
			cipher, err := store.NewCipher("seed-development-only-not-a-secret")
			require.NoError(b, err)
			st = st.WithCipher(cipher)
			opts := seed.DefaultOptions()
			opts.Households, opts.Transactions = 1, size
			opts.Storage = &provider.LocalStorage{BasePath: b.TempDir()}
			require.NoError(b, seed.Run(b.Context(), st, opts))
			counts, err := seed.Counts(b.Context(), st)
			require.NoError(b, err)
			require.Equal(b, int64(size), counts["transactions"])
			spaces, err := st.ListSpaces(b.Context())
			require.NoError(b, err)
			user, err := st.GetUserByEmail(b.Context(), "seed.1@example.invalid")
			require.NoError(b, err)
			env := NewEnv(testConfig(), st)
			env.Now = func() time.Time { return opts.Today }
			token, _, err := env.Tokens.Issue(b.Context(), user.ID, store.SessionWeb, "benchmark")
			require.NoError(b, err)
			handler := RouterFor(env)
			for _, endpoint := range []struct{ name, path string }{
				{"register", "/transactions"},
				{"filtered_register", "/transactions?search=caf%C3%A9&from=2026-06-01&to=2026-06-30"},
				{"spending_report", "/reports/spending?grain=month&period=2026-06-01"},
				{"net_worth", "/net-worth?from=2026-06-30&to=2026-06-30"},
			} {
				b.Run(endpoint.name, func(b *testing.B) {
					measureLatency(b, func() {
						req := httptest.NewRequest(http.MethodGet, endpoint.path, nil)
						req.Header.Set("Authorization", "Bearer "+token)
						req.Header.Set("X-Space-Id", spaces[0].ID.String())
						rec := httptest.NewRecorder()
						handler.ServeHTTP(rec, req)
						if rec.Code != http.StatusOK {
							b.Fatalf("%s: %d: %s", endpoint.path, rec.Code, rec.Body.String())
						}
					})
				})
			}
			b.Run("plan_compute_save", func(b *testing.B) {
				plan := service.NewPlan(st)
				plan.Now = func() time.Time { return opts.Today }
				measureLatency(b, func() {
					view, err := plan.Compute(b.Context(), spaces[0].ID, domain.MonthOf(domain.DateOf(opts.Today)))
					require.NoError(b, err)
					require.NoError(b, plan.SaveResults(b.Context(), spaces[0].ID, view))
				})
			})
		})
	}
}

// A bounded reservoir keeps latency bookkeeping independent of benchmark length.
// With -benchtime=100x every operation is retained; longer runs sample uniformly.
func measureLatency(b *testing.B, operation func()) {
	b.Helper()
	operation()
	const capacity = 10000
	samples := make([]int64, 0, min(b.N, capacity))
	stride := max(1, (b.N+capacity-1)/capacity)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		started := time.Now()
		operation()
		elapsed := time.Since(started).Nanoseconds()
		if i%stride == 0 {
			samples = append(samples, elapsed)
		}
	}
	b.StopTimer()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	b.ReportMetric(float64(samples[(len(samples)-1)*50/100]), "p50-ns/op")
	b.ReportMetric(float64(samples[(len(samples)-1)*95/100]), "p95-ns/op")
}
