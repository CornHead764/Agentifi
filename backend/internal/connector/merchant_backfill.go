package connector

import (
	"context"
	"encoding/json"

	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// BackfillInvoices reopens the given orders' invoice pages with the kept
// session, one at a time, and hands each printed invoice over as it is
// printed so a walk cut short keeps what it had. It never signs in: a session
// the merchant refuses ends the walk.
func (e Merchants) BackfillInvoices(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage, orders []string,
	each func(orderNumber string, invoice *provider.MerchantInvoice),
) (provider.MerchantBackfillResult, error) {
	module, err := e.pick(merchant)
	if err != nil {
		return provider.MerchantBackfillResult{}, err
	}
	backfiller, ok := module.(merchants.InvoiceBackfiller)
	if !ok {
		return provider.MerchantBackfillResult{}, agentError(provider.ErrAgentBadRequest,
			"%s has no invoice pages to reopen", merchants.Name(module))
	}
	if len(storageState) == 0 {
		return provider.MerchantBackfillResult{}, agentError(provider.ErrAgentBadRequest,
			"there is no session to reopen the invoices with")
	}
	if _, handedOver := handedOverKind(storageState); handedOver {
		return provider.MerchantBackfillResult{}, agentError(provider.ErrAgentBadRequest,
			"%s's invoice pages need a browser session", merchants.Name(module))
	}

	return guarded(e.Engine, merchants.Name(module), "the invoice backfill", func() (provider.MerchantBackfillResult, error) {
		opened, err := e.opener(module, agent.Open{State: storageState})
		if err != nil {
			return provider.MerchantBackfillResult{}, err
		}
		defer closeOpened(module, opened)
		if opened.Page == nil {
			return provider.MerchantBackfillResult{}, agentError(provider.ErrAgentFailed,
				"the browser opened with no page")
		}
		module.Attach(opened.Page)
		notes := e.notes(merchants.Name(module))
		call := merchants.Call{Ctx: ctx, Page: opened.Page, Session: storageState, Notes: notes, Now: e.Now}
		stopped, signIn := backfiller.BackfillInvoices(call, orders, func(orderID string, printed *merchants.Invoice) {
			if printed == nil || len(printed.PDF) == 0 {
				each(orderID, nil)
				return
			}
			each(orderID, &provider.MerchantInvoice{
				OrderNumber: printed.OrderID, Filename: printed.Filename, PDF: printed.PDF,
			})
		})
		result := provider.MerchantBackfillResult{Stopped: stopped, NeedsSignIn: signIn}
		if !signIn && opened.StorageState != nil {
			if taken, err := opened.StorageState(); err == nil {
				result.StorageState = taken
			}
		}
		result.Notes = notes.List()
		return result, nil
	})
}
