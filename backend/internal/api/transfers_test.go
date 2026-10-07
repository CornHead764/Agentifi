package api

import (
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The transfers endpoints, over the whole stack.
//
// Every assertion here is about trap 2. A pair is a token two rows share, and
// the failure mode is a token on one row alone: that row is excluded from
// profit and loss forever while being ineligible to re-match, and nothing on
// any screen says so. So the tests check the *rows*, not the response — an
// unpair that answered 204 while leaving a token behind would pass a test
// written against the status code.

// seedLeg writes one transaction directly, for the shapes the seeded ledger
// does not carry: an unpaired credit, a leg whose partner never existed.
func seedLeg(
	t *testing.T,
	spaceID store.SpaceID,
	accountID uuid.UUID,
	amount string,
	on domain.Date,
	source domain.Source,
	pairID uuid.UUID,
) uuid.UUID {
	t.Helper()
	txn := &store.Transaction{
		AccountID: accountID, Date: on, Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: "SEEDED", Payee: "Seeded", Source: source, TransferPairID: pairID,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	return txn.ID
}

// legsFor is what actually carries the token, read past the API.
func legsFor(t *testing.T, spaceID store.SpaceID, pairID uuid.UUID) []store.TransferLeg {
	t.Helper()
	legs, err := db(t).ListTransferLegs(t.Context(), spaceID,
		store.TransferLegQuery{PairIDs: []uuid.UUID{pairID}})
	require.NoError(t, err)
	return legs
}

func requireUnpaired(t *testing.T, spaceID store.SpaceID, id uuid.UUID) store.Transaction {
	t.Helper()
	txn, err := db(t).GetTransaction(t.Context(), spaceID, id)
	require.NoError(t, err, "the transaction is gone; unpairing must not delete either leg")
	require.False(t, txn.IsDeleted, "unpairing soft-deleted a leg")
	require.Equal(t, uuid.Nil, txn.TransferPairID, "the row still carries a pair token")
	return txn
}

func TestTheListingShowsBothSidesOfTheSeededMovement(t *testing.T) {
	l := buildLedger(t)

	body := l.alex.get("/transfers").requireStatus(http.StatusOK).json()
	transfers := body["transfers"].([]any)
	require.Len(t, transfers, 1)

	first := transfers[0].(map[string]any)
	require.Equal(t, l.str("transfer_pair"), first["pair_id"])
	require.Equal(t, "200.00", first["amount"], "the movement is stated as a magnitude")
	require.Equal(t, false, first["paired_by_hand"], "the pairer made this one")

	from := first["from"].(map[string]any)
	to := first["to"].(map[string]any)
	require.Equal(t, l.str("transfer_out"), from["transaction_id"])
	require.Equal(t, "Everyday Checking", from["account_name"])
	require.Equal(t, l.str("transfer_in"), to["transaction_id"])
	require.Equal(t, "Rewards Card", to["account_name"])

	// The window this screen reads is the posted date, not the reporting one:
	// the two legs of one movement post together while their effective dates
	// can be a statement cycle apart.
	require.Equal(t, "posted", body["window"].(map[string]any)["date_field"])
	require.Equal(t, float64(0), body["orphan_count"])
}

func TestUnpairingLeavesBothTransactionsPresentAndUnpaired(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	l.alex.del("/transfers/" + l.str("transfer_pair")).requireStatus(http.StatusNoContent)

	// Both legs stay in the register as ordinary rows. Releasing one and not
	// the other is the orphan; deleting either is a different bug with the
	// same symptom on the balance.
	requireUnpaired(t, space, l.id("transfer_out"))
	requireUnpaired(t, space, l.id("transfer_in"))
	require.Empty(t, legsFor(t, space, l.id("transfer_pair")))

	body := l.alex.get("/transfers").requireStatus(http.StatusOK).json()
	require.Empty(t, body["transfers"])
	require.Equal(t, float64(0), body["orphan_count"],
		"an unpair that left a token behind would report an orphan here")
}

func TestUnpairingAPairThatIsNotThereIsNotFound(t *testing.T) {
	l := buildLedger(t)
	l.alex.del("/transfers/" + uuid.NewString()).requireStatus(http.StatusNotFound)
}

func TestAViewerCannotUnpairATransfer(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").del("/transfers/" + l.str("transfer_pair")).requireStatus(http.StatusForbidden)
	require.Len(t, legsFor(t, store.SpaceIDOf(l.id("space")), l.id("transfer_pair")), 2)
}

func TestPairingByHandJoinsExactlyTwoRows(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	// The pair the automatic pairer would never propose, twice over: a fee in
	// transit means the magnitudes differ, and the paying row was typed by
	// hand. Both are allowed here because the user is stating the pair rather
	// than a coincidence being read as one.
	out := seedLeg(t, space, l.id("checking"), "-500.00",
		domain.NewDate(2026, time.September, 1), domain.SourceManual, uuid.Nil)
	in := seedLeg(t, space, l.id("card"), "495.00",
		domain.NewDate(2026, time.September, 3), domain.SourceSync, uuid.Nil)

	created := l.alex.post("/transfers", map[string]any{
		"paying_transaction_id":    out.String(),
		"receiving_transaction_id": in.String(),
	}).requireStatus(http.StatusCreated).json()

	require.Equal(t, true, created["paired_by_hand"])
	require.Equal(t, "500.00", created["amount"])
	require.Equal(t, out.String(), created["from"].(map[string]any)["transaction_id"])
	require.Equal(t, in.String(), created["to"].(map[string]any)["transaction_id"])
	// The later of the two posted dates: the day the money had finished moving.
	require.Equal(t, "2026-09-03", created["moved_on"])

	pairID, err := uuid.Parse(created["pair_id"].(string))
	require.NoError(t, err)

	// Exactly two rows, and exactly these two. A third row picking up the
	// token, or only one row taking it, is the failure.
	legs := legsFor(t, space, pairID)
	require.Len(t, legs, 2)
	carriers := []uuid.UUID{legs[0].TransactionID, legs[1].TransactionID}
	require.ElementsMatch(t, []uuid.UUID{out, in}, carriers)

	// The seeded automatic pair is untouched beside it.
	require.Len(t, legsFor(t, space, l.id("transfer_pair")), 2)
}

func TestAHandPairedTransferCanBeUnpairedAgain(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	out := seedLeg(t, space, l.id("checking"), "-40.00",
		domain.NewDate(2026, time.September, 5), domain.SourceManual, uuid.Nil)
	in := seedLeg(t, space, l.id("card"), "40.00",
		domain.NewDate(2026, time.September, 5), domain.SourceManual, uuid.Nil)

	created := l.alex.post("/transfers", map[string]any{
		"paying_transaction_id":    out.String(),
		"receiving_transaction_id": in.String(),
	}).requireStatus(http.StatusCreated).json()
	pairID := created["pair_id"].(string)

	l.alex.del("/transfers/" + pairID).requireStatus(http.StatusNoContent)
	requireUnpaired(t, space, out)
	requireUnpaired(t, space, in)

	// The manual mark went with the token. If it had not, a later automatic
	// pair could not inherit it — tokens are fresh uuids — but the row would
	// describe a pair that no longer exists.
	body := l.alex.get("/transfers").requireStatus(http.StatusOK).json()
	require.Len(t, body["transfers"].([]any), 1)
	require.Equal(t, false, body["transfers"].([]any)[0].(map[string]any)["paired_by_hand"])
}

func TestPairingRefusesTheShapesThatAreNotOneMovement(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	sameAccount := seedLeg(t, space, l.id("checking"), "60.00",
		domain.NewDate(2026, time.September, 7), domain.SourceSync, uuid.Nil)

	for _, tc := range []struct {
		name              string
		paying, receiving uuid.UUID
	}{
		// Re-pairing a paired row would strand its partner holding a token
		// nothing else carries.
		{"already paired", l.id("transfer_out"), sameAccount},
		// Both sides in one account is not money moving between accounts.
		{"one account", l.id("august_groceries"), sameAccount},
		// Two debits are two expenses, and pairing them removes both from
		// profit and loss without anything having arrived anywhere.
		{"two debits", l.id("august_groceries"), l.id("august_corner")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			l.alex.post("/transfers", map[string]any{
				"paying_transaction_id":    tc.paying.String(),
				"receiving_transaction_id": tc.receiving.String(),
			}).requireStatus(http.StatusConflict)
		})
	}

	// Nothing above wrote a token on anything.
	require.Equal(t, uuid.Nil, requireUnpaired(t, space, sameAccount).TransferPairID)
	require.Len(t, legsFor(t, space, l.id("transfer_pair")), 2)
}

func TestAnOrphanLegIsFoundAndCanBeReleased(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	// A leg whose partner never existed — what a delete that skipped the
	// release leaves behind.
	strandedPair := uuid.New()
	orphan := seedLeg(t, space, l.id("checking"), "-310.00",
		domain.NewDate(2026, time.September, 9), domain.SourceSync, strandedPair)

	found := l.alex.get("/transfers/orphans").requireStatus(http.StatusOK).json()
	legs := found["orphans"].([]any)
	require.Len(t, legs, 1)
	require.Equal(t, orphan.String(), legs[0].(map[string]any)["transaction_id"])
	require.Equal(t, strandedPair.String(), legs[0].(map[string]any)["pair_id"])

	// The listing counts it without rendering it: half a movement shown as a
	// transfer would look like a rendering fault rather than the damage it is.
	listing := l.alex.get("/transfers").requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), listing["orphan_count"])
	require.Len(t, listing["transfers"].([]any), 1)

	released := l.alex.post("/transfers/orphans/repair", nil).
		requireStatus(http.StatusOK).json()
	require.Len(t, released["orphans"].([]any), 1)

	// The row stays; only the token goes, so it counts as an expense again and
	// is eligible to re-match.
	requireUnpaired(t, space, orphan)
	require.Len(t, legsFor(t, space, l.id("transfer_pair")), 2)

	// Safe to run repeatedly: a healthy space finds nothing.
	again := l.alex.post("/transfers/orphans/repair", nil).requireStatus(http.StatusOK).json()
	require.Empty(t, again["orphans"])
	require.Empty(t, l.alex.get("/transfers/orphans").
		requireStatus(http.StatusOK).json()["orphans"])
}

