package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The starter tree, adopted into a space that already has some of it.

func defaultTreeSize() int {
	size := 0
	for _, group := range domain.DefaultCategories {
		size += 1 + len(group.Children)
	}
	return size
}

func categoriesByPath(rows []any) map[string]map[string]any {
	byID := map[string]map[string]any{}
	for _, one := range rows {
		row := one.(map[string]any)
		byID[row["id"].(string)] = row
	}
	out := map[string]map[string]any{}
	for _, row := range byID {
		path := row["name"].(string)
		for parent, _ := row["parent_id"].(string); parent != ""; {
			up := byID[parent]
			path = up["name"].(string) + ":" + path
			parent, _ = up["parent_id"].(string)
		}
		out[path] = row
	}
	return out
}

func TestTheDefaultTreeFillsInAroundWhatTheSpaceAlreadyHas(t *testing.T) {
	// The household already has Food & Dining > Groceries and a system Opening
	// Balance. Those two paths are found rather than duplicated, and the rest
	// of Food & Dining is filed under the group that is already there.
	l := buildLedger(t)
	body := l.alex.post("/categories/defaults", nil).requireStatus(http.StatusCreated).json()

	require.Equal(t, 2, int(body["existing"].(float64)))
	require.Equal(t, defaultTreeSize()-2, int(body["created"].(float64)))

	byPath := categoriesByPath(body["categories"].([]any))
	require.Equal(t, l.str("food"), byPath["Food & Dining"]["id"])
	require.Equal(t, l.str("food"), byPath["Food & Dining:Restaurants"]["parent_id"])
	require.Equal(t, l.str("groceries"), byPath["Food & Dining:Groceries"]["id"])

	// Every row has a path of its own, and the system category is kept.
	require.Len(t, body["categories"].([]any), defaultTreeSize()+1)
	require.Len(t, byPath, defaultTreeSize()+1)

	income := byPath["Personal Income:Paycheck"]
	require.Equal(t, "income", income["kind"])
	require.Equal(t, false, income["excluded_from_reports"])
	require.Equal(t, false, income["excluded_from_spending_plan"])

	// Simplifi's markers ride along, and its system category stays fixed.
	estimated := byPath["Taxes:Federal Estimated Tax Payment"]
	require.Equal(t, "7000050000", estimated["known_category_id"])
	require.Equal(t, "USA_521", estimated["txf_id"])
	require.Equal(t, false, estimated["is_editable"])
	require.Equal(t, true, byPath["Taxes:Federal Tax"]["is_editable"])
	require.Nil(t, byPath["Home:Tools"]["known_category_id"])
}

func TestAdoptingTheDefaultTreeTwiceAddsNothing(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/categories/defaults", nil).requireStatus(http.StatusCreated)
	body := l.alex.post("/categories/defaults", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, 0, int(body["created"].(float64)))
	require.Equal(t, defaultTreeSize(), int(body["existing"].(float64)))
}

func TestAViewerCannotAdoptTheDefaultTree(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").post("/categories/defaults", nil).requireStatus(http.StatusForbidden)
	require.Len(t, l.alex.get("/categories").requireStatus(http.StatusOK).list(), 3)
}

func TestTheDefaultTreeStaysInTheSpaceItWasAskedFor(t *testing.T) {
	l := buildLedger(t)
	l.alex.post("/categories/defaults", nil).requireStatus(http.StatusCreated)
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))
	theirs := bob.get("/categories").requireStatus(http.StatusOK).list()
	require.Len(t, theirs, 1)
}
