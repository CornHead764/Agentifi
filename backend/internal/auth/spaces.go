package auth

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Tenancy resolution: which space one request is about.
//
// Every tenant-scoped handler receives a SpaceContext resolved here, from the
// authenticated user and the optional X-Space-Id header, never a space id from
// the body or path. Membership and role are read fresh on every request, so a
// removal or demotion takes effect on the next request.
//
// That costs three indexed reads per request (revocation, user, membership).
// Collapsing them with a per-token cache would delay every revocation,
// deactivation and membership change — the property this design exists for —
// unless the cache is invalidated by every such write across processes.

// HeaderSpaceID is the request header that selects a space explicitly.
const HeaderSpaceID = "X-Space-Id"

// SpaceResolver is the slice of internal/store this package needs. Narrow on
// purpose: resolving tenancy must not grow the ability to read a space's rows.
type SpaceResolver interface {
	ResolveSpace(ctx context.Context, userID uuid.UUID, requested *store.SpaceID) (store.Space, store.Membership, error)
}

// SpaceContext is the resolved tenant for one request: the space, the
// caller's membership and the user.
type SpaceContext struct {
	Space      store.Space
	Membership store.Membership
	User       store.User
}

func (c SpaceContext) ID() store.SpaceID { return c.Space.ID }
func (c SpaceContext) UserID() uuid.UUID { return c.User.ID }
func (c SpaceContext) Role() store.Role  { return c.Membership.Role }
func (c SpaceContext) CanWrite() bool    { return c.Membership.Role.CanWrite() }
func (c SpaceContext) IsOwner() bool     { return c.Membership.Role.IsOwner() }

// RequireWrite refuses a viewer. Distinguishable from ErrNoSpace on purpose:
// the caller is already known to be a member.
func (c SpaceContext) RequireWrite() error {
	if !c.CanWrite() {
		return ErrReadOnly
	}
	return nil
}

// RequireOwner refuses anyone who may not change membership, delete the space
// or edit its settings.
func (c SpaceContext) RequireOwner() error {
	if !c.IsOwner() {
		return ErrReadOnly
	}
	return nil
}

// ResolveSpace decides which space this request is about.
//
// Empty requested falls to the user's oldest accepted membership. An
// outstanding invitation grants nothing (internal/store checks accepted_at).
//
// A space the user is not a member of, or an unparseable header, returns
// ErrNoSpace, never a permission or validation error: anything
// distinguishable is a membership oracle.
func ResolveSpace(ctx context.Context, spaces SpaceResolver, user store.User, requested string) (SpaceContext, error) {
	var wanted *store.SpaceID
	if requested != "" {
		id, err := store.ParseSpaceID(requested)
		if err != nil {
			return SpaceContext{}, ErrNoSpace
		}
		wanted = &id
	}

	space, membership, err := spaces.ResolveSpace(ctx, user.ID, wanted)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return SpaceContext{}, ErrNoSpace
		}
		return SpaceContext{}, err
	}
	return SpaceContext{Space: space, Membership: membership, User: user}, nil
}

// ResolveSpaceForRequest reads the header and resolves.
func ResolveSpaceForRequest(ctx context.Context, spaces SpaceResolver, user store.User, r *http.Request) (SpaceContext, error) {
	return ResolveSpace(ctx, spaces, user, r.Header.Get(HeaderSpaceID))
}
