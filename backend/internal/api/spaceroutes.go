package api

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/backup"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Which spaces the caller can see, who else is in them, and who may change
// that. This answers from the caller's memberships, before a client can send
// X-Space-Id.
//
// The membership routes name their space in the path, so they are User routes
// rather than Write ones; rights are checked through callerMembership, the only
// way to get one.

func init() {
	RegisterIdentity(Resource{Prefix: "/spaces", Routes: func(rt *Routes) {
		rt.User(http.MethodGet, "/", listSpaces)
		rt.User(http.MethodPost, "/", createSpace)
		rt.Read(http.MethodGet, "/current", readCurrentSpace)
		rt.Write(http.MethodPatch, "/current/preferences", updatePreferences)
		// A Read route, not a Write: arranging your own dashboard is not a
		// change to the household's money, and a viewer's dashboard is theirs.
		rt.Read(http.MethodGet, "/current/dashboard", readDashboardLayout)
		rt.Read(http.MethodPut, "/current/dashboard", saveDashboardLayout)
		// The invitee's side: the caller has no space yet, so there is no header
		// a Read or Write could resolve.
		rt.User(http.MethodGet, "/invitations", listInvitations)
		rt.User(http.MethodPost, "/invitations/{membership_id}/accept", acceptInvitation)
		rt.User(http.MethodDelete, "/invitations/{membership_id}", declineInvitation)
		rt.User(http.MethodPatch, "/{space_id}", renameSpace)
		// A POST, since the typed confirmation travels in the body.
		rt.User(http.MethodPost, "/{space_id}/delete", deleteSpace)
		rt.User(http.MethodGet, "/{space_id}/members", listMembers)
		rt.User(http.MethodPost, "/{space_id}/members", inviteMember)
		rt.User(http.MethodPatch, "/{space_id}/members/{membership_id}", updateMember)
		rt.User(http.MethodDelete, "/{space_id}/members/{membership_id}", removeMember)
	}})
}

// SpaceResponse is a space as seen by one member, with that member's rights
// attached.
type SpaceResponse struct {
	ID              uuid.UUID `json:"id"`
	Name            string    `json:"name"`
	PrimaryCurrency string    `json:"primary_currency"`
	Timezone        string    `json:"timezone"`
	// DefaultDateRange is the preset a page opens on; "" is the client's
	// default. SidebarAccountTypes null means "list them all", unlike [].
	DefaultDateRange    string     `json:"default_date_range"`
	SidebarAccountTypes []string   `json:"sidebar_account_types"`
	Role                store.Role `json:"role"`
	CanWrite            bool       `json:"can_write"`
	IsOwner             bool       `json:"is_owner"`
	JoinedAt            *time.Time `json:"joined_at"`
}

type MembershipResponse struct {
	ID         uuid.UUID  `json:"id"`
	UserID     uuid.UUID  `json:"user_id"`
	Email      string     `json:"email"`
	FullName   *string    `json:"full_name"`
	Role       store.Role `json:"role"`
	InvitedAt  *time.Time `json:"invited_at"`
	AcceptedAt *time.Time `json:"accepted_at"`
}

// InvitationResponse is an outstanding invitation as the invitee sees it, with
// the space's name, which nothing else will give a non-member.
type InvitationResponse struct {
	ID        uuid.UUID  `json:"id"`
	SpaceID   uuid.UUID  `json:"space_id"`
	SpaceName string     `json:"space_name"`
	Role      store.Role `json:"role"`
	InvitedAt *time.Time `json:"invited_at"`
}

type SpaceCreate struct {
	Name string `json:"name"`
}

type SpaceUpdate struct {
	Name Opt[string] `json:"name"`
}

// SpacePreferences is how the space is displayed. Not in SpaceUpdate because
// any member who can write sets these; renaming is an owner's act.
type SpacePreferences struct {
	DefaultDateRange    Opt[string]   `json:"default_date_range"`
	SidebarAccountTypes Opt[[]string] `json:"sidebar_account_types"`
	// PrimaryCurrency is the currency every figure in this space is reported
	// in. Changing it restamps the ledger — see updatePreferences.
	PrimaryCurrency Opt[string] `json:"primary_currency"`
}

