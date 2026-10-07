package api

import (
	"net/http"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Balances the sync refused to apply. When a connected account with a stable,
// material balance suddenly reports 0.00 that its transactions do not explain,
// the sync holds the last-known balance (domain.SuspectBalanceReset,
// domain.ExplainReportedBalance). This resource lets the household confirm the
// zero; leaving it alone keeps the hold. The holds are read off /accounts.
//
// The hold lives on the account row, so a later real balance clears it without
// anyone visiting this screen.

func init() {
	Register(Resource{Prefix: "/held-balances", Routes: func(rt *Routes) {
		rt.Write(http.MethodPost, "/{account_id}/accept", acceptHeldBalance)
	}})
}

// acceptHeldBalance confirms the zero is real: the withheld figure becomes the
// balance, the guard is switched off for this account, and the hold is cleared.
// Running balances are recomputed because the anchor moved.
func acceptHeldBalance(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	account, err := liveAccount(r, env, sp)
	if err != nil {
		return err
	}
	if account.WithheldBalanceAt == nil || !account.HasWithheldBalance {
		return errConflict("account has no held balance to confirm")
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

	if err := env.DB.UpdateAccount(r.Context(), sp.ID(), &account); err != nil {
		return err
	}
	if err := recomputeRunningBalances(r.Context(), env, sp, account.ID); err != nil {
		return err
	}
	return writeAccount(env, w, r, sp, account, http.StatusOK)
}
