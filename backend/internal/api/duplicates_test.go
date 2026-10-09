package api

import (
	"net/http"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Possible duplicates, end to end: calculations.md §2, "possible_duplicates".
//
// A Simplifi import followed by a SimpleFIN link leaves the same charge twice,
// worded and dated a little differently. The pair is proposed, never retired
// on its own, and a verdict is remembered.

func spaceOf(l *ledger) store.SpaceID { return store.SpaceIDOf(l.id("space")) }

// seedCopy writes one row of a charge from one source. A bank row carries the
// aggregator's id, as the sync writes it.
func seedCopy(
	t *testing.T, l *ledger, source domain.Source, on, amount, statement string,
) store.Transaction {
	t.Helper()
	txn := &store.Transaction{
		AccountID:     l.id("checking"),
		Date:          day(on),
		Amount:        domain.MustFromString(amount),
		Currency:      "USD",
		StatementName: statement,
		Payee:         statement,
		Source:        source,
	}
	if source == domain.SourceSync {
		txn.ExternalID = "bank-" + uuid.NewString()
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceOf(l), txn))
	return *txn
}

func day(s string) domain.Date {
	d, err := domain.ParseDate(s)
	if err != nil {
		panic(err)
	}
	return d
}

func openDuplicates(l *ledger, amount string) []map[string]any {
	l.t.Helper()
	var out []map[string]any
	body := l.alex.get("/transaction-duplicates").requireStatus(http.StatusOK).json()
	for _, raw := range body["pairs"].([]any) {
		pair := raw.(map[string]any)
		if pair["first"].(map[string]any)["amount"] == amount {
			out = append(out, pair)
		}
	}
	return out
}

func scanDups(l *ledger) map[string]any {
	l.t.Helper()
	return l.alex.post("/transaction-duplicates/scan", nil).requireStatus(http.StatusOK).json()
}

func TestAScanProposesTheSameChargeFromTwoSourcesAndRetiresNothing(t *testing.T) {
	l := buildLedger(t)
	imported := seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.34", "Corner Market")
	synced := seedCopy(t, l, domain.SourceSync, "2026-02-11", "-612.34", "CORNER MKT #0042")

	scanDups(l)

	pairs := openDuplicates(l, "-612.34")
	require.Len(t, pairs, 1)
	pair := pairs[0]
	require.Equal(t, l.str("checking"), pair["account_id"])
	require.Equal(t, "Everyday Checking", pair["account_name"])
	require.EqualValues(t, 1, pair["days_apart"])
	require.Equal(t, imported.ID.String(), pair["suggested_keep_id"], "the import, not the bank's copy")

	for _, id := range []uuid.UUID{imported.ID, synced.ID} {
		row, err := db(t).GetTransaction(t.Context(), spaceOf(l), id)
		require.NoError(t, err)
		require.False(t, row.IsDeleted, "a proposal never deletes")
	}
}

func TestADuplicateVerdictRetiresTheBanksCopyAndTheKeptRowTakesItsId(t *testing.T) {
	l := buildLedger(t)
	imported := seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.35", "Corner Market")
	synced := seedCopy(t, l, domain.SourceSync, "2026-02-11", "-612.35", "CORNER MKT #0042")
	scanDups(l)
	pair := openDuplicates(l, "-612.35")[0]

	decided := l.alex.post("/transaction-duplicates/"+pair["id"].(string)+"/decide",
		map[string]any{"verdict": "duplicate"}).requireStatus(http.StatusOK).json()
	require.Equal(t, imported.ID.String(), decided["kept_id"])
	require.Equal(t, synced.ID.String(), decided["retired_id"])

	kept, err := db(t).GetTransaction(t.Context(), spaceOf(l), imported.ID)
	require.NoError(t, err)
	require.False(t, kept.IsDeleted)
	require.Equal(t, synced.ExternalID, kept.ExternalID,
		"the next sync settles the kept row by id instead of writing the charge again")

	gone, err := db(t).GetTransaction(t.Context(), spaceOf(l), synced.ID)
	require.NoError(t, err)
	require.True(t, gone.IsDeleted)
	require.Empty(t, gone.ExternalID)

	require.Empty(t, openDuplicates(l, "-612.35"))
	scanDups(l)
	require.Empty(t, openDuplicates(l, "-612.35"), "a decided pair is not proposed again")

	l.alex.post("/transaction-duplicates/"+pair["id"].(string)+"/decide",
		map[string]any{"verdict": "duplicate"}).requireStatus(http.StatusConflict)
}

func TestTheKeptCopyCanBeTheBanksOne(t *testing.T) {
	l := buildLedger(t)
	imported := seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.36", "Corner Market")
	synced := seedCopy(t, l, domain.SourceSync, "2026-02-10", "-612.36", "CORNER MKT")
	scanDups(l)
	pair := openDuplicates(l, "-612.36")[0]

	l.alex.post("/transaction-duplicates/"+pair["id"].(string)+"/decide",
		map[string]any{"verdict": "duplicate", "keep_id": synced.ID}).requireStatus(http.StatusOK)

	kept, err := db(t).GetTransaction(t.Context(), spaceOf(l), synced.ID)
	require.NoError(t, err)
	require.False(t, kept.IsDeleted)
	require.NotEmpty(t, kept.ExternalID, "the bank's own id stays with the bank's row")
	gone, err := db(t).GetTransaction(t.Context(), spaceOf(l), imported.ID)
	require.NoError(t, err)
	require.True(t, gone.IsDeleted)
}

