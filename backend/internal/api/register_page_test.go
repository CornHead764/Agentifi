package api

import (
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/seed"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func TestRegisterPageMatchesWindow(t *testing.T) {
	t.Parallel()
	// Expected value: paging through the register returns the same rows as the unpaged list.
	st := storetest.New(t)
	cipher, err := store.NewCipher("seed-development-only-not-a-secret")
	require.NoError(t, err)
	st = st.WithCipher(cipher)
	opts := seed.DefaultOptions()
	opts.Households, opts.Transactions = 1, 2000
	opts.Storage = &provider.LocalStorage{BasePath: t.TempDir()}
	require.NoError(t, seed.Run(t.Context(), st, opts))
	spaces, err := st.ListSpaces(t.Context())
	require.NoError(t, err)
	sp := auth.SpaceContext{Space: spaces[0]}
	env := NewEnv(testConfig(), st)
	filters, err := st.ListFilters(t.Context(), sp.ID(), false)
	require.NoError(t, err)
	queries := []string{"", "reviewed=true", "reviewed=false", "search=caf%C3%A9", "search=%25", "padding=hide", "from=2026-06-01&to=2026-06-30", "from=1900-01-01&to=1900-01-02", "account_id="}
	for _, filter := range filters {
		queries = append(queries, "filter_id="+filter.ID.String())
	}
	for _, raw := range queries {
		for _, mode := range []string{"posted", "effective"} {
			for _, order := range []string{"asc", "desc"} {
				t.Run(raw+"/"+mode+"/"+order, func(t *testing.T) {
					req := httptest.NewRequest("GET", "/transactions?"+raw+"&date_field="+mode+"&order="+order+"&limit=7&offset=6", nil)
					query, err := registerQuery(req)
					require.NoError(t, err)
					old, err := matchRegister(t.Context(), env, sp, query)
					require.NoError(t, err)
					postings := old.Postings
					var padding []domain.Posting
					if query.HidePadding {
						postings, padding = domain.FoldPadding(postings)
					}
					got, totals, err := loadRegisterPage(t.Context(), env, sp, query)
					require.NoError(t, err)
					kept := func(p domain.Posting) domain.Money { return domain.MatchedAmount(p, old.Partial[p.Txn.ID]) }
					require.Equal(t, len(postings), totals.Count)
					require.Equal(t, domain.Sum(postings, kept).String(), totals.Total.String())
					require.Equal(t, domain.Sum(postings, domain.Posting.Amount).String(), totals.FullTotal.String())
					partial := 0
					for _, posting := range postings {
						if _, ok := old.Partial[posting.Txn.ID]; ok {
							partial++
						}
					}
					require.Equal(t, partial, totals.PartialCount)
					require.Equal(t, len(padding), totals.PaddingCount)
					require.Equal(t, domain.Sum(padding, kept).String(), totals.PaddingTotal.String())
					want := registerSlice(postings, query.Offset, query.Limit)
					require.Len(t, got.Postings, len(want))
					if !query.HasFilter {
						require.Len(t, got.Rows, len(want), "only page rows are hydrated")
					}
					for i, p := range want {
						require.Equal(t, p.Txn.ID, got.Postings[i].Txn.ID)
						id := uuid.MustParse(string(p.Txn.ID))
						require.Equal(t, old.Rows[id], got.Rows[id])
					}
				})
			}
		}
	}
}

func TestRegisterPageDateTies(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	day := domain.NewDate(2027, time.January, 15)
	// Creation order deliberately differs from ID order.
	for _, n := range []int{3, 1, 4, 2} {
		row := store.Transaction{ID: uuid.MustParse(fmt.Sprintf("00000000-0000-0000-0000-%012d", n)), AccountID: l.id("checking"), Date: day.AddDays(n), EffectiveDate: day,
			Currency: "USD", Amount: domain.MustFromString("-10.25"), StatementName: "CAFÉ TIE", Payee: "Tie", Source: domain.SourceManual}
		require.NoError(t, l.env.DB.CreateTransaction(t.Context(), space, &row))
	}
	sp := auth.SpaceContext{Space: store.Space{ID: space}}
	for _, order := range []string{"asc", "desc"} {
		for _, offset := range []int{0, 2, 4, 20} {
			raw := fmt.Sprintf("from=2027-01-15&to=2027-01-15&date_field=effective&search=caf%%C3%%A9&order=%s&offset=%d&limit=2", order, offset)
			query, err := registerQuery(httptest.NewRequest("GET", "/transactions?"+raw, nil))
			require.NoError(t, err)
			old, err := matchRegister(t.Context(), l.env, sp, query)
			require.NoError(t, err)
			page := registerPage(l, raw)
			require.EqualValues(t, 4, page["count"])
			require.Equal(t, "-41.00", page["total"])
			items := page["items"].([]any)
			want := registerSlice(old.Postings, offset, 2)
			require.Len(t, items, len(want))
			for i, item := range items {
				require.Equal(t, string(want[i].Txn.ID), item.(map[string]any)["id"])
			}
		}
	}
	empty := registerPage(l, "from=1900-01-01&to=1900-01-02")
	require.EqualValues(t, 0, empty["count"])
	require.Equal(t, "0.00", empty["total"])
	require.Empty(t, empty["items"])
}

func TestRegisterPageForeignAmounts(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	account := store.Account{Name: "Yen checking", Kind: domain.KindCash, Type: "checking", Currency: "JPY", IncludeInNetWorth: true}
	require.NoError(t, l.env.DB.CreateAccount(t.Context(), space, &account))
	day := domain.NewDate(2027, time.January, 15)
	for _, fixture := range []struct{ native, primary, name string }{
		{"-1000", "-10.00", "FX known charge"},
		{"2000", "20.00", "FX known refund"},
		{"-500", "", "FX unknown"},
	} {
		row := store.Transaction{AccountID: account.ID, Date: day, Currency: "JPY",
			Amount: domain.MustFromString(fixture.native), StatementName: fixture.name, Payee: fixture.name, Source: domain.SourceManual}
		if fixture.primary != "" {
			row.AmountPrimary, row.HasAmountPrimary = domain.MustFromString(fixture.primary), true
		}
		require.NoError(t, l.env.DB.CreateTransaction(t.Context(), space, &row))
	}
	sp := auth.SpaceContext{Space: store.Space{ID: space}}
	// The unknown FX row falls back to -500 native units, not zero or -5 USD.
	for _, fixture := range []struct {
		search, total string
		count         int
	}{
		{"FX", "-490.00", 3},
		{"known", "-490.00", 3},
		{"FX+known", "10.00", 2},
		{"FX+unknown", "-500.00", 1},
	} {
		t.Run(fixture.search, func(t *testing.T) {
			query, err := registerQuery(httptest.NewRequest("GET", "/transactions?from=2027-01-15&to=2027-01-15&account_id="+account.ID.String()+"&search="+fixture.search+"&limit=1", nil))
			require.NoError(t, err)
			old, err := matchRegister(t.Context(), l.env, sp, query)
			require.NoError(t, err)
			_, totals, err := loadRegisterPage(t.Context(), l.env, sp, query)
			require.NoError(t, err)
			require.Len(t, old.Postings, fixture.count)
			require.Equal(t, fixture.count, totals.Count)
			oldTotal := domain.Sum(old.Postings, domain.Posting.Amount)
			require.Equal(t, fixture.total, oldTotal.String())
			require.Equal(t, oldTotal.String(), totals.Total.String())
			require.Equal(t, oldTotal.String(), totals.FullTotal.String())
		})
	}
}
