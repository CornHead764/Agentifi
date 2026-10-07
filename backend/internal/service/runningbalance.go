package service

import (
	"context"
	"errors"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// RecomputeRunningBalances rewrites the stored per-row balance for one account.
// The balance column is materialized, so every path that adds, moves or
// removes a row must call this. It recomputes the whole account rather than a
// tail so same-day rows keep a stable order.
func RecomputeRunningBalances(
	ctx context.Context, st *store.Store, spaceID store.SpaceID, accountID uuid.UUID,
) error {
	if accountID == uuid.Nil {
		return nil
	}
	account, err := st.GetAccount(ctx, spaceID, accountID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil
		}
		return err
	}

	postings, rows, err := LoadPostings(ctx, st, spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{accountID}})
	if err != nil {
		return err
	}

	balances := domain.RunningBalances(store.DomainAccount(account), postings, domain.DatePosted)
	moved := make(map[uuid.UUID]domain.Money, len(balances))
	for id, balance := range balances {
		key, err := store.ParseID(id)
		if err != nil {
			return err
		}
		row, ok := rows[key]
		if !ok {
			continue
		}
		if row.HasBalance && row.Balance.Equal(balance) {
			continue
		}
		moved[key] = balance
	}
	return st.SetTransactionBalances(ctx, spaceID, moved)
}
