package service

import (
	"testing"

	"github.com/shopspring/decimal"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// rate reads a conversion factor the way the column stores one.
func rate(text string) domain.Rate {
	parsed, err := decimal.NewFromString(text)
	if err != nil {
		panic(err)
	}
	return parsed
}

func TestAForeignRowIsConvertedAtItsOwnDate(t *testing.T) {
	space := newSpace(t)
	database := db(t)
	rates := &provider.FxRates{Store: store.NewFxRates(database)}
	converter := NewCurrency(database, rates)

	account := &store.Account{
		Name: "Travel", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))

	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	require.NoError(t, store.NewFxRates(database).UpsertRates(t.Context(), []provider.FxRate{{
		SpaceID: domain.ID(space.UUID().String()), BaseCurrency: "USD", QuoteCurrency: "EUR",
		On: on, Rate: rate("0.9"), Source: "test",
	}}))

	txn := &store.Transaction{
		AccountID: account.ID, Date: on, EffectiveDate: on,
		Amount: domain.MustFromString("-40.00"), Currency: "EUR",
		StatementName: "CAFE PARIS", Payee: "Cafe Paris", Source: domain.SourceManual,
	}
	require.NoError(t, database.CreateTransaction(t.Context(), space, txn))

	out, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 1, out.Stamped)
	require.Zero(t, out.Unrated)

	stamped, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.True(t, stamped.HasAmountPrimary)
	// 40 EUR at 0.9 EUR per USD is 44.44 USD, not 40.
	require.Equal(t, "-44.44", stamped.AmountPrimary.String())
	require.True(t, stamped.HasFxRateUsed)
}

func TestClearingAStaleConversionLetsStampSpaceReDerive(t *testing.T) {
	// StampSpace skips a row that already carries a primary amount, so a write
	// path that changes the amount must call ClearPrimaryAmount.
	space := newSpace(t)
	database := db(t)
	rates := &provider.FxRates{Store: store.NewFxRates(database)}
	converter := NewCurrency(database, rates)

	account := &store.Account{
		Name: "Travel", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))

	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	require.NoError(t, store.NewFxRates(database).UpsertRates(t.Context(), []provider.FxRate{{
		SpaceID: domain.ID(space.UUID().String()), BaseCurrency: "USD", QuoteCurrency: "EUR",
		On: on, Rate: rate("0.9"), Source: "test",
	}}))

	txn := &store.Transaction{
		AccountID: account.ID, Date: on, EffectiveDate: on,
		Amount: domain.MustFromString("-40.00"), Currency: "EUR",
		StatementName: "CAFE PARIS", Source: domain.SourceManual,
	}
	require.NoError(t, database.CreateTransaction(t.Context(), space, txn))

	_, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	stamped, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.Equal(t, "-44.44", stamped.AmountPrimary.String())

	// The charge posts at a new amount. Without clearing, StampSpace would skip
	// it and the -44.44 would stand forever.
	stamped.Amount = domain.MustFromString("-60.00")
	ClearPrimaryAmount(&stamped)
	require.NoError(t, database.UpdateTransaction(t.Context(), space, &stamped))

	out, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 1, out.Stamped)
	restamped, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	// 60 EUR at 0.9 EUR per USD is 66.67 USD, the new figure — not the stale one.
	require.Equal(t, "-66.67", restamped.AmountPrimary.String())
}

