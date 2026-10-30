package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/fieldmaskpb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// AdminService over its own protocol. The REST URLs it used to answer are
// the bridge's, and admin_test.go, backups_test.go, server_test.go,
// update_test.go and oidcgroups_test.go still reach them.
//
// Expected values come from the rules in admin.go's and backups.go's comments,
// and from the bodies the REST routes wrote before the bridge.

func TestEveryAdminProcedureRefusesAnOrdinaryAccount(t *testing.T) {
	t.Parallel()
	// Refused before the handler runs, so an empty request is enough, and a
	// method added later is covered without being named.
	c := newClient(t).as(makeUser(t, testPassword))
	methods := agentifiv1.File_agentifi_v1_admin_proto.Services().ByName("AdminService").Methods()
	require.Positive(t, methods.Len())
	for i := range methods.Len() {
		path := "/agentifi.v1.AdminService/" + string(methods.Get(i).Name())
		c.rpc(path, map[string]any{}).requireCode(connect.CodePermissionDenied)
	}
}

func TestAMintedPasswordComesBackOnceAndAChosenOneNever(t *testing.T) {
	t.Parallel()
	c := newClient(t).as(makeAdmin(t))

	minted, err := call[agentifiv1.CreateUserRequest, agentifiv1.CreateUserResponse](
		c, agentifiv1connect.AdminServiceCreateUserProcedure, &agentifiv1.CreateUserRequest{
			Email: "minted-" + uuid.NewString() + "@example.test", SpaceName: "Minted House",
		})
	require.Nil(t, err)
	require.NotEmpty(t, minted.GetTemporaryPassword())
	require.True(t, minted.GetUser().GetMustChangePassword(), "an operator-chosen password is owed a change")
	require.Len(t, minted.GetUser().GetMemberships(), 1)
	require.Equal(t, agentifiv1.Role_ROLE_OWNER, minted.GetUser().GetMemberships()[0].GetRole())

	chosen, err := call[agentifiv1.CreateUserRequest, agentifiv1.CreateUserResponse](
		c, agentifiv1connect.AdminServiceCreateUserProcedure, &agentifiv1.CreateUserRequest{
			Email: "chosen-" + uuid.NewString() + "@example.test", SpaceName: "Chosen House",
			Password: testPassword, MustChangePassword: proto.Bool(false),
		})
	require.Nil(t, err)
	require.Nil(t, chosen.TemporaryPassword)
	require.False(t, chosen.GetUser().GetMustChangePassword())

	reset, err := call[agentifiv1.SetUserPasswordRequest, agentifiv1.SetUserPasswordResponse](
		c, agentifiv1connect.AdminServiceSetUserPasswordProcedure,
		&agentifiv1.SetUserPasswordRequest{UserId: chosen.GetUser().GetId()})
	require.Nil(t, err)
	require.NotEmpty(t, reset.GetTemporaryPassword())
}

func TestAnAccountJoiningASpaceNeedsAWellFormedSpace(t *testing.T) {
	t.Parallel()
	c := newClient(t).as(makeAdmin(t))
	target := makeUser(t, testPassword)

	_, err := call[agentifiv1.AddMembershipRequest, agentifiv1.AddMembershipResponse](
		c, agentifiv1connect.AdminServiceAddMembershipProcedure, &agentifiv1.AddMembershipRequest{
			UserId: target.ID.String(), SpaceId: "not-an-id", Role: agentifiv1.Role_ROLE_MEMBER,
		})
	require.NotNil(t, err)
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "space_id"}, problemIn(t, err).GetFields()[0].GetLoc())

	_, err = call[agentifiv1.AddMembershipRequest, agentifiv1.AddMembershipResponse](
		c, agentifiv1connect.AdminServiceAddMembershipProcedure, &agentifiv1.AddMembershipRequest{
			UserId: target.ID.String(), SpaceId: uuid.NewString(),
		})
	require.NotNil(t, err)
	require.Equal(t, "role", problemIn(t, err).GetFields()[0].GetLoc()[1], "a role is required")

	space := makeSpace(t, makeUser(t, testPassword), "Joined", store.RoleOwner, true)
	added, err := call[agentifiv1.AddMembershipRequest, agentifiv1.AddMembershipResponse](
		c, agentifiv1connect.AdminServiceAddMembershipProcedure, &agentifiv1.AddMembershipRequest{
			UserId: target.ID.String(), SpaceId: space.ID.String(), Role: agentifiv1.Role_ROLE_VIEWER,
		})
	require.Nil(t, err)
	require.True(t, added.GetMembership().GetAccepted(), "accepted, not invited")
	require.Equal(t, "Joined", added.GetMembership().GetSpaceName())

	_, err = call[agentifiv1.RemoveMembershipRequest, agentifiv1.RemoveMembershipResponse](
		c, agentifiv1connect.AdminServiceRemoveMembershipProcedure, &agentifiv1.RemoveMembershipRequest{
			UserId: target.ID.String(), MembershipId: added.GetMembership().GetId(),
		})
	require.Nil(t, err)
}