func TestAViewerCannotRunTheRepairJob(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	orphan := seedLeg(t, space, l.id("checking"), "-11.00",
		domain.NewDate(2026, time.September, 11), domain.SourceSync, uuid.New())

	l.as("vera").post("/transfers/orphans/repair", nil).requireStatus(http.StatusForbidden)
	// Reading the damage is not a write, so a viewer may still see it.
	require.Len(t, l.as("vera").get("/transfers/orphans").
		requireStatus(http.StatusOK).json()["orphans"].([]any), 1)

	txn, err := db(t).GetTransaction(t.Context(), space, orphan)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, txn.TransferPairID)
}

func TestCandidatesOfferTheUnpairedRowsIncludingHandEnteredOnes(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	typed := seedLeg(t, space, l.id("card"), "77.00",
		domain.NewDate(2026, time.September, 13), domain.SourceManual, uuid.Nil)

	body := l.alex.get("/transfers/candidates").requireStatus(http.StatusOK).json()
	var ids []string
	for _, entry := range body["candidates"].([]any) {
		ids = append(ids, entry.(map[string]any)["transaction_id"].(string))
	}
	// The hand-entered row is offered, which the automatic pairer's own sweep
	// excludes: a person may say the row they typed is the other half.
	require.Contains(t, ids, typed.String())
	require.Contains(t, ids, l.str("august_groceries"))
	// Already paired, so not a candidate for a second pairing.
	require.NotContains(t, ids, l.str("transfer_out"))
	require.NotContains(t, ids, l.str("transfer_in"))
}

