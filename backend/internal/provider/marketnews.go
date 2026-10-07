package provider

import (
	"context"
	"encoding/json"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MarketNewsProvider is separate from MarketPriceProvider so a price feed with
// no stories loses the carousel rather than the valuations.

type NewsItem struct {
	// ID collapses one story reached through several symbols.
	ID           string
	Title        string
	Publisher    string
	URL          string
	ThumbnailURL string
	// Symbols are the held tickers the story was found through.
	Symbols     []string
	PublishedAt time.Time
}

type MarketNewsProvider interface {
	// News returns at most limit stories, newest first.
	News(ctx context.Context, symbols []string, limit int) ([]NewsItem, error)
}

const (
	// Every symbol is its own request against a source that throttles.
	newsPerSymbol  = 4
	maxNewsSymbols = 12
)

type yahooNewsResponse struct {
	News []struct {
		UUID      string `json:"uuid"`
		Title     string `json:"title"`
		Publisher string `json:"publisher"`
		Link      string `json:"link"`
		// Epoch seconds; missing leaves the story undated, not 1970.
		ProviderPublishTime *json.Number `json:"providerPublishTime"`
		Thumbnail           struct {
			Resolutions []struct {
				URL    string `json:"url"`
				Width  int    `json:"width"`
				Height int    `json:"height"`
			} `json:"resolutions"`
		} `json:"thumbnail"`
		RelatedTickers []string `json:"relatedTickers"`
	} `json:"news"`
}

// News reads the same search endpoint as Search, with quotes switched off.
func (y *YahooProvider) News(ctx context.Context, symbols []string, limit int) ([]NewsItem, error) {
	unique := make([]string, 0, len(symbols))
	seen := map[string]bool{}
	for _, s := range symbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		unique = append(unique, s)
		if len(unique) == maxNewsSymbols {
			break
		}
	}
	if len(unique) == 0 {
		return nil, nil
	}
	if limit <= 0 {
		limit = yahooDefaultResults
	}

	concurrency := y.Concurrency
	if concurrency <= 0 {
		concurrency = yahooConcurrency
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		byID   = map[string]*NewsItem{}
		failed error
		tokens = make(chan struct{}, concurrency)
	)
	for _, symbol := range unique {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens <- struct{}{}
			defer func() { <-tokens }()

			stories, err := y.newsFor(ctx, symbol)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				// The first error surfaces only if no ticker found anything.
				if failed == nil {
					failed = err
				}
				return
			}
			for _, story := range stories {
				if held, ok := byID[story.ID]; ok {
					held.Symbols = append(held.Symbols, symbol)
					continue
				}
				story.Symbols = []string{symbol}
				byID[story.ID] = &story
			}
		}()
	}
	wg.Wait()

	if failed != nil && len(byID) == 0 {
		return nil, failed
	}

	out := make([]NewsItem, 0, len(byID))
	for _, story := range byID {
		sort.Strings(story.Symbols)
		out = append(out, *story)
	}
	// The title breaks ties so the order is stable across polls.
	sort.Slice(out, func(i, j int) bool {
		if !out[i].PublishedAt.Equal(out[j].PublishedAt) {
			return out[i].PublishedAt.After(out[j].PublishedAt)
		}
		return out[i].Title < out[j].Title
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (y *YahooProvider) newsFor(ctx context.Context, symbol string) ([]NewsItem, error) {
	var raw yahooNewsResponse
	err := y.get(ctx, "/v1/finance/search", url.Values{
		"q":                {symbol},
		"quotesCount":      {"0"},
		"newsCount":        {strconv.Itoa(newsPerSymbol)},
		"listsCount":       {"0"},
		"enableFuzzyQuery": {"false"},
	}, &raw)
	if err != nil {
		if err == errSymbolUnknown {
			return nil, nil
		}
		return nil, err
	}

	out := make([]NewsItem, 0, len(raw.News))
	for _, one := range raw.News {
		if one.Link == "" || one.UUID == "" || one.Title == "" {
			continue
		}
		out = append(out, NewsItem{
			ID:           one.UUID,
			Title:        one.Title,
			Publisher:    one.Publisher,
			URL:          one.Link,
			ThumbnailURL: smallestThumbnail(one.Thumbnail.Resolutions),
			PublishedAt:  epochSeconds(one.ProviderPublishTime),
		})
	}
	return out, nil
}

// smallestThumbnail: the carousel draws a 64-pixel square.
func smallestThumbnail(resolutions []struct {
	URL    string `json:"url"`
	Width  int    `json:"width"`
	Height int    `json:"height"`
}) string {
	best := ""
	bestWidth := 0
	for _, one := range resolutions {
		if one.URL == "" {
			continue
		}
		if best == "" || (one.Width > 0 && one.Width < bestWidth) {
			best, bestWidth = one.URL, one.Width
		}
	}
	return best
}

func epochSeconds(value *json.Number) time.Time {
	if value == nil {
		return time.Time{}
	}
	seconds, err := value.Int64()
	if err != nil || seconds <= 0 {
		return time.Time{}
	}
	return time.Unix(seconds, 0).UTC()
}

// CachedNews spares a hard-throttling source a request per ticker on every
// visit to the portfolio screen.
type CachedNews struct {
	Inner MarketNewsProvider
	// Zero TTL means DefaultNewsTTL.
	TTL time.Duration
	Now func() time.Time

	mu      sync.Mutex
	entries map[string]newsEntry
}

const DefaultNewsTTL = 15 * time.Minute

type newsEntry struct {
	items     []NewsItem
	fetchedAt time.Time
	// err is cached too, so a throttling source is not asked on every load.
	err error
}

func (c *CachedNews) now() time.Time {
	if c.Now == nil {
		return time.Now()
	}
	return c.Now()
}

func (c *CachedNews) ttl() time.Duration {
	if c.TTL <= 0 {
		return DefaultNewsTTL
	}
	return c.TTL
}

// News keys on the exact symbol set and limit, so a new holding misses the
// cache.
func (c *CachedNews) News(ctx context.Context, symbols []string, limit int) ([]NewsItem, error) {
	if c.Inner == nil {
		return nil, nil
	}
	key := newsKey(symbols, limit)

	c.mu.Lock()
	entry, held := c.entries[key]
	fresh := held && c.now().Sub(entry.fetchedAt) < c.ttl()
	c.mu.Unlock()
	if fresh {
		return entry.items, entry.err
	}

	items, err := c.Inner.News(ctx, symbols, limit)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = map[string]newsEntry{}
	}
	c.entries[key] = newsEntry{items: items, fetchedAt: c.now(), err: err}
	return items, err
}

func newsKey(symbols []string, limit int) string {
	normalized := make([]string, 0, len(symbols))
	for _, s := range symbols {
		if s = strings.ToUpper(strings.TrimSpace(s)); s != "" {
			normalized = append(normalized, s)
		}
	}
	sort.Strings(normalized)
	return strconv.Itoa(limit) + "|" + strings.Join(normalized, ",")
}
