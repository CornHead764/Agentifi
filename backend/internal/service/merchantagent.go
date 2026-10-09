package service

import (
	"context"
	"encoding/json"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// MerchantAgent is what service.Merchants asks of the merchant engine; an
// interface so tests can fake it and service/ need not import the browser.
type MerchantAgent interface {
	Available() bool
	Health(ctx context.Context, merchant domain.MerchantID) error

	StartSignIn(ctx context.Context, merchant domain.MerchantID, email, password string) (provider.MerchantSignInState, error)
	AnswerSignIn(ctx context.Context, sessionID, code string) (provider.MerchantSignInState, error)
	SignInStatus(ctx context.Context, sessionID string) (provider.MerchantSignInState, error)
	CompleteSignIn(ctx context.Context, sessionID string) (json.RawMessage, string, error)

	// Fetch pulls with the kept session. skipDetails are the orders read in full,
	// invoiced those whose invoice is on file, and refundChecks those whose
	// invoice is read again for what was refunded. credential, when set, is the
	// kept login a lapsed session may sign in with once.
	Fetch(ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage, sinceDays int, skipDetails, invoiced, refundChecks []string, credential *provider.MerchantCredential) (provider.MerchantFetchResult, error)
	// BackfillInvoices reopens these orders' invoice pages with the kept
	// session, in order and paced, and hands each over as it goes: a nil
	// invoice is a page that gave nothing. It never signs in.
	BackfillInvoices(ctx context.Context, merchant domain.MerchantID, storageState json.RawMessage, orders []string, each func(orderNumber string, invoice *provider.MerchantInvoice)) (provider.MerchantBackfillResult, error)
	// LayOutReceipt prints a stored purchase as its receipt document, with no
	// request to the merchant, and names the file.
	LayOutReceipt(receipt provider.MerchantReceipt) (string, []byte, error)
}

// HasAgent says a browser is available; without one the connector takes
// files only.
func (a *Merchants) HasAgent() bool { return a.Agent != nil && a.Agent.Available() }

func (a *Merchants) agent() (MerchantAgent, error) {
	if !a.HasAgent() {
		return nil, provider.ErrMerchantAgentUnavailable
	}
	return a.Agent, nil
}

func (a *Merchants) Health(ctx context.Context, merchant domain.MerchantID) error {
	agent, err := a.agent()
	if err != nil {
		return err
	}
	return agent.Health(ctx, merchant)
}
