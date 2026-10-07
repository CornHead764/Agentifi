package importer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// Two levels, "Food" over "Café", both ids invented for the test.
func TestCategoryPathFoldsAccents(t *testing.T) {
	foodID, cafeID := uuid.New(), uuid.New()
	nodes := map[uuid.UUID]struct {
		name   string
		parent uuid.UUID
	}{
		foodID: {"Food", uuid.Nil},
		cafeID: {"Café", foodID},
	}
	lookup := func(id uuid.UUID) (string, uuid.UUID, bool) {
		found, ok := nodes[id]
		return found.name, found.parent, ok
	}
	require.Equal(t, "food\x00cafe", categoryPath(cafeID, lookup))
}

// A corrupt parent cycle: "a" claims "b" as its parent and "b" claims "a".
func TestCategoryPathStopsOnAParentCycle(t *testing.T) {
	a, b := uuid.New(), uuid.New()
	nodes := map[uuid.UUID]struct {
		name   string
		parent uuid.UUID
	}{
		a: {"A", b},
		b: {"B", a},
	}
	lookup := func(id uuid.UUID) (string, uuid.UUID, bool) {
		found, ok := nodes[id]
		return found.name, found.parent, ok
	}
	require.Equal(t, "b\x00a", categoryPath(a, lookup))
}
