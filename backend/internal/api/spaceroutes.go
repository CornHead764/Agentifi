package api

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Which spaces the caller can see, who else is in them, and who may change
// that. This answers from the caller's memberships, before a client can send
// X-Space-Id.
//
// The membership methods name their space in the request, so they are USER
// methods rather than WRITE ones; rights are checked through callerMembership,
// the only way to get one.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewSpaceServiceHandler(spaceService{env}, opts...)
	})
}

type spaceService struct{ env *Env }

// ListSpaces is every space the caller has actually joined, with their role in
// each. An outstanding invitation grants nothing and is not listed.
func (s spaceService) ListSpaces(ctx context.Context, _ *agentifiv1.ListSpacesRequest) (*agentifiv1.ListSpacesResponse, error) {
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
func (s spaceService) GetCurrentSpace(ctx context.Context, _ *agentifiv1.GetCurrentSpaceRequest) (*agentifiv1.GetCurrentSpaceResponse, error) {
	sp := spaceFrom(ctx)
	space, err := currentSpace(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.GetCurrentSpaceResponse{Space: spaceProto(space, sp.Membership)}, nil
}

// currentSpace is the resolved space read whole: the resolver reads only what
// tenancy needs, so its copy has no display preferences, and saving it back
// would clear them.
func currentSpace(ctx context.Context, env *Env, sp auth.SpaceContext) (store.Space, error) {
	return env.DB.GetSpace(ctx, sp.ID())
}

// CreateSpace opens a second set of finances, owned by whoever asked for it.
func (s spaceService) CreateSpace(ctx context.Context, req *agentifiv1.CreateSpaceRequest) (*agentifiv1.CreateSpaceResponse, error) {
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
	// One write, because a space whose creator never got a membership is a
	// space nobody can reach and nobody can delete.
	err := s.env.DB.InTx(ctx, func(tx *store.Store) error {
		if err := tx.CreateSeededSpace(ctx, space); err != nil {
			return err
		}
		return tx.CreateMembership(ctx, space.ID, membership)
	})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.CreateSpaceResponse{Space: spaceProto(*space, *membership)}, nil
}

// ListInvitations is what the caller has been asked to join and has not taken:
// not spaces they have, so not in ListSpaces.
func (s spaceService) ListInvitations(ctx context.Context, _ *agentifiv1.ListInvitationsRequest) (*agentifiv1.ListInvitationsResponse, error) {
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
			Role:      string(invitation.Membership.Role),
			InvitedAt: pbTimeOrNil(invitation.Membership.InvitedAt),
		})
	}
	return out, nil
}

