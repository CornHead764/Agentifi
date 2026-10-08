package api

import (
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// SpaceService over its own protocol. spaces_test.go and spacedelete_test.go
// still reach it through the REST bridge.

func TestASpaceTheCallerIsNotInIsNotFoundByItsId(t *testing.T) {
	user := makeUser(t, testPassword)
	stranger := makeUser(t, testPassword)
	makeSpace(t, user, "Mine", store.RoleOwner, true)
	theirs := makeSpace(t, stranger, "Theirs", store.RoleOwner, true)
	pending := makeSpace(t, stranger, "Pending", store.RoleOwner, true)
	makeMembership(t, pending, user, store.RoleMember, false)
	c := newClient(t).as(user)

	for _, id := range []string{theirs.ID.String(), pending.ID.String(), "not-an-id"} {
		_, err := call[agentifiv1.ListMembersRequest, agentifiv1.ListMembersResponse](
			c, agentifiv1connect.SpaceServiceListMembersProcedure, &agentifiv1.ListMembersRequest{SpaceId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "Space not found", err.Message())
	}
}

func TestAViewerMayArrangeTheirDashboardButNotTheSpace(t *testing.T) {
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Household", store.RoleViewer, true)
	c := newClient(t).as(user).inSpace(space.ID)

	_, err := call[agentifiv1.UpdateSpacePreferencesRequest, agentifiv1.UpdateSpacePreferencesResponse](
		c, agentifiv1connect.SpaceServiceUpdateSpacePreferencesProcedure,
		&agentifiv1.UpdateSpacePreferencesRequest{DefaultDateRange: proto.String("3M")})
	require.Equal(t, connect.CodePermissionDenied, err.Code())
	require.Equal(t, int32(http.StatusForbidden), problemIn(t, err).GetStatus())

	layout, listErr := structpb.NewValue([]any{map[string]any{"id": "net_worth", "on": true}})
	require.NoError(t, listErr)
	saved, err := call[agentifiv1.SetDashboardLayoutRequest, agentifiv1.SetDashboardLayoutResponse](
		c, agentifiv1connect.SpaceServiceSetDashboardLayoutProcedure,
		&agentifiv1.SetDashboardLayoutRequest{Layout: layout})
	require.Nil(t, err)
	require.Len(t, saved.GetLayout().GetListValue().GetValues(), 1)

	read, err := call[agentifiv1.GetDashboardLayoutRequest, agentifiv1.GetDashboardLayoutResponse](
		c, agentifiv1connect.SpaceServiceGetDashboardLayoutProcedure, &agentifiv1.GetDashboardLayoutRequest{})
	require.Nil(t, err)
	require.True(t, proto.Equal(layout, read.GetLayout()))
}

func TestADashboardLayoutIsAListOrNothing(t *testing.T) {
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)
	c := newClient(t).as(user).inSpace(space.ID)

	refused := c.rpc(agentifiv1connect.SpaceServiceSetDashboardLayoutProcedure, `{"layout": "net_worth"}`).
		requireCode(connect.CodeInvalidArgument).requireStatus(http.StatusUnprocessableEntity)
	detail := refused.json()["detail"].([]any)[0].(map[string]any)
	require.Equal(t, []any{"body", "layout"}, detail["loc"])

	c.rpc(agentifiv1connect.SpaceServiceSetDashboardLayoutProcedure, `{"layout": [{"id": "budget"}]}`).
		requireStatus(http.StatusOK)
	// JSON null is a Value of its own on this wire, and clears as unset does.
	cleared := c.rpc(agentifiv1connect.SpaceServiceSetDashboardLayoutProcedure, `{"layout": null}`).
		requireStatus(http.StatusOK).json()
	require.Nil(t, cleared["layout"])
	read := c.rpc(agentifiv1connect.SpaceServiceGetDashboardLayoutProcedure, `{}`).
		requireStatus(http.StatusOK).json()
	require.Nil(t, read["layout"])
}

func TestPreferencesAreSetLeftAloneOrClearedByTheMask(t *testing.T) {
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)
	c := newClient(t).as(user).inSpace(space.ID)
	update := func(req *agentifiv1.UpdateSpacePreferencesRequest) *agentifiv1.Space {
		t.Helper()
		res, err := call[agentifiv1.UpdateSpacePreferencesRequest, agentifiv1.UpdateSpacePreferencesResponse](
			c, agentifiv1connect.SpaceServiceUpdateSpacePreferencesProcedure, req)
		require.Nil(t, err)
		return res.GetSpace()
	}

	// Set, an empty list being an answer of its own.
	got := update(&agentifiv1.UpdateSpacePreferencesRequest{
		DefaultDateRange:    proto.String("YTD"),
		SidebarAccountTypes: &structpb.ListValue{},
	})
	require.Equal(t, "YTD", got.GetDefaultDateRange())
	require.NotNil(t, got.GetSidebarAccountTypes())
	require.Empty(t, got.GetSidebarAccountTypes().GetValues())

	// Absent: what the mask does not name is left as it was.
	types, listErr := structpb.NewList([]any{"checking"})
	require.NoError(t, listErr)
	got = update(&agentifiv1.UpdateSpacePreferencesRequest{
		SidebarAccountTypes: types,
		UpdateMask:          &fieldmaskpb.FieldMask{Paths: []string{"sidebar_account_types"}},
	})
	require.Equal(t, "YTD", got.GetDefaultDateRange())
	require.Equal(t, "checking", got.GetSidebarAccountTypes().GetValues()[0].GetStringValue())

	// Cleared: named and unset is back to the default, which for the sidebar
	// is "list them all" rather than an empty list.
	current, err := call[agentifiv1.GetCurrentSpaceRequest, agentifiv1.GetCurrentSpaceResponse](
		c, agentifiv1connect.SpaceServiceGetCurrentSpaceProcedure, &agentifiv1.GetCurrentSpaceRequest{})
	require.Nil(t, err)
	require.Equal(t, "YTD", current.GetSpace().GetDefaultDateRange(), "the current space reads its preferences")

	got = update(&agentifiv1.UpdateSpacePreferencesRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"default_date_range", "sidebar_account_types"}},
	})
	require.Empty(t, got.GetDefaultDateRange())
	require.Nil(t, got.GetSidebarAccountTypes())

	// A list holding something other than strings is not a list of types.
	notTypes, listErr := structpb.NewList([]any{1.0})
	require.NoError(t, listErr)
	_, refused := call[agentifiv1.UpdateSpacePreferencesRequest, agentifiv1.UpdateSpacePreferencesResponse](
		c, agentifiv1connect.SpaceServiceUpdateSpacePreferencesProcedure,
		&agentifiv1.UpdateSpacePreferencesRequest{SidebarAccountTypes: notTypes})
	require.Equal(t, connect.CodeInvalidArgument, refused.Code())
}

