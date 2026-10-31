package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/backup"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// HouseholdService: which spaces the caller can see, who else is in them, and
// who may change that. This answers from the caller's memberships, before a
// client can send X-Space-Id.
//
// The membership methods name their space in the request, so they are USER
// methods rather than WRITE ones; rights are checked through callerMembership,
// the only way to get one.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewHouseholdServiceHandler(householdService{env}, opts...)
	})
}

type householdService struct{ env *Env }

// ListSpaces is every space the caller has actually joined, with their role
// in each. An outstanding invitation grants nothing and is not listed.
func (s householdService) ListSpaces(ctx context.Context, _ *agentifiv1.ListSpacesRequest) (*agentifiv1.ListSpacesResponse, error) {
	user := userFrom(ctx)
	spaces, err := s.env.DB.ListSpacesForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListSpacesResponse{Spaces: make([]*agentifiv1.Space, 0, len(spaces))}
	for _, space := range spaces {
		membership, err := s.env.DB.GetMembership(ctx, space.ID, user.ID)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return nil, err
		}
		out.Spaces = append(out.Spaces, spaceProto(space, membership))
	}
	return out, nil
}

// GetCurrentSpace is the space X-Space-Id selected, or the default it falls
// back to, so the client never has to guess.
func (s householdService) GetCurrentSpace(ctx context.Context, _ *agentifiv1.GetCurrentSpaceRequest) (*agentifiv1.GetCurrentSpaceResponse, error) {
	sp := spaceFrom(ctx)
	return &agentifiv1.GetCurrentSpaceResponse{Space: spaceProto(sp.Space, sp.Membership)}, nil
}

// CreateSpace opens a second set of finances, owned by whoever asked for it.
func (s householdService) CreateSpace(ctx context.Context, req *agentifiv1.CreateSpaceRequest) (*agentifiv1.CreateSpaceResponse, error) {
	user := userFrom(ctx)
	name := strings.TrimSpace(req.GetName())
	if name == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}

	now := s.env.now().UTC()
	space := &store.Space{Name: name, PrimaryCurrency: s.env.Cfg.PrimaryCurrency}
	membership := &store.Membership{
		UserID: user.ID, Role: store.RoleOwner, InvitedAt: &now, AcceptedAt: &now,
	}
	// Provision the separate household file first; compensate if its owner
	// membership cannot be stored. This is recoverable, not cross-file atomic.
	err := s.env.DB.CreateSeededSpace(ctx, space)
	if err == nil {
		err = s.env.DB.CreateMembership(ctx, space.ID, membership)
		if err != nil {
			err = errors.Join(err, s.env.DB.DeleteSpace(context.WithoutCancel(ctx), space.ID))
		}
	}
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateSpaceResponse{Space: spaceProto(*space, *membership)}, nil
}

// ListInvitations is what the caller has been asked to join and has not
// taken: not spaces they have, so not in ListSpaces.
func (s householdService) ListInvitations(ctx context.Context, _ *agentifiv1.ListInvitationsRequest) (*agentifiv1.ListInvitationsResponse, error) {
	invitations, err := s.env.DB.ListPendingInvitations(ctx, userFrom(ctx).ID)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListInvitationsResponse{Invitations: make([]*agentifiv1.Invitation, 0, len(invitations))}
	for _, invitation := range invitations {
		out.Invitations = append(out.Invitations, &agentifiv1.Invitation{
			Id:        invitation.Membership.ID.String(),
			SpaceId:   invitation.Membership.SpaceID.UUID().String(),
			SpaceName: invitation.SpaceName,
			Role:      roleProto(invitation.Membership.Role),
			InvitedAt: timestampProto(invitation.Membership.InvitedAt),
		})
	}
	return out, nil
}

