package provider

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"
)

func TestFetchingLatestRatesKeepsThePublishedDigits(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/latest.json", r.URL.Path)
		require.Equal(t, "test-app-id", r.URL.Query().Get("app_id"))
		require.Equal(t, "EUR,GBP", r.URL.Query().Get("symbols"))
		fmt.Fprint(w, `{"base":"USD","rates":{"EUR":0.923456,"GBP":0.789012}}`)
	}))
	defer server.Close()

	oer := &OpenExchangeRates{AppID: "test-app-id", Symbols: []string{"EUR", "GBP"},
		BaseURL: server.URL, HTTPClient: server.Client()}

	rates, err := oer.Latest(context.Background())
	require.NoError(t, err)
	require.Equal(t, "0.923456", rates["EUR"].String())
	require.Equal(t, "0.789012", rates["GBP"].String())
}

func TestHistoricalRatesAskForThatDay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/historical/2026-03-14.json", r.URL.Path)
		fmt.Fprint(w, `{"rates":{"EUR":0.91}}`)
	}))
	defer server.Close()

	oer := &OpenExchangeRates{AppID: "id", BaseURL: server.URL, HTTPClient: server.Client()}
	rates, err := oer.Historical(context.Background(), domain.NewDate(2026, time.March, 14))
	require.NoError(t, err)
	require.Len(t, rates, 1)
}

func TestFetchingWithoutAnAppIDIsAConfigurationErrorNotARequest(t *testing.T) {
	oer := &OpenExchangeRates{BaseURL: "http://127.0.0.1:1"}
	_, err := oer.Latest(context.Background())
	require.Error(t, err)
}

type fakeRateStore struct {
	rows      map[domain.ID]map[string]map[domain.Date]domain.Rate
	upserts   int
	upsertErr error
}

func newFakeRateStore() *fakeRateStore {
	return &fakeRateStore{rows: map[domain.ID]map[string]map[domain.Date]domain.Rate{}}
}

func (s *fakeRateStore) put(spaceID domain.ID, currency string, on domain.Date, rate string) {
	if s.rows[spaceID] == nil {
		s.rows[spaceID] = map[string]map[domain.Date]domain.Rate{}
	}
	if s.rows[spaceID][currency] == nil {
		s.rows[spaceID][currency] = map[domain.Date]domain.Rate{}
	}
	s.rows[spaceID][currency][on] = decimal.RequireFromString(rate)
}

func (s *fakeRateStore) ExactRate(_ context.Context, spaceID domain.ID, currency string, on domain.Date) (domain.Rate, bool, error) {
	rate, ok := s.rows[spaceID][currency][on]
	return rate, ok, nil
}

func (s *fakeRateStore) ClosestRate(_ context.Context, spaceID domain.ID, currency string, on domain.Date) (domain.Rate, bool, error) {
	var (
		best     domain.Rate
		bestDate domain.Date
		found    bool
	)
	for day, rate := range s.rows[spaceID][currency] {
		if day.After(on) {
			continue
		}
		if !found || day.After(bestDate) {
			best, bestDate, found = rate, day, true
		}
	}
	if found {
		return best, true, nil
	}
	for day, rate := range s.rows[spaceID][currency] {
		if !found || day.Before(bestDate) {
			best, bestDate, found = rate, day, true
		}
	}
	return best, found, nil
}

func (s *fakeRateStore) UpsertRates(_ context.Context, rates []FxRate) error {
	if s.upsertErr != nil {
		return s.upsertErr
	}
	s.upserts++
	for _, r := range rates {
		s.put(r.SpaceID, r.QuoteCurrency, r.On, r.Rate.String())
	}
	return nil
}

type fakeFxProvider struct {
	latest     map[string]domain.Rate
	historical map[string]domain.Rate
	err        error
	calls      []string
}

func (f *fakeFxProvider) Name() string { return "fake" }

func (f *fakeFxProvider) Latest(context.Context) (map[string]domain.Rate, error) {
	f.calls = append(f.calls, "latest")
	return f.latest, f.err
}

func (f *fakeFxProvider) Historical(_ context.Context, on domain.Date) (map[string]domain.Rate, error) {
	f.calls = append(f.calls, "historical "+on.String())
	return f.historical, f.err
}

