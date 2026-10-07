package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"mime"
	"net/http"
	"path"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// Documents is the one way bytes enter the application. Its rules:
//
//   - The content type is sniffed, not believed: the bytes are served from the
//     app's origin, so HTML claiming to be a PNG would run with the user's
//     session. The sniffed type decides acceptance and is what is stored.
//   - The storage key is built here from the document's uuid, never from a
//     caller's filename.
//   - The same bytes are one document per space, keyed by SHA-256.
//   - The blob is written before the row, so a failure leaves an unreferenced
//     file rather than a row whose bytes 404.
type Documents struct {
	base
	// Storage nil means no file storage is configured; Store refuses.
	Storage provider.StorageProvider
	Log     *slog.Logger
	// Now is nil for the real clock.
	Now func() time.Time
}

func NewDocuments(st *store.Store, storage provider.StorageProvider) *Documents {
	return &Documents{base: newBase(st), Storage: storage}
}

// MaxDocumentBytes bounds one file, because the whole body is read into
// memory. It admits a full-resolution photo from a phone's camera.
const MaxDocumentBytes = 25 << 20

// DocumentPurgeGrace is how long a document nothing links to is kept, so a
// mistaken delete of its transaction is recoverable.
const DocumentPurgeGrace = 7 * 24 * time.Hour

// AllowedDocumentTypes is an allowlist of types that are inert when a browser
// renders them, with the extension each gets. SVG is deliberately absent: it
// can carry script. HEIC and HEIF are what an iPhone's camera saves; most
// browsers cannot draw them, so they are kept and served but get no thumbnail.
var AllowedDocumentTypes = map[string]string{
	"image/jpeg":      ".jpg",
	"image/png":       ".png",
	"image/gif":       ".gif",
	"image/webp":      ".webp",
	"image/heic":      ".heic",
	"image/heif":      ".heif",
	"application/pdf": ".pdf",
}

var ErrNoStorage = errors.New("service: this deployment has no document storage configured")

// ErrUnsupportedDocument is also returned for an empty file.
var ErrUnsupportedDocument = errors.New("service: unsupported document type")

var ErrDocumentTooLarge = errors.New("service: document too large")

type DocumentUpload struct {
	Bytes    []byte
	Filename string
	// Source is one of store.DocumentSource*.
	Source string
	// SourceRef is where a pulled or mailed document came from. Never a
	// credential.
	SourceRef string
	// UploadedByUserID is uuid.Nil for anything no person picked.
	UploadedByUserID uuid.UUID
	// Link is required: a document with no link is an orphan the purge
	// collects.
	Link store.DocumentLink
}

// Store puts one file in the store and links it, and says whether the bytes
// were new.
func (d *Documents) Store(
	ctx context.Context, spaceID store.SpaceID, up DocumentUpload,
) (store.Document, bool, error) {
	if err := d.Check(up); err != nil {
		return store.Document{}, false, err
	}
	contentType, extension, _ := SniffDocument(up.Bytes)

	digest := sha256.Sum256(up.Bytes)
	hash := hex.EncodeToString(digest[:])

	// A lookup rather than an upsert, because the blob is written before the
	// row and not at all for bytes already held. A race lands on the unique
	// index below.
	existing, err := d.store.GetDocumentByContentHash(ctx, spaceID, hash)
	if err == nil {
		return existing, false, d.link(ctx, spaceID, existing.ID, up.Link)
	}
	if !errors.Is(err, store.ErrNotFound) {
		return store.Document{}, false, err
	}

	row := &store.Document{
		ID:               uuid.New(),
		ContentSHA256:    hash,
		ContentType:      contentType,
		SizeBytes:        len(up.Bytes),
		Filename:         DocumentFilename(up.Filename, extension),
		Source:           up.Source,
		SourceRef:        up.SourceRef,
		UploadedByUserID: up.UploadedByUserID,
	}
	row.StorageKey = DocumentStorageKey(spaceID, row.ID, extension)

	if _, err := d.Storage.Upload(ctx, row.StorageKey, up.Bytes, contentType); err != nil {
		return store.Document{}, false, err
	}
	if err := d.store.CreateDocument(ctx, spaceID, row); err != nil {
		// Possibly lost a race on the unique index: drop this blob and use
		// the winner's row.
		_ = d.Storage.Delete(ctx, row.StorageKey)
		if winner, lookupErr := d.store.GetDocumentByContentHash(ctx, spaceID, hash); lookupErr == nil {
			return winner, false, d.link(ctx, spaceID, winner.ID, up.Link)
		}
		return store.Document{}, false, err
	}
	if err := d.link(ctx, spaceID, row.ID, up.Link); err != nil {
		return store.Document{}, false, err
	}
	return *row, true, nil
}

