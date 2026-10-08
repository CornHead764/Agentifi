package api

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// WatchlistService over its own protocol. watchlists_test.go reaches the same
// methods through the REST bridge.

func createFoodWatchlist(t *testing.T, l *ledger) *agentifiv1.WatchlistSummary {
	t.Helper()
	res, err := call[agentifiv1.CreateWatchlistRequest, agentifiv1.CreateWatchlistResponse](
		l.alex, agentifiv1connect.WatchlistServiceCreateWatchlistProcedure, &agentifiv1.CreateWatchlistRequest{
			Name:         "Food",
			CategoryIds:  []string{l.str("food")},
			TargetAmount: &agentifiv1.NullableMoney{Amount: "400.00"},
		})
	require.Nil(t, err)
	return res.GetWatchlist()
}

func TestAWatchlistCreatedOverRPCCarriesItsTarget(t *testing.T) {
	l := buildLedger(t)
	card := createFoodWatchlist(t, l)
	require.Equal(t, "400.00", card.GetTargetAmount().GetAmount())
	require.NotNil(t, card.GetLeftToTarget())
	require.Equal(t, "month", card.GetPeriod())
	require.Len(t, card.GetMonthlyTrend(), defaultTrendMonths)

	detail, err := call[agentifiv1.GetWatchlistRequest, agentifiv1.GetWatchlistResponse](
		l.as("vera"), agentifiv1connect.WatchlistServiceGetWatchlistProcedure,
		&agentifiv1.GetWatchlistRequest{WatchlistId: card.GetId(), Month: "2026-08"})
	require.Nil(t, err)
	require.Equal(t, "2026-08", detail.GetWatchlist().GetMonth())
	require.Equal(t, card.GetId(), detail.GetWatchlist().GetId())
}

func TestClearingAWatchlistTargetLeavesNothingToBreach(t *testing.T) {
	l := buildLedger(t)
	card := createFoodWatchlist(t, l)
	res, err := call[agentifiv1.UpdateWatchlistRequest, agentifiv1.UpdateWatchlistResponse](
		l.alex, agentifiv1connect.WatchlistServiceUpdateWatchlistProcedure, &agentifiv1.UpdateWatchlistRequest{
			WatchlistId: card.GetId(),
			UpdateMask:  &fieldmaskpb.FieldMask{Paths: []string{"target_amount"}},
		})
	require.Nil(t, err)
	require.Nil(t, res.GetWatchlist().GetTargetAmount())
	require.Nil(t, res.GetWatchlist().GetLeftToTarget())
	require.Nil(t, res.GetWatchlist().PctOfTarget)
	require.Equal(t, "Food", res.GetWatchlist().GetName())
}

func TestAWatchlistUpdateTakesFilterIdOrItemsNotBoth(t *testing.T) {
	l := buildLedger(t)
	card := createFoodWatchlist(t, l)
	_, err := call[agentifiv1.UpdateWatchlistRequest, agentifiv1.UpdateWatchlistResponse](
		l.alex, agentifiv1connect.WatchlistServiceUpdateWatchlistProcedure, &agentifiv1.UpdateWatchlistRequest{
			WatchlistId: card.GetId(),
			FilterId:    &[]string{l.str("filter")}[0],
			Items: []*agentifiv1.FilterItemWrite{{
				Field: string(domain.FieldCategory), ValueIds: []string{l.str("groceries")},
			}},
		})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())

	// Items alone rewrite the watchlist's own filter in place.
	res, err := call[agentifiv1.UpdateWatchlistRequest, agentifiv1.UpdateWatchlistResponse](
		l.alex, agentifiv1connect.WatchlistServiceUpdateWatchlistProcedure, &agentifiv1.UpdateWatchlistRequest{
			WatchlistId: card.GetId(),
			Items: []*agentifiv1.FilterItemWrite{{
				Field: string(domain.FieldCategory), ValueIds: []string{l.str("groceries")},
			}},
		})
	require.Nil(t, err)
	require.Equal(t, card.GetFilterId(), res.GetWatchlist().GetFilterId())
}

func TestAnotherSpacesWatchlistIsNotFoundAndAViewerCannotDeleteOne(t *testing.T) {
	l := buildLedger(t)
	card := createFoodWatchlist(t, l)

	bob := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space")))
	_, err := call[agentifiv1.GetWatchlistRequest, agentifiv1.GetWatchlistResponse](
		bob, agentifiv1connect.WatchlistServiceGetWatchlistProcedure,
		&agentifiv1.GetWatchlistRequest{WatchlistId: card.GetId()})
	require.Equal(t, connect.CodeNotFound, err.Code())

	_, err = call[agentifiv1.DeleteWatchlistRequest, agentifiv1.DeleteWatchlistResponse](
		l.as("vera"), agentifiv1connect.WatchlistServiceDeleteWatchlistProcedure,
		&agentifiv1.DeleteWatchlistRequest{WatchlistId: card.GetId()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