const (
	spaceA = domain.ID("space-a")
	spaceB = domain.ID("space-b")
)

var testToday = domain.NewDate(2026, time.March, 20)

func newFxRates(store FxRateStore, provider FxRateProvider) *FxRates {
	return &FxRates{
		Provider:  provider,
		Store:     store,
		Supported: []string{"EUR", "GBP", "USD"},
		Now:       func() time.Time { return testToday.Time() },
	}
}

func TestACrossRateGoesThroughUSD(t *testing.T) {
	store := newFakeRateStore()
	store.put(spaceA, "EUR", testToday, "0.5")
	store.put(spaceA, "GBP", testToday, "0.25")
	fx := newFxRates(store, &fakeFxProvider{})

	rate, ok, err := fx.Resolve(context.Background(), spaceA,
		RateQuery{From: "EUR", To: "GBP", On: testToday, NoFetch: true})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0.5", rate.String())
}

func TestOneSpacesRateCannotMoveAnothersNetWorth(t *testing.T) {
	store := newFakeRateStore()
	store.put(spaceB, "EUR", testToday, "0.5")
	fx := newFxRates(store, &fakeFxProvider{})

	_, ok, err := fx.Resolve(context.Background(), spaceA,
		RateQuery{From: "EUR", To: "USD", On: testToday, NoFetch: true})
	require.NoError(t, err)
	require.False(t, ok)
}

func TestAMissingRateFallsBackToTheClosestStoredDay(t *testing.T) {
	store := newFakeRateStore()
	store.put(spaceA, "EUR", domain.NewDate(2026, time.March, 18), "0.9")
	fx := newFxRates(store, &fakeFxProvider{})

	rate, ok, err := fx.Resolve(context.Background(), spaceA,
		RateQuery{From: "EUR", To: "USD", On: testToday, NoFetch: true})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1.1111111111111111", rate.String())
}

func TestAFutureDatedRowReadsTodaysRate(t *testing.T) {
	// There is no historical snapshot for a day that has not happened.
	store := newFakeRateStore()
	store.put(spaceA, "EUR", testToday, "0.8")
	provider := &fakeFxProvider{}
	fx := newFxRates(store, provider)

	rate, ok, err := fx.Resolve(context.Background(), spaceA,
		RateQuery{From: "EUR", To: "USD", On: testToday.AddDays(30), NoFetch: true})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1.25", rate.String())
	require.Empty(t, provider.calls)
}

func TestAMissingRateIsFetchedOnDemandOnce(t *testing.T) {
	store := newFakeRateStore()
	provider := &fakeFxProvider{latest: map[string]domain.Rate{"EUR": decimal.RequireFromString("0.5")}}
	fx := newFxRates(store, provider)

	rate, ok, err := fx.Resolve(context.Background(), spaceA, RateQuery{From: "EUR", To: "USD", On: testToday})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "2", rate.String())
	require.Equal(t, []string{"latest"}, provider.calls)
}

func TestTheHealingPassNeverSpendsAPIQuota(t *testing.T) {
	store := newFakeRateStore()
	provider := &fakeFxProvider{}
	fx := newFxRates(store, provider)

	_, ok, err := fx.Resolve(context.Background(), spaceA,
		RateQuery{From: "EUR", To: "USD", On: testToday, NoFetch: true})
	require.NoError(t, err)
	require.False(t, ok)
	require.Empty(t, provider.calls)
}

func TestAProviderOutageStillAnswersFromTheCache(t *testing.T) {
	store := newFakeRateStore()
	store.put(spaceA, "EUR", domain.NewDate(2026, time.March, 1), "0.5")
	provider := &fakeFxProvider{err: errors.New("upstream is down")}
	fx := newFxRates(store, provider)

	rate, ok, err := fx.Resolve(context.Background(), spaceA, RateQuery{From: "EUR", To: "USD", On: testToday})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "2", rate.String())
}