// AttachStatement stores a file as the bill's one statement; a statement it
// had before is unlinked.
func (d *Documents) AttachStatement(
	ctx context.Context, spaceID store.SpaceID, billID uuid.UUID, up DocumentUpload,
) (store.Document, error) {
	up.Link = store.DocumentLink{
		Kind: store.DocumentLinkBill, Role: store.DocumentRoleStatement, TargetID: billID,
	}
	document, _, err := d.Store(ctx, spaceID, up)
	return document, err
}

// BillOfCycle finds the bill of one cycle in what Bills.Ingest answered, which
// is the subaccount's whole record: a bill's due date and invoice are its
// identity.
func BillOfCycle(bills []store.Bill, due domain.Date, invoice string) (store.Bill, bool) {
	want := billCycle{due: due, invoice: invoice}
	for _, one := range bills {
		if cycleOf(one) == want {
			return one, true
		}
	}
	return store.Bill{}, false
}

// Check is every reason Store would refuse a file, asked without storing it,
// for a caller that must validate before creating the link target.
func (d *Documents) Check(up DocumentUpload) error {
	if d.Storage == nil {
		return ErrNoStorage
	}
	if len(up.Bytes) == 0 {
		return fmt.Errorf("%w: the file is empty", ErrUnsupportedDocument)
	}
	if len(up.Bytes) > MaxDocumentBytes {
		return ErrDocumentTooLarge
	}
	if contentType, _, ok := SniffDocument(up.Bytes); !ok {
		return fmt.Errorf("%w: %s", ErrUnsupportedDocument, contentType)
	}
	if !up.Link.Kind.Valid() {
		return fmt.Errorf("service: %q is not a document link kind", string(up.Link.Kind))
	}
	return nil
}

func (d *Documents) link(
	ctx context.Context, spaceID store.SpaceID, documentID uuid.UUID, link store.DocumentLink,
) error {
	link.DocumentID = documentID
	link.SpaceID = spaceID
	return d.store.LinkDocument(ctx, spaceID, link)
}

// Unlink releases one target's hold on a document and reports whether anything
// still holds it. An orphaned document is left for the purge.
func (d *Documents) Unlink(
	ctx context.Context, spaceID store.SpaceID, documentID uuid.UUID,
	kind store.DocumentLinkKind, targetID uuid.UUID,
) (stillLinked bool, err error) {
	if err := d.store.UnlinkDocument(ctx, spaceID, documentID, kind, targetID); err != nil {
		return false, err
	}
	links, err := d.store.ListDocumentLinks(ctx, spaceID, documentID)
	if err != nil {
		return false, err
	}
	if len(links) > 0 {
		return true, nil
	}
	// The purge clock starts when the last link goes.
	return false, d.store.TouchDocument(ctx, spaceID, documentID)
}

