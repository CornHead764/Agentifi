package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The watched mailbox over HTTP, against a stand-in for a mail server.
//
// Three things are worth the whole stack: that a mailbox is connected and
// listed without its credential coming back; that a mailed bill lands on the
// same row a pull would have put it on, once, however many polls see the same
// message; and that a code in the mailbox finishes a sign-in a pull parked
// without anybody being told about it.
//
// Every address, code, account number and figure here is invented.

// fakeMailbox is a mail server that hands over a scripted batch, and refuses
// the sign-in when it is told to.
type fakeMailbox struct {
	mu sync.Mutex
	// messages is what the next read answers.
	messages []provider.MailMessage
	// refuse makes a sign-in fail, the way a server refuses a wrong password.
	refuse bool
}

func (f *fakeMailbox) ListNew(
	_ context.Context, _ json.RawMessage, _ time.Duration,
) ([]provider.MailMessage, json.RawMessage, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.messages, json.RawMessage(`{"last_uid":1}`), nil
}

// Fetch answers a message the reader has already been past, which is what a
// re-read asks for.
func (f *fakeMailbox) Fetch(_ context.Context, messageID string) (provider.MailMessage, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, message := range f.messages {
		if message.ID == messageID {
			return message, true, nil
		}
	}
	return provider.MailMessage{}, false, nil
}

func (f *fakeMailbox) Verify(context.Context) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.refuse {
		return errors.New("imap.example.invalid refused the sign-in: [AUTHENTICATIONFAILED]")
	}
	return nil
}

// useFakeMailbox points the reader at one for the length of a test, and the
// printer at one that answers a PDF without a browser.
func useFakeMailbox(t *testing.T, mailbox *fakeMailbox) {
	t.Helper()
	openMailbox = func(store.EmailConnection, string) (provider.Mailbox, error) {
		return mailbox, nil
	}
	printMail = func(string) ([]byte, error) { return []byte("%PDF-1.7\n% the mail, printed\n"), nil }
	t.Cleanup(func() {
		openMailbox = nil
		printMail = nil
	})
}

// codeRelay stands in for the household's SMS-to-email forwarder, so a
// sign-in code arrives the way a texted one does.
const codeRelay = "texts@relay.example.invalid"

func relayingCodes(c *client) { c.env.Cfg.EmailOTPRelays = []string{codeRelay} }

// newMailboxConnection creates one and hands back its id.
func newMailboxConnection(c *client, body map[string]any) map[string]any {
	c.t.Helper()
	payload := map[string]any{
		"kind": "imap", "label": "the bills mailbox",
		"address": "bills@example.invalid", "host": "imap.example.invalid",
	}
	for key, value := range body {
		payload[key] = value
	}
	return c.post("/email/connections", payload).requireStatus(http.StatusCreated).json()
}

