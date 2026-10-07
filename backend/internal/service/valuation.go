package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/browser"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A revaluation is written as a SourceBalanceAdjustment transaction, not a
// balance edit: the balance is the sum of the rows, and an adjustment is not
// cash flow, so an appreciating house stays out of the month's income. An
// imported asset's balance is the provider figure the import carried, so its
// revaluation moves that figure too (domain.RevaluationOf). A connected
// asset is not revalued: its bank's figure stands.

// StaleAfterDays is how long an asset's recorded value stands before it is
// re-priced; each lookup drives a browser through a site that walls off
// automated visitors.
const StaleAfterDays = 30

// Valuation re-prices physical assets from an outside estimate.
type Valuation struct {
	base
	// Valuers is keyed by asset type; an asset type with no valuer is skipped.
	Valuers map[string]provider.AssetValuationProvider

	Log *slog.Logger
	Now func() time.Time
}

func NewValuation(
	st *store.Store,
	valuers map[string]provider.AssetValuationProvider,
) *Valuation {
	return &Valuation{base: newBase(st), Valuers: valuers}
}

func (v *Valuation) now() time.Time {
	if v.Now == nil {
		return time.Now()
	}
	return v.Now()
}

func (v *Valuation) log() *slog.Logger {
	if v.Log == nil {
		return slog.Default()
	}
	return v.Log
}

type ValuationResult struct {
	AccountID uuid.UUID
	Name      string
	// Skipped says why nothing was written; an unpriceable asset is a result,
	// not an error.
	Skipped string
	Source  string
	// Estimate is what the source said; Adjustment is what was written, the
	// difference from the ledger.
	Estimate    domain.Money
	HasEstimate bool
	Adjustment  domain.Money
	HasAdjust   bool
	MileageUsed int
	HasMileage  bool
	// Priced is what the source priced; nil when it gave no estimate.
	Priced *provider.PricedAs
}

// RevalueAll re-prices every asset in the space that is due, or every asset
// when force is set.
func (v *Valuation) RevalueAll(ctx context.Context, spaceID store.SpaceID, force bool) ([]ValuationResult, error) {
	due, err := v.dueIn(ctx, spaceID, force)
	if err != nil {
		return nil, err
	}
	var out []ValuationResult
	for _, account := range due {
		result, ran, err := v.revalueOne(ctx, spaceID, account.ID, force)
		if err != nil {
			v.log().Warn("asset valuation failed", "account", account.Name, "error", err)
			out = append(out, ValuationResult{
				AccountID: account.ID, Name: account.Name, Skipped: skippedFor(err),
			})
			continue
		}
		if ran {
			out = append(out, result)
		}
	}
	return out, nil
}

// dueAsset is one asset the scheduled pass re-prices.
type dueAsset struct {
	spaceID store.SpaceID
	account store.Account
}

// RevalueDue re-prices every due asset in every space, as the scheduled pass,
// and reports how many were looked up. The error is only the listing's.
func (v *Valuation) RevalueDue(ctx context.Context) (int, error) {
	var listErr error
	ran := runDue(ctx, v.store, v.log(), dueRun[dueAsset]{
		kind: "revalue",
		list: func(ctx context.Context) ([]dueAsset, error) {
			spaces, err := v.store.ListSpaces(ctx)
			if err != nil {
				listErr = err
				return nil, err
			}
			var out []dueAsset
			for _, space := range spaces {
				due, err := v.dueIn(ctx, space.ID, false)
				if err != nil {
					listErr = err
					return nil, err
				}
				for _, account := range due {
					out = append(out, dueAsset{spaceID: space.ID, account: account})
				}
			}
			return out, nil
		},
		run: func(ctx context.Context, due dueAsset) bool {
			result, ran, err := v.revalueOne(ctx, due.spaceID, due.account.ID, false)
			if err != nil {
				v.log().Warn("asset valuation failed", "account", due.account.Name, "error", err)
				return true
			}
			if result.HasAdjust {
				v.log().Info("asset revalued",
					"account", result.Name, "source", result.Source,
					"estimate", result.Estimate.String(), "adjustment", result.Adjustment.String())
			}
			return ran
		},
	})
	return ran, listErr
}

