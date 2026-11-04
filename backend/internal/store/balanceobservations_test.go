package store

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestBalanceObservationsRoundTrip(t *testing.T) {
	space := newSpace(t)
	checking := newAccount(t, space, "Observed Checking")
	other := newAccount(t, space, "Observed Savings")
	first := time.Date(2026, time.August, 1, 6, 0, 0, 0, time.UTC)
	observation := func(at time.Time, reported string) domain.BalanceObservation {
		return domain.BalanceObservation{
			At: at, Reported: domain.MustFromString(reported),
			Ledger: domain.MustFromString("-12.34"), Settled: domain.MustFromString("-2.34"),
			Movement: domain.MustFromString("-10.00"), SettledMovement: domain.MustFromString("0.00"),
		}
	}
	// Written newest first: the listing orders by when each was observed.
	later := observation(first.Add(24*time.Hour), "990.00")
	earlier := observation(first, "1000.00")
	require.NoError(t, db(t).RecordBalanceObservation(t.Context(), space, checking.ID, later))
	require.NoError(t, db(t).RecordBalanceObservation(t.Context(), space, checking.ID, earlier))
	require.NoError(t, db(t).RecordBalanceObservation(t.Context(), space, other.ID, earlier))

	got, err := db(t).ListBalanceObservations(t.Context(), space, checking.ID)
	require.NoError(t, err)
	require.Equal(t, []domain.BalanceObservation{earlier, later}, got)

	all, err := db(t).BalanceObservationsByAccount(t.Context(), space)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.Equal(t, []domain.BalanceObservation{earlier}, all[other.ID])
}

func TestTheSyncsPendingReadingSurvivesAnAccountSave(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Read Checking")
	require.NoError(t, db(t).SetAccountPendingReading(t.Context(), space, account.ID, true))

	// A settings save carries a copy read before the sync decided.
	require.NoError(t, db(t).UpdateAccount(t.Context(), space, account))
	stored, err := db(t).GetAccount(t.Context(), space, account.ID)
	require.NoError(t, err)
	require.True(t, stored.BankIncludesPending)
	require.True(t, DomainAccount(stored).BankIncludesPending)
}
