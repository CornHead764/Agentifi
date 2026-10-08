package api

import (
	"context"
	"net/http"
	"sort"
	"strconv"

	"connectrpc.com/connect"
	"github.com/google/uuid"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Accounts the household has ignored: accounts that exist (usually made by a
// sync) and are wanted out of everything. An ignored account leaves the lists,
// pickers, net worth, reports and the spending plan (domain.Account.IsIgnored),
// is offered to no pairing or matching, and the sync stops feeding it while
// keeping its link. Its transactions and exclusion flags are untouched, so
// un-ignoring restores exactly what was there.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewIgnoredAccountServiceHandler(ignoredAccountService{env}, opts...)
	})
}

type ignoredAccountService struct{ env *Env }

func (s ignoredAccountService) ListIgnoredAccounts(
	ctx context.Context, _ *agentifiv1.ListIgnoredAccountsRequest,
) (*agentifiv1.ListIgnoredAccountsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	all, err := env.DB.ListAccounts(ctx, sp.ID(),
		store.AccountQuery{IncludeClosed: true, IncludeIgnored: true})
	if err != nil {
		return nil, err
	}
	ignored := make([]store.Account, 0)
	for _, account := range all {
		if account.IsIgnored() {
			ignored = append(ignored, account)
		}
	}
	balances, err := listingBalances(ctx, env, sp, ignored)
	if err != nil {
		return nil, err
	}
	names, err := institutionNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}

	// Newest first, the way a connection lists its ignored remote accounts.
	sort.SliceStable(ignored, func(i, j int) bool { return ignored[i].IgnoredAt.After(*ignored[j].IgnoredAt) })
	out := &agentifiv1.ListIgnoredAccountsResponse{Accounts: make([]*agentifiv1.IgnoredAccount, 0, len(ignored))}
	for _, account := range ignored {
		row := &agentifiv1.IgnoredAccount{
			Id:            account.ID.String(),
			Name:          account.Name,
			Type:          account.Type,
			Kind:          string(account.Kind),
			MaskedNumber:  dbconv.NullText(account.MaskedNumber),
			ConnectionId:  idOrNil(account.ConnectionID),
			InstitutionId: idOrNil(account.InstitutionID),
			Balance:       moneyProto(balances[account.ID]),
			IgnoredAt:     timestamppb.New(*account.IgnoredAt),
		}
		if name, ok := names[account.InstitutionID]; ok && account.InstitutionID != uuid.Nil {
			row.Institution = &name
		}
		out.Accounts = append(out.Accounts, row)
	}
	return out, nil
}

func (s ignoredAccountService) IgnoreAccounts(
	ctx context.Context, req *agentifiv1.IgnoreAccountsRequest,
) (*agentifiv1.IgnoreAccountsResponse, error) {
	sp := spaceFrom(ctx)
	if len(req.GetAccountIds()) == 0 {
		return nil, errInvalid("missing", []string{"body", "account_ids"}, "account_ids names no account")
	}
	ids := make([]uuid.UUID, 0, len(req.GetAccountIds()))
	for i, raw := range req.GetAccountIds() {
		id, err := uuid.Parse(raw)
		if err != nil {
			return nil, errInvalid("uuid_parsing", []string{"body", "account_ids", strconv.Itoa(i)},
				"account_ids must be uuids")
		}
		ids = append(ids, id)
	}
	changed, err := s.env.DB.SetAccountsIgnored(ctx, sp.ID(), ids, true)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.IgnoreAccountsResponse{AccountIds: idStrings(changed)}, nil
}

// IgnoreEmptyAccounts ignores every account at one institution that holds
// nothing (domain.HoldsNothing), by the same balance the account list shows.
// Closed accounts are swept in too; one already ignored is left as it was.
func (s ignoredAccountService) IgnoreEmptyAccounts(
	ctx context.Context, req *agentifiv1.IgnoreEmptyAccountsRequest,
) (*agentifiv1.IgnoreEmptyAccountsResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	if req.GetInstitutionId() == "" {
		return nil, errInvalid("missing", []string{"body", "institution_id"}, "institution_id is required")
	}
	institutionID, err := uuid.Parse(req.GetInstitutionId())
	if err != nil {
		return nil, errInvalid("uuid_parsing", []string{"body", "institution_id"}, "institution_id must be a uuid")
	}
	if institutionID == uuid.Nil {
		return nil, errInvalid("missing", []string{"body", "institution_id"}, "institution_id is required")
	}
	names, err := institutionNames(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	if _, ok := names[institutionID]; !ok {
		return nil, errNotFound("Institution")
	}

	live, err := env.DB.ListAccounts(ctx, sp.ID(), store.AccountQuery{IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	held := make([]store.Account, 0)
	for _, account := range live {
		if account.InstitutionID == institutionID {
			held = append(held, account)
		}
	}
	balances, err := listingBalances(ctx, env, sp, held)
	if err != nil {
		return nil, err
	}
	empty := make([]uuid.UUID, 0)
	for _, account := range held {
		if domain.HoldsNothing(balances[account.ID]) {
			empty = append(empty, account.ID)
		}
	}
	changed, err := env.DB.SetAccountsIgnored(ctx, sp.ID(), empty, true)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.IgnoreEmptyAccountsResponse{AccountIds: idStrings(changed)}, nil
}

// UnignoreAccount puts an account back. An account that is not ignored is a
// 404, not a silent success: a client that thinks it un-ignored one it never
// ignored is looking at a stale list.
func (s ignoredAccountService) UnignoreAccount(
	ctx context.Context, req *agentifiv1.UnignoreAccountRequest,
) (*agentifiv1.UnignoreAccountResponse, error) {
	id, err := idFrom(req.GetAccountId(), "Ignored account")
	if err != nil {
		return nil, err
	}
	changed, err := s.env.DB.SetAccountsIgnored(ctx, spaceFrom(ctx).ID(), []uuid.UUID{id}, false)
	if err != nil {
		return nil, err
	}
	if len(changed) == 0 {
		return nil, errNotFound("Ignored account")
	}
	return &agentifiv1.UnignoreAccountResponse{}, nil
}

func idStrings(ids []uuid.UUID) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		out = append(out, id.String())
	}
	return out
}

// listingBalances is each account's balance as the account listing shows it.
func listingBalances(
	ctx context.Context, env *Env, sp auth.SpaceContext, accounts []store.Account,
) (map[uuid.UUID]domain.Money, error) {
	out := make(map[uuid.UUID]domain.Money, len(accounts))
	if len(accounts) == 0 {
		return out, nil
	}
	byAccount, err := postingsByAccount(ctx, env, sp, accounts)
	if err != nil {
		return nil, err
	}
	reserves, err := goalReserves(ctx, env, sp)
	if err != nil {
		return nil, err
	}
	for _, account := range accounts {
		out[account.ID] = balancesFor(account, byAccount[account.ID], reserves[account.ID]).Balance
	}
	return out, nil
}

func institutionNames(ctx context.Context, env *Env, sp auth.SpaceContext) (map[uuid.UUID]string, error) {
	rows, err := env.DB.ListInstitutions(ctx, sp.ID())
	if err != nil {
		return nil, err
	}
	out := make(map[uuid.UUID]string, len(rows))
	for _, row := range rows {
		out[row.ID] = row.Name
	}
	return out, nil
}
