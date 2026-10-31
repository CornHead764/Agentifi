package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// AdminService: administering the server from the app rather than from the
// box. Its methods are the only ones that resolve no space, have a caller, and
// answer about every household at once, so the service is SCOPE_ADMIN and
// every method ACCESS_SUPERUSER: the check is in the access interceptor, not
// the handlers. The service is DISPATCH_DENIED, so the assistant cannot reach
// it. The users, spaces and single sign-on are here; backups.go, server.go and
// update.go hold the rest.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAdminServiceHandler(adminService{env}, opts...)
	})
}

type adminService struct{ env *Env }

// errNotAdministrator is the refusal every administration method gives a
// caller who is not a superuser. Raised before a handler runs, by the access
// interceptor and by serve.
var errNotAdministrator = errors.New("api: this account does not administer this server")

// --- Users --------------------------------------------------------------------

func (s adminService) ListUsers(ctx context.Context, _ *agentifiv1.ListUsersRequest) (*agentifiv1.ListUsersResponse, error) {
	users, err := s.env.DB.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	memberships, err := adminMemberships(s.env, ctx)
	if err != nil {
		return nil, err
	}
	names, err := adminSpaceNames(s.env, ctx)
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.ListUsersResponse{Users: make([]*agentifiv1.AdminUser, 0, len(users))}
	for _, user := range users {
		one, err := adminUserProto(s.env, ctx, user, memberships[user.ID], names)
		if err != nil {
			return nil, err
		}
		out.Users = append(out.Users, one)
	}
	return out, nil
}

func (s adminService) CreateUser(ctx context.Context, req *agentifiv1.CreateUserRequest) (*agentifiv1.CreateUserResponse, error) {
	if strings.TrimSpace(req.GetEmail()) == "" {
		return nil, errInvalid("missing", []string{"body", "email"}, "email is required")
	}

	placement, err := adminPlacement(s.env, ctx, req)
	if err != nil {
		return nil, err
	}

	password, minted, err := mintedPassword(req.GetPassword())
	if err != nil {
		return nil, err
	}
	mustChange := true
	if req.MustChangePassword != nil {
		mustChange = req.GetMustChangePassword()
	}

	created, err := service.CreateAccount(ctx, s.env.DB, service.NewAccount{
		Email:              req.GetEmail(),
		FullName:           req.GetFullName(),
		Password:           password,
		MustChangePassword: mustChange,
		IsSuperuser:        req.GetIsSuperuser(),
		Placement:          placement,
	}, s.env.now())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailTaken):
			return nil, errConflict("An account already uses %s", strings.TrimSpace(req.GetEmail()))
		case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
			return nil, errInvalid("value", []string{"body", "password"}, "%s", err.Error())
		}
		return nil, err
	}

	user, err := adminOneUser(s.env, ctx, created)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateUserResponse{User: user, TemporaryPassword: minted}, nil
}

// mintedPassword is the password an account gets: the one sent, or a fresh
// one when that is blank, which is also returned second so it is shown once.
func mintedPassword(sent string) (string, *string, error) {
	if strings.TrimSpace(sent) != "" {
		return sent, nil, nil
	}
	password, err := service.TemporaryPassword()
	if err != nil {
		return "", nil, err
	}
	return password, &password, nil
}

