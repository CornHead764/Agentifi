package provider

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newsJSON is one Yahoo search response carrying only the news half.
func newsJSON(stories ...string) string {
	body := ""
	for i, one := range stories {
		if i > 0 {
			body += ","
		}
		body += one
	}
	return `{"quotes":[],"news":[` + body + `]}`
}

func story(uuid, title string, published int64) string {
	return fmt.Sprintf(`{"uuid":%q,"title":%q,"publisher":"Reuters",
		"link":"https://example.test/%s","providerPublishTime":%d,
		"thumbnail":{"resolutions":[
			{"url":"https://img.test/large.jpg","width":800,"height":600},
			{"url":"https://img.test/small.jpg","width":140,"height":140}]},
		"relatedTickers":["AAPL"]}`, uuid, title, uuid, published)
}

func TestNewsReturnsTheStoriesNewestFirst(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/finance/search", r.URL.Path)
		require.Equal(t, "0", r.URL.Query().Get("quotesCount"))
		require.NotEqual(t, "0", r.URL.Query().Get("newsCount"))
		fmt.Fprint(w, newsJSON(
			story("a", "Older story", 1_700_000_000),
			story("b", "Newer story", 1_700_009_999),
		))
	})

	items, err := yahoo.News(context.Background(), []string{"AAPL"}, 10)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "Newer story", items[0].Title)
	require.Equal(t, "Older story", items[1].Title)
	require.Equal(t, "Reuters", items[0].Publisher)
	require.Equal(t, "https://example.test/b", items[0].URL)
	require.Equal(t, time.Unix(1_700_009_999, 0).UTC(), items[0].PublishedAt)
}

func TestNewsPicksTheSmallestThumbnail(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, newsJSON(story("a", "Story", 1_700_000_000)))
	})
	items, err := yahoo.News(context.Background(), []string{"AAPL"}, 10)
	require.NoError(t, err)
	require.Equal(t, "https://img.test/small.jpg", items[0].ThumbnailURL)
}

func TestOneStoryFoundThroughSeveralTickersCollapsesToOneCard(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, newsJSON(story("shared", "The chip market", 1_700_000_000)))
	})

	items, err := yahoo.News(context.Background(), []string{"NVDA", "AMD", "INTC"}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, []string{"AMD", "INTC", "NVDA"}, items[0].Symbols)
}

func TestNewsAsksAboutAtMostTwelveTickers(t *testing.T) {
	var mu sync.Mutex
	asked := map[string]bool{}
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		asked[r.URL.Query().Get("q")] = true
		mu.Unlock()
		fmt.Fprint(w, newsJSON())
	})

	symbols := make([]string, 0, 60)
	for i := range 60 {
		symbols = append(symbols, fmt.Sprintf("TICK%d", i))
	}
	_, err := yahoo.News(context.Background(), symbols, 10)
	require.NoError(t, err)
	require.Len(t, asked, maxNewsSymbols)
}

func TestNewsHonoursTheLimit(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, newsJSON(
			story("a", "One", 1_700_000_001),
			story("b", "Two", 1_700_000_002),
			story("c", "Three", 1_700_000_003),
		))
	})
	items, err := yahoo.News(context.Background(), []string{"AAPL"}, 2)
	require.NoError(t, err)
	require.Len(t, items, 2)
	require.Equal(t, "Three", items[0].Title)
}

func TestAStoryWithNoLinkOrIdIsDropped(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `{"news":[
			{"uuid":"a","title":"No link","link":"","providerPublishTime":1700000000},
			{"uuid":"","title":"No id","link":"https://example.test/x","providerPublishTime":1700000000},
			{"uuid":"c","title":"","link":"https://example.test/c","providerPublishTime":1700000000},
			{"uuid":"d","title":"Usable","publisher":"AP","link":"https://example.test/d","providerPublishTime":1700000000}]}`)
	})
	items, err := yahoo.News(context.Background(), []string{"AAPL"}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "Usable", items[0].Title)
}

