package domain

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// The drift fixtures: a connected checking account the bank puts at 960.00,
// whose rows are a 1,000.00 deposit from July and a -40.00 charge from July
// 20th. Every sync lands at 06:00 on an August day; the check runs on August
// 30th, so syncs up to the 23rd are settled.

var driftNow = time.Date(2026, time.August, 30, 12, 0, 0, 0, time.UTC)

func syncedOn(day int) time.Time { return time.Date(2026, time.August, day, 6, 0, 0, 0, time.UTC) }

// sawPosted is a sync over posted rows only: both readings see the same.
func sawPosted(day int, reported, total, movement string) BalanceObservation {
	return BalanceObservation{
		At: syncedOn(day), Reported: MustFromString(reported),
		Ledger: MustFromString(total), Settled: MustFromString(total),
		Movement: MustFromString(movement), SettledMovement: MustFromString(movement),
	}
}

func driftAccount() Account {
	acct := balanceAccount()
	acct.ProviderBalance, acct.HasProviderBalance = MustFromString("960.00"), true
	return acct
}

func driftRow(id ID, amount string, added time.Time) Transaction {
	row := balanceTxn(id, 1, amount)
	row.AddedAt = added
	return row
}

func driftRows(acct Account, extra ...Transaction) []Posting {
	return balancePostings(acct, append([]Transaction{
		driftRow("deposit", "1000.00", time.Date(2026, time.July, 1, 6, 0, 0, 0, time.UTC)),
		driftRow("charge", "-40.00", time.Date(2026, time.July, 20, 6, 0, 0, 0, time.UTC)),
	}, extra...)...)
}

// The same -40.00 charge arrives again just before the sync on the 2nd.
var resent = driftRow("resent", "-40.00", syncedOn(2).Add(-time.Hour))

func TestLedgerDrift(t *testing.T) {
	acct := driftAccount()
	// The resent charge moves the ledger -40 while the bank stays at 960:
	// a drift of -40 from the 2nd, standing to the 6th and on.
	doubled := []BalanceObservation{
		sawPosted(1, "960.00", "960.00", "0"),
		sawPosted(2, "960.00", "920.00", "-40.00"),
		sawPosted(4, "960.00", "920.00", "0"),
		sawPosted(6, "960.00", "920.00", "0"),
		sawPosted(25, "960.00", "920.00", "0"),
	}
	deleted := resent
	deleted.IsDeleted = true

	cases := []struct {
		name         string
		observations []BalanceObservation
		rows         []Posting
		want         *Drift
	}{
		{
			name:         "a duplicate stands",
			observations: doubled,
			rows:         driftRows(acct, resent),
			want: &Drift{Amount: MustFromString("-40.00"), Since: syncedOn(2),
				Suspect: SuspectDuplicate, SuspectID: "resent"},
		},
		{
			// The ledger is 960 again: -40 moved back by the deletion.
			name:         "deleting the duplicate clears it",
			observations: doubled,
			rows:         driftRows(acct, deleted),
		},
		{
			// The bank takes the -40 a day after the row arrives: -40 then +40.
			name: "a lag that catches up is noise",
			observations: []BalanceObservation{
				sawPosted(1, "960.00", "960.00", "0"),
				sawPosted(2, "960.00", "920.00", "-40.00"),
				sawPosted(3, "920.00", "920.00", "0"),
				sawPosted(6, "920.00", "920.00", "0"),
				sawPosted(25, "920.00", "920.00", "0"),
			},
			rows: driftRows(acct, resent),
		},
		{
			// Settled syncs stop at the 23rd: the 20th and 22nd span two days.
			name: "a drift younger than its settling stands nothing",
			observations: []BalanceObservation{
				sawPosted(1, "960.00", "960.00", "0"),
				sawPosted(20, "960.00", "920.00", "-40.00"),
				sawPosted(22, "960.00", "920.00", "0"),
				sawPosted(25, "960.00", "920.00", "0"),
			},
			rows: driftRows(acct, resent),
		},
		{
			name: "one settled sync is not enough",
			observations: []BalanceObservation{
				sawPosted(1, "960.00", "960.00", "0"),
				sawPosted(2, "960.00", "920.00", "-40.00"),
				sawPosted(25, "960.00", "920.00", "0"),
			},
			rows: driftRows(acct, resent),
		},
		{
			// The bank takes two -25.00 charges; one arrives. The ledger moves
			// -25 while the bank moves -50: a drift of +25, the charge negated.
			name: "a missing row names its twin",
			observations: []BalanceObservation{
				sawPosted(1, "960.00", "960.00", "0"),
				sawPosted(2, "910.00", "935.00", "-25.00"),
				sawPosted(4, "910.00", "935.00", "0"),
				sawPosted(6, "910.00", "935.00", "0"),
				sawPosted(25, "910.00", "935.00", "0"),
			},
			rows: driftRows(acct, driftRow("cafe", "-25.00", syncedOn(2).Add(-time.Hour))),
			want: &Drift{Amount: MustFromString("25.00"), Since: syncedOn(2),
				Suspect: SuspectMissing, SuspectID: "cafe"},
		},
		{
			// Two rows of the drift's amount arrived together: neither is named.
			name:         "two candidates name none",
			observations: doubled,
			rows: driftRows(acct, resent,
				driftRow("other", "-40.00", syncedOn(2).Add(-2*time.Hour))),
			want: &Drift{Amount: MustFromString("-40.00"), Since: syncedOn(2)},
		},
		{
			// The ledger was 100 over the bank before the first sync, and a
			// stray row is deleted before the second: the drift never moved.
			name: "tidying a ledger that agreed raises nothing",
			observations: []BalanceObservation{
				sawPosted(1, "960.00", "1060.00", "0"),
				sawPosted(3, "960.00", "960.00", "0"),
				sawPosted(6, "960.00", "960.00", "0"),
				sawPosted(25, "960.00", "960.00", "0"),
			},
			rows: driftRows(acct),
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := LedgerDrift(acct, tc.observations, tc.rows, driftNow)
			if tc.want == nil {
				require.False(t, ok, "drift %s", got.Amount)
				return
			}
			require.True(t, ok)
			require.Equal(t, tc.want.Amount.String(), got.Amount.String())
			require.Equal(t, tc.want.Since, got.Since)
			require.Equal(t, tc.want.Suspect, got.Suspect)
			require.Equal(t, tc.want.SuspectID, got.SuspectID)
		})
	}
}