// connectedMailbox is one whose app password has been accepted and sealed.
func connectedMailbox(t *testing.T, alex *client) string {
	t.Helper()
	connection := newMailboxConnection(alex, nil)
	id := connection["id"].(string)
	signedIn := alex.post("/email/connections/"+id+"/sign-in",
		map[string]any{"password": "abcd efgh ijkl mnop"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, signedIn["connected"])
	return id
}

// providerMailMessage is the bill-ready mail in the shape the parser reads.
func providerMailMessage(id, account, amount string, received time.Time) provider.MailMessage {
	return provider.MailMessage{
		ID: id, ProviderID: "1", Sender: "noreply@myaccount.alliantenergy.com",
		Subject: "Your Alliant Energy bill is ready to view", ReceivedAt: received,
		HTML: `<html><body><table>` +
			`<tr><th>Account No.</th><th>Total Amount Due($)</th><th>Due Date</th></tr>` +
			`<tr><td>` + account + `</td><td>` + amount + `</td><td>10-26-2026</td></tr>` +
			`</table></body></html>`,
	}
}

func TestAMailboxIsConnectedWithoutItsCredentialComingBack(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	created := newMailboxConnection(alex, nil)
	require.Equal(t, false, created["connected"])
	require.Equal(t, "Inbox", created["folder"])
	require.EqualValues(t, 993, created["port"], "an IMAP mailbox defaults to the TLS port")
	require.NotContains(t, created, "secret")
	require.NotContains(t, created, "cursor")

	// A second mailbox with the same label is the same mailbox twice.
	alex.post("/email/connections", map[string]any{
		"kind": "imap", "label": "the bills mailbox",
		"address": "other@example.invalid", "host": "imap.example.invalid",
	}).requireStatus(http.StatusConflict)

	// An Office 365 mailbox needs its app registration before it can sign in.
	alex.post("/email/connections", map[string]any{
		"kind": "graph", "label": "the office mailbox", "address": "bills@example.invalid",
	}).requireStatus(http.StatusUnprocessableEntity)

	id := created["id"].(string)
	signedIn := alex.post("/email/connections/"+id+"/sign-in",
		map[string]any{"password": "abcd efgh ijkl mnop"}).
		requireStatus(http.StatusOK).json()
	require.Equal(t, true, signedIn["connected"])

	listing := alex.get("/email/connections").requireStatus(http.StatusOK)
	require.NotContains(t, listing.Body.String(), "abcd efgh ijkl mnop")
	rows := listing.list()
	require.Len(t, rows, 1)
	require.Equal(t, true, rows[0]["connected"])
	for field := range rows[0] {
		require.NotContains(t, []string{"secret", "password", "cursor", "refresh_token"}, field)
	}

	// Disconnecting gives the credential back and leaves the mailbox in place.
	forgotten := alex.del("/email/connections/" + id + "/secret").
		requireStatus(http.StatusOK).json()
	require.Equal(t, false, forgotten["connected"])
	require.Equal(t, true, forgotten["enabled"])
}

func TestAPasswordTheServerRefusesIsNeverSealed(t *testing.T) {
	// A password that does not work, sealed anyway, is a mailbox that sits in
	// the settings page looking connected and failing every poll for a week.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	useFakeMailbox(t, &fakeMailbox{refuse: true})

	id := newMailboxConnection(alex, nil)["id"].(string)
	refused := alex.post("/email/connections/"+id+"/sign-in",
		map[string]any{"password": "not-the-password"}).
		requireStatus(http.StatusUnprocessableEntity)
	require.Contains(t, refused.Body.String(), "AUTHENTICATIONFAILED")
	require.NotContains(t, refused.Body.String(), "not-the-password")

	require.Equal(t, false,
		alex.get("/email/connections/" + id).requireStatus(http.StatusOK).json()["connected"])
	var sealed *string
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT secret FROM email_connections WHERE id = $1`, id).Scan(&sealed))
	require.Nil(t, sealed)
}

func TestEmailBillLandsOnTheSameRowAsThePull(t *testing.T) {
	// The identity rule is what makes a mailbox just another source: the same
	// subaccount and due date is one bill, whether a pull or a mail said so.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	// Two accounts at the same provider are connected; the mail names one of them by its
	// last four.
	connection := newBillConnection(alex, map[string]any{
		"biller": string(domain.BillerAlliant), "label": "Main account",
	})
	billConnectionID := connection["id"].(string)
	for _, account := range []map[string]any{
		{"external_id": "30001234", "label": "Second account", "masked_number": "••••1234"},
		{"external_id": "30005678", "label": "Main account", "masked_number": "••••5678"},
	} {
		alex.post("/bills/connections/"+billConnectionID+"/subaccounts", account).
			requireStatus(http.StatusCreated)
	}

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{providerMailMessage(
		"<b71f0c2a@mail.example.invalid>", "30005678", "120.00",
		time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC))}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["read"])
	require.EqualValues(t, 1, polled["bills"])
	require.Equal(t, "", polled["error"])

	subaccounts := alex.get("/bills/subaccounts?connection_id=" + billConnectionID).
		requireStatus(http.StatusOK).list()
	require.Len(t, subaccounts, 2, "the mail named an account the household already bills")
	var house map[string]any
	for _, row := range subaccounts {
		if row["external_id"] == "30005678" {
			house = row
		}
	}
	require.NotNil(t, house)

	bills := alex.get("/bills/subaccounts/" + house["id"].(string) + "/bills").
		requireStatus(http.StatusOK).list()
	require.Len(t, bills, 1)
	require.Equal(t, "120.00", bills[0]["amount_due"])
	require.Equal(t, "2026-10-26", bills[0]["due_on"])
	require.Equal(t, "email", bills[0]["source"])

	// The message log says what the reader made of it, and carries no body.
	messages := alex.get("/email/connections/" + mailboxID + "/messages?limit=50").
		requireStatus(http.StatusOK).list()
	require.Len(t, messages, 1)
	require.Equal(t, "bill", messages[0]["outcome"])
	require.Equal(t, string(domain.BillerAlliant), messages[0]["biller"])
	require.Equal(t, bills[0]["id"], messages[0]["bill_id"])

	// The same message again changes nothing at all.
	again := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, again["read"])
	require.EqualValues(t, 0, again["bills"], "the log already holds it")
	require.Len(t, alex.get("/bills/subaccounts/"+house["id"].(string)+"/bills").
		requireStatus(http.StatusOK).list(), 1)
	require.Len(t, alex.get("/email/connections/"+mailboxID+"/messages").
		requireStatus(http.StatusOK).list(), 1)
}

func TestOTPFromMailboxAnswersAWaitingChallenge(t *testing.T) {
	// A pull that meets "we texted you a code" finishes without anybody in
	// the loop, and the challenge says the mailbox answered it.
	l := buildLedger(t)
	agent := newFakeBillsAgent(t)
	alex := billsClient(l, agent)
	relayingCodes(alex)

	server := &fakeMailbox{messages: []provider.MailMessage{{
		ID: "<c1f8@mail.example.invalid>", Sender: codeRelay,
		Subject: "Your Spectrum security code",
		// After the challenge is parked, which is what makes it this sign-in's
		// code rather than one from an hour ago.
		ReceivedAt: time.Now().Add(time.Minute),
		Text:       "Your Spectrum security code is 314159. It expires in ten minutes.",
	}}}
	useFakeMailbox(t, server)

	id := signInThroughTheAgent(t, alex, agent)
	connectedMailbox(t, alex)

	agent.mu.Lock()
	agent.challenge = true
	agent.mu.Unlock()

	result := alex.post("/bills/connections/"+id+"/pull", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "ok", result["status"], "the code in the mailbox finished the sign-in")
	require.EqualValues(t, 1, result["unchanged"], "the cycle the sign-in's own pull filed")

	challenges := alex.get("/bills/challenges").requireStatus(http.StatusOK).list()
	require.Len(t, challenges, 1)
	require.Equal(t, "answered", challenges[0]["state"])
	require.Equal(t, "mailbox", challenges[0]["answered_by"])

	// Nobody was told about it, because nobody had to be.
	feed := alex.get("/notifications").requireStatus(http.StatusOK).json()
	for _, one := range feed["notifications"].([]any) {
		require.NotEqual(t, "bill_challenge", one.(map[string]any)["alert_type"])
	}

	// The code was read from the mailbox and is in no row anywhere.
	log := alex.get("/email/connections").requireStatus(http.StatusOK).list()
	require.Len(t, log, 1)
	messages := alex.get("/email/connections/" + log[0]["id"].(string) + "/messages").
		requireStatus(http.StatusOK)
	require.NotContains(t, messages.Body.String(), "314159")
	rows := messages.list()
	require.Len(t, rows, 1)
	require.Equal(t, "otp", rows[0]["outcome"])
	require.Equal(t, string(domain.BillerSpectrum), rows[0]["biller"])
}

func TestTheAssistantCannotSignInToAMailbox(t *testing.T) {
	// Signing in to the watched mailbox hands over read access to every mail
	// this household gets, codes included; dropping its secret disconnects the
	// thing that answers them. Neither is a change to the ledger a person
	// could look at afterwards and undo.
	for _, route := range dispatchableRoutes() {
		if !strings.HasPrefix(route.Path(), "/email") {
			continue
		}
		for _, refused := range []string{"sign-in", "/secret"} {
			require.NotContains(t, route.Path(), refused,
				"%s %s is reachable from an in-process call", route.Method, route.Path())
		}
	}
	for _, path := range []string{
		"/email/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in",
		"/email/connections/8c6b1f33-0000-4000-8000-000000000001/sign-in/session-1",
		"/email/connections/8c6b1f33-0000-4000-8000-000000000001/secret",
	} {
		require.Error(t, refuseDeniedPath(path), "%s is reachable", path)
	}
	// The rest of the resource still is: a poll is a read somebody could press
	// themselves, and the message log is an ordinary listing.
	require.NoError(t, refuseDeniedPath("/email/connections"))
	require.NoError(t, refuseDeniedPath(
		"/email/connections/8c6b1f33-0000-4000-8000-000000000001/poll"))
}

// --- The household's own mail rules ---------------------------------------------
//
// A rule is the household's reading of its own mail, so what is worth the
// whole stack is the ledger it writes: the two rows a payroll-deducted receipt
// becomes, once, with the signs and the categories the rule named; that a rule
// beats a parser that would also have claimed the mail; and that a rule
// written after the fact reaches the mail that made somebody write it.
//
// The employer, the cafe, the figures and the receipt id are invented.

// lunchMailMessage is a receipt for something already paid by payroll
// deduction, in the shape one arrives in.
func lunchMailMessage(id string, received time.Time) provider.MailMessage {
	return provider.MailMessage{
		ID: id, ProviderID: "9", Sender: "receipts@employer.example",
		Subject: "Lunch Receipt", ReceivedAt: received,
		Text: "Your receipt from Some Cafe\n" +
			"Receipt Date: 9/27/26\n" +
			"Receipt Total: $4.50\n" +
			"\n" +
			"Item              Qty   Price\n" +
			"Soup               1     2.50\n" +
			"Coffee             1     2.00\n" +
			"\n" +
			"ReceiptID: AB1234567\n" +
			"Paid by payroll deduction.\n",
	}
}

// lunchRuleBody is the rule that reads it, with the caller's overrides.
func lunchRuleBody(l *ledger, body map[string]any) map[string]any {
	payload := map[string]any{
		"name": "Lunch", "sender": "@employer.example",
		"subject_contains": "Lunch Receipt", "amount_label": "Receipt Total",
		"date_label": "Receipt Date", "reference_label": "ReceiptID",
		"payee_label": "Your receipt from",
		"account_id":  l.str("checking"), "category_id": l.str("groceries"),
	}
	for key, value := range body {
		payload[key] = value
	}
	return payload
}

// septemberRows is the register for the month the receipt is dated in.
func septemberRows(c *client) []map[string]any {
	c.t.Helper()
	page := c.get("/transactions?from=2026-09-01&to=2026-09-30").
		requireStatus(http.StatusOK).json()
	out := []map[string]any{}
	for _, raw := range page["items"].([]any) {
		out = append(out, raw.(map[string]any))
	}
	return out
}

func TestAMailRulePostsTheExpenseAndPadsTheIncomeOnce(t *testing.T) {
	// The whole point of padding: the paycheck that lands in the bank is
	// already net of the deduction, so the expense on its own would make the
	// month look cheaper than it was.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	padding := alex.post("/categories", map[string]any{
		"name": "Payroll Padding", "kind": string(domain.CategoryIncome),
	}).requireStatus(http.StatusCreated).json()["id"].(string)
	rule := alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"pad_income": true, "income_category_id": padding,
	})).requireStatus(http.StatusCreated).json()
	require.Equal(t, "transaction", rule["action"])
	require.Equal(t, "expense", rule["direction"])
	require.Nil(t, rule["income_account_id"], "the pad lands in the expense's own account")

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{lunchMailMessage(
		"<rc9001@mail.example.invalid>",
		time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC))}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["rules"])
	require.EqualValues(t, 0, polled["unrecognised"])

	rows := septemberRows(alex)
	require.Len(t, rows, 2)
	byPayee := map[string]map[string]any{}
	for _, row := range rows {
		byPayee[row["payee"].(string)] = row
	}
	expense := byPayee["Some Cafe"]
	require.NotNil(t, expense)
	require.Equal(t, "-4.50", expense["amount"])
	require.Equal(t, "2026-09-27", expense["date"], "the receipt's own date, not the mail's")
	require.Equal(t, l.str("groceries"), expense["category_id"])
	require.Equal(t, "email", expense["source"])
	require.Equal(t, "AB1234567", expense["notes"], "the receipt id is how a person finds it again")
	require.Equal(t, "Lunch Receipt", expense["memo"])

	pad := byPayee["Some Cafe (paycheck deduction)"]
	require.NotNil(t, pad)
	require.Equal(t, "4.50", pad["amount"])
	require.Equal(t, padding, pad["category_id"])

	// The external ids are the message's, which is what makes a second
	// reading of the same mail a no-op at the database.
	var external []string
	found, err := db(t).Pool().Query(t.Context(),
		`SELECT external_id FROM transactions WHERE source = 'email' ORDER BY external_id`)
	require.NoError(t, err)
	for found.Next() {
		var one string
		require.NoError(t, found.Scan(&one))
		external = append(external, one)
	}
	found.Close()
	require.Equal(t, []string{
		"mail:<rc9001@mail.example.invalid>", "mail:<rc9001@mail.example.invalid>:income",
	}, external)

	// The log says which rule read it and what it posted.
	messages := alex.get("/email/connections/" + mailboxID + "/messages").
		requireStatus(http.StatusOK).list()
	require.Len(t, messages, 1)
	require.Equal(t, "rule", messages[0]["outcome"])
	require.Equal(t, rule["id"], messages[0]["rule_id"])
	require.Equal(t, expense["id"], messages[0]["transaction_id"])
	require.Contains(t, messages[0]["note"], `Rule "Lunch": posted 4.50`)
	require.Contains(t, messages[0]["note"], "income padded")

	// The same message again changes nothing at all.
	again := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 0, again["rules"], "the log already holds it")
	require.Len(t, septemberRows(alex), 2)
}

func TestAMailRuleBeatsAParserThatWouldAlsoClaimTheMessage(t *testing.T) {
	// A rule is what somebody wrote about their own mail; a parser that also
	// claims it is this build guessing.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"name": "The energy bill, my way", "sender": "@myaccount.alliantenergy.com",
		"subject_contains": "Alliant Energy bill", "amount_label": "",
		"amount_pattern": `([0-9]+\.[0-9]{2})`, "date_label": "", "reference_label": "",
		"payee_label": "", "payee": "Alliant Energy",
	})).requireStatus(http.StatusCreated)

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{providerMailMessage(
		"<b71f0c2a@mail.example.invalid>", "30005678", "120.00",
		time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC))}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["rules"])
	require.EqualValues(t, 0, polled["bills"], "the rule claimed it before the parser could")

	require.Empty(t, alex.get("/bills/connections").requireStatus(http.StatusOK).list(),
		"no bill, so no connection invented to hang one on")
	rows := septemberRows(alex)
	require.Len(t, rows, 1)
	require.Equal(t, "-120.00", rows[0]["amount"])
	require.Equal(t, "Alliant Energy", rows[0]["payee"])
	require.Equal(t, "2026-09-30", rows[0]["date"], "no date source, so the mail's own day")
}

func TestARuleThatCannotReadTheAmountPostsNothingAndSaysSo(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"amount_label": "Amount Charged",
	})).requireStatus(http.StatusCreated)

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{lunchMailMessage(
		"<rc9002@mail.example.invalid>",
		time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC))}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["failed"])
	require.EqualValues(t, 0, polled["rules"])
	require.EqualValues(t, 0, polled["unrecognised"],
		"the household said what this message is; it is not passed on to the parsers")

	messages := alex.get("/email/connections/" + mailboxID + "/messages").
		requireStatus(http.StatusOK).list()
	require.Len(t, messages, 1)
	require.Equal(t, "failed", messages[0]["outcome"])
	require.Contains(t, messages[0]["note"], `no amount follows "Amount Charged"`)
	require.Nil(t, messages[0]["transaction_id"])
	require.Empty(t, septemberRows(alex))
}

func TestTryingARuleAnswersTheExtractionAndStoresNothing(t *testing.T) {
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	padding := alex.post("/categories", map[string]any{
		"name": "Payroll Padding", "kind": string(domain.CategoryIncome),
	}).requireStatus(http.StatusCreated).json()["id"].(string)

	sample := lunchMailMessage("<rc9003@mail.example.invalid>", time.Now().UTC())
	tried := alex.post("/email/rules/try", map[string]any{
		"rule": lunchRuleBody(l, map[string]any{
			"pad_income": true, "income_category_id": padding,
			"notes_label": "Item", "notes_end_label": "ReceiptID",
		}),
		"sample": map[string]any{
			"sender": sample.Sender, "subject": sample.Subject, "text": sample.Text,
		},
	}).requireStatus(http.StatusOK).json()

	require.Equal(t, true, tried["matched"])
	require.Equal(t, "4.50", tried["amount"])
	require.Equal(t, "2026-09-27", tried["date"])
	require.Equal(t, "AB1234567", tried["reference"])
	require.Equal(t, "Some Cafe", tried["payee"])
	require.Equal(t, "AB1234567\nSoup 1 2.50\nCoffee 1 2.00", tried["notes"])
	require.Equal(t, "", tried["error"])
	posting := tried["would_post"].([]any)
	require.Len(t, posting, 2)
	require.Equal(t, "-4.50", posting[0].(map[string]any)["amount"])
	require.Equal(t, l.str("groceries"), posting[0].(map[string]any)["category_id"])
	require.Equal(t, "4.50", posting[1].(map[string]any)["amount"])
	require.Equal(t, padding, posting[1].(map[string]any)["category_id"])

	// Neither the rule nor the sample was stored.
	require.Empty(t, alex.get("/email/rules").requireStatus(http.StatusOK).list())
	require.Empty(t, septemberRows(alex))

	// The box runs before the account is chosen — seeing what a rule reads is
	// how somebody decides where to post it — so the extraction comes back
	// without a posting rather than as a refusal.
	early := alex.post("/email/rules/try", map[string]any{
		"rule": lunchRuleBody(l, map[string]any{
			"account_id": nil, "category_id": nil,
		}),
		"sample": map[string]any{
			"sender": sample.Sender, "subject": sample.Subject, "text": sample.Text,
		},
	}).requireStatus(http.StatusOK).json()
	require.Equal(t, true, early["matched"])
	require.Equal(t, "4.50", early["amount"])
	require.Empty(t, early["would_post"])

	// A pattern that will not compile is refused here too, so the editor says
	// so before anybody saves it.
	alex.post("/email/rules/try", map[string]any{
		"rule": lunchRuleBody(l, map[string]any{
			"amount_label": "", "amount_pattern": `total: ([0-9`,
		}),
		"sample": map[string]any{"sender": sample.Sender, "subject": sample.Subject},
	}).requireStatus(http.StatusUnprocessableEntity)
}

func TestARuleWrittenAfterTheMailReachesItOnARereadAndOnlyOnce(t *testing.T) {
	// The poll skips a message the log already holds, on purpose. Without the
	// re-read a rule only ever works on mail that has not arrived yet.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")
	server := &fakeMailbox{}
	useFakeMailbox(t, server)

	mailboxID := connectedMailbox(t, alex)
	server.mu.Lock()
	server.messages = []provider.MailMessage{lunchMailMessage(
		"<rc9004@mail.example.invalid>",
		time.Date(2026, time.September, 28, 13, 0, 0, 0, time.UTC))}
	server.mu.Unlock()

	polled := alex.post("/email/connections/"+mailboxID+"/poll", nil).
		requireStatus(http.StatusOK).json()
	require.EqualValues(t, 1, polled["unrecognised"])
	logged := alex.get("/email/connections/" + mailboxID + "/messages").
		requireStatus(http.StatusOK).list()
	require.Len(t, logged, 1)
	messageID := logged[0]["id"].(string)

	rule := alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"notes_label": "Receipt Total",
	})).requireStatus(http.StatusCreated).json()
	require.Equal(t, "Receipt Total", rule["notes_label"])
	require.Equal(t, "", rule["notes_end_label"])

	reread := alex.post(
		"/email/connections/"+mailboxID+"/messages/"+messageID+"/reread", nil).
		requireStatus(http.StatusOK).json()
	require.Equal(t, "rule", reread["outcome"])
	require.Equal(t, rule["id"], reread["rule_id"])
	require.NotNil(t, reread["transaction_id"])
	rows := septemberRows(alex)
	require.Len(t, rows, 1)
	require.Equal(t, "AB1234567\nItem Qty Price\nSoup 1 2.50\nCoffee 1 2.00", rows[0]["notes"],
		"the reference, then what was bought")

	// A message that has already produced something is not read again: doing
	// so would either post it twice or quietly unpick what it posted.
	alex.post("/email/connections/"+mailboxID+"/messages/"+messageID+"/reread", nil).
		requireStatus(http.StatusConflict)
	require.Len(t, septemberRows(alex), 1)

	// A row the mailbox no longer holds is a 404 rather than a failed rule.
	server.mu.Lock()
	server.messages = nil
	server.mu.Unlock()
	other := alex.post("/email/connections/"+mailboxID+"/messages/"+l.str("checking")+"/reread", nil)
	require.Equal(t, http.StatusNotFound, other.Code)
}

func TestARulePointingAtAnotherHouseholdsAccountIsRefused(t *testing.T) {
	// A rule saved against somebody else's account is a rule that fails every
	// message for as long as nobody reads the log.
	l := buildLedger(t)
	alex := frozenClient(l, "alex")

	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"account_id": l.str("stranger_account"),
	})).requireStatus(http.StatusUnprocessableEntity)
	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"category_id": l.str("stranger_category"),
	})).requireStatus(http.StatusUnprocessableEntity)

	// And the rest of the shape: an amount source, a category to post under,
	// and the income category a padding rule lands in.
	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"amount_label": "", "amount_pattern": "",
	})).requireStatus(http.StatusUnprocessableEntity)
	alex.post("/email/rules", lunchRuleBody(l, map[string]any{
		"pad_income": true,
	})).requireStatus(http.StatusUnprocessableEntity)

	alex.post("/email/rules", lunchRuleBody(l, nil)).requireStatus(http.StatusCreated)
	alex.post("/email/rules", lunchRuleBody(l, nil)).requireStatus(http.StatusConflict)
}
