package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The mailbox reader against a stand-in mail server: a message read twice
// writes one bill and one log row, a mail for an unbilled account makes one,
// and a code never reaches a column. Every address, message-id and figure here
// is invented.

// stubMailbox hands over a scripted batch and remembers what cursor it was
// asked from.
type stubMailbox struct {
	batches [][]provider.MailMessage
	asked   []string
	cursor  int
	// err, when set, is what every read answers instead of mail.
	err error
}

func (s *stubMailbox) ListNew(
	_ context.Context, cursor json.RawMessage, _ time.Duration,
) ([]provider.MailMessage, json.RawMessage, error) {
	s.asked = append(s.asked, string(cursor))
	if s.err != nil {
		return nil, nil, s.err
	}
	var batch []provider.MailMessage
	if s.cursor < len(s.batches) {
		batch = s.batches[s.cursor]
		s.cursor++
	}
	return batch, json.RawMessage(`{"last_uid":` + itoa(s.cursor) + `}`), nil
}

// Fetch answers one message out of every batch the stub was scripted with,
// whether or not the reader has already been past it.
func (s *stubMailbox) Fetch(_ context.Context, messageID string) (provider.MailMessage, bool, error) {
	for _, batch := range s.batches {
		for _, message := range batch {
			if message.ID == messageID {
				return message, true, nil
			}
		}
	}
	return provider.MailMessage{}, false, nil
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var out []byte
	for ; n > 0; n /= 10 {
		out = append([]byte{byte('0' + n%10)}, out...)
	}
	return string(out)
}

// alliantMail is a provider's bill-ready mail in the shape its parser reads,
// with an invented household on it.
func alliantMail(id string, received time.Time) provider.MailMessage {
	return provider.MailMessage{
		ID: id, ProviderID: "1", Sender: "noreply@myaccount.alliantenergy.com",
		Subject: "Your Alliant Energy bill is ready to view", ReceivedAt: received,
		HTML: `<html><body><table>
<tr><th>Account No.</th><th>Total Amount Due($)</th><th>Due Date</th></tr>
<tr><td>30005678</td><td>120.00</td><td>10-26-2026</td></tr>
</table></body></html>`,
	}
}

type mailFixture struct {
	mailbox    *Mailbox
	space      store.SpaceID
	connection *store.EmailConnection
	reader     *stubMailbox
}

func newMailFixture(t *testing.T, batches ...[]provider.MailMessage) mailFixture {
	t.Helper()
	cipher, err := store.NewCipher("the-credential-key")
	require.NoError(t, err)
	sealed := db(t).WithCipher(cipher)

	space := newSpace(t)
	connection := &store.EmailConnection{
		Label: "the bills mailbox", Kind: store.EmailKindIMAP,
		Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993,
		Username: "bills@example.invalid", Folder: "Inbox", Enabled: true,
	}
	require.NoError(t, sealed.CreateEmailConnection(t.Context(), space, connection))
	require.NoError(t, sealed.SaveEmailSecret(
		t.Context(), space, connection.ID, "abcd efgh ijkl mnop"))

	reader := &stubMailbox{batches: batches}
	mailbox := NewMailbox(sealed)
	mailbox.Bills = NewBills(sealed)
	mailbox.Now = func() time.Time { return time.Date(2026, time.October, 1, 9, 0, 0, 0, time.UTC) }
	mailbox.Open = func(store.EmailConnection, string) (provider.Mailbox, error) {
		return reader, nil
	}
	return mailFixture{mailbox: mailbox, space: space, connection: connection, reader: reader}
}

