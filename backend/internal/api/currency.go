package api

import (
	"context"
	"errors"
	"log/slog"
	"sort"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/shopspring/decimal"
)

// Building the currency converter, in one place. The scheduler uses this too:
// a converter without its rate provider writes no `fx_rates`, so every foreign
// row stays unconverted and is summed at its native amount everywhere.

// NewFxRates is the rate service, with a provider when the deployment has a
// credential for one. No credential is supported: imported rates still convert
// the rows they cover, and every fetch path checks CanFetch first.
func NewFxRates(cfg *config.Config, db *store.Store) *provider.FxRates {
	rates := &provider.FxRates{
		Store: store.NewFxRates(db),
		// Supported bounds what is stored to the currencies this deployment
		// offers, which is also what the rate provider's free plan returns for
		// one request.
		Supported: cfg.SupportedCurrencies,
	}
	if cfg.OpenExchangeRatesAppID != "" {
		rates.Provider = &provider.OpenExchangeRates{
			AppID:   cfg.OpenExchangeRatesAppID,
			Symbols: cfg.SupportedCurrencies,
		}
	}
	return rates
}

// NewCurrency is the converter, wired to whatever rate source the deployment
// has.
func NewCurrency(cfg *config.Config, db *store.Store) *service.Currency {
	return service.NewCurrency(db, NewFxRates(cfg, db))
}

// stampForeignAmount fills a row's primary amount before it is written;
// Posting.Amount() falls back to the native figure when it is missing.
//
// A missing rate leaves the row unstamped rather than converted at a guess,
// and the daily pass stamps it later. The fetch is allowed here, unlike in the
// bulk pass, because this is one row a person is waiting on.
func stampForeignAmount(
	ctx context.Context, env *Env, sp auth.SpaceContext, row *store.Transaction,
) error {
	primary := sp.Space.PrimaryCurrency
	if primary == "" || row.Currency == "" || row.Currency == primary {
		return nil
	}
	stamped, ok, err := NewFxRates(env.Cfg, env.DB).StampPrimary(ctx,
		domain.ID(sp.ID().UUID().String()), row.Amount, row.Currency, primary, row.Date, false)
	if err != nil {
		// A rate source that is down must not stop somebody entering a
		// transaction; the daily pass picks the row up.
		slog.Warn("could not convert a foreign amount on write",
			"currency", row.Currency, "primary", primary, "error", err)
		return nil
	}
	if !ok {
		return nil
	}
	row.AmountPrimary = stamped.AmountPrimary
	row.HasAmountPrimary = true
	row.FxRateUsed = stamped.RateUsed
	row.HasFxRateUsed = true
	return nil
}

// inCurrency gives a request's amount the scale of the currency it is in,
// refusing one with more decimal places than that currency holds rather than
// rounding it away.
func inCurrency(amount domain.Money, currency, field string) (domain.Money, error) {
	places, err := minorunits.Decimals(currency)
	if err != nil {
		return domain.Zero, errInvalid("format", []string{"body", field}, "%q is not an ISO 4217 currency with a minor unit", currency)
	}
	scaled, err := amount.AtPlaces(places)
	if errors.Is(err, domain.ErrAmountOutOfRange) {
		return domain.Zero, errInvalid("value_error", []string{"body", field},
			"%s is beyond the largest amount, ±900,000,000,000", amount.Decimal().String())
	}
	if err != nil {
		return domain.Zero, errInvalid("value_error", []string{"body", field},
			"%s has more decimal places than %s allows (%d)", amount.Decimal().String(), currency, places)
	}
	return scaled, nil
}

// inPrimary is inCurrency for an amount in the space's primary currency.
func inPrimary(sp auth.SpaceContext, amount domain.Money, field string) (domain.Money, error) {
	return inCurrency(amount, sp.Space.PrimaryCurrency, field)
}

