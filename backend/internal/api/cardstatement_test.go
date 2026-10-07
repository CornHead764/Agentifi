package api

import (
	"net/http"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A billed account linked to the card it bills, over HTTP: the link is a card
// or a loan and nothing else, a statement filed on it fills the card's three
// statement figures and says where they came from, and the precedence is
// domain.StatementAfterBill's — a newer cycle replaces, an older one never
// does, and a figure typed for the same cycle stands. Every figure is
// invented, and each expected one is the bill's own.

func cardStatementSetup(t *testing.T) (l *ledger, alex *client, card, connection, subaccount string) {
	t.Helper()
	l = buildLedger(t)
	alex = frozenClient(l, "alex")
	card = newAccount(l, "Northwind Card", "credit_card", "credit_card", "0")
	created := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Northwind Card",
	})
	connection = created["id"].(string)
	rows := alex.get("/bills/subaccounts?connection_id=" + connection).requireStatus(http.StatusOK).list()
	require.Len(t, rows, 1)
	return l, alex, card, connection, rows[0]["id"].(string)
}

func TestABilledAccountLinksToACardOrALoanAndNothingElse(t *testing.T) {
	l, alex, card, _, subaccount := cardStatementSetup(t)
	checking := newAccount(l, "Everyday", "cash", "checking", "0")

	refused := alex.patch("/bills/subaccounts/"+subaccount, map[string]any{"account_id": checking})
	refused.requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, refused.Body.String(), "credit card or a loan")

	linked := alex.patch("/bills/subaccounts/"+subaccount, map[string]any{"account_id": card}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, card, linked["account_id"])
	listed := alex.get("/bills/subaccounts").requireStatus(http.StatusOK).list()
	require.Equal(t, card, listed[0]["account_id"])

	unlinked := alex.patch("/bills/subaccounts/"+subaccount, map[string]any{"account_id": nil}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, unlinked["account_id"])

	l.as("vera").patch("/bills/subaccounts/"+subaccount, map[string]any{"account_id": card}).
		requireStatus(http.StatusForbidden)
}

func TestAStatementFiledOnALinkedBilledAccountFillsTheCard(t *testing.T) {
	l, alex, card, connection, subaccount := cardStatementSetup(t)
	alex.patch("/bills/subaccounts/"+subaccount, map[string]any{"account_id": card}).
		requireStatus(http.StatusOK)
	file := func(due, amount, minimum string) {
		body := map[string]any{"due_on": due, "amount_due": amount}
		if minimum != "" {
			body["minimum_due"] = minimum
		}
		alex.post("/bills/connections/"+connection+"/bills", body).requireStatus(http.StatusCreated)
	}
	read := func() map[string]any {
		return alex.get("/accounts/" + card).requireStatus(http.StatusOK).json()
	}

	file("2026-09-21", "1100.00", "35.00")
	got := read()
	require.Equal(t, "1100.00", got["statement_balance"])
	require.Equal(t, "35.00", got["minimum_due"])
	require.Equal(t, "2026-09-21", got["due_date"])
	source := got["statement_source"].(map[string]any)
	require.Equal(t, "manual", source["source"])
	require.Equal(t, "Northwind Card", source["provider"])
	require.Equal(t, "2026-09-21", source["due_on"])

	bills := alex.get("/bills/subaccounts/" + subaccount + "/bills").requireStatus(http.StatusOK).list()
	require.Equal(t, "35.00", bills[0]["minimum_due"])

	// A newer cycle replaces all three; one with no minimum clears it.
	file("2026-10-21", "1250.00", "")
	got = read()
	require.Equal(t, "1250.00", got["statement_balance"])
	require.Nil(t, got["minimum_due"])
	require.Equal(t, "2026-10-21", got["due_date"])

	// An older statement arriving late changes nothing.
	file("2026-08-21", "990.00", "30.00")
	require.Equal(t, "1250.00", read()["statement_balance"])

	// A figure typed over the bill's is the person's: the source goes, and the
	// same cycle filed again leaves it alone.
	typed := alex.patch("/accounts/"+card, map[string]any{"statement_balance": "1290.00"}).
		requireStatus(http.StatusOK).json()
	require.Nil(t, typed["statement_source"])
	file("2026-10-21", "1284.99", "40.00")
	require.Equal(t, "1290.00", read()["statement_balance"])

	// The next cycle is the statement's again.
	file("2026-11-21", "640.00", "25.00")
	got = read()
	require.Equal(t, "640.00", got["statement_balance"])
	require.Equal(t, "25.00", got["minimum_due"])
	require.NotNil(t, got["statement_source"])

	listed := listedAccount(l, card)
	require.NotNil(t, listed["statement_source"], "the listing names the source too")
}