func TestNoneOfItReachesAcrossSpaces(t *testing.T) {
	l := buildLedger(t)
	otherSpace := store.SpaceIDOf(l.id("other_space"))
	stranger, err := db(t).GetTransaction(t.Context(), otherSpace, l.id("stranger_txn"))
	require.NoError(t, err)

	// A paired movement and a stranded leg, both in the other household.
	strangerPair := uuid.New()
	strangerOut := seedLeg(t, otherSpace, stranger.AccountID, "-88.00",
		domain.NewDate(2026, time.August, 14), domain.SourceSync, strangerPair)
	seedLeg(t, otherSpace, stranger.AccountID, "88.00",
		domain.NewDate(2026, time.August, 14), domain.SourceSync, strangerPair)
	strangerOrphan := seedLeg(t, otherSpace, stranger.AccountID, "-44.00",
		domain.NewDate(2026, time.August, 15), domain.SourceSync, uuid.New())

	listing := l.alex.get("/transfers").requireStatus(http.StatusOK).json()
	require.Len(t, listing["transfers"].([]any), 1)
	require.Equal(t, l.str("transfer_pair"),
		listing["transfers"].([]any)[0].(map[string]any)["pair_id"])
	require.Equal(t, float64(0), listing["orphan_count"],
		"the other household's stranded leg was counted here")
	require.Empty(t, l.alex.get("/transfers/orphans").
		requireStatus(http.StatusOK).json()["orphans"])

	var candidates []string
	for _, entry := range l.alex.get("/transfers/candidates").
		requireStatus(http.StatusOK).json()["candidates"].([]any) {
		candidates = append(candidates, entry.(map[string]any)["transaction_id"].(string))
	}
	require.NotContains(t, candidates, l.str("stranger_txn"))

	// A pair token from another space names no transfer this caller may see.
	l.alex.del("/transfers/" + strangerPair.String()).requireStatus(http.StatusNotFound)
	require.Len(t, legsFor(t, otherSpace, strangerPair), 2)

	// Neither half of a cross-space pairing is reachable, whichever side it is
	// named on.
	local := seedLeg(t, store.SpaceIDOf(l.id("space")), l.id("checking"), "-88.00",
		domain.NewDate(2026, time.August, 14), domain.SourceSync, uuid.Nil)
	l.alex.post("/transfers", map[string]any{
		"paying_transaction_id":    local.String(),
		"receiving_transaction_id": l.str("stranger_txn"),
	}).requireStatus(http.StatusNotFound)
	l.alex.post("/transfers", map[string]any{
		"paying_transaction_id":    strangerOut.String(),
		"receiving_transaction_id": local.String(),
	}).requireStatus(http.StatusNotFound)

	// And the repair job stops at the boundary too.
	l.alex.post("/transfers/orphans/repair", nil).requireStatus(http.StatusOK)
	stillStranded, err := db(t).GetTransaction(t.Context(), otherSpace, strangerOrphan)
	require.NoError(t, err)
	require.NotEqual(t, uuid.Nil, stillStranded.TransferPairID)
}

