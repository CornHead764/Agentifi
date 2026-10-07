package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// categoryPath renders a path for a person to read, so it keeps the
// category's own casing and accents rather than folding them away.
func TestCategoryPathKeepsCasingAndAccentsForDisplay(t *testing.T) {
	foodID, cafeID := uuid.New(), uuid.New()
	byID := map[uuid.UUID]store.Category{
		foodID: {ID: foodID, Name: "Food"},
		cafeID: {ID: cafeID, Name: "Café", ParentID: foodID},
	}
	require.Equal(t, "Food · Café", categoryPath(byID, byID[cafeID]))
}

// A corrupt parent cycle: "a" claims "b" as its parent and "b" claims "a".
func TestCategoryPathStopsOnAParentCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	byID := map[uuid.UUID]store.Category{
		a: {ID: a, Name: "A", ParentID: b},
		b: {ID: b, Name: "B", ParentID: a},
	}
	require.Equal(t, "B · A", categoryPath(byID, byID[a]))
}
