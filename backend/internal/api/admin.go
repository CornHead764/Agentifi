package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Administering the server from the app rather than from the box.
//
// These are the only routes that resolve no space, have a caller, and answer
// about every household at once, so they are registered through RegisterAdmin
// and mounted as Superuser: the check is in the registry, not the handlers.
// The assistant's dispatcher mounts only Read and Write, so a model cannot
// reach this file.
//
// Two rules guard failures that are unrecoverable without a shell:
//
//   - A superuser cannot deactivate or demote their own account.
//   - The last active superuser cannot be deactivated or demoted by anybody;
//     only a superuser can make another.
//
// Nothing here returns a password hash, a TOTP seed or the OIDC client secret;
// the response types have no field any of them could ride on.

func init() {
	RegisterAdmin(Resource{Prefix: "/admin", Routes: func(rt *Routes) {
		rt.Superuser(http.MethodGet, "/users", listAllUsers)
		rt.Superuser(http.MethodPost, "/users", createUserAccount)
		rt.Superuser(http.MethodPatch, "/users/{user_id}", updateUserAccount)
		rt.Superuser(http.MethodPost, "/users/{user_id}/password", setUserAccountPassword)
		rt.Superuser(http.MethodPost, "/users/{user_id}/memberships", addUserToSpace)
		rt.Superuser(http.MethodDelete,
			"/users/{user_id}/memberships/{membership_id}", removeUserFromSpace)

		rt.Superuser(http.MethodGet, "/spaces", listAllSpaces)

		rt.Superuser(http.MethodGet, "/oidc", readOIDCSettings)
		rt.Superuser(http.MethodPut, "/oidc", saveOIDCSettings)
		rt.Superuser(http.MethodPost, "/oidc/test", testOIDCSettings)
	}})
}

// errNotAdministrator is the refusal every route here gives a caller who is not
// a superuser. Raised by the registry before a handler runs — see serve.
var errNotAdministrator = errors.New("api: this account does not administer this server")

// --- Users --------------------------------------------------------------------

// AdminUserResponse is one account as the administration screen sees it. The
// sign-in methods are counts and booleans, never the credentials.
type AdminUserResponse struct {
	ID                 uuid.UUID  `json:"id"`
	Email              string     `json:"email"`
	FullName           *string    `json:"full_name"`
	IsActive           bool       `json:"is_active"`
	IsSuperuser        bool       `json:"is_superuser"`
	IsVerified         bool       `json:"is_verified"`
	MustChangePassword bool       `json:"must_change_password"`
	CreatedAt          time.Time  `json:"created_at"`
	LastLoginAt        *time.Time `json:"last_login_at"`

	HasPassword bool `json:"has_password"`
	HasTOTP     bool `json:"has_totp"`
	HasOIDC     bool `json:"has_oidc"`
	// OIDCIssuer names which provider vouches for this account, because an
	// install can have been repointed and the issuer is half the identity.
	OIDCIssuer   string `json:"oidc_issuer"`
	PasskeyCount int    `json:"passkey_count"`

	Memberships []AdminMembershipResponse `json:"memberships"`
}

// AdminMembershipResponse is one person's place in one space, with the space's
// name, which the administrator has no other way to read.
type AdminMembershipResponse struct {
	ID        uuid.UUID  `json:"id"`
	SpaceID   uuid.UUID  `json:"space_id"`
	SpaceName string     `json:"space_name"`
	Role      store.Role `json:"role"`
	Accepted  bool       `json:"accepted"`
}

// AdminUserCreate is an account to make. Either SpaceName creates a space the
// account owns, or SpaceID joins one that exists with Role.
type AdminUserCreate struct {
	Email    string `json:"email"`
	FullName string `json:"full_name"`
	// Password is optional. Omitted, the server mints one and returns it once.
	Password string `json:"password"`
	// MustChangePassword defaults to true, because whoever runs the server
	// knows whatever password this account starts with.
	MustChangePassword *bool `json:"must_change_password"`
	IsSuperuser        bool  `json:"is_superuser"`

	SpaceName string     `json:"space_name"`
	Currency  string     `json:"currency"`
	SpaceID   *uuid.UUID `json:"space_id"`
	Role      store.Role `json:"role"`
}

