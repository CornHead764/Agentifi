package service

import (
	"database/sql"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
)

func TestSQLiteDirectSQLHouseholdReads(t *testing.T) {
	st := storetest.New(t)
	id := storetest.NewHousehold(t, st)
	other := storetest.NewHousehold(t, st)
	ctx := t.Context()
	transfers := NewTransfers(st)
	legs, err := transfers.loadLegs(ctx, id, domain.Date{}, domain.Date{})
	require.NoError(t, err)
	require.Empty(t, legs)
	pairs, err := transfers.FindOrphanPairs(ctx, id)
	require.NoError(t, err)
	require.Empty(t, pairs)
	_, err = transfers.RepairOrphanPairs(ctx, id)
	require.NoError(t, err)
	rules, err := NewRules(st).LoadRules(ctx, id)
	require.NoError(t, err)
	require.Empty(t, rules)
	matcher := NewSeriesMatcher(st)
	ids := []uuid.UUID{uuid.New()}
	_, _, err = matcher.learned(ctx, id, ids)
	require.NoError(t, err)
	from := domain.NewDate(2026, 1, 1)
	to := domain.NewDate(2026, 12, 31)
	_, err = matcher.loadPlaceholders(ctx, id, ids, from, to)
	require.NoError(t, err)
	_, err = matcher.loadSettled(ctx, id, ids, from, to)
	require.NoError(t, err)
	_, err = matcher.MatchHistory(ctx, other, ids, from, to)
	require.NoError(t, err)
	_, err = NewAlerts(st).goalStandings(ctx, id, nil, from)
	require.NoError(t, err)
	_, err = NewSync(st, nil).newestTransactionDate(ctx, id, uuid.New())
	require.NoError(t, err)
}

func TestSQLiteDirectSQLCodecs(t *testing.T) {
	marks, args := store.InArgs([]string{"a", "b"})
	require.Equal(t, "(?,?)", marks)
	require.Equal(t, []any{"a", "b"}, args)
	marks, args = store.InArgs([]string(nil))
	require.Equal(t, "(NULL)", marks)
	require.Empty(t, args)
	require.Nil(t, store.NullDate(domain.Date{}))
	require.Equal(t, "2026-01-01", store.NullDate(domain.NewDate(2026, 1, 1)))
	require.True(t, nullDate(sql.Null[string]{}).IsZero())
}
