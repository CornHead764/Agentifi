package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The invoice backfill of one merchant login: every stored order still
// without an invoice document, filed in the background. The routes are under
// /{merchant}/accounts/{id}/backfill in merchant.go; the assistant is refused
// the start (dispatch.go), since it opens the merchant's site once an order.

// MerchantBackfillResponse is a running backfill's progress, or how the last
// one ended.
type MerchantBackfillResponse struct {
	Running bool `json:"running"`
	// Total, Done and Filed are the orders the running one set out to file,
	// how many it has tried and how many it has filed.
	Total int `json:"total"`
	Done  int `json:"done"`
	Filed int `json:"filed"`
	// Left is the orders the last one left without an invoice, and Stopped
	// why it ended before the last order ("" for none).
	Left       int        `json:"left"`
	Stopped    string     `json:"stopped"`
	FinishedAt *time.Time `json:"finished_at"`
}

type MerchantBackfillWantingResponse struct {
	// Wanting is the stored orders a backfill would file an invoice for.
	Wanting int `json:"wanting"`
}

const errBackfillRunning = "An invoice backfill of this account is running; it will say how it went when it finishes"

func merchantBackfillResponse(one store.MerchantAccount) *MerchantBackfillResponse {
	if progress, running := service.MerchantBackfillRunning(one.ID); running {
		return &MerchantBackfillResponse{
			Running: true, Total: progress.Total, Done: progress.Done, Filed: progress.Filed,
		}
	}
	if one.InvoiceBackfillAt == nil {
		return nil
	}
	return &MerchantBackfillResponse{
		Filed: one.InvoiceBackfillFiled, Left: one.InvoiceBackfillLeft,
		Stopped: one.InvoiceBackfillStopped, FinishedAt: one.InvoiceBackfillAt,
	}
}

func merchantBackfillWanting(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	one, err := ownMerchantAccount(env, r, sp, id)
	if err != nil {
		return err
	}
	wanting, err := merchantService(env).InvoicesWanting(r.Context(), sp.ID(), one)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, MerchantBackfillWantingResponse{Wanting: wanting})
}

// startMerchantBackfill starts the backfill and answers at once with the
// account, whose backfill field then reports the progress.
func startMerchantBackfill(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "id", merchantOf(r).Name+" account")
	if err != nil {
		return err
	}
	if _, err := ownMerchantAccount(env, r, sp, id); err != nil {
		return err
	}
	err = merchantService(env).StartInvoiceBackfill(r.Context(), sp.ID(), id)
	var refused service.BackfillRefused
	switch {
	case errors.As(err, &refused):
		return errConflict("%s", refused.Reason)
	case errors.Is(err, service.ErrPullRunning):
		if _, backfilling := service.MerchantBackfillRunning(id); backfilling {
			return errConflict("%s", errBackfillRunning)
		}
		return errConflict("A pull of this account is running; backfill its invoices when it finishes")
	case err != nil:
		return agentError(r, err)
	}
	out, err := merchantAccountOut(env, r, sp, id)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusAccepted, out)
}
