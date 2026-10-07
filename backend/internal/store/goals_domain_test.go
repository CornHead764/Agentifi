package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// GoalContributions is the one place a goal link becomes a domain row; the
// direction cannot be recovered from the amount, so a caller reading txn_ids
// alone would count every withdrawal as a contribution.

func goalLinkFixture(t *testing.T) (GoalLinks, func(uuid.UUID) (Transaction, bool), []uuid.UUID) {
	t.Helper()
	in, out, spent := uuid.New(), uuid.New(), uuid.New()
	ledger := map[uuid.UUID]Transaction{
		in:    {ID: in, AccountID: uuid.New(), Date: domainDate(2026, time.August, 1), Amount: domain.MustFromString("-800.00")},
		out:   {ID: out, AccountID: uuid.New(), Date: domainDate(2026, time.August, 10), Amount: domain.MustFromString("300.00")},
		spent: {ID: spent, AccountID: uuid.New(), Date: domainDate(2026, time.August, 12), Amount: domain.MustFromString("-250.00")},
	}
	links := GoalLinks{
		GoalID:           uuid.New(),
		TxnIDs:           []uuid.UUID{in, out, spent},
		WithdrawalTxnIDs: []uuid.UUID{out},
		SpendingTxnIDs:   []uuid.UUID{spent},
		IsTakenFromPlan:  true,
	}
	return links, LedgerLookup(ledger), []uuid.UUID{in, out, spent}
}

func TestEachLinkCarriesTheDirectionItWasFiledUnder(t *testing.T) {
	links, ledger, ids := goalLinkFixture(t)

	rows := GoalContributions(links, ledger)

	require.Len(t, rows, 3)
	byID := map[domain.ID]domain.GoalContribution{}
	for _, row := range rows {
		byID[row.TxnID] = row
	}
	require.Equal(t, domain.GoalIn, byID[domain.ID(ids[0].String())].Kind)
	require.Equal(t, domain.GoalOut, byID[domain.ID(ids[1].String())].Kind)
	require.Equal(t, domain.GoalSpent, byID[domain.ID(ids[2].String())].Kind)
}

// Left to the zero value every kind would read as a contribution and this
// would be 1,350 — a raided goal reading as nearly funded.
func TestProgressIsNetOfWithdrawalsAndIgnoresSpending(t *testing.T) {
	links, ledger, _ := goalLinkFixture(t)
	goal := domain.Goal{ID: domainID(links.GoalID), TargetAmount: domain.MustFromString("1000.00")}

	rows := GoalContributions(links, ledger)

	require.Equal(t, "500.00", domain.SavedSoFar(goal, rows).String(), "800 in, 300 back out")
	require.Equal(t, "250.00", domain.SpentOn(goal, rows).String())
}

// A row named by neither subset is a contribution.
func TestALinkNamingNoSubsetIsAContribution(t *testing.T) {
	id := uuid.New()
	ledger := LedgerLookup(map[uuid.UUID]Transaction{
		id: {ID: id, AccountID: uuid.New(), Date: domainDate(2026, time.August, 1), Amount: domain.MustFromString("-100.00")},
	})
	links := GoalLinks{GoalID: uuid.New(), TxnIDs: []uuid.UUID{id}}

	rows := GoalContributions(links, ledger)

	require.Len(t, rows, 1)
	require.Equal(t, domain.GoalIn, rows[0].Kind)
}

// Withdrawal wins a row named by both columns — the store's tie-break.
func TestARowInBothSubsetsReadsAsAWithdrawal(t *testing.T) {
	id := uuid.New()
	ledger := LedgerLookup(map[uuid.UUID]Transaction{
		id: {ID: id, AccountID: uuid.New(), Date: domainDate(2026, time.August, 1), Amount: domain.MustFromString("100.00")},
	})
	links := GoalLinks{
		GoalID: uuid.New(), TxnIDs: []uuid.UUID{id},
		WithdrawalTxnIDs: []uuid.UUID{id}, SpendingTxnIDs: []uuid.UUID{id},
	}

	require.Equal(t, domain.GoalOut, GoalContributions(links, ledger)[0].Kind)
}

// A contribution whose row is gone has no amount to add.
func TestALinkWhoseRowIsGoneContributesNothing(t *testing.T) {
	live, deleted, missing := uuid.New(), uuid.New(), uuid.New()
	ledger := LedgerLookup(map[uuid.UUID]Transaction{
		live:    {ID: live, AccountID: uuid.New(), Date: domainDate(2026, time.August, 1), Amount: domain.MustFromString("-50.00")},
		deleted: {ID: deleted, IsDeleted: true, AccountID: uuid.New(), Amount: domain.MustFromString("-50.00")},
	})
	links := GoalLinks{GoalID: uuid.New(), TxnIDs: []uuid.UUID{live, deleted, missing}}

	require.Len(t, GoalContributions(links, ledger), 1)
}

// SlotHolders must call a skip tombstone a skip and a projection a forecast,
// or domain.SettledSlots is deciding on facts it was given wrong.
func TestSlotHoldersNameWhatEachRowIs(t *testing.T) {
	seriesID, due := uuid.New(), domainDate(2026, time.September, 1)
	rows := []Transaction{
		{ID: uuid.New(), SeriesID: seriesID, SeriesDueOn: due, Date: due},
		{ID: uuid.New(), SeriesID: seriesID, SeriesDueOn: due, Date: due, EstimateStatus: ProjectedEstimate},
		{ID: uuid.New(), SeriesID: seriesID, SeriesDueOn: due, Date: due, EstimateStatus: SkippedEstimate, IsDeleted: true},
		{ID: uuid.New(), Date: due},
	}

	holders := SlotHolders(rows)

	require.Len(t, holders, 3, "a row holding no slot is not a holder")
	require.False(t, holders[0].IsForecast)
	require.False(t, holders[0].IsSkipped)
	require.True(t, holders[1].IsForecast)
	require.True(t, holders[2].IsSkipped)
	require.False(t, holders[2].IsForecast, "a skip is an answer, not a projection")
}
