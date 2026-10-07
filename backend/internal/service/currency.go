package service

import (
	"context"
	"fmt"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
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
	space, err := c.store.GetSpace(ctx, spaceID)
	if err != nil {
		return StampResult{}, err
	}
	primary := space.PrimaryCurrency
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

		stamped := row.Amount.Scale(rate).Round()
		if err := c.store.SetTransactionConversion(ctx, spaceID, row.ID, stamped, rate); err != nil {
			return out, err
		}
		out.Stamped++
	}
	return out, nil
}
