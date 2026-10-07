package connector

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/merchants"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/importer/merchantimport"
)

// Which browser a merchant runs in, and the session a Camoufox sign-in keeps.

// firefoxFake is a merchant whose sign-in only works in Firefox.
type firefoxFake struct {
	*fakeModule
	session json.RawMessage
}

func (f *firefoxFake) RunsInFirefox() bool { return true }

func (f *firefoxFake) FirefoxOrigin() (string, string) { return "https://merchant.test", "/" }

func (f *firefoxFake) SessionFromPage(page browser.Page, at time.Time) (json.RawMessage, bool, error) {
	return f.session, len(f.session) > 0, nil
}

// fakeFetcher is a caller that records it was used and let go.
type fakeFetcher struct{ calls, released int }

func (f *fakeFetcher) Do(req *http.Request) (*http.Response, error) {
	f.calls++
	return nil, nil
}

func withFirefox(engine Merchants, firefox *openedBrowser, fetcher *fakeFetcher) {
	engine.OpenFirefox = firefox.opener()
	engine.Fetcher = func(firefox bool, origin, document string) (browser.Fetcher, func(), error) {
		if !firefox || origin != "https://merchant.test" {
			return nil, nil, fmt.Errorf("asked for a caller at %q, in Firefox %v", origin, firefox)
		}
		return fetcher, func() { fetcher.released++ }, nil
	}
}

func TestATypedSignInAtAFirefoxMerchantOpensCamoufox(t *testing.T) {
	module := &firefoxFake{fakeModule: &fakeModule{
		id: domain.MerchantCostco, states: []merchants.State{{State: merchants.StateSignedIn}},
	}}
	chrome, firefox := stubBrowser(), stubBrowser()
	engine, _ := engineWith(t, module, chrome)
	withFirefox(engine, firefox, &fakeFetcher{})

	_, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")

	require.NoError(t, err)
	require.Equal(t, 1, firefox.opens)
	require.Equal(t, 0, chrome.opens)
}

func TestAMerchantThatIsNotAFirefoxOneStaysInChrome(t *testing.T) {
	module := &fakeModule{states: []merchants.State{{State: merchants.StateSignedIn}}}
	chrome, firefox := stubBrowser(), stubBrowser()
	engine, _ := engineWith(t, module, chrome)
	withFirefox(engine, firefox, &fakeFetcher{})

	_, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")

	require.NoError(t, err)
	require.Equal(t, 1, chrome.opens)
	require.Equal(t, 0, firefox.opens)
}

// With no Camoufox server configured a Camoufox merchant is told so, and never
// opened in the Chrome its site refuses.
func TestAFirefoxMerchantWithNoCamoufoxIsRefusedRatherThanRunInChrome(t *testing.T) {
	module := &firefoxFake{fakeModule: &fakeModule{
		id: domain.MerchantCostco, states: []merchants.State{{State: merchants.StateSignedIn}},
		kinds: []string{"costco-b2c"},
	}}
	chrome := stubBrowser()
	engine, _ := engineWith(t, module, chrome)

	_, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.ErrorIs(t, err, browser.ErrNoFirefox)

	_, err = engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"costco-b2c","refresh_token":"old"}`), 30, nil, nil, nil)
	require.ErrorIs(t, err, browser.ErrNoFirefox)

	require.Equal(t, 0, chrome.opens)
}

// A handed-over session's calls go through the Camoufox page, and the page is
// let go when the pull is done.
func TestAHandedOverSessionAtAFirefoxMerchantCallsThroughCamoufox(t *testing.T) {
	fetcher := &fakeFetcher{}
	module := &firefoxFake{fakeModule: &fakeModule{
		id: domain.MerchantCostco, kinds: []string{"costco-b2c"},
		fetch: func(call merchants.Call) (merchants.Result, error) {
			require.Same(t, fetcher, call.HTTP)
			return merchants.Result{Parsed: &merchantimport.Parsed{}}, nil
		},
	}}
	chrome, firefox := stubBrowser(), stubBrowser()
	engine, _ := engineWith(t, module, chrome)
	withFirefox(engine, firefox, fetcher)

	_, err := engine.Fetch(context.Background(), module.ID(),
		json.RawMessage(`{"kind":"costco-b2c","refresh_token":"old"}`), 30, nil, nil, nil)

	require.NoError(t, err)
	require.Equal(t, 1, fetcher.released)
	require.Equal(t, 0, chrome.opens+firefox.opens, "the calls need a page, not a pull's browser")
}

// A merchant whose page holds a session keeps that session, not the jar.
func TestCompleteKeepsTheSessionThePageHolds(t *testing.T) {
	module := &firefoxFake{
		fakeModule: &fakeModule{id: domain.MerchantCostco, states: []merchants.State{{State: merchants.StateSignedIn}}},
		session:    json.RawMessage(`{"kind":"costco-b2c","refresh_token":"from-the-page"}`),
	}
	chrome, firefox := stubBrowser(), stubBrowser()
	engine, _ := engineWith(t, module, chrome)
	withFirefox(engine, firefox, &fakeFetcher{})
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)

	kept, hint, err := engine.CompleteSignIn(context.Background(), state.SessionID)

	require.NoError(t, err)
	require.JSONEq(t, `{"kind":"costco-b2c","refresh_token":"from-the-page"}`, string(kept))
	require.Equal(t, "Alex", hint)
}

// A page with no session in it keeps the jar.
func TestCompleteKeepsTheJarWhenThePageHoldsNoSession(t *testing.T) {
	module := &firefoxFake{fakeModule: &fakeModule{
		id: domain.MerchantCostco, states: []merchants.State{{State: merchants.StateSignedIn}},
	}}
	chrome, firefox := stubBrowser(), stubBrowser()
	engine, _ := engineWith(t, module, chrome)
	withFirefox(engine, firefox, &fakeFetcher{})
	state, err := engine.StartSignIn(context.Background(), module.ID(), "a@example.test", "p")
	require.NoError(t, err)

	kept, _, err := engine.CompleteSignIn(context.Background(), state.SessionID)

	require.NoError(t, err)
	require.JSONEq(t, firefox.jar, string(kept))
}

// A session read out of Costco's MSAL cache is a handed-over one: its pull
// opens no browser.
func TestACostcoSessionFromTheCacheIsHandedOver(t *testing.T) {
	cache := merchants.CostcoMSALCache{Refresh: `{"secret":"invented-refresh","realm":"realm-tenant"}`}
	session, found := merchants.CostcoSessionFromCache(cache, time.Date(2026, 9, 23, 0, 0, 0, 0, time.UTC))
	require.True(t, found)

	kind, handedOver := handedOverKind(session)
	require.True(t, handedOver)
	require.Equal(t, "costco-b2c", kind)
}
