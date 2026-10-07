package service

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Reading a code with the household's model behind the deterministic reader,
// and the waits that sign-ins park on. Every sender, code and figure here is
// invented.

// scriptedModel answers every question with one reply and counts the asking.
type scriptedModel struct {
	reply string
	err   error
	asked []provider.ChatMessage
	tools int
}

func (s *scriptedModel) Complete(
	_ context.Context, messages []provider.ChatMessage, tools []domain.AssistantTool,
) (provider.Reply, error) {
	s.asked = append(s.asked, messages...)
	s.tools += len(tools)
	if s.err != nil {
		return provider.Reply{}, s.err
	}
	return provider.Reply{Content: s.reply}, nil
}

func (s *scriptedModel) calls() int {
	n := 0
	for _, one := range s.asked {
		if one.Role == "system" {
			n++
		}
	}
	return n
}

func withModel(mailbox *Mailbox, model MailModel) {
	mailbox.Model = func(context.Context, store.SpaceID) (MailModel, error) { return model, nil }
}

// codeMailWithNoCue is a code mail the digit reader has no cue for.
func codeMailWithNoCue(id string, received time.Time) provider.MailMessage {
	return provider.MailMessage{
		ID: id, Sender: "MyAccount@spectrumemails.com", Subject: "Finish signing in",
		ReceivedAt: received, Text: "Use 314159 to finish signing in. Account ending 0000.",
	}
}

func TestTheDigitReaderGoesFirstAndTheModelIsNotAsked(t *testing.T) {
	received := time.Now().UTC()
	fixture := newMailFixture(t, []provider.MailMessage{{
		ID: "<first-" + uuid.NewString() + "@mail.example.invalid>", Sender: "noreply@myaccount.alliantenergy.com",
		Subject: "Your security code", ReceivedAt: received,
		Text: "Your security code is 604218.",
	}})
	model := &scriptedModel{reply: `{"code":"111111"}`}
	withModel(fixture.mailbox, model)
	closeWait := openCodeWait(codeWait{
		spaceID: fixture.space, owner: codeOwner{biller: domain.BillerAlliant}, name: "Alliant Energy",
		senders: billmail.CodeSendersFor(domain.BillerAlliant), since: received,
	})
	defer closeWait()

	result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.OTPs)
	require.Zero(t, model.calls(), "the model is asked only when the digit reader finds nothing")
	code, found := takeCode(fixture.space, domain.BillerAlliant, received.Add(-time.Minute), false)
	require.True(t, found)
	require.Equal(t, "604218", code)
}

func TestTheModelReadsACodeTheDigitReaderCannotWhileASignInWaits(t *testing.T) {
	received := time.Now().UTC()
	fixture := newMailFixture(t, []provider.MailMessage{
		codeMailWithNoCue("<fallback-"+uuid.NewString()+"@mail.example.invalid>", received),
	})
	model := &scriptedModel{reply: "```json\n{\"code\": \"314159\"}\n```"}
	withModel(fixture.mailbox, model)
	closeWait := openCodeWait(codeWait{
		spaceID: fixture.space, owner: codeOwner{biller: domain.BillerSpectrum}, name: "Spectrum",
		senders: billmail.CodeSendersFor(domain.BillerSpectrum), since: received,
	})
	defer closeWait()

	result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, 1, result.OTPs)
	require.Equal(t, 1, model.calls())
	require.Zero(t, model.tools, "the code question carries no tools")
	require.Contains(t, model.asked[1].Content, "Spectrum")
	require.Contains(t, model.asked[1].Content, "<<<EMAIL", "the mail goes in as delimited data")
	require.Contains(t, model.asked[0].Content, "untrusted data")

	code, found := takeCode(fixture.space, domain.BillerSpectrum, received.Add(-time.Minute), false)
	require.True(t, found)
	require.Equal(t, "314159", code)

	log, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 5)
	require.NoError(t, err)
	require.Equal(t, store.EmailOutcomeOTP, log[0].Outcome)
	require.NotContains(t, log[0].Note, "314159")
}

