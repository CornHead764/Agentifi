package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/migrations"
)

// runNameUnnamedSeries applies the Up half of the migration to every space; the
// test spaces are the only rows its predicates can match that matter here.
func runNameUnnamedSeries(t *testing.T) {
	t.Helper()
	body, err := migrations.FS.ReadFile("00010_name_unnamed_series.sql")
	require.NoError(t, err)
	_, after, _ := strings.Cut(string(body), "-- +goose Up")
	up, _, _ := strings.Cut(after, "-- +goose Down")
	_, err = db(t).Pool().Exec(t.Context(), up)
	require.NoError(t, err)
}

func seriesWithoutName(t *testing.T, space SpaceID, account uuid.UUID, description, displayName, templatePayee string) uuid.UUID {
	t.Helper()
	id := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(),
		`INSERT INTO series (id, space_id, account_id, description, display_name, template_payee, amount,
		                     currency, alias, frequency, interval, start_on, kind, reminder_days, match_criteria)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, ''), -80.00, 'USD', 'monthly', 'monthly', 1,
		         '2026-01-14', 'bill', 3, 'auto')`,
		id, space.UUID(), account, description, displayName, templatePayee)
	require.NoError(t, err)
	return id
}

func seriesDisplayName(t *testing.T, id uuid.UUID) string {
	t.Helper()
	var name *string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT display_name FROM series WHERE id = $1`, id).Scan(&name))
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

	runNameUnnamedSeries(t)
	require.Equal(t, "Example Power", seriesDisplayName(t, unnamed), "named for its own text, trimmed")
	require.Equal(t, "Family Video", seriesDisplayName(t, named), "a name a person set is never replaced")

	runNameUnnamedSeries(t)
	require.Equal(t, "Example Power", seriesDisplayName(t, unnamed), "a second run changes nothing")
}