func TestOneThrottledTickerDoesNotLoseTheRest(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("q") == "AAPL" {
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		fmt.Fprint(w, newsJSON(story("b", "Still here", 1_700_000_000)))
	})
	items, err := yahoo.News(context.Background(), []string{"AAPL", "MSFT"}, 10)
	require.NoError(t, err)
	require.Len(t, items, 1)
	require.Equal(t, "Still here", items[0].Title)
}

// Every ticker throttled is a failure, not an empty feed.
func TestEveryTickerThrottledIsReportedAsAFailure(t *testing.T) {
	yahoo := yahooServer(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusTooManyRequests)
	})
	_, err := yahoo.News(context.Background(), []string{"AAPL", "MSFT"}, 10)
	require.ErrorIs(t, err, ErrRateLimited)
}

func TestNewsWithNoSymbolsAsksNothing(t *testing.T) {
	yahoo := yahooServer(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("the news lookup should not reach the network with no symbols")
	})
	items, err := yahoo.News(context.Background(), nil, 10)
	require.NoError(t, err)
	require.Empty(t, items)
}

type countingNews struct {
	mu    sync.Mutex
	calls int
	items []NewsItem
	err   error
}

func (c *countingNews) News(context.Context, []string, int) ([]NewsItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.calls++
	return c.items, c.err
}

func TestASecondReadInsideTheWindowDoesNotReachTheSource(t *testing.T) {
	inner := &countingNews{items: []NewsItem{{ID: "a", Title: "Story"}}}
	clock := time.Unix(1_700_000_000, 0)
	cache := &CachedNews{Inner: inner, TTL: time.Minute, Now: func() time.Time { return clock }}

	first, err := cache.News(context.Background(), []string{"AAPL"}, 5)
	require.NoError(t, err)
	second, err := cache.News(context.Background(), []string{"AAPL"}, 5)
	require.NoError(t, err)

	require.Equal(t, first, second)
	require.Equal(t, 1, inner.calls)
}

func TestTheCacheExpires(t *testing.T) {
	inner := &countingNews{items: []NewsItem{{ID: "a"}}}
	clock := time.Unix(1_700_000_000, 0)
	cache := &CachedNews{Inner: inner, TTL: time.Minute, Now: func() time.Time { return clock }}

	_, err := cache.News(context.Background(), []string{"AAPL"}, 5)
	require.NoError(t, err)
	clock = clock.Add(2 * time.Minute)
	_, err = cache.News(context.Background(), []string{"AAPL"}, 5)
	require.NoError(t, err)
	require.Equal(t, 2, inner.calls)
}

func TestADifferentPortfolioIsADifferentCacheEntry(t *testing.T) {
	inner := &countingNews{}
	clock := time.Unix(1_700_000_000, 0)
	cache := &CachedNews{Inner: inner, TTL: time.Hour, Now: func() time.Time { return clock }}

	_, _ = cache.News(context.Background(), []string{"AAPL"}, 5)
	_, _ = cache.News(context.Background(), []string{"AAPL", "MSFT"}, 5)
	require.Equal(t, 2, inner.calls)

	// Order and case are not a different portfolio.
	_, _ = cache.News(context.Background(), []string{"msft", "aapl"}, 5)
	require.Equal(t, 2, inner.calls)
}

func TestAFailureIsCachedSoTheSourceIsNotHammered(t *testing.T) {
	inner := &countingNews{err: ErrMarketPriceRateLimited}
	clock := time.Unix(1_700_000_000, 0)
	cache := &CachedNews{Inner: inner, TTL: time.Minute, Now: func() time.Time { return clock }}

	_, err := cache.News(context.Background(), []string{"AAPL"}, 5)
	require.ErrorIs(t, err, ErrRateLimited)
	_, err = cache.News(context.Background(), []string{"AAPL"}, 5)
	require.ErrorIs(t, err, ErrRateLimited)
	require.Equal(t, 1, inner.calls)
}

func TestACacheWithNoSourceAnswersEmpty(t *testing.T) {
	cache := &CachedNews{}
	items, err := cache.News(context.Background(), []string{"AAPL"}, 5)
	require.NoError(t, err)
	require.Empty(t, items)
}
