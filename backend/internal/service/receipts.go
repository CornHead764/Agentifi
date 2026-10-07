package service

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// ReceiptStatuses answers domain.ReceiptStatusOf for each row, keyed by id.
// documented is the rows with any document behind them, as
// store.TransactionsWithDocuments counts one. A row no account holds to a
// receipt is left out of the map, which reads as domain.ReceiptNotRequired.
func ReceiptStatuses(
	ctx context.Context, db *store.Store, spaceID store.SpaceID,
	rows map[uuid.UUID]store.Transaction, documented map[uuid.UUID]bool,
) (map[uuid.UUID]domain.ReceiptStatus, error) {
	out := map[uuid.UUID]domain.ReceiptStatus{}
	if len(rows) == 0 {
		return out, nil
	}
	accounts, err := db.ListAccounts(ctx, spaceID,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	requiring := false
	for _, account := range accounts {
		requiring = requiring || store.DomainAccount(account).RequiresReceipts
	}
	if !requiring {
		return out, nil
	}
	categories, err := db.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	txns := make([]store.Transaction, 0, len(rows))
	for _, row := range rows {
		txns = append(txns, row)
	}
	postings, err := store.BuildPostings(txns, accounts, categories)
	if err != nil {
		return nil, err
	}
	byID := make(map[domain.ID]domain.Category, len(categories))
	for _, category := range categories {
		byID[domain.ID(category.ID.String())] = store.DomainCategory(category)
	}
	for _, posting := range postings {
		id, err := store.ParseID(posting.Txn.ID)
		if err != nil {
			return nil, err
		}
		if status := domain.ReceiptStatusOf(posting, byID, documented[id]); status != domain.ReceiptNotRequired {
			out[id] = status
		}
	}
	return out, nil
}
