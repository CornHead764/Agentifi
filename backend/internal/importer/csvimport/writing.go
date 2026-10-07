package csvimport

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The only part of the CSV import that touches a database.
//
// A CSV row carries no ids, so each account, category, tag and transaction is
// matched against what the space already holds by its natural key (name, full
// path, name, derived external id), and an id the mapper allocated is replaced
// by the existing row's wherever one is found. The whole run is one
// transaction.

// ErrRefused is returned rather than writing a ledger the report says not to
// trust.
var ErrRefused = errors.New("csvimport: refusing to write a run with errors")

// WriteResult separates what the run added from what was already there.
type WriteResult struct {
	AccountsCreated  int
	AccountsExisting int

	CategoriesCreated  int
	CategoriesExisting int

	TagsCreated  int
	TagsExisting int

	TransactionsWritten int
	// TransactionsSkipped counts rows already in the space under the same
	// derived external id.
	TransactionsSkipped int
	SplitsWritten       int
	TagLinksWritten     int

	// TransactionIDs are the rows this run inserted, in file order, for the
	// caller to run through service.Ingest.AfterIngest.
	TransactionIDs []uuid.UUID
}

// Rows returns the total the CLI prints.
func (r WriteResult) Rows() int {
	return r.AccountsCreated + r.CategoriesCreated + r.TagsCreated +
		r.TransactionsWritten + r.SplitsWritten + r.TagLinksWritten
}

// Write inserts one mapped file into a space, inside a single transaction.
func Write(ctx context.Context, db *store.Store, spaceID store.SpaceID, m *Mapped) (WriteResult, error) {
	if !m.Report.OK() {
		return WriteResult{}, fmt.Errorf("%w: %d errors; nothing was written. See the report for "+
			"the line and column of each", ErrRefused, len(m.Report.Errors))
	}
	var result WriteResult
	err := db.InTx(ctx, func(tx *store.Store) error {
		w := &writer{ctx: ctx, tx: tx, space: spaceID, remap: map[uuid.UUID]uuid.UUID{}}
		if err := w.writeAccounts(m, &result); err != nil {
			return err
		}
		if err := w.writeCategories(m, &result); err != nil {
			return err
		}
		if err := w.writeTags(m, &result); err != nil {
			return err
		}
		return w.writeTransactions(m, &result)
	})
	if err != nil {
		return WriteResult{}, err
	}
	return result, nil
}

type writer struct {
	ctx   context.Context
	tx    *store.Store
	space store.SpaceID
	// remap sends an id the mapper allocated to the id of the row that turned
	// out to already exist. Applied to every foreign key before it is written.
	remap map[uuid.UUID]uuid.UUID
}

func (w *writer) id(allocated uuid.UUID) uuid.UUID {
	if existing, found := w.remap[allocated]; found {
		return existing
	}
	return allocated
}

func (w *writer) writeAccounts(m *Mapped, result *WriteResult) error {
	// Closed and deleted accounts count as existing, so a re-import never
	// splits one account's history across two of the same name.
	existing, err := w.tx.ListAccounts(w.ctx, w.space,
		store.AccountQuery{IncludeDeleted: true, IncludeClosed: true})
	if err != nil {
		return err
	}
	byName := make(map[string]uuid.UUID, len(existing))
	for _, account := range existing {
		byName[strings.ToLower(account.Name)] = account.ID
	}

	for _, account := range m.Accounts {
		if found, ok := byName[strings.ToLower(account.Row.Name)]; ok {
			w.remap[account.Row.ID] = found
			result.AccountsExisting++
			continue
		}
		row := account.Row
		row.SortOrder = len(existing) + result.AccountsCreated
		if err := w.tx.CreateAccount(w.ctx, w.space, &row); err != nil {
			return err
		}
		byName[strings.ToLower(row.Name)] = row.ID
		result.AccountsCreated++
	}
	return nil
}

func (w *writer) writeCategories(m *Mapped, result *WriteResult) error {
	existing, err := w.tx.ListCategories(w.ctx, w.space, true)
	if err != nil {
		return err
	}
	byPath := existingCategoryPaths(existing)

	// m.Categories is already shallowest-first, which both the parent_id
	// lookup below and Postgres' own check on the insert need.
	for _, category := range m.Categories {
		key := categoryLevelsKey(splitCategoryPath(category.Path))
		if found, ok := byPath[key]; ok {
			w.remap[category.Row.ID] = found
			result.CategoriesExisting++
			continue
		}
		row := category.Row
		row.ParentID = w.id(row.ParentID)
		row.SortOrder = len(existing) + result.CategoriesCreated
		if err := w.tx.CreateCategory(w.ctx, w.space, &row); err != nil {
			return err
		}
		byPath[key] = row.ID
		result.CategoriesCreated++
	}
	return nil
}

