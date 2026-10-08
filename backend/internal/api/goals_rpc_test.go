package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// GoalService over its own protocol. goals_test.go reaches the same methods
// through the REST bridge.

func createRainyDayGoal(t *testing.T, l *ledger) *agentifiv1.Goal {
	t.Helper()
	res, err := call[agentifiv1.CreateGoalRequest, agentifiv1.CreateGoalResponse](
		l.alex, agentifiv1connect.GoalServiceCreateGoalProcedure, &agentifiv1.CreateGoalRequest{
			Name:         "Rainy day",
			AccountId:    l.str("checking"),
			TargetAmount: &agentifiv1.Money{Amount: "1000.00"},
		})
	require.Nil(t, err)
	return res.GetGoal()
}

func TestAGoalCreatedOverRPCListsWithItsFigures(t *testing.T) {
	l := buildLedger(t)
	goal := createRainyDayGoal(t, l)
	require.Equal(t, "1000.00", goal.GetTargetAmount().GetAmount())
	require.Equal(t, "Everyday Checking", goal.GetAccountName())
	require.Equal(t, "0.00", goal.GetSavedSoFar().GetAmount())
	require.Nil(t, goal.Emoji)
	require.Nil(t, goal.TargetOn)
	require.Nil(t, goal.MonthlyNeeded, "an open-ended goal has no required rate")
	require.True(t, goal.GetIsTakenFromPlan())

	listed, err := call[agentifiv1.ListGoalsRequest, agentifiv1.ListGoalsResponse](
		l.as("vera"), agentifiv1connect.GoalServiceListGoalsProcedure, &agentifiv1.ListGoalsRequest{})
	require.Nil(t, err)
	require.Len(t, listed.GetGoals(), 1)
	require.Equal(t, goal.GetId(), listed.GetGoals()[0].GetId())
}

func TestAGoalUpdateLeavesAloneWhatTheMaskDoesNotName(t *testing.T) {
	l := buildLedger(t)
	goal := createRainyDayGoal(t, l)
	update := func(req *agentifiv1.UpdateGoalRequest) *agentifiv1.Goal {
		t.Helper()
		req.GoalId = goal.GetId()
		res, err := call[agentifiv1.UpdateGoalRequest, agentifiv1.UpdateGoalResponse](
			l.alex, agentifiv1connect.GoalServiceUpdateGoalProcedure, req)
		require.Nil(t, err)
		return res.GetGoal()
	}

	// Set.
	updated := update(&agentifiv1.UpdateGoalRequest{
		Emoji: proto.String("☂"), TargetOn: proto.String("2030-01-01"),
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"emoji", "target_on"}},
	})
	require.Equal(t, "☂", updated.GetEmoji())
	require.Equal(t, "2030-01-01", updated.GetTargetOn())
	require.NotNil(t, updated.MonthlyNeeded)

	// Absent: a new target leaves the emoji and the date.
	updated = update(&agentifiv1.UpdateGoalRequest{
		TargetAmount: &agentifiv1.NullableMoney{Amount: "1200.00"},
		UpdateMask:   &fieldmaskpb.FieldMask{Paths: []string{"target_amount"}},
	})
	require.Equal(t, "1200.00", updated.GetTargetAmount().GetAmount())
	require.Equal(t, "☂", updated.GetEmoji())

	// Cleared: named and unset.
	updated = update(&agentifiv1.UpdateGoalRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"emoji", "target_on"}},
	})
	require.Nil(t, updated.Emoji)
	require.Nil(t, updated.TargetOn)
	require.Nil(t, updated.MonthlyNeeded)

	// A required field cannot be cleared, as over REST.
	l.alex.rpc(agentifiv1connect.GoalServiceUpdateGoalProcedure, map[string]any{
		"goal_id": goal.GetId(), "update_mask": "targetAmount",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestAnUnsetIdSetLeavesTheLinksAndAnEmptyOneEmptiesThem(t *testing.T) {
	l := buildLedger(t)
	goal := createRainyDayGoal(t, l)
	update := func(req *agentifiv1.UpdateGoalRequest) *agentifiv1.Goal {
		t.Helper()
		req.GoalId = goal.GetId()
		res, err := call[agentifiv1.UpdateGoalRequest, agentifiv1.UpdateGoalResponse](
			l.alex, agentifiv1connect.GoalServiceUpdateGoalProcedure, req)
		require.Nil(t, err)
		return res.GetGoal()
	}

	linked := update(&agentifiv1.UpdateGoalRequest{
		FundingAccountIds: &agentifiv1.IdSet{Ids: []string{l.str("checking")}},
	})
	require.Equal(t, []string{l.str("checking")}, linked.GetFundingAccountIds())

	kept := update(&agentifiv1.UpdateGoalRequest{Name: proto.String("Rainy days")})
	require.Equal(t, []string{l.str("checking")}, kept.GetFundingAccountIds())

	emptied := update(&agentifiv1.UpdateGoalRequest{FundingAccountIds: &agentifiv1.IdSet{}})
	require.Empty(t, emptied.GetFundingAccountIds())
}

func TestAMalformedAccountIdIsRefusedAtItsField(t *testing.T) {
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateGoalRequest, agentifiv1.CreateGoalResponse](
		l.alex, agentifiv1connect.GoalServiceCreateGoalProcedure,
		&agentifiv1.CreateGoalRequest{Name: "Trip", AccountId: "not-an-id"})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "account_id"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAnotherSpacesGoalIsNotFoundAndAViewerCannotCloseOne(t *testing.T) {
	l := buildLedger(t)
	goal := createRainyDayGoal(t, l)

	bob := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space")))
	_, err := call[agentifiv1.CloseGoalRequest, agentifiv1.CloseGoalResponse](
		bob, agentifiv1connect.GoalServiceCloseGoalProcedure, &agentifiv1.CloseGoalRequest{GoalId: goal.GetId()})
	require.Equal(t, connect.CodeNotFound, err.Code())

	_, err = call[agentifiv1.CloseGoalRequest, agentifiv1.CloseGoalResponse](
		l.as("vera"), agentifiv1connect.GoalServiceCloseGoalProcedure, &agentifiv1.CloseGoalRequest{GoalId: goal.GetId()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())

	closed, err := call[agentifiv1.CloseGoalRequest, agentifiv1.CloseGoalResponse](
		l.alex, agentifiv1connect.GoalServiceCloseGoalProcedure, &agentifiv1.CloseGoalRequest{GoalId: goal.GetId()})
	require.Nil(t, err)
	require.Equal(t, "closed", closed.GetGoal().GetStage())
	require.NotNil(t, closed.GetGoal().ClosedOn)
}
