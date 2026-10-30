package api

import (
	"context"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func loadRegisterPage(ctx context.Context, env *Env, sp auth.SpaceContext, query RegisterQuery) (registerMatch, TransactionPage, error) {
	totals := TransactionPage{Window: windowResponse(query.Window), Limit: query.Limit, Offset: query.Offset}
	var matched registerMatch
	if query.HasFilter {
		var err error
		matched, err = matchRegister(ctx, env, sp, query)
		if err != nil {
			return registerMatch{}, totals, err
		}
		postings := matched.Postings
		var padding []domain.Posting
		if query.HidePadding {
			postings, padding = domain.FoldPadding(postings)
		}
		kept := func(p domain.Posting) domain.Money { return domain.MatchedAmount(p, matched.Partial[p.Txn.ID]) }
		totals.Count = len(postings)
		totals.Total = domain.Sum(postings, kept)
		totals.FullTotal = domain.Sum(postings, domain.Posting.Amount)
		totals.PaddingCount = len(padding)
		totals.PaddingTotal = domain.Sum(padding, kept)
		for _, posting := range postings {
			if _, ok := matched.Partial[posting.Txn.ID]; ok {
				totals.PartialCount++
			}
		}
		matched.Postings = registerSlice(postings, query.Offset, query.Limit)
		return matched, totals, nil
	}
	matched.Rows = map[uuid.UUID]store.Transaction{}
	if query.Accounts.selectsNothing() {
		return matched, totals, nil
	}
	from, to, mode := query.Window.storeQuery()
	entries, err := env.DB.RegisterEntries(ctx, sp.ID(), store.RegisterQuery{
		TransactionQuery: store.TransactionQuery{AccountIDs: query.Accounts.IDs, From: from, To: to, DateMode: mode},
		HasReviewed:      query.HasReviewed, IsReviewed: query.IsReviewed, Search: query.Search, Descending: query.Descending,
	})
	if err != nil {
		return matched, totals, err
	}
	var padding []store.RegisterEntry
	if query.HidePadding {
		shown := entries[:0]
		for _, entry := range entries {
			if entry.IsPadding {
				padding = append(padding, entry)
			} else {
				shown = append(shown, entry)
			}
		}
		entries = shown
	}
	amount := func(entry store.RegisterEntry) domain.Money { return entry.Amount }
	totals.Count = len(entries)
	totals.Total = domain.Sum(entries, amount)
	totals.FullTotal = totals.Total
	totals.PaddingCount = len(padding)
	totals.PaddingTotal = domain.Sum(padding, amount)
	page := registerSlice(entries, query.Offset, query.Limit)
	if len(page) == 0 {
		return matched, totals, nil
	}
	ids := make([]uuid.UUID, 0, len(page))
	for _, entry := range page {
		ids = append(ids, entry.ID)
		matched.Postings = append(matched.Postings, domain.Posting{Txn: domain.Transaction{ID: domain.ID(entry.ID.String())}})
	}
	rows, err := env.DB.ListTransactions(ctx, sp.ID(), store.TransactionQuery{IDs: ids})
	if err != nil {
		return matched, totals, err
	}
	for _, row := range rows {
		matched.Rows[row.ID] = row
	}
	return matched, totals, nil
}

func registerSlice[T any](rows []T, offset, limit int) []T {
	if offset >= len(rows) {
		return nil
	}
	return rows[offset:min(len(rows), offset+limit)]
}
