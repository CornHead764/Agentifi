package service

import (
	"context"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// BillsAgent is what service.Bills asks of a bills engine. It is an interface so
// tests can supply an engine and service/ need not import the Chromium driver.
type BillsAgent interface {
	Available() bool
	Health(ctx context.Context) error
	Providers(ctx context.Context) ([]provider.BillProvider, error)

	StartConnect(ctx context.Context, request provider.BillConnectStart) (provider.BillConnectState, error)
	StartLiveConnect(ctx context.Context, providerID, profile, site string, width, height int) (provider.BillConnectState, error)
	ConnectStatus(ctx context.Context, sessionID string) (provider.BillConnectState, error)
	ConnectTrail(ctx context.Context, sessionID string) ([]provider.BillTrailEntry, error)
	AnswerConnect(ctx context.Context, sessionID, code string) (provider.BillConnectState, error)
	CancelSignIn(ctx context.Context, sessionID string) error
	// SignInInput plays a person's clicks into a sign-in parked on a page
	// check, in its live view.
	SignInInput(ctx context.Context, sessionID string, events []provider.BillLiveInput) error
	CompleteConnect(ctx context.Context, sessionID string) (provider.BillConnectComplete, error)

	// The four developer steers, over a sign-in somebody is already sitting at.
	// The engine will not steer the browser off the provider's own site.
	SteerTo(ctx context.Context, sessionID, address string) (provider.BillSignInSteer, error)
	SteerClick(ctx context.Context, sessionID, text, selector string) (provider.BillSignInSteer, error)
	SteerDOM(ctx context.Context, sessionID, selector string, limit int) (provider.BillSignInDOM, error)
	SteerFetch(ctx context.Context, sessionID string, request provider.BillSignInFetchRequest) (provider.BillSignInFetch, error)

	Pull(ctx context.Context, request provider.BillPullRequest) (provider.BillPull, error)
	ResumePull(ctx context.Context, sessionID string) (provider.BillPull, error)
	FetchDocument(ctx context.Context, ref string) ([]byte, string, string, error)
	Keepalive(ctx context.Context, providerID, profile, site, sessionState string) (provider.BillKeepalive, error)
	ForgetProfile(ctx context.Context, profile string) error
	// ReleaseProfile lets go of a connection's browser (this process's session and
	// any hold a dead one left on the volume) but keeps the profile, unlike forget.
	ReleaseProfile(ctx context.Context, profile string) (provider.BillProfileRelease, error)
}

func (b *Bills) HasAgent() bool { return b.Agent != nil && b.Agent.Available() }

// agent is the engine, or the refusal every connect answers without one.
func (b *Bills) agent() (BillsAgent, error) {
	if !b.HasAgent() {
		return nil, provider.ErrBillsAgentUnavailable
	}
	return b.Agent, nil
}
