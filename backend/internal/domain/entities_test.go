package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// Saving splits nulls the parent category, so `CategoryID == ""` is true of
// every saved split row.

func TestARowWithNoCategoryAndNoSplitsIsUncategorized(t *testing.T) {
	require.True(t, Transaction{}.IsUncategorized())
	require.False(t, Transaction{CategoryID: "cat-food"}.IsUncategorized())
}

func TestAFullyCategorizedSplitRowIsNotUncategorized(t *testing.T) {
	// The shape the Amazon connector writes: a category on every split and
	// none on the parent.
	txn := Transaction{Splits: []Split{
		{ID: "s-1", CategoryID: "cat-pets"},
		{ID: "s-2", CategoryID: "cat-office"},
	}}

	require.True(t, txn.CategoryID == "", "the parent is nulled when splits are saved")
	require.False(t, txn.IsUncategorized())
}

func TestASplitRowWithOnePartUnfiledStillNeedsWork(t *testing.T) {
	txn := Transaction{Splits: []Split{
		{ID: "s-1", CategoryID: "cat-pets"},
		{ID: "s-2"},
	}}

	require.True(t, txn.IsUncategorized())
}

func TestASplitRowIsFiledUnderItsSplitsCategoriesNotItsParents(t *testing.T) {
	require.Nil(t, Transaction{}.CategoryIDs())
	require.Equal(t, []ID{"cat-food"}, Transaction{CategoryID: "cat-food"}.CategoryIDs())

	txn := Transaction{Splits: []Split{
		{ID: "s-1", CategoryID: "cat-pets"},
		{ID: "s-2"},
		{ID: "s-3", CategoryID: "cat-office"},
	}}
	require.Equal(t, []ID{"cat-pets", "cat-office"}, txn.CategoryIDs())
}
