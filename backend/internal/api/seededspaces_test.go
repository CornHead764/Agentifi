package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Every space a person opens starts with the default category tree.

func requireDefaultTree(t *testing.T, c *client, spaceID string) {
	t.Helper()
	id, err := uuid.Parse(spaceID)
	require.NoError(t, err)
	in := c.inSpace(store.SpaceIDOf(id))
	rows := in.get("/categories").requireStatus(http.StatusOK).list()
	require.Len(t, rows, defaultTreeSize())

	anyRows := make([]any, 0, len(rows))
	for _, row := range rows {
		anyRows = append(anyRows, map[string]any(row))
	}
	byPath := categoriesByPath(anyRows)
	require.Equal(t, false, byPath["Taxes:Federal Estimated Tax Payment"]["is_editable"])
	require.Equal(t, "income", byPath["Personal Income:Paycheck"]["kind"])

	// Adopting the tree again finds all of it.
	again := in.post("/categories/defaults", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, 0, int(again["created"].(float64)))
	require.Equal(t, defaultTreeSize(), int(again["existing"].(float64)))
}

func TestASpaceSomebodyOpensStartsWithTheDefaultCategories(t *testing.T) {
	c := newClient(t)
	c.as(makeUser(t, testPassword))
	created := c.post("/spaces", map[string]any{"name": "Cabin"}).
		requireStatus(http.StatusCreated).json()
	requireDefaultTree(t, c, created["id"].(string))
}

func TestTheSpaceAnAdministratorMakesStartsWithTheDefaultCategories(t *testing.T) {
	c := newClient(t).as(makeAdmin(t))
	body := c.post("/admin/users", map[string]any{
		"email": "seeded-" + uuid.NewString() + "@example.test", "space_name": "Their House",
	}).requireStatus(http.StatusCreated).json()
	membership := body["user"].(map[string]any)["memberships"].([]any)[0].(map[string]any)

	// The new account must change its password before it reads anything, so
	// the rows are read from the store.
	space, err := store.ParseSpaceID(membership["space_id"].(string))
	require.NoError(t, err)
	rows, err := c.env.DB.ListCategories(t.Context(), space, true)
	require.NoError(t, err)
	require.Len(t, rows, defaultTreeSize())
	fixed := 0
	for _, row := range rows {
		if !row.IsEditable {
			fixed++
			require.Equal(t, "Federal Estimated Tax Payment", row.Name)
		}
	}
	require.Equal(t, 1, fixed)
}

func TestTheSpaceASignInCreatesStartsWithTheDefaultCategories(t *testing.T) {
	c := newClient(t)
	user := makeUser(t, testPassword)
	require.NoError(t, createPersonalSpace(t.Context(), c.env, user))
	spaces := c.as(user).get("/spaces").requireStatus(http.StatusOK).list()
	require.Len(t, spaces, 1)
	requireDefaultTree(t, c, spaces[0]["id"].(string))
}

func TestJoiningASpaceSeedsNothing(t *testing.T) {
	// Only a new space is seeded: an account added to one that exists finds
	// the categories the space already has.
	admin := makeAdmin(t)
	owner := makeUser(t, testPassword)
	space := makeSpace(t, owner, "Shared", store.RoleOwner, true)
	newClient(t).as(admin).post("/admin/users", map[string]any{
		"email":    "joiner-" + uuid.NewString() + "@example.test",
		"password": "a long enough password",
		"space_id": space.ID.String(),
		"role":     "viewer",
	}).requireStatus(http.StatusCreated)
	rows := newClient(t).as(owner).inSpace(space.ID).get("/categories").requireStatus(http.StatusOK).list()
	require.Empty(t, rows)
}