func TestARowAlreadyInTheSpacesMoneyIsLeftAlone(t *testing.T) {
	space := newSpace(t)
	database := db(t)
	converter := NewCurrency(database, &provider.FxRates{Store: store.NewFxRates(database)})

	account := &store.Account{
		Name: "Everyday", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	txn := &store.Transaction{
		AccountID: account.ID, Date: on, EffectiveDate: on,
		Amount: domain.MustFromString("-40.00"), Currency: "USD",
		StatementName: "SHOP", Payee: "Shop", Source: domain.SourceManual,
	}
	require.NoError(t, database.CreateTransaction(t.Context(), space, txn))

	out, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Zero(t, out.Stamped, "a USD row in a USD space needs no conversion")

	stored, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.False(t, stored.HasAmountPrimary, "and carries no converted amount")
}

func TestAForeignRowWithNoRateIsLeftAloneAndCounted(t *testing.T) {
	// A converted-at-a-guess figure is indistinguishable from a real one once
	// it is stored, so a missing rate has to stay missing and be reported.
	space := newSpace(t)
	database := db(t)
	converter := NewCurrency(database, &provider.FxRates{Store: store.NewFxRates(database)})

	account := &store.Account{
		Name: "Travel", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	txn := &store.Transaction{
		AccountID: account.ID, Date: on, EffectiveDate: on,
		Amount: domain.MustFromString("-40.00"), Currency: "JPY",
		StatementName: "TOKYO", Payee: "Tokyo", Source: domain.SourceManual,
	}
	require.NoError(t, database.CreateTransaction(t.Context(), space, txn))

	out, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Zero(t, out.Stamped)
	require.Equal(t, 1, out.Unrated)

	stored, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.False(t, stored.HasAmountPrimary)
}

func TestStampingTwiceRestatesNothing(t *testing.T) {
	// Reconverting at a moved rate would restate past spending.
	space := newSpace(t)
	database := db(t)
	fx := store.NewFxRates(database)
	converter := NewCurrency(database, &provider.FxRates{Store: fx})

	account := &store.Account{
		Name: "Travel", Kind: domain.KindCash, Type: "checking",
		Currency: "USD", IncludeInNetWorth: true,
	}
	require.NoError(t, database.CreateAccount(t.Context(), space, account))
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	require.NoError(t, fx.UpsertRates(t.Context(), []provider.FxRate{{
		SpaceID: domain.ID(space.UUID().String()), BaseCurrency: "USD", QuoteCurrency: "EUR",
		On: on, Rate: rate("0.9"), Source: "test",
	}}))
	txn := &store.Transaction{
		AccountID: account.ID, Date: on, EffectiveDate: on,
		Amount: domain.MustFromString("-40.00"), Currency: "EUR",
		StatementName: "CAFE", Payee: "Cafe", Source: domain.SourceManual,
	}
	require.NoError(t, database.CreateTransaction(t.Context(), space, txn))

	first, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, 1, first.Stamped)

	// The rate moves. The already-stamped row must not follow it.
	require.NoError(t, fx.UpsertRates(t.Context(), []provider.FxRate{{
		SpaceID: domain.ID(space.UUID().String()), BaseCurrency: "USD", QuoteCurrency: "EUR",
		On: on, Rate: rate("0.5"), Source: "test",
	}}))
	second, err := converter.StampSpace(t.Context(), space)
	require.NoError(t, err)
	require.Zero(t, second.Stamped)

	stored, err := database.GetTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.Equal(t, "-44.44", stored.AmountPrimary.String(), "still the rate of the day")
}

func TestTheStoredRateSurvivesTheTenthDecimalPlace(t *testing.T) {
	// numeric(20,10) exists for a reason; a float on the way through loses it.
	space := newSpace(t)
	fx := store.NewFxRates(db(t))
	on := domain.Date{Year: 2026, Month: 8, Day: 12}
	require.NoError(t, fx.UpsertRates(t.Context(), []provider.FxRate{{
		SpaceID: domain.ID(space.UUID().String()), BaseCurrency: "USD", QuoteCurrency: "EUR",
		On: on, Rate: rate("0.1234567891"), Source: "test",
	}}))

	got, ok, err := fx.ExactRate(t.Context(), domain.ID(space.UUID().String()), "EUR", on)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0.1234567891", got.String())
}

func TestTheClosestRatePrefersTheDayBefore(t *testing.T) {
	// A rate from the day before is what the money was worth; one from next
	// week is a guess about a day that had not happened.
	space := newSpace(t)
	fx := store.NewFxRates(db(t))
	id := domain.ID(space.UUID().String())
	require.NoError(t, fx.UpsertRates(t.Context(), []provider.FxRate{
		{SpaceID: id, BaseCurrency: "USD", QuoteCurrency: "EUR",
			On: domain.Date{Year: 2026, Month: 8, Day: 10}, Rate: rate("0.90"), Source: "t"},
		{SpaceID: id, BaseCurrency: "USD", QuoteCurrency: "EUR",
			On: domain.Date{Year: 2026, Month: 8, Day: 20}, Rate: rate("0.80"), Source: "t"},
	}))

	got, ok, err := fx.ClosestRate(t.Context(), id, "EUR",
		domain.Date{Year: 2026, Month: 8, Day: 12})
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "0.9", got.String())
}
