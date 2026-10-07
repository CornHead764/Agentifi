package api

import (
	"time"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

// Options only tests pass to NewEnv.

func WithPrices(prices provider.MarketPriceProvider) Option {
	return func(e *Env) { e.Prices = prices }
}

func WithNews(news provider.MarketNewsProvider) Option {
	return func(e *Env) { e.News = news }
}

func WithClock(now func() time.Time) Option {
	return func(e *Env) { e.Now = now }
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}
