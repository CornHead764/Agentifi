package api

import (
	"context"
	"net/http"

	"connectrpc.com/connect"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
)

// Balances the sync refused to apply. When a connected account with a stable,
// material balance suddenly reports 0.00 that its transactions do not explain,
// the sync holds the last-known balance (domain.SuspectBalanceReset,
// domain.ExplainReportedBalance). This service lets the household confirm the
// zero; leaving it alone keeps the hold. The holds are read off the accounts.
//
// The hold lives on the account row, so a later real balance clears it without
// anyone visiting this screen.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewHeldBalanceServiceHandler(heldBalanceService{env}, opts...)
	})
}

type heldBalanceService struct{ env *Env }

// AcceptHeldBalance confirms the zero is real: the withheld figure becomes the
// balance, the guard is switched off for this account, and the hold is cleared.
// Running balances are recomputed because the anchor moved.
func (s heldBalanceService) AcceptHeldBalance(
	ctx context.Context, req *agentifiv1.AcceptHeldBalanceRequest,
) (*agentifiv1.AcceptHeldBalanceResponse, error) {
	env, sp := s.env, spaceFrom(ctx)
	account, err := liveAccount(ctx, env, sp, req.GetAccountId())
	if err != nil {
		return nil, err
	}
	if account.WithheldBalanceAt == nil || !account.HasWithheldBalance {
		return nil, errConflict("account has no held balance to confirm")
	}

	now := env.now()
	account.ProviderBalance = account.WithheldBalance
	account.HasProviderBalance = true
	account.ProviderBalanceAt = &now
	account.AcceptZeroBalance = true
	account.WithheldBalance = domain.Zero
	account.HasWithheldBalance = false
	account.WithheldBalanceAt = nil
	account.WithheldBalanceReason = ""

	if err := env.DB.UpdateAccount(ctx, sp.ID(), &account); err != nil {
		return nil, err
	}
	if err := recomputeRunningBalances(ctx, env, sp, account.ID); err != nil {
		return nil, err
	}
	out, err := accountReply(ctx, env, sp, account)
	if err != nil {
		return nil, err
	}
	return &agentifiv1.AcceptHeldBalanceResponse{Account: out}, nil
}
