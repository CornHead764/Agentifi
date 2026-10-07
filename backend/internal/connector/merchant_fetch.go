package connector

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/merchants"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The sealed session is either a Playwright storage state, which the pull
// opens a browser with, or a handed-over session (an object with a `kind` the
// module names in SessionKinds), for which the module makes its own calls
// through the caller the engine hands over and says what to keep.

// maxSinceDays is ten years on purpose: a backfill reads that far.
const (
	minSinceDays = 1
	maxSinceDays = 3650
)

// Fetch pulls one account's recent orders and charges with its kept session.
// With a kept credential, a pull that meets a sign-in screen signs in once and
// pulls again (merchant_resignin.go); a fresh session refused as well stops there.
func (e Merchants) Fetch(
	ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage,
	sinceDays int, skipDetails, invoiced []string, credential *provider.MerchantCredential,
) (provider.MerchantFetchResult, error) {
	module, err := e.pick(merchant)
	if err != nil {
		return provider.MerchantFetchResult{}, err
	}
	if len(storageState) == 0 {
		return provider.MerchantFetchResult{}, agentError(provider.ErrAgentBadRequest,
			"there is no session to pull with")
	}
	if sinceDays < minSinceDays {
		sinceDays = 30
	}
	if sinceDays > maxSinceDays {
		sinceDays = maxSinceDays
	}
	if kind, handedOver := handedOverKind(storageState); handedOver && !takes(module, kind) {
		return provider.MerchantFetchResult{}, agentError(provider.ErrAgentBadRequest,
			"%s does not take a %q session", merchants.Name(module), kind)
	}

	return guarded(e.Engine, merchants.Name(module), "the pull", func() (provider.MerchantFetchResult, error) {
		notes := e.notes(merchants.Name(module))
		skip, filed := named(skipDetails), named(invoiced)
		result, err := e.fetchOnce(ctx, module, storageState, sinceDays, skip, filed, notes)
		if err != nil || !result.NeedsSignIn || !credential.Usable() {
			return result, err
		}
		notes.Addf("%s asked to sign in again; signing in with the kept password", merchants.Name(module))
		session, stopped, err := e.signInAgain(ctx, module, storageState, *credential, notes)
		if err != nil {
			return provider.MerchantFetchResult{}, err
		}
		if stopped != nil {
			return *stopped, nil
		}
		again, err := e.fetchOnce(ctx, module, session, sinceDays, skip, filed, notes)
		if err != nil {
			return provider.MerchantFetchResult{}, err
		}
		if again.NeedsSignIn {
			again.Reason = merchants.Name(module) + " refused the session the kept password had just opened"
			return again, nil
		}
		if len(again.StorageState) == 0 {
			again.StorageState = session
		}
		return again, nil
	})
}

func (e Merchants) fetchOnce(
	ctx context.Context, module merchants.Module, storageState json.RawMessage,
	sinceDays int, skip, filed map[string]bool, notes *merchants.Notes,
) (provider.MerchantFetchResult, error) {
	call := merchants.Call{
		Ctx: ctx, Session: storageState, SinceDays: sinceDays,
		SkipDetails: skip, Invoiced: filed, Notes: notes, Now: e.Now,
	}
	var opened *OpenBrowser
	if _, handedOver := handedOverKind(storageState); handedOver {
		caller, release, err := e.httpFor(module)
		if err != nil {
			return provider.MerchantFetchResult{}, err
		}
		defer release()
		call.HTTP = caller
	} else {
		var err error
		opened, err = e.opener(module, agent.Open{State: storageState})
		if err != nil {
			return provider.MerchantFetchResult{}, err
		}
		defer closeOpened(module, opened)
		if opened.Page == nil {
			return provider.MerchantFetchResult{}, agentError(provider.ErrAgentFailed,
				"the browser opened with no page")
		}
		module.Attach(opened.Page)
		call.Page = opened.Page
	}

	result, err := module.Fetch(call)
	if err != nil {
		return provider.MerchantFetchResult{}, agent.Pictured(call.Page, err)
	}
	if result.NeedsSignIn {
		reason := result.Reason
		if reason == "" {
			reason = merchants.Name(module) + " asked to sign in again"
		}
		image := result.Image
		if image == "" && call.Page != nil {
			image = agent.Screenshot(call.Page)
		}
		return provider.MerchantFetchResult{
			NeedsSignIn: true, Reason: reason, Image: image, Notes: notes.List(),
		}, nil
	}

	// Amazon rolls its cookies, so the session kept is never the one sent.
	kept := result.StorageState
	if opened != nil && opened.StorageState != nil {
		taken, err := opened.StorageState()
		if err != nil {
			return provider.MerchantFetchResult{}, err
		}
		kept = taken
	}
	invoices := e.printInvoices(result.Invoices, notes)
	return provider.MerchantFetchResult{
		AccountHint: result.AccountHint, StorageState: kept, Parsed: result.Parsed,
		Orders: result.Orders, Charges: result.Charges, Notes: notes.List(), Invoices: invoices,
	}, nil
}

// printInvoices prints what a module laid out as HTML. A failed print costs
// that invoice, not the pull.
func (e Merchants) printInvoices(invoices []merchants.Invoice, notes *merchants.Notes) []provider.MerchantInvoice {
	var out []provider.MerchantInvoice
	for _, one := range invoices {
		printed := one.PDF
		if len(printed) == 0 && one.HTML != "" && e.Print != nil {
			var err error
			if printed, err = e.Print(one.HTML); err != nil {
				notes.Addf("the invoices could not be printed, so none were filed: %v", err)
				return out
			}
		}
		if len(printed) == 0 {
			continue
		}
		out = append(out, provider.MerchantInvoice{
			OrderNumber: one.OrderID, Filename: one.Filename, PDF: printed,
		})
	}
	return out
}

// handedOverKind tells a handed-over session, which names its own kind, from a
// browser's storage state, which has a cookies list and no kind.
func handedOverKind(state json.RawMessage) (string, bool) {
	var shape struct {
		Kind    string          `json:"kind"`
		Cookies json.RawMessage `json:"cookies"`
	}
	if json.Unmarshal(state, &shape) != nil || shape.Kind == "" {
		return "", false
	}
	if len(shape.Cookies) > 0 && shape.Cookies[0] == '[' {
		return "", false
	}
	return shape.Kind, true
}

func takes(module merchants.Module, kind string) bool {
	for _, known := range module.SessionKinds() {
		if known == kind {
			return true
		}
	}
	return false
}

func named(values []string) map[string]bool {
	out := make(map[string]bool, len(values))
	for _, value := range values {
		out[value] = true
	}
	return out
}

// LayOutReceipt prints a stored purchase as its receipt document, the same
// layout a pull makes, with no request to the merchant.
func (e Merchants) LayOutReceipt(receipt provider.MerchantReceipt) (string, []byte, error) {
	filename, page, err := merchants.Receipt(receipt)
	if err != nil {
		return "", nil, err
	}
	if e.Print == nil {
		return "", nil, errors.New("connector: there is no browser to print the receipt in")
	}
	printed, err := e.Print(page)
	if err != nil {
		return "", nil, err
	}
	return filename, printed, nil
}
