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

// The refund-link endpoints, over the whole stack.
//
// The assertions that matter are about the discipline trap 2 names. A refund link
// is a row rather than a token on two transactions precisely so it cannot
// half-exist, and the test that proves it is a delete followed by a read of the
// *links*, not of a status code.

// seedCharge writes one transaction directly, for the shapes the seeded ledger
// does not carry: a credit filed under nothing, a charge far enough back to fall
// outside the offer window.
func seedCharge(
	t *testing.T,
	spaceID store.SpaceID,
	accountID, categoryID uuid.UUID,
	amount, payee string,
	on domain.Date,
) uuid.UUID {
	t.Helper()
	txn := &store.Transaction{
		AccountID: accountID, CategoryID: categoryID, Date: on,
		Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: payee, Payee: payee, Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	return txn.ID
}

func refundLinkRows(t *testing.T, spaceID store.SpaceID, id uuid.UUID) []store.RefundLink {
	t.Helper()
	links, err := db(t).ListRefundLinksFor(t.Context(), spaceID, []uuid.UUID{id})
	require.NoError(t, err)
	return links
}

func TestLinkingARefundReportsBothEndsOfTheLink(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
	// Filed under nothing, which is the case a link exists for: the sign says
	// income and no category says otherwise.
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
		"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))

	body := l.alex.post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, true, body["can_be_a_refund"])
	refunds := body["refunds"].([]any)
	require.Len(t, refunds, 1)
	require.Equal(t, charge.String(), refunds[0].(map[string]any)["id"])
	require.Equal(t, "-50.00", refunds[0].(map[string]any)["amount"])
	require.Empty(t, body["refunded_by"])

	// The charge answers the other half of the same link.
	back := l.alex.get("/refunds/transactions/" + charge.String()).
		requireStatus(http.StatusOK).json()
	require.Empty(t, back["refunds"])
	refundedBy := back["refunded_by"].([]any)
	require.Len(t, refundedBy, 1)
	require.Equal(t, credit.String(), refundedBy[0].(map[string]any)["id"])
	// A charge is not itself a credit, so the affordance is not offered on it.
	require.Equal(t, false, back["can_be_a_refund"])
}

// Linking says the money came back out of that purchase, so the two rows are one
// decision about where it is filed — in both directions, and from the moment the
// link is made rather than at the next edit.
func TestALinkedRefundAndItsChargeShareOneCategory(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
		"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))

	l.alex.post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusOK)

	// The credit was filed under nothing; linking files it where the money left.
	require.Equal(t, l.id("groceries"), categoryOf(t, space, credit))

	// Recategorising the charge carries the credit with it.
	l.alex.patch("/transactions/"+charge.String(), map[string]any{
		"category_id": l.id("food").String(),
	}).requireStatus(http.StatusOK)
	require.Equal(t, l.id("food"), categoryOf(t, space, credit))

	// And the other direction: the charge follows the credit.
	l.alex.patch("/transactions/"+credit.String(), map[string]any{
		"category_id": l.id("groceries").String(),
	}).requireStatus(http.StatusOK)
	require.Equal(t, l.id("groceries"), categoryOf(t, space, charge))
}

func categoryOf(t *testing.T, spaceID store.SpaceID, id uuid.UUID) uuid.UUID {
	t.Helper()
	txn, err := db(t).GetTransaction(t.Context(), spaceID, id)
	require.NoError(t, err)
	return txn.CategoryID
}

// Trap 2's discipline, in the shape this table takes: deleting either side must
// leave no half behind.
func TestDeletingEitherSideReleasesTheRefundLink(t *testing.T) {
	for _, which := range []string{"the credit", "the charge"} {
		t.Run("deleting "+which, func(t *testing.T) {
			l := buildLedger(t)
			space := store.SpaceIDOf(l.id("space"))

			charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
				"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
			credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
				"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))
			l.alex.post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
				"charge_transaction_id": charge.String(),
			}).requireStatus(http.StatusOK)
			require.Len(t, refundLinkRows(t, space, credit), 1)

			doomed, survivor := credit, charge
			if which == "the charge" {
				doomed, survivor = charge, credit
			}
			l.alex.del("/transactions/" + doomed.String()).requireStatus(http.StatusNoContent)

			// Read the links, not a response: a delete that answered 204 while
			// leaving the row behind would pass an assertion on the status code
			// and leave the survivor refiling money against something gone.
			require.Empty(t, refundLinkRows(t, space, survivor),
				"the surviving row still carries a link to a deleted transaction")
			require.Empty(t, refundLinkRows(t, space, doomed))
		})
	}
}

