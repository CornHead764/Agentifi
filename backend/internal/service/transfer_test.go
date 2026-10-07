package service

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Transfer pairing: the rules, then the part only a database can prove.

func TestOnlySyncedAndImportedRowsPair(t *testing.T) {
	// A hand-entered row is never paired behind the user's back.
	require.True(t, pairableSources[domain.SourceSync])
	require.True(t, pairableSources[domain.SourceSimplifiImport])
	require.False(t, pairableSources[domain.SourceManual])
	require.False(t, pairableSources[domain.SourceOpeningBalance])
}

func twoAccounts(t *testing.T) (store.SpaceID, *store.Account, *store.Account) {
	t.Helper()
	spaceID := newSpace(t)
	return spaceID, newAccount(t, spaceID, "Checking"), newAccount(t, spaceID, "Savings")
}

func TestPairingSkipsALegAlreadyPairedAndLeavesNoOrphan(t *testing.T) {
	// A leg that became paired between the plan and the write must not have
	// its token overwritten, or its existing partner is stranded.
	spaceID, checking, savings := twoAccounts(t)
	paying := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	receiving := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	// The receiving leg is already spoken for — it carries a token from an
	// existing pair, exactly the state a concurrent pairing would leave it in.
	existingToken := uuid.New()
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET transfer_pair_id = $2 WHERE id = $1`,
		receiving.ID, existingToken)
	require.NoError(t, err)

	_, paired, err := db(t).PairTransactions(t.Context(), spaceID, paying.ID, receiving.ID, false)
	require.NoError(t, err)
	require.False(t, paired, "an already-paired leg must not be re-paired")

	// The paying leg was never left holding the abandoned new token, and the
	// receiving leg kept the token it already had.
	require.Equal(t, uuid.Nil, reload(t, spaceID, paying.ID).TransferPairID)
	require.Equal(t, existingToken, reload(t, spaceID, receiving.ID).TransferPairID)
}

func TestPairingSkipsADeletedLeg(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	paying := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	receiving := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET is_deleted = true WHERE id = $1`, receiving.ID)
	require.NoError(t, err)

	_, paired, err := db(t).PairTransactions(t.Context(), spaceID, paying.ID, receiving.ID, false)
	require.NoError(t, err)
	require.False(t, paired)
	require.Equal(t, uuid.Nil, reload(t, spaceID, paying.ID).TransferPairID)
}

func TestBothLegsAreWrittenWithOneToken(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	into := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	count, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, count)

	first, second := reload(t, spaceID, out.ID), reload(t, spaceID, into.ID)
	require.NotEqual(t, uuid.Nil, first.TransferPairID)
	require.Equal(t, first.TransferPairID, second.TransferPairID)
}

func TestARowInAnIgnoredAccountIsNeverPaired(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")
	_, err := db(t).SetAccountsIgnored(t.Context(), spaceID, []uuid.UUID{savings.ID}, true)
	require.NoError(t, err)

	count, err := NewTransfers(db(t)).DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, count)
	require.Equal(t, uuid.Nil, reload(t, spaceID, out.ID).TransferPairID)
}

func TestAHandEnteredRowIsLeftAloneAgainstARealLedger(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00",
		withSource(domain.SourceManual))
	newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00",
		withSource(domain.SourceManual))

	count, err := NewTransfers(db(t)).DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestACardDebitIsLeftAloneAgainstARealLedger(t *testing.T) {
	spaceID := newSpace(t)
	card := newAccount(t, spaceID, "Card", withKind(domain.KindCreditCard))
	checking := newAccount(t, spaceID, "Checking")
	newTransaction(t, spaceID, card, on(2026, time.March, 2), "-42.00")
	newTransaction(t, spaceID, checking, on(2026, time.March, 3), "42.00")

	count, err := NewTransfers(db(t)).DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, count)
}

func TestUnlinkClearsBothLegs(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	into := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	_, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	pairID := reload(t, spaceID, out.ID).TransferPairID

	released, err := transfers.UnlinkPair(t.Context(), spaceID, pairID)
	require.NoError(t, err)
	require.Equal(t, 2, released)
	require.Equal(t, uuid.Nil, reload(t, spaceID, out.ID).TransferPairID)
	require.Equal(t, uuid.Nil, reload(t, spaceID, into.ID).TransferPairID)
}

