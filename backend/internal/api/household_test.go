package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/fieldmaskpb"
	"google.golang.org/protobuf/types/known/structpb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// HouseholdService over its own protocol. The REST URLs it used to answer are
// the bridge's, and spaces_test.go, setupguide_test.go and spacedelete_test.go
// still reach them.
//
// Expected values come from the rules in spaceroutes.go's and setupguide.go's
// comments, and from the bodies the REST routes wrote before the bridge.

func TestListSpacesIsTheCallersJoinedSpacesWithTheirRole(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	user := makeUser(t, testPassword)
	owned := makeSpace(t, user, "Owned", store.RoleOwner, true)
	viewed := makeSpace(t, user, "Viewed", store.RoleViewer, true)
	makeSpace(t, user, "Pending", store.RoleMember, false)
	makeSpace(t, makeUser(t, testPassword), "Theirs", store.RoleOwner, true)

	res, err := call[agentifiv1.ListSpacesRequest, agentifiv1.ListSpacesResponse](
		c.as(user), agentifiv1connect.HouseholdServiceListSpacesProcedure, &agentifiv1.ListSpacesRequest{})
	require.Nil(t, err)
	byID := map[string]*agentifiv1.Space{}
	for _, space := range res.GetSpaces() {
		byID[space.GetId()] = space
	}
	require.Len(t, byID, 2, "joined spaces only: not a pending invitation, not somebody else's")
	require.Equal(t, agentifiv1.Role_ROLE_OWNER, byID[owned.ID.String()].GetRole())
	require.True(t, byID[owned.ID.String()].GetIsOwner())
	require.Equal(t, agentifiv1.Role_ROLE_VIEWER, byID[viewed.ID.String()].GetRole())
	require.False(t, byID[viewed.ID.String()].GetCanWrite())
	require.NotNil(t, byID[viewed.ID.String()].GetJoinedAt())
	require.Nil(t, byID[owned.ID.String()].GetSidebarAccountTypes(), "never chosen lists every type")
}

func TestAnotherSpacesMembersAreNotFound(t *testing.T) {
	t.Parallel()
	// The same refusal as an id nobody has used, or one that is not an id: a
	// distinguishable one confirms the space exists.
	c := newClient(t)
	user := makeUser(t, testPassword)
	makeSpace(t, user, "Mine", store.RoleOwner, true)
	theirs := makeSpace(t, makeUser(t, testPassword), "Theirs", store.RoleOwner, true)
	c.as(user)
	for _, id := range []string{theirs.ID.String(), uuid.NewString(), "not-an-id"} {
		_, err := call[agentifiv1.ListMembersRequest, agentifiv1.ListMembersResponse](
			c, agentifiv1connect.HouseholdServiceListMembersProcedure, &agentifiv1.ListMembersRequest{SpaceId: id})
		require.NotNil(t, err, id)
		require.Equal(t, connect.CodeNotFound, err.Code(), id)
		require.Equal(t, "Space not found", err.Message(), id)
	}
}

func TestNoAccountTypesIsNotEveryAccountType(t *testing.T) {
	t.Parallel()
	// store.Space.SidebarAccountTypes: nil is "all" and empty non-nil "none";
	// Space.sidebar_account_types keeps them apart as unset and an empty set.
	owner := store.Membership{Role: store.RoleOwner}
	require.Nil(t, spaceProto(store.Space{}, owner).GetSidebarAccountTypes())
	none := spaceProto(store.Space{SidebarAccountTypes: []string{}}, owner).GetSidebarAccountTypes()
	require.NotNil(t, none)
	require.Empty(t, none.GetIds())
	require.Equal(t, []string{"checking"},
		spaceProto(store.Space{SidebarAccountTypes: []string{"checking"}}, owner).GetSidebarAccountTypes().GetIds())

	// Cleared through the mask is back to every type.
	l := buildLedger(t)
	res, err := call[agentifiv1.UpdatePreferencesRequest, agentifiv1.UpdatePreferencesResponse](
		l.alex, agentifiv1connect.HouseholdServiceUpdatePreferencesProcedure, &agentifiv1.UpdatePreferencesRequest{
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"sidebar_account_types"}},
		})
	require.Nil(t, err)
	require.Nil(t, res.GetSpace().GetSidebarAccountTypes())
}

