package api

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

func TestPastRowsFiledUnderUncategorizedNeverWinTheCategoryVote(t *testing.T) {
	none := store.Category{ID: uuid.New(), Name: "Uncategorized", Kind: domain.CategoryExpense}
	groceries := store.Category{ID: uuid.New(), Name: "Groceries", Kind: domain.CategoryExpense}
	names := nameLookup{
		accounts:   map[uuid.UUID]store.Account{},
		categories: map[uuid.UUID]store.Category{none.ID: none, groceries.ID: groceries},
	}
	on := func(date string) domain.Date {
		parsed, err := domain.ParseDate(date)
		require.NoError(t, err)
		return parsed
	}
	past := func(category uuid.UUID, date string) scoredTransaction {
		return scoredTransaction{score: 10, row: store.Transaction{
			ID: uuid.New(), CategoryID: category, Date: on(date), Amount: domain.MustFromString("-5.00"),
		}}
	}
	history := []scoredTransaction{
		past(none.ID, "2026-08-01"), past(none.ID, "2026-08-08"), past(none.ID, "2026-08-15"),
		past(groceries.ID, "2026-08-22"),
	}
	subject := store.Transaction{ID: uuid.New(), Date: on("2026-09-01"), Amount: domain.MustFromString("-5.00")}

	got := automationHost{}.assess(subject, history, historyFromPayee, 0.5, names)
	require.NotEqual(t, none.ID.String(), got.Verdict.CategoryID)
	for _, share := range got.Verdict.Shares {
		require.NotEqual(t, none.ID.String(), share.CategoryID)
	}
}
