package service

import (
	"context"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func storedFilter(t *testing.T, spaceID store.SpaceID, field domain.FilterField) uuid.UUID {
	t.Helper()
	filter := &store.Filter{Name: "Coffee", Scope: "watchlist", Items: []store.FilterItem{{
		Field: string(field), Operator: string(domain.OpContains), ValueTexts: []string{"coffee"},
	}}}
	require.NoError(t, db(t).CreateFilter(t.Context(), spaceID, filter))
	return filter.ID
}

func TestPrepareFiltersSkipsAMissingFilterAndKeepsTheRest(t *testing.T) {
	space := newSpace(t)
	kept := storedFilter(t, space, domain.FieldPayee)
	missing := uuid.New()

	filters, facets, err := PrepareFilters(t.Context(), db(t), space,
		[]uuid.UUID{kept, missing, kept}, map[uuid.UUID]store.Transaction{})
	require.NoError(t, err)
	require.Len(t, filters, 1)
	require.Contains(t, filters, domain.ID(kept.String()))
	require.NotNil(t, facets)
}

func TestPrepareFiltersRefusesAFilterTheEvaluatorCannotRun(t *testing.T) {
	// An unknown field's item would quietly match less, and the figure built
	// on it would still look like a fact.
	space := newSpace(t)
	broken := storedFilter(t, space, domain.FilterField("not_a_field"))

	_, _, err := PrepareFilters(t.Context(), db(t), space, []uuid.UUID{broken}, nil)
	require.ErrorIs(t, err, ErrInvalidFilter)
}

func TestPrepareFiltersReturnsADatabaseFailure(t *testing.T) {
	// A failed read is not a missing filter: skipping it would read as nothing
	// spent.
	space := newSpace(t)
	kept := storedFilter(t, space, domain.FieldPayee)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	filters, _, err := PrepareFilters(ctx, db(t), space, []uuid.UUID{kept}, nil)
	require.Error(t, err)
	require.NotErrorIs(t, err, store.ErrNotFound)
	require.Nil(t, filters)
}

func TestTheWatchlistAlertSurfacesAFilterItCannotRead(t *testing.T) {
	alerts, space, _, _ := alertFixture(t)
	broken := storedFilter(t, space, domain.FilterField("not_a_field"))
	require.NoError(t, db(t).CreateWatchlist(t.Context(), space, &store.Watchlist{
		FilterID: broken, Name: "Coffee", Period: "monthly",
		TargetAmount: domain.MustFromString("50.00"), HasTarget: true,
	}))

	_, err := alerts.watchlists(t.Context(), space, nil, nil, nil, domain.DateOf(alerts.now()))
	require.ErrorIs(t, err, ErrInvalidFilter)
}
