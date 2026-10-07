package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// AccountForecasts reads the input for domain.EstimateAccountCashFlow and
// stores its answer per account.
type AccountForecasts struct {
	base
}

func NewAccountForecasts(st *store.Store) *AccountForecasts {
	return &AccountForecasts{base: newBase(st)}
}

// RefreshAccount re-estimates one account from its ledger as of today. False
// with no error when there is no complete month to average; any earlier
// estimate is left standing.
func (f *AccountForecasts) RefreshAccount(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID, today domain.Date,
) (store.CashFlowForecast, bool, error) {
	postings, _, err := LoadPostings(ctx, f.store, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{accountID}})
	if err != nil {
		return store.CashFlowForecast{}, false, err
	}
	return f.save(ctx, spaceID, accountID, postings, today)
}

// RefreshSpace re-estimates every open account in a space and reports how
// many estimates it stored. One account's failure does not stop the rest; the
// first error is returned at the end.
func (f *AccountForecasts) RefreshSpace(
	ctx context.Context, spaceID store.SpaceID, today domain.Date,
) (int, error) {
	accounts, err := f.store.ListAccounts(ctx, spaceID, store.AccountQuery{})
	if err != nil {
		return 0, err
	}
	if len(accounts) == 0 {
		return 0, nil
	}
	ids := make([]uuid.UUID, 0, len(accounts))
	for _, account := range accounts {
		ids = append(ids, account.ID)
	}
	postings, _, err := LoadPostings(ctx, f.store, spaceID, store.TransactionQuery{AccountIDs: ids})
	if err != nil {
		return 0, err
	}
	byAccount := make(map[domain.ID][]domain.Posting, len(accounts))
	for _, posting := range postings {
		byAccount[posting.Txn.AccountID] = append(byAccount[posting.Txn.AccountID], posting)
	}

	made := 0
	var first error
	for _, account := range accounts {
		if ctx.Err() != nil {
			return made, ctx.Err()
		}
		_, stored, err := f.save(ctx, spaceID, account.ID,
			byAccount[domain.ID(account.ID.String())], today)
		if err != nil {
			if first == nil {
				first = err
			}
			continue
		}
		if stored {
			made++
		}
	}
	return made, first
}

func (f *AccountForecasts) save(
	ctx context.Context, spaceID store.SpaceID, accountID uuid.UUID,
	postings []domain.Posting, today domain.Date,
) (store.CashFlowForecast, bool, error) {
	forecast, ok := domain.EstimateAccountCashFlow(domain.AccountCashFlows(postings), today)
	if !ok {
		return store.CashFlowForecast{}, false, nil
	}
	row := store.CashFlowForecast{AccountID: accountID, GeneratedOn: today, Forecast: forecast}
	if err := f.store.ReplaceAccountForecast(ctx, spaceID, &row); err != nil {
		return store.CashFlowForecast{}, false, err
	}
	return row, true, nil
}
