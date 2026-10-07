package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Two levels, "Food" over "Café", both ids invented for the test.
func TestLiveCategoryPathsFoldsAccentsLikeTheFileImporters(t *testing.T) {
	foodID, cafeID := uuid.New(), uuid.New()
	categories := []Category{
		{ID: foodID, Name: "Food"},
		{ID: cafeID, Name: "Café", ParentID: foodID},
	}
	paths := liveCategoryPaths(categories)
	require.Equal(t, cafeID, paths["food\x00cafe"], "the accented name folds to the same key a plain one would")
}

// A corrupt parent cycle: "a" claims "b" as its parent and "b" claims "a".
func TestLiveCategoryPathsStopsOnAParentCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	categories := []Category{
		{ID: a, Name: "A", ParentID: b},
		{ID: b, Name: "B", ParentID: a},
	}
	paths := liveCategoryPaths(categories)
	require.Len(t, paths, 2, "each category still gets a path, just not an endless one")
}