func TestAMailedBillMakesTheAccountItNamesAndThenReusesIt(t *testing.T) {
	received := time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC)
	fixture := newMailFixture(t,
		[]provider.MailMessage{alliantMail("<m1@mail.example.invalid>", received)},
		[]provider.MailMessage{alliantMail("<m2@mail.example.invalid>", received.Add(time.Hour))},
	)

	first, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Bills)
	require.Empty(t, first.Error)

	// A provider the household had no connection for gets one, with the billed
	// account the mail named.
	connections, err := db(t).ListBillConnections(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	require.Equal(t, domain.BillerAlliant, connections[0].Biller)
	require.Equal(t, "Mailbox", connections[0].Label)

	subaccounts, err := db(t).ListBillSubaccounts(t.Context(), fixture.space, connections[0].ID)
	require.NoError(t, err)
	require.Len(t, subaccounts, 1)
	require.Equal(t, "30005678", subaccounts[0].ExternalID)
	require.Equal(t, "••••5678", subaccounts[0].MaskedNumber)

	bills, err := db(t).ListBills(t.Context(), fixture.space, subaccounts[0].ID)
	require.NoError(t, err)
	require.Len(t, bills, 1)
	require.Equal(t, domain.MustFromString("120.00"), bills[0].AmountDue)
	require.Equal(t, domain.NewDate(2026, time.October, 26), bills[0].DueOn)
	require.Equal(t, store.BillSourceEmail, bills[0].Source)
	require.Equal(t, "<m1@mail.example.invalid>", bills[0].ExternalID)

	// The same cycle, mailed again: one bill on the one row, and the second
	// connection is not made.
	second, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, second.Bills)

	connections, err = db(t).ListBillConnections(t.Context(), fixture.space)
	require.NoError(t, err)
	require.Len(t, connections, 1)
	bills, err = db(t).ListBills(t.Context(), fixture.space, subaccounts[0].ID)
	require.NoError(t, err)
	require.Len(t, bills, 1, "the same subaccount and due date is one bill")

	require.Len(t, fixture.reader.asked, 2)
	require.Empty(t, fixture.reader.asked[0], "the first read has nowhere to stand")
	require.JSONEq(t, `{"last_uid":1}`, fixture.reader.asked[1],
		"the second poll carries on from where the first stood")
}

func TestAMessageTheReaderHasAlreadySeenIsNotReadAgain(t *testing.T) {
	received := time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC)
	message := alliantMail("<m1@mail.example.invalid>", received)
	fixture := newMailFixture(t,
		[]provider.MailMessage{message},
		[]provider.MailMessage{message},
	)

	first, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Bills)

	second, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, second.Read)
	require.Equal(t, 0, second.Bills, "the log already holds it")

	log, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 50)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, store.EmailOutcomeBill, log[0].Outcome)
	require.Equal(t, domain.BillerAlliant, log[0].Biller)
	require.NotEqual(t, log[0].BillID.String(), "00000000-0000-0000-0000-000000000000")
}

func TestAMailNoParserClaimsIsRecordedRatherThanDropped(t *testing.T) {
	fixture := newMailFixture(t, []provider.MailMessage{{
		ID: "<n1@mail.example.invalid>", Sender: "news@example.invalid",
		Subject:    "This month at Example",
		ReceivedAt: time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC),
		HTML:       "<p>Nothing to do with a bill.</p>",
	}})

	result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.Unrecognised)

	log, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 50)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, store.EmailOutcomeUnrecognised, log[0].Outcome)
	require.Empty(t, log[0].Biller)
}

func TestACodeIsKeptInMemoryAndNeverInAColumn(t *testing.T) {
	// The row says a code arrived; the code itself is never stored. The held
	// code expires against the wall clock, so it arrives just now.
	received := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	fixture := newMailFixture(t, []provider.MailMessage{{
		ID: "<c1@mail.example.invalid>", Sender: "noreply@myaccount.alliantenergy.com",
		Subject: "Your security code", ReceivedAt: received,
		Text: "Your security code is 481902. It expires in ten minutes.",
	}})

	result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.OTPs)

	log, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 50)
	require.NoError(t, err)
	require.Len(t, log, 1)
	require.Equal(t, store.EmailOutcomeOTP, log[0].Outcome)
	require.NotContains(t, log[0].Note, "481902")
	require.NotContains(t, log[0].Subject, "481902")

	code, found := takeCode(fixture.space, domain.BillerAlliant, received.Add(-time.Minute), false)
	require.True(t, found)
	require.Equal(t, "481902", code)

	_, found = takeCode(fixture.space, domain.BillerAlliant, received.Add(-time.Minute), false)
	require.False(t, found, "a code is spent once")
}