// adminPlacement turns the two ways of saying where an account goes into the
// one the service takes, refusing an account with nowhere to be.
func adminPlacement(
	env *Env, ctx context.Context, req *agentifiv1.CreateUserRequest,
) (service.Placement, error) {
	if req.SpaceId != nil {
		id, err := spaceIDFromBody(req.GetSpaceId())
		if err != nil {
			return service.Placement{}, err
		}
		spaceID := store.SpaceIDOf(id)
		space, err := env.DB.GetSpace(ctx, spaceID)
		if err != nil {
			return service.Placement{}, notFoundAs(err, "Space")
		}
		if space.IsDeleted {
			return service.Placement{}, errNotFound("Space")
		}
		role, err := parseRole(roleFrom(req.GetRole()))
		if err != nil {
			return service.Placement{}, err
		}
		return service.Placement{SpaceID: &spaceID, Role: role}, nil
	}

	name := strings.TrimSpace(req.GetSpaceName())
	if name == "" {
		return service.Placement{}, errInvalid("missing", []string{"body", "space_name"},
			"give a space to create, or a space_id and a role to join one")
	}
	currency := strings.ToUpper(strings.TrimSpace(req.GetCurrency()))
	if currency == "" {
		currency = env.Cfg.PrimaryCurrency
	}
	if len(currency) != 3 {
		return service.Placement{}, errInvalid("format", []string{"body", "currency"},
			"a currency is a three-letter ISO 4217 code")
	}
	return service.Placement{SpaceName: name, Currency: currency}, nil
}

// spaceIDFromBody reads the space a body names. A malformed one is a bad
// request rather than a row nobody may see, as it was when the body decoded
// into a uuid.UUID.
func spaceIDFromBody(raw string) (uuid.UUID, error) {
	id, err := uuid.Parse(raw)
	if err != nil {
		return uuid.Nil, errInvalid("type_error", []string{"body", "space_id"}, "space_id is not an id: %s", err.Error())
	}
	return id, nil
}

func (s adminService) UpdateUser(ctx context.Context, req *agentifiv1.UpdateUserRequest) (*agentifiv1.UpdateUserResponse, error) {
	target, err := adminTargetUser(s.env, ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}
	fullName := optOf(mask, "full_name", req.FullName)
	isActive := optOf(mask, "is_active", req.IsActive)
	isSuperuser := optOf(mask, "is_superuser", req.IsSuperuser)

	deactivating := isActive.Present() && !isActive.Value && target.IsActive
	demoting := isSuperuser.Present() && !isSuperuser.Value && target.IsSuperuser
	if err := guardTheLastAdministrator(s.env, ctx, userFrom(ctx), target, deactivating, demoting); err != nil {
		return nil, err
	}

	if fullName.Present() {
		name := strings.TrimSpace(fullName.Value)
		if name == "" {
			return nil, errInvalid("missing", []string{"body", "full_name"}, "a name cannot be blank")
		}
		if err := s.env.DB.SetUserFullName(ctx, target.ID, name); err != nil {
			return nil, err
		}
		target.FullName = name
	}
	if isActive.Present() {
		if err := s.env.DB.SetUserActive(ctx, target.ID, isActive.Value); err != nil {
			return nil, err
		}
		target.IsActive = isActive.Value
	}
	if isSuperuser.Present() {
		err := s.env.DB.SetUserSuperuser(ctx, target.ID, isSuperuser.Value)
		if errors.Is(err, store.ErrLastSuperuser) {
			return nil, errConflict(lastAdministrator)
		}
		if err != nil {
			return nil, err
		}
		target.IsSuperuser = isSuperuser.Value
	}

	user, err := adminOneUser(s.env, ctx, target)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateUserResponse{User: user}, nil
}

// guardTheLastAdministrator refuses the changes that cannot be undone from
// inside the application: demoting or deactivating yourself, or the last
// active superuser. Since reaching this method needs an active superuser, the
// self check catches the common case; the count check states the invariant.
// The two refusals read differently because they lead to different next steps.
func guardTheLastAdministrator(
	env *Env, ctx context.Context,
	caller, target store.User, deactivating, demoting bool,
) error {
	if !deactivating && !demoting {
		return nil
	}
	if caller.ID == target.ID {
		if demoting {
			return errConflict(
				"You cannot take your own administrator rights away. " +
					"Ask another administrator to do it.")
		}
		return errConflict("You cannot deactivate your own account.")
	}
	if !target.IsSuperuser || !target.IsActive {
		return nil
	}
	remaining, err := env.DB.CountActiveSuperusers(ctx, target.ID)
	if err != nil {
		return err
	}
	if remaining == 0 {
		return errConflict(lastAdministrator)
	}
	return nil
}

const lastAdministrator = "This is the last administrator. Make somebody else an administrator first."

