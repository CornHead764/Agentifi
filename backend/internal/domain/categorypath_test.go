package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestCategoryPathWalksRootFirst(t *testing.T) {
	tree := map[string][2]string{
		"groceries": {"Groceries", "food"},
		"food":      {"Food", ""},
	}
	lookup := func(id string) (string, string, bool) {
		found, ok := tree[id]
		return found[0], found[1], ok
	}
	require.Equal(t, []string{"Food", "Groceries"}, CategoryPath("groceries", lookup))
}

func TestCategoryPathStopsAtAnUnknownParent(t *testing.T) {
	lookup := func(id string) (string, string, bool) { return "", "", false }
	require.Empty(t, CategoryPath("orphan", lookup))
}

func TestCategoryPathStopsOnACycleRatherThanLoopingForever(t *testing.T) {
	// A data error: "a" claims "b" as its parent and "b" claims "a" right back.
	tree := map[string][2]string{
		"a": {"A", "b"},
		"b": {"B", "a"},
	}
	lookup := func(id string) (string, string, bool) {
		found, ok := tree[id]
		return found[0], found[1], ok
	}
	require.Equal(t, []string{"B", "A"}, CategoryPath("a", lookup))
}
