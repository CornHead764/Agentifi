package connector

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/browser/agent"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A provider that runs in Camoufox runs there for every browser the engine
// opens for it, and every other provider stays in Chrome. Where no Camoufox
// server is configured it is refused, never opened in Chrome.
func TestTheEngineOpensCamoufoxForTheProvidersThatAskForIt(t *testing.T) {
	var opened []string
	opener := func(name string) agent.Opener {
		return func(agent.Open) (*OpenBrowser, error) {
			opened = append(opened, name)
			return &OpenBrowser{}, nil
		}
	}
	engine := &Engine{Open: opener("chrome"), OpenFirefox: opener("firefox")}
	firefox := billers.NewCommunityConnect().WithSite("town")
	chrome := billers.NewAlliant()

	_, _ = engine.opener(firefox, agent.Open{})
	_, _ = engine.opener(chrome, agent.Open{})
	engine.OpenFirefox = nil
	_, err := engine.opener(firefox, agent.Open{})

	require.ErrorIs(t, err, browser.ErrNoFirefox)
	require.Equal(t, []string{"firefox", "chrome"}, opened)
}

func TestACamoufoxProviderOffersOnlyTheTypedSignInAndRefusesALiveOne(t *testing.T) {
	registry := billers.New()
	for _, entry := range registry.Providers() {
		if entry.ID == string(domain.BillerCommunityConnect) {
			require.Equal(t, []string{"typed"}, entry.SignIn.Kinds)
		}
	}

	engine := Bills{&Engine{Billers: registry}}
	_, err := engine.StartLiveConnect(t.Context(), string(domain.BillerCommunityConnect), "profile", "town", 1024, 768)
	require.Error(t, err)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}