func TestAnAdministratorUpdateChangesOnlyWhatItNames(t *testing.T) {
	t.Parallel()
	admin := makeAdmin(t)
	c := newClient(t).as(admin)
	target := makeUser(t, testPassword)

	// is_active is named and unset, which is the null a REST body sent: left
	// alone rather than read as false.
	updated, err := call[agentifiv1.UpdateUserRequest, agentifiv1.UpdateUserResponse](
		c, agentifiv1connect.AdminServiceUpdateUserProcedure, &agentifiv1.UpdateUserRequest{
			UserId: target.ID.String(), FullName: proto.String("Renamed Person"),
			UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"full_name", "is_active"}},
		})
	require.Nil(t, err)
	require.Equal(t, "Renamed Person", updated.GetUser().GetFullName())
	require.True(t, updated.GetUser().GetIsActive())

	_, err = call[agentifiv1.UpdateUserRequest, agentifiv1.UpdateUserResponse](
		c, agentifiv1connect.AdminServiceUpdateUserProcedure, &agentifiv1.UpdateUserRequest{
			UserId: admin.ID.String(), IsSuperuser: proto.Bool(false),
		})
	require.NotNil(t, err)
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
	require.Contains(t, err.Message(), "your own administrator rights")
}

func TestEveryProviderSettingSaysWhereItCameFrom(t *testing.T) {
	t.Parallel()
	// effectiveOIDC's sources, by the name OIDCSettings gives each value.
	sources := map[string]string{}
	for _, field := range oidcSettingSources {
		sources[field] = "environment"
	}
	sources["client_id"] = "database"
	settings := oidcSettingsProto(newClient(t).env, auth.OIDCSettings{}, sources)

	got := settings.GetSources().ProtoReflect()
	fields := got.Descriptor().Fields()
	require.Equal(t, len(oidcSettingSources), fields.Len(), "a source per stored setting")
	for i := range fields.Len() {
		field := fields.Get(i)
		want := agentifiv1.SettingSource_SETTING_SOURCE_ENVIRONMENT
		if field.Name() == "client_id" {
			want = agentifiv1.SettingSource_SETTING_SOURCE_DATABASE
		}
		require.Equal(t, protoreflect.EnumNumber(want), got.Get(field).Enum(), "%s", field.Name())
	}
	require.Equal(t, auth.DefaultGroupsClaim, settings.GetGroupsClaim())
}

func TestTheUpdateCheckAsksAgainOnlyWhenTold(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 10, 8, 12, 0, 0, 0, time.UTC)
	releases := &fakeReleases{release: provider.Release{Version: "v0.3.0", URL: "https://example.test/v0.3.0"}}
	c := updateClient(t, true, releases, &now)

	for _, refresh := range []bool{false, false, true} {
		res, err := call[agentifiv1.GetUpdateRequest, agentifiv1.GetUpdateResponse](
			c, agentifiv1connect.AdminServiceGetUpdateProcedure, &agentifiv1.GetUpdateRequest{Refresh: refresh})
		require.Nil(t, err)
		require.True(t, res.GetUpdateAvailable())
		require.True(t, res.GetCheckedAt().AsTime().Equal(now))
	}
	require.Equal(t, 2, releases.calls, "the second read is the kept answer")
}

func TestTheAdminUserKeepsItsRESTShape(t *testing.T) {
	t.Parallel()
	// The keys, in order, and the kinds of value AdminUserResponse and
	// AdminUserCreated wrote.
	c := newClient(t).as(makeAdmin(t))
	res := c.post("/admin/users", map[string]any{
		"email": "shape-" + uuid.NewString() + "@example.test", "password": testPassword, "space_name": "Shape House",
	}).requireStatus(http.StatusCreated)
	require.Equal(t, []string{"user", "temporary_password"}, objectKeys(t, res.Body.Bytes()))

	var created struct {
		User              json.RawMessage `json:"user"`
		TemporaryPassword *string         `json:"temporary_password"`
	}
	require.NoError(t, json.Unmarshal(res.Body.Bytes(), &created))
	require.Nil(t, created.TemporaryPassword, "a chosen password is not echoed")
	require.Equal(t, []string{
		"id", "email", "full_name", "is_active", "is_superuser", "is_verified", "must_change_password",
		"created_at", "last_login_at", "has_password", "has_totp", "has_oidc", "oidc_issuer",
		"passkey_count", "memberships",
	}, objectKeys(t, created.User))

	var user map[string]any
	require.NoError(t, json.Unmarshal(created.User, &user))
	require.Nil(t, user["last_login_at"])
	require.Equal(t, float64(0), user["passkey_count"])
	membership := user["memberships"].([]any)[0].(map[string]any)
	require.Equal(t, "owner", membership["role"])
	_, err := time.Parse(time.RFC3339Nano, user["created_at"].(string))
	require.NoError(t, err)
}

func TestTheSettingSourcesKeepTheirRESTShape(t *testing.T) {
	t.Parallel()
	// The sources were a map of "environment" or "database" by field; the
	// message writes the same object with every key.
	c := newClient(t).as(makeAdmin(t))
	oidc := c.get("/admin/oidc").requireStatus(http.StatusOK).json()
	sources := oidc["sources"].(map[string]any)
	require.Len(t, sources, len(oidcSettingSources))
	for field, source := range sources {
		require.Contains(t, []any{"environment", "database"}, source, field)
	}

	backups := c.get("/admin/backups").requireStatus(http.StatusOK).json()
	require.Len(t, backups["sources"].(map[string]any), 3)
	require.Contains(t, []any{"environment", "database"}, backups["sources"].(map[string]any)["keep_days"])
}
