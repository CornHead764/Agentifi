package api

import (
	"context"
	"log/slog"
	"sort"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/config"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
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

// balanceConverter restates account balances in the space's primary currency.
// One rate for the whole series, as of the window's end, so the net-worth line
// does not move when the household's money did not: a valuation in one
// currency, not a historical record.
type balanceConverter struct {
	// rates is keyed by account, so an account in the primary currency costs
	// no lookup and no multiplication.
	rates map[domain.ID]domain.Rate
	// Unconverted names the currencies with no rate. Their balances pass
	// through at face value rather than being dropped, and the response says
	// so.
	Unconverted []string
}

// newBalanceConverter resolves one rate per foreign currency in use.
func newBalanceConverter(
	ctx context.Context, env *Env, sp auth.SpaceContext,
	accounts []store.Account, on domain.Date,
) (balanceConverter, error) {
	primary := sp.Space.PrimaryCurrency
	out := balanceConverter{rates: map[domain.ID]domain.Rate{}}
	if primary == "" {
		return out, nil
	}

	byCurrency := map[string][]domain.ID{}
	for _, account := range accounts {
		if account.Currency == "" || account.Currency == primary {
			continue
		}
		id := domain.ID(account.ID.String())
		byCurrency[account.Currency] = append(byCurrency[account.Currency], id)
	}
	if len(byCurrency) == 0 {
		return out, nil
	}

	rates := NewFxRates(env.Cfg, env.DB)
	spaceID := domain.ID(sp.ID().UUID().String())
	missing := map[string]bool{}
	for currency, ids := range byCurrency {
		// Stored rates only: a page load must not spend the deployment's API
		// quota, and the daily pass is what fills the table.
		rate, ok, err := rates.Resolve(ctx, spaceID, provider.RateQuery{
			From: currency, To: primary, On: on, NoFetch: true,
		})
		if err != nil {
			return balanceConverter{}, err
		}
		if !ok {
			missing[currency] = true
			continue
		}
		for _, id := range ids {
			out.rates[id] = rate
		}
	}
	for currency := range missing {
		out.Unconverted = append(out.Unconverted, currency)
	}
	sort.Strings(out.Unconverted)
	return out, nil
}

// apply restates one day's balances. An account with no rate keeps its own
// figure, the register's fallback for an unconverted transaction.
func (c balanceConverter) apply(balances map[domain.ID]domain.Money) map[domain.ID]domain.Money {
	if len(c.rates) == 0 {
		return balances
	}
	out := make(map[domain.ID]domain.Money, len(balances))
	for id, balance := range balances {
		if rate, ok := c.rates[id]; ok {
			out[id] = balance.Scale(rate).Round()
			continue
		}
		out[id] = balance
	}
	return out
}