func (s adminService) SetUserPassword(ctx context.Context, req *agentifiv1.SetUserPasswordRequest) (*agentifiv1.SetUserPasswordResponse, error) {
	target, err := adminTargetUser(s.env, ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}

	password, minted, err := mintedPassword(req.GetPassword())
	if err != nil {
		return nil, err
	}
	mustChange := true
	if req.MustChangePassword != nil {
		mustChange = req.GetMustChangePassword()
	}

	err = service.SetAccountPassword(ctx, s.env.DB, target.ID, password, mustChange, s.env.now())
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return nil, errInvalid("value", []string{"body", "password"}, "%s", err.Error())
		}
		return nil, err
	}
	return &agentifiv1.SetUserPasswordResponse{TemporaryPassword: minted}, nil
}

func (s adminService) AddMembership(ctx context.Context, req *agentifiv1.AddMembershipRequest) (*agentifiv1.AddMembershipResponse, error) {
	target, err := adminTargetUser(s.env, ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	if req.GetSpaceId() == "" {
		return nil, errInvalid("missing", []string{"body", "space_id"}, "space_id is required")
	}
	id, err := spaceIDFromBody(req.GetSpaceId())
	if err != nil {
		return nil, err
	}
	role, err := parseRole(roleFrom(req.GetRole()))
	if err != nil {
		return nil, err
	}

	spaceID := store.SpaceIDOf(id)
	space, err := s.env.DB.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, notFoundAs(err, "Space")
	}
	if space.IsDeleted {
		return nil, errNotFound("Space")
	}

	switch existing, err := s.env.DB.GetMembership(ctx, spaceID, target.ID); {
	case err == nil && existing.IsAccepted():
		return nil, errConflict("%s is already in %s", target.Email, space.Name)
	case err == nil:
		return nil, errConflict("%s has already been invited to %s", target.Email, space.Name)
	case !isNotFound(err):
		return nil, err
	}

	now := s.env.now().UTC()
	membership := &store.Membership{
		UserID: target.ID, Role: role, InvitedAt: &now, AcceptedAt: &now,
	}
	if err := s.env.DB.CreateMembership(ctx, spaceID, membership); err != nil {
		return nil, err
	}
	return &agentifiv1.AddMembershipResponse{Membership: &agentifiv1.AdminMembership{
		Id:        membership.ID.String(),
		SpaceId:   spaceID.String(),
		SpaceName: space.Name,
		Role:      roleProto(membership.Role),
		Accepted:  true,
	}}, nil
}

func (s adminService) RemoveMembership(ctx context.Context, req *agentifiv1.RemoveMembershipRequest) (*agentifiv1.RemoveMembershipResponse, error) {
	target, err := adminTargetUser(s.env, ctx, req.GetUserId())
	if err != nil {
		return nil, err
	}
	id, err := idFrom(req.GetMembershipId(), "Member")
	if err != nil {
		return nil, err
	}

	memberships, err := adminMemberships(s.env, ctx)
	if err != nil {
		return nil, err
	}
	index := slices.IndexFunc(memberships[target.ID], func(m store.Membership) bool {
		return m.ID == id
	})
	if index < 0 {
		return nil, errNotFound("Member")
	}
	membership := memberships[target.ID][index]

	// A space whose last accepted owner is removed cannot be shared, renamed
	// or recovered.
	if err := ensureAnOwnerRemains(ctx, s.env, membership.SpaceID, membership); err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteMembership(ctx, membership.SpaceID, membership.ID); err != nil {
		return nil, notFoundAs(err, "Member")
	}
	return &agentifiv1.RemoveMembershipResponse{}, nil
}

// adminTargetUser is the account a request names.
func adminTargetUser(env *Env, ctx context.Context, raw string) (store.User, error) {
	id, err := idFrom(raw, "User")
	if err != nil {
		return store.User{}, err
	}
	user, err := env.DB.GetUser(ctx, id)
	if err != nil {
		return store.User{}, notFoundAs(err, "User")
	}
	return user, nil
}

