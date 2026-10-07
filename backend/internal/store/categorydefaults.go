package store

import (
	"context"
	"strings"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// SeedCategoriesResult counts what a seed wrote and what it found already there.
type SeedCategoriesResult struct {
	Created  int
	Existing int
}

// CreateSeededSpace creates a space holding domain.DefaultCategories, the
// categories every space a person opens starts with.
func (s *Store) CreateSeededSpace(ctx context.Context, space *Space) error {
	return s.InTx(ctx, func(tx *Store) error {
		if err := tx.CreateSpace(ctx, space); err != nil {
			return err
		}
		_, err := tx.SeedCategories(ctx, space.ID, domain.DefaultCategories)
		return err
	})
}

// SeedCategories adds the given tree to a space, skipping every group and
// subcategory whose path the space already has.
//
// Paths match foldably (case and accent) against live categories only, the
// same rule the file importers use, so seeding a space that was imported into
// (or seeding twice) adds only what is missing. A missing subcategory of an
// existing group is filed under that group, whatever the group's own kind.
func (s *Store) SeedCategories(ctx context.Context, spaceID SpaceID, tree []domain.DefaultCategory) (SeedCategoriesResult, error) {
	var result SeedCategoriesResult
	err := s.InTx(ctx, func(tx *Store) error {
		existing, err := tx.ListCategories(ctx, spaceID, false)
		if err != nil {
			return err
		}
		byPath := liveCategoryPaths(existing)
		byMarker := map[string]uuid.UUID{}
		for _, one := range existing {
			if _, depended := domain.CategoryDependedOn(one.KnownCategoryID); depended {
				byMarker[one.KnownCategoryID] = one.ID
			}
		}
		order := len(existing)

		add := func(path string, parent uuid.UUID, want Category) (uuid.UUID, error) {
			if id, ok := byPath[path]; ok {
				result.Existing++
				return id, nil
			}
			// A process finds these by marker, so a renamed one is still there.
			if id, ok := byMarker[want.KnownCategoryID]; ok {
				result.Existing++
				return id, nil
			}
			row := want
			row.ParentID = parent
			row.SortOrder = order
			if err := tx.CreateCategory(ctx, spaceID, &row); err != nil {
				return uuid.Nil, err
			}
			order++
			result.Created++
			byPath[path] = row.ID
			return row.ID, nil
		}

		for _, group := range tree {
			groupPath := domain.FoldName(group.Name)
			groupID, err := add(groupPath, uuid.Nil, seededCategory(group, group))
			if err != nil {
				return err
			}
			for _, child := range group.Children {
				if _, err := add(groupPath+"\x00"+domain.FoldName(child.Name), groupID, seededCategory(group, child)); err != nil {
					return err
				}
			}
		}
		return nil
	})
	return result, err
}

// seededCategory is the row a seed writes for one entry of the tree: the
// entry's name and markers, its group's kind and exclusion pair.
func seededCategory(group, entry domain.DefaultCategory) Category {
	return Category{
		Name:                     entry.Name,
		Kind:                     group.Kind,
		KnownCategoryID:          entry.KnownCategoryID,
		TxfID:                    entry.TxfID,
		TxfIDs:                   []string{},
		IsUserAssignable:         true,
		IsEditable:               !entry.NotEditable,
		ExcludedFromReports:      group.ExcludedFromReports,
		ExcludedFromSpendingPlan: group.ExcludedFromSpendingPlan,
	}
}

// OnlyUntouchedDefaults reports whether every category the space holds,
// deleted ones included, is an entry of the tree exactly as SeedCategories
// wrote it, at its path, and nothing in the space names any of them. Sort
// order is not compared. A space with no categories holds only untouched
// defaults.
func (s *Store) OnlyUntouchedDefaults(ctx context.Context, spaceID SpaceID, tree []domain.DefaultCategory) (bool, error) {
	held, err := s.ListCategories(ctx, spaceID, true)
	if err != nil {
		return false, err
	}
	if len(held) == 0 {
		return true, nil
	}
	want := map[string]Category{}
	for _, group := range tree {
		groupPath := domain.FoldName(group.Name)
		want[groupPath] = seededCategory(group, group)
		for _, child := range group.Children {
			want[groupPath+"\x00"+domain.FoldName(child.Name)] = seededCategory(group, child)
		}
	}
	paths := liveCategoryPaths(held)
	if len(paths) != len(held) {
		return false, nil
	}
	byID := make(map[uuid.UUID]Category, len(held))
	for _, row := range held {
		byID[row.ID] = row
	}
	for path, id := range paths {
		expected, ok := want[path]
		if !ok || !asSeeded(byID[id], expected) {
			return false, nil
		}
	}
	uses, err := s.CategoryUses(ctx, spaceID, nil)
	if err != nil {
		return false, err
	}
	return len(uses) == 0, nil
}

func asSeeded(row, want Category) bool {
	return !row.IsDeleted &&
		row.Name == want.Name &&
		row.Kind == want.Kind &&
		row.KnownCategoryID == want.KnownCategoryID &&
		row.TxfID == want.TxfID &&
		len(row.TxfIDs) == 0 &&
		row.IsUserAssignable == want.IsUserAssignable &&
		row.IsEditable == want.IsEditable &&
		row.ExcludedFromReports == want.ExcludedFromReports &&
		row.ExcludedFromSpendingPlan == want.ExcludedFromSpendingPlan &&
		!row.ExcludedFromCategoryList
}

// liveCategoryPaths keys each category by its folded path from the root,
// levels joined by NUL so no category name can forge a separator.
func liveCategoryPaths(categories []Category) map[string]uuid.UUID {
	byID := make(map[uuid.UUID]Category, len(categories))
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
			return domain.FoldName(found.Name), found.ParentID, true
		})
		out[strings.Join(levels, "\x00")] = category.ID
	}
	return out
}
