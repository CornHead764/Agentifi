package service

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func addForecastRow(t *testing.T, space store.SpaceID, account *store.Account, on domain.Date, amount string) {
	t.Helper()
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, &store.Transaction{
		AccountID: account.ID, Date: on, Amount: domain.MustFromString(amount),
		Currency: "USD", StatementName: "X", Payee: "X", Source: domain.SourceManual,
	}))
}

func TestTheDailyPassEstimatesEachAccountOnceTheSyncWindowOpens(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Forecast Checking")
	today := domain.NewDate(2026, time.September, 10)
	addForecastRow(t, space, account, domain.NewDate(2026, time.July, 1), "2000")
	addForecastRow(t, space, account, domain.NewDate(2026, time.July, 15), "-900")
	addForecastRow(t, space, account, domain.NewDate(2026, time.August, 1), "2000")
	addForecastRow(t, space, account, domain.NewDate(2026, time.August, 20), "-1300")

	clock := today.Time().Add(3 * time.Hour)
	scheduler := &Scheduler{
		Store: db(t), Log: quietLogger(), At: SyncWindow{Hour: 4},
		Forecasts: NewAccountForecasts(db(t)),
	}
	scheduler.refreshForecasts(t.Context(), clock)
	_, err := db(t).LatestAccountForecast(t.Context(), space, account.ID)
	require.ErrorIs(t, err, store.ErrNotFound, "nothing is estimated before the window opens")

	clock = today.Time().Add(4*time.Hour + 5*time.Minute)
	scheduler.refreshForecasts(t.Context(), clock)
	row, err := db(t).LatestAccountForecast(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, store.ForecastMethodAverage, row.Method)
	require.Equal(t, today, row.GeneratedOn)
	require.Equal(t, "2026-09", row.Forecast.Months[0].Month.String())
	// July and August: 4000 in and 2200 out over two months.
	require.Equal(t, "2000.00", row.Forecast.Months[0].In.String())
	require.Equal(t, "1100.00", row.Forecast.Months[0].Out.String())

	// Once a day: a second pass the same day stores nothing new.
	scheduler.refreshForecasts(t.Context(), clock.Add(time.Hour))
	again, err := db(t).LatestAccountForecast(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.Equal(t, row.ID, again.ID)
}

func TestReEstimatingAnAccountReplacesItsLastEstimate(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Replaced Checking")
	addForecastRow(t, space, account, domain.NewDate(2026, time.August, 3), "-60")
	forecasts := NewAccountForecasts(db(t))
	today := domain.NewDate(2026, time.September, 10)

	first, ok, err := forecasts.RefreshAccount(t.Context(), space, account.ID, today)
	require.NoError(t, err)
	require.True(t, ok)
	second, ok, err := forecasts.RefreshAccount(t.Context(), space, account.ID, today)
	require.NoError(t, err)
	require.True(t, ok)
	require.NotEqual(t, first.ID, second.ID)

	var count int
	require.NoError(t, db(t).Conn().QueryRow(t.Context(),
		`SELECT count(*) FROM cash_flow_forecasts WHERE account_id = $1`, account.ID).Scan(&count))
	require.Equal(t, 1, count)
}

func TestAnAccountWithNoCompleteMonthIsNotEstimated(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "New Checking")
	today := domain.NewDate(2026, time.September, 10)
	addForecastRow(t, space, account, domain.NewDate(2026, time.September, 2), "-60")

	_, ok, err := NewAccountForecasts(db(t)).RefreshAccount(t.Context(), space, account.ID, today)
	require.NoError(t, err)
	require.False(t, ok)
}
