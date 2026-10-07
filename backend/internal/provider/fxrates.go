package provider

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/shopspring/decimal"
)

// A stored rate is the price of one USD in the quote currency, per space and
// per day. Anything that stores a conversion leaves it unset when Resolve has
// no rate: an invented 1:1 in the ledger looks real and never heals.

type FxRateProvider interface {
	Name() string
	Latest(ctx context.Context) (map[string]domain.Rate, error)
	Historical(ctx context.Context, on domain.Date) (map[string]domain.Rate, error)
}

type FxRate struct {
	SpaceID       domain.ID
	BaseCurrency  string
	QuoteCurrency string
	On            domain.Date
	Rate          domain.Rate
	Source        string
}

// FxRateStore is space-scoped throughout, so one space's corrected rate cannot
// move another's net worth.
type FxRateStore interface {
	ExactRate(ctx context.Context, spaceID domain.ID, quoteCurrency string, on domain.Date) (domain.Rate, bool, error)
	// ClosestRate returns the nearest stored rate, preferring the most recent
	// day at or before the target and falling back to the earliest day after
	// it.
	ClosestRate(ctx context.Context, spaceID domain.ID, quoteCurrency string, on domain.Date) (domain.Rate, bool, error)
	UpsertRates(ctx context.Context, rates []FxRate) error
}

var oneRate = decimal.NewFromInt(1)

const (
	openExchangeRatesBaseURL = "https://openexchangerates.org/api"
	openExchangeRatesTimeout = 30 * time.Second
)

type OpenExchangeRates struct {
	AppID string
	// Symbols keeps the response small for the free plan; empty asks for all.
	Symbols    []string
	BaseURL    string
	HTTPClient *http.Client
}

// Name is stored as the source of every rate this provider wrote, so it must
// not change.
func (o *OpenExchangeRates) Name() string { return "openexchangerates" }

func (o *OpenExchangeRates) Latest(ctx context.Context) (map[string]domain.Rate, error) {
	return o.fetch(ctx, "/latest.json")
}

func (o *OpenExchangeRates) Historical(ctx context.Context, on domain.Date) (map[string]domain.Rate, error) {
	return o.fetch(ctx, "/historical/"+on.String()+".json")
}

func (o *OpenExchangeRates) fetch(ctx context.Context, path string) (map[string]domain.Rate, error) {
	if o.AppID == "" {
		return nil, errors.New("openexchangerates: no app id is configured")
	}
	base := strings.TrimRight(o.BaseURL, "/")
	if base == "" {
		base = openExchangeRatesBaseURL
	}
	endpoint := base + path

	params := url.Values{"app_id": {o.AppID}}
	if len(o.Symbols) > 0 {
		params.Set("symbols", strings.Join(o.Symbols, ","))
	}
	secretURL := endpoint + "?" + params.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, secretURL, nil)
	if err != nil {
		return nil, fmt.Errorf("openexchangerates: cannot build the request for %s", path)
	}
	req.Header.Set("Accept", "application/json")

	client := o.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: openExchangeRatesTimeout}
	}
	resp, err := httpx.Read(client, req, 0)
	if err != nil {
		// The key rides in the query string, and transport errors quote the URL.
		return nil, fmt.Errorf("openexchangerates: request failed: %w", redactedError(err, secretURL, endpoint))
	}

	if resp.Status == http.StatusTooManyRequests {
		return nil, fmt.Errorf("openexchangerates: %s is being throttled: %w", path, ErrRateLimited)
	}
	if !resp.OK() {
		return nil, fmt.Errorf("openexchangerates: %s: %w", path, resp.Err())
	}

	var payload struct {
		Rates map[string]any `json:"rates"`
	}
	if err := httpx.DecodeJSON(resp.Body, &payload); err != nil {
		return nil, fmt.Errorf("openexchangerates: %s answered with something that is not JSON: %w", path, err)
	}

	out := make(map[string]domain.Rate, len(payload.Rates))
	for code, value := range payload.Rates {
		var text string
		switch typed := value.(type) {
		case json.Number:
			text = typed.String()
		case string:
			text = typed
		default:
			continue
		}
		rate, err := decimal.NewFromString(text)
		if err != nil {
			continue
		}
		out[code] = rate
	}
	return out, nil
}

type FxRates struct {
	Provider FxRateProvider
	Store    FxRateStore
	// Supported matches the frontend's currency list; empty stores everything.
	Supported []string
	Now       func() time.Time
}

// CanFetch guards every fetch path: a nil Provider is a supported deployment
// that works off stored rates.
func (f *FxRates) CanFetch() bool { return f.Provider != nil }

