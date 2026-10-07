package connector

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billers"
	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// A provider deployed once per customer, aimed or refused. The slug is
// invented.

const siteTestSlug = "exampletown"

func TestAConnectionThatNamesNoDeploymentIsRefusedBeforeABrowserOpens(t *testing.T) {
	engine := testEngine(t, billers.NewCommunityConnect(), &browser.StubPage{})

	_, err := engine.StartLiveConnect(context.Background(),
		"community-connect", "connection-1", "", 900, 700)
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
	require.ErrorContains(t, err, "deployed once per customer")
	require.ErrorContains(t, err, "has not been told which")

	// The same refusal on the unattended paths, so a nightly pull says what is
	// missing rather than reporting a sign-in the household does not owe.
	_, err = engine.Pull(context.Background(), provider.BillPullRequest{
		Provider: "community-connect", Profile: "connection-1",
	})
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)

	_, err = engine.Keepalive(context.Background(),
		"community-connect", "connection-1", "", "")
	require.ErrorIs(t, err, provider.ErrAgentBadRequest)
}

func TestASteerStaysOnTheDeploymentWhereItIsNotUnderTheCatalogueHome(t *testing.T) {
	var engine Bills
	aimed := billers.NewMyChart().WithSite("https://mychart.examplehealth.example/MyChart/Home")
	require.Equal(t, "https://mychart.examplehealth.example/MyChart", engine.home(aimed))
	require.Empty(t, engine.home(billers.NewMyChart()), "an unaimed portal is nowhere to steer")
	require.Equal(t, "https://www.ourcommunityconnect.com",
		engine.home(billers.NewCommunityConnect().WithSite(siteTestSlug)))
}

func TestASignInOpensTheDeploymentTheConnectionNames(t *testing.T) {
	page := &browser.StubPage{}
	engine := testEngine(t, billers.NewCommunityConnect(), page)

	state, err := engine.StartConnect(context.Background(), provider.BillConnectStart{
		Provider: "community-connect", Profile: "connection-1", Site: siteTestSlug,
		Username: "someone@example.test", Password: "invented",
	})
	require.NoError(t, err)
	state = awaitSignIn(t, engine, state)
	require.NotEmpty(t, page.Visited)
	require.Equal(t, "https://"+siteTestSlug+".ourcommunityconnect.com/", page.Visited[0])
	require.NoError(t, engine.CancelSignIn(context.Background(), state.SessionID))
}
