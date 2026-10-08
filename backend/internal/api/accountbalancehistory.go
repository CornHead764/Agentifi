package api

import (
	"context"
	"net/http"
	"strings"

	"connectrpc.com/connect"
	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Where one account's balance has been: a point per day, derived on read by
// domain.BalanceHistory, the same balance-as-of-a-day the projection opens on,
// so the line ends where the projection begins.
//
// Both ends are required (trap 5), and `to` is held at today.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewAccountBalanceHistoryServiceHandler(accountBalanceHistoryService{env}, opts...)
	})
}

type accountBalanceHistoryService struct{ env *Env }

// maxBalanceHistoryDays bounds one request: two years of daily points.
const maxBalanceHistoryDays = 731

func (s accountBalanceHistoryService) GetAccountBalanceHistory(
	ctx context.Context, req *agentifiv1.GetAccountBalanceHistoryRequest,
) (*agentifiv1.GetAccountBalanceHistoryResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	raw := strings.TrimSpace(req.GetAccountId())
	if raw == "" {
		return nil, errInvalid("missing", []string{"query", "account_id"}, "account_id is required")
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return nil, errInvalid("uuid_parsing", []string{"query", "account_id"}, "account_id must be a uuid")
	}
	window, err := windowBetween(req.GetFrom(), req.GetTo(), domain.DatePosted)
	if err != nil {
		return nil, err
	}
	if !window.HasFrom || !window.HasTo {
		return nil, errBadRequest("from and to are both required: a balance history has no " +
			"default window")
	}
	from, to := window.From, window.To
	if span := domain.DaysBetween(from, to) + 1; span > maxBalanceHistoryDays {
		return nil, errBadRequest("a balance history covers at most %d days", maxBalanceHistoryDays)
	}
	if today := domain.DateOf(env.now()); to.After(today) {
		to = today
	}

	account, err := env.DB.GetAccount(ctx, sp.ID(), id)
	if err != nil {
		return nil, notFoundAs(err, "Account")
	}
	if account.IsDeleted {
		return nil, errNotFound("Account")
	}
	postings, err := postingsByAccount(ctx, env, sp, []store.Account{account})
	if err != nil {
		return nil, err
	}
	domainAccount := store.DomainAccount(account)
	history := domain.BalanceHistory(domainAccount, postings[account.ID], from, to)

	points := make([]*agentifiv1.AccountBalancePoint, 0, len(history))
	for _, point := range history {
		points = append(points, &agentifiv1.AccountBalancePoint{
			On: point.On.String(), Balance: moneyProto(point.Balance),
		})
	}
	return &agentifiv1.GetAccountBalanceHistoryResponse{
		AccountId: account.ID.String(), From: from.String(), To: to.String(),
		Balance: moneyProto(domain.AccountBalance(domainAccount, postings[account.ID])),
		Points:  points,
	}, nil
}