// AdminUserCreated carries the new account and, when the server minted one, the
// password — shown once and never readable again.
type AdminUserCreated struct {
	User AdminUserResponse `json:"user"`
	// TemporaryPassword is empty when the caller supplied the password. Nothing
	// reads it back.
	TemporaryPassword string `json:"temporary_password,omitempty"`
}

type AdminUserUpdate struct {
	FullName    Opt[string] `json:"full_name"`
	IsActive    Opt[bool]   `json:"is_active"`
	IsSuperuser Opt[bool]   `json:"is_superuser"`
}

// AdminPasswordWrite sets a password for somebody else. An empty one asks the
// server to mint it.
type AdminPasswordWrite struct {
	Password           string `json:"password"`
	MustChangePassword *bool  `json:"must_change_password"`
}

type AdminPasswordSet struct {
	TemporaryPassword string `json:"temporary_password,omitempty"`
}

type AdminMembershipWrite struct {
	SpaceID uuid.UUID  `json:"space_id"`
	Role    store.Role `json:"role"`
}

func listAllUsers(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	users, err := env.DB.ListUsers(r.Context())
	if err != nil {
		return err
	}
	memberships, err := adminMemberships(env, r.Context())
	if err != nil {
		return err
	}
	names, err := adminSpaceNames(env, r.Context())
	if err != nil {
		return err
	}

	out := make([]AdminUserResponse, 0, len(users))
	for _, user := range users {
		response, err := adminUserResponse(
			env, r.Context(), user, memberships[user.ID], names)
		if err != nil {
			return err
		}
		out = append(out, response)
	}
	return writeJSON(w, http.StatusOK, out)
}

func createUserAccount(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminUserCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if strings.TrimSpace(body.Email) == "" {
		return errInvalid("missing", []string{"body", "email"}, "email is required")
	}

	placement, err := adminPlacement(env, r.Context(), body)
	if err != nil {
		return err
	}

	password, minted := body.Password, ""
	if strings.TrimSpace(password) == "" {
		if password, err = service.TemporaryPassword(); err != nil {
			return err
		}
		minted = password
	}
	mustChange := true
	if body.MustChangePassword != nil {
		mustChange = *body.MustChangePassword
	}

	created, err := service.CreateAccount(r.Context(), env.DB, service.NewAccount{
		Email:              body.Email,
		FullName:           body.FullName,
		Password:           password,
		MustChangePassword: mustChange,
		IsSuperuser:        body.IsSuperuser,
		Placement:          placement,
	}, env.now())
	if err != nil {
		switch {
		case errors.Is(err, service.ErrEmailTaken):
			return errConflict("An account already uses %s", strings.TrimSpace(body.Email))
		case errors.Is(err, auth.ErrPasswordTooShort), errors.Is(err, auth.ErrPasswordTooLong):
			return errInvalid("value", []string{"body", "password"}, "%s", err.Error())
		}
		return err
	}

	response, err := adminOneUser(env, r, created)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated,
		AdminUserCreated{User: response, TemporaryPassword: minted})
}

// adminPlacement turns the two ways of saying where an account goes into the
// one the service takes, refusing an account with nowhere to be.
func adminPlacement(
	env *Env, ctx context.Context, body AdminUserCreate,
) (service.Placement, error) {
	if body.SpaceID != nil {
		spaceID := store.SpaceIDOf(*body.SpaceID)
		space, err := env.DB.GetSpace(ctx, spaceID)
		if err != nil {
			return service.Placement{}, notFoundAs(err, "Space")
		}
		if space.IsDeleted {
			return service.Placement{}, errNotFound("Space")
		}
		role, err := parseRole(body.Role)
		if err != nil {
			return service.Placement{}, err
		}
		return service.Placement{SpaceID: &spaceID, Role: role}, nil
	}

	name := strings.TrimSpace(body.SpaceName)
	if name == "" {
		return service.Placement{}, errInvalid("missing", []string{"body", "space_name"},
			"give a space to create, or a space_id and a role to join one")
	}
	currency := strings.ToUpper(strings.TrimSpace(body.Currency))
	if currency == "" {
		currency = env.Cfg.PrimaryCurrency
	}
	if len(currency) != 3 {
		return service.Placement{}, errInvalid("format", []string{"body", "currency"},
			"a currency is a three-letter ISO 4217 code")
	}
	return service.Placement{SpaceName: name, Currency: currency}, nil
}

