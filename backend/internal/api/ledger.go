package api

import (
	"context"
	"net/http"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Orchestration the ledger resources share. Every lookup passes the
// SpaceContext's id to internal/store, where the tenancy filter lives; nothing
// here holds a "current space" a query could forget.

// requireAccount refuses an account id that is not in this space: a conflict
// rather than a 404, because the id came from a well-formed body. Writing the
// row anyway would put another household's account on this transaction.
func requireAccount(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) (store.Account, error) {
	account, err := env.DB.GetAccount(ctx, sp.ID(), id)
	if err != nil {
		if isNotFound(err) {
			return store.Account{}, errConflict("account %s is not in this space", id)
		}
		return store.Account{}, err
	}
	if account.IsDeleted {
		return store.Account{}, errConflict("account %s is not in this space", id)
	}
	return account, nil
}

// checkCategory refuses a category id that is not in this space. The nil id is
// "uncategorized", which is a real thing and not an error.
func checkCategory(ctx context.Context, env *Env, sp auth.SpaceContext, id uuid.UUID) error {
	if id == uuid.Nil {
		return nil
	}
	category, err := env.DB.GetCategory(ctx, sp.ID(), id)
	if err != nil {
		if isNotFound(err) {
			return errConflict("category %s is not in this space", id)
		}
		return err
	}
	if category.IsDeleted {
		return errConflict("category %s is not in this space", id)
	}
	return nil
}

// resolveTags returns the given tag ids, refusing any that is not in this
// space and dropping duplicates. Refused rather than skipped, so the caller does
// not believe the tagging landed.
func resolveTags(ctx context.Context, env *Env, sp auth.SpaceContext, ids []uuid.UUID) ([]uuid.UUID, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	known, err := env.DB.ListTags(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	inSpace := make(map[uuid.UUID]bool, len(known))
	for _, tag := range known {
		inSpace[tag.ID] = true
	}

	seen := make(map[uuid.UUID]bool, len(ids))
	out := make([]uuid.UUID, 0, len(ids))
	for _, id := range ids {
		if seen[id] {
			continue
		}
		if !inSpace[id] {
			return nil, errConflict("tag %s is not in this space", id)
		}
		seen[id] = true
		out = append(out, id)
	}
	return out, nil
}

// recomputeRunningBalances rewrites the stored per-row balance for one account,
// through internal/service, which the sync and importers share.
func recomputeRunningBalances(ctx context.Context, env *Env, sp auth.SpaceContext, accountID uuid.UUID) error {
	return service.RecomputeRunningBalances(ctx, env.DB, sp.ID(), accountID)
}

// --- Patch field application -------------------------------------------------
//
// An explicit null on a column that cannot hold one is refused here, not left
// to fail as a 500 at the database.

func applyRequired[T any](field string, opt Opt[T], dst *T) error {
	if !opt.Set {
		return nil
	}
	if opt.Null {
		return errConflict("%s cannot be cleared", field)
	}
	*dst = opt.Value
	return nil
}

// applyNullable writes T's zero value for an explicit null, which is how
// internal/store spells a cleared note, id or date.
func applyNullable[T any](opt Opt[T], dst *T) {
	if !opt.Set {
		return
	}
	if opt.Null {
		var zero T
		*dst = zero
		return
	}
	*dst = opt.Value
}

// applyNullableMoney keeps the value and its Has flag together, so "no credit
// limit" stays a different fact from "a limit of zero".
func applyNullableMoney(opt Opt[domain.Money], dst *domain.Money, has *bool) {
	if !opt.Set {
		return
	}
	if opt.Null {
		*dst, *has = domain.Zero, false
		return
	}
	*dst, *has = opt.Value, true
}

// deleted maps a soft delete to the response every delete route gives.
func deleted(w http.ResponseWriter, err error, what string) error {
	if err != nil {
		return notFoundAs(err, what)
	}
	return writeNoContent(w)
}