// adminMemberships is every membership on the install, grouped by user, in one
// query.
func adminMemberships(env *Env, ctx context.Context) (map[uuid.UUID][]store.Membership, error) {
	memberships, err := env.DB.ListAllMemberships(ctx)
	if err != nil {
		return nil, err
	}
	grouped := map[uuid.UUID][]store.Membership{}
	for _, membership := range memberships {
		grouped[membership.UserID] = append(grouped[membership.UserID], membership)
	}
	return grouped, nil
}

// adminUserProto is one account as the administration screen sees it. The
// sign-in methods are counts and booleans, never the credentials.
func adminUserProto(
	env *Env, ctx context.Context, user store.User,
	memberships []store.Membership, names map[store.SpaceID]string,
) (*agentifiv1.AdminUser, error) {
	// Per user, because the passkey store answers per user.
	keys, err := env.Keys.ListPasskeys(ctx, user.ID)
	if err != nil {
		return nil, err
	}

	placed := make([]*agentifiv1.AdminMembership, 0, len(memberships))
	for _, membership := range memberships {
		name, live := names[membership.SpaceID]
		// A membership into a deleted space is not a place anybody is.
		if !live {
			continue
		}
		placed = append(placed, &agentifiv1.AdminMembership{
			Id:        membership.ID.String(),
			SpaceId:   membership.SpaceID.String(),
			SpaceName: name,
			Role:      roleProto(membership.Role),
			Accepted:  membership.IsAccepted(),
		})
	}

	return &agentifiv1.AdminUser{
		Id:                 user.ID.String(),
		Email:              user.Email,
		FullName:           store.NullText(user.FullName),
		IsActive:           user.IsActive,
		IsSuperuser:        user.IsSuperuser,
		IsVerified:         user.IsVerified,
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          timestamppb.New(user.CreatedAt),
		LastLoginAt:        timestampProto(user.LastLoginAt),
		HasPassword:        user.HashedPassword != "",
		HasTotp:            user.TOTPSecret != "",
		HasOidc:            user.OIDCSubject != "",
		OidcIssuer:         user.OIDCIssuer,
		PasskeyCount:       int32(len(keys)),
		Memberships:        placed,
	}, nil
}

// adminOneUser is the listing's shape for a single account, for the methods
// that answer with the row they just wrote.
func adminOneUser(env *Env, ctx context.Context, user store.User) (*agentifiv1.AdminUser, error) {
	memberships, err := adminMemberships(env, ctx)
	if err != nil {
		return nil, err
	}
	names, err := adminSpaceNames(env, ctx)
	if err != nil {
		return nil, err
	}
	return adminUserProto(env, ctx, user, memberships[user.ID], names)
}

func adminSpaceNames(env *Env, ctx context.Context) (map[store.SpaceID]string, error) {
	spaces, err := env.DB.ListSpaces(ctx)
	if err != nil {
		return nil, err
	}
	names := make(map[store.SpaceID]string, len(spaces))
	for _, space := range spaces {
		names[space.ID] = space.Name
	}
	return names, nil
}

// --- Spaces -------------------------------------------------------------------

func (s adminService) ListAllSpaces(ctx context.Context, _ *agentifiv1.ListAllSpacesRequest) (*agentifiv1.ListAllSpacesResponse, error) {
	spaces, err := s.env.DB.ListSpaces(ctx)
	if err != nil {
		return nil, err
	}
	users, err := s.env.DB.ListUsers(ctx)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]store.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	memberships, err := s.env.DB.ListAllMemberships(ctx)
	if err != nil {
		return nil, err
	}
	bySpace := map[store.SpaceID][]store.Membership{}
	for _, membership := range memberships {
		bySpace[membership.SpaceID] = append(bySpace[membership.SpaceID], membership)
	}

	out := &agentifiv1.ListAllSpacesResponse{Spaces: make([]*agentifiv1.AdminSpace, 0, len(spaces))}
	for _, space := range spaces {
		members := make([]*agentifiv1.AdminSpaceMember, 0, len(bySpace[space.ID]))
		for _, membership := range bySpace[space.ID] {
			member, known := byID[membership.UserID]
			if !known {
				continue
			}
			members = append(members, &agentifiv1.AdminSpaceMember{
				MembershipId: membership.ID.String(),
				UserId:       member.ID.String(),
				Email:        member.Email,
				FullName:     store.NullText(member.FullName),
				Role:         roleProto(membership.Role),
				Accepted:     membership.IsAccepted(),
			})
		}
		out.Spaces = append(out.Spaces, &agentifiv1.AdminSpace{
			Id:              space.ID.String(),
			Name:            space.Name,
			PrimaryCurrency: space.PrimaryCurrency,
			Timezone:        space.Timezone,
			CreatedAt:       timestamppb.New(space.CreatedAt),
			Members:         members,
		})
	}
	return out, nil
}