func TestARenameCannotClearTheName(t *testing.T) {
	user := makeUser(t, testPassword)
	space := makeSpace(t, user, "Household", store.RoleOwner, true)
	c := newClient(t).as(user)

	renamed, err := call[agentifiv1.UpdateSpaceRequest, agentifiv1.UpdateSpaceResponse](
		c, agentifiv1connect.SpaceServiceUpdateSpaceProcedure,
		&agentifiv1.UpdateSpaceRequest{SpaceId: space.ID.String(), Name: proto.String("  Home  ")})
	require.Nil(t, err)
	require.Equal(t, "Home", renamed.GetSpace().GetName())

	_, err = call[agentifiv1.UpdateSpaceRequest, agentifiv1.UpdateSpaceResponse](
		c, agentifiv1connect.SpaceServiceUpdateSpaceProcedure, &agentifiv1.UpdateSpaceRequest{
			SpaceId: space.ID.String(), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}},
		})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
}

func TestAMemberWhoIsNotAnOwnerCannotInvite(t *testing.T) {
	owner := makeUser(t, testPassword)
	member := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	makeMembership(t, space, member, store.RoleMember, true)

	_, err := call[agentifiv1.InviteMemberRequest, agentifiv1.InviteMemberResponse](
		newClient(t).as(member), agentifiv1connect.SpaceServiceInviteMemberProcedure,
		&agentifiv1.InviteMemberRequest{SpaceId: space.ID.String(), Email: invitee.Email, Role: "viewer"})
	require.Equal(t, connect.CodePermissionDenied, err.Code())

	invited, err := call[agentifiv1.InviteMemberRequest, agentifiv1.InviteMemberResponse](
		newClient(t).as(owner), agentifiv1connect.SpaceServiceInviteMemberProcedure,
		&agentifiv1.InviteMemberRequest{SpaceId: space.ID.String(), Email: invitee.Email, Role: "viewer"})
	require.Nil(t, err)
	require.NotNil(t, invited.GetMember().GetInvitedAt())
	require.Nil(t, invited.GetMember().GetAcceptedAt(), "an invitation is pending, not membership")
}

// makeMembership puts an existing account into an existing space.
func makeMembership(t *testing.T, space store.Space, user store.User, role store.Role, accepted bool) {
	t.Helper()
	membership := &store.Membership{UserID: user.ID, Role: role}
	invited := time.Now().UTC()
	membership.InvitedAt = &invited
	if accepted {
		membership.AcceptedAt = &invited
	}
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, membership))
}