// existingCategoryPaths rebuilds each stored category's full path, so the
// file's "Auto & Transport:Registration" matches the row it already has; a
// leaf name alone is ambiguous.
func existingCategoryPaths(categories []store.Category) map[string]uuid.UUID {
	byID := make(map[uuid.UUID]store.Category, len(categories))
	for _, category := range categories {
		byID[category.ID] = category
	}
	out := make(map[string]uuid.UUID, len(categories))
	for _, category := range categories {
		levels := domain.CategoryPath(category.ID, func(id uuid.UUID) (string, uuid.UUID, bool) {
			found, ok := byID[id]
			if !ok {
				return "", uuid.Nil, false
			}
			return found.Name, found.ParentID, true
		})
		out[categoryLevelsKey(levels)] = category.ID
	}
	return out
}

// categoryLevelsKey is the key a path is matched by: its levels folded and
// joined by a byte no name holds, so a stored category whose own name holds
// the file's separator is never read as two nested ones.
func categoryLevelsKey(levels []string) string {
	return domain.FoldName(strings.Join(levels, "\x00"))
}

func (w *writer) writeTags(m *Mapped, result *WriteResult) error {
	existing, err := w.tx.ListTags(w.ctx, w.space, true)
	if err != nil {
		return err
	}
	byName := make(map[string]uuid.UUID, len(existing))
	for _, tag := range existing {
		byName[strings.ToLower(tag.Name)] = tag.ID
	}

	for _, tag := range m.Tags {
		if found, ok := byName[strings.ToLower(tag.Name)]; ok {
			w.remap[tag.ID] = found
			result.TagsExisting++
			continue
		}
		row := *tag
		if err := w.tx.CreateTag(w.ctx, w.space, &row); err != nil {
			return err
		}
		byName[strings.ToLower(row.Name)] = row.ID
		result.TagsCreated++
	}
	return nil
}

func (w *writer) writeTransactions(m *Mapped, result *WriteResult) error {
	// Deleted rows count as present. A row a previous import wrote and the
	// user then deleted must not come back on the next run.
	existing, err := w.tx.ListTransactions(w.ctx, w.space,
		store.TransactionQuery{IncludeDeleted: true})
	if err != nil {
		return err
	}
	written := make(map[string]bool, len(existing))
	for _, txn := range existing {
		if txn.ExternalID != "" {
			// Keyed on the account too: an OFX FITId is unique per account
			// only.
			written[txn.AccountID.String()+"\x00"+txn.ExternalID] = true
		}
	}

	for _, mapped := range m.Transactions {
		row := *mapped
		row.AccountID = w.id(row.AccountID)
		row.CategoryID = w.id(row.CategoryID)
		row.TagIDs = w.ids(row.TagIDs)
		row.Splits = make([]store.Split, len(mapped.Splits))
		for i, split := range mapped.Splits {
			split.CategoryID = w.id(split.CategoryID)
			split.TagIDs = w.ids(split.TagIDs)
			row.Splits[i] = split
		}
		if written[row.AccountID.String()+"\x00"+row.ExternalID] {
			result.TransactionsSkipped++
			continue
		}
		// Written with the row: the settle runs after commit, and one that
		// fails must leave its backlog on the row for `agentifi settle`.
		row.NeedsSettle = true
		if err := w.tx.CreateTransaction(w.ctx, w.space, &row); err != nil {
			return err
		}
		written[row.AccountID.String()+"\x00"+row.ExternalID] = true
		result.TransactionIDs = append(result.TransactionIDs, row.ID)
		result.TransactionsWritten++
		result.SplitsWritten += len(row.Splits)
		result.TagLinksWritten += len(row.TagIDs)
		for _, split := range row.Splits {
			result.TagLinksWritten += len(split.TagIDs)
		}
	}
	return nil
}

func (w *writer) ids(allocated []uuid.UUID) []uuid.UUID {
	if len(allocated) == 0 {
		return nil
	}
	out := make([]uuid.UUID, len(allocated))
	for i, id := range allocated {
		out[i] = w.id(id)
	}
	return out
}
