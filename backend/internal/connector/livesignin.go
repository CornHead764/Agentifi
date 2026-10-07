package connector

import (
	"context"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// The live sign-in: the person signs in by hand in a browser they can see,
// over internal/browser's LiveView.

// StartLiveConnect opens a browser the person drives themself.
func (e Bills) StartLiveConnect(
	ctx context.Context, providerID, profile, site string, width, height int,
) (provider.BillConnectState, error) {
	module, err := e.pick(providerID)
	if err != nil {
		return provider.BillConnectState{}, err
	}
	if module, err = e.aimed(module, site); err != nil {
		return provider.BillConnectState{}, err
	}
	browserModule, ok := module.(billers.BrowserModule)
	if !ok {
		return provider.BillConnectState{}, agentError(provider.ErrAgentBadRequest,
			"%s has no sign-in page; send a username and password", billerName(module))
	}
	if browser.RunsInFirefox(module) {
		return provider.BillConnectState{}, agentError(provider.ErrAgentBadRequest,
			"%s signs in with a username and password only: its site does not accept the "+
				"browser a person signs in through", billerName(module))
	}
	return guarded(e.Engine, billerName(module), "the live sign-in", func() (provider.BillConnectState, error) {
		return e.startLiveConnect(browserModule, profile, site,
			browser.Size{Width: width, Height: height})
	})
}

func (e Bills) startLiveConnect(
	module billers.BrowserModule, profile, site string, asked browser.Size,
) (provider.BillConnectState, error) {
	s := e.open(module)
	s.profile = profile
	s.site = site
	s.live = true
	opened, err := e.Open(agent.Open{
		Profile: profile, Viewport: browser.ClampViewport(asked), Note: s.notes.Trace,
	})
	if err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}
	s.Attach(opened)
	if opened.Page == nil || opened.Live == nil {
		e.close(s)
		return provider.BillConnectState{}, agentError(provider.ErrAgentFailed,
			"the browser opened with no page to sign in on")
	}
	view, err := opened.Live()
	if err != nil {
		e.close(s)
		return provider.BillConnectState{}, err
	}
	closeBrowser := opened.Close
	opened.Close = func() {
		view.Close()
		if closeBrowser != nil {
			closeBrowser()
		}
	}
	// A page that will not load is still worth showing the person.
	_ = opened.Page.Goto(module.SignInURL())

	state := e.state(s, billers.State{
		State: billers.StateInteractive, Prompt: module.SignInPrompt(),
	})
	state.Width, state.Height = view.Viewport().Width, view.Viewport().Height
	return state, nil
}

// liveState is where the page stands, classified at most every
// browser.ClassifyEvery. A page that cannot be read is `interactive`: the
// person is driving, and a failure said over a sign-in going fine would be
// wrong.
func (e *Engine) liveState(s *session) billers.State {
	module, ok := s.module.(billers.BrowserModule)
	opened := s.Opened()
	if !ok || opened == nil || opened.Page == nil {
		return billers.State{State: billers.StateInteractive}
	}
	now := e.now()
	if read, at := s.reading(); !at.IsZero() && now.Sub(at) < browser.ClassifyEvery {
		return read
	}
	where, err := module.Classify(opened.Page)
	if err != nil {
		where = billers.State{State: billers.StateInteractive}
	}
	// In a live browser the person answers a factor menu themself.
	if where.State == billers.StateFactor || where.State == "" {
		where.State = billers.StateInteractive
	}
	s.record(where, now)
	return where
}