// AcceptInvitation takes an invitation. The store scopes the write to the
// caller's own user id, so someone else's invitation is not found. The space
// comes back whole for the client to switch to.
func (s householdService) AcceptInvitation(ctx context.Context, req *agentifiv1.AcceptInvitationRequest) (*agentifiv1.AcceptInvitationResponse, error) {
	id, err := idFrom(req.GetMembershipId(), "Invitation")
	if err != nil {
		return nil, err
	}
	membership, err := s.env.DB.AcceptInvitation(ctx, id, userFrom(ctx).ID, s.env.now().UTC())
	if err != nil {
		return nil, notFoundAs(err, "Invitation")
	}
	space, err := s.env.DB.GetSpace(ctx, membership.SpaceID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.AcceptInvitationResponse{Space: spaceProto(space, membership)}, nil
}

// DeclineInvitation drops one, which changes nothing about what the caller
// can see: the invitation granted nothing to begin with.
func (s householdService) DeclineInvitation(ctx context.Context, req *agentifiv1.DeclineInvitationRequest) (*agentifiv1.DeclineInvitationResponse, error) {
	id, err := idFrom(req.GetMembershipId(), "Invitation")
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeclineInvitation(ctx, id, userFrom(ctx).ID); err != nil {
		return nil, notFoundAs(err, "Invitation")
	}
	return &agentifiv1.DeclineInvitationResponse{}, nil
}

func (s householdService) RenameSpace(ctx context.Context, req *agentifiv1.RenameSpaceRequest) (*agentifiv1.RenameSpaceResponse, error) {
	spaceID, caller, err := callerMembership(ctx, s.env, req.GetSpaceId(), userFrom(ctx))
	if err != nil {
		return nil, err
	}
	if err := requireOwner(caller); err != nil {
		return nil, err
	}
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}

	space, err := s.env.DB.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, notFoundAs(err, "Space")
	}
	// Deleted is gone: ListSpacesForUser already hides it, and a rename that
	// lands on one succeeds without anybody ever seeing the new name.
	if space.IsDeleted {
		return nil, errNotFound("Space")
	}
	if err := applyRequired("name", optOf(mask, "name", req.Name), &space.Name); err != nil {
		return nil, err
	}
	space.Name = strings.TrimSpace(space.Name)
	if space.Name == "" {
		return nil, errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := s.env.DB.UpdateSpace(ctx, &space); err != nil {
		return nil, err
	}
	return &agentifiv1.RenameSpaceResponse{Space: spaceProto(space, caller)}, nil
}