func TestLedgerDriftReadsTheAccountsPendingReading(t *testing.T) {
	// A pending -30.00 arrives on the 2nd and stays pending; the bank stays at
	// 960. Leaving pending rows out, nothing is unexplained. Counting them,
	// the rows moved -30 past the bank, and the pending row is that amount.
	hold := driftRow("hold", "-30.00", syncedOn(2).Add(-time.Hour))
	hold.IsPending = true
	seen := func(day int, movement string) BalanceObservation {
		return BalanceObservation{
			At: syncedOn(day), Reported: MustFromString("960.00"),
			Ledger: MustFromString("930.00"), Settled: MustFromString("960.00"),
			Movement: MustFromString(movement), SettledMovement: Zero,
		}
	}
	observations := []BalanceObservation{
		sawPosted(1, "960.00", "960.00", "0"),
		seen(2, "-30.00"), seen(4, "0"), seen(6, "0"), seen(25, "0"),
	}

	acct := driftAccount()
	_, ok := LedgerDrift(acct, observations, driftRows(acct, hold), driftNow)
	require.False(t, ok)

	acct.BankIncludesPending = true
	got, ok := LedgerDrift(acct, observations, driftRows(acct, hold), driftNow)
	require.True(t, ok)
	require.Equal(t, "-30.00", got.Amount.String())
	require.Equal(t, ID("hold"), got.SuspectID)
}

func TestAManualAccountHasNoLedgerDrift(t *testing.T) {
	acct := balanceAccount()
	_, ok := LedgerDrift(acct, []BalanceObservation{
		sawPosted(1, "960.00", "960.00", "0"),
		sawPosted(2, "960.00", "920.00", "-40.00"),
		sawPosted(6, "960.00", "920.00", "0"),
	}, driftRows(acct, resent), driftNow)
	require.False(t, ok)
}