func updateUserAccount(env *Env, w http.ResponseWriter, r *http.Request, caller store.User) error {
	target, err := adminTargetUser(env, r)
	if err != nil {
		return err
	}
	var body AdminUserUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	deactivating := body.IsActive.Present() && !body.IsActive.Value && target.IsActive
	demoting := body.IsSuperuser.Present() && !body.IsSuperuser.Value && target.IsSuperuser
	if err := guardTheLastAdministrator(env, r.Context(), caller, target, deactivating, demoting); err != nil {
		return err
	}

	if body.FullName.Present() {
		name := strings.TrimSpace(body.FullName.Value)
		if name == "" {
			return errInvalid("missing", []string{"body", "full_name"}, "a name cannot be blank")
		}
		if err := env.DB.SetUserFullName(r.Context(), target.ID, name); err != nil {
			return err
		}
		target.FullName = name
	}
	if body.IsActive.Present() {
		if err := env.DB.SetUserActive(r.Context(), target.ID, body.IsActive.Value); err != nil {
			return err
		}
		target.IsActive = body.IsActive.Value
	}
	if body.IsSuperuser.Present() {
		err := env.DB.SetUserSuperuser(r.Context(), target.ID, body.IsSuperuser.Value)
		if errors.Is(err, store.ErrLastSuperuser) {
			return errConflict(lastAdministrator)
		}
		if err != nil {
			return err
		}
		target.IsSuperuser = body.IsSuperuser.Value
	}

	response, err := adminOneUser(env, r, target)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

// guardTheLastAdministrator refuses the changes that cannot be undone from
// inside the application: demoting or deactivating yourself, or the last
// active superuser. Since reaching this route needs an active superuser, the
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

// setUserAccountPassword hands an account a password its owner did not choose.
// Every existing session for it ends, deliberately.
func setUserAccountPassword(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	target, err := adminTargetUser(env, r)
	if err != nil {
		return err
	}
	var body AdminPasswordWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	password, minted := body.Password, ""
	if strings.TrimSpace(password) == "" {
		if password, err = service.TemporaryPassword(); err != nil {
			return err
		}
		minted = password
	}
	// True unless the caller says otherwise: whoever runs the server now knows
	// this password, so the account owes a change.
	mustChange := true
	if body.MustChangePassword != nil {
		mustChange = *body.MustChangePassword
	}

	err = service.SetAccountPassword(r.Context(), env.DB, target.ID, password, mustChange, env.now())
	if err != nil {
		if errors.Is(err, auth.ErrPasswordTooShort) || errors.Is(err, auth.ErrPasswordTooLong) {
			return errInvalid("value", []string{"body", "password"}, "%s", err.Error())
		}
		return err
	}
	return writeJSON(w, http.StatusOK, AdminPasswordSet{TemporaryPassword: minted})
}

// addUserToSpace puts somebody in a space, accepted rather than invited, unlike
// POST /spaces/{id}/members: the account may have been created a moment ago and
// has nobody to answer an invitation.
func addUserToSpace(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	target, err := adminTargetUser(env, r)
	if err != nil {
		return err
	}
	var body AdminMembershipWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.SpaceID == uuid.Nil {
		return errInvalid("missing", []string{"body", "space_id"}, "space_id is required")
	}
	role, err := parseRole(body.Role)
	if err != nil {
		return err
	}

	spaceID := store.SpaceIDOf(body.SpaceID)
	space, err := env.DB.GetSpace(r.Context(), spaceID)
	if err != nil {
		return notFoundAs(err, "Space")
	}
	if space.IsDeleted {
		return errNotFound("Space")
	}

	switch existing, err := env.DB.GetMembership(r.Context(), spaceID, target.ID); {
	case err == nil && existing.IsAccepted():
		return errConflict("%s is already in %s", target.Email, space.Name)
	case err == nil:
		return errConflict("%s has already been invited to %s", target.Email, space.Name)
	case !isNotFound(err):
		return err
	}

	now := env.now().UTC()
	membership := &store.Membership{
		UserID: target.ID, Role: role, InvitedAt: &now, AcceptedAt: &now,
	}
	if err := env.DB.CreateMembership(r.Context(), spaceID, membership); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, AdminMembershipResponse{
		ID:        membership.ID,
		SpaceID:   spaceID.UUID(),
		SpaceName: space.Name,
		Role:      membership.Role,
		Accepted:  true,
	})
}