// Forget removes a document and its bytes now, with no grace period, for an
// explicit "remove this file".
func (d *Documents) Forget(ctx context.Context, spaceID store.SpaceID, documentID uuid.UUID) error {
	row, err := d.store.GetDocument(ctx, spaceID, documentID)
	if err != nil {
		return err
	}
	// The row first: a blob that fails to delete leaves an orphaned file
	// rather than a document the user removed.
	if err := d.store.DeleteDocument(ctx, spaceID, documentID); err != nil {
		return err
	}
	if d.Storage != nil {
		_ = d.Storage.Delete(ctx, row.StorageKey)
	}
	return nil
}

// Purge deletes every document nothing has linked for longer than the grace
// period, across every space, and reports how many went.
func (d *Documents) Purge(ctx context.Context) (int, error) {
	spaces, err := d.store.ListSpaces(ctx)
	if err != nil {
		return 0, err
	}
	cutoff := d.now().Add(-DocumentPurgeGrace)
	purged := 0
	for _, space := range spaces {
		if ctx.Err() != nil {
			return purged, ctx.Err()
		}
		count, err := d.PurgeSpace(ctx, space.ID, cutoff)
		purged += count
		if err != nil {
			return purged, err
		}
	}
	return purged, nil
}

func (d *Documents) PurgeSpace(
	ctx context.Context, spaceID store.SpaceID, cutoff time.Time,
) (int, error) {
	orphans, err := d.store.UnlinkedDocumentsOlderThan(ctx, spaceID, cutoff)
	if err != nil {
		return 0, err
	}
	purged := 0
	for _, one := range orphans {
		if err := d.store.DeleteDocument(ctx, spaceID, one.ID); err != nil {
			if errors.Is(err, store.ErrNotFound) {
				continue
			}
			return purged, err
		}
		if d.Storage != nil {
			// Logged, not retried: the row holding the key is gone.
			if err := d.Storage.Delete(ctx, one.StorageKey); err != nil {
				d.log().Warn("purged document left its bytes behind",
					"document", one.ID, "storage_key", one.StorageKey, "error", err)
			}
		}
		purged++
	}
	return purged, nil
}

func (d *Documents) now() time.Time {
	if d.Now == nil {
		return time.Now()
	}
	return d.Now()
}

func (d *Documents) log() *slog.Logger {
	if d.Log == nil {
		return slog.Default()
	}
	return d.Log
}

// SniffDocument decides the type from the bytes with the WHATWG algorithm the
// browser also uses, which does not know HEIF, so a HEIF file is recognised by
// its own box header. Parameters such as a charset are stripped.
func SniffDocument(data []byte) (contentType, extension string, ok bool) {
	sniffed := http.DetectContentType(data)
	if base, _, err := mime.ParseMediaType(sniffed); err == nil {
		sniffed = base
	}
	if heif := sniffHEIF(data); heif != "" {
		sniffed = heif
	}
	extension, ok = AllowedDocumentTypes[sniffed]
	return sniffed, extension, ok
}

// sniffHEIF reads the major brand of an ISO base media file's leading `ftyp`
// box: HEIC for the HEVC-coded brands, HEIF for the generic image brands, and
// "" for anything else (a video, an AVIF, not a box at all).
func sniffHEIF(data []byte) string {
	if len(data) < 12 || string(data[4:8]) != "ftyp" {
		return ""
	}
	switch string(data[8:12]) {
	case "heic", "heix", "heim", "heis":
		return "image/heic"
	case "mif1", "msf1":
		return "image/heif"
	}
	return ""
}

// DocumentFilename keeps the uploader's name for display, forces the extension
// to match the bytes, and drops path separators and control characters
// because the name goes into a Content-Disposition header.
func DocumentFilename(raw, extension string) string {
	name := path.Base(strings.ReplaceAll(strings.TrimSpace(raw), `\`, "/"))
	name = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, name)
	name = strings.TrimSuffix(name, path.Ext(name))
	if name == "" || name == "." || name == ".." {
		name = "attachment"
	}
	return textutil.Clip(name, 200) + extension
}

func DocumentStorageKey(spaceID store.SpaceID, id uuid.UUID, extension string) string {
	return path.Join(spaceID.String(), id.String()+extension)
}