// AcceptInvitation takes an invitation. The store scopes the write to the
// caller's own user id, so someone else's invitation is a 404. The space comes
// back whole for the client to switch to.
func (s spaceService) AcceptInvitation(ctx context.Context, req *agentifiv1.AcceptInvitationRequest) (*agentifiv1.AcceptInvitationResponse, error) {
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

// DeclineInvitation drops one, which changes nothing about what the caller can
// see — the invitation granted nothing to begin with.
func (s spaceService) DeclineInvitation(ctx context.Context, req *agentifiv1.DeclineInvitationRequest) (*agentifiv1.DeclineInvitationResponse, error) {
	id, err := idFrom(req.GetMembershipId(), "Invitation")
	if err != nil {
		return nil, err
	}
	if err := s.env.DB.DeclineInvitation(ctx, id, userFrom(ctx).ID); err != nil {
		return nil, notFoundAs(err, "Invitation")
	}
	return &agentifiv1.DeclineInvitationResponse{}, nil
}

func (s spaceService) UpdateSpace(ctx context.Context, req *agentifiv1.UpdateSpaceRequest) (*agentifiv1.UpdateSpaceResponse, error) {
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
	return &agentifiv1.UpdateSpaceResponse{Space: spaceProto(space, caller)}, nil
}

// DeleteSpace removes a space and everything in it, for every member. Only an
// owner may, never an admin, and only with the space's name typed out. The
// caller keeps at least one space, so the app always has one to open on.
// Where the server writes backups, a set is taken first and a failed one
// deletes nothing.
func (s spaceService) DeleteSpace(ctx context.Context, req *agentifiv1.DeleteSpaceRequest) (*agentifiv1.DeleteSpaceResponse, error) {
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

// UpdateSpacePreferences sets how this space is displayed. Clearing either
// display preference puts it back to its default.
func (s spaceService) UpdateSpacePreferences(ctx context.Context, req *agentifiv1.UpdateSpacePreferencesRequest) (*agentifiv1.UpdateSpacePreferencesResponse, error) {
	sp := spaceFrom(ctx)
	mask, err := maskOf(req)
	if err != nil {
		return nil, err
	}

	space, err := currentSpace(ctx, s.env, sp)
	if err != nil {
		return nil, err
	}
	dateRange := optOf(mask, "default_date_range", req.DefaultDateRange)
	switch {
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
	if mask["sidebar_account_types"] {
		// A sent-but-empty list is a real answer: the user unticked every
		// type. It stays non-nil so it does not read as "never chosen".
		types, err := stringsFrom(req.GetSidebarAccountTypes(), "body", "sidebar_account_types")
		if err != nil {
			return nil, err
		}
		space.SidebarAccountTypes = types
	}

	// The currency every other figure is reported in, so it is the one
	// preference that is not just a display choice.
	restamp := false
	if currencyOpt := optOf(mask, "primary_currency", req.PrimaryCurrency); currencyOpt.Present() {
		currency := strings.ToUpper(strings.TrimSpace(currencyOpt.Value))
		if len(currency) != 3 {
			return nil, errInvalid("format", []string{"body", "primary_currency"},
				"a currency is a three-letter ISO 4217 code")
		}
		if !isSupportedCurrency(s.env.Cfg.SupportedCurrencies, currency) {
			return nil, errInvalid("enum", []string{"body", "primary_currency"},
				"%s is not one of the currencies this deployment quotes rates for", currency)
		}
		restamp = currency != space.PrimaryCurrency
		space.PrimaryCurrency = currency
	}

	// One transaction: stored conversions were made against the old currency,
	// and the stamping pass only fills nulls, so a failure between clear and
	// restamp would leave the ledger counted at face value. StampSpace reads
	// the space's currency, so it must run inside the transaction that set it.
	err = s.env.DB.InTx(ctx, func(tx *store.Store) error {
		if err := tx.UpdateSpace(ctx, &space); err != nil {
			return err
		}
		if !restamp {
			return nil
		}
		if _, err := tx.ClearConversions(ctx, sp.ID()); err != nil {
			return err
		}
		_, err := NewCurrency(s.env.Cfg, tx).StampSpace(ctx, sp.ID())
		return err
	})
	if err != nil {
		return nil, err
	}
	return &agentifiv1.UpdateSpacePreferencesResponse{Space: spaceProto(space, sp.Membership)}, nil
}

// stringsFrom reads a list of strings sent as a ListValue: nil when unset, and
// non-nil, perhaps empty, when sent.
func stringsFrom(list *structpb.ListValue, loc ...string) ([]string, error) {
	if list == nil {
		return nil, nil
	}
	out := make([]string, 0, len(list.GetValues()))
	for _, value := range list.GetValues() {
		text, ok := value.GetKind().(*structpb.Value_StringValue)
		if !ok {
			return nil, errInvalid("type_error", loc, "%s must be a list of strings", loc[len(loc)-1])
		}
		out = append(out, text.StringValue)
	}
	return out, nil
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
func (s spaceService) ListMembers(ctx context.Context, req *agentifiv1.ListMembersRequest) (*agentifiv1.ListMembersResponse, error) {
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
func (s spaceService) InviteMember(ctx context.Context, req *agentifiv1.InviteMemberRequest) (*agentifiv1.InviteMemberResponse, error) {
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
	role, err := parseRole(store.Role(req.GetRole()))
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

func (s spaceService) UpdateMember(ctx context.Context, req *agentifiv1.UpdateMemberRequest) (*agentifiv1.UpdateMemberResponse, error) {
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
	role, err := parseRole(store.Role(req.GetRole()))
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
func (s spaceService) RemoveMember(ctx context.Context, req *agentifiv1.RemoveMemberRequest) (*agentifiv1.RemoveMemberResponse, error) {
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
// the caller is not in, an unparseable id and an outstanding invitation are all
// 404: a 403 would confirm the space exists.
func callerMembership(
	ctx context.Context, env *Env, rawSpaceID string, user store.User,
) (store.SpaceID, store.Membership, error) {
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

// targetMembership is the membership the request names, which has to be one of
// this space's own: an id from another space is a 404 like any other row.
func targetMembership(
	ctx context.Context, env *Env, spaceID store.SpaceID, rawID string,
) (store.Membership, error) {
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
func ensureAnOwnerRemains(
	ctx context.Context, env *Env, spaceID store.SpaceID, target store.Membership,
) error {
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
		FullName:   dbconv.NullText(member.FullName),
		Role:       string(membership.Role),
		InvitedAt:  pbTimeOrNil(membership.InvitedAt),
		AcceptedAt: pbTimeOrNil(membership.AcceptedAt),
	}
}

func spaceProto(space store.Space, membership store.Membership) *agentifiv1.Space {
	out := &agentifiv1.Space{
		Id:               space.ID.UUID().String(),
		Name:             space.Name,
		PrimaryCurrency:  space.PrimaryCurrency,
		Timezone:         space.Timezone,
		DefaultDateRange: space.DefaultDateRange,
		Role:             string(membership.Role),
		CanWrite:         membership.Role.CanWrite(),
		IsOwner:          membership.Role.IsOwner(),
		JoinedAt:         pbTimeOrNil(membership.AcceptedAt),
	}
	// Nil lists every type, which is not the same answer as an empty list.
	if space.SidebarAccountTypes != nil {
		out.SidebarAccountTypes = &structpb.ListValue{
			Values: make([]*structpb.Value, 0, len(space.SidebarAccountTypes)),
		}
		for _, kind := range space.SidebarAccountTypes {
			out.SidebarAccountTypes.Values = append(out.SidebarAccountTypes.Values, structpb.NewStringValue(kind))
		}
	}
	return out
}

// pbTimeOrNil is an optional timestamp field: unset for a nil time.
func pbTimeOrNil(at *time.Time) *timestamppb.Timestamp {
	if at == nil {
		return nil
	}
	return timestamppb.New(*at)
}

// --- The dashboard arrangement -----------------------------------------------
//
// Stored on the membership: one person's arrangement of one space. The layout
// is opaque here, since the widgets are the client's business; only something
// that is not a JSON array is refused.

func (s spaceService) GetDashboardLayout(ctx context.Context, _ *agentifiv1.GetDashboardLayoutRequest) (*agentifiv1.GetDashboardLayoutResponse, error) {
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

func (s spaceService) SetDashboardLayout(ctx context.Context, req *agentifiv1.SetDashboardLayoutRequest) (*agentifiv1.SetDashboardLayoutResponse, error) {
	sp := spaceFrom(ctx)
	// Unset or null clears it, which is how a client says "back to the
	// built-in order".
	var stored []byte
	layout := req.GetLayout()
	if _, isNull := layout.GetKind().(*structpb.Value_NullValue); layout != nil && !isNull {
		if layout.GetListValue() == nil {
			return nil, errInvalid("type", []string{"body", "layout"}, "layout must be a list")
		}
		encoded, err := json.Marshal(layout.AsInterface())
		if err != nil {
			return nil, err
		}
		stored = encoded
	}
	if err := s.env.DB.SetDashboardLayout(ctx, sp.ID(), sp.Membership.UserID, stored); err != nil {
		return nil, err
	}
	saved, err := layoutProto(stored)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.SetDashboardLayoutResponse{Layout: saved}, nil
}

// layoutProto is a stored layout, unset when there is none.
func layoutProto(stored []byte) (*structpb.Value, error) {
	if len(stored) == 0 || string(stored) == "null" {
		return nil, nil
	}
	var decoded any
	if err := json.Unmarshal(stored, &decoded); err != nil {
		return nil, err
	}
	return structpb.NewValue(decoded)
}
