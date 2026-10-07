package agent

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

func TestAFailureCarriesThePageItHappenedOn(t *testing.T) {
	covered := errors.New("something on the page covered the button")
	page := &browser.StubPage{OnScreen: func() ([]byte, error) { return []byte("the page"), nil }}

	err := Pictured(page, covered)
	require.ErrorIs(t, err, covered, "the error is still the one that happened")
	require.Equal(t, covered.Error(), err.Error(), "and reads the same")
	require.Equal(t, []byte("the page"), provider.ScreenshotOf(err))

	refused := &provider.AgentError{Kind: provider.ErrAgentFailed, Message: "refused"}
	require.ErrorIs(t, Pictured(page, refused), provider.ErrAgentFailed, "a kind survives the picture")
	require.Equal(t, "refused", provider.AgentMessage(Pictured(page, refused)))
}

func TestAFailureIsPicturedOnceAndOnlyWhenThereIsAPage(t *testing.T) {
	covered := errors.New("covered")
	shots := 0
	page := &browser.StubPage{OnScreen: func() ([]byte, error) {
		shots++
		return []byte("the page"), nil
	}}

	require.NoError(t, Pictured(page, nil), "no error, no picture")
	require.Zero(t, shots)
	require.Same(t, covered, Pictured(nil, covered), "no page to show")

	once := Pictured(page, covered)
	require.Same(t, once, Pictured(page, once), "the first page an error met is the one it failed on")
	require.Equal(t, 1, shots)

	blank := &browser.StubPage{OnScreen: func() ([]byte, error) { return nil, errors.New("navigating") }}
	require.Same(t, covered, Pictured(blank, covered), "a page that cannot be pictured leaves the error alone")
	require.Nil(t, provider.ScreenshotOf(covered))
}

func TestAHeldBrowserPicturesItsPageUntilItIsTaken(t *testing.T) {
	covered := errors.New("covered")
	var hold Hold
	require.Same(t, covered, hold.Pictured(covered), "nothing held")

	hold.Attach(&Browser{Page: &browser.StubPage{}})
	require.NotNil(t, provider.ScreenshotOf(hold.Pictured(covered)))

	hold.Take()
	require.Same(t, covered, hold.Pictured(covered), "a closed browser has no page")
}
