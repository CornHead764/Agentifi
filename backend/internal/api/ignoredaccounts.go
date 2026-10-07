package api

import (
	"net/http"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/pgconv"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Accounts the household has ignored: accounts that exist (usually made by a
// sync) and are wanted out of everything. An ignored account leaves the lists,
// pickers, net worth, reports and the spending plan (domain.Account.IsIgnored),
// is offered to no pairing or matching, and the sync stops feeding it while
// keeping its link. Its transactions and exclusion flags are untouched, so
// un-ignoring restores exactly what was there.

func init() {
	Register(Resource{Prefix: "/ignored-accounts", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listIgnoredAccounts)
		rt.Write(http.MethodPost, "/", ignoreAccounts)
		rt.Write(http.MethodPost, "/empty", ignoreEmptyAccounts)
		rt.Write(http.MethodDelete, "/{account_id}", unignoreAccount)
	}})
}

// IgnoredAccountRow is one ignored account as the settings list shows it,
// balance included so the list can say what is being left out.
type IgnoredAccountRow struct {
	ID           uuid.UUID          `json:"id"`
	Name         string             `json:"name"`
	Type         string             `json:"type"`
	Kind         domain.AccountKind `json:"kind"`
	MaskedNumber *string            `json:"masked_number"`
	ConnectionID *uuid.UUID         `json:"connection_id"`
	// Institution is the bank's name, null for an account held at none.
	InstitutionID *uuid.UUID   `json:"institution_id"`
	Institution   *string      `json:"institution"`
	Balance       domain.Money `json:"balance"`
	IgnoredAt     time.Time    `json:"ignored_at"`
}

// IgnoreAccountsRequest names the accounts to ignore.
type IgnoreAccountsRequest struct {
	AccountIDs []uuid.UUID `json:"account_ids"`
}

// IgnoreEmptyRequest names the institution whose empty accounts to ignore.
type IgnoreEmptyRequest struct {
	InstitutionID uuid.UUID `json:"institution_id"`
}

// IgnoredAccountsResult is which accounts one request actually ignored; an
// account already ignored, deleted or in another space is not among them.
type IgnoredAccountsResult struct {
	AccountIDs []uuid.UUID `json:"account_ids"`
}

func listIgnoredAccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	all, err := env.DB.ListAccounts(r.Context(), sp.ID(),
		store.AccountQuery{IncludeClosed: true, IncludeIgnored: true})
	if err != nil {
		return err
	}
	ignored := make([]store.Account, 0)
	for _, account := range all {
		if account.IsIgnored() {
			ignored = append(ignored, account)
		}
	}
	balances, err := listingBalances(r, env, sp, ignored)
	if err != nil {
		return err
	}
	names, err := institutionNames(r, env, sp)
	if err != nil {
		return err
	}

	out := make([]IgnoredAccountRow, 0, len(ignored))
	for _, account := range ignored {
		row := IgnoredAccountRow{
			ID:           account.ID,
			Name:         account.Name,
			Type:         account.Type,
			Kind:         account.Kind,
			MaskedNumber: pgconv.NullText(account.MaskedNumber),
			Balance:      balances[account.ID],
			IgnoredAt:    *account.IgnoredAt,
		}
		if account.ConnectionID != uuid.Nil {
			row.ConnectionID = &account.ConnectionID
		}
		if account.InstitutionID != uuid.Nil {
			row.InstitutionID = &account.InstitutionID
			if name, ok := names[account.InstitutionID]; ok {
				row.Institution = &name
			}
		}
		out = append(out, row)
	}
	// Newest first, the way a connection lists its ignored remote accounts.
	sort.SliceStable(out, func(i, j int) bool { return out[i].IgnoredAt.After(out[j].IgnoredAt) })
	return writeJSON(w, http.StatusOK, out)
}

func ignoreAccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body IgnoreAccountsRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if len(body.AccountIDs) == 0 {
		return errInvalid("missing", []string{"body", "account_ids"}, "account_ids names no account")
	}
	changed, err := env.DB.SetAccountsIgnored(r.Context(), sp.ID(), body.AccountIDs, true)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, IgnoredAccountsResult{AccountIDs: store.NonNil(changed)})
}

// ignoreEmptyAccounts ignores every account at one institution that holds
// nothing (domain.HoldsNothing), by the same balance the account list shows.
// Closed accounts are swept in too; one already ignored is left as it was.
func ignoreEmptyAccounts(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body IgnoreEmptyRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if body.InstitutionID == uuid.Nil {
		return errInvalid("missing", []string{"body", "institution_id"}, "institution_id is required")
	}
	names, err := institutionNames(r, env, sp)
	if err != nil {
		return err
	}
	if _, ok := names[body.InstitutionID]; !ok {
		return errNotFound("Institution")
	}

	live, err := env.DB.ListAccounts(r.Context(), sp.ID(), store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return err
	}
	held := make([]store.Account, 0)
	for _, account := range live {
		if account.InstitutionID == body.InstitutionID {
			held = append(held, account)
		}
	}
	balances, err := listingBalances(r, env, sp, held)
	if err != nil {
		return err
	}
	empty := make([]uuid.UUID, 0)
	for _, account := range held {
		if domain.HoldsNothing(balances[account.ID]) {
			empty = append(empty, account.ID)
		}
	}
	changed, err := env.DB.SetAccountsIgnored(r.Context(), sp.ID(), empty, true)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, IgnoredAccountsResult{AccountIDs: store.NonNil(changed)})
}

// unignoreAccount puts an account back. An account that is not ignored is a
// 404, not a silent success: a client that thinks it un-ignored one it never
// ignored is looking at a stale list.
func unignoreAccount(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "account_id", "Ignored account")
	if err != nil {
		return err
	}
	changed, err := env.DB.SetAccountsIgnored(r.Context(), sp.ID(), []uuid.UUID{id}, false)
	if err != nil {
		return err
	}
	if len(changed) == 0 {
		return errNotFound("Ignored account")
	}
	return writeNoContent(w)
}

// listingBalances is each account's balance as GET /accounts shows it.
func listingBalances(
	r *http.Request, env *Env, sp auth.SpaceContext, accounts []store.Account,
) (map[uuid.UUID]domain.Money, error) {
	out := make(map[uuid.UUID]domain.Money, len(accounts))
	if len(accounts) == 0 {
		return out, nil
	}
	byAccount, err := postingsByAccount(r.Context(), env, sp, accounts)
	if err != nil {
		return nil, err
	}
	reserves, err := goalReserves(r.Context(), env, sp)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		out[account.ID] = balancesFor(account, byAccount[account.ID], reserves[account.ID]).Balance
	}
	return out, nil
}

func institutionNames(r *http.Request, env *Env, sp auth.SpaceContext) (map[uuid.UUID]string, error) {
	rows, err := env.DB.ListInstitutions(r.Context(), sp.ID())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}