func removeUserFromSpace(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	target, err := adminTargetUser(env, r)
	if err != nil {
		return err
	}
	id, err := pathUUID(r, "membership_id", "Member")
	if err != nil {
		return err
	}

	memberships, err := adminMemberships(env, r.Context())
	if err != nil {
		return err
	}
	index := slices.IndexFunc(memberships[target.ID], func(m store.Membership) bool {
		return m.ID == id
	})
	if index < 0 {
		return errNotFound("Member")
	}
	membership := memberships[target.ID][index]

	// A space whose last accepted owner is removed cannot be shared, renamed
	// or recovered.
	if err := ensureAnOwnerRemains(env, r, membership.SpaceID, membership); err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteMembership(r.Context(), membership.SpaceID, membership.ID), "Member")
}

// adminTargetUser is the account named in the path.
func adminTargetUser(env *Env, r *http.Request) (store.User, error) {
	id, err := pathUUID(r, "user_id", "User")
	if err != nil {
		return store.User{}, err
	}
	user, err := env.DB.GetUser(r.Context(), id)
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

func adminUserResponse(
	env *Env, ctx context.Context, user store.User,
	memberships []store.Membership, names map[store.SpaceID]string,
) (AdminUserResponse, error) {
	// Per user, because the passkey store answers per user.
	keys, err := env.Keys.ListPasskeys(ctx, user.ID)
	if err != nil {
		return AdminUserResponse{}, err
	}

	placed := make([]AdminMembershipResponse, 0, len(memberships))
	for _, membership := range memberships {
		name, live := names[membership.SpaceID]
		// A membership into a deleted space is not a place anybody is.
		if !live {
			continue
		}
		placed = append(placed, AdminMembershipResponse{
			ID:        membership.ID,
			SpaceID:   membership.SpaceID.UUID(),
			SpaceName: name,
			Role:      membership.Role,
			Accepted:  membership.IsAccepted(),
		})
	}

	return AdminUserResponse{
		ID:                 user.ID,
		Email:              user.Email,
		FullName:           dbconv.NullText(user.FullName),
		IsActive:           user.IsActive,
		IsSuperuser:        user.IsSuperuser,
		IsVerified:         user.IsVerified,
		MustChangePassword: user.MustChangePassword,
		CreatedAt:          user.CreatedAt,
		LastLoginAt:        user.LastLoginAt,
		HasPassword:        user.HashedPassword != "",
		HasTOTP:            user.TOTPSecret != "",
		HasOIDC:            user.OIDCSubject != "",
		OIDCIssuer:         user.OIDCIssuer,
		PasskeyCount:       len(keys),
		Memberships:        placed,
	}, nil
}

// adminOneUser is the listing's shape for a single account, for the handlers
// that answer with the row they just wrote.
func adminOneUser(env *Env, r *http.Request, user store.User) (AdminUserResponse, error) {
	memberships, err := adminMemberships(env, r.Context())
	if err != nil {
		return AdminUserResponse{}, err
	}
	names, err := adminSpaceNames(env, r.Context())
	if err != nil {
		return AdminUserResponse{}, err
	}
	return adminUserResponse(env, r.Context(), user, memberships[user.ID], names)
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

type AdminSpaceResponse struct {
	ID              uuid.UUID                  `json:"id"`
	Name            string                     `json:"name"`
	PrimaryCurrency string                     `json:"primary_currency"`
	Timezone        string                     `json:"timezone"`
	CreatedAt       time.Time                  `json:"created_at"`
	Members         []AdminSpaceMemberResponse `json:"members"`
}

type AdminSpaceMemberResponse struct {
	MembershipID uuid.UUID  `json:"membership_id"`
	UserID       uuid.UUID  `json:"user_id"`
	Email        string     `json:"email"`
	FullName     *string    `json:"full_name"`
	Role         store.Role `json:"role"`
	Accepted     bool       `json:"accepted"`
}

func listAllSpaces(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	spaces, err := env.DB.ListSpaces(r.Context())
	if err != nil {
		return err
	}
	users, err := env.DB.ListUsers(r.Context())
	if err != nil {
		return err
	}
	byID := make(map[uuid.UUID]store.User, len(users))
	for _, user := range users {
		byID[user.ID] = user
	}
	memberships, err := env.DB.ListAllMemberships(r.Context())
	if err != nil {
		return err
	}
	bySpace := map[store.SpaceID][]store.Membership{}
	for _, membership := range memberships {
		bySpace[membership.SpaceID] = append(bySpace[membership.SpaceID], membership)
	}

	out := make([]AdminSpaceResponse, 0, len(spaces))
	for _, space := range spaces {
		members := make([]AdminSpaceMemberResponse, 0, len(bySpace[space.ID]))
		for _, membership := range bySpace[space.ID] {
			member, known := byID[membership.UserID]
			if !known {
				continue
			}
			members = append(members, AdminSpaceMemberResponse{
				MembershipID: membership.ID,
				UserID:       member.ID,
				Email:        member.Email,
				FullName:     dbconv.NullText(member.FullName),
				Role:         membership.Role,
				Accepted:     membership.IsAccepted(),
			})
		}
		out = append(out, AdminSpaceResponse{
			ID:              space.ID.UUID(),
			Name:            space.Name,
			PrimaryCurrency: space.PrimaryCurrency,
			Timezone:        space.Timezone,
			CreatedAt:       space.CreatedAt,
			Members:         members,
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

// --- Single sign-on -----------------------------------------------------------
//
// The provider is a stored setting, with the environment behind any value never
// saved. A save takes effect on the next login through auth.OIDC.Configure,
// which also drops the cached discovery document: a verifier built for the
// previous client id would fail every sign-in.

// oidcSettingSources names each value in AdminOIDCResponse the way the response
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
}

type AdminOIDCResponse struct {
	Enabled      bool   `json:"enabled"`
	ProviderName string `json:"provider_name"`
	DiscoveryURL string `json:"discovery_url"`
	ClientID     string `json:"client_id"`
	// HasClientSecret is the only thing said about the secret in either
	// direction. There is no field it could come back on.
	HasClientSecret      bool     `json:"has_client_secret"`
	Scopes               []string `json:"scopes"`
	AutoRegister         bool     `json:"auto_register"`
	RequireVerifiedEmail bool     `json:"require_verified_email"`
	LinkExistingEmail    bool     `json:"link_existing_email"`

	// Sources says where each value came from: "database" or "environment".
	Sources map[string]string `json:"sources"`
	// CallbackURL is what has to be registered at the provider, derived so it
	// cannot be mistyped.
	CallbackURL string `json:"callback_url"`
	// Configured mirrors what the login screen is told: enabled, with a client
	// id and a discovery URL.
	Configured bool `json:"configured"`
}

// AdminOIDCWrite is the whole form. An omitted or empty client secret keeps
// what is stored, so the secret never passes through a browser again.
type AdminOIDCWrite struct {
	Enabled              bool     `json:"enabled"`
	ProviderName         string   `json:"provider_name"`
	DiscoveryURL         string   `json:"discovery_url"`
	ClientID             string   `json:"client_id"`
	ClientSecret         string   `json:"client_secret"`
	Scopes               []string `json:"scopes"`
	AutoRegister         bool     `json:"auto_register"`
	RequireVerifiedEmail bool     `json:"require_verified_email"`
	LinkExistingEmail    bool     `json:"link_existing_email"`
}

type AdminOIDCTest struct {
	DiscoveryURL string `json:"discovery_url"`
}

// AdminOIDCTestResponse is what the provider's metadata says, or why it could
// not be read. A failure is 200 with Valid false, so the provider's own
// complaint reaches the screen rather than a generic toast.
type AdminOIDCTestResponse struct {
	Valid   bool   `json:"valid"`
	Issuer  string `json:"issuer"`
	Message string `json:"message"`

	AuthorizationEndpoint string `json:"authorization_endpoint"`
	TokenEndpoint         string `json:"token_endpoint"`
	UserInfoEndpoint      string `json:"userinfo_endpoint"`
	JWKSURI               string `json:"jwks_uri"`
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
		}
		sources[oidcSettingSources[key]] = "database"
	}
	return settings, sources, nil
}

func readOIDCSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	settings, sources, err := effectiveOIDC(r.Context(), env)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, oidcSettingsResponse(env, settings, sources))
}

func saveOIDCSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminOIDCWrite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	discovery := strings.TrimSpace(body.DiscoveryURL)
	clientID := strings.TrimSpace(body.ClientID)
	// An enabled provider with no discovery URL would put a login button on
	// screen that 404s.
	if body.Enabled && (discovery == "" || clientID == "") {
		return errInvalid("missing", []string{"body", "discovery_url"},
			"turning single sign-on on needs a discovery URL and a client id")
	}

	values := map[string]string{
		store.OIDCEnabledSetting:             strconv.FormatBool(body.Enabled),
		store.OIDCProviderNameSetting:        strings.TrimSpace(body.ProviderName),
		store.OIDCDiscoveryURLSetting:        discovery,
		store.OIDCClientIDSetting:            clientID,
		store.OIDCScopesSetting:              strings.Join(body.Scopes, " "),
		store.OIDCAutoRegisterSetting:        strconv.FormatBool(body.AutoRegister),
		store.OIDCRequireVerifiedMailSetting: strconv.FormatBool(body.RequireVerifiedEmail),
		store.OIDCLinkExistingEmailSetting:   strconv.FormatBool(body.LinkExistingEmail),
	}
	// Blank keeps what is stored: the stored value never comes back to the
	// browser, so the field is blank on every visit.
	if secret := strings.TrimSpace(body.ClientSecret); secret != "" {
		values[store.OIDCClientSecretSetting] = secret
	}

	db, err := settingsStore(env.Cfg, env.DB)
	if err != nil {
		return err
	}
	if err := db.SetServerSettings(r.Context(), store.OIDCSettingKeys, values); err != nil {
		return err
	}

	settings, sources, err := effectiveOIDC(r.Context(), env)
	if err != nil {
		return err
	}
	// Live, before the response goes out: the screen's next act is often to
	// open a private window and try the sign-in button.
	env.OIDC.Configure(settings)
	return writeJSON(w, http.StatusOK, oidcSettingsResponse(env, settings, sources))
}