func TestDeletingALegLeavesTheSurvivorOrdinary(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	into := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	_, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)

	require.NoError(t, db(t).DeleteTransaction(t.Context(), spaceID, out.ID))
	require.Equal(t, uuid.Nil, reload(t, spaceID, into.ID).TransferPairID)
}

func TestDeletingAnAccountReleasesThePartnerThatSurvives(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	into := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	_, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)

	require.NoError(t, db(t).DeleteAccount(t.Context(), spaceID, checking.ID))
	require.Equal(t, uuid.Nil, reload(t, spaceID, into.ID).TransferPairID)
}

func TestTheRepairJobReleasesALegWhosePartnerIsGone(t *testing.T) {
	// Hard-deletes one leg underneath the pair, as a path that forgot to
	// release it would.
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	into := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	_, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)

	// Deleted without releasing the pair first — the orphan this repairs.
	_, err = db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET is_deleted = true WHERE id = $1`, out.ID)
	require.NoError(t, err)

	orphans, err := transfers.FindOrphanPairs(t.Context(), spaceID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{into.ID}, orphans)

	repaired, err := transfers.RepairOrphanPairs(t.Context(), spaceID)
	require.NoError(t, err)
	require.Equal(t, []uuid.UUID{into.ID}, repaired)
	require.Equal(t, uuid.Nil, reload(t, spaceID, into.ID).TransferPairID)

	// Idempotent. The deleted row keeps its token and nobody cares — it is
	// excluded everywhere; the survivor is the one that had to be released.
	again, err := transfers.RepairOrphanPairs(t.Context(), spaceID)
	require.NoError(t, err)
	require.Empty(t, again)

	var stillPaired int
	require.NoError(t, db(t).Pool().QueryRow(t.Context(), `
		SELECT count(*) FROM transactions
		WHERE space_id = $1 AND NOT is_deleted AND transfer_pair_id IS NOT NULL`,
		spaceID.UUID()).Scan(&stillPaired))
	require.Equal(t, 0, stillPaired)
}

func TestAHealthyPairIsNotReportedAsAnOrphan(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	transfers := NewTransfers(db(t))
	_, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)

	orphans, err := transfers.FindOrphanPairs(t.Context(), spaceID)
	require.NoError(t, err)
	require.Empty(t, orphans)
}

func TestASyncOnlyPairsTheRowsThatJustArrived(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	oldOut := newTransaction(t, spaceID, checking, on(2026, time.January, 5), "-250.00")
	oldIn := newTransaction(t, spaceID, savings, on(2026, time.January, 5), "250.00")
	freshOut := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	freshIn := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")

	count, err := NewTransfers(db(t)).DetectPairs(t.Context(), spaceID,
		PairOptions{CandidateIDs: []uuid.UUID{freshIn.ID}})
	require.NoError(t, err)
	require.Equal(t, 1, count)

	require.Equal(t, uuid.Nil, reload(t, spaceID, oldOut.ID).TransferPairID)
	require.Equal(t, uuid.Nil, reload(t, spaceID, oldIn.ID).TransferPairID)
	require.Equal(t,
		reload(t, spaceID, freshOut.ID).TransferPairID,
		reload(t, spaceID, freshIn.ID).TransferPairID)
	require.NotEqual(t, uuid.Nil, reload(t, spaceID, freshIn.ID).TransferPairID)
}

// A forecast is not a leg of anything: pairing one against a real debit
// strands the real credit that arrives later, which then counts as income.
func TestAForecastRowIsNeverATransferLeg(t *testing.T) {
	spaceID, checking, savings := twoAccounts(t)
	out := newTransaction(t, spaceID, checking, on(2026, time.March, 2), "-500.00")
	forecast := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET estimate_status = $2, source = $3 WHERE id = $1`,
		forecast.ID, store.ProjectedEstimate, string(domain.SourceSimplifiImport))
	require.NoError(t, err)

	transfers := NewTransfers(db(t))
	count, err := transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 0, count, "the projection is not money that moved")
	require.Equal(t, uuid.Nil, reload(t, spaceID, out.ID).TransferPairID)

	// And the real credit that eventually posts pairs with it.
	real := newTransaction(t, spaceID, savings, on(2026, time.March, 3), "500.00")
	count, err = transfers.DetectPairs(t.Context(), spaceID, PairOptions{})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	require.Equal(t, reload(t, spaceID, out.ID).TransferPairID,
		reload(t, spaceID, real.ID).TransferPairID)
}
