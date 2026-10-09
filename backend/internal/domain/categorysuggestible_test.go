package domain_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func TestUncategorizedIsNeverASuggestibleCategory(t *testing.T) {
	for _, name := range []string{"Uncategorized", "uncategorized", "  UNCATEGORIZED "} {
		require.False(t, domain.Category{Name: name}.CanBeSuggested(), name)
	}
	for _, name := range []string{"Groceries", "Uncategorized Gifts", "", "Categorized"} {
		require.True(t, domain.Category{Name: name}.CanBeSuggested(), name)
	}
}
