package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/proto"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	agentifiv1 "github.com/CornHead764/agentifi/backend/internal/gen/agentifi/v1"
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
	t.Parallel()
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
	t.Parallel()
	_, err := ResolveWindow(
		domain.NewDate(2026, 8, 31), true, domain.NewDate(2026, 8, 1), true, domain.DatePosted)
	require.Error(t, err)
}

// A procedure's window reads as WindowFromRequest reads the query: an unset
// bound is open, the posted date is the default, and the same values are
// refused, under the field they were sent in.
func TestAProcedureWindowReadsAsTheQueryDoes(t *testing.T) {
	t.Parallel()
	open, err := windowOf(nil)
	require.NoError(t, err)
	require.Equal(t, Window{Mode: domain.DatePosted}, open)

	august, err := windowOf(&agentifiv1.Window{
		From: proto.String("2025-08-01"), To: proto.String("2025-08-31"), DateField: "effective",
	})
	require.NoError(t, err)
	from, _ := parseDate("2025-08-01")
	to, _ := parseDate("2025-08-31")
	require.Equal(t, Window{From: from, HasFrom: true, To: to, HasTo: true, Mode: domain.DateEffective}, august)
	require.True(t, proto.Equal(&agentifiv1.Window{
		From: proto.String("2025-08-01"), To: proto.String("2025-08-31"), DateField: "effective",
	}, windowProto(august)))

	for _, c := range []struct {
		window *agentifiv1.Window
		loc    []string
		kind   string
	}{
		{&agentifiv1.Window{From: proto.String("August")}, []string{"window", "from"}, "date_parsing"},
		{&agentifiv1.Window{To: proto.String("2025-02-30")}, []string{"window", "to"}, "date_parsing"},
		{&agentifiv1.Window{DateField: "booked"}, []string{"window", "date_field"}, "enum"},
		{&agentifiv1.Window{From: proto.String("2025-09-01"), To: proto.String("2025-08-01")}, []string{"query", "from"}, "window_inverted"},
	} {
		_, err := windowOf(c.window)
		p := classify(t.Context(), err)
		require.Equal(t, http.StatusUnprocessableEntity, p.status, c.window.String())
		require.Equal(t, c.loc, p.fields[0].Loc)
		require.Equal(t, c.kind, p.fields[0].Type)
	}
}
