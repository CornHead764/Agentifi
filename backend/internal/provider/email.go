package provider

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"net/smtp"
	"strings"
	"time"
)

// Alert mail over SMTP. Nothing retries: a failed alert is still in the in-app
// feed, and a retry queue would eventually send a stale warning.

type Email struct {
	To      string
	Subject string
	Body    string
}

type Transport interface {
	Send(ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte) error
}

type Mailer struct {
	Host      string
	Port      int
	Username  string
	Password  string
	From      string
	StartTLS  bool
	Transport Transport
	Now       func() time.Time
}

// ErrEmailNotConfigured means the channel is off, not failed.
var ErrEmailNotConfigured = errors.New("provider: no SMTP host is configured")

func (m *Mailer) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Mailer) transport() Transport {
	if m.Transport != nil {
		return m.Transport
	}
	return smtpTransport{startTLS: m.StartTLS}
}

// Sender falls back to the username when it is an address: relays commonly
// refuse a mismatched From.
func (m *Mailer) Sender() string {
	if from := strings.TrimSpace(m.From); from != "" {
		return from
	}
	if strings.Contains(m.Username, "@") {
		return strings.TrimSpace(m.Username)
	}
	return ""
}

func (m *Mailer) Send(ctx context.Context, email Email) error {
	if strings.TrimSpace(m.Host) == "" {
		return ErrEmailNotConfigured
	}
	to := strings.TrimSpace(email.To)
	if to == "" || !strings.Contains(to, "@") {
		return fmt.Errorf("provider: %q is not an address to send to", email.To)
	}
	from := m.Sender()
	if from == "" {
		return errors.New("provider: SMTP_FROM is needed when the username is not an address")
	}

	port := m.Port
	if port == 0 {
		port = 587
	}
	var auth smtp.Auth
	if m.Username != "" && m.Password != "" {
		// An empty AUTH makes a relay that wants none refuse the message.
		auth = smtp.PlainAuth("", m.Username, m.Password, m.Host)
	}

	addr := net.JoinHostPort(m.Host, fmt.Sprint(port))
	return m.transport().Send(ctx, addr, auth, from, []string{to}, m.message(from, email))
}

// message dot-stuffs the body: a line of "." would end the DATA command.
func (m *Mailer) message(from string, email Email) []byte {
	var b strings.Builder
	fmt.Fprintf(&b, "From: %s\r\n", from)
	fmt.Fprintf(&b, "To: %s\r\n", email.To)
	fmt.Fprintf(&b, "Subject: %s\r\n", headerValue(email.Subject))
	fmt.Fprintf(&b, "Date: %s\r\n", m.now().Format(time.RFC1123Z))
	b.WriteString("MIME-Version: 1.0\r\n")
	b.WriteString("Content-Type: text/plain; charset=utf-8\r\n")
	b.WriteString("\r\n")

	for _, line := range strings.Split(strings.ReplaceAll(email.Body, "\r\n", "\n"), "\n") {
		if strings.HasPrefix(line, ".") {
			line = "." + line
		}
		b.WriteString(line)
		b.WriteString("\r\n")
	}
	return []byte(b.String())
}

// headerValue keeps a header on one line: subjects carry bank-chosen payees,
// and a newline would be header injection.
func headerValue(value string) string {
	return strings.Join(strings.Fields(value), " ")
}

type smtpTransport struct{ startTLS bool }

func (t smtpTransport) Send(
	ctx context.Context, addr string, auth smtp.Auth, from string, to []string, msg []byte,
) error {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return fmt.Errorf("provider: %s is not a host and port: %w", addr, err)
	}

	dialer := &net.Dialer{Timeout: 15 * time.Second}
	var conn net.Conn
	if port == "465" {
		// Implicit TLS; STARTTLS on 465 is a protocol error.
		conn, err = tls.DialWithDialer(dialer, "tcp", addr, &tls.Config{ServerName: host})
	} else {
		conn, err = dialer.DialContext(ctx, "tcp", addr)
	}
	if err != nil {
		return fmt.Errorf("provider: cannot reach the mail server at %s: %w", addr, err)
	}

	client, err := smtp.NewClient(conn, host)
	if err != nil {
		conn.Close()
		return fmt.Errorf("provider: %s did not answer as a mail server: %w", addr, err)
	}
	defer client.Close()

	if port != "465" && t.startTLS {
		// Required, not advisory: a stripped advertisement must not send in
		// cleartext.
		if ok, _ := client.Extension("STARTTLS"); !ok {
			conn.Close()
			client.Close()
			return fmt.Errorf("provider: %s does not offer STARTTLS, which this configuration requires", addr)
		}
		if err := client.StartTLS(&tls.Config{ServerName: host}); err != nil {
			return fmt.Errorf("provider: STARTTLS refused by %s: %w", addr, err)
		}
	}
	if auth != nil {
		if err := client.Auth(auth); err != nil {
			return fmt.Errorf("provider: the mail server refused the credentials: %w", err)
		}
	}
	if err := client.Mail(from); err != nil {
		return fmt.Errorf("provider: sender %s refused: %w", from, err)
	}
	for _, one := range to {
		if err := client.Rcpt(one); err != nil {
			return fmt.Errorf("provider: recipient %s refused: %w", one, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("provider: the mail server refused the message: %w", err)
	}
	if _, err := writer.Write(msg); err != nil {
		return fmt.Errorf("provider: writing the message: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("provider: the mail server rejected the message: %w", err)
	}
	return client.Quit()
}
