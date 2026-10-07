package csvimport

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Two levels, "Food & Dining" over "Café", both ids invented for the test.
func TestExistingCategoryPathsFoldsAccentsLikeTheFileSays(t *testing.T) {
	diningID, cafeID := uuid.New(), uuid.New()
	categories := []store.Category{
		{ID: diningID, Name: "Food & Dining"},
		{ID: cafeID, Name: "Café", ParentID: diningID},
	}
	paths := existingCategoryPaths(categories)
	require.Equal(t, cafeID, paths[categoryLevelsKey(splitCategoryPath("Food & Dining:Cafe"))])
}

// A stored category whose own name holds the file's separator is one level,
// so the file's "Home:Tools", which is always Home over Tools, finds the
// nested pair and never the single category.
func TestASeparatorInAStoredNameIsNotTwoNestedCategories(t *testing.T) {
	soloID, homeID, toolsID := uuid.New(), uuid.New(), uuid.New()
	paths := existingCategoryPaths([]store.Category{
		{ID: soloID, Name: "Home:Tools"},
		{ID: homeID, Name: "Home"},
		{ID: toolsID, Name: "Tools", ParentID: homeID},
	})
	require.Len(t, paths, 3)
	require.Equal(t, toolsID, paths[categoryLevelsKey(splitCategoryPath("Home:Tools"))])
	require.Equal(t, soloID, paths[categoryLevelsKey([]string{"Home:Tools"})])
}

// A corrupt parent cycle: "a" claims "b" as its parent and "b" claims "a".
func TestExistingCategoryPathsStopsOnAParentCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	categories := []store.Category{
		{ID: a, Name: "A", ParentID: b},
		{ID: b, Name: "B", ParentID: a},
	}
	paths := existingCategoryPaths(categories)
	require.Len(t, paths, 2, "each category still gets a path, just not an endless one")
}