func TestAModelAnswerThatIsNotInTheMailOrNotACodeIsDropped(t *testing.T) {
	for name, reply := range map[string]string{
		"invented":        `{"code":"271828"}`,
		"inside a digit":  `{"code":"3141"}`,
		"the wrong shape": `{"code":"abc12"}`,
		"too long":        `{"code":"31415926535"}`,
		"not json":        `the code is 314159`,
		"null":            `{"code":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			received := time.Now().UTC()
			fixture := newMailFixture(t, []provider.MailMessage{
				codeMailWithNoCue("<drop-"+uuid.NewString()+"@mail.example.invalid>", received),
			})
			withModel(fixture.mailbox, &scriptedModel{reply: reply})
			closeWait := openCodeWait(codeWait{
				spaceID: fixture.space, owner: codeOwner{biller: domain.BillerSpectrum}, name: "Spectrum",
				senders: billmail.CodeSendersFor(domain.BillerSpectrum), since: received,
			})
			defer closeWait()

			result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
			require.NoError(t, err)
			require.Zero(t, result.OTPs)
			_, found := takeCode(fixture.space, domain.BillerSpectrum, received.Add(-time.Minute), false)
			require.False(t, found)
		})
	}
}

func TestTheModelIsNotAskedWhenNobodyIsWaitingOrItFails(t *testing.T) {
	received := time.Now().UTC()
	fixture := newMailFixture(t, []provider.MailMessage{
		codeMailWithNoCue("<nobody-"+uuid.NewString()+"@mail.example.invalid>", received),
	})
	model := &scriptedModel{reply: `{"code":"314159"}`}
	withModel(fixture.mailbox, model)

	result, err := fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Zero(t, result.OTPs)
	require.Zero(t, model.calls(), "no sign-in waiting, no question")

	// A model that is down is the digit reader's answer: nothing.
	fixture = newMailFixture(t, []provider.MailMessage{
		codeMailWithNoCue("<down-"+uuid.NewString()+"@mail.example.invalid>", received),
	})
	withModel(fixture.mailbox, &scriptedModel{err: errors.New("connection refused")})
	closeWait := openCodeWait(codeWait{
		spaceID: fixture.space, owner: codeOwner{biller: domain.BillerSpectrum}, name: "Spectrum",
		senders: billmail.CodeSendersFor(domain.BillerSpectrum), since: received,
	})
	defer closeWait()
	result, err = fixture.mailbox.Poll(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Zero(t, result.OTPs)
}

func TestAMerchantsMailedCodeIsFoundWhileItsSignInWaits(t *testing.T) {
	since := time.Now().UTC()
	fixture := newMailFixture(t, []provider.MailMessage{{
		ID:     "<amazon-" + uuid.NewString() + "@mail.example.invalid>",
		Sender: "account-update@amazon.com", Subject: "Your Amazon sign-in", ReceivedAt: since.Add(5 * time.Second),
		Text: "To sign in, enter 482913. Do not share it.",
	}})
	fixture.mailbox.OTPWait = time.Millisecond
	withModel(fixture.mailbox, &scriptedModel{reply: `{"code":"482913"}`})

	code, found := fixture.mailbox.WaitForMerchantCode(t.Context(), fixture.space, domain.MerchantAmazon, since)
	require.True(t, found)
	require.Equal(t, "482913", code)

	log, err := db(t).ListBillEmails(t.Context(), fixture.space, fixture.connection.ID, 5)
	require.NoError(t, err)
	require.Equal(t, store.EmailOutcomeOTP, log[0].Outcome)
	require.Contains(t, log[0].Note, "Amazon")
	require.NotContains(t, log[0].Note, "482913")
}

func TestAMerchantCodeThatNeverArrivesIsNotInvented(t *testing.T) {
	since := time.Now().UTC()
	fixture := newMailFixture(t)
	fixture.mailbox.OTPWait = time.Millisecond
	started := time.Now()

	_, found := fixture.mailbox.WaitForMerchantCode(t.Context(), fixture.space, domain.MerchantCostco, since)
	require.False(t, found)
	require.Less(t, time.Since(started), 10*time.Second, "the wait ends at its window")
}

// mailedBridge is a bill connection and a mailbox in one space, with the
// mailbox answering the connection's challenges the way the application
// wires it.
func mailedBridge(t *testing.T, messages []provider.MailMessage, model MailModel) bridgeFixture {
	t.Helper()
	fixture := newBridgeFixture(t, domain.BillerSpectrum)
	connection := &store.EmailConnection{
		Label: "the bills mailbox", Kind: store.EmailKindIMAP,
		Address: "bills@example.invalid", Host: "imap.example.invalid", Port: 993,
		Username: "bills@example.invalid", Folder: "Inbox", Enabled: true,
	}
	require.NoError(t, fixture.bills.store.CreateEmailConnection(t.Context(), fixture.space, connection))
	require.NoError(t, fixture.bills.store.SaveEmailSecret(
		t.Context(), fixture.space, connection.ID, "abcd efgh ijkl mnop"))

	mailbox := NewMailbox(fixture.bills.store)
	mailbox.Bills = fixture.bills
	mailbox.OTPWait = time.Millisecond
	mailbox.Open = func(store.EmailConnection, string) (provider.Mailbox, error) {
		return &stubMailbox{batches: [][]provider.MailMessage{messages}}, nil
	}
	if model != nil {
		withModel(mailbox, model)
	}
	fixture.bills.Answerers = []BillChallengeAnswerer{mailbox.AnswerChallenge}
	fixture.agent.answers = []map[string]any{{
		"provider": "spectrum",
		"challenge": map[string]any{
			"session_id": "park-mail", "state": "otp", "method": "email",
			"prompt": "Enter the code we emailed you", "image": "",
		},
		"notes": []string{},
	}}
	fixture.agent.resume = okPull(`{"token":"rolled-two"}`,
		pulledStatement("stmt-9", "2026-10-20", "71.00"))
	return fixture
}

func TestAnUnattendedSignInFinishedByAMailedCodeIsNotPaused(t *testing.T) {
	fixture := mailedBridge(t, []provider.MailMessage{
		codeMailWithNoCue("<bridge-"+uuid.NewString()+"@mail.example.invalid>", time.Now().UTC().Add(time.Second)),
	}, &scriptedModel{reply: `{"code":"314159"}`})

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, result.Status)
	fixture.agent.mu.Lock()
	require.Equal(t, []string{"314159"}, fixture.agent.codes)
	fixture.agent.mu.Unlock()

	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Empty(t, row.SignInPausedFor)
	require.Empty(t, fixture.push.sent, "nobody was interrupted")
}

func TestAnUnattendedSignInWithNoCodeInTheMailboxPausesAsCodeNeeded(t *testing.T) {
	fixture := mailedBridge(t, nil, nil)

	result, err := fixture.bills.Pull(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, store.BillPullChallenge, result.Status)
	fixture.agent.mu.Lock()
	require.Empty(t, fixture.agent.codes)
	fixture.agent.mu.Unlock()

	row, err := fixture.bills.store.GetBillConnection(t.Context(), fixture.space, fixture.connection.ID)
	require.NoError(t, err)
	require.Equal(t, provider.SignInPausedCodeNeeded, row.SignInPausedFor)
}

// A login whose household said its codes come by e-mail is answered from the
// mailbox at any provider — here one the catalogue expects no code from and
// whose code sender nobody has written down — by a code mailed from its own
// domain, a host under it included. Every value is invented.
func TestALoginWhoseCodesComeByEmailIsAnsweredFromItsOwnDomain(t *testing.T) {
	received := time.Now().UTC().Add(time.Second)
	fixture := mailedBridge(t, []provider.MailMessage{{
		ID: "<tg-" + uuid.NewString() + "@mail.example.invalid>", Sender: "noreply@email.trugreen.com",
		Subject: "Your sign-in code", ReceivedAt: received,
		Text: "Your verification code is 604183. It expires in ten minutes.",
	}}, nil)
	quiet := &store.BillConnection{
		Biller: domain.BillerTruGreen, Label: "the lawn", CredentialSource: store.BillCredentialSession,
		AutopayRule: domain.AutopayNone, PullEnabled: true,
	}
	require.NoError(t, fixture.bills.store.CreateBillConnection(t.Context(), fixture.space, quiet))
	require.NoError(t, fixture.bills.store.SaveBillConnectionSession(
		t.Context(), fixture.space, quiet.ID, fakeBillSession, "profile-tg", true))
	require.NoError(t, fixture.bills.store.SetBillConnectionSecondFactor(
		t.Context(), fixture.space, quiet.ID, domain.SecondFactorEmail))
	fixture.agent.answers[0]["provider"] = "trugreen"

	result, err := fixture.bills.Pull(t.Context(), fixture.space, quiet.ID)

	require.NoError(t, err)
	require.Equal(t, store.BillPullOK, result.Status)
	fixture.agent.mu.Lock()
	require.Equal(t, []string{"604183"}, fixture.agent.codes)
	fixture.agent.mu.Unlock()
}

func TestAMailFromAProvidersDomainIsFromItAndALookalikeIsNot(t *testing.T) {
	require.Equal(t, "trugreen.com", homeDomain("https://www.trugreen.com"))
	require.Equal(t, "secure.example.test", homeDomain("https://secure.example.test/login?x=1"))
	domains := []string{"example.com"}
	require.True(t, billmail.SentFromDomain(billmail.Message{Sender: "noreply@example.com"}, domains))
	require.True(t, billmail.SentFromDomain(billmail.Message{Sender: "NoReply@Mail.Example.com"}, domains))
	require.False(t, billmail.SentFromDomain(billmail.Message{Sender: "noreply@example.com.lookalike.test"}, domains))
	require.False(t, billmail.SentFromDomain(billmail.Message{Sender: "example.com@example.invalid"}, domains))
}

func TestAPortalPerCustomerWaitsForMailFromThatCustomersDomain(t *testing.T) {
	connection := store.BillConnection{
		Biller: domain.BillerMyChart, SecondFactor: domain.SecondFactorEmail,
		Site: "https://mychart.examplehealth.example/MyChart",
	}
	wait := billCodeWait(store.SpaceID{}, connection, time.Time{})
	require.Equal(t, []string{"examplehealth.example"}, wait.domains)

	connection.Site = "https://examplehealth.example/MyChart"
	require.Equal(t, []string{"examplehealth.example"}, billCodeWait(store.SpaceID{}, connection, time.Time{}).domains)
}