func testOIDCSettings(env *Env, w http.ResponseWriter, r *http.Request, _ store.User) error {
	var body AdminOIDCTest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	discovery := strings.TrimSpace(body.DiscoveryURL)
	if discovery == "" {
		settings, _, err := effectiveOIDC(r.Context(), env)
		if err != nil {
			return err
		}
		discovery = settings.DiscoveryURL
	}
	if discovery == "" {
		return errInvalid("missing", []string{"body", "discovery_url"},
			"there is no discovery URL to test")
	}

	document, err := env.OIDC.ProbeDiscovery(r.Context(), discovery)
	if err != nil {
		return writeJSON(w, http.StatusOK, AdminOIDCTestResponse{
			Valid: false, Issuer: document.Issuer, Message: err.Error(),
		})
	}
	return writeJSON(w, http.StatusOK, AdminOIDCTestResponse{
		Valid:                 true,
		Issuer:                document.Issuer,
		Message:               "The provider answered.",
		AuthorizationEndpoint: document.AuthorizationEndpoint,
		TokenEndpoint:         document.TokenEndpoint,
		UserInfoEndpoint:      document.UserInfoEndpoint,
		JWKSURI:               document.JWKSURI,
	})
}

func oidcSettingsResponse(
	env *Env, settings auth.OIDCSettings, sources map[string]string,
) AdminOIDCResponse {
	scopes := settings.Scopes
	if scopes == nil {
		scopes = []string{}
	}
	return AdminOIDCResponse{
		Enabled:              settings.Enabled,
		ProviderName:         settings.ProviderName,
		DiscoveryURL:         settings.DiscoveryURL,
		ClientID:             settings.ClientID,
		HasClientSecret:      settings.ClientSecret != "",
		Scopes:               scopes,
		AutoRegister:         settings.AutoRegister,
		RequireVerifiedEmail: settings.RequireVerifiedEmail,
		LinkExistingEmail:    settings.LinkExistingEmail,
		Sources:              sources,
		CallbackURL:          env.OIDC.CallbackURI(),
		Configured:           settings.IsConfigured(),
	}
}
