package api

import (
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
)

// SetupGuideService and SuggestionBatchService over their own protocol.

func TestAnUnsetSkippedListIsLeftAloneAndAnEmptyOneEmptiesIt(t *testing.T) {
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateSetupGuideRequest) *agentifiv1.SetupGuide {
		t.Helper()
		res, err := call[agentifiv1.UpdateSetupGuideRequest, agentifiv1.UpdateSetupGuideResponse](
			l.alex, agentifiv1connect.SetupGuideServiceUpdateSetupGuideProcedure, req)
		require.Nil(t, err)
		return res.GetGuide()
	}

	guide := update(&agentifiv1.UpdateSetupGuideRequest{
		Skipped: &agentifiv1.IdSet{Ids: []string{stepBills, stepBills}},
	})
	require.Equal(t, []string{stepBills}, guide.GetSkipped())

	guide = update(&agentifiv1.UpdateSetupGuideRequest{Dismissed: proto.Bool(true)})
	require.True(t, guide.GetDismissed())
	require.Equal(t, []string{stepBills}, guide.GetSkipped())

	guide = update(&agentifiv1.UpdateSetupGuideRequest{Skipped: &agentifiv1.IdSet{}})
	require.Empty(t, guide.GetSkipped())
	require.True(t, guide.GetDismissed())
}

func TestAStepThatCannotBeSkippedIsRefused(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.UpdateSetupGuideRequest, agentifiv1.UpdateSetupGuideResponse](
		l.alex, agentifiv1connect.SetupGuideServiceUpdateSetupGuideProcedure,
		&agentifiv1.UpdateSetupGuideRequest{Skipped: &agentifiv1.IdSet{Ids: []string{stepMatch}}})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())

	_, err = call[agentifiv1.UpdateSetupGuideRequest, agentifiv1.UpdateSetupGuideResponse](
		l.as("vera"), agentifiv1connect.SetupGuideServiceUpdateSetupGuideProcedure,
		&agentifiv1.UpdateSetupGuideRequest{Dismissed: proto.Bool(true)})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}

func TestNoBatchToFollowIsAnUnsetBatch(t *testing.T) {
	l := buildLedger(t)
	res, err := call[agentifiv1.GetLatestSuggestionBatchRequest, agentifiv1.GetLatestSuggestionBatchResponse](
		l.as("vera"), agentifiv1connect.SuggestionBatchServiceGetLatestSuggestionBatchProcedure,
		&agentifiv1.GetLatestSuggestionBatchRequest{})
	require.Nil(t, err)
	require.Nil(t, res.GetBatch())

	_, err = call[agentifiv1.GetSuggestionBatchRequest, agentifiv1.GetSuggestionBatchResponse](
		l.alex, agentifiv1connect.SuggestionBatchServiceGetSuggestionBatchProcedure,
		&agentifiv1.GetSuggestionBatchRequest{BatchId: l.str("tag")})
	require.Equal(t, connect.CodeNotFound, err.Code())

	_, err = call[agentifiv1.DismissSuggestionBatchRequest, agentifiv1.DismissSuggestionBatchResponse](
		l.as("vera"), agentifiv1connect.SuggestionBatchServiceDismissSuggestionBatchProcedure,
		&agentifiv1.DismissSuggestionBatchRequest{BatchId: l.str("tag")})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