func (f *FxRates) today() domain.Date {
	if f.Now != nil {
		return domain.DateOf(f.Now().UTC())
	}
	return domain.DateOf(time.Now().UTC())
}

func (f *FxRates) supported(code string) bool {
	if len(f.Supported) == 0 {
		return true
	}
	for _, c := range f.Supported {
		if c == code {
			return true
		}
	}
	return false
}

// target is the day a rate is wanted for: on, or today when on is unset or
// has not happened yet.
func (f *FxRates) target(on domain.Date) domain.Date {
	today := f.today()
	if on.IsZero() || on.After(today) {
		return today
	}
	return on
}

func (f *FxRates) Sync(ctx context.Context, spaceID domain.ID, on domain.Date) (int, error) {
	if !f.CanFetch() {
		return 0, nil
	}
	target := f.target(on)

	var (
		fetched map[string]domain.Rate
		err     error
	)
	if target == f.today() {
		fetched, err = f.Provider.Latest(ctx)
	} else {
		fetched, err = f.Provider.Historical(ctx, target)
	}
	if err != nil {
		return 0, err
	}

	rows := make([]FxRate, 0, len(fetched))
	for code, rate := range fetched {
		if !f.supported(code) {
			continue
		}
		rows = append(rows, FxRate{
			SpaceID: spaceID, BaseCurrency: "USD", QuoteCurrency: code,
			On: target, Rate: rate, Source: f.Provider.Name(),
		})
	}
	if len(rows) == 0 {
		return 0, nil
	}
	if err := f.Store.UpsertRates(ctx, rows); err != nil {
		return 0, err
	}
	return len(rows), nil
}

type RateQuery struct {
	From string
	To   string
	// A zero On means today.
	On domain.Date
	// NoFetch is set by the re-stamping pass so a backlog cannot spend the API
	// quota in one run.
	NoFetch bool
}

// Resolve looks each side up as its USD rate: that exact day, then one sync of
// that day, then the closest stored day. A failed sync is logged and read past.
func (f *FxRates) Resolve(ctx context.Context, spaceID domain.ID, q RateQuery) (domain.Rate, bool, error) {
	if q.From == q.To {
		return oneRate, true, nil
	}
	target := f.target(q.On)
	currencies := [2]string{q.From, q.To}
	var (
		rates [2]domain.Rate
		found [2]bool
	)

	lookup := func(read func(context.Context, domain.ID, string, domain.Date) (domain.Rate, bool, error)) error {
		for i, currency := range currencies {
			if found[i] {
				continue
			}
			if currency == "USD" {
				rates[i], found[i] = oneRate, true
				continue
			}
			rate, ok, err := read(ctx, spaceID, currency, target)
			if err != nil {
				return err
			}
			rates[i], found[i] = rate, ok
		}
		return nil
	}

	if err := lookup(f.Store.ExactRate); err != nil {
		return decimal.Zero, false, err
	}
	if (!found[0] || !found[1]) && !q.NoFetch && f.CanFetch() {
		written, err := f.Sync(ctx, spaceID, target)
		if err != nil {
			slog.Warn("fx: fetching rates failed; answering from stored rates",
				"space", spaceID, "on", target.String(), "error", err)
		}
		if written > 0 {
			if err := lookup(f.Store.ExactRate); err != nil {
				return decimal.Zero, false, err
			}
		}
	}
	if !found[0] || !found[1] {
		if err := lookup(f.Store.ClosestRate); err != nil {
			return decimal.Zero, false, err
		}
	}

	if !found[0] || !found[1] || rates[0].IsZero() {
		return decimal.Zero, false, nil
	}
	return rates[1].Div(rates[0]), true, nil
}

type StampedAmount struct {
	AmountPrimary domain.Money
	RateUsed      domain.Rate
}

// StampPrimary's ok=false means leave the row's existing values alone: a new
// row stays NULL (read back through COALESCE(amount_primary, amount)) and a
// stamped one keeps its figure until a rate for its day lands.
func (f *FxRates) StampPrimary(
	ctx context.Context,
	spaceID domain.ID,
	amount domain.Money,
	currency, primaryCurrency string,
	on domain.Date,
	noFetch bool,
) (StampedAmount, bool, error) {
	if currency == primaryCurrency {
		return StampedAmount{AmountPrimary: amount.Round(), RateUsed: oneRate}, true, nil
	}

	rate, ok, err := f.Resolve(ctx, spaceID, RateQuery{
		From: currency, To: primaryCurrency, On: on, NoFetch: noFetch,
	})
	if err != nil || !ok {
		return StampedAmount{}, false, err
	}
	return StampedAmount{AmountPrimary: amount.Scale(rate).Round(), RateUsed: rate}, true, nil
}