func TestADistinctVerdictKeepsBothRowsAndTheQuestionIsNeverAskedAgain(t *testing.T) {
	l := buildLedger(t)
	imported := seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.37", "Corner Market")
	synced := seedCopy(t, l, domain.SourceSync, "2026-02-11", "-612.37", "CORNER MKT")
	scanDups(l)
	pair := openDuplicates(l, "-612.37")[0]

	l.alex.post("/transaction-duplicates/"+pair["id"].(string)+"/decide",
		map[string]any{"verdict": "distinct"}).requireStatus(http.StatusOK)

	for _, id := range []uuid.UUID{imported.ID, synced.ID} {
		row, err := db(t).GetTransaction(t.Context(), spaceOf(l), id)
		require.NoError(t, err)
		require.False(t, row.IsDeleted)
	}
	scanDups(l)
	require.Empty(t, openDuplicates(l, "-612.37"))
}

func TestRetiringACopyThatIsATransferLegReleasesItsPartner(t *testing.T) {
	l := buildLedger(t)
	// A payment recorded twice: the bank's copy of the checking leg is paired
	// with the card's leg (trap 2).
	imported := seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.38", "Card payment")
	synced := seedCopy(t, l, domain.SourceSync, "2026-02-11", "-612.38", "CARD PMT")
	card := seedLeg(t, spaceOf(l), l.id("card"), "612.38", day("2026-02-11"), domain.SourceSync, uuid.Nil)
	pairID, ok, err := db(t).PairTransactions(t.Context(), spaceOf(l), synced.ID, card, false)
	require.NoError(t, err)
	require.True(t, ok)

	scanDups(l)
	pair := openDuplicates(l, "-612.38")
	require.Len(t, pair, 1)
	require.True(t, pair[0]["second"].(map[string]any)["is_transfer_leg"].(bool) ||
		pair[0]["first"].(map[string]any)["is_transfer_leg"].(bool))

	l.alex.post("/transaction-duplicates/"+pair[0]["id"].(string)+"/decide",
		map[string]any{"verdict": "duplicate"}).requireStatus(http.StatusOK)

	orphans := l.alex.get("/transfers/orphans").requireStatus(http.StatusOK).json()["orphans"].([]any)
	for _, raw := range orphans {
		require.NotEqual(t, card.String(), raw.(map[string]any)["transaction_id"],
			"the card leg was left holding a token for a partner that is gone")
	}
	got, err := db(t).GetTransaction(t.Context(), spaceOf(l), card)
	require.NoError(t, err)
	require.NotEqual(t, pairID, got.TransferPairID, "the card leg was released from the retired pair")
	kept, err := db(t).GetTransaction(t.Context(), spaceOf(l), imported.ID)
	require.NoError(t, err)
	require.False(t, kept.IsDeleted)
}

func TestAViewerReadsTheProposalsButCannotDecideThem(t *testing.T) {
	l := buildLedger(t)
	seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.39", "Corner Market")
	seedCopy(t, l, domain.SourceSync, "2026-02-10", "-612.39", "CORNER MKT")
	scanDups(l)
	pair := openDuplicates(l, "-612.39")[0]

	vera := l.as("vera")
	vera.get("/transaction-duplicates").requireStatus(http.StatusOK)
	vera.post("/transaction-duplicates/"+pair["id"].(string)+"/decide",
		map[string]any{"verdict": "duplicate"}).requireStatus(http.StatusForbidden)
	vera.post("/transaction-duplicates/scan", nil).requireStatus(http.StatusForbidden)
}

func TestAVerdictNeedsAVerdictAndARowOfThePair(t *testing.T) {
	l := buildLedger(t)
	seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.40", "Corner Market")
	seedCopy(t, l, domain.SourceSync, "2026-02-10", "-612.40", "CORNER MKT")
	scanDups(l)
	path := "/transaction-duplicates/" + openDuplicates(l, "-612.40")[0]["id"].(string) + "/decide"

	l.alex.post(path, map[string]any{"verdict": "maybe"}).requireStatus(http.StatusUnprocessableEntity)
	l.alex.post(path, map[string]any{"verdict": "duplicate", "keep_id": l.id("checking")}).
		requireStatus(http.StatusUnprocessableEntity)
	l.alex.post("/transaction-duplicates/"+uuid.NewString()+"/decide",
		map[string]any{"verdict": "distinct"}).requireStatus(http.StatusNotFound)
}

func TestRowsArrivingThroughTheIngestAreCheckedAgainstWhatIsAlreadyThere(t *testing.T) {
	l := buildLedger(t)
	seedCopy(t, l, domain.SourceSimplifiImport, "2026-02-10", "-612.41", "Corner Market")
	arriving := seedCopy(t, l, domain.SourceSync, "2026-02-12", "-612.41", "CORNER MKT")

	_, err := NewIngest(l.env).AfterIngest(t.Context(), l.env.DB, spaceOf(l), []uuid.UUID{arriving.ID})
	require.NoError(t, err)

	require.Len(t, openDuplicates(l, "-612.41"), 1, "no scan was asked for")
}

func TestAnAmountOnlyOneSourceHasIsNotProposed(t *testing.T) {
	l := buildLedger(t)
	seedCopy(t, l, domain.SourceSync, "2026-02-10", "-612.42", "CORNER MKT")
	seedCopy(t, l, domain.SourceSync, "2026-02-10", "-612.42", "CORNER MKT")
	scanDups(l)
	require.Empty(t, openDuplicates(l, "-612.42"))
}