// DeleteSpace removes a space and everything in it, for every member. Only an
// owner may, never an admin, and only with the space's name typed out. The
// caller keeps at least one space, so the app always has one to open on.
// Where the server writes backups, a set is taken first and a failed one
// deletes nothing.
func (s householdService) DeleteSpace(ctx context.Context, req *agentifiv1.DeleteSpaceRequest) (*agentifiv1.DeleteSpaceResponse, error) {
	user := userFrom(ctx)
	spaceID, caller, err := callerMembership(ctx, s.env, req.GetSpaceId(), user)
	if err != nil {
		return nil, err
	}
	if !caller.Role.Owns() {
		return nil, auth.ErrReadOnly
	}
	space, err := s.env.DB.GetSpace(ctx, spaceID)
	if err != nil {
		return nil, notFoundAs(err, "Space")
	}
	if strings.TrimSpace(req.GetConfirmName()) != space.Name {
		return nil, errInvalid("value", []string{"body", "confirm_name"},
			"type the space's name exactly to delete it")
	}
	joined, err := s.env.DB.ListSpacesForUser(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	if len(joined) < 2 {
		return nil, errConflictCode("last_space",
			"This is your only space. Create another before deleting this one.")
	}

	out := &agentifiv1.DeleteSpaceResponse{}
	if backups := s.env.ServerBackups(); backups.Enabled() {
		set, err := backups.Take(ctx, backup.TriggerSpaceDelete)
		switch {
		case errors.Is(err, service.ErrBackupRunning):
			return nil, errConflict("A backup is running. Try again when it has finished.")
		case err != nil:
			slog.Warn("the backup before deleting a space failed", "space", spaceID, "error", err)
			return nil, errConflict("The backup taken before deleting failed, so nothing was deleted. " +
				"The server log says why.")
		}
		out.BackupSet = &set.Name
	}

	if err := service.DeleteSpace(
		ctx, s.env.DB, s.env.Storage, billsService(s.env), spaceID); err != nil {
		return nil, notFoundAs(err, "Space")
	}
	return out, nil
}

// UpdatePreferences sets how this space is displayed. A cleared preference
// goes back to its default.
func (s householdService) UpdatePreferences(ctx context.Context, req *agentifiv1.UpdatePreferencesRequest) (*agentifiv1.UpdatePreferencesResponse, error) {
	sp := spaceFrom(ctx)
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}

	space := sp.Space
	primary, err := s.env.DB.HouseholdCurrency(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	space.PrimaryCurrency = primary
	switch dateRange := optOf(mask, "default_date_range", req.DefaultDateRange); {
	case dateRange.Cleared():
		space.DefaultDateRange = ""
	case dateRange.Present():
		preset := strings.TrimSpace(dateRange.Value)
		if preset != "" && !isRangePreset(preset) {
			return nil, errInvalid("enum", []string{"body", "default_date_range"},
				"%q is not a range preset", preset)
		}
		space.DefaultDateRange = preset
	}
	switch {
	case mask["sidebar_account_types"] && req.SidebarAccountTypes == nil:
		space.SidebarAccountTypes = nil
	case mask["sidebar_account_types"]:
		// A sent-but-empty list is a real answer: the user unticked every
		// type. It stays non-nil so it does not read as "never chosen".
		types := req.SidebarAccountTypes.GetIds()
		if types == nil {
			types = []string{}
		}
		space.SidebarAccountTypes = types
	}

	// The currency every other figure is reported in, so it is the one
	// preference that is not just a display choice.
	restamp := false
	if currency := optOf(mask, "primary_currency", req.PrimaryCurrency); currency.Present() {
		code := strings.ToUpper(strings.TrimSpace(currency.Value))
		if len(code) != 3 {
			return nil, errInvalid("format", []string{"body", "primary_currency"},
				"a currency is a three-letter ISO 4217 code")
		}
		if !isSupportedCurrency(s.env.Cfg.SupportedCurrencies, code) {
			return nil, errInvalid("enum", []string{"body", "primary_currency"},
				"%s is not one of the currencies this deployment quotes rates for", code)
		}
		restamp = code != space.PrimaryCurrency
		space.PrimaryCurrency = code
	}

	// Household data and its authoritative marker commit together. Server
	// preferences/mirror follow; a stale mirror is recoverable on scope.
	if restamp {
		if err := NewCurrency(s.env.Cfg, s.env.DB).SwitchPrimaryCurrency(ctx, sp.ID(), space.PrimaryCurrency); err != nil {
			var rescale *store.CurrencyRescaleError
			if errors.As(err, &rescale) {
				return nil, errConflict("%s", rescale.Error())
			}
			return nil, err
		}
	}
	if err := s.env.DB.UpdateSpace(ctx, &space); err != nil {
		return nil, err
	}
	return &agentifiv1.UpdatePreferencesResponse{Space: spaceProto(space, sp.Membership)}, nil
}

// rangePresets is the client's RANGE_PRESETS vocabulary. A preset the client
// does not know renders as no selection, so the lists must agree.
var rangePresets = []string{"1M", "3M", "6M", "1Y", "5Y", "QTD", "YTD", "ALL"}

func isRangePreset(preset string) bool {
	return slices.Contains(rangePresets, preset)
}

// isSupportedCurrency holds the reporting currency to the list the deployment
// fetches rates for; outside it, every foreign row would stay unconverted. An
// unset list is no restriction.
func isSupportedCurrency(supported []string, currency string) bool {
	if len(supported) == 0 {
		return true
	}
	return slices.ContainsFunc(supported, func(one string) bool {
		return strings.EqualFold(strings.TrimSpace(one), currency)
	})
}

// ListMembers is everyone in one space, invitations included.
func (s householdService) ListMembers(ctx context.Context, req *agentifiv1.ListMembersRequest) (*agentifiv1.ListMembersResponse, error) {
	spaceID, _, err := callerMembership(ctx, s.env, req.GetSpaceId(), userFrom(ctx))
	if err != nil {
		return nil, err
	}
	memberships, err := s.env.DB.ListMemberships(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	out := &agentifiv1.ListMembersResponse{Members: make([]*agentifiv1.Member, 0, len(memberships))}
	for _, membership := range memberships {
		member, err := s.env.DB.GetUser(ctx, membership.UserID)
		if err != nil {
			return nil, err
		}
		out.Members = append(out.Members, memberProto(membership, member))
	}
	return out, nil
}

// InviteMember invites an existing account into the space, unaccepted
// (invited_at, no accepted_at), which grants nothing until taken. Only the
// server administrator makes logins, so owning a space is not a way to mint
// accounts. An address with no account gets one fixed refusal that does not
// echo it back.
func (s householdService) InviteMember(ctx context.Context, req *agentifiv1.InviteMemberRequest) (*agentifiv1.InviteMemberResponse, error) {
	spaceID, caller, err := callerMembership(ctx, s.env, req.GetSpaceId(), userFrom(ctx))
	if err != nil {
		return nil, err
	}
	if err := requireOwner(caller); err != nil {
		return nil, err
	}
	email := strings.TrimSpace(req.GetEmail())
	if email == "" {
		return nil, errInvalid("missing", []string{"body", "email"}, "email is required")
	}
	role, err := parseRole(roleFrom(req.GetRole()))
	if err != nil {
		return nil, err
	}
	if err := requireOwnerToTouchAnOwner(caller, role); err != nil {
		return nil, err
	}

	invitee, err := s.env.DB.GetUserByEmail(ctx, email)
	if err != nil {
		if isNotFound(err) {
			return nil, errConflict(
				"No account uses that email. Ask the server administrator to create one.")
		}
		return nil, err
	}

	existing, err := s.env.DB.GetMembership(ctx, spaceID, invitee.ID)
	switch {
	case err == nil && existing.IsAccepted():
		return nil, errConflict("%s is already in this space", invitee.Email)
	case err == nil:
		return nil, errConflict("%s has already been invited", invitee.Email)
	case !isNotFound(err):
		return nil, err
	}

	invited := s.env.now().UTC()
	membership := &store.Membership{UserID: invitee.ID, Role: role, InvitedAt: &invited}
	if err := s.env.DB.CreateMembership(ctx, spaceID, membership); err != nil {
		return nil, err
	}
	return &agentifiv1.InviteMemberResponse{Member: memberProto(*membership, invitee)}, nil
}

func (s householdService) UpdateMember(ctx context.Context, req *agentifiv1.UpdateMemberRequest) (*agentifiv1.UpdateMemberResponse, error) {
	spaceID, caller, err := callerMembership(ctx, s.env, req.GetSpaceId(), userFrom(ctx))
	if err != nil {
		return nil, err
	}
	if err := requireOwner(caller); err != nil {
		return nil, err
	}
	target, err := targetMembership(ctx, s.env, spaceID, req.GetMembershipId())
	if err != nil {
		return nil, err
	}
	role, err := parseRole(roleFrom(req.GetRole()))
	if err != nil {
		return nil, err
	}
	if err := requireOwnerToTouchAnOwner(caller, target.Role, role); err != nil {
		return nil, err
	}
	if !role.Owns() {
		if err := ensureAnOwnerRemains(ctx, s.env, spaceID, target); err != nil {
			return nil, err
		}
	}

	target.Role = role
	if err := s.env.DB.UpdateMembership(ctx, spaceID, &target); err != nil {
		return nil, err
	}
	member, err := s.env.DB.GetUser(ctx, target.UserID)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateMemberResponse{Member: memberProto(target, member)}, nil
}

// RemoveMember revokes somebody else's membership, or gives up the caller's
// own. Leaving is not an owner's privilege; taking somebody else out is.
func (s householdService) RemoveMember(ctx context.Context, req *agentifiv1.RemoveMemberRequest) (*agentifiv1.RemoveMemberResponse, error) {
	user := userFrom(ctx)
	spaceID, caller, err := callerMembership(ctx, s.env, req.GetSpaceId(), user)
	if err != nil {
		return nil, err
	}
	target, err := targetMembership(ctx, s.env, spaceID, req.GetMembershipId())
	if err != nil {
		return nil, err
	}
	if target.UserID != user.ID {
		if err := requireOwner(caller); err != nil {
			return nil, err
		}
		if err := requireOwnerToTouchAnOwner(caller, target.Role); err != nil {
			return nil, err
		}
	}
	if err := ensureAnOwnerRemains(ctx, s.env, spaceID, target); err != nil {
		return nil, err
	}
	if err := s.env.DB.DeleteMembership(ctx, spaceID, target.ID); err != nil {
		return nil, notFoundAs(err, "Member")
	}
	return &agentifiv1.RemoveMemberResponse{}, nil
}

// callerMembership is the caller's own accepted membership in the space the
// request names, and the only place that id is trusted for anything. A space
// the caller is not in, an unparseable id and an outstanding invitation are
// all not found: a refusal would confirm the space exists.
func callerMembership(ctx context.Context, env *Env, rawSpaceID string, user store.User) (store.SpaceID, store.Membership, error) {
	var none store.SpaceID
	id, err := idFrom(rawSpaceID, "Space")
	if err != nil {
		return none, store.Membership{}, err
	}
	spaceID := store.SpaceIDOf(id)

	membership, err := env.DB.GetMembership(ctx, spaceID, user.ID)
	if err != nil {
		return none, store.Membership{}, notFoundAs(err, "Space")
	}
	if !membership.IsAccepted() {
		return none, store.Membership{}, errNotFound("Space")
	}
	return spaceID, membership, nil
}

// targetMembership is the membership the request names, which has to be one
// of this space's own: an id from another space is not found like any other
// row.
func targetMembership(ctx context.Context, env *Env, spaceID store.SpaceID, rawID string) (store.Membership, error) {
	id, err := idFrom(rawID, "Member")
	if err != nil {
		return store.Membership{}, err
	}
	memberships, err := env.DB.ListMemberships(ctx, spaceID)
	if err != nil {
		return store.Membership{}, err
	}
	for _, membership := range memberships {
		if membership.ID == id {
			return membership, nil
		}
	}
	return store.Membership{}, errNotFound("Member")
}

// requireOwner is auth.SpaceContext.RequireOwner's rule for methods that
// name their space in the request.
func requireOwner(membership store.Membership) error {
	if !membership.Role.IsOwner() {
		return auth.ErrReadOnly
	}
	return nil
}

// requireOwnerToTouchAnOwner keeps an admin out of the owner's membership:
// otherwise an admin could demote or remove the owner, or promote themselves,
// and still satisfy ensureAnOwnerRemains. The roles passed are whichever the
// act touches.
func requireOwnerToTouchAnOwner(caller store.Membership, roles ...store.Role) error {
	if caller.Role.Owns() {
		return nil
	}
	for _, role := range roles {
		if role.Owns() {
			return auth.ErrReadOnly
		}
	}
	return nil
}

// ensureAnOwnerRemains refuses a change that would leave a space with no
// accepted owner: only an owner hands out the owner role, so the space would be
// unrecoverable. Admins and outstanding invitations do not count.
func ensureAnOwnerRemains(ctx context.Context, env *Env, spaceID store.SpaceID, target store.Membership) error {
	if !target.IsAccepted() || !target.Role.Owns() {
		return nil
	}
	memberships, err := env.DB.ListMemberships(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, membership := range memberships {
		if membership.ID == target.ID {
			continue
		}
		if membership.IsAccepted() && membership.Role.Owns() {
			return nil
		}
	}
	return errConflict("A space needs an owner. Make somebody else an owner first.")
}

func parseRole(role store.Role) (store.Role, error) {
	switch role {
	case store.RoleOwner, store.RoleAdmin, store.RoleMember, store.RoleViewer:
		return role, nil
	case "":
		return "", errInvalid("missing", []string{"body", "role"}, "role is required")
	}
	return "", errInvalid("enum", []string{"body", "role"},
		"role must be one of owner, admin, member, viewer")
}

func memberProto(membership store.Membership, member store.User) *agentifiv1.Member {
	return &agentifiv1.Member{
		Id:         membership.ID.String(),
		UserId:     membership.UserID.String(),
		Email:      member.Email,
		FullName:   store.NullText(member.FullName),
		Role:       roleProto(membership.Role),
		InvitedAt:  timestampProto(membership.InvitedAt),
		AcceptedAt: timestampProto(membership.AcceptedAt),
	}
}

func spaceProto(space store.Space, membership store.Membership) *agentifiv1.Space {
	out := &agentifiv1.Space{
		Id:               space.ID.UUID().String(),
		Name:             space.Name,
		PrimaryCurrency:  space.PrimaryCurrency,
		Timezone:         space.Timezone,
		DefaultDateRange: space.DefaultDateRange,
		Role:             roleProto(membership.Role),
		CanWrite:         membership.Role.CanWrite(),
		IsOwner:          membership.Role.IsOwner(),
		JoinedAt:         timestampProto(membership.AcceptedAt),
	}
	if space.SidebarAccountTypes != nil {
		out.SidebarAccountTypes = &agentifiv1.IdSet{Ids: space.SidebarAccountTypes}
	}
	return out
}

var roles = map[store.Role]agentifiv1.Role{
	store.RoleOwner:  agentifiv1.Role_ROLE_OWNER,
	store.RoleAdmin:  agentifiv1.Role_ROLE_ADMIN,
	store.RoleMember: agentifiv1.Role_ROLE_MEMBER,
	store.RoleViewer: agentifiv1.Role_ROLE_VIEWER,
}

func roleProto(role store.Role) agentifiv1.Role { return roles[role] }

// roleFrom is the role a request sent, or "" for none, which parseRole
// refuses as missing.
func roleFrom(role agentifiv1.Role) store.Role {
	for stored, wire := range roles {
		if wire == role {
			return stored
		}
	}
	return ""
}

// --- The dashboard arrangement -----------------------------------------------
//
// Stored on the membership: one person's arrangement of one space. The layout
// is opaque here, since the widgets are the client's business; only something
// that is not a list is refused.

func (s householdService) GetDashboardLayout(ctx context.Context, _ *agentifiv1.GetDashboardLayoutRequest) (*agentifiv1.GetDashboardLayoutResponse, error) {
	sp := spaceFrom(ctx)
	membership, err := s.env.DB.GetMembership(ctx, sp.ID(), sp.Membership.UserID)
	if err != nil {
		return nil, err
	}
	layout, err := layoutProto(membership.DashboardLayout)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetDashboardLayoutResponse{Layout: layout}, nil
}

func (s householdService) SaveDashboardLayout(ctx context.Context, req *agentifiv1.SaveDashboardLayoutRequest) (*agentifiv1.SaveDashboardLayoutResponse, error) {
	sp := spaceFrom(ctx)
	// Unset or null clears it, which is how a client says "back to the
	// built-in order".
	var stored []byte
	switch kind := req.GetLayout().GetKind().(type) {
	case nil, *structpb.Value_NullValue:
	case *structpb.Value_ListValue:
		encoded, err := json.Marshal(kind.ListValue.AsSlice())
		if err != nil {
			return nil, err
		}
		stored = encoded
	default:
		return nil, errInvalid("type", []string{"body", "layout"}, "layout must be a list")
	}
	if err := s.env.DB.SetDashboardLayout(ctx, sp.ID(), sp.Membership.UserID, stored); err != nil {
		return nil, err
	}
	layout, err := layoutProto(stored)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.SaveDashboardLayoutResponse{Layout: layout}, nil
}

// layoutProto is a stored arrangement, or unset when there is none.
func layoutProto(stored []byte) (*structpb.Value, error) {
	if len(stored) == 0 {
		return nil, nil
	}
	var list []any
	if err := json.Unmarshal(stored, &list); err != nil {
		return nil, err
	}
	return structpb.NewValue(list)
}
