package provider

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"mime/quotedprintable"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
)

// IMAP with a password (Gmail app passwords read a label as a folder). The
// cursor is the folder's UIDVALIDITY and the last UID read; a changed
// UIDVALIDITY means a renumbered folder, so the read starts over from the
// lookback rather than trusting the old UID as a floor.

type IMAPMailbox struct {
	Host     string
	Port     int
	Username string
	Password string
	Folder   string

	// nil means imapclient.DialTLS.
	Dial func(address string, options *imapclient.Options) (*imapclient.Client, error)
}

type imapCursor struct {
	UIDValidity uint32 `json:"uidvalidity"`
	LastUID     uint32 `json:"last_uid"`
}

func (m *IMAPMailbox) address() string {
	port := m.Port
	if port == 0 {
		port = 993
	}
	return m.Host + ":" + strconv.Itoa(port)
}

func (m *IMAPMailbox) folder() string {
	if strings.TrimSpace(m.Folder) == "" {
		return "INBOX"
	}
	return m.Folder
}

const imapSessionTimeout = 5 * time.Minute

// dial closes the connection when ctx ends (or after imapSessionTimeout): a
// half-open socket otherwise blocks a read forever.
func (m *IMAPMailbox) dial(ctx context.Context) (*imapclient.Client, func(), error) {
	dial := m.Dial
	if dial == nil {
		dial = imapclient.DialTLS
	}
	client, err := dial(m.address(), &imapclient.Options{
		Dialer: &net.Dialer{Timeout: 30 * time.Second},
	})
	if err != nil {
		return nil, nil, fmt.Errorf("provider: connecting to %s: %w", m.Host, err)
	}
	limited, cancel := context.WithTimeout(ctx, imapSessionTimeout)
	stop := context.AfterFunc(limited, func() { _ = client.Close() })
	done := func() {
		stop()
		cancel()
		_ = client.Close()
	}
	if err := client.Login(m.Username, m.Password).Wait(); err != nil {
		done()
		return nil, nil, fmt.Errorf("provider: %s refused the sign-in: %w", m.Host, err)
	}
	return client, done, nil
}

// Verify signs in and opens the folder, so a bad password is refused before
// it is sealed.
func (m *IMAPMailbox) Verify(ctx context.Context) error {
	client, done, err := m.dial(ctx)
	if err != nil {
		return err
	}
	defer done()
	if _, err := client.Select(m.folder(), nil).Wait(); err != nil {
		return fmt.Errorf("provider: %s has no folder called %q: %w", m.Host, m.folder(), err)
	}
	return client.Logout().Wait()
}

func (m *IMAPMailbox) ListNew(
	ctx context.Context, cursor json.RawMessage, lookback time.Duration,
) ([]MailMessage, json.RawMessage, error) {
	client, done, err := m.dial(ctx)
	if err != nil {
		return nil, cursor, err
	}
	defer done()

	selected, err := client.Select(m.folder(), nil).Wait()
	if err != nil {
		return nil, cursor, fmt.Errorf(
			"provider: %s has no folder called %q: %w", m.Host, m.folder(), err)
	}

	stand := readIMAPCursor(cursor)
	criteria, stand := imapSearchFrom(stand, selected.UIDValidity, lookback, time.Now().UTC())

	found, err := client.UIDSearch(criteria, nil).Wait()
	if err != nil {
		return nil, imapCursorOf(stand), fmt.Errorf("provider: searching %s: %w", m.folder(), err)
	}
	uids := found.AllUIDs()
	if len(uids) == 0 {
		return nil, imapCursorOf(stand), nil
	}

	buffers, err := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{
		UID: true, Envelope: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return nil, imapCursorOf(stand), fmt.Errorf("provider: reading %s: %w", m.folder(), err)
	}

	var out []MailMessage
	for _, buffer := range buffers {
		if buffer.Envelope == nil {
			continue
		}
		message, err := m.assemble(client, buffer)
		if err != nil {
			return out, imapCursorOf(stand), err
		}
		out = append(out, message)
		if uint32(buffer.UID) > stand.LastUID {
			stand.LastUID = uint32(buffer.UID)
		}
	}
	sortByReceived(out)
	return out, imapCursorOf(stand), nil
}

