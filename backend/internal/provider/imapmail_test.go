package provider

import (
	"encoding/base64"
	"encoding/json"
	"testing"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/stretchr/testify/require"
)

func TestIMAPMailboxReadsOnlyNewUIDs(t *testing.T) {
	now := time.Date(2026, 9, 18, 9, 0, 0, 0, time.UTC)
	lookback := 30 * 24 * time.Hour

	// A first read has no numbering to stand on, so it asks by date.
	criteria, stand := imapSearchFrom(imapCursor{}, 412, lookback, now)
	require.Empty(t, criteria.UID)
	require.Equal(t, now.Add(-lookback), criteria.Since)
	require.Equal(t, imapCursor{UIDValidity: 412}, stand)

	// A cursor that agrees with the folder asks for everything above the last
	// UID it read, and for nothing by date.
	criteria, stand = imapSearchFrom(imapCursor{UIDValidity: 412, LastUID: 1809}, 412, lookback, now)
	require.True(t, criteria.Since.IsZero(), "the UID floor is the whole question")
	require.Equal(t, []imap.UIDSet{{imap.UIDRange{Start: 1810, Stop: 0}}}, criteria.UID)
	require.Equal(t, imapCursor{UIDValidity: 412, LastUID: 1809}, stand)

	// A renumbered folder starts over rather than trusting the old floor.
	criteria, stand = imapSearchFrom(imapCursor{UIDValidity: 412, LastUID: 1809}, 9001, lookback, now)
	require.Empty(t, criteria.UID)
	require.Equal(t, now.Add(-lookback), criteria.Since)
	require.Equal(t, imapCursor{UIDValidity: 9001}, stand)

	require.Equal(t, imapCursor{}, readIMAPCursor(json.RawMessage(`{"uidvalidity":`)))
	require.Equal(t, imapCursor{UIDValidity: 412, LastUID: 1809},
		readIMAPCursor(json.RawMessage(`{"uidvalidity":412,"last_uid":1809}`)))
}

func TestIMAPMailboxTakesTheBodiesAndThePDFAndNothingElse(t *testing.T) {
	structure := &imap.BodyStructureMultiPart{
		Subtype: "mixed",
		Children: []imap.BodyStructure{
			&imap.BodyStructureMultiPart{
				Subtype: "alternative",
				Children: []imap.BodyStructure{
					&imap.BodyStructureSinglePart{
						Type: "text", Subtype: "plain", Encoding: "quoted-printable",
						Params: map[string]string{"charset": "utf-8"}, Size: 40,
					},
					&imap.BodyStructureSinglePart{
						Type: "text", Subtype: "html", Encoding: "base64",
						Params: map[string]string{"charset": "utf-8"}, Size: 60,
					},
				},
			},
			&imap.BodyStructureSinglePart{
				Type: "application", Subtype: "pdf", Encoding: "base64", Size: 26,
				Params: map[string]string{"name": "statement.pdf"},
			},
			// A logo is not a statement.
			&imap.BodyStructureSinglePart{
				Type: "image", Subtype: "png", Encoding: "base64", Size: 4096,
				Params: map[string]string{"name": "logo.png"},
			},
		},
	}

	parts := wantedIMAPParts(structure)
	require.Len(t, parts, 3)
	require.Equal(t, imapPartText, parts[0].Kind)
	require.Equal(t, "1.1", imapPartKey(parts[0].Path))
	require.Equal(t, imapPartHTML, parts[1].Kind)
	require.Equal(t, imapPartPDF, parts[2].Kind)
	require.Equal(t, "statement.pdf", parts[2].Filename)

	const pdf = "%PDF-1.4 invented statement"
	bodies := map[string][]byte{
		"1.1": []byte("Amount due: $42.00=0D=0A"),
		"1.2": []byte(base64.StdEncoding.EncodeToString([]byte("<p>Amount due</p>"))),
		"2":   []byte(base64.StdEncoding.EncodeToString([]byte(pdf))),
	}
	envelope := &imap.Envelope{
		Date:      time.Date(2026, 9, 14, 12, 0, 0, 0, time.UTC),
		Subject:   "Your statement is ready",
		MessageID: "<b71f0c2a-4ee1@mail.example.invalid>",
		From: []imap.Address{{
			Name: "Example Utility", Mailbox: "Billing", Host: "Example.Invalid",
		}},
	}

	message := assembleIMAPMessage(envelope, parts, bodies)
	require.Equal(t, "<b71f0c2a-4ee1@mail.example.invalid>", message.ID)
	require.Equal(t, "billing@example.invalid", message.Sender)
	require.Equal(t, "Amount due: $42.00\r\n", message.Text)
	require.Equal(t, "<p>Amount due</p>", message.HTML)
	require.Len(t, message.Attachments, 1)
	require.Equal(t, []byte(pdf), message.Attachments[0].Bytes)
	require.Equal(t, MailAttachmentContentType, message.Attachments[0].ContentType)
}

func TestAPartThatWillNotDecodeComesBackAsItArrived(t *testing.T) {
	require.Equal(t, []byte("not base64 at all!"),
		decodeIMAPPart("base64", []byte("not base64 at all!")))
	require.Equal(t, []byte("plain"), decodeIMAPPart("7bit", []byte("plain")))
}
