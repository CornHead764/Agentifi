package service

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// fixedValuer answers whatever estimate the test last set, and counts lookups.
type fixedValuer struct {
	estimate string
	priced   provider.PricedAs
	lookups  int
}

func (f *fixedValuer) Name() string         { return "test-valuer" }
func (f *fixedValuer) AssetTypes() []string { return []string{"vehicle"} }
func (f *fixedValuer) IsConfigured() bool   { return true }
func (f *fixedValuer) Estimate(context.Context, provider.ValuationSubject) (*provider.ValuationQuote, error) {
	f.lookups++
	return &provider.ValuationQuote{
		Value: domain.MustFromString(f.estimate), Currency: "USD", Priced: f.priced,
	}, nil
}

func TestARevaluationSaysWhatTheSourcePriced(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID)

	valuer := &fixedValuer{estimate: "17500.00", priced: provider.PricedAs{
		Year: "2019", Make: "Examplemotors", Model: "Roadster", Trim: "Sport",
		Mileage: 41000, HasMileage: true,
	}}
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": valuer})
	result, err := valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.NotNil(t, result.Priced)
	require.Equal(t, valuer.priced, *result.Priced)

	result, err = valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.False(t, result.HasAdjust)
	require.NotNil(t, result.Priced, "an estimate that changes nothing still says what it priced")
}

// importedVehicle is an asset as the Simplifi import leaves it: a provider
// balance and no connection or SimpleFIN link.
func importedVehicle(t *testing.T, spaceID store.SpaceID, opts ...storetest.AccountOption) *store.Account {
	t.Helper()
	importedOn := time.Date(2026, time.March, 1, 0, 0, 0, 0, time.UTC)
	return newAccount(t, spaceID, "Car 1", append([]storetest.AccountOption{func(a *store.Account) {
		a.Kind, a.Type = domain.KindAsset, "vehicle"
		a.VehicleVIN = "TESTVIN0000000001"
		a.ProviderBalance, a.HasProviderBalance = domain.MustFromString("20000.00"), true
		a.ProviderBalanceAt = &importedOn
	}}, opts...)...)
}