// Fetch finds one message by its Message-ID header without touching the
// cursor.
func (m *IMAPMailbox) Fetch(ctx context.Context, messageID string) (MailMessage, bool, error) {
	client, done, err := m.dial(ctx)
	if err != nil {
		return MailMessage{}, false, err
	}
	defer done()

	if _, err := client.Select(m.folder(), nil).Wait(); err != nil {
		return MailMessage{}, false, fmt.Errorf(
			"provider: %s has no folder called %q: %w", m.Host, m.folder(), err)
	}
	found, err := client.UIDSearch(&imap.SearchCriteria{
		Header: []imap.SearchCriteriaHeaderField{{Key: "Message-Id", Value: messageID}},
	}, nil).Wait()
	if err != nil {
		return MailMessage{}, false, fmt.Errorf("provider: searching %s: %w", m.folder(), err)
	}
	uids := found.AllUIDs()
	if len(uids) == 0 {
		return MailMessage{}, false, nil
	}

	buffers, err := client.Fetch(imap.UIDSetNum(uids[len(uids)-1]), &imap.FetchOptions{
		UID: true, Envelope: true, BodyStructure: &imap.FetchItemBodyStructure{Extended: true},
	}).Collect()
	if err != nil {
		return MailMessage{}, false, fmt.Errorf("provider: reading %s: %w", m.folder(), err)
	}
	for _, buffer := range buffers {
		if buffer.Envelope == nil {
			continue
		}
		message, err := m.assemble(client, buffer)
		if err != nil {
			return MailMessage{}, false, err
		}
		return message, true, nil
	}
	return MailMessage{}, false, nil
}

func (m *IMAPMailbox) assemble(
	client *imapclient.Client, buffer *imapclient.FetchMessageBuffer,
) (MailMessage, error) {
	parts := wantedIMAPParts(buffer.BodyStructure)
	bodies, err := m.fetchParts(client, buffer.UID, parts)
	if err != nil {
		return MailMessage{}, err
	}
	message := assembleIMAPMessage(buffer.Envelope, parts, bodies)
	message.ProviderID = strconv.FormatUint(uint64(buffer.UID), 10)
	return message, nil
}

func (m *IMAPMailbox) fetchParts(
	client *imapclient.Client, uid imap.UID, parts []imapPart,
) (map[string][]byte, error) {
	if len(parts) == 0 {
		return nil, nil
	}
	options := &imap.FetchOptions{}
	for _, part := range parts {
		options.BodySection = append(options.BodySection,
			&imap.FetchItemBodySection{Part: part.Path})
	}
	buffers, err := client.Fetch(imap.UIDSetNum(uid), options).Collect()
	if err != nil {
		return nil, fmt.Errorf("provider: reading a message's parts: %w", err)
	}
	out := map[string][]byte{}
	for _, buffer := range buffers {
		for _, section := range buffer.BodySection {
			out[imapPartKey(section.Section.Part)] = section.Bytes
		}
	}
	return out, nil
}

type imapPart struct {
	Path     []int
	Kind     string
	Encoding string
	Charset  string
	Filename string
}

const (
	imapPartText = "text"
	imapPartHTML = "html"
	imapPartPDF  = "pdf"
)

func imapPartKey(path []int) string {
	parts := make([]string, 0, len(path))
	for _, one := range path {
		parts = append(parts, strconv.Itoa(one))
	}
	return strings.Join(parts, ".")
}

// readIMAPCursor answers a zero cursor for anything unreadable: a re-read
// from the lookback rather than a missed bill.
func readIMAPCursor(cursor json.RawMessage) imapCursor {
	var stand imapCursor
	if len(cursor) == 0 {
		return imapCursor{}
	}
	if err := json.Unmarshal(cursor, &stand); err != nil {
		return imapCursor{}
	}
	return stand
}

