package service

import (
	"context"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/minorunits"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/shopspring/decimal"
)

// Foreign-currency rows are converted at the rate on the row's own date and
// stored in amount_primary, which Posting.Amount() prefers. Converting at read
// time instead would restate past spending whenever the rate moved.

// ClearPrimaryAmount drops a row's stored conversion so it will be re-derived.
// Call it wherever a write changes a row's amount or currency: StampSpace
// skips any row that already carries a primary amount.
func ClearPrimaryAmount(txn *store.Transaction) {
	txn.AmountPrimary = domain.Zero
	txn.HasAmountPrimary = false
	txn.FxRateUsed = domain.Rate{}
	txn.HasFxRateUsed = false
}

type Currency struct {
	base
	rates *provider.FxRates
}

func NewCurrency(st *store.Store, rates *provider.FxRates) *Currency {
	return &Currency{base: newBase(st), rates: rates}
}

// FetchRates stores the day's exchange rates for a space; StampSpace reads
// only stored rates. With no rate-provider credential it does nothing.
func (c *Currency) FetchRates(ctx context.Context, spaceID store.SpaceID, on domain.Date) error {
	if c.rates == nil || !c.rates.CanFetch() {
		return nil
	}
	_, err := c.rates.Sync(ctx, domain.ID(spaceID.UUID().String()), on)
	return err
}

type StampResult struct {
	// Unrated counts rows left unconverted because no rate was stored for
	// their date.
	Considered int
	Stamped    int
	Unrated    int
}

// StampSpace converts every row in a space whose currency is not the space's
// and which carries no conversion yet. It writes only the conversion columns,
// so a concurrent edit to the row is not reverted.
func (c *Currency) StampSpace(ctx context.Context, spaceID store.SpaceID) (StampResult, error) {
	primary, err := c.store.HouseholdCurrency(ctx, spaceID)
	if err != nil {
		return StampResult{}, err
	}
	if primary == "" {
		return StampResult{}, fmt.Errorf("service: space %s has no primary currency", spaceID)
	}

	rows, err := c.store.ListUnstampedForeign(ctx, spaceID, primary)
	if err != nil {
		return StampResult{}, err
	}
	out := StampResult{Considered: len(rows)}

	for _, row := range rows {
		rate, ok, err := c.rates.Resolve(ctx, domain.ID(spaceID.UUID().String()), provider.RateQuery{
			From: row.Currency, To: primary, On: row.Date,
			// Stored rates only, so a long backlog cannot exhaust the API
			// quota; FetchRates fills the table daily.
			NoFetch: true,
		})
		if err != nil {
			return out, err
		}
		if !ok {
			// A guessed rate would be indistinguishable from a real one once
			// stored.
			out.Unrated++
			continue
		}

		exponent, err := minorunits.Decimals(primary)
		if err != nil {
			return out, err
		}
		stamped := row.Amount.Convert(rate, exponent).Round()
		if err := c.store.SetTransactionConversion(ctx, spaceID, row.ID, stamped, rate); err != nil {
			return out, err
		}
		out.Stamped++
	}
	return out, nil
}

// Restater restates amounts in a space's primary currency at the stored rate
// for a day, one lookup per currency. A currency with no stored rate counts
// at face value, as net worth counts it.
type Restater struct {
	primary string
	places  int
	resolve func(currency string) (domain.Rate, bool, error)
	rates   map[string]domain.Rate
}

func NewRestater(ctx context.Context, st *store.Store, spaceID store.SpaceID, on domain.Date) (*Restater, error) {
	primary, err := st.HouseholdCurrency(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	places, err := minorunits.Decimals(primary)
	if err != nil {
		return nil, err
	}
	rates := &provider.FxRates{Store: store.NewFxRates(st)}
	id := domain.ID(spaceID.UUID().String())
	return &Restater{
		primary: primary, places: places, rates: map[string]domain.Rate{},
		resolve: func(currency string) (domain.Rate, bool, error) {
			return rates.Resolve(ctx, id, provider.RateQuery{From: currency, To: primary, On: on, NoFetch: true})
		},
	}, nil
}

func (r *Restater) Restate(amount domain.Money, currency string) (domain.Money, error) {
	if currency == "" || currency == r.primary {
		return amount, nil
	}
	rate, seen := r.rates[currency]
	if !seen {
		found, ok, err := r.resolve(currency)
		if err != nil {
			return domain.Zero, err
		}
		rate = decimal.NewFromInt(1)
		if ok {
			rate = found
		}
		r.rates[currency] = rate
	}
	return amount.Convert(rate, r.places).Round(), nil
}

// Occurrences restates each occurrence's amount in the primary currency, for
// a total across series.
func (r *Restater) Occurrences(in []domain.Occurrence) ([]domain.Occurrence, error) {
	out := make([]domain.Occurrence, len(in))
	for i, one := range in {
		amount, err := r.Restate(one.Amount, one.Currency)
		if err != nil {
			return nil, err
		}
		one.Amount, one.Currency = amount, r.primary
		out[i] = one
	}
	return out, nil
}

// SwitchPrimaryCurrency commits all household changes before updating the
// listing mirror. A crash between commits is repaired by the next scope.
func (c *Currency) SwitchPrimaryCurrency(ctx context.Context, spaceID store.SpaceID, next string) error {
	catalogue, err := c.store.SnapshotFxRates(ctx)
	if err != nil {
		return err
	}
	rates := &provider.FxRates{Store: catalogue}
	if c.rates != nil {
		rates.Now = c.rates.Now
	}
	scoped, err := c.store.ForHousehold(ctx, spaceID)
	if err != nil {
		return err
	}
	err = scoped.InTx(ctx, func(tx *store.Store) error {
		if err := tx.RescalePrimaryCurrency(ctx, spaceID, next); err != nil {
			return err
		}
		if _, err := tx.ClearConversions(ctx, spaceID); err != nil {
			return err
		}
		_, err := NewCurrency(tx, rates).StampSpace(ctx, spaceID)
		return err
	})
	if err != nil {
		return err
	}
	_, err = c.store.ForHousehold(ctx, spaceID)
	return err
}