// --- Single sign-on -----------------------------------------------------------
//
// The provider is a stored setting, with the environment behind any value never
// saved. A save takes effect on the next login through auth.OIDC.Configure,
// which also drops the cached discovery document: a verifier built for the
// previous client id would fail every sign-in.

// oidcSettingSources names each value in OIDCSettings the way the message
// spells it, so a screen can label fields still coming from the environment.
var oidcSettingSources = map[string]string{
	store.OIDCEnabledSetting:             "enabled",
	store.OIDCProviderNameSetting:        "provider_name",
	store.OIDCDiscoveryURLSetting:        "discovery_url",
	store.OIDCClientIDSetting:            "client_id",
	store.OIDCClientSecretSetting:        "client_secret",
	store.OIDCScopesSetting:              "scopes",
	store.OIDCAutoRegisterSetting:        "auto_register",
	store.OIDCRequireVerifiedMailSetting: "require_verified_email",
	store.OIDCLinkExistingEmailSetting:   "link_existing_email",
	store.OIDCGroupsClaimSetting:         "groups_claim",
	store.OIDCAdminGroupsSetting:         "admin_groups",
}

// OIDCFromEnvironment is the provider as the process configuration describes
// it, which is what stands behind anything saved in the app.
func OIDCFromEnvironment(cfg *config.Config) auth.OIDCSettings {
	return auth.OIDCSettings{
		Enabled:              cfg.OIDCEnabled,
		ProviderName:         cfg.OIDCProviderName,
		DiscoveryURL:         cfg.OIDCDiscoveryURL,
		ClientID:             cfg.OIDCClientID,
		ClientSecret:         cfg.OIDCClientSecret,
		Scopes:               cfg.OIDCScopes,
		AutoRegister:         cfg.OIDCAutoRegister,
		RequireVerifiedEmail: cfg.OIDCRequireVerifiedMail,
		LinkExistingEmail:    cfg.OIDCLinkExistingEmail,
		GroupsClaim:          cfg.OIDCGroupsClaim,
		AdminGroups:          cfg.OIDCAdminGroups,
	}
}

// ReloadOIDC reads the stored provider settings and applies them to the running
// server. Called at startup (by `serve`) and after every save, so both merge
// the environment and the database the same way.
func (e *Env) ReloadOIDC(ctx context.Context) error {
	settings, _, err := effectiveOIDC(ctx, e)
	if err != nil {
		return err
	}
	e.OIDC.Configure(settings)
	return nil
}