func imapCursorOf(stand imapCursor) json.RawMessage {
	raw, err := json.Marshal(stand)
	if err != nil {
		return nil
	}
	return raw
}

// imapSearchFrom asks for everything above the last UID when the cursor's
// UIDVALIDITY matches, and otherwise for the lookback by date under the
// folder's current numbering.
func imapSearchFrom(
	stand imapCursor, uidValidity uint32, lookback time.Duration, now time.Time,
) (*imap.SearchCriteria, imapCursor) {
	if stand.UIDValidity == uidValidity && stand.LastUID > 0 {
		return &imap.SearchCriteria{
			UID: []imap.UIDSet{{imap.UIDRange{Start: imap.UID(stand.LastUID + 1), Stop: 0}}},
		}, stand
	}
	return &imap.SearchCriteria{Since: now.Add(-lookback)},
		imapCursor{UIDValidity: uidValidity}
}

// wantedIMAPParts names the text and HTML bodies and any PDF small enough to be
// a statement.
func wantedIMAPParts(structure imap.BodyStructure) []imapPart {
	if structure == nil {
		return nil
	}
	var out []imapPart
	structure.Walk(func(path []int, part imap.BodyStructure) bool {
		single, ok := part.(*imap.BodyStructureSinglePart)
		if !ok {
			return true
		}
		media := single.MediaType()
		switch {
		case media == "text/plain" && !isIMAPAttachment(single):
			out = append(out, imapPart{
				Path: path, Kind: imapPartText,
				Encoding: single.Encoding, Charset: single.Params["charset"],
			})
		case media == "text/html" && !isIMAPAttachment(single):
			out = append(out, imapPart{
				Path: path, Kind: imapPartHTML,
				Encoding: single.Encoding, Charset: single.Params["charset"],
			})
		case wantedAttachment(media, int(single.Size)):
			out = append(out, imapPart{
				Path: path, Kind: imapPartPDF,
				Encoding: single.Encoding, Filename: single.Filename(),
			})
		}
		return true
	})
	return out
}

func isIMAPAttachment(part *imap.BodyStructureSinglePart) bool {
	disposition := part.Disposition()
	return disposition != nil && strings.EqualFold(disposition.Value, "attachment")
}

func assembleIMAPMessage(
	envelope *imap.Envelope, parts []imapPart, bodies map[string][]byte,
) MailMessage {
	message := MailMessage{
		ID:         envelope.MessageID,
		Subject:    envelope.Subject,
		ReceivedAt: envelope.Date.UTC(),
	}
	if len(envelope.From) > 0 {
		message.Sender = senderAddress(envelope.From[0].Addr())
	}
	for _, part := range parts {
		raw, held := bodies[imapPartKey(part.Path)]
		if !held {
			continue
		}
		decoded := decodeIMAPPart(part.Encoding, raw)
		switch part.Kind {
		case imapPartText:
			if message.Text == "" {
				message.Text = string(decoded)
			}
		case imapPartHTML:
			if message.HTML == "" {
				message.HTML = string(decoded)
			}
		case imapPartPDF:
			if len(decoded) > MaxMailAttachmentBytes {
				continue
			}
			message.Attachments = append(message.Attachments, MailAttachment{
				Filename: part.Filename, ContentType: MailAttachmentContentType, Bytes: decoded,
			})
		}
	}
	return message
}

// decodeIMAPPart returns a part that will not decode as it arrived rather than
// as nothing, so the message reads as unrecognised rather than empty.
func decodeIMAPPart(encoding string, raw []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(
			strings.Join(strings.Fields(string(raw)), ""))
		if err != nil {
			return raw
		}
		return decoded
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(strings.NewReader(string(raw))))
		if err != nil {
			return raw
		}
		return decoded
	default:
		return raw
	}
}