func TestARelayedCodeAnswersOnlyTheOneSignInThatIsWaiting(t *testing.T) {
	space := newSpace(t)
	at := time.Now().UTC().Truncate(time.Second)
	rememberCode(space, billmail.OTP{Code: "552310", Relayed: true}, at, "<r1@mail.example.invalid>")

	// Two sign-ins are stopped, so "which of these did that code come for" has
	// no answer and the code is not spent on a guess.
	_, found := takeCode(space, domain.BillerSpectrum, at.Add(-time.Minute), false)
	require.False(t, found)

	code, found := takeCode(space, domain.BillerSpectrum, at.Add(-time.Minute), true)
	require.True(t, found)
	require.Equal(t, "552310", code)
}

// forwardedBillMail is a provider's bill message sent on with
// Forward: the sender is the household, the subject carries FW:, and the
// header block sits above the provider's body. Invented throughout.
func forwardedBillMail(id, sender string, received time.Time) provider.MailMessage {
	original := alliantMail(id, received)
	return provider.MailMessage{
		ID: id, ProviderID: "1", Sender: sender,
		Subject: "FW: " + original.Subject, ReceivedAt: received,
		HTML: `<html><body><p>See below.</p><hr>
<p><b>From:</b> Alliant Energy &lt;noreply@myaccount.alliantenergy.com&gt;<br>
<b>Sent:</b> Wednesday, September 30, 2026 7:00 AM<br>
<b>To:</b> Alex Example &lt;alex@example.invalid&gt;<br>
<b>Subject:</b> ` + original.Subject + `</p>` + original.HTML,
	}
}

func TestAForwardFromTheHouseholdReadsAsTheProvidersMailAndAStrangersDoesNot(t *testing.T) {
	// The watched mailbox is bills@example.invalid, so a forward from anyone
	// at example.invalid is the household's own; one from elsewhere is a
	// claim about a sender that nothing here believes.
	received := time.Date(2026, time.September, 30, 7, 0, 0, 0, time.UTC)
	fixture := newMailFixture(t,
		[]provider.MailMessage{forwardedBillMail("<fw1@mail.example.invalid>", "alex@example.invalid", received)},
		[]provider.MailMessage{forwardedBillMail("<fw2@mail.example.invalid>", "someone@elsewhere.invalid", received.Add(time.Hour))},
	)

	first, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, first.Bills)
	require.Equal(t, 0, first.Unrecognised)

	second, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 0, second.Bills)
	require.Equal(t, 1, second.Unrecognised)

	rows, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 10)
	require.NoError(t, err)
	require.Len(t, rows, 2)
}

func rememberCode(spaceID store.SpaceID, otp billmail.OTP, at time.Time, messageID string) {
	rememberOwnedCode(spaceID, foundCode{
		owner: codeOwner{biller: otp.Biller}, code: otp.Code, relayed: otp.Relayed,
	}, at, messageID)
}

func takeCode(
	spaceID store.SpaceID, biller domain.BillerID, after time.Time, soleChallenge bool,
) (string, bool) {
	return takeOwnedCode(spaceID, codeOwner{biller: biller}, after, soleChallenge)
}

func TestAMailboxAlertQuotesAHostNotARequestURL(t *testing.T) {
	detail := `Get "https://graph.example.test/v1.0/me/mailFolders/inbox/messages/delta?$deltatoken=abc123": dial tcp: lookup graph.example.test: no such host`
	got := alertDetail(detail)
	require.NotContains(t, got, "deltatoken")
	require.Contains(t, got, `Get "graph.example.test"`)

	long := alertDetail(strings.Repeat("x", 400))
	require.Equal(t, 160, len([]rune(long)))
}
