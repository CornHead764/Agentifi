package store

import (
	"context"
	"fmt"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/pgconv"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Database rows to domain structs — the only place that mapping exists, so
// services cannot drift on defaults such as effective date falling back to the
// posted date. Ids become strings because the domain (and the golden tests,
// built from Simplifi's opaque ids) uses strings; parse back with ParseID.
//
// Splits and tags must already be attached (attachAllocations does it for
// every Transaction this package returns); a hand-assembled row maps as
// untagged and unsplit.

func DomainAccount(a Account) domain.Account {
	return domain.Account{
		ID:                       domainID(a.ID),
		Name:                     a.Name,
		Kind:                     a.Kind,
		Currency:                 a.Currency,
		ProviderBalance:          a.ProviderBalance,
		HasProviderBalance:       a.HasProviderBalance,
		OpeningBalance:           a.OpeningBalance,
		AddedOn:                  addedOn(a.CreatedAt),
		OpeningBalanceOn:         a.OpeningBalanceOn,
		ObservedSince:            a.ObservedSince,
		HistoryStartsOn:          a.HistoryStartsOn,
		IsClosed:                 a.IsClosed,
		AcceptZeroBalance:        a.AcceptZeroBalance,
		IsIgnored:                a.IsIgnored(),
		ExcludedFromReports:      a.ExcludedFromReports,
		ExcludedFromSpendingPlan: a.ExcludedFromSpendingPlan,
		IncludeInNetWorth:        a.IncludeInNetWorth,
		SecuredByAccountID:       nullDomainID(pgconv.NullUUID(a.SecuredByAccountID)),
		GoalBalance:              a.GoalBalance,
		PendingHolds:             a.PendingHolds,
		CreditLimit:              a.CreditLimit,
		HasCreditLimit:           a.HasCreditLimit,
		RequiresReceipts: !a.IsDeleted &&
			domain.AccountRequiresReceipts(a.Type, a.Kind, a.Name, a.RequiresReceipts, a.HasRequiresReceipts),
	}
}

// addedOn is the account's creation day in the server's timezone — the one
// the scheduler dates today's snapshot in. Zero for an unwritten row.
func addedOn(createdAt time.Time) domain.Date {
	if createdAt.IsZero() {
		return domain.Date{}
	}
	return domain.DateOf(createdAt.Local())
}

func DomainCategory(c Category) domain.Category {
	return domain.Category{
		ID:              domainID(c.ID),
		Name:            c.Name,
		Kind:            c.Kind,
		ParentID:        nullDomainID(pgconv.NullUUID(c.ParentID)),
		TxfID:           c.TxfID,
		KnownCategoryID: c.KnownCategoryID,

		ExcludedFromReports:      c.ExcludedFromReports,
		ExcludedFromSpendingPlan: c.ExcludedFromSpendingPlan,
	}
}

// DomainSplit maps one split. Its tags are its own: the union with the
// parent's tags is a domain filtering rule.
func DomainSplit(s Split) domain.Split {
	return domain.Split{
		ID:         domainID(s.ID),
		Amount:     s.Amount,
		CategoryID: nullDomainID(pgconv.NullUUID(s.CategoryID)),
		Memo:       s.Memo,
		TagIDs:     domainIDs(s.TagIDs),
	}
}

func DomainTransaction(t Transaction) domain.Transaction {
	splits := make([]domain.Split, len(t.Splits))
	for i, split := range t.Splits {
		splits[i] = DomainSplit(split)
	}
	return domain.Transaction{
		ID:                       domainID(t.ID),
		AccountID:                domainID(t.AccountID),
		Date:                     t.Date,
		Amount:                   t.Amount,
		StatementName:            t.StatementName,
		Payee:                    t.Payee,
		EffectiveDate:            t.EffectiveDate,
		CategoryID:               nullDomainID(pgconv.NullUUID(t.CategoryID)),
		Source:                   t.Source,
		Currency:                 t.Currency,
		AmountPrimary:            t.AmountPrimary,
		HasAmountPrimary:         t.HasAmountPrimary,
		FxRateUsed:               t.FxRateUsed,
		IsPending:                t.IsPending,
		IsEstimate:               t.EstimateStatus != "",
		IsDeleted:                t.IsDeleted,
		IsReviewed:               t.IsReviewed,
		ExcludedFromReports:      t.ExcludedFromReports,
		ExcludedFromSpendingPlan: t.ExcludedFromSpendingPlan,
		ReceiptNotNeeded:         t.ReceiptNotNeeded,
		TransferPairID:           nullDomainID(pgconv.NullUUID(t.TransferPairID)),
		PaddedTxnID:              nullDomainID(pgconv.NullUUID(t.PaddedTxnID)),
		Splits:                   splits,
		TagIDs:                   domainIDs(t.TagIDs),
	}
}

// CountsTowardBalance answers domain.CountsTowardBalance for an unjoined row —
// the Go form of MoneyMoved. Both halves matter: not-deleted alone admits
// every forecast a provider wrote ahead of its due date.
func CountsTowardBalance(txn Transaction) bool {
	return domain.CountsTowardBalance(domain.Posting{Txn: DomainTransaction(txn)})
}

// IsTransfer answers domain.Posting.IsTransfer for an unjoined row: a leg of a
// matched pair *or* a row filed under a transfer-type category. Checking the
// pair id alone misses households that file transfers by category without
// matching legs.
//
// categories must be the space's own, deleted included — a deleted category
// still says what its rows are. A category missing from the map reads as
// uncategorized, not a transfer.
//
// Questions about the link itself (re-pairing, finding the other leg) test the
// pair id, not this.
func IsTransfer(txn Transaction, categories map[uuid.UUID]Category) bool {
	posting := domain.Posting{Txn: DomainTransaction(txn)}
	if txn.CategoryID != uuid.Nil {
		if category, ok := categories[txn.CategoryID]; ok {
			posting.Category, posting.HasCategory = DomainCategory(category), true
		}
	}
	return posting.IsTransfer()
}

// BuildPostings joins transactions to their account and category.
//
// A transaction whose account is missing is an error, not a skipped row: it
// means the loads used different filters, a tenancy bug that would otherwise
// show as a quietly short total. An empty category id is uncategorized; a
// non-empty one that fails to resolve is an error too, since the schema nulls
// ids on delete, and an unresolved transfer category would count as income or
// expense.
func BuildPostings(txns []Transaction, accounts []Account, categories []Category) ([]domain.Posting, error) {
	accountByID := make(map[domain.ID]domain.Account, len(accounts))
	for _, a := range accounts {
		accountByID[domainID(a.ID)] = DomainAccount(a)
	}
	categoryByID := make(map[domain.ID]domain.Category, len(categories))
	for _, c := range categories {
		categoryByID[domainID(c.ID)] = DomainCategory(c)
	}
	postings, err := domain.ResolvePostings(DomainTransactions(txns), accountByID, categoryByID)
	if err != nil {
		return nil, fmt.Errorf("store: a load left rows unresolved: %w", err)
	}
	return postings, nil
}

// LoadPostings loads the transactions a query selects, joined to every account
// and category in the space. Accounts and categories are loaded unfiltered —
// deleted and closed included — because a transaction in a closed account still
// has to resolve. Linked refunds are refiled, as in service.LoadPostings, so
// both read paths answer alike.
func (s *Store) LoadPostings(ctx context.Context, spaceID SpaceID, q TransactionQuery) ([]domain.Posting, error) {
	txns, err := s.ListTransactions(ctx, spaceID, q)
	if err != nil {
		return nil, err
	}
	accounts, err := s.ListAccounts(ctx, spaceID, AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return nil, err
	}
	categories, err := s.ListCategories(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	postings, err := BuildPostings(txns, accounts, categories)
	if err != nil {
		return nil, err
	}
	return s.RefileRefunds(ctx, spaceID, postings, categories)
}

func DomainAccounts(rows []Account) []domain.Account {
	out := make([]domain.Account, len(rows))
	for i, row := range rows {
		out[i] = DomainAccount(row)
	}
	return out
}

func DomainTransactions(rows []Transaction) []domain.Transaction {
	out := make([]domain.Transaction, len(rows))
	for i, row := range rows {
		out[i] = DomainTransaction(row)
	}
	return out
}

// TransactionFacets are the row attributes a filter can test that a
// domain.Transaction does not carry. An unknown facet fails its item rather
// than passing it, so every filter caller must supply these or a flagged-only
// view silently drops rows.
type TransactionFacets struct {
	UserFlag             string
	IsBillOrSubscription bool
	CategoryChecked      bool
	// HasCategorySuggestion is not on the row; see TransactionsWithSuggestion.
	HasCategorySuggestion bool
	// HasAttachment is not on the row either; see TransactionsWithDocuments.
	HasAttachment bool
	// MissingReceipt is not on the row either; see service.ReceiptStatuses.
	MissingReceipt bool
}

func Facets(t Transaction) TransactionFacets {
	return TransactionFacets{
		UserFlag:             t.UserFlag,
		IsBillOrSubscription: t.IsBill || t.IsSubscription,
		CategoryChecked:      t.CategoryCheckedAt != nil,
	}
}
