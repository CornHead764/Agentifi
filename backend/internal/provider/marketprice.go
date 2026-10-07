package provider

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/shopspring/decimal"
)

type SymbolMatch struct {
	Symbol    string
	Name      string
	Exchange  string
	QuoteType string // EQUITY, ETF, CRYPTOCURRENCY, MUTUALFUND, ...
}

type SymbolQuote struct {
	Symbol    string
	Currency  string
	Price     domain.Rate
	Name      string
	Exchange  string
	QuoteType string
	// LogoURL is empty when the source exposes no company website.
	LogoURL string
}

type MarketPriceProvider interface {
	Name() string

	Search(ctx context.Context, query string, limit int) ([]SymbolMatch, error)

	// Quote answers nil, nil for an unknown symbol.
	Quote(ctx context.Context, symbol string) (*SymbolQuote, error)

	// LatestPrices leaves out a symbol it could not price rather than
	// answering zero, which would wipe a holding's value.
	LatestPrices(ctx context.Context, symbols []string) (map[string]domain.Rate, error)

	// History returns daily closes, oldest first. An empty series with a nil
	// error is an answer (a private fund has no public history).
	History(ctx context.Context, symbol string, from, to domain.Date) ([]domain.PricePoint, error)
}

// ErrMarketPriceRateLimited tells the caller to back off rather than treat the
// symbol as unpriceable.
var ErrMarketPriceRateLimited = fmt.Errorf("market price: %w", ErrRateLimited)

// minorUnitCurrencies are the quote source's non-ISO codes for prices in a
// minor unit; read as the major currency they are a hundred times too high.
var minorUnitCurrencies = map[string]struct {
	code       string
	multiplier domain.Rate
}{
	"GBp": {code: "GBP", multiplier: decimal.New(1, -2)}, // pence
	"ZAc": {code: "ZAR", multiplier: decimal.New(1, -2)}, // cents
}

func NormalizeMinorUnits(currency string, price domain.Rate) (string, domain.Rate) {
	conv, ok := minorUnitCurrencies[currency]
	if !ok {
		return currency, price
	}
	return conv.code, price.Mul(conv.multiplier)
}

// YahooProvider reads Yahoo Finance's unofficial public endpoints: search and
// chart, which need no session. The crumb-cookie endpoints (v7 batch quotes,
// quoteSummary) are deliberately not used, so there is no session to break.
type YahooProvider struct {
	BaseURL    string
	HTTPClient *http.Client
	// Concurrency bounds a LatestPrices batch; there is no bulk endpoint
	// without a crumb. Zero means the default.
	Concurrency int
}

const (
	yahooBaseURL        = "https://query1.finance.yahoo.com"
	yahooTimeout        = 30 * time.Second
	yahooConcurrency    = 4
	yahooDefaultResults = 20
)

func (y *YahooProvider) Name() string { return "yahoo" }

func (y *YahooProvider) base() string {
	if y.BaseURL != "" {
		return strings.TrimRight(y.BaseURL, "/")
	}
	return yahooBaseURL
}

func (y *YahooProvider) client() *http.Client {
	if y.HTTPClient != nil {
		return y.HTTPClient
	}
	return &http.Client{Timeout: yahooTimeout}
}

func (y *YahooProvider) get(ctx context.Context, path string, params url.Values, into any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, y.base()+path+"?"+params.Encode(), nil)
	if err != nil {
		return fmt.Errorf("yahoo: request: %w", err)
	}
	req.Header.Set("Accept", "application/json")
	// Yahoo answers a request with no User-Agent with an HTML consent page
	// rather than JSON.
	req.Header.Set("User-Agent", "Agentifi/0.1")

	resp, err := httpx.Read(y.client(), req, 0)
	if err != nil {
		return fmt.Errorf("yahoo: request failed: %w", err)
	}

	switch {
	case resp.Status == http.StatusTooManyRequests:
		return ErrMarketPriceRateLimited
	case resp.Status == http.StatusNotFound:
		return errSymbolUnknown
	case !resp.OK():
		return fmt.Errorf("yahoo: %s: %w", path, resp.Err())
	}
	if err := httpx.DecodeJSON(resp.Body, into); err != nil {
		return fmt.Errorf("yahoo: cannot parse %s response: %w", path, err)
	}
	return nil
}