// dueIn lists the space's assets that have a source and are stale, or every
// one with a source when force is set.
func (v *Valuation) dueIn(ctx context.Context, spaceID store.SpaceID, force bool) ([]store.Account, error) {
	accounts, err := v.store.ListAccounts(ctx, spaceID, store.AccountQuery{})
	if err != nil {
		return nil, err
	}
	today := domain.DateOf(v.now())
	var out []store.Account
	for _, account := range accounts {
		if account.Kind != domain.KindAsset {
			continue
		}
		if _, ok := v.Valuers[account.Type]; !ok {
			continue
		}
		if !force && !domain.StaleValuation(lastValuedOn(account), today, StaleAfterDays) {
			continue
		}
		out = append(out, account)
	}
	return out, nil
}

func (v *Valuation) Revalue(ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID) (ValuationResult, error) {
	result, _, err := v.revalueOne(ctx, spaceID, accountID, true)
	return result, err
}

// revalueOne re-prices one asset under its lock, and says whether it was
// looked at: unforced, an asset another pass stamped while this one waited is
// passed over. Two lookups of one asset at once would each write the full
// difference from the same ledger, and the asset would move twice.
func (v *Valuation) revalueOne(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, force bool,
) (ValuationResult, bool, error) {
	release, err := v.store.NamedLock(ctx, "revalue:"+accountID.String())
	if err != nil {
		return ValuationResult{}, false, err
	}
	defer release()

	account, err := v.store.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		return ValuationResult{}, false, err
	}
	if account.Kind != domain.KindAsset {
		return ValuationResult{}, false, fmt.Errorf("service: %q is not an asset account", account.Name)
	}
	today := domain.DateOf(v.now())
	if !force && !domain.StaleValuation(lastValuedOn(account), today, StaleAfterDays) {
		return ValuationResult{AccountID: account.ID, Name: account.Name}, false, nil
	}
	result, err := v.revalue(ctx, spaceID, account, today)
	return result, true, err
}

// skippedFor is the reason a failed lookup gives in a run's results.
func skippedFor(err error) string {
	if errors.Is(err, browser.ErrNoFirefox) {
		return ErrValuationNeedsCamoufox.Error()
	}
	return err.Error()
}

// ErrValuationNeedsCamoufox is a lookup on a server with no Camoufox: the
// valuation sources run in Firefox via Camoufox, and only there.
var ErrValuationNeedsCamoufox = errors.New(
	"asset valuation runs in Camoufox, and this server has no CAMOUFOX_URL set")

