package importer

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// seededSpace is what signing up leaves behind: a space with one owner and
// the default category tree.
func seededSpace(t *testing.T) (store.SpaceID, store.User) {
	t.Helper()
	ctx := t.Context()
	user := store.User{Email: "seeded-" + uuid.NewString()[:8] + "@example.test", IsActive: true}
	require.NoError(t, db(t).CreateUser(ctx, &user))
	space := &store.Space{Name: "Personal", PrimaryCurrency: "USD"}
	require.NoError(t, db(t).CreateSeededSpace(ctx, space))
	now := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(ctx, space.ID, &store.Membership{
		SpaceID: space.ID, UserID: user.ID, Role: store.RoleOwner, InvitedAt: &now, AcceptedAt: &now,
	}))
	return space.ID, user
}

func categoryPaths(t *testing.T, space store.SpaceID) []string {
	t.Helper()
	rows, err := db(t).ListCategories(t.Context(), space, true)
	require.NoError(t, err)
	byID := map[uuid.UUID]store.Category{}
	for _, row := range rows {
		byID[row.ID] = row
	}
	var out []string
	for _, row := range rows {
		path := row.Name
		for parent := row.ParentID; parent != uuid.Nil; parent = byID[parent].ParentID {
			path = byID[parent].Name + ":" + path
		}
		out = append(out, path)
	}
	return out
}

func TestAnImportIntoASeededSpaceLeavesOneRowPerCategory(t *testing.T) {
	space, user := seededSpace(t)
	require.NotEmpty(t, categoryPaths(t, space))

	out := mappedInto(t, space, user.Email)
	require.NoError(t, Refusal(t.Context(), db(t), out))
	require.NoError(t, Write(t.Context(), db(t), out))

	// The export's categories stand in for the defaults: Food & Dining and
	// Groceries, which both hold, appear once, under the export's ids and
	// markers, and no default the export lacks is left beside them except
	// Credit Card Payment, which pairing files under. The export's Transfer is
	// adopted as the app's, so it takes the app's marker.
	paths := categoryPaths(t, space)
	require.Len(t, paths, len(out.Categories)+1)
	require.ElementsMatch(t, []string{"Food & Dining", "Food & Dining:Groceries", "Transfer",
		"Transfer:Credit Card Payment", "Paycheck"}, paths)

	for _, category := range out.Categories {
		row, err := db(t).GetCategory(t.Context(), space, category.ID)
		require.NoError(t, err)
		require.Equal(t, category.Name, row.Name)
		if category.Name == "Transfer" {
			require.Equal(t, domain.KnownCategoryTransfer, row.KnownCategoryID)
			continue
		}
		require.Equal(t, category.KnownCategoryID, row.KnownCategoryID)
	}
	require.Equal(t, 0, scanOne[int](t, t.Context(),
		`SELECT count(*) FROM transactions WHERE space_id = $1 AND category_id IS NOT NULL
		   AND category_id NOT IN (SELECT id FROM categories WHERE space_id = $1)`, uuid.UUID(space)))
}

func TestAnImportRefusesDefaultsSomebodyChanged(t *testing.T) {
	cases := map[string]func(t *testing.T, space store.SpaceID){
		"renamed": func(t *testing.T, space store.SpaceID) {
			_, err := db(t).Pool().Exec(t.Context(),
				`UPDATE categories SET name = 'Takeout' WHERE space_id = $1 AND name = 'Fast Food'`, uuid.UUID(space))
			require.NoError(t, err)
		},
		"deleted": func(t *testing.T, space store.SpaceID) {
			_, err := db(t).Pool().Exec(t.Context(),
				`UPDATE categories SET is_deleted = true WHERE space_id = $1 AND name = 'Tolls'`, uuid.UUID(space))
			require.NoError(t, err)
		},
		"one of their own added": func(t *testing.T, space store.SpaceID) {
			require.NoError(t, db(t).CreateCategory(t.Context(), space, &store.Category{
				Name: "Boat", Kind: domain.CategoryExpense, IsUserAssignable: true, IsEditable: true,
			}))
		},
		"excluded from reports": func(t *testing.T, space store.SpaceID) {
			_, err := db(t).Pool().Exec(t.Context(),
				`UPDATE categories SET excluded_from_reports = true WHERE space_id = $1 AND name = 'Gifts'`, uuid.UUID(space))
			require.NoError(t, err)
		},
		"named by a saved filter": func(t *testing.T, space store.SpaceID) {
			gifts := scanOne[uuid.UUID](t, t.Context(),
				`SELECT id FROM categories WHERE space_id = $1 AND name = 'Gifts'`, uuid.UUID(space))
			filter := uuid.New()
			_, err := db(t).Pool().Exec(t.Context(),
				`INSERT INTO filters (id, space_id, scope) VALUES ($1, $2, 'report')`, filter, uuid.UUID(space))
			require.NoError(t, err)
			_, err = db(t).Pool().Exec(t.Context(), `
				INSERT INTO filter_items (id, space_id, filter_id, field, operator, group_index, position, value_ids)
				VALUES ($1, $2, $3, 'category', 'in', 0, 0, $4)`, uuid.New(), uuid.UUID(space), filter, []uuid.UUID{gifts})
			require.NoError(t, err)
		},
	}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			space, user := seededSpace(t)
			change(t, space)
			before := categoryPaths(t, space)

			out := mappedInto(t, space, user.Email)
			err := Write(t.Context(), db(t), out)
			require.ErrorIs(t, err, ErrDuplicate)
			require.True(t, strings.Contains(err.Error(), "categories"), err.Error())
			require.ElementsMatch(t, before, categoryPaths(t, space))
		})
	}
}
