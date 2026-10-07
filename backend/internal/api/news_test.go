package api

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Related news.
//
// The property under test throughout is that the carousel is never allowed to
// break the investments screen. The portfolio's numbers do not come from this
// source, so a source that is missing, throttling or unreachable has to render
// as an absent carousel and a 200 — not as a failed page.

// fakeNews stands in for the market source so no test reaches the network.
type fakeNews struct {
	asked []string
	items []provider.NewsItem
	err   error
}

func (f *fakeNews) News(_ context.Context, symbols []string, _ int) ([]provider.NewsItem, error) {
	f.asked = symbols
	return f.items, f.err
}

// newsClient points the ledger's environment at a source under the test's
// control. The default environment carries the real Yahoo provider, and a test
// that used it would be a network call in the suite.
func newsClient(l *ledger, source provider.MarketNewsProvider) *client {
	l.t.Helper()
	env := NewEnv(testConfig(), db(l.t), WithNews(source))
	return (&client{t: l.t, env: env, handler: RouterFor(env)}).
		as(l.users["alex"]).inSpace(store.SpaceIDOf(l.id("space")))
}

func newsFor(l *ledger, source provider.MarketNewsProvider) map[string]any {
	l.t.Helper()
	return newsClient(l, source).get("/news").requireStatus(http.StatusOK).json()
}

func TestTheCarouselCarriesTheStoriesForTheHeldTickers(t *testing.T) {
	l := buildPortfolioLedger(t)
	published := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
	source := &fakeNews{items: []provider.NewsItem{{
		ID: "one", Title: "AAA beats expectations", Publisher: "Reuters",
		URL: "https://example.test/one", ThumbnailURL: "https://img.test/one.jpg",
		Symbols: []string{"AAA"}, PublishedAt: published,
	}}}

	body := newsFor(l, source)
	require.Equal(t, true, body["available"])
	require.Equal(t, "", body["unavailable"])
	require.ElementsMatch(t, []string{"AAA", "BBB"}, source.asked)

	items := body["items"].([]any)
	require.Len(t, items, 1)
	first := items[0].(map[string]any)
	require.Equal(t, "AAA beats expectations", first["title"])
	require.Equal(t, "Reuters", first["publisher"])
	require.Equal(t, "https://example.test/one", first["url"])
	require.Equal(t, []any{"AAA"}, first["symbols"])
	require.Equal(t, "2026-08-20T12:00:00Z", first["published_at"])
}

// A closed position is not this portfolio's news. The row stays for the
// transaction history; the ticker is not something to fetch stories about.
func TestASecurityWithNoLivePositionIsNotAskedAbout(t *testing.T) {
	l := buildPortfolioLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	sold := seedSecurity(t, space, "ZZZ", stringPtrOf("10.00"), nil)
	seedHolding(t, space, l.id("brokerage"), sold, "0", nil, false)

	source := &fakeNews{}
	newsFor(l, source)
	require.NotContains(t, source.asked, "ZZZ")
}

func TestAHouseholdThatHoldsNothingAsksForNoNews(t *testing.T) {
	l := buildLedger(t)
	source := &fakeNews{}
	body := newsFor(l, source)

	require.Empty(t, source.asked, "the source should not be reached with no holdings")
	require.Empty(t, body["items"])
	require.Equal(t, true, body["available"])
	require.Empty(t, body["symbols"])
}

// A throttled source says so, and still answers 200: taking the investments
// screen down over a carousel would lose every figure on it.
func TestAThrottledSourceLeavesTheScreenStanding(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := newsFor(l, &fakeNews{err: provider.ErrMarketPriceRateLimited})

	require.Equal(t, true, body["available"])
	require.Contains(t, body["unavailable"], "busy")
	require.Empty(t, body["items"])
}

func TestAnUnreachableSourceAlsoLeavesTheScreenStanding(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := newsFor(l, &fakeNews{err: context.DeadlineExceeded})

	require.Equal(t, true, body["available"])
	require.Contains(t, body["unavailable"], "could not be reached")
}

// A deployment with the carousel switched off reports that, so the screen can
// drop the section rather than draw an empty one that reads as "no news".
func TestADeploymentWithNoNewsSourceSaysSo(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := newsFor(l, nil)
	require.Equal(t, false, body["available"])
	require.Empty(t, body["items"])
}

// Another household's holdings are not this one's tickers.
func TestAnotherHouseholdsTickersAreNotAskedAbout(t *testing.T) {
	l := buildPortfolioLedger(t)
	other := store.SpaceIDOf(l.id("other_space"))
	strangerAccount := seedInvestmentAccount(t, other, "Their Brokerage", stringPtrOf("500.00"))
	stranger := seedSecurity(t, other, "SECRET", stringPtrOf("10.00"), nil)
	seedHolding(t, other, strangerAccount, stranger, "5", nil, false)

	source := &fakeNews{}
	newsFor(l, source)
	require.NotContains(t, source.asked, "SECRET")
}

// A story the source dated with nothing usable still renders; the card simply
// carries no timestamp.
func TestAnUndatedStoryStillReachesTheCarousel(t *testing.T) {
	l := buildPortfolioLedger(t)
	body := newsFor(l, &fakeNews{items: []provider.NewsItem{{
		ID: "one", Title: "Undated", URL: "https://example.test/one",
	}}})

	first := body["items"].([]any)[0].(map[string]any)
	require.Nil(t, first["published_at"])
	require.Equal(t, []any{}, first["symbols"])
}

func TestAViewerCanReadTheCarousel(t *testing.T) {
	l := buildPortfolioLedger(t)
	env := NewEnv(testConfig(), db(t), WithNews(&fakeNews{}))
	viewer := (&client{t: t, env: env, handler: RouterFor(env)}).
		as(l.users["vera"]).inSpace(store.SpaceIDOf(l.id("space")))
	viewer.get("/news").requireStatus(http.StatusOK)
}

// A security with no price on file is a private fund or an import artefact —
// an employer's target-date fund, a provider's internal id. There is no public
// news about it, and asking returns whatever the source's search makes of the
// string, which then arrives looking like news about a holding.
func TestASecurityWithNoQuoteIsNotAskedAbout(t *testing.T) {
	l := buildPortfolioLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	unquoted := seedSecurity(t, space, "NW60", nil, nil)
	seedHolding(t, space, l.id("brokerage"), unquoted, "100", nil, false)

	source := &fakeNews{}
	newsFor(l, source)
	require.NotContains(t, source.asked, "NW60")
	require.ElementsMatch(t, []string{"AAA", "BBB"}, source.asked)
}

// The lookup is capped, so the order decides which holdings get a carousel.
// By value, not by symbol: sorted alphabetically the cap would drop the
// largest positions in favour of two rows that sorted earlier.
func TestTheBiggestPositionsAreAskedAboutFirst(t *testing.T) {
	l := buildPortfolioLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	// AAA is 10 × 100 = 1,000 and BBB is 20 × 50 = 1,000; this one is 1 × 5
	// and sorts first alphabetically.
	tiny := seedSecurity(t, space, "AAAA", stringPtrOf("5.00"), nil)
	seedHolding(t, space, l.id("brokerage"), tiny, "1", nil, false)

	source := &fakeNews{}
	newsFor(l, source)
	require.Len(t, source.asked, 3)
	require.Equal(t, "AAAA", source.asked[2], "the smallest position should be asked about last")
}