// SpaceDelete is the typed confirmation: the space's name as it stands.
type SpaceDelete struct {
	ConfirmName string `json:"confirm_name"`
}

// SpaceDeleted names the backup set taken first, or null when backups are
// off on this server.
type SpaceDeleted struct {
	BackupSet *string `json:"backup_set"`
}

type MemberInvite struct {
	Email string     `json:"email"`
	Role  store.Role `json:"role"`
}

type MemberUpdate struct {
	Role store.Role `json:"role"`
}

// listSpaces is every space the caller has actually joined, with their role in
// each. An outstanding invitation grants nothing and is not listed.
func listSpaces(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaces, err := env.DB.ListSpacesForUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]SpaceResponse, 0, len(spaces))
	for _, space := range spaces {
		membership, err := env.DB.GetMembership(r.Context(), space.ID, user.ID)
		if err != nil {
			if isNotFound(err) {
				continue
			}
			return err
		}
		out = append(out, spaceResponse(space, membership))
	}
	return writeJSON(w, http.StatusOK, out)
}

// readCurrentSpace is the space X-Space-Id selected, or the default it falls
// back to, so the client never has to guess.
func readCurrentSpace(_ *Env, w http.ResponseWriter, _ *http.Request, sp auth.SpaceContext) error {
	return writeJSON(w, http.StatusOK, spaceResponse(sp.Space, sp.Membership))
}

// createSpace opens a second set of finances, owned by whoever asked for it.
func createSpace(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	var body SpaceCreate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	name := strings.TrimSpace(body.Name)
	if name == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}

	now := env.now().UTC()
	space := &store.Space{Name: name, PrimaryCurrency: env.Cfg.PrimaryCurrency}
	membership := &store.Membership{
		UserID: user.ID, Role: store.RoleOwner, InvitedAt: &now, AcceptedAt: &now,
	}
	// One write, because a space whose creator never got a membership is a
	// space nobody can reach and nobody can delete.
	err := env.DB.InTx(r.Context(), func(tx *store.Store) error {
		if err := tx.CreateSeededSpace(r.Context(), space); err != nil {
			return err
		}
		return tx.CreateMembership(r.Context(), space.ID, membership)
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, spaceResponse(*space, *membership))
}