func TestAPreferenceLeftOutOfTheMaskIsKept(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	set := func(req *agentifiv1.UpdatePreferencesRequest) *agentifiv1.Space {
		t.Helper()
		res, err := call[agentifiv1.UpdatePreferencesRequest, agentifiv1.UpdatePreferencesResponse](
			l.alex, agentifiv1connect.HouseholdServiceUpdatePreferencesProcedure, req)
		require.Nil(t, err)
		return res.GetSpace()
	}
	require.Equal(t, "3M", set(&agentifiv1.UpdatePreferencesRequest{DefaultDateRange: proto.String("3M")}).GetDefaultDateRange())
	// primary_currency cannot be cleared: named and unset changes nothing.
	kept := set(&agentifiv1.UpdatePreferencesRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"primary_currency"}},
	})
	require.Equal(t, "3M", kept.GetDefaultDateRange())
	require.Equal(t, "USD", kept.GetPrimaryCurrency())
	cleared := set(&agentifiv1.UpdatePreferencesRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"default_date_range"}},
	})
	require.Empty(t, cleared.GetDefaultDateRange(), "a cleared preset is the client's default")

	_, err := call[agentifiv1.UpdatePreferencesRequest, agentifiv1.UpdatePreferencesResponse](
		l.alex, agentifiv1connect.HouseholdServiceUpdatePreferencesProcedure,
		&agentifiv1.UpdatePreferencesRequest{DefaultDateRange: proto.String("2W")})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "default_date_range"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestOnlyAnOwnerRenamesASpaceAndANameIsNotCleared(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	owner := makeUser(t, testPassword)
	member := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	membership := &store.Membership{UserID: member.ID, Role: store.RoleMember}
	now := time.Now().UTC()
	membership.InvitedAt, membership.AcceptedAt = &now, &now
	require.NoError(t, db(t).CreateMembership(t.Context(), space.ID, membership))

	rename := func(as store.User, req *agentifiv1.RenameSpaceRequest) (*agentifiv1.RenameSpaceResponse, *connect.Error) {
		req.SpaceId = space.ID.String()
		return call[agentifiv1.RenameSpaceRequest, agentifiv1.RenameSpaceResponse](
			c.as(as), agentifiv1connect.HouseholdServiceRenameSpaceProcedure, req)
	}
	_, err := rename(member, &agentifiv1.RenameSpaceRequest{Name: proto.String("Mine now")})
	require.Equal(t, connect.CodePermissionDenied, err.Code())

	renamed, err := rename(owner, &agentifiv1.RenameSpaceRequest{Name: proto.String("  Home  ")})
	require.Nil(t, err)
	require.Equal(t, "Home", renamed.GetSpace().GetName())

	_, err = rename(owner, &agentifiv1.RenameSpaceRequest{UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"name"}}})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code(), "the name cannot be cleared, a 409 on REST")
}

func TestAnInvitationNeedsARole(t *testing.T) {
	t.Parallel()
	c := newClient(t)
	owner := makeUser(t, testPassword)
	invitee := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Household", store.RoleOwner, true)
	c.as(owner)

	_, err := call[agentifiv1.InviteMemberRequest, agentifiv1.InviteMemberResponse](
		c, agentifiv1connect.HouseholdServiceInviteMemberProcedure,
		&agentifiv1.InviteMemberRequest{SpaceId: space.ID.String(), Email: invitee.Email})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	field := problemIn(t, err).GetFields()[0]
	require.Equal(t, []string{"body", "role"}, field.GetLoc())
	require.Equal(t, "missing", field.GetType())

	// A role REST never had is refused under the same field.
	refused := c.post("/spaces/"+space.ID.String()+"/members", map[string]any{"email": invitee.Email, "role": "boss"}).
		requireStatus(http.StatusUnprocessableEntity).json()
	require.Equal(t, []any{"body", "role"}, refused["detail"].([]any)[0].(map[string]any)["loc"])

	invited, err := call[agentifiv1.InviteMemberRequest, agentifiv1.InviteMemberResponse](
		c, agentifiv1connect.HouseholdServiceInviteMemberProcedure, &agentifiv1.InviteMemberRequest{
			SpaceId: space.ID.String(), Email: invitee.Email, Role: agentifiv1.Role_ROLE_VIEWER,
		})
	require.Nil(t, err)
	require.Equal(t, agentifiv1.Role_ROLE_VIEWER, invited.GetMember().GetRole())
	require.NotNil(t, invited.GetMember().GetInvitedAt())
	require.Nil(t, invited.GetMember().GetAcceptedAt(), "an invitation grants nothing until taken")
}

