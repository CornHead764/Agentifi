package agent

import (
	"bytes"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestATraceIsLoggedAndNeverListedForTheHousehold(t *testing.T) {
	var logged bytes.Buffer
	notes := &Notes{Log: slog.New(slog.NewTextHandler(&logged, nil))}

	notes.Add("no tenders found on any purchase")
	notes.Tracef("the token refresh answered with %s", "an id token")

	require.Equal(t, []string{"no tenders found on any purchase"}, notes.List())
	require.Equal(t, 1, notes.Len())
	require.Equal(t, []string{"the token refresh answered with an id token"}, notes.Traces())
	require.Contains(t, logged.String(), "the token refresh answered with an id token")
	require.NotContains(t, logged.String(), "no tenders")
}

func TestANilNotesTakesNoTrace(t *testing.T) {
	var notes *Notes
	notes.Tracef("anything")
	require.Nil(t, notes.Traces())
}