// listInvitations is what the caller has been asked to join and has not taken:
// not spaces they have, so not in listSpaces.
func listInvitations(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	invitations, err := env.DB.ListPendingInvitations(r.Context(), user.ID)
	if err != nil {
		return err
	}
	out := make([]InvitationResponse, 0, len(invitations))
	for _, invitation := range invitations {
		out = append(out, InvitationResponse{
			ID:        invitation.Membership.ID,
			SpaceID:   invitation.Membership.SpaceID.UUID(),
			SpaceName: invitation.SpaceName,
			Role:      invitation.Membership.Role,
			InvitedAt: invitation.Membership.InvitedAt,
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

// acceptInvitation takes an invitation. The store scopes the write to the
// caller's own user id, so someone else's invitation is a 404. The space comes
// back whole for the client to switch to.
func acceptInvitation(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	id, err := pathUUID(r, "membership_id", "Invitation")
	if err != nil {
		return err
	}
	membership, err := env.DB.AcceptInvitation(r.Context(), id, user.ID, env.now().UTC())
	if err != nil {
		return notFoundAs(err, "Invitation")
	}
	space, err := env.DB.GetSpace(r.Context(), membership.SpaceID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, spaceResponse(space, membership))
}

// declineInvitation drops one, which changes nothing about what the caller can
// see — the invitation granted nothing to begin with.
func declineInvitation(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	id, err := pathUUID(r, "membership_id", "Invitation")
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeclineInvitation(r.Context(), id, user.ID), "Invitation")
}

func renameSpace(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, caller, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	if err := requireOwner(caller); err != nil {
		return err
	}
	var body SpaceUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	space, err := env.DB.GetSpace(r.Context(), spaceID)
	if err != nil {
		return notFoundAs(err, "Space")
	}
	// Deleted is gone: ListSpacesForUser already hides it, and a rename that
	// lands on one succeeds without anybody ever seeing the new name.
	if space.IsDeleted {
		return errNotFound("Space")
	}
	if err := applyRequired("name", body.Name, &space.Name); err != nil {
		return err
	}
	space.Name = strings.TrimSpace(space.Name)
	if space.Name == "" {
		return errInvalid("missing", []string{"body", "name"}, "name is required")
	}
	if err := env.DB.UpdateSpace(r.Context(), &space); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, spaceResponse(space, caller))
}

// deleteSpace removes a space and everything in it, for every member. Only an
// owner may, never an admin, and only with the space's name typed out. The
// caller keeps at least one space, so the app always has one to open on.
// Where the server writes backups, a set is taken first and a failed one
// deletes nothing.
func deleteSpace(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, caller, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	if !caller.Role.Owns() {
		return auth.ErrReadOnly
	}
	var body SpaceDelete
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	space, err := env.DB.GetSpace(r.Context(), spaceID)
	if err != nil {
		return notFoundAs(err, "Space")
	}
	if strings.TrimSpace(body.ConfirmName) != space.Name {
		return errInvalid("value", []string{"body", "confirm_name"},
			"type the space's name exactly to delete it")
	}
	joined, err := env.DB.ListSpacesForUser(r.Context(), user.ID)
	if err != nil {
		return err
	}
	if len(joined) < 2 {
		return errConflictCode("last_space",
			"This is your only space. Create another before deleting this one.")
	}

	var taken *string
	if backups := env.ServerBackups(); backups.Enabled() {
		set, err := backups.Take(r.Context(), backup.TriggerSpaceDelete)
		switch {
		case errors.Is(err, service.ErrBackupRunning):
			return errConflict("A backup is running. Try again when it has finished.")
		case err != nil:
			slog.Warn("the backup before deleting a space failed", "space", spaceID, "error", err)
			return errConflict("The backup taken before deleting failed, so nothing was deleted. " +
				"The server log says why.")
		}
		taken = &set.Name
	}

	if err := service.DeleteSpace(
		r.Context(), env.DB, env.Storage, billsService(env), spaceID); err != nil {
		return notFoundAs(err, "Space")
	}
	return writeJSON(w, http.StatusOK, SpaceDeleted{BackupSet: taken})
}

// updatePreferences sets how this space is displayed. Null clears either
// preference back to its default.
func updatePreferences(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body SpacePreferences
	if err := decodeBody(r, &body); err != nil {
		return err
	}

	space := sp.Space
	switch {
	case body.DefaultDateRange.Cleared():
		space.DefaultDateRange = ""
	case body.DefaultDateRange.Present():
		preset := strings.TrimSpace(body.DefaultDateRange.Value)
		if preset != "" && !isRangePreset(preset) {
			return errInvalid("enum", []string{"body", "default_date_range"},
				"%q is not a range preset", preset)
		}
		space.DefaultDateRange = preset
	}
	switch {
	case body.SidebarAccountTypes.Cleared():
		space.SidebarAccountTypes = nil
	case body.SidebarAccountTypes.Present():
		// A sent-but-empty list is a real answer: the user unticked every
		// type. It stays non-nil so it does not read as "never chosen".
		types := body.SidebarAccountTypes.Value
		if types == nil {
			types = []string{}
		}
		space.SidebarAccountTypes = types
	}

	// The currency every other figure is reported in, so it is the one
	// preference that is not just a display choice.
	restamp := false
	if body.PrimaryCurrency.Present() {
		currency := strings.ToUpper(strings.TrimSpace(body.PrimaryCurrency.Value))
		if len(currency) != 3 {
			return errInvalid("format", []string{"body", "primary_currency"},
				"a currency is a three-letter ISO 4217 code")
		}
		if !isSupportedCurrency(env.Cfg.SupportedCurrencies, currency) {
			return errInvalid("enum", []string{"body", "primary_currency"},
				"%s is not one of the currencies this deployment quotes rates for", currency)
		}
		restamp = currency != space.PrimaryCurrency
		space.PrimaryCurrency = currency
	}

	// One transaction: stored conversions were made against the old currency,
	// and the stamping pass only fills nulls, so a failure between clear and
	// restamp would leave the ledger counted at face value. StampSpace reads
	// the space's currency, so it must run inside the transaction that set it.
	err := env.DB.InTx(r.Context(), func(tx *store.Store) error {
		if err := tx.UpdateSpace(r.Context(), &space); err != nil {
			return err
		}
		if !restamp {
			return nil
		}
		if _, err := tx.ClearConversions(r.Context(), sp.ID()); err != nil {
			return err
		}
		_, err := NewCurrency(env.Cfg, tx).StampSpace(r.Context(), sp.ID())
		return err
	})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, spaceResponse(space, sp.Membership))
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

// listMembers is everyone in one space, invitations included.
func listMembers(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, _, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	memberships, err := env.DB.ListMemberships(r.Context(), spaceID)
	if err != nil {
		return err
	}
	out := make([]MembershipResponse, 0, len(memberships))
	for _, membership := range memberships {
		member, err := env.DB.GetUser(r.Context(), membership.UserID)
		if err != nil {
			return err
		}
		out = append(out, membershipResponse(membership, member))
	}
	return writeJSON(w, http.StatusOK, out)
}

// inviteMember invites an existing account into the space, unaccepted
// (invited_at, no accepted_at), which grants nothing until taken. Only the
// server administrator makes logins, so owning a space is not a way to mint
// accounts. An address with no account gets one fixed refusal that does not
// echo it back.
func inviteMember(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, caller, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	if err := requireOwner(caller); err != nil {
		return err
	}
	var body MemberInvite
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	email := strings.TrimSpace(body.Email)
	if email == "" {
		return errInvalid("missing", []string{"body", "email"}, "email is required")
	}
	role, err := parseRole(body.Role)
	if err != nil {
		return err
	}
	if err := requireOwnerToTouchAnOwner(caller, role); err != nil {
		return err
	}

	invitee, err := env.DB.GetUserByEmail(r.Context(), email)
	if err != nil {
		if isNotFound(err) {
			return errConflict(
				"No account uses that email. Ask the server administrator to create one.")
		}
		return err
	}

	existing, err := env.DB.GetMembership(r.Context(), spaceID, invitee.ID)
	switch {
	case err == nil && existing.IsAccepted():
		return errConflict("%s is already in this space", invitee.Email)
	case err == nil:
		return errConflict("%s has already been invited", invitee.Email)
	case !isNotFound(err):
		return err
	}

	invited := env.now().UTC()
	membership := &store.Membership{UserID: invitee.ID, Role: role, InvitedAt: &invited}
	if err := env.DB.CreateMembership(r.Context(), spaceID, membership); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, membershipResponse(*membership, invitee))
}

func updateMember(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, caller, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	if err := requireOwner(caller); err != nil {
		return err
	}
	target, err := targetMembership(env, r, spaceID)
	if err != nil {
		return err
	}
	var body MemberUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	role, err := parseRole(body.Role)
	if err != nil {
		return err
	}
	if err := requireOwnerToTouchAnOwner(caller, target.Role, role); err != nil {
		return err
	}
	if !role.Owns() {
		if err := ensureAnOwnerRemains(env, r, spaceID, target); err != nil {
			return err
		}
	}

	target.Role = role
	if err := env.DB.UpdateMembership(r.Context(), spaceID, &target); err != nil {
		return err
	}
	member, err := env.DB.GetUser(r.Context(), target.UserID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, membershipResponse(target, member))
}

// removeMember revokes somebody else's membership, or gives up the caller's
// own. Leaving is not an owner's privilege; taking somebody else out is.
func removeMember(env *Env, w http.ResponseWriter, r *http.Request, user store.User) error {
	spaceID, caller, err := callerMembership(env, r, user)
	if err != nil {
		return err
	}
	target, err := targetMembership(env, r, spaceID)
	if err != nil {
		return err
	}
	if target.UserID != user.ID {
		if err := requireOwner(caller); err != nil {
			return err
		}
		if err := requireOwnerToTouchAnOwner(caller, target.Role); err != nil {
			return err
		}
	}
	if err := ensureAnOwnerRemains(env, r, spaceID, target); err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteMembership(r.Context(), spaceID, target.ID), "Member")
}

