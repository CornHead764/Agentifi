package service

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSQLitePlanMonthPersistence(t *testing.T) {
	st := storetest.New(t)
	id := storetest.NewHousehold(t, st)
	server, err := st.HouseholdDB(t.Context(), id)
	require.NoError(t, err)
	_, err = server.Write().ExecContext(t.Context(), `UPDATE household_metadata SET primary_currency = 'KWD' WHERE id = 1`)
	require.NoError(t, err)
	scoped, err := st.ForHousehold(t.Context(), id)
	require.NoError(t, err)
	p := NewPlan(scoped)
	on, err := domain.ParseDate("2026-10-01")
	require.NoError(t, err)
	row := NewPlanMonthRow(domain.MonthOf(on))
	require.NoError(t, scoped.InHouseholdTx(t.Context(), id, func(tx *store.Store) error {
		db, err := tx.HouseholdDB(t.Context(), id)
		if err != nil {
			return err
		}
		row.ID, err = insertMonth(t.Context(), db, row)
		return err
	}))
	row.Excluded[0] = []uuid.UUID{uuid.New()}
	row.Over[0] = domain.MustFromString("12.345")
	row.HasOver[0] = true
	row.ProjectionBuffer = domain.MustFromString("0.001")
	require.NoError(t, p.SaveMonthUserState(t.Context(), id, row))
	loaded, err := p.readMonth(t.Context(), id, row.Month)
	require.NoError(t, err)
	require.True(t, loaded.Over[0].Equal(row.Over[0]))
	require.True(t, loaded.HasOver[0])
	require.Equal(t, row.Excluded[0], loaded.Excluded[0])
	require.True(t, loaded.ProjectionBuffer.Equal(row.ProjectionBuffer))
	all, err := p.listMonths(t.Context(), id)
	require.NoError(t, err)
	require.Len(t, all, 1)
	row.ProjectionBuffer = domain.MustFromString("0.0001")
	require.Error(t, p.SaveMonthUserState(t.Context(), id, row), "fractional minor units must not round")
	other := storetest.NewHousehold(t, st)
	_, err = NewPlan(st).readMonth(t.Context(), other, row.Month)
	require.ErrorIs(t, err, store.ErrNotFound)
}
