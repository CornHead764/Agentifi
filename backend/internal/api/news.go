package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Related news: the stories behind the tickers this household holds.
//
// A source that publishes no stories, and one that is throttling, are answers
// rather than errors: both render as an absent carousel, and the investments
// screen's numbers do not depend on this.

func init() {
	Register(Resource{Prefix: "/news", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listNews)
	}})
}

// maxNewsItems is what one response carries.
const maxNewsItems = 20

type NewsItemResponse struct {
	ID        string `json:"id"`
	Title     string `json:"title"`
	Publisher string `json:"publisher"`
	URL       string `json:"url"`
	// ThumbnailURL is empty when the source published no image; the card draws
	// a placeholder rather than a broken one.
	ThumbnailURL string `json:"thumbnail_url"`
	// Symbols are the held tickers the story was found through.
	Symbols []string `json:"symbols"`
	// PublishedAt is null for a story the source dated with nothing usable.
	PublishedAt *time.Time `json:"published_at"`
}

// NewsResponse is the carousel.
type NewsResponse struct {
	Items []NewsItemResponse `json:"items"`
	// Available is false when this deployment has no news source; the screen
	// hides the carousel rather than showing an empty one.
	Available bool `json:"available"`
	// Unavailable explains a source that exists but would not answer, so the
	// card can say "try again shortly" rather than claiming there is no news.
	Unavailable string `json:"unavailable"`
	// Symbols are the tickers asked about, so the empty state can say whether
	// the household simply holds nothing.
	Symbols []string `json:"symbols"`
}

func listNews(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if env.News == nil {
		return writeJSON(w, http.StatusOK, NewsResponse{Items: []NewsItemResponse{}, Symbols: []string{}})
	}

	symbols, err := env.DB.HeldSymbols(r.Context(), sp.ID())
	if err != nil {
		return err
	}

	out := NewsResponse{Items: []NewsItemResponse{}, Available: true, Symbols: symbols}
	if len(symbols) == 0 {
		return writeJSON(w, http.StatusOK, out)
	}

	items, err := env.News.News(r.Context(), symbols, maxNewsItems)
	if err != nil {
		// A throttled or unreachable source is not a failed request: a 502
		// would take the whole screen down for a carousel.
		if errors.Is(err, provider.ErrRateLimited) {
			out.Unavailable = "the news source is busy; try again shortly"
		} else {
			out.Unavailable = "the news source could not be reached"
		}
		return writeJSON(w, http.StatusOK, out)
	}

	for _, one := range items {
		item := NewsItemResponse{
			ID:           one.ID,
			Title:        one.Title,
			Publisher:    one.Publisher,
			URL:          one.URL,
			ThumbnailURL: one.ThumbnailURL,
			Symbols:      one.Symbols,
		}
		if !one.PublishedAt.IsZero() {
			when := one.PublishedAt
			item.PublishedAt = &when
		}
		if item.Symbols == nil {
			item.Symbols = []string{}
		}
		out.Items = append(out.Items, item)
	}
	return writeJSON(w, http.StatusOK, out)
}