// effectiveOIDC merges what was saved over what the environment says, and
// reports which of the two answered for each value.
func effectiveOIDC(
	ctx context.Context, env *Env,
) (auth.OIDCSettings, map[string]string, error) {
	settings := OIDCFromEnvironment(env.Cfg)
	sources := map[string]string{}
	for _, field := range oidcSettingSources {
		sources[field] = "environment"
	}

	db, err := settingsStore(env.Cfg, env.DB)
	if err != nil {
		return settings, sources, err
	}
	stored, err := db.GetServerSettings(ctx, store.OIDCSettingKeys)
	if err != nil {
		return settings, sources, err
	}

	for key, value := range stored {
		switch key {
		case store.OIDCEnabledSetting:
			settings.Enabled = value == "true"
		case store.OIDCProviderNameSetting:
			settings.ProviderName = value
		case store.OIDCDiscoveryURLSetting:
			settings.DiscoveryURL = value
		case store.OIDCClientIDSetting:
			settings.ClientID = value
		case store.OIDCClientSecretSetting:
			settings.ClientSecret = value
		case store.OIDCScopesSetting:
			settings.Scopes = strings.Fields(value)
		case store.OIDCAutoRegisterSetting:
			settings.AutoRegister = value == "true"
		case store.OIDCRequireVerifiedMailSetting:
			settings.RequireVerifiedEmail = value == "true"
		case store.OIDCLinkExistingEmailSetting:
			settings.LinkExistingEmail = value == "true"
		case store.OIDCGroupsClaimSetting:
			settings.GroupsClaim = value
		case store.OIDCAdminGroupsSetting:
			settings.AdminGroups = adminGroups(strings.Split(value, ","))
		}
		sources[oidcSettingSources[key]] = "database"
	}
	return settings, sources, nil
}

// adminGroups trims each group and drops the blanks.
func adminGroups(groups []string) []string {
	var out []string
	for _, group := range groups {
		if group = strings.TrimSpace(group); group != "" {
			out = append(out, group)
		}
	}
	return out
}

func (s adminService) GetOIDCSettings(ctx context.Context, _ *agentifiv1.GetOIDCSettingsRequest) (*agentifiv1.GetOIDCSettingsResponse, error) {
	settings, sources, err := effectiveOIDC(ctx, s.env)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetOIDCSettingsResponse{Settings: oidcSettingsProto(s.env, settings, sources)}, nil
}

func (s adminService) SaveOIDCSettings(ctx context.Context, req *agentifiv1.SaveOIDCSettingsRequest) (*agentifiv1.SaveOIDCSettingsResponse, error) {
	discovery := strings.TrimSpace(req.GetDiscoveryUrl())
	clientID := strings.TrimSpace(req.GetClientId())
	// An enabled provider with no discovery URL would put a login button on
	// screen that 404s.
	if req.GetEnabled() && (discovery == "" || clientID == "") {
		return nil, errInvalid("missing", []string{"body", "discovery_url"},
			"turning single sign-on on needs a discovery URL and a client id")
	}
	groups := adminGroups(req.GetAdminGroups())
	for _, group := range groups {
		if strings.Contains(group, ",") {
			return nil, errInvalid("invalid", []string{"body", "admin_groups"},
				"a group name cannot contain a comma")
		}
	}

	values := map[string]string{
		store.OIDCEnabledSetting:             strconv.FormatBool(req.GetEnabled()),
		store.OIDCProviderNameSetting:        strings.TrimSpace(req.GetProviderName()),
		store.OIDCDiscoveryURLSetting:        discovery,
		store.OIDCClientIDSetting:            clientID,
		store.OIDCScopesSetting:              strings.Join(req.GetScopes(), " "),
		store.OIDCAutoRegisterSetting:        strconv.FormatBool(req.GetAutoRegister()),
		store.OIDCRequireVerifiedMailSetting: strconv.FormatBool(req.GetRequireVerifiedEmail()),
		store.OIDCLinkExistingEmailSetting:   strconv.FormatBool(req.GetLinkExistingEmail()),
		store.OIDCGroupsClaimSetting:         strings.TrimSpace(req.GetGroupsClaim()),
		store.OIDCAdminGroupsSetting:         strings.Join(groups, ","),
	}
	// Blank keeps what is stored: the stored value never comes back to the
	// browser, so the field is blank on every visit.
	if secret := strings.TrimSpace(req.GetClientSecret()); secret != "" {
		values[store.OIDCClientSecretSetting] = secret
	}

	db, err := settingsStore(s.env.Cfg, s.env.DB)
	if err != nil {
		return nil, err
	}
	if err := db.SetServerSettings(ctx, store.OIDCSettingKeys, values); err != nil {
		return nil, err
	}

	settings, sources, err := effectiveOIDC(ctx, s.env)
	if err != nil {
		return nil, err
	}
	// Live, before the response goes out: the screen's next act is often to
	// open a private window and try the sign-in button.
	s.env.OIDC.Configure(settings)
	return &agentifiv1.SaveOIDCSettingsResponse{Settings: oidcSettingsProto(s.env, settings, sources)}, nil
}

