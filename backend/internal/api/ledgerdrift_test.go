package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The seeded card is at -300.00 by the bank, with +200.00 and -75.00 posted.
// Its -75.00 charge arrives a second time while the bank's figure stays put,
// so its rows come to 75.00 less than the bank's balance (calculations.md §3,
// "Ledger drift").
func TestAnAccountWhoseRowsStopAddingUpReportsItsDrift(t *testing.T) {
	t.Parallel()
	l := buildLedger(t)
	ctx := t.Context()
	space := store.SpaceIDOf(l.id("space"))
	resent := &store.Transaction{
		AccountID: l.id("card"), Date: domain.NewDate(2026, time.August, 25),
		Amount: domain.MustFromString("-75.00"), Currency: "USD",
		StatementName: "SQ *COFFEE XXXXXX1234 USA", Payee: "Harbor Coffee",
		Source: domain.SourceSync,
	}
	require.NoError(t, l.env.DB.CreateTransaction(ctx, space, resent))
	stored, err := l.env.DB.GetTransaction(ctx, space, resent.ID)
	require.NoError(t, err)

	// The first copy arrived a month before the second.
	_, err = l.sql().ExecContext(ctx, `UPDATE transactions SET created_at = ? WHERE id = ?`,
		stored.CreatedAt.AddDate(0, -1, 0).UnixMilli(), l.id("card_charge"))
	require.NoError(t, err)

	arrived := stored.CreatedAt.Add(time.Hour)
	observe := func(at time.Time, settled, movement string) {
		require.NoError(t, l.env.DB.RecordBalanceObservation(ctx, space, l.id("card"), domain.BalanceObservation{
			At: at, Reported: domain.MustFromString("-300.00"),
			Ledger: domain.MustFromString(settled), Settled: domain.MustFromString(settled),
			Movement: domain.MustFromString(movement), SettledMovement: domain.MustFromString(movement),
		}))
	}
	observe(stored.CreatedAt.Add(-time.Hour), "125.00", "0")
	observe(arrived, "50.00", "-75.00")
	observe(arrived.AddDate(0, 0, 2), "50.00", "0")
	observe(arrived.AddDate(0, 0, 4), "50.00", "0")
	observe(arrived.AddDate(0, 0, 23), "50.00", "0")
	now := arrived.AddDate(0, 0, 28)
	env := NewEnv(testConfig(), db(t), WithClock(func() time.Time { return now }))
	alex := (&client{t: t, env: env, handler: RouterFor(env)}).as(l.users["alex"]).inSpace(space)

	want := map[string]any{
		"amount": "-75.00",
		"since":  arrived.UTC().Format(time.RFC3339Nano),
		"suspect": map[string]any{
			"kind": "duplicate", "transaction_id": resent.ID.String(),
		},
	}
	drifts := map[string]any{}
	for _, row := range alex.get("/accounts").requireStatus(http.StatusOK).list() {
		drifts[row["id"].(string)] = row["ledger_drift"]
	}
	require.Equal(t, want, drifts[l.str("card")])
	require.Nil(t, drifts[l.str("checking")], "a manual account's rows are its balance")

	tools := assistantTools{env: env, sp: auth.SpaceContext{Space: store.Space{ID: space}}}
	answer, err := tools.Run(ctx, "list_accounts", map[string]any{})
	require.NoError(t, err)
	encoded, err := json.Marshal(answer)
	require.NoError(t, err)
	var read struct {
		Accounts []map[string]any `json:"accounts"`
	}
	require.NoError(t, json.Unmarshal(encoded, &read))
	for _, account := range read.Accounts {
		if account["id"] == l.str("card") {
			require.Equal(t, want, account["ledger_drift"])
		} else {
			require.NotContains(t, account, "ledger_drift")
		}
	}
}
