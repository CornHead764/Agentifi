package store

import (
	"github.com/stretchr/testify/require"
	"testing"
)

func TestHouseholdScopeMaterializesAndRefreshesCurrency(t *testing.T) {
	st := newSQLiteTest(t)
	id := newSQLiteSpace(t, st)
	_, err := st.primaryCurrency()
	require.Error(t, err)
	first, err := st.scoped(t.Context(), id)
	require.NoError(t, err)
	currency, err := first.primaryCurrency()
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
	_, err = st.sqlite.server.Write().ExecContext(t.Context(), `UPDATE households SET primary_currency = 'JPY' WHERE id = ?`, id.UUID())
	require.NoError(t, err)
	cached, err := st.scoped(t.Context(), id)
	require.NoError(t, err)
	currency, err = cached.primaryCurrency()
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
	refreshed, err := st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	currency, err = refreshed.primaryCurrency()
	require.NoError(t, err)
	require.Equal(t, "USD", currency)
	mirror, err := st.GetSpace(t.Context(), id)
	require.NoError(t, err)
	require.Equal(t, "USD", mirror.PrimaryCurrency)
	household, err := st.HouseholdDB(t.Context(), id)
	require.NoError(t, err)
	_, err = household.Write().ExecContext(t.Context(), `UPDATE household_metadata SET primary_currency = 'JPY' WHERE id = 1`)
	require.NoError(t, err)
	refreshed, err = st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	_, err = first.scoped(t.Context(), NewSpaceID())
	require.Error(t, err)
	require.NoError(t, refreshed.InTx(t.Context(), func(tx *Store) error {
		same, err := tx.scoped(t.Context(), id)
		require.NoError(t, err)
		require.Same(t, tx, same)
		currency, err := same.primaryCurrency()
		require.NoError(t, err)
		require.Equal(t, "JPY", currency)
		_, err = tx.ForHousehold(t.Context(), id)
		require.Error(t, err)
		return nil
	}))
	require.NoError(t, st.InTx(t.Context(), func(tx *Store) error {
		_, err := tx.scoped(t.Context(), id)
		require.Error(t, err)
		return nil
	}))
}