func TestUnlinkingARefundLeavesBothTransactionsAlone(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
		"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))
	l.alex.post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusOK)

	l.alex.del("/refunds/transactions/" + credit.String() + "/charges/" + charge.String()).
		requireStatus(http.StatusNoContent)
	require.Empty(t, refundLinkRows(t, space, credit))

	for _, id := range []uuid.UUID{charge, credit} {
		row, err := db(t).GetTransaction(t.Context(), space, id)
		require.NoError(t, err, "unlinking must not delete either transaction")
		require.False(t, row.IsDeleted)
	}

	l.alex.del("/refunds/transactions/" + credit.String() + "/charges/" + charge.String()).
		requireStatus(http.StatusNotFound)
}

func TestTheRefundAffordanceIsRefusedWhereARefundMeansNothing(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))

	// A leg of the seeded transfer. Whatever it is filed under, it is money
	// moving inside the household and never a refund — which is also how a
	// credit-card payment is refused, since that is what it is.
	leg := l.id("transfer_in")
	l.alex.get("/refunds/transactions/" + leg.String() + "/candidates").
		requireStatus(http.StatusConflict)
	require.Equal(t, false,
		l.alex.get("/refunds/transactions/" + leg.String()).
			requireStatus(http.StatusOK).json()["can_be_a_refund"])
	l.alex.post("/refunds/transactions/"+leg.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusConflict)

	// A charge is not a credit either way round.
	l.alex.post("/refunds/transactions/"+charge.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusConflict)
}

func TestARefundCannotBeLinkedToASmallerCharge(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))

	small := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-10.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
		"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))

	l.alex.post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
		"charge_transaction_id": small.String(),
	}).requireStatus(http.StatusConflict)
	require.Empty(t, refundLinkRows(t, space, credit))
}

func TestTheCandidateListOffersTheLikeliestChargeFirst(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	on := domain.NewDate(2026, time.September, 20)

	// Exact magnitude and the same payee beats a bigger charge from elsewhere.
	bigger := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-90.00", "Hardware Store", on.AddDays(-3))
	exact := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-20.00", "Fresh Market", on.AddDays(-4))
	// Too small to be what a $20 credit gave back, and too old to be offered
	// unprompted.
	tooSmall := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-5.00", "Fresh Market", on.AddDays(-2))
	tooOld := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-90.00", "Ancient Shop", on.AddDays(-domain.RefundCandidateWindowDays-5))
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil, "20.00", "Fresh Market", on)

	body := l.alex.get("/refunds/transactions/" + credit.String() + "/candidates").
		requireStatus(http.StatusOK).json()
	var got []string
	for _, one := range body["candidates"].([]any) {
		got = append(got, one.(map[string]any)["id"].(string))
	}
	require.Contains(t, got, exact.String())
	require.Contains(t, got, bigger.String())
	require.Equal(t, exact.String(), got[0], "the exact-amount same-payee charge comes first")
	require.NotContains(t, got, tooSmall.String())
	require.NotContains(t, got, tooOld.String())
	require.NotContains(t, got, credit.String(), "a credit is never offered as its own charge")

	// A search reaches past the window, which is the whole point of having one.
	searched := l.alex.get("/refunds/transactions/" + credit.String() +
		"/candidates?q=Ancient").requireStatus(http.StatusOK).json()
	var found []string
	for _, one := range searched["candidates"].([]any) {
		found = append(found, one.(map[string]any)["id"].(string))
	}
	require.Equal(t, []string{tooOld.String()}, found)
}

func TestAViewerCannotLinkARefund(t *testing.T) {
	l := buildLedger(t)
	space := store.SpaceIDOf(l.id("space"))
	charge := seedCharge(t, space, l.id("checking"), l.id("groceries"),
		"-50.00", "Fresh Market", domain.NewDate(2026, time.September, 2))
	credit := seedCharge(t, space, l.id("checking"), uuid.Nil,
		"20.00", "Fresh Market", domain.NewDate(2026, time.September, 9))

	l.as("vera").post("/refunds/transactions/"+credit.String()+"/charges", map[string]any{
		"charge_transaction_id": charge.String(),
	}).requireStatus(http.StatusForbidden)
	require.Empty(t, refundLinkRows(t, space, credit))
}

func TestARefundInAnotherHouseholdIsNotFound(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/refunds/transactions/" + uuid.NewString()).requireStatus(http.StatusNotFound)
}