func (v *Valuation) revalue(
	ctx context.Context, spaceID store.SpaceID, account store.Account, today domain.Date,
) (ValuationResult, error) {
	result := ValuationResult{AccountID: account.ID, Name: account.Name}

	valuer, ok := v.Valuers[account.Type]
	if !ok {
		result.Skipped = "no valuation source is configured for this asset type"
		return result, nil
	}
	result.Source = valuer.Name()
	if account.IsConnected() {
		// The feed rewrites the balance on every sync, so an adjustment
		// would only bend the history behind a figure it cannot move.
		result.Skipped = "a connected account's balance comes from its bank"
		return result, nil
	}

	subject := provider.ValuationSubject{
		AssetType: account.Type,
		Address:   account.PropertyAddress,
		VIN:       account.VehicleVIN,
		Mileage:   -1,
	}
	if account.HasVehicleMileage {
		// Aged forward from the last odometer reading.
		subject.Mileage = domain.ProjectedMileage(
			account.VehicleMileage, account.MileageAsOf,
			account.MilesPerYear, account.HasMilesPerYear, today)
		result.MileageUsed, result.HasMileage = subject.Mileage, true
	}

	quote, err := valuer.Estimate(ctx, subject)
	if errors.Is(err, browser.ErrNoFirefox) {
		return result, fmt.Errorf("%w: %w", ErrValuationNeedsCamoufox, err)
	}
	if err != nil {
		return result, err
	}
	if quote == nil {
		result.Skipped = "the source had no estimate for this asset"
		return result, nil
	}
	result.Estimate, result.HasEstimate = quote.Value, true
	priced := quote.Priced
	result.Priced = &priced

	postings, err := v.store.LoadPostings(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	if err != nil {
		return result, err
	}
	revaluation, moved := domain.RevaluationOf(store.DomainAccount(account), postings, quote.Value)
	at := v.now()
	account.ValuationSource = valuer.Name()
	account.ValuedAt = &at
	if !moved {
		// Still stamped, or the asset stays due and is looked up again.
		result.Skipped = "the estimate matches what the ledger already says"
		return result, v.store.UpdateAccount(ctx, spaceID, &account)
	}
	result.Adjustment, result.HasAdjust = revaluation.Adjustment, true
	if revaluation.MovesAnchor {
		account.ProviderBalance, account.ProviderBalanceAt = revaluation.Anchor, &at
	}

	txn := &store.Transaction{
		AccountID:     account.ID,
		Date:          today,
		Amount:        revaluation.Adjustment,
		Currency:      account.Currency,
		StatementName: fmt.Sprintf("Valuation — %s", valuer.Name()),
		Payee:         "Revaluation",
		Source:        domain.SourceBalanceAdjustment,
		IsReviewed:    true,
	}
	// One transaction: a row without its anchor, or the reverse, shifts the
	// history by the adjustment.
	return result, v.store.InTx(ctx, func(tx *store.Store) error {
		if err := tx.CreateTransaction(ctx, spaceID, txn); err != nil {
			return err
		}
		if err := tx.UpdateAccount(ctx, spaceID, &account); err != nil {
			return err
		}
		return RecomputeRunningBalances(ctx, tx, spaceID, account.ID)
	})
}

func lastValuedOn(account store.Account) domain.Date {
	if account.ValuedAt == nil {
		return domain.Date{}
	}
	return domain.DateOf(*account.ValuedAt)
}

// ----- value history ---------------------------------------------------------

// ImportHistory writes an asset's exported value history into the ledger as
// differences against what the account already has, so reimporting the same
// file writes nothing. All in one transaction.
func (v *Valuation) ImportHistory(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, points []domain.ValuePoint,
) (int, error) {
	account, err := v.store.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		return 0, err
	}

	txns, err := v.store.ListTransactions(ctx, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{account.ID}})
	if err != nil {
		return 0, err
	}
	existing := make([]domain.DatedAmount, 0, len(txns))
	for _, txn := range txns {
		existing = append(existing, domain.DatedAmount{On: txn.Date, Amount: txn.Amount})
	}

	rows := domain.ValuationAdjustments(account.OpeningBalance, existing, points)
	err = v.store.InTx(ctx, func(tx *store.Store) error {
		for _, row := range rows {
			txn := &store.Transaction{
				AccountID:     account.ID,
				Date:          row.On,
				Amount:        row.Amount,
				Currency:      account.Currency,
				StatementName: "Valuation — imported history",
				Payee:         "Revaluation",
				Source:        domain.SourceBalanceAdjustment,
				IsReviewed:    true,
			}
			if err := tx.CreateTransaction(ctx, spaceID, txn); err != nil {
				return err
			}
		}
		// Rebuild now so the chart reaches back without waiting for the
		// nightly pass.
		return tx.RebuildAccountHistory(ctx, spaceID, account.ID, domain.DateOf(v.now()))
	})
	if err != nil {
		return 0, err
	}
	return len(rows), nil
}
