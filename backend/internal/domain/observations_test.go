package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// observedSync is one observation: the reported figure and the sync's own
// movement counting pending rows and counting posted rows only.
func observedSync(reported, movement, settledMovement string) BalanceObservation {
	return BalanceObservation{
		Reported:        MustFromString(reported),
		Movement:        MustFromString(movement),
		SettledMovement: MustFromString(settledMovement),
	}
}

func TestPendingReading(t *testing.T) {
	// Each sync after the first decides when exactly one reading explains the
	// change in the reported figure (§3).
	start := observedSync("1000.00", "0", "0")
	// The bank leaves pending rows out: a pending -50 arrives and the figure
	// stays, then it posts and the figure falls.
	leavingArrives := observedSync("1000.00", "-50.00", "0")
	leavingPosts := observedSync("950.00", "0", "-50.00")
	leavingArrivesAgain := observedSync("950.00", "-20.00", "0")
	// The bank counts pending rows: the figure falls when the pending -50
	// arrives, and stays when it posts.
	countingArrives := observedSync("950.00", "-50.00", "0")
	countingPosts := observedSync("950.00", "0", "-50.00")
	countingArrivesAgain := observedSync("930.00", "-20.00", "0")
	// After countingArrives: a posted -30 either reading explains, and a fall
	// of 40 with no row behind it, which neither explains.
	posted := observedSync("920.00", "-30.00", "-30.00")
	unexplained := observedSync("910.00", "0", "0")

	cases := []struct {
		name         string
		current      bool
		observations []BalanceObservation
		want         bool
	}{
		{"three syncs leave pending out", true,
			[]BalanceObservation{start, leavingArrives, leavingPosts, leavingArrivesAgain}, false},
		{"three syncs count pending", false,
			[]BalanceObservation{start, countingArrives, countingPosts, countingArrivesAgain}, true},
		{"two deciding syncs keep the current reading", false,
			[]BalanceObservation{start, countingArrives, countingPosts}, false},
		{"a sync either reading explains decides nothing", false,
			[]BalanceObservation{start, countingArrives, posted, observedSync("920.00", "0", "-50.00")}, false},
		{"a sync neither reading explains decides nothing", false,
			[]BalanceObservation{start, countingArrives, unexplained, observedSync("860.00", "-50.00", "0")}, false},
		{"disagreeing syncs keep the current reading", false,
			[]BalanceObservation{start, countingArrives, observedSync("950.00", "-20.00", "0"),
				observedSync("930.00", "-20.00", "0")}, false},
		{"only the newest three deciding syncs count", true,
			[]BalanceObservation{start, countingArrives, observedSync("950.00", "-20.00", "0"),
				observedSync("930.00", "0", "-20.00"), observedSync("930.00", "-10.00", "0")}, false},
		{"no observations keep the current reading", true, nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, PendingReading(tc.current, tc.observations))
		})
	}
}

func TestObserveBalance(t *testing.T) {
	// A pending -50 posts at -55 and a new posted -20 arrives: counting
	// pending rows the sync moved the ledger -25; counting posted rows only,
	// -75. The totals are the account's rows once they are in.
	acct := balanceAccount()
	pending := ledgerRow("p", "-50.00", true)
	before := []Transaction{pending}
	after := []Transaction{ledgerRow("p", "-55.00", false), ledgerRow("new", "-20.00", false)}
	older := balanceTxn("old", 1, "500.00")
	rows := balancePostings(acct, append([]Transaction{older}, after...)...)
	at := time.Date(2026, time.August, 10, 9, 0, 0, 0, time.UTC)

	got := ObserveBalance(at, MustFromString("425.00"), before, after, rows)
	require.Equal(t, at, got.At)
	require.Equal(t, "425.00", got.Reported.String())
	require.Equal(t, "425.00", got.Ledger.String())
	require.Equal(t, "425.00", got.Settled.String())
	require.Equal(t, "-25.00", got.Movement.String())
	require.Equal(t, "-75.00", got.SettledMovement.String())
}
