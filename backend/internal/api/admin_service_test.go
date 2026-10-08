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
)

// The administration services over their own protocol. admin_test.go,
// backups_test.go and server_test.go still reach them through the REST bridge.

func TestEveryAdministrationProcedureRefusesAnOrdinaryAccount(t *testing.T) {
	c := newClient(t).as(makeUser(t, testPassword))
	for name, proc := range procedures {
		if proc.scope != agentifiv1.Scope_SCOPE_ADMIN {
			continue
		}
		// The refusal comes before the request is read, so an empty one does.
		c.rpc(name, `{}`).requireCode(connect.CodePermissionDenied).requireStatus(http.StatusForbidden)
	}
	newClient(t).rpc(agentifiv1connect.AdminServiceListUsersProcedure, `{}`).
		requireCode(connect.CodeUnauthenticated)
}

func TestAMintedPasswordIsAnsweredOnceAndAChosenOneNever(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	space := makeSpace(t, makeUser(t, testPassword), "Household", "owner", true)

	minted, err := call[agentifiv1.CreateUserRequest, agentifiv1.CreateUserResponse](
		c, agentifiv1connect.AdminServiceCreateUserProcedure, &agentifiv1.CreateUserRequest{
			Email: "minted@example.test", SpaceId: proto.String(space.ID.String()), Role: "viewer",
		})
	require.Nil(t, err)
	require.NotEmpty(t, minted.GetTemporaryPassword())
	require.True(t, minted.GetUser().GetMustChangePassword(), "an operator-known password is owed a change")
	require.Len(t, minted.GetUser().GetMemberships(), 1)

	chosen, err := call[agentifiv1.CreateUserRequest, agentifiv1.CreateUserResponse](
		c, agentifiv1connect.AdminServiceCreateUserProcedure, &agentifiv1.CreateUserRequest{
			Email: "chosen@example.test", Password: testPassword, MustChangePassword: proto.Bool(false),
			SpaceName: "Their own",
		})
	require.Nil(t, err)
	require.Nil(t, chosen.TemporaryPassword)
	require.False(t, chosen.GetUser().GetMustChangePassword())

	_, err = call[agentifiv1.CreateUserRequest, agentifiv1.CreateUserResponse](
		c, agentifiv1connect.AdminServiceCreateUserProcedure, &agentifiv1.CreateUserRequest{
			Email: "nowhere@example.test", SpaceId: proto.String("not-an-id"), Role: "viewer",
		})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "space_id"}, problemIn(t, err).GetFields()[0].GetLoc())
}

func TestAnAccountUpdateIsSetLeftAloneOrIgnoredWhenCleared(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	target := makeUser(t, testPassword)
	update := func(req *agentifiv1.UpdateUserRequest) *agentifiv1.AdminUser {
		t.Helper()
		req.UserId = target.ID.String()
		res, err := call[agentifiv1.UpdateUserRequest, agentifiv1.UpdateUserResponse](
			c, agentifiv1connect.AdminServiceUpdateUserProcedure, req)
		require.Nil(t, err)
		return res.GetUser()
	}

	// Set.
	user := update(&agentifiv1.UpdateUserRequest{FullName: proto.String("Sam Example"), IsActive: proto.Bool(false)})
	require.Equal(t, "Sam Example", user.GetFullName())
	require.False(t, user.GetIsActive())

	// Absent: what the mask does not name is left alone.
	user = update(&agentifiv1.UpdateUserRequest{
		IsActive: proto.Bool(true), UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"is_active"}},
	})
	require.True(t, user.GetIsActive())
	require.Equal(t, "Sam Example", user.GetFullName())

	// Cleared: none of these has a null to go back to, so clearing one
	// changes nothing, as a null did over REST.
	user = update(&agentifiv1.UpdateUserRequest{
		UpdateMask: &fieldmaskpb.FieldMask{Paths: []string{"full_name", "is_active", "is_superuser"}},
	})
	require.Equal(t, "Sam Example", user.GetFullName())
	require.True(t, user.GetIsActive())
	require.False(t, user.GetIsSuperuser())
}

func TestAnAdministratorCannotStandThemselfDown(t *testing.T) {
	admin := makeAdmin(t)
	_, err := call[agentifiv1.UpdateUserRequest, agentifiv1.UpdateUserResponse](
		newClient(t).as(admin), agentifiv1connect.AdminServiceUpdateUserProcedure,
		&agentifiv1.UpdateUserRequest{UserId: admin.ID.String(), IsSuperuser: proto.Bool(false)})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
}

func TestAnUnknownAccountIsNotFound(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	for _, id := range []string{"00000000-0000-0000-0000-000000000000", "not-an-id"} {
		_, err := call[agentifiv1.SetUserPasswordRequest, agentifiv1.SetUserPasswordResponse](
			c, agentifiv1connect.AdminServiceSetUserPasswordProcedure,
			&agentifiv1.SetUserPasswordRequest{UserId: id})
		require.Equal(t, connect.CodeNotFound, err.Code())
		require.Equal(t, "User not found", err.Message())
	}
}

func TestABackupRunWithBackupsOffIsRefused(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearBackupSettings(t, c.env)

	backups, err := call[agentifiv1.GetBackupsRequest, agentifiv1.GetBackupsResponse](
		c, agentifiv1connect.AdminBackupServiceGetBackupsProcedure, &agentifiv1.GetBackupsRequest{})
	require.Nil(t, err)
	require.False(t, backups.GetBackups().GetEnabled())
	require.Nil(t, backups.GetBackups().GetNextRun())

	_, err = call[agentifiv1.RunBackupRequest, agentifiv1.RunBackupResponse](
		c, agentifiv1connect.AdminBackupServiceRunBackupProcedure, &agentifiv1.RunBackupRequest{})
	require.Equal(t, connect.CodeFailedPrecondition, err.Code())
}

func TestAServerSettingOutsideTheListIsRefused(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	clearSavedSettings(t, c)
	_, err := call[agentifiv1.UpdateServerSettingsRequest, agentifiv1.UpdateServerSettingsResponse](
		c, agentifiv1connect.AdminServerServiceUpdateServerSettingsProcedure,
		&agentifiv1.UpdateServerSettingsRequest{Values: map[string]string{"SECRET_KEY": "x"}})
	require.Equal(t, connect.CodeInvalidArgument, err.Code())
	require.Equal(t, []string{"body", "values", "SECRET_KEY"}, problemIn(t, err).GetFields()[0].GetLoc())

	info, err := call[agentifiv1.GetServerInfoRequest, agentifiv1.GetServerInfoResponse](
		c, agentifiv1connect.AdminServerServiceGetServerInfoProcedure, &agentifiv1.GetServerInfoRequest{})
	require.Nil(t, err)
	require.Positive(t, info.GetSchemaVersion())
}