func TestAWriteLeavesTheRowUnconvertedRatherThanStoringAFakeRate(t *testing.T) {
	fx := newFxRates(newFakeRateStore(), &fakeFxProvider{})

	_, ok, err := fx.StampPrimary(context.Background(), spaceA,
		domain.MustFromString("100.00"), "EUR", "USD", testToday, true)
	require.NoError(t, err)
	require.False(t, ok)
}

func TestASameCurrencyRowIsAlwaysSafeToStamp(t *testing.T) {
	fx := newFxRates(newFakeRateStore(), &fakeFxProvider{})

	stamped, ok, err := fx.StampPrimary(context.Background(), spaceA,
		domain.MustFromString("250.005"), "USD", "USD", testToday, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "250.01", stamped.AmountPrimary.String())
	require.Equal(t, "1", stamped.RateUsed.String())
}

func TestAStampedAmountCarriesTheRateItUsed(t *testing.T) {
	store := newFakeRateStore()
	store.put(spaceA, "EUR", testToday, "0.8")
	fx := newFxRates(store, &fakeFxProvider{})

	stamped, ok, err := fx.StampPrimary(context.Background(), spaceA,
		domain.MustFromString("80.00"), "EUR", "USD", testToday, true)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "100.00", stamped.AmountPrimary.String())
	require.Equal(t, "1.25", stamped.RateUsed.String())
}

func TestSyncStoresOnlyTheSupportedCurrencies(t *testing.T) {
	store := newFakeRateStore()
	provider := &fakeFxProvider{latest: map[string]domain.Rate{
		"EUR": decimal.RequireFromString("0.92"),
		"GBP": decimal.RequireFromString("0.79"),
		"XPF": decimal.RequireFromString("110"),
	}}
	fx := newFxRates(store, provider)

	count, err := fx.Sync(context.Background(), spaceA, testToday)
	require.NoError(t, err)
	require.Equal(t, 2, count)
	require.NotContains(t, store.rows[spaceA], "XPF")
}

func TestSyncingAFutureDayStoresTodaysPublishedRate(t *testing.T) {
	store := newFakeRateStore()
	provider := &fakeFxProvider{latest: map[string]domain.Rate{"EUR": decimal.RequireFromString("0.92")}}
	fx := newFxRates(store, provider)

	_, err := fx.Sync(context.Background(), spaceA, testToday.AddDays(10))
	require.NoError(t, err)
	require.Equal(t, []string{"latest"}, provider.calls)
	require.Contains(t, store.rows[spaceA]["EUR"], testToday)
}

func TestSyncingAPastDayAsksForThatDaysSnapshot(t *testing.T) {
	store := newFakeRateStore()
	provider := &fakeFxProvider{historical: map[string]domain.Rate{"EUR": decimal.RequireFromString("0.9")}}
	fx := newFxRates(store, provider)

	past := domain.NewDate(2026, time.January, 5)
	_, err := fx.Sync(context.Background(), spaceA, past)
	require.NoError(t, err)
	require.Equal(t, []string{"historical 2026-01-05"}, provider.calls)
	require.Contains(t, store.rows[spaceA]["EUR"], past)
}

func TestAServiceWithNoProviderFetchesNothingRatherThanPanicking(t *testing.T) {
	rates := &FxRates{Store: newFakeRateStore(), Now: func() time.Time { return testToday.Time() }}
	require.False(t, rates.CanFetch())

	written, err := rates.Sync(context.Background(), "space-1", testToday)
	require.NoError(t, err)
	require.Zero(t, written)
}

func TestAResolveWithNoProviderAnswersFromStorageAlone(t *testing.T) {
	store := newFakeRateStore()
	rates := &FxRates{Store: store, Now: func() time.Time { return testToday.Time() }}
	on := testToday
	store.put("space-1", "EUR", on, "0.9")
	store.put("space-1", "USD", on, "1")

	// NoFetch false takes the fetch branch.
	rate, ok, err := rates.Resolve(context.Background(), "space-1",
		RateQuery{From: "EUR", To: "USD", On: on})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "1.1111111111", rate.StringFixed(10))

	_, ok, err = rates.Resolve(context.Background(), "space-1",
		RateQuery{From: "JPY", To: "USD", On: on})
	require.NoError(t, err)
	require.False(t, ok)
}
