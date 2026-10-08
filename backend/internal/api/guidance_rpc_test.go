package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// GuidanceService over its own protocol. guidance_test.go reaches the same
// methods through the REST bridge.

func createCornerNote(t *testing.T, l *ledger) *agentifiv1.Guidance {
	t.Helper()
	res, err := call[agentifiv1.CreateGuidanceRequest, agentifiv1.CreateGuidanceResponse](
		l.alex, agentifiv1connect.GuidanceServiceCreateGuidanceProcedure, &agentifiv1.CreateGuidanceRequest{
			Name:        "Corner Store",
			Instruction: "A charge here is coffee.",
			Conditions:  cornerConditions(),
		})
	require.Nil(t, err)
	return res.GetGuidance()
}

func TestANoteCreatedOverRPCDescribesItsConditions(t *testing.T) {
	l := buildLedger(t)
	note := createCornerNote(t, l)
	require.True(t, note.GetOwnsFilter())
	require.True(t, note.GetIsActive())
	require.Equal(t, `the statement name contains "CORNER"`, note.GetAppliesTo())

	listed, err := call[agentifiv1.ListGuidanceRequest, agentifiv1.ListGuidanceResponse](
		l.as("vera"), agentifiv1connect.GuidanceServiceListGuidanceProcedure, &agentifiv1.ListGuidanceRequest{})
	require.Nil(t, err)
	require.Len(t, listed.GetGuidance(), 1)
}

func TestANoteUpdateChangesWhatItsMaskNames(t *testing.T) {
	l := buildLedger(t)
	note := createCornerNote(t, l)

	res, err := call[agentifiv1.UpdateGuidanceRequest, agentifiv1.UpdateGuidanceResponse](
		l.alex, agentifiv1connect.GuidanceServiceUpdateGuidanceProcedure, &agentifiv1.UpdateGuidanceRequest{
			GuidanceId: note.GetId(),
			IsActive:   proto.Bool(false),
			Conditions: []*agentifiv1.FilterItemWrite{{
				Field: string(domain.FieldStatementName), Operator: string(domain.OpContains),
				ValueTexts: []string{"STORE"},
			}},
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_active", "conditions"}},
		})
	require.Nil(t, err)
	require.False(t, res.GetGuidance().GetIsActive())
	require.Equal(t, "A charge here is coffee.", res.GetGuidance().GetInstruction())
	require.Equal(t, `the statement name contains "STORE"`, res.GetGuidance().GetAppliesTo())

	// The pointer at the conditions cannot be cleared: the note would match
	// nothing while it still looks live.
	l.alex.rpc(agentifiv1connect.GuidanceServiceUpdateGuidanceProcedure, map[string]any{
		"guidance_id": note.GetId(), "update_mask": "filterId",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestAPartialGuidanceOrderIsRefused(t *testing.T) {
	l := buildLedger(t)
	createCornerNote(t, l)
	_, err := call[agentifiv1.ReorderGuidanceRequest, agentifiv1.ReorderGuidanceResponse](
		l.alex, agentifiv1connect.GuidanceServiceReorderGuidanceProcedure, &agentifiv1.ReorderGuidanceRequest{})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, "incomplete", problemIn(t, err).GetFields()[0].GetType())
}

func TestAnotherSpacesNoteIsNotFoundAndAViewerCannotDeleteOne(t *testing.T) {
	l := buildLedger(t)
	note := createCornerNote(t, l)

	bob := l.as("bob").inSpace(store.SpaceIDOf(l.id("other_space")))
	_, err := call[agentifiv1.GetGuidanceRequest, agentifiv1.GetGuidanceResponse](
		bob, agentifiv1connect.GuidanceServiceGetGuidanceProcedure, &agentifiv1.GetGuidanceRequest{GuidanceId: note.GetId()})
	require.Equal(t, connect.CodeNotFound, err.Code())

	_, err = call[agentifiv1.DeleteGuidanceRequest, agentifiv1.DeleteGuidanceResponse](
		l.as("vera"), agentifiv1connect.GuidanceServiceDeleteGuidanceProcedure,
		&agentifiv1.DeleteGuidanceRequest{GuidanceId: note.GetId()})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
}
