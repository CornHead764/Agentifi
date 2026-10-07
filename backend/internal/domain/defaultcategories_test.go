package domain_test

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestTheDefaultTreeHasOnePathPerCategory(t *testing.T) {
	seen := map[string]bool{}
	for _, group := range domain.DefaultCategories {
		path := strings.ToLower(group.Name)
		require.False(t, seen[path], "%s is listed twice", group.Name)
		seen[path] = true
		for _, child := range group.Children {
			require.Empty(t, child.Children, "%s:%s: the tree is two levels deep", group.Name, child.Name)
			childPath := path + ":" + strings.ToLower(child.Name)
			require.False(t, seen[childPath], "%s:%s is listed twice", group.Name, child.Name)
			seen[childPath] = true
		}
	}
}

func TestTheDefaultTreeLeavesTheEngineCategoriesOut(t *testing.T) {
	// Opening Balance and Balance Adjustment are marked by source, and a
	// category a person could pick with those names would read as one of them.
	for _, group := range domain.DefaultCategories {
		for _, reserved := range []string{"opening balance", "balance adjustment", "uncategorized"} {
			require.NotEqual(t, reserved, strings.ToLower(group.Name))
		}
	}
}

func TestTheDefaultTreeHoldsTheTransferCategories(t *testing.T) {
	var transfers []domain.DefaultCategory
	for _, group := range domain.DefaultCategories {
		if group.Kind == domain.CategoryTransfer {
			transfers = append(transfers, group)
		}
	}
	require.Len(t, transfers, 1, "one transfer group")
	group := transfers[0]
	require.Equal(t, "Transfer", group.Name)
	require.Equal(t, domain.KnownCategoryTransfer, group.KnownCategoryID)
	require.False(t, group.NotEditable, "Transfer can be renamed")
	require.Len(t, group.Children, 1)
	require.Equal(t, "Credit Card Payment", group.Children[0].Name)
	require.Equal(t, domain.KnownCategoryCreditCardPayment, group.Children[0].KnownCategoryID)

	// Its kind already keeps it out of both totals; the flags agree, so the
	// settings screen does not offer to include it.
	category := domain.Category{
		Kind:                     group.Kind,
		ExcludedFromReports:      group.ExcludedFromReports,
		ExcludedFromSpendingPlan: group.ExcludedFromSpendingPlan,
	}
	require.True(t, group.ExcludedFromReports)
	require.True(t, group.ExcludedFromSpendingPlan)
	require.False(t, category.CountsAsIncomeOrExpense())
	require.False(t, category.CountsTowardSpendingPlan())
}

func TestEveryDefaultSpendingAndIncomeCategoryCountsTowardBothHalves(t *testing.T) {
	// Each flag is asked separately, because they are independent.
	for _, group := range domain.DefaultCategories {
		if group.Kind == domain.CategoryTransfer {
			continue
		}
		require.Contains(t, []domain.CategoryKind{domain.CategoryIncome, domain.CategoryExpense}, group.Kind, group.Name)
		category := domain.Category{
			Name: group.Name, Kind: group.Kind,
			ExcludedFromReports:      group.ExcludedFromReports,
			ExcludedFromSpendingPlan: group.ExcludedFromSpendingPlan,
		}
		require.True(t, category.CountsAsIncomeOrExpense(), group.Name)
		require.True(t, category.CountsTowardSpendingPlan(), group.Name)
	}
}

func TestTheDefaultTreeCarriesSimplifisMarkers(t *testing.T) {
	// Simplifi's export carries these values for the categories of these
	// names; the tree mirrors them so tax and system meanings read the same.
	find := func(group, child string) domain.DefaultCategory {
		for _, one := range domain.DefaultCategories {
			if one.Name != group {
				continue
			}
			if child == "" {
				return one
			}
			for _, sub := range one.Children {
				if sub.Name == child {
					return sub
				}
			}
		}
		t.Fatalf("%s:%s is not in the tree", group, child)
		return domain.DefaultCategory{}
	}

	estimated := find("Taxes", "Federal Estimated Tax Payment")
	require.Equal(t, "7000050000", estimated.KnownCategoryID)
	require.Equal(t, "USA_521", estimated.TxfID)
	require.True(t, estimated.NotEditable)

	income := find("Personal Income", "")
	require.Equal(t, domain.CategoryIncome, income.Kind)
	require.Equal(t, "5750000000", income.KnownCategoryID)
	require.Equal(t, "USA_257", income.TxfID)

	require.Equal(t, "USA_484", find("Health", "Dentist").TxfID)
	require.Empty(t, find("Home", "Tools").KnownCategoryID)
	require.Empty(t, find("Food & Dining", "").KnownCategoryID)

	notEditable := 0
	for _, group := range domain.DefaultCategories {
		require.False(t, group.NotEditable, group.Name)
		for _, child := range group.Children {
			if child.NotEditable {
				notEditable++
			}
		}
	}
	require.Equal(t, 1, notEditable)
}
