package importer

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// emptySpace is what signing up leaves behind: a space with one owner and
// nothing in it. The email is unique per call so the duplicate-name guard
// only ever sees this test's own spaces.
func emptySpace(t *testing.T) (store.SpaceID, store.User) {
	t.Helper()
	ctx := t.Context()
	user := store.User{Email: "into-" + uuid.NewString()[:8] + "@example.test", IsActive: true}
	require.NoError(t, db(t).CreateUser(ctx, &user))
	return storetest.NewOwnedSpace(t, "Personal", user.ID), user
}

func mappedInto(t *testing.T, space store.SpaceID, email string) *Mapped {
	t.Helper()
	out, err := MapExport(complete(t), "", Options{OwnerEmail: email, IntoSpace: space})
	require.NoError(t, err)
	require.True(t, out.Report.OK(), out.Report.Render())
	return out
}

func TestAnImportFillsAnEmptySpaceInPlace(t *testing.T) {
	space, user := emptySpace(t)
	out := mappedInto(t, space, user.Email)
	require.Equal(t, space, out.SpaceID)
	require.NoError(t, Write(t.Context(), db(t), out))
	ctx := t.Context()

	id := uuid.UUID(space)
	require.Equal(t, out.Space.Name, scanOne[string](t, ctx, `SELECT name FROM spaces WHERE id = $1`, id))
	require.Equal(t, len(out.Transactions), scanOne[int](t, ctx,
		`SELECT count(*) FROM transactions WHERE space_id = $1`, id))
	require.Equal(t, len(out.Accounts), scanOne[int](t, ctx,
		`SELECT count(*) FROM accounts WHERE space_id = $1`, id))
	// The person who uploaded stays the only member, under their own account.
	require.Equal(t, 1, scanOne[int](t, ctx, `SELECT count(*) FROM memberships WHERE space_id = $1`, id))
	require.Equal(t, user.ID, scanOne[uuid.UUID](t, ctx,
		`SELECT user_id FROM memberships WHERE space_id = $1`, id))
}

func TestAnImportReplacesTheAlertSettingsASpaceStartsWith(t *testing.T) {
	space, user := emptySpace(t)
	out := mappedInto(t, space, user.Email)
	require.NotEmpty(t, out.AlertRules)
	ctx := t.Context()
	for _, rule := range out.AlertRules {
		require.NoError(t, db(t).SaveAlertRule(ctx, space, user.ID, &store.AlertRule{
			UserID: user.ID, AlertType: domain.AlertType(rule.AlertType), IsEnabled: true,
		}))
	}
	require.NoError(t, Write(ctx, db(t), out))
	require.Equal(t, len(out.AlertRules), scanOne[int](t, ctx,
		`SELECT count(*) FROM alert_rules WHERE space_id = $1`, uuid.UUID(space)))
}

func TestAnImportRefusesASpaceThatAlreadyHoldsData(t *testing.T) {
	space, user := emptySpace(t)
	ctx := t.Context()
	_, err := db(t).Pool().Exec(ctx,
		`INSERT INTO tags (id, space_id, name) VALUES ($1, $2, 'kept')`, uuid.New(), uuid.UUID(space))
	require.NoError(t, err)

	out := mappedInto(t, space, user.Email)
	require.ErrorIs(t, Refusal(ctx, db(t), out), ErrDuplicate)
	err = Write(ctx, db(t), out)
	require.ErrorIs(t, err, ErrDuplicate)
	require.ErrorContains(t, err, "tag")
	require.Equal(t, "Personal", scanOne[string](t, ctx,
		`SELECT name FROM spaces WHERE id = $1`, uuid.UUID(space)))
}

func TestAnImportIntoASpaceStillRefusesASecondCopyOfTheDataset(t *testing.T) {
	space, user := emptySpace(t)
	require.NoError(t, Write(t.Context(), db(t), mappedInto(t, space, user.Email)))

	other := storetest.NewOwnedSpace(t, "Personal", user.ID)
	err := Write(t.Context(), db(t), mappedInto(t, other, user.Email))
	require.ErrorIs(t, err, ErrDuplicate)
	require.ErrorContains(t, err, "already exists")
}