// callerMembership is the caller's own accepted membership in the space named
// in the path, and the only place the path is trusted for anything. A space
// the caller is not in, an unparseable id and an outstanding invitation are all
// 404: a 403 would confirm the space exists.
func callerMembership(env *Env, r *http.Request, user store.User) (store.SpaceID, store.Membership, error) {
	var none store.SpaceID
	id, err := pathUUID(r, "space_id", "Space")
	if err != nil {
		return none, store.Membership{}, err
	}
	spaceID := store.SpaceIDOf(id)

	membership, err := env.DB.GetMembership(r.Context(), spaceID, user.ID)
	if err != nil {
		return none, store.Membership{}, notFoundAs(err, "Space")
	}
	if !membership.IsAccepted() {
		return none, store.Membership{}, errNotFound("Space")
	}
	return spaceID, membership, nil
}

// targetMembership is the membership named in the path, which has to be one of
// this space's own: an id from another space is a 404 like any other row.
func targetMembership(env *Env, r *http.Request, spaceID store.SpaceID) (store.Membership, error) {
	id, err := pathUUID(r, "membership_id", "Member")
	if err != nil {
		return store.Membership{}, err
	}
	memberships, err := env.DB.ListMemberships(r.Context(), spaceID)
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

// requireOwner is auth.SpaceContext.RequireOwner's rule for routes that
// resolve their space from the path.
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
func ensureAnOwnerRemains(env *Env, r *http.Request, spaceID store.SpaceID, target store.Membership) error {
	if !target.IsAccepted() || !target.Role.Owns() {
		return nil
	}
	memberships, err := env.DB.ListMemberships(r.Context(), spaceID)
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

func membershipResponse(membership store.Membership, member store.User) MembershipResponse {
	return MembershipResponse{
		ID:         membership.ID,
		UserID:     membership.UserID,
		Email:      member.Email,
		FullName:   pgconv.NullText(member.FullName),
		Role:       membership.Role,
		InvitedAt:  membership.InvitedAt,
		AcceptedAt: membership.AcceptedAt,
	}
}

func spaceResponse(space store.Space, membership store.Membership) SpaceResponse {
	return SpaceResponse{
		ID:                  space.ID.UUID(),
		Name:                space.Name,
		PrimaryCurrency:     space.PrimaryCurrency,
		Timezone:            space.Timezone,
		DefaultDateRange:    space.DefaultDateRange,
		SidebarAccountTypes: space.SidebarAccountTypes,
		Role:                membership.Role,
		CanWrite:            membership.Role.CanWrite(),
		IsOwner:             membership.Role.IsOwner(),
		JoinedAt:            membership.AcceptedAt,
	}
}

// --- The dashboard arrangement -----------------------------------------------
//
// Stored on the membership: one person's arrangement of one space. The layout
// is opaque here, since the widgets are the client's business; only something
// that is not a JSON array is refused.

// DashboardLayout is the stored arrangement, or null when there is none —
// which the client reads as its own built-in order.
type DashboardLayout struct {
	Layout json.RawMessage `json:"layout"`
}

func readDashboardLayout(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	membership, err := env.DB.GetMembership(r.Context(), sp.ID(), sp.Membership.UserID)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, DashboardLayout{Layout: membership.DashboardLayout})
}

func saveDashboardLayout(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body DashboardLayout
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	// Null clears it, which is how a client says "back to the built-in order".
	var stored []byte
	if len(body.Layout) > 0 && string(body.Layout) != "null" {
		var probe []any
		if err := json.Unmarshal(body.Layout, &probe); err != nil {
			return errInvalid("type", []string{"body", "layout"}, "layout must be a list")
		}
		stored = body.Layout
	}
	if err := env.DB.SetDashboardLayout(
		r.Context(), sp.ID(), sp.Membership.UserID, stored); err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, DashboardLayout{Layout: stored})
}