func TestSweepingPairsAHistoryThatArrivedByImport(t *testing.T) {
	// The sync pairs the rows it just wrote, which is right for a feed and
	// useless for an imported year: those rows were never candidates, so
	// without a sweep a household's card payments stay unpaired forever.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	seedLeg(t, space, l.id("checking"), "-450.00",
		domain.NewDate(2026, time.July, 15), domain.SourceSimplifiImport, uuid.Nil)
	seedLeg(t, space, l.id("card"), "450.00",
		domain.NewDate(2026, time.July, 15), domain.SourceSimplifiImport, uuid.Nil)

	// Counted rather than assumed empty: the seeded ledger already holds a
	// paired transfer, and asserting on the total would pass for the wrong
	// reason the day that fixture changes.
	before := l.alex.get("/transfers").requireStatus(http.StatusOK).
		json()["transfers"].([]any)

	swept := l.alex.post("/transfers/detect", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), swept["paired"], "the imported pair was not found")

	after := l.alex.get("/transfers").requireStatus(http.StatusOK).json()["transfers"].([]any)
	require.Len(t, after, len(before)+1)

	// And a second sweep finds nothing new: the token is already written.
	again := l.alex.post("/transfers/detect", nil).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(0), again["paired"], "a second sweep re-paired what was already paired")
}

