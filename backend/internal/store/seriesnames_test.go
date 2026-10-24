package store

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/migrations"
)

// runNameUnnamedSeries exercises the historical name backfill against SQLite.
func runNameUnnamedSeries(t *testing.T, space SpaceID) {
	t.Helper()
	body, err := migrations.FS.ReadFile("00010_name_unnamed_series.sql")
	require.NoError(t, err)
	_, after, _ := strings.Cut(string(body), "-- +goose Up")
	up, _, _ := strings.Cut(after, "-- +goose Down")
	_, err = householdDB(t, space).Write().ExecContext(t.Context(), strings.ReplaceAll(up, "btrim(", "trim("))
	require.NoError(t, err)
}

func seriesWithoutName(t *testing.T, space SpaceID, account uuid.UUID, description, displayName, templatePayee string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := householdDB(t, space).Write().ExecContext(t.Context(),
		`INSERT INTO series (id, account_id, description, display_name, template_payee, amount,
		                     currency, alias, frequency, interval, start_on, kind, reminder_days, match_criteria, created_at, updated_at)
		 VALUES (?, ?, ?, NULLIF(?, ''), NULLIF(?, ''), -8000, 'USD', 'monthly', 'monthly', 1,
		         '2026-01-14', 'bill', 3, 'auto', ?, ?)`,
		id, account, description, displayName, templatePayee, time.Now().UnixMilli(), time.Now().UnixMilli())
	require.NoError(t, err)
	return id
}

func seriesDisplayName(t *testing.T, space SpaceID, id uuid.UUID) string {
	t.Helper()
	var name *string
	require.NoError(t, householdDB(t, space).Read().QueryRowContext(t.Context(),
		`SELECT display_name FROM series WHERE id = ?`, id).Scan(&name))
	if name == nil {
		return ""
	}
	return *name
}

func TestAnUnnamedSeriesIsNamedForItsMatchingTextAndANameIsKept(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	unnamed := seriesWithoutName(t, space, account.ID, "  Example Power  ", "", "")
	named := seriesWithoutName(t, space, account.ID, "EXAMPLE STREAM 800-555-0100", "Family Video", "")

	runNameUnnamedSeries(t, space)
	require.Equal(t, "Example Power", seriesDisplayName(t, space, unnamed), "named for its own text, trimmed")
	require.Equal(t, "Family Video", seriesDisplayName(t, space, named), "a name a person set is never replaced")

	runNameUnnamedSeries(t, space)
	require.Equal(t, "Example Power", seriesDisplayName(t, space, unnamed), "a second run changes nothing")
}