func (s adminService) TestOIDCSettings(ctx context.Context, req *agentifiv1.TestOIDCSettingsRequest) (*agentifiv1.TestOIDCSettingsResponse, error) {
	discovery := strings.TrimSpace(req.GetDiscoveryUrl())
	if discovery == "" {
		settings, _, err := effectiveOIDC(ctx, s.env)
		if err != nil {
			return nil, err
		}
		discovery = settings.DiscoveryURL
	}
	if discovery == "" {
		return nil, errInvalid("missing", []string{"body", "discovery_url"},
			"there is no discovery URL to test")
	}

	document, err := s.env.OIDC.ProbeDiscovery(ctx, discovery)
	if err != nil {
		return &agentifiv1.TestOIDCSettingsResponse{
			Valid: false, Issuer: document.Issuer, Message: err.Error(),
		}, nil
	}
	return &agentifiv1.TestOIDCSettingsResponse{
		Valid:                 true,
		Issuer:                document.Issuer,
		Message:               "The provider answered.",
		AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint:         document.TokenEndpoint,
		UserinfoEndpoint:      document.UserInfoEndpoint,
		JwksUri:               document.JWKSURI,
	}, nil
}

func oidcSettingsProto(
	env *Env, settings auth.OIDCSettings, sources map[string]string,
) *agentifiv1.OIDCSettings {
	claim := settings.GroupsClaim
	if claim == "" {
		claim = auth.DefaultGroupsClaim
	}
	return &agentifiv1.OIDCSettings{
		Enabled:              settings.Enabled,
		ProviderName:         settings.ProviderName,
		DiscoveryUrl:         settings.DiscoveryURL,
		ClientId:             settings.ClientID,
		HasClientSecret:      settings.ClientSecret != "",
		Scopes:               settings.Scopes,
		AutoRegister:         settings.AutoRegister,
		RequireVerifiedEmail: settings.RequireVerifiedEmail,
		LinkExistingEmail:    settings.LinkExistingEmail,
		GroupsClaim:          claim,
		AdminGroups:          settings.AdminGroups,
		Sources: &agentifiv1.OIDCSettingSources{
			Enabled:              settingSource(sources["enabled"]),
			ProviderName:         settingSource(sources["provider_name"]),
			DiscoveryUrl:         settingSource(sources["discovery_url"]),
			ClientId:             settingSource(sources["client_id"]),
			ClientSecret:         settingSource(sources["client_secret"]),
			Scopes:               settingSource(sources["scopes"]),
			AutoRegister:         settingSource(sources["auto_register"]),
			RequireVerifiedEmail: settingSource(sources["require_verified_email"]),
			LinkExistingEmail:    settingSource(sources["link_existing_email"]),
			GroupsClaim:          settingSource(sources["groups_claim"]),
			AdminGroups:          settingSource(sources["admin_groups"]),
		},
		CallbackUrl: env.OIDC.CallbackURI(),
		Configured:  settings.IsConfigured(),
	}
}

// settingSource is where a value came from, as config.Source and the backup
// settings both spell it.
func settingSource[S ~string](source S) agentifiv1.SettingSource {
	switch config.Source(source) {
	case config.FromEnvironment:
		return agentifiv1.SettingSource_SETTING_SOURCE_ENVIRONMENT
	case config.FromDatabase:
		return agentifiv1.SettingSource_SETTING_SOURCE_DATABASE
	case config.FromDefault:
		return agentifiv1.SettingSource_SETTING_SOURCE_DEFAULT
	}
	return agentifiv1.SettingSource_SETTING_SOURCE_UNSPECIFIED
}