var errSymbolUnknown = fmt.Errorf("yahoo: symbol not found")

type yahooSearchResponse struct {
	Quotes []struct {
		Symbol    string `json:"symbol"`
		ShortName string `json:"shortname"`
		LongName  string `json:"longname"`
		Exchange  string `json:"exchange"`
		ExchDisp  string `json:"exchDisp"`
		QuoteType string `json:"quoteType"`
	} `json:"quotes"`
}

func (y *YahooProvider) Search(ctx context.Context, query string, limit int) ([]SymbolMatch, error) {
	query = strings.TrimSpace(query)
	if query == "" {
		return nil, nil
	}
	if limit <= 0 {
		limit = yahooDefaultResults
	}

	var raw yahooSearchResponse
	err := y.get(ctx, "/v1/finance/search", url.Values{
		"q":                {query},
		"quotesCount":      {strconv.Itoa(limit)},
		"newsCount":        {"0"},
		"listsCount":       {"0"},
		"enableFuzzyQuery": {"true"},
	}, &raw)
	if err != nil {
		if err == errSymbolUnknown {
			return nil, nil
		}
		return nil, err
	}

	matches := make([]SymbolMatch, 0, len(raw.Quotes))
	for _, q := range raw.Quotes {
		if q.Symbol == "" {
			continue
		}
		matches = append(matches, SymbolMatch{
			Symbol:    q.Symbol,
			Name:      cmp.Or(q.LongName, q.ShortName),
			Exchange:  cmp.Or(q.ExchDisp, q.Exchange),
			QuoteType: strings.ToUpper(q.QuoteType),
		})
	}
	return matches, nil
}

type yahooChartResponse struct {
	Chart struct {
		Result []struct {
			Meta struct {
				Currency           string       `json:"currency"`
				Symbol             string       `json:"symbol"`
				ExchangeName       string       `json:"exchangeName"`
				FullExchangeName   string       `json:"fullExchangeName"`
				InstrumentType     string       `json:"instrumentType"`
				ShortName          string       `json:"shortName"`
				LongName           string       `json:"longName"`
				RegularMarketPrice *json.Number `json:"regularMarketPrice"`
				ChartPreviousClose *json.Number `json:"chartPreviousClose"`
				PreviousClose      *json.Number `json:"previousClose"`
			} `json:"meta"`
			// Timestamp and Close are parallel; a session with no close is a
			// null, not a missing entry.
			Timestamp  []int64 `json:"timestamp"`
			Indicators struct {
				Quote []struct {
					Close []*json.Number `json:"close"`
				} `json:"quote"`
			} `json:"indicators"`
		} `json:"result"`
		Error *struct {
			Code        string `json:"code"`
			Description string `json:"description"`
		} `json:"error"`
	} `json:"chart"`
}

// Quote never fills LogoURL: that needs quoteSummary, which needs a crumb
// cookie.
func (y *YahooProvider) Quote(ctx context.Context, symbol string) (*SymbolQuote, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" {
		return nil, nil
	}

	var raw yahooChartResponse
	err := y.get(ctx, "/v8/finance/chart/"+url.PathEscape(symbol), url.Values{
		"range":    {"1d"},
		"interval": {"1d"},
	}, &raw)
	if err != nil {
		if err == errSymbolUnknown {
			return nil, nil
		}
		return nil, err
	}
	if len(raw.Chart.Result) == 0 {
		return nil, nil
	}
	meta := raw.Chart.Result[0].Meta

	// regularMarketPrice is absent until the market ticks at the open.
	price, ok := decimalFromJSON(meta.RegularMarketPrice, meta.ChartPreviousClose, meta.PreviousClose)
	if !ok || meta.Currency == "" {
		return nil, nil
	}
	currency, price := NormalizeMinorUnits(meta.Currency, price)

	return &SymbolQuote{
		Symbol:    symbol,
		Name:      cmp.Or(meta.LongName, meta.ShortName),
		Exchange:  cmp.Or(meta.FullExchangeName, meta.ExchangeName),
		Currency:  currency,
		Price:     price,
		QuoteType: strings.ToUpper(meta.InstrumentType),
	}, nil
}