func TestAViewerSavesTheirOwnDashboardAndOnlyAListIsALayout(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	vera := l.as("vera")
	save := func(layout *structpb.Value) (*agentifiv1.SaveDashboardLayoutResponse, *connect.Error) {
		return call[agentifiv1.SaveDashboardLayoutRequest, agentifiv1.SaveDashboardLayoutResponse](
			vera, agentifiv1connect.HouseholdServiceSaveDashboardLayoutProcedure,
			&agentifiv1.SaveDashboardLayoutRequest{Layout: layout})
	}
	list, err := structpb.NewValue([]any{map[string]any{"id": "net_worth", "on": true}})
	require.NoError(t, err)
	saved, refused := save(list)
	require.Nil(t, refused)
	require.True(t, proto.Equal(list, saved.GetLayout()))

	read, refused := call[agentifiv1.GetDashboardLayoutRequest, agentifiv1.GetDashboardLayoutResponse](
		vera, agentifiv1connect.HouseholdServiceGetDashboardLayoutProcedure, &agentifiv1.GetDashboardLayoutRequest{})
	require.Nil(t, refused)
	require.True(t, proto.Equal(list, read.GetLayout()))

	_, refused = save(structpb.NewStringValue("net_worth"))
	require.Equal(t, connect.CodeInvalidArgument, refused.Code())
	require.Equal(t, []string{"body", "layout"}, problemIn(t, refused).GetFields()[0].GetLoc())

	// protojson reads the null the server wrote back as a null Value.
	cleared, refused := save(structpb.NewNullValue())
	require.Nil(t, refused)
	require.IsType(t, &structpb.Value_NullValue{}, cleared.GetLayout().GetKind(), "null is back to the built-in order")
}

func TestSkippedStepsAreTheWholeListAndOnlySkippableOnes(t *testing.T) {
	t.Parallel()
	c, _ := guideClient(t)
	update := func(req *agentifiv1.UpdateSetupGuideRequest) (*agentifiv1.SetupGuide, *connect.Error) {
		res, err := call[agentifiv1.UpdateSetupGuideRequest, agentifiv1.UpdateSetupGuideResponse](
			c, agentifiv1connect.HouseholdServiceUpdateSetupGuideProcedure, req)
		return res.GetGuide(), err
	}
	guide, err := update(&agentifiv1.UpdateSetupGuideRequest{Skipped: &agentifiv1.IdSet{Ids: []string{"bills", "bills"}}})
	require.Nil(t, err)
	require.Equal(t, []string{"bills"}, guide.GetSkipped())
	require.False(t, guide.GetDismissed())

	guide, err = update(&agentifiv1.UpdateSetupGuideRequest{Dismissed: proto.Bool(true)})
	require.Nil(t, err)
	require.True(t, guide.GetDismissed())
	require.Equal(t, []string{"bills"}, guide.GetSkipped(), "left out of the request, left alone")

	_, err = update(&agentifiv1.UpdateSetupGuideRequest{Skipped: &agentifiv1.IdSet{Ids: []string{"match"}}})
	require.Equal(t, connect.CodeInvalidArgument, err.Code(), "matching follows connecting and is not skipped alone")
}

func TestTheSpaceKeepsItsRESTShape(t *testing.T) {
	t.Parallel()
	// The keys, in order, and the kinds of value SpaceResponse wrote.
	l := buildLedger(t)
	body := l.alex.get("/spaces/current").requireStatus(http.StatusOK).Body.Bytes()
	require.Equal(t, []string{
		"id", "name", "primary_currency", "timezone", "default_date_range", "sidebar_account_types",
		"role", "can_write", "is_owner", "joined_at",
	}, objectKeys(t, body))
	var space map[string]any
	require.NoError(t, json.Unmarshal(body, &space))
	require.Equal(t, "owner", space["role"])
	require.Nil(t, space["sidebar_account_types"])
	joined, ok := space["joined_at"].(string)
	require.True(t, ok)
	_, err := time.Parse(time.RFC3339Nano, joined)
	require.NoError(t, err)
}

// objectKeys is a JSON object's keys in the order they were written.
func objectKeys(t *testing.T, body []byte) []string {
	t.Helper()
	decoder := json.NewDecoder(bytes.NewReader(body))
	_, err := decoder.Token()
	require.NoError(t, err)
	var keys []string
	for decoder.More() {
		key, err := decoder.Token()
		require.NoError(t, err)
		keys = append(keys, key.(string))
		var skip json.RawMessage
		require.NoError(t, decoder.Decode(&skip))
	}
	return keys
}
