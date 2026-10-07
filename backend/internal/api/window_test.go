package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The register and the account summary must resolve their window through the
// *same* function.
//
// Agreeing today is not enough: this proves they share the code path, so a
// later change to one cannot leave the other behind.

func TestTheRegisterAndTheSummaryShareOneWindowResolver(t *testing.T) {
	l := buildLedger(t)

	var calls []Window
	original := resolveWindow
	resolveWindow = func(from domain.Date, hasFrom bool, to domain.Date, hasTo bool, mode domain.DateMode) (Window, error) {
		window, err := original(from, hasFrom, to, hasTo, mode)
		calls = append(calls, window)
		return window, err
	}
	t.Cleanup(func() { resolveWindow = original })

	l.alex.get("/transactions?" + august).requireStatus(http.StatusOK)
	require.Len(t, calls, 1, "the register did not go through the shared resolver")

	l.alex.get("/accounts/" + l.str("checking") + "/summary?" + august).
		requireStatus(http.StatusOK)
	require.Len(t, calls, 2, "the account summary did not go through the shared resolver")

	require.Equal(t, calls[0], calls[1], "the two endpoints resolved different windows")
}

func TestTheOpeningBalanceIsTakenFromTheDayBeforeTheWindow(t *testing.T) {
	// Using the window's own start would fold its first day into the opening
	// figure and then list that day again.
	window, err := ResolveWindow(
		domain.NewDate(2026, 8, 1), true, domain.NewDate(2026, 8, 31), true, domain.DatePosted)
	require.NoError(t, err)

	before, bounded := window.DayBeforeStart()
	require.True(t, bounded)
	require.Equal(t, domain.NewDate(2026, 7, 31), before)
}

func TestAnInvertedWindowIsRefusedByTheResolverItself(t *testing.T) {
	_, err := ResolveWindow(
		domain.NewDate(2026, 8, 31), true, domain.NewDate(2026, 8, 1), true, domain.DatePosted)
	require.Error(t, err)
}
