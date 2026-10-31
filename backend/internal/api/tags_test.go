package api

import (
	"net/http"
	"testing"

	"connectrpc.com/connect"
	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
)

// TagService over its own protocol. The REST URLs it used to answer are the
// bridge's, and ledger_test.go and the assistant's tests still reach them.
//
// Expected values come from the update_mask rules in docs/development.md and
// from the statuses and bodies the REST routes returned before the bridge.

func TestListTagsScopedToSpace(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	res, err := call[agentifiv1.ListTagsRequest, agentifiv1.ListTagsResponse](
		l.alex, agentifiv1connect.TagServiceListTagsProcedure, &agentifiv1.ListTagsRequest{})
	require.Nil(t, err)
	require.Len(t, res.GetTags(), 1)
	require.Equal(t, l.str("tag"), res.GetTags()[0].GetId())
	require.Equal(t, "reimbursable", res.GetTags()[0].GetName())
	require.Nil(t, res.GetTags()[0].Color)
}

func TestCreateTag(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	created, err := call[agentifiv1.CreateTagRequest, agentifiv1.CreateTagResponse](
		l.alex, agentifiv1connect.TagServiceCreateTagProcedure,
		&agentifiv1.CreateTagRequest{Name: "Vacation", Color: proto.String("#00aa00")})
	require.Nil(t, err)

	read, err := call[agentifiv1.GetTagRequest, agentifiv1.GetTagResponse](
		l.alex, agentifiv1connect.TagServiceGetTagProcedure,
		&agentifiv1.GetTagRequest{TagId: created.GetTag().GetId()})
	require.Nil(t, err)
	require.Equal(t, "Vacation", read.GetTag().GetName())
	require.Equal(t, "#00aa00", read.GetTag().GetColor())
}

func TestUpdateKeepsFieldsOutsideMask(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	update := func(req *agentifiv1.UpdateTagRequest) *agentifiv1.Tag {
		t.Helper()
		req.TagId = l.str("tag")
		res, err := call[agentifiv1.UpdateTagRequest, agentifiv1.UpdateTagResponse](
			l.alex, agentifiv1connect.TagServiceUpdateTagProcedure, req)
		require.Nil(t, err)
		return res.GetTag()
	}

	// Set.
	tag := update(&agentifiv1.UpdateTagRequest{
		Color: proto.String("#123456"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"color"}},
	})
	require.Equal(t, "#123456", tag.GetColor())

	// Absent: a rename leaves the colour as it was.
	tag = update(&agentifiv1.UpdateTagRequest{
		Name: proto.String("work"), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
	})
	require.Equal(t, "work", tag.GetName())
	require.Equal(t, "#123456", tag.GetColor())

	// Cleared: named in the mask and unset.
	tag = update(&agentifiv1.UpdateTagRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"color"}}})
	require.Nil(t, tag.Color)
	require.Equal(t, "work", tag.GetName())
}

func TestUpdateWithoutMask(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	l.alex.rpc(agentifiv1connect.TagServiceUpdateTagProcedure,
		map[string]any{"tag_id": l.str("tag"), "color": "#abcdef"}).requireStatus(http.StatusOK)
	body := l.alex.rpc(agentifiv1connect.TagServiceUpdateTagProcedure,
		map[string]any{"tag_id": l.str("tag"), "name": "renamed"}).requireStatus(http.StatusOK).json()
	tag := body["tag"].(map[string]any)
	require.Equal(t, "renamed", tag["name"])
	require.Equal(t, "#abcdef", tag["color"])
}

func TestUpdateRefusesFieldOutsideMask(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	l.alex.rpc(agentifiv1connect.TagServiceUpdateTagProcedure, map[string]any{
		"tag_id": l.str("tag"), "name": "renamed", "color": "#abcdef", "update_mask": "name",
	}).requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
}