func TestAViewerCannotSweepForTransfers(t *testing.T) {
	l := buildLedger(t)
	l.as("vera").post("/transfers/detect", nil).requireStatus(http.StatusForbidden)
}

// A forecast is not a transfer leg, on the one path that never asks the
// candidate load.
//
// The pairer's own candidate query excludes estimates (see store.MoneyMoved):
// next month's mortgage must not be offered as a leg to pair against.
// Hand-pairing skips that query entirely, so it has to refuse a forecast
// itself, not only a deleted row. An imported ledger can carry pair tokens on
// projected rows, so the shape is not hypothetical.

func TestAForecastIsRefusedAsAHandPairedLeg(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	forecast := &store.Transaction{
		AccountID: l.id("checking"), Date: domain.NewDate(2026, time.October, 1),
		Amount: domain.MustFromString("-1800.00"), Currency: "USD",
		StatementName: "MORTGAGE", Payee: "Mortgage",
		EstimateStatus: store.ProjectedEstimate, Source: domain.SourceSimplifiImport,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, forecast))
	arriving := seedLeg(t, space, l.id("card"), "1800.00",
		domain.NewDate(2026, time.October, 1), domain.SourceSync, uuid.Nil)

	// Refused whichever half of the movement it is offered as.
	for _, tc := range []struct {
		name              string
		paying, receiving uuid.UUID
	}{
		{"the forecast pays", forecast.ID, arriving},
		{"the forecast receives", arriving, forecast.ID},
	} {
		t.Run(tc.name, func(t *testing.T) {
			body := l.alex.post("/transfers", map[string]any{
				"paying_transaction_id":    tc.paying.String(),
				"receiving_transaction_id": tc.receiving.String(),
			}).requireStatus(http.StatusConflict)
			require.Contains(t, body.Body.String(), "forecast",
				"the refusal does not say a forecast is the problem")
		})
	}

	// And no token was written on either row on the way to being refused —
	// half a pair is the orphan this whole file exists to prevent.
	require.Equal(t, uuid.Nil, requireUnpaired(t, space, forecast.ID).TransferPairID)
	require.Equal(t, uuid.Nil, requireUnpaired(t, space, arriving).TransferPairID)
}

func TestADeletedRowIsStillRefusedAndStillSaysSo(t *testing.T) {
	// The two halves of the rule fail differently on purpose: they are fixed
	// differently, so the message has to say which one it is.
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	gone := seedLeg(t, space, l.id("checking"), "-40.00",
		domain.NewDate(2026, time.September, 7), domain.SourceSync, uuid.Nil)
	require.NoError(t, db(t).DeleteTransaction(t.Context(), space, gone))
	arriving := seedLeg(t, space, l.id("card"), "40.00",
		domain.NewDate(2026, time.September, 7), domain.SourceSync, uuid.Nil)

	body := l.alex.post("/transfers", map[string]any{
		"paying_transaction_id":    gone.String(),
		"receiving_transaction_id": arriving.String(),
	}).requireStatus(http.StatusConflict)
	require.Contains(t, body.Body.String(), "deleted")
}
