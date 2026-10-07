package store

import (
	"context"
	"errors"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// ErrCategoryProtected is a delete of a category domain.CategoryProtection
// keeps.
var ErrCategoryProtected = errors.New("store: category is protected")

// releasePairSet is the SET clause of every write that releases a transfer
// pair: the category the pairing filed goes with it, one a person chose stays.
const releasePairSet = `transfer_pair_id = NULL,
	category_id = CASE WHEN category_from_pair THEN NULL ELSE category_id END,
	category_from_pair = false, updated_at = now()`

// UnlinkTransferPair releases both legs of one pair and reports how many rows
// it released.
func (s *Store) UnlinkTransferPair(ctx context.Context, spaceID SpaceID, pairID uuid.UUID) (int, error) {
	tag, err := s.db.Exec(ctx, `UPDATE transactions SET `+releasePairSet+`
		WHERE space_id = $1 AND transfer_pair_id = $2`, spaceID.UUID(), pairID)
	if err != nil {
		return 0, wrap("store: unlink transfer pair", err)
	}
	return int(tag.RowsAffected()), nil
}

// ReleaseTransferLegs releases the given legs alone, for a leg whose partner
// is gone.
func (s *Store) ReleaseTransferLegs(ctx context.Context, spaceID SpaceID, ids []uuid.UUID) error {
	_, err := s.db.Exec(ctx, `UPDATE transactions SET `+releasePairSet+`
		WHERE space_id = $1 AND id = ANY($2)`, spaceID.UUID(), ids)
	return wrap("store: release transfer legs", err)
}

// FileTransferLegs files every matched leg nobody has categorized, after a
// write that pairs rows outside PairTransactions.
func (s *Store) FileTransferLegs(ctx context.Context, spaceID SpaceID) error {
	return s.InTx(ctx, func(tx *Store) error { return tx.fileTransferLegs(ctx, spaceID, nil) })
}

// fileTransferLegs puts each uncategorized, unsplit leg of the given pairs
// (every pair when pairIDs is nil) under the category
// domain.TransferCategoryFor names for its pair, marked as the pairing's so a
// release takes it back off. A leg already categorized keeps its category.
func (s *Store) fileTransferLegs(ctx context.Context, spaceID SpaceID, pairIDs []uuid.UUID) error {
	byMarker, err := s.EnsureTransferCategories(ctx, spaceID)
	if err != nil {
		return err
	}
	rows, err := s.db.Query(ctx, `
		SELECT t.id, t.transfer_pair_id, a.kind,
		       t.category_id IS NULL AND NOT EXISTS (
		           SELECT 1 FROM transaction_splits s WHERE s.transaction_id = t.id)
		FROM transactions t JOIN accounts a ON a.id = t.account_id
		WHERE t.space_id = $1 AND t.transfer_pair_id IS NOT NULL AND NOT t.is_deleted
		  AND ($2::uuid[] IS NULL OR t.transfer_pair_id = ANY($2))`,
		spaceID.UUID(), pairIDs)
	if err != nil {
		return wrap("store: load transfer legs", err)
	}
	defer rows.Close()

	kinds := map[uuid.UUID][]domain.AccountKind{}
	unfiled := map[uuid.UUID][]uuid.UUID{}
	for rows.Next() {
		var id, pairID uuid.UUID
		var kind string
		var open bool
		if err := rows.Scan(&id, &pairID, &kind, &open); err != nil {
			return wrap("store: load transfer legs", err)
		}
		kinds[pairID] = append(kinds[pairID], domain.AccountKind(kind))
		if open {
			unfiled[pairID] = append(unfiled[pairID], id)
		}
	}
	if err := rows.Err(); err != nil {
		return wrap("store: load transfer legs", err)
	}

	byCategory := map[uuid.UUID][]uuid.UUID{}
	for pairID, ids := range unfiled {
		category := byMarker[domain.TransferCategoryFor(kinds[pairID]...)]
		byCategory[category] = append(byCategory[category], ids...)
	}
	for category, ids := range byCategory {
		if _, err := s.db.Exec(ctx, `
			UPDATE transactions SET category_id = $3, category_from_pair = true, updated_at = now()
			WHERE space_id = $1 AND id = ANY($2) AND category_id IS NULL`,
			spaceID.UUID(), ids, category); err != nil {
			return wrap("store: file transfer legs", err)
		}
	}
	return nil
}

// EnsureTransferCategories returns the live Transfer and Credit Card Payment
// of a space by marker, making each that is missing: a deleted marked row
// comes back, a live one of the same name in the same place is adopted when
// it is a transfer category or nothing is filed under it, and otherwise the
// default is written.
func (s *Store) EnsureTransferCategories(ctx context.Context, spaceID SpaceID) (map[string]uuid.UUID, error) {
	markers := []string{domain.KnownCategoryTransfer, domain.KnownCategoryCreditCardPayment}
	found, err := s.liveCategoriesByMarker(ctx, spaceID, markers)
	if err != nil || len(found) == len(markers) {
		return found, err
	}

	err = s.InTx(ctx, func(tx *Store) error {
		// Two pairings in one space must not each write a Transfer.
		if _, err := tx.db.Exec(ctx, `SELECT 1 FROM spaces WHERE id = $1 FOR UPDATE`, spaceID.UUID()); err != nil {
			return wrap("store: lock space", err)
		}
		held, err := tx.ListCategories(ctx, spaceID, true)
		if err != nil {
			return err
		}
		order := 0
		for _, one := range held {
			if !one.IsDeleted {
				order++
			}
		}

		var group domain.DefaultCategory
		for _, one := range domain.DefaultCategories {
			if one.KnownCategoryID == domain.KnownCategoryTransfer {
				group = one
			}
		}
		entries := []domain.DefaultCategory{group}
		entries = append(entries, group.Children...)
		for _, entry := range entries {
			parent := uuid.Nil
			if entry.KnownCategoryID != group.KnownCategoryID {
				parent = found[group.KnownCategoryID]
			}
			id, err := tx.ensureTransferCategory(ctx, spaceID, held, group, entry, parent, order)
			if err != nil {
				return err
			}
			found[entry.KnownCategoryID] = id
			order++
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return found, nil
}

func (s *Store) ensureTransferCategory(
	ctx context.Context, spaceID SpaceID, held []Category,
	group, entry domain.DefaultCategory, parent uuid.UUID, order int,
) (uuid.UUID, error) {
	for i := range held {
		if held[i].KnownCategoryID == entry.KnownCategoryID && !held[i].IsDeleted {
			return held[i].ID, nil
		}
	}
	for i := range held {
		if held[i].KnownCategoryID == entry.KnownCategoryID {
			held[i].IsDeleted = false
			return held[i].ID, s.UpdateCategory(ctx, spaceID, &held[i])
		}
	}
	names := map[string]bool{strings.ToLower(entry.Name): true, strings.ToLower(entry.Name) + "s": true}
	for i := range held {
		one := &held[i]
		if one.IsDeleted || !names[strings.ToLower(strings.TrimSpace(one.Name))] {
			continue
		}
		if _, depended := domain.CategoryDependedOn(one.KnownCategoryID); depended {
			continue
		}
		if one.ParentID != uuid.Nil && (parent == uuid.Nil || one.ParentID != parent) {
			continue
		}
		if one.Kind != domain.CategoryTransfer {
			used, err := s.categoryInUse(ctx, one.ID)
			if err != nil {
				return uuid.Nil, err
			}
			if used {
				continue
			}
		}
		one.Kind = domain.CategoryTransfer
		one.KnownCategoryID = entry.KnownCategoryID
		return one.ID, s.UpdateCategory(ctx, spaceID, one)
	}
	row := seededCategory(group, entry)
	row.ParentID = parent
	row.SortOrder = order
	if err := s.CreateCategory(ctx, spaceID, &row); err != nil {
		return uuid.Nil, err
	}
	return row.ID, nil
}

func (s *Store) liveCategoriesByMarker(ctx context.Context, spaceID SpaceID, markers []string) (map[string]uuid.UUID, error) {
	rows, err := s.db.Query(ctx, `
		SELECT DISTINCT ON (known_category_id) known_category_id, id FROM categories
		WHERE space_id = $1 AND known_category_id = ANY($2) AND NOT is_deleted
		ORDER BY known_category_id, created_at, id`, spaceID.UUID(), markers)
	if err != nil {
		return nil, wrap("store: find categories by marker", err)
	}
	defer rows.Close()
	out := map[string]uuid.UUID{}
	for rows.Next() {
		var marker string
		var id uuid.UUID
		if err := rows.Scan(&marker, &id); err != nil {
			return nil, wrap("store: find categories by marker", err)
		}
		out[marker] = id
	}
	return out, wrap("store: find categories by marker", rows.Err())
}

func (s *Store) categoryInUse(ctx context.Context, id uuid.UUID) (bool, error) {
	var used bool
	err := s.db.QueryRow(ctx, `SELECT
		EXISTS (SELECT 1 FROM transactions WHERE category_id = $1)
		OR EXISTS (SELECT 1 FROM transaction_splits WHERE category_id = $1)
		OR EXISTS (SELECT 1 FROM series WHERE category_id = $1)`, id).Scan(&used)
	return used, wrap("store: check category use", err)
}