func TestNullNameRefusedAsOnREST(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	refused := l.alex.rpc(agentifiv1connect.TagServiceCreateTagProcedure, `{"name": null}`).
		requireCode(connect.CodeInvalidArgument).
		requireStatus(http.StatusUnprocessableEntity)
	detail := refused.json()["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"body", "name"}, detail["loc"])
	require.Equal(t, "missing", detail["type"])

	// Clearing a name the tag already has is a 409, as applyRequired answers
	// for every patch.
	l.alex.rpc(agentifiv1connect.TagServiceUpdateTagProcedure, map[string]any{
		"tag_id": l.str("tag"), "update_mask": "name",
	}).requireCode(connect.CodeFailedPrecondition).requireStatus(http.StatusConflict)
}

func TestViewerCannotWriteTag(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	_, err := call[agentifiv1.CreateTagRequest, agentifiv1.CreateTagResponse](
		l.as("vera"), agentifiv1connect.TagServiceCreateTagProcedure,
		&agentifiv1.CreateTagRequest{Name: "Mine"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())

	// And reads as anybody in the space does.
	_, err = call[agentifiv1.ListTagsRequest, agentifiv1.ListTagsResponse](
		l.as("vera"), agentifiv1connect.TagServiceListTagsProcedure, &agentifiv1.ListTagsRequest{})
	require.Nil(t, err)
}

func TestForeignSpaceNotFound(t *testing.T) {
	t.Parallel()
	l := buildLedger(t).withOtherHousehold()
	outsider := l.as("alex")
	outsider.spaceID = l.str("other_space")
	_, err := call[agentifiv1.ListTagsRequest, agentifiv1.ListTagsResponse](
		outsider, agentifiv1connect.TagServiceListTagsProcedure, &agentifiv1.ListTagsRequest{})
	require.Equal(t, connect.CodeNotFound, err.Code())
	require.Equal(t, int32(http.StatusNotFound), problemIn(t, err).GetStatus())
}

func TestForeignTagNotFound(t *testing.T) {
	t.Parallel()
	l := buildLedger(t).withOtherHousehold()
	for _, id := range []string{l.str("stranger_tag"), "not-an-id"} {
		_, err := call[agentifiv1.GetTagRequest, agentifiv1.GetTagResponse](
			l.alex, agentifiv1connect.TagServiceGetTagProcedure, &agentifiv1.GetTagRequest{TagId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Tag not found", err.Message())
	}
}

func TestUnknownFieldRefused(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	l.alex.rpc(agentifiv1connect.TagServiceCreateTagProcedure, `{"name": "x", "colour": "#fff"}`).
		requireCode(connect.CodeInvalidArgument)
}

func TestNoTokenUnauthenticated(t *testing.T) {
	t.Parallel()
	newClient(t).rpc(agentifiv1connect.TagServiceListTagsProcedure, `{}`).
		requireCode(connect.CodeUnauthenticated).requireStatus(http.StatusUnauthorized)
}

func TestDeleteTag(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	_, err := call[agentifiv1.DeleteTagRequest, agentifiv1.DeleteTagResponse](
		l.alex, agentifiv1connect.TagServiceDeleteTagProcedure, &agentifiv1.DeleteTagRequest{TagId: l.str("tag")})
	require.Nil(t, err)
	_, err = call[agentifiv1.GetTagRequest, agentifiv1.GetTagResponse](
		l.alex, agentifiv1connect.TagServiceGetTagProcedure, &agentifiv1.GetTagRequest{TagId: l.str("tag")})
	require.Equal(t, connect.CodeNotFound, err.Code())
}

func TestTagsViaAssistantDispatch(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	sp, err := auth.ResolveSpace(t.Context(), l.env.DB, l.users["alex"], l.str("space"))
	require.NoError(t, err)
	created, err := l.env.dispatch(t.Context(), sp, http.MethodPost, "/tags", nil, []byte(`{"name":"Travel","color":"#abcdef"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusCreated, created.Status, created.Body)
	listed, err := l.env.dispatch(t.Context(), sp, http.MethodGet, "/tags", nil, nil)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, listed.Status, listed.Body)
	require.Contains(t, listed.Body, `"name":"Travel"`)
	require.Contains(t, listed.Body, `"color":"#abcdef"`)
	viewer, err := auth.ResolveSpace(t.Context(), l.env.DB, l.users["vera"], l.str("space"))
	require.NoError(t, err)
	refused, err := l.env.dispatch(t.Context(), viewer, http.MethodPost, "/tags", nil, []byte(`{"name":"Denied"}`))
	require.NoError(t, err)
	require.Equal(t, http.StatusForbidden, refused.Status, refused.Body)
}

func TestRESTTagPatchColor(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	path := "/tags/" + l.str("tag")
	l.alex.patch(path, map[string]any{"color": "#abcdef"}).requireStatus(http.StatusOK)
	renamed := l.alex.patch(path, map[string]any{"name": "Travel"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "#abcdef", renamed["color"])
	cleared := l.alex.patch(path, map[string]any{"color": nil}).requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["color"])
	require.Equal(t, "Travel", cleared["name"])
	l.alex.del(path).requireStatus(http.StatusNoContent)
	l.alex.get(path).requireStatus(http.StatusNotFound)
}
