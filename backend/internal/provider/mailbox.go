package provider

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// The Graph and IMAP readers share two rules. A cursor is opaque above the
// provider and stored as it comes back; an empty one starts at the lookback,
// not the beginning of time. Only small PDFs are fetched: other attachments on
// a billing mail are logos.

const MaxMailAttachmentBytes = 15 << 20

const MailAttachmentContentType = "application/pdf"

type MailMessage struct {
	// ID is the internet message-id, angle brackets and all, so the same mail
	// read through Graph and IMAP is one message. It is the bill's external id.
	ID string
	// ProviderID is Graph's message id or IMAP's UID.
	ProviderID string
	// Sender is the bare address, lower-cased.
	Sender     string
	Subject    string
	ReceivedAt time.Time
	// Text is empty for an HTML-only mail; the caller strips HTML once.
	Text        string
	HTML        string
	Attachments []MailAttachment
}

type MailAttachment struct {
	Filename    string
	ContentType string
	Bytes       []byte
}

type Mailbox interface {
	// ListNew answers oldest first, so a poll that stops halfway leaves a
	// cursor that skips nothing unread.
	ListNew(ctx context.Context, cursor json.RawMessage, lookback time.Duration) ([]MailMessage, json.RawMessage, error)

	// Fetch finds a message by internet message-id without consulting or
	// moving the cursor.
	Fetch(ctx context.Context, messageID string) (MailMessage, bool, error)
}

// RotatedSecret is asked after every call and re-sealed: Microsoft's refresh
// tokens roll on use.
type RotatedSecret interface {
	// RotatedSecret is empty when the credential did not change.
	RotatedSecret() string
}

func senderAddress(value string) string {
	value = strings.TrimSpace(value)
	if open := strings.LastIndex(value, "<"); open >= 0 {
		if close := strings.Index(value[open:], ">"); close > 0 {
			value = value[open+1 : open+close]
		}
	}
	return strings.ToLower(strings.TrimSpace(value))
}

func wantedAttachment(contentType string, size int) bool {
	if size > MaxMailAttachmentBytes {
		return false
	}
	kind, _, _ := strings.Cut(strings.ToLower(contentType), ";")
	return strings.TrimSpace(kind) == MailAttachmentContentType
}
