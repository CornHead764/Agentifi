package api

import (
	"context"
	"errors"
	"net/http"

	"connectrpc.com/connect"
	"google.golang.org/protobuf/types/known/timestamppb"

	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
	"github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1/agentifiv1connect"
	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Related news: the stories behind the tickers this household holds.
//
// A source that publishes no stories, and one that is throttling, are answers
// rather than errors: both render as an absent carousel, and the investments
// screen's numbers do not depend on this.

func init() {
	RegisterService(func(env *Env, opts ...connect.HandlerOption) (string, http.Handler) {
		return agentifiv1connect.NewNewsServiceHandler(newsService{env}, opts...)
	})
}

type newsService struct{ env *Env }

// maxNewsItems is what one response carries.
const maxNewsItems = 20

func (s newsService) ListNews(ctx context.Context, _ *agentifiv1.ListNewsRequest) (*agentifiv1.ListNewsResponse, error) {
	if s.env.News == nil {
		return &agentifiv1.ListNewsResponse{}, nil
	}

	symbols, err := s.env.DB.HeldSymbols(ctx, spaceFrom(ctx).ID())
	if err != nil {
		return nil, err
	}

	out := &agentifiv1.ListNewsResponse{Available: true, Symbols: symbols}
	if len(symbols) == 0 {
		return out, nil
	}

	items, err := s.env.News.News(ctx, symbols, maxNewsItems)
	if err != nil {
		// A throttled or unreachable source is not a failed request: a 502
		// would take the whole screen down for a carousel.
		if errors.Is(err, provider.ErrRateLimited) {
			out.Unavailable = "the news source is busy; try again shortly"
		} else {
			out.Unavailable = "the news source could not be reached"
		}
		return out, nil
	}

	for _, one := range items {
		item := &agentifiv1.NewsItem{
			Id:           one.ID,
			Title:        one.Title,
			Publisher:    one.Publisher,
			Url:          one.URL,
			ThumbnailUrl: one.ThumbnailURL,
			Symbols:      one.Symbols,
		}
		if !one.PublishedAt.IsZero() {
			item.PublishedAt = timestamppb.New(one.PublishedAt)
		}
		out.Items = append(out.Items, item)
	}
	return out, nil
}
