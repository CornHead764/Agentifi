package api

import (
	"bytes"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// Attachments, end to end.
//
// The assertions worth having here are not "a file round-trips". They are the
// ones about a file that lies: a PNG that is really HTML, a filename that is
// really a path, an upload aimed at another household's transaction. Each of
// those is a way the obvious implementation serves somebody else's bytes or
// executes somebody else's script on this origin.

// pngBytes is the smallest thing http.DetectContentType calls an image/png:
// the eight-byte signature is the whole test, and the sniffer reads no further.
func pngBytes() []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte("body")...)
}

// upload posts one multipart file the way the browser's FormData does.
func (c *client) upload(path, field, filename string, content []byte, fields map[string]string) *response {
	c.t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for key, value := range fields {
		require.NoError(c.t, writer.WriteField(key, value))
	}
	if filename != "" {
		part, err := writer.CreateFormFile(field, filename)
		require.NoError(c.t, err)
		_, err = part.Write(content)
		require.NoError(c.t, err)
	}
	require.NoError(c.t, writer.Close())

	r := httptest.NewRequest(http.MethodPost, path, &body)
	r.Header.Set("Content-Type", writer.FormDataContentType())
	if c.token != "" {
		r.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.spaceID != "" {
		r.Header.Set("X-Space-Id", c.spaceID)
	}
	recorder := httptest.NewRecorder()
	c.handler.ServeHTTP(recorder, r)
	return &response{t: c.t, ResponseRecorder: recorder}
}

func attachTo(l *ledger, txn, filename string, content []byte) map[string]any {
	l.t.Helper()
	return l.alex.upload("/documents", "file", filename, content,
		map[string]string{"kind": "transaction", "target_id": txn}).
		requireStatus(http.StatusCreated).json()
}

func TestAnUploadedReceiptComesBackWithItsOwnBytes(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())

	require.Equal(t, "receipt.png", created["filename"])
	require.Equal(t, "image/png", created["content_type"])
	require.Equal(t, float64(len(pngBytes())), created["size_bytes"])

	content := l.alex.get(fmt.Sprint(created["url"])).requireStatus(http.StatusOK)
	require.Equal(t, pngBytes(), content.Body.Bytes())
	require.Equal(t, "image/png", content.Header().Get("Content-Type"))
	// nosniff and the sandbox policy are what stop a stored file being
	// reinterpreted as a document on this origin.
	require.Equal(t, "nosniff", content.Header().Get("X-Content-Type-Options"))
	require.Contains(t, content.Header().Get("Content-Security-Policy"), "default-src 'none'")
	require.Contains(t, content.Header().Get("Content-Disposition"), "inline")
	require.Contains(t, content.Header().Get("Content-Disposition"), "receipt.png")
}

func TestAPDFIsAcceptedAndServedAsOne(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("july"), "statement.pdf", storetest.PDF())
	require.Equal(t, "application/pdf", created["content_type"])
	require.Equal(t,
		"application/pdf",
		l.alex.get(fmt.Sprint(created["url"])).requireStatus(http.StatusOK).
			Header().Get("Content-Type"))
}

// A file that claims to be an image and is in fact HTML is the reason the
// stored type is sniffed. Accepting the client's word here would mean serving
// a script from the application's own origin, with the user's session.
func TestAnHTMLFileWearingAPNGNameIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "totally-a-receipt.png",
		[]byte("<html><script>alert(document.cookie)</script></html>"),
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusConflict)
}

func TestAnSVGIsRefusedBecauseItCanCarryScript(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "logo.svg",
		[]byte(`<?xml version="1.0"?><svg xmlns="http://www.w3.org/2000/svg"></svg>`),
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusConflict)
}

// The extension follows the bytes, not the name the uploader chose: a PDF
// called "receipt.png" would otherwise be offered to the browser under a name
// that contradicts the type it is served with.
func TestTheStoredNameTakesItsExtensionFromTheBytes(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("july"), "scan.png", storetest.PDF())
	require.Equal(t, "scan.pdf", created["filename"])
}

// The filename reaches a Content-Disposition header and the DOM, and never the
// filesystem — but a traversal in it is still evidence the caller is trying
// something, and the label it produces would be wrong either way.
func TestAFilenameCannotCarryAPath(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("july"), "../../../etc/passwd.png", pngBytes())
	require.Equal(t, "passwd.png", created["filename"])

	// The stored key is the attachment's own uuid, so nothing the caller sent
	// decides where the bytes land.
	entries, err := os.ReadDir(filepath.Join(testStoragePath, l.str("space")))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), "passwd")
	}
	require.Contains(t, fmt.Sprint(created["url"]), fmt.Sprint(created["id"]))
}

func TestAnEmptyFileIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "nothing.png", nil,
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAnUploadWithNoFilePartIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "", nil,
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAnUploadWithNoTransactionIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "receipt.png", pngBytes(), map[string]string{"kind": "transaction"}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestAnUploadOverTheSizeLimitIsRefused(t *testing.T) {
	l := buildLedger(t)
	oversized := append(pngBytes(), bytes.Repeat([]byte("x"), service.MaxDocumentBytes)...)
	l.alex.upload("/documents", "file", "huge.png", oversized,
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusUnprocessableEntity)
}

func TestListingReturnsOneTransactionsFilesOldestFirst(t *testing.T) {
	l := buildLedger(t)
	first := attachTo(l, l.str("july"), "first.png", pngBytes())
	second := attachTo(l, l.str("july"), "second.pdf", storetest.PDF())
	attachTo(l, l.str("august_groceries"), "elsewhere.png", pngBytes())

	rows := l.alex.get("/documents?transaction_id=" + l.str("july")).
		requireStatus(http.StatusOK).list()
	require.Len(t, rows, 2)
	require.Equal(t, first["id"], rows[0]["id"])
	require.Equal(t, second["id"], rows[1]["id"])
}

func TestListingWithoutATransactionIsRefused(t *testing.T) {
	l := buildLedger(t)
	l.alex.get("/documents").requireStatus(http.StatusUnprocessableEntity)
}

func TestDeletingAnAttachmentRemovesTheRowAndTheFile(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	url := fmt.Sprint(created["url"])

	l.alex.del("/attachments/" + fmt.Sprint(created["id"])).requireStatus(http.StatusNoContent)
	l.alex.get(url).requireStatus(http.StatusNotFound)
	require.Empty(t, l.alex.get("/documents?transaction_id="+l.str("august_groceries")).
		requireStatus(http.StatusOK).list())

	entries, err := os.ReadDir(filepath.Join(testStoragePath, l.str("space")))
	require.NoError(t, err)
	for _, entry := range entries {
		require.NotContains(t, entry.Name(), fmt.Sprint(created["id"]))
	}
}

func TestTheRegisterCountsAttachmentsPerRow(t *testing.T) {
	l := buildLedger(t)
	attachTo(l, l.str("july"), "one.png", pngBytes())
	attachTo(l, l.str("july"), "two.pdf", storetest.PDF())

	page := l.alex.get("/transactions?start=2026-07-01&end=2026-08-31").
		requireStatus(http.StatusOK).json()
	counts := map[string]float64{}
	for _, raw := range page["items"].([]any) {
		item := raw.(map[string]any)
		counts[fmt.Sprint(item["id"])] = item["attachment_count"].(float64)
	}
	require.Equal(t, float64(2), counts[l.str("july")])
	require.Equal(t, float64(0), counts[l.str("august_groceries")])

	one := l.alex.get("/transactions/" + l.str("july")).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(2), one["attachment_count"])
}

// --- Tenancy and permission -------------------------------------------------

func TestAnotherHouseholdsAttachmentIsNotReadable(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))

	bob.get("/documents/" + fmt.Sprint(created["id"])).requireStatus(http.StatusNotFound)
	bob.get(fmt.Sprint(created["url"])).requireStatus(http.StatusNotFound)
	bob.del("/attachments/" + fmt.Sprint(created["id"])).requireStatus(http.StatusNotFound)
}

func TestAFileCannotBeAttachedToAnotherHouseholdsTransaction(t *testing.T) {
	l := buildLedger(t)
	l.alex.upload("/documents", "file", "receipt.png", pngBytes(),
		map[string]string{"kind": "transaction", "target_id": l.str("stranger_txn")}).
		requireStatus(http.StatusNotFound)
}

func TestAViewerCanReadAttachmentsButNotUploadOrDelete(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	vera := l.as("vera")

	vera.get("/documents?transaction_id=" + l.str("august_groceries")).
		requireStatus(http.StatusOK)
	vera.get(fmt.Sprint(created["url"])).requireStatus(http.StatusOK)
	vera.upload("/documents", "file", "mine.png", pngBytes(),
		map[string]string{"kind": "transaction", "target_id": l.str("august_groceries")}).
		requireStatus(http.StatusForbidden)
	vera.del("/attachments/" + fmt.Sprint(created["id"])).requireStatus(http.StatusForbidden)
}