// History never carries a price forward over a null close, and normalizes
// minor units exactly as Quote does so history and latest price share a unit.
func (y *YahooProvider) History(
	ctx context.Context, symbol string, from, to domain.Date,
) ([]domain.PricePoint, error) {
	symbol = strings.ToUpper(strings.TrimSpace(symbol))
	if symbol == "" || from.IsZero() || to.IsZero() || to.Before(from) {
		return nil, nil
	}

	var raw yahooChartResponse
	err := y.get(ctx, "/v8/finance/chart/"+url.PathEscape(symbol), url.Values{
		"period1": {strconv.FormatInt(from.Time().Unix(), 10)},
		// period2 is exclusive.
		"period2":  {strconv.FormatInt(to.AddDays(1).Time().Unix(), 10)},
		"interval": {"1d"},
	}, &raw)
	if err != nil {
		if err == errSymbolUnknown {
			return nil, nil
		}
		return nil, err
	}
	if len(raw.Chart.Result) == 0 || len(raw.Chart.Result[0].Indicators.Quote) == 0 {
		return nil, nil
	}
	result := raw.Chart.Result[0]
	closes := result.Indicators.Quote[0].Close

	points := make([]domain.PricePoint, 0, len(result.Timestamp))
	for index, stamp := range result.Timestamp {
		if index >= len(closes) {
			break
		}
		price, ok := decimalFromJSON(closes[index])
		if !ok {
			continue
		}
		_, price = NormalizeMinorUnits(result.Meta.Currency, price)
		points = append(points, domain.PricePoint{
			On:    domain.DateOf(time.Unix(stamp, 0).UTC()),
			Close: price,
		})
	}
	return points, nil
}

// LatestPrices makes one request per symbol, a few at a time. An error is
// returned only when nothing at all was priced.
func (y *YahooProvider) LatestPrices(ctx context.Context, symbols []string) (map[string]domain.Rate, error) {
	unique := make([]string, 0, len(symbols))
	seen := map[string]bool{}
	for _, s := range symbols {
		s = strings.ToUpper(strings.TrimSpace(s))
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		unique = append(unique, s)
	}
	if len(unique) == 0 {
		return map[string]domain.Rate{}, nil
	}

	limit := y.Concurrency
	if limit <= 0 {
		limit = yahooConcurrency
	}

	var (
		mu     sync.Mutex
		wg     sync.WaitGroup
		out    = make(map[string]domain.Rate, len(unique))
		failed error
		tokens = make(chan struct{}, limit)
	)
	for _, symbol := range unique {
		wg.Add(1)
		go func() {
			defer wg.Done()
			tokens <- struct{}{}
			defer func() { <-tokens }()

			quote, err := y.Quote(ctx, symbol)
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				if failed == nil {
					failed = err
				}
				return
			}
			if quote != nil {
				out[symbol] = quote.Price
			}
		}()
	}
	wg.Wait()

	if failed != nil && len(out) == 0 {
		return nil, failed
	}
	return out, nil
}

// decimalFromJSON converts the first present JSON number to an exact decimal,
// never through float64.
func decimalFromJSON(candidates ...*json.Number) (domain.Rate, bool) {
	for _, candidate := range candidates {
		if candidate == nil || string(*candidate) == "" {
			continue
		}
		value, err := decimal.NewFromString(string(*candidate))
		if err != nil {
			continue
		}
		return value, true
	}
	return decimal.Zero, false
}
