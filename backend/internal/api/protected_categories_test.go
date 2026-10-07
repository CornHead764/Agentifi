package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// transferCategories gives the ledger's space its Transfer and Credit Card
// Payment categories, which a seeded space has from the start.
func transferCategories(t *testing.T, l *ledger) (transfer, cardPayment string) {
	t.Helper()
	_, err := l.env.DB.EnsureTransferCategories(t.Context(), store.SpaceIDOf(l.id("space")))
	require.NoError(t, err)
	return categoryWithMarker(l, domain.KnownCategoryTransfer),
		categoryWithMarker(l, domain.KnownCategoryCreditCardPayment)
}

func TestACategoryAProcessDependsOnCannotBeDeleted(t *testing.T) {
	l := buildLedger(t)
	transfer, cardPayment := transferCategories(t, l)
	for _, id := range []string{transfer, cardPayment, l.str("system_category")} {
		refused := l.alex.del("/categories/" + id).requireStatus(http.StatusConflict)
		require.Contains(t, refused.Body.String(), "cannot be deleted")
	}
	refused := l.alex.del("/categories/" + transfer).requireStatus(http.StatusConflict)
	require.Contains(t, refused.Body.String(), "matched transfers are filed under it")

	listed := map[string]bool{}
	for _, category := range l.alex.get("/categories").requireStatus(http.StatusOK).list() {
		listed[category["id"].(string)] = true
	}
	require.True(t, listed[transfer])
	require.True(t, listed[cardPayment])
}

func TestAProtectedCategoryCanBeRenamedButKeepsItsKind(t *testing.T) {
	l := buildLedger(t)
	transfer, _ := transferCategories(t, l)
	renamed := l.alex.patch("/categories/"+transfer,
		map[string]any{"name": "Moving money"}).requireStatus(http.StatusOK).json()
	require.Equal(t, "Moving money", renamed["name"])
	require.Equal(t, domain.KnownCategoryTransfer, renamed["known_category_id"])

	refused := l.alex.patch("/categories/"+transfer,
		map[string]any{"kind": "expense"}).requireStatus(http.StatusConflict)
	require.Contains(t, refused.Body.String(), "cannot change")
	require.Equal(t, "transfer", l.alex.get("/categories/" + transfer).
		requireStatus(http.StatusOK).json()["kind"])
}

func TestThePurgeRefusesAProtectedCategory(t *testing.T) {
	l := buildLedger(t)
	transfer, _ := transferCategories(t, l)
	spareOne := spare(t, l, "Alimony")
	purge(l, []uuid.UUID{uuid.MustParse(transfer), spareOne.ID}, nil).
		requireStatus(http.StatusConflict)
	require.NotContains(t, unused(l).categories, "Transfer",
		"an unused Transfer is never offered for the purge")
	require.Contains(t, unused(l).categories, "Alimony",
		"a refused purge deletes nothing")
}

func TestTheCategoryListSaysWhyACategoryIsKept(t *testing.T) {
	l := buildLedger(t)
	transfer, _ := transferCategories(t, l)
	byID := map[string]map[string]any{}
	for _, category := range l.alex.get("/categories").requireStatus(http.StatusOK).list() {
		byID[category["id"].(string)] = category
	}
	require.Equal(t, "matched transfers are filed under it", byID[transfer]["protected_reason"])
	require.Equal(t, "it is maintained by the app", byID[l.str("system_category")]["protected_reason"])
	require.Contains(t, byID[l.str("groceries")], "protected_reason")
	require.Nil(t, byID[l.str("groceries")]["protected_reason"])
}