// balanceConverter restates account balances in the space's primary currency.
// One rate for the whole series, as of the window's end, so the net-worth line
// does not move when the household's money did not: a valuation in one
// currency, not a historical record.
type balanceConverter struct {
	currencyConverter
	// accounts maps a foreign account to its currency, so an account in the
	// primary currency costs no lookup and no multiplication.
	accounts map[domain.ID]string
}

// currencyConverter restates amounts in the space's primary currency at one
// rate per currency.
type currencyConverter struct {
	primary string
	places  int
	rates   map[string]domain.Rate
	// Unconverted names the currencies with no rate. Their amounts are counted
	// at face value rather than being dropped, and the response says so.
	Unconverted []string
}

// newBalanceConverter resolves one rate per foreign currency in use.
func newBalanceConverter(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accounts []store.Account, on domain.Date,
) (balanceConverter, error) {
	out := balanceConverter{accounts: map[domain.ID]string{}}
	currencies := make([]string, 0, len(accounts))
	for _, account := range accounts {
		out.accounts[domain.ID(account.ID.String())] = account.Currency
		currencies = append(currencies, account.Currency)
	}
	converter, err := newCurrencyConverter(ctx, env, sp, currencies, on)
	out.currencyConverter = converter
	return out, err
}

// newCurrencyConverter resolves one rate, as of on, for each of currencies
// that is not the primary one.
func newCurrencyConverter(
	ctx context.Context, env *Env, sp auth.SpaceContext, currencies []string, on domain.Date,
) (currencyConverter, error) {
	primary := sp.Space.PrimaryCurrency
	out := currencyConverter{primary: primary, rates: map[string]domain.Rate{}}
	if primary == "" {
		return out, nil
	}
	places, err := minorunits.Decimals(primary)
	if err != nil {
		return currencyConverter{}, err
	}
	out.places = places

	rates := NewFxRates(env.Cfg, env.DB)
	spaceID := domain.ID(sp.ID().UUID().String())
	for _, currency := range currencies {
		if out.isPrimary(currency) {
			continue
		}
		if _, seen := out.rates[currency]; seen {
			continue
		}
		// Stored rates only: a page load must not spend the deployment's API
		// quota, and the daily pass is what fills the table.
		rate, ok, err := rates.Resolve(ctx, spaceID, provider.RateQuery{
			From: currency, To: primary, On: on, NoFetch: true,
		})
		if err != nil {
			return currencyConverter{}, err
		}
		if !ok {
			rate = decimal.NewFromInt(1)
			out.Unconverted = append(out.Unconverted, currency)
		}
		out.rates[currency] = rate
	}
	sort.Strings(out.Unconverted)
	return out, nil
}

func (c currencyConverter) isPrimary(currency string) bool {
	return currency == "" || currency == c.primary
}

// restate is one amount in the primary currency. A currency with no rate
// counts at face value, at the primary currency's scale.
func (c currencyConverter) restate(amount domain.Money, currency string) domain.Money {
	if c.isPrimary(currency) || c.primary == "" {
		return amount
	}
	rate, ok := c.rates[currency]
	if !ok {
		rate = decimal.NewFromInt(1)
	}
	return amount.Convert(rate, c.places).Round()
}

// fromPrimary is restate run backwards: an amount in the primary currency
// restated in currency, at one over that currency's rate.
func (c currencyConverter) fromPrimary(amount domain.Money, currency string) domain.Money {
	if c.isPrimary(currency) || c.primary == "" {
		return amount
	}
	places, err := minorunits.Decimals(currency)
	if err != nil {
		return amount
	}
	rate, ok := c.rates[currency]
	if !ok || rate.IsZero() {
		rate = decimal.NewFromInt(1)
	}
	return amount.Convert(decimal.NewFromInt(1).Div(rate), places).Round()
}

// apply restates one day's balances.
func (c balanceConverter) apply(balances map[domain.ID]domain.Money) map[domain.ID]domain.Money {
	if len(c.rates) == 0 {
		return balances
	}
	out := make(map[domain.ID]domain.Money, len(balances))
	for id, balance := range balances {
		out[id] = c.restate(balance, c.accounts[id])
	}
	return out
}
