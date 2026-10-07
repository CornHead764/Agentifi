package provider_test

import (
	"context"
	"net/smtp"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
)

type recorded struct {
	addr string
	auth smtp.Auth
	from string
	to   []string
	msg  string
	err  error
}

func (r *recorded) Send(
	_ context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte,
) error {
	r.addr, r.auth, r.from, r.to, r.msg = addr, auth, from, to, string(msg)
	return r.err
}

func mailer(sent *recorded) *provider.Mailer {
	return &provider.Mailer{
		Host: "mail.example.test", Port: 587,
		Username: "agentifi@example.test", Password: "secret",
		From: "agentifi@example.test", StartTLS: true, Transport: sent,
		Now: func() time.Time { return time.Date(2026, time.August, 22, 9, 0, 0, 0, time.UTC) },
	}
}

func TestAMessageCarriesTheHeadersAMailServerWants(t *testing.T) {
	sent := &recorded{}
	require.NoError(t, mailer(sent).Send(t.Context(), provider.Email{
		To: "alex@example.test", Subject: "Alex's Visa is at $6,543.21",
		Body: "Above the $5,000.00 you asked to be told about.",
	}))

	require.Equal(t, "mail.example.test:587", sent.addr)
	require.Equal(t, "agentifi@example.test", sent.from)
	require.Equal(t, []string{"alex@example.test"}, sent.to)
	require.Contains(t, sent.msg, "From: agentifi@example.test\r\n")
	require.Contains(t, sent.msg, "To: alex@example.test\r\n")
	require.Contains(t, sent.msg, "Subject: Alex's Visa is at $6,543.21\r\n")
	require.Contains(t, sent.msg, "Content-Type: text/plain; charset=utf-8\r\n")
	require.Contains(t, sent.msg, "\r\n\r\nAbove the $5,000.00 you asked to be told about.\r\n")
}

func TestANewlineInASubjectCannotInjectAHeader(t *testing.T) {
	// Subjects carry bank-chosen payees; a newline would inject a header.
	sent := &recorded{}
	require.NoError(t, mailer(sent).Send(t.Context(), provider.Email{
		To:      "alex@example.test",
		Subject: "Payment\r\nBcc: somebody@elsewhere.test",
		Body:    "body",
	}))
	require.Contains(t, sent.msg, "Subject: Payment Bcc: somebody@elsewhere.test\r\n")
	require.NotContains(t, sent.msg, "\r\nBcc:")
}

func TestALoneDotInTheBodyDoesNotTruncateTheMessage(t *testing.T) {
	// A body line of "." ends the DATA command.
	sent := &recorded{}
	require.NoError(t, mailer(sent).Send(t.Context(), provider.Email{
		To: "alex@example.test", Subject: "s", Body: "first\n.\nlast",
	}))
	require.Contains(t, sent.msg, "first\r\n..\r\nlast\r\n")
}

func TestNoRelayIsOffRatherThanBroken(t *testing.T) {
	m := &provider.Mailer{}
	err := m.Send(t.Context(), provider.Email{To: "alex@example.test", Subject: "s", Body: "b"})
	require.ErrorIs(t, err, provider.ErrEmailNotConfigured)
}

func TestAnAddressThatIsNotOneIsRefusedBeforeDialling(t *testing.T) {
	sent := &recorded{}
	for _, to := range []string{"", "   ", "not-an-address"} {
		require.Error(t, mailer(sent).Send(t.Context(),
			provider.Email{To: to, Subject: "s", Body: "b"}))
	}
	require.Empty(t, sent.addr, "nothing was dialled")
}

func TestAuthIsOmittedWhenTheRelayWantsNone(t *testing.T) {
	// An empty AUTH makes a relay that wants none refuse the message.
	sent := &recorded{}
	m := mailer(sent)
	m.Username, m.Password = "", ""
	m.From = "agentifi@example.test"
	require.NoError(t, m.Send(t.Context(),
		provider.Email{To: "alex@example.test", Subject: "s", Body: "b"}))
	require.Nil(t, sent.auth)
}

func TestTheSenderFallsBackToTheUsernameWhenItIsAnAddress(t *testing.T) {
	m := &provider.Mailer{Host: "h", Username: "agentifi@example.test"}
	require.Equal(t, "agentifi@example.test", m.Sender())

	m = &provider.Mailer{Host: "h", Username: "agentifi"}
	require.Empty(t, m.Sender(), "a bare username is not an address to send from")
}

func TestASenderThatCannotBeWorkedOutIsAnErrorNotAGuess(t *testing.T) {
	sent := &recorded{}
	m := mailer(sent)
	m.From, m.Username = "", "agentifi"
	require.Error(t, m.Send(t.Context(),
		provider.Email{To: "alex@example.test", Subject: "s", Body: "b"}))
	require.Empty(t, sent.addr)
}

func TestTheBodySurvivesWindowsLineEndings(t *testing.T) {
	sent := &recorded{}
	require.NoError(t, mailer(sent).Send(t.Context(), provider.Email{
		To: "alex@example.test", Subject: "s", Body: "one\r\ntwo",
	}))
	require.Equal(t, 1, strings.Count(sent.msg, "one\r\ntwo\r\n"))
}
