package api

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// A mail rule that files a bill, over HTTP, onto a company that has no
// connector: the email-only provider. The company, its mail and its figures
// are invented.

// waterBillMail is the utility's bill-ready mail: HTML only, no attachment.
func waterBillMail(id string) provider.MailMessage {
	return provider.MailMessage{
		ID: id, ProviderID: "1", Sender: "billing@water.example.invalid",
		Subject:    "Your water bill is ready",
		ReceivedAt: time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC),
		HTML: `<html><body><table>
<tr><td>Account number:</td><td>5550 0056 78</td></tr>
<tr><td>Statement date:</td><td>September 29, 2026</td></tr>
<tr><td>Amount due:</td><td>$60.00</td></tr>
<tr><td>Due date:</td><td>October 20, 2026</td></tr></table></body></html>`,
	}
}

func waterRuleBody(connectionID string, body map[string]any) map[string]any {
	payload := map[string]any{
		"name": "Water bill", "action": "bill", "sender": "@water.example.invalid",
		"subject_contains": "water bill", "amount_label": "Amount due", "date_label": "Due date",
		"issued_label": "Statement date", "reference_label": "Account number",
		"bill_connection_id": connectionID,
	}
	for key, value := range body {
		payload[key] = value
	}
	return payload
}

func TestAnEmailOnlyProviderIsNamedByTheHouseholdAndNeverPulled(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	// The name is the company, so there is no such thing as an unnamed one.
	alex.post("/bills/connections", map[string]any{"biller": string(domain.BillerEmailOnly)}).
		requireStatus(http.StatusUnprocessableEntity)

	water := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Example Water",
	})
	require.Equal(t, "email-only", water["biller"])
	require.Equal(t, false, water["pull_enabled"], "nothing is ever pulled here")
	id := water["id"].(string)

	alex.patch("/bills/connections/"+id, map[string]any{"label": " "}).
		requireStatus(http.StatusUnprocessableEntity)

	listed := alex.get("/bills/connections").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, "Example Water", listed[0]["label"])

	// Its one billed account is made with it, the way any mail provider's is.
	subaccounts := alex.get("/bills/subaccounts?connection_id=" + id).
		requireStatus(http.StatusOK).list()
	require.Len(t, subaccounts, 1)
	require.Equal(t, "Example Water", subaccounts[0]["label"])
}

func TestABillRuleFilesOnAnEmailOnlyProviderAndTheLogNamesIt(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	water := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Example Water",
	})
	waterID := water["id"].(string)

	rule := alex.post("/email/rules", waterRuleBody(waterID, nil)).
		requireStatus(http.StatusCreated).json()
	require.Equal(t, "bill", rule["action"])
	require.Equal(t, waterID, rule["bill_connection_id"])
	require.Nil(t, rule["bill_subaccount_id"])
	require.Equal(t, "Statement date", rule["issued_label"])
	require.Nil(t, rule["account_id"], "a bill rule posts nowhere")

	listed := alex.get("/email/rules").requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, "bill", listed[0]["action"])

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{waterBillMail("<w1@mail.example.invalid>")}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["bills"])
	require.EqualValues(t, 0, polled["rules"])

	subaccountID := alex.get("/bills/subaccounts?connection_id=" + waterID).
		requireStatus(http.StatusOK).list()[0]["id"].(string)
	bills := alex.get("/bills/subaccounts/" + subaccountID + "/bills").
		requireStatus(http.StatusOK).list()
	require.Len(t, bills, 1)
	require.Equal(t, "60.00", bills[0]["amount_due"])
	require.Equal(t, "2026-10-20", bills[0]["due_on"])
	require.Equal(t, "email", bills[0]["source"])
	require.NotNil(t, bills[0]["document_id"], "the mail, printed, is the statement")

	messages := alex.get("/email/messages").requireStatus(http.StatusOK).list()
	require.Len(t, messages, 1)
	require.Equal(t, "bill", messages[0]["outcome"])
	require.Equal(t, rule["id"], messages[0]["rule_id"])
	require.Equal(t, bills[0]["id"], messages[0]["bill_id"])
	require.Equal(t, waterID, messages[0]["bill_connection_id"])
	require.Equal(t, bills[0]["document_id"], messages[0]["document_id"])
	require.Nil(t, messages[0]["transaction_id"])
	require.Empty(t, septemberRows(alex), "no transaction is posted from a bill")
}