func TestRevaluingAnImportedAssetMovesItsBalanceAndKeepsItsHistory(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID)

	valuer := &fixedValuer{}
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": valuer})
	first := time.Date(2026, time.April, 10, 12, 0, 0, 0, time.Local)
	second := time.Date(2026, time.June, 10, 12, 0, 0, 0, time.Local)

	valuer.estimate = "17500.00"
	valuation.Now = func() time.Time { return first }
	result, err := valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.Equal(t, "-2500.00", result.Adjustment.String())

	valuer.estimate = "18250.00"
	valuation.Now = func() time.Time { return second }
	result, err = valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.Equal(t, "750.00", result.Adjustment.String(),
		"the second adjustment is measured from the first estimate")

	stored, err := db(t).GetAccount(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.Equal(t, "18250.00", stored.ProviderBalance.String())
	require.True(t, stored.ProviderBalanceAt.Equal(second))
	require.True(t, stored.ValuedAt.Equal(second))

	postings, err := db(t).LoadPostings(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	require.NoError(t, err)
	acct := store.DomainAccount(stored)
	balanceOn := func(day time.Time) string {
		return domain.LedgerBalanceAsOf(acct, postings, domain.DateOf(day), domain.DatePosted).String()
	}
	require.Equal(t, "18250.00", domain.AccountBalance(acct, postings).String())
	require.Equal(t, "20000.00", balanceOn(first.AddDate(0, 0, -1)))
	require.Equal(t, "17500.00", balanceOn(first.AddDate(0, 0, 1)))
	require.Equal(t, "17500.00", balanceOn(second.AddDate(0, 0, -1)))
	require.Equal(t, "18250.00", balanceOn(second))

	rows, err := db(t).ListTransactions(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		require.True(t, row.HasBalance, "the running balance is rewritten with the anchor")
		if row.Date == domain.DateOf(second) {
			require.Equal(t, "18250.00", row.Balance.String())
		} else {
			require.Equal(t, "17500.00", row.Balance.String())
		}
	}
}

func TestAConnectedAssetIsNotRevalued(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID, withConnection(newConnection(t, spaceID)))

	valuer := &fixedValuer{estimate: "17500.00"}
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": valuer})
	result, err := valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.NotEmpty(t, result.Skipped)
	require.False(t, result.HasAdjust)
	require.Zero(t, valuer.lookups, "no lookup for a figure the bank supplies")

	stored, err := db(t).GetAccount(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.Equal(t, "20000.00", stored.ProviderBalance.String())
	rows, err := db(t).ListTransactions(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	require.NoError(t, err)
	require.Empty(t, rows)
}

func TestAManualAssetIsRevaluedByItsRowsAlone(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID, func(a *store.Account) {
		a.ProviderBalance, a.HasProviderBalance, a.ProviderBalanceAt = domain.Zero, false, nil
		a.OpeningBalance = domain.MustFromString("20000.00")
	})

	valuer := &fixedValuer{estimate: "17500.00"}
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": valuer})
	_, err := valuation.Revalue(ctx, spaceID, account.ID)
	require.NoError(t, err)

	stored, err := db(t).GetAccount(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.False(t, stored.HasProviderBalance)
	postings, err := db(t).LoadPostings(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	require.NoError(t, err)
	require.Equal(t, "17500.00", domain.AccountBalance(store.DomainAccount(stored), postings).String())
}

// heldValuer holds its first lookup until released, so a second pass can be
// started while the first is mid-lookup.
type heldValuer struct {
	fixedValuer
	entered chan struct{}
	release chan struct{}
	lookups atomic.Int32
}

func (h *heldValuer) Estimate(ctx context.Context, subject provider.ValuationSubject) (*provider.ValuationQuote, error) {
	if h.lookups.Add(1) == 1 {
		close(h.entered)
		<-h.release
	}
	return h.fixedValuer.Estimate(ctx, subject)
}

func TestTwoPassesAtOnceRevalueAnAssetOnce(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID)

	valuer := &heldValuer{
		fixedValuer: fixedValuer{estimate: "17500.00"},
		entered:     make(chan struct{}), release: make(chan struct{}),
	}
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": valuer})

	first := make(chan error, 1)
	go func() {
		_, err := valuation.RevalueAll(ctx, spaceID, false)
		first <- err
	}()
	<-valuer.entered
	second := make(chan error, 1)
	go func() {
		_, err := valuation.RevalueAll(ctx, spaceID, false)
		second <- err
	}()
	time.Sleep(100 * time.Millisecond)
	close(valuer.release)
	require.NoError(t, <-first)
	require.NoError(t, <-second)

	require.EqualValues(t, 1, valuer.lookups.Load(), "the second pass finds the asset stamped")
	rows, err := db(t).ListTransactions(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.Equal(t, "-2500.00", rows[0].Amount.String())
}

// noCamoufox is a valuer on a server with no Camoufox.
type noCamoufox struct{ fixedValuer }

func (*noCamoufox) Estimate(context.Context, provider.ValuationSubject) (*provider.ValuationQuote, error) {
	return nil, browser.ErrNoFirefox
}

func TestALookupWithNoCamoufoxSaysSoAndStampsNothing(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := importedVehicle(t, spaceID)
	valuation := NewValuation(db(t), map[string]provider.AssetValuationProvider{"vehicle": &noCamoufox{}})

	_, err := valuation.Revalue(ctx, spaceID, account.ID)
	require.ErrorIs(t, err, ErrValuationNeedsCamoufox)

	results, err := valuation.RevalueAll(ctx, spaceID, false)
	require.NoError(t, err)
	require.Len(t, results, 1)
	require.Equal(t, ErrValuationNeedsCamoufox.Error(), results[0].Skipped)

	stored, err := db(t).GetAccount(ctx, spaceID, account.ID)
	require.NoError(t, err)
	require.Nil(t, stored.ValuedAt, "the asset stays due for when Camoufox is set")
}
