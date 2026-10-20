package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/migrations"
)

// An envelope that did not auto-release carried an overspend as well as unspent
// funds, and one that did carried neither. Run inside a transaction that is
// rolled back, from the schema before the migration, using the file's own
// statements.
func TestMigrationElevenKeepsWhatEveryEnvelopeCarriedBefore(t *testing.T) {
	ctx := t.Context()
	file, err := migrations.FS.ReadFile("00011_envelope_roll_under.sql")
	require.NoError(t, err)
	up, down, ok := strings.Cut(string(file), "-- +goose Down")
	require.True(t, ok, "the migration has a Down")
	_, up, ok = strings.Cut(up, "-- +goose Up")
	require.True(t, ok, "the migration has an Up")

	space := newSpace(t)
	filter := &Filter{Name: "Groceries", Scope: "envelope"}
	require.NoError(t, db(t).CreateFilter(ctx, space, filter))

	tx, err := db(t).pool.Begin(ctx)
	require.NoError(t, err)
	defer tx.Rollback(ctx) //nolint:errcheck

	_, err = tx.Exec(ctx, down)
	require.NoError(t, err)

	month := uuid.New()
	_, err = tx.Exec(ctx,
		`INSERT INTO spending_plan_months (id, space_id, month, projection_type, projection_window_months)
		 VALUES ($1, $2, '2026-08-01', 'run_rate', 3)`, month, space.UUID())
	require.NoError(t, err)
	insert := func(autoRelease bool) uuid.UUID {
		id := uuid.New()
		_, err := tx.Exec(ctx,
			`INSERT INTO envelopes (id, space_id, spending_plan_month_id, filter_id, name, auto_release_rollover)
			 VALUES ($1, $2, $3, $4, 'Groceries', $5)`, id, space.UUID(), month, filter.ID, autoRelease)
		require.NoError(t, err)
		return id
	}
	rolling, releasing := insert(false), insert(true)

	_, err = tx.Exec(ctx, up)
	require.NoError(t, err)

	rollUnder := func(id uuid.UUID) bool {
		var value bool
		require.NoError(t, tx.QueryRow(ctx, `SELECT roll_under FROM envelopes WHERE id = $1`, id).Scan(&value))
		return value
	}
	require.True(t, rollUnder(rolling), "an envelope that carried unspent funds carried its overspend")
	require.False(t, rollUnder(releasing), "an envelope that auto-released carried neither")

	var fresh bool
	id := uuid.New()
	_, err = tx.Exec(ctx,
		`INSERT INTO envelopes (id, space_id, spending_plan_month_id, filter_id, name)
		 VALUES ($1, $2, $3, $4, 'Dining')`, id, space.UUID(), month, filter.ID)
	require.NoError(t, err)
	require.NoError(t, tx.QueryRow(ctx, `SELECT roll_under FROM envelopes WHERE id = $1`, id).Scan(&fresh))
	require.True(t, fresh, "an envelope inserted without the column rolls under, as a rolling envelope did")
}