func TestABillRuleNeedsAProviderOfThisHouseholdsAndOnlyItsBilledAccounts(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	water := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Example Water",
	})["id"].(string)
	cable := newBillConnection(alex, nil)["id"].(string)
	line := alex.post("/bills/connections/"+cable+"/subaccounts", map[string]any{
		"external_id": "line-1", "label": "Internet",
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	stranger := &store.BillConnection{
		Biller: domain.BillerEmailOnly, Label: "Someone Else's Water",
		CredentialSource: store.BillCredentialSession, AutopayRule: domain.AutopayNone,
	}
	require.NoError(t, db(t).CreateBillConnection(t.Context(),
		store.SpaceIDOf(l.id("other_space")), stranger))

	alex.post("/email/rules", waterRuleBody(water, map[string]any{"bill_connection_id": nil})).
		requireStatus(http.StatusUnprocessableEntity)
	alex.post("/email/rules", waterRuleBody(stranger.ID.String(), nil)).
		requireStatus(http.StatusUnprocessableEntity)
	alex.post("/email/rules", waterRuleBody(water, map[string]any{"bill_subaccount_id": line})).
		requireStatus(http.StatusUnprocessableEntity)
	alex.post("/email/rules", waterRuleBody(water, map[string]any{"action": "invoice"})).
		requireStatus(http.StatusUnprocessableEntity)

	// Pinned to one of the provider's own billed accounts, and with no
	// account or category, because a bill rule posts nothing.
	pinned := alex.post("/email/rules", waterRuleBody(cable, map[string]any{
		"bill_subaccount_id": line,
	})).requireStatus(http.StatusCreated).json()
	require.Equal(t, line, pinned["bill_subaccount_id"])
}

func TestTryingABillRuleAnswersTheBillItWouldFile(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	water := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Example Water",
	})["id"].(string)

	sample := waterBillMail("<w2@mail.example.invalid>")
	tried := alex.post("/email/rules/try", map[string]any{
		"rule": waterRuleBody(water, nil),
		"sample": map[string]any{
			"sender": sample.Sender, "subject": sample.Subject,
			"text": "Account number: 5550 0056 78\nStatement date: September 29, 2026\n" +
				"Amount due: $60.00\nDue date: October 20, 2026",
		},
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, true, tried["matched"])
	require.Equal(t, true, tried["would_file"])
	require.Equal(t, "60.00", tried["amount"])
	require.Equal(t, "2026-10-20", tried["date"])
	require.Equal(t, "2026-09-29", tried["issued_on"])
	require.Equal(t, "5550005678", tried["account"])
	require.Empty(t, tried["would_post"])
	require.Empty(t, alex.get("/email/rules").requireStatus(http.StatusOK).list())
}

func TestASuggestedRuleCanFileABillOnTheHouseholdsProvider(t *testing.T) {
	l := buildLedger(t)
	// The mail arrives before any rule or provider, so it is logged as
	// unrecognised and is there to draft from.
	rows := loggedMail(t, l, waterBillMail("<w3@mail.example.invalid>"))
	water := newBillConnection(l.alex, map[string]any{
		"biller": string(domain.BillerEmailOnly), "label": "Example Water",
	})["id"].(string)

	encoded, err := json.Marshal(map[string]any{
		"action": "bill", "bill_provider": "Example Water", "name": "Water bill",
		"sender": "billing@water.example.invalid", "amount_label": "Amount due",
		"date_label": "Due date", "issued_label": "Statement date",
		"reference_label": "Account number",
	})
	require.NoError(t, err)
	model := newFakeModel(t, answerReply(string(encoded)))
	configure(l, model)

	body := l.alex.post("/email/messages/"+rows["Your water bill is ready"]+"/suggest-rule", nil).
		requireStatus(http.StatusOK).json()
	rule := body["rule"].(map[string]any)
	require.Equal(t, "bill", rule["action"])
	require.Equal(t, water, rule["bill_connection_id"])
	require.Equal(t, "Statement date", rule["issued_label"])
	require.Nil(t, rule["account_id"])
	require.Empty(t, body["dropped"])

	sent, err := json.Marshal(model.seen[len(model.seen)-1])
	require.NoError(t, err)
	require.Contains(t, string(sent), "Example Water", "the model is shown the household's providers")
	require.Empty(t, l.alex.get("/email/rules").requireStatus(http.StatusOK).list(), "nothing was saved")
}
