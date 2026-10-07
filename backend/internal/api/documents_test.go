package api

import (
	"fmt"
	"mime"
	"net/http"
	"sort"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// The document store over HTTP.
//
// A transaction's attachments are documents linked to it, uploaded through
// POST /documents, and the register's paperclip and the detail dialog read
// them; the attachment suite beside this file covers their behaviour. This
// file pins what a behavioural test would not notice: the exact response
// shape, the id being the document's, and the count the register reads.

// TestAnUploadAnswersWithTheDocument: the upload's contract, field by field.
func TestAnUploadAnswersWithTheDocument(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("july"), "receipt.png", pngBytes())

	// The response is the one the client was written against: these keys and
	// no others. A field quietly added or dropped here is a frontend that
	// renders "undefined" against somebody's statement.
	keys := make([]string, 0, len(created))
	for key := range created {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	require.Equal(t, []string{
		"content_type", "created_at", "filename", "id", "size_bytes",
		"source", "source_ref", "uploaded_by_user_id", "url",
	}, keys)
	require.Equal(t,
		fmt.Sprintf("/documents/%v/content", created["id"]), created["url"])

	listed := l.alex.get("/documents?transaction_id=" + l.str("july")).
		requireStatus(http.StatusOK).list()
	require.Len(t, listed, 1)
	require.Equal(t, created["id"], listed[0]["id"])

	// The attachment's id *is* the document's id.
	document := l.alex.get("/documents/" + fmt.Sprint(created["id"])).
		requireStatus(http.StatusOK).json()
	require.Equal(t, created["filename"], document["filename"])
	require.Equal(t, "upload", document["source"])

	// The register's paperclip counts what the row holds.
	row := l.alex.get("/transactions/" + l.str("july")).requireStatus(http.StatusOK).json()
	require.Equal(t, float64(1), row["attachment_count"])
}

// The same file on two rows is one document with two links, and letting one
// row go leaves the other's copy where it is.
func TestOneFileOnTwoRowsIsOneDocument(t *testing.T) {
	l := buildLedger(t)
	first := attachTo(l, l.str("july"), "receipt.png", pngBytes())
	second := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	require.Equal(t, first["id"], second["id"], "the same bytes are one document")

	// Removing it from one row is scoped by transaction_id; the other keeps it.
	l.alex.del(fmt.Sprintf("/attachments/%v?transaction_id=%s", first["id"], l.str("july"))).
		requireStatus(http.StatusNoContent)
	require.Empty(t, l.alex.get("/documents?transaction_id="+l.str("july")).
		requireStatus(http.StatusOK).list())

	kept := l.alex.get("/documents?transaction_id=" + l.str("august_groceries")).
		requireStatus(http.StatusOK).list()
	require.Len(t, kept, 1)
	l.alex.get(fmt.Sprint(kept[0]["url"])).requireStatus(http.StatusOK)
}

// Deleting the transaction releases its files rather than destroying them: the
// row is soft-deleted, the document may be a bill's statement as well, and the
// purge job's week is the window in which the wrong delete is recoverable.
func TestDeletingATransactionReleasesItsDocumentsRatherThanTheirBytes(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_corner"), "receipt.png", pngBytes())

	l.alex.del("/transactions/" + l.str("august_corner")).requireStatus(http.StatusNoContent)
	// Not an attachment any more: nothing links it to a transaction.
	links, err := db(t).ListDocumentLinks(t.Context(), store.SpaceIDOf(l.id("space")),
		uuid.MustParse(fmt.Sprint(created["id"])))
	require.NoError(t, err)
	for _, link := range links {
		require.NotEqual(t, store.DocumentLinkTransaction, link.Kind)
	}
	// Still a document, and still readable, until the purge takes it.
	l.alex.get("/documents/" + fmt.Sprint(created["id"])).requireStatus(http.StatusOK)
}

// --- The documents resource -------------------------------------------------

func TestDocumentsBehindARowCarryTheirOriginAndNoBytes(t *testing.T) {
	l := buildLedger(t)
	attachTo(l, l.str("july"), "receipt.png", pngBytes())
	attachTo(l, l.str("july"), "statement.pdf", storetest.PDF())

	rows := l.alex.get("/documents?transaction_id=" + l.str("july")).
		requireStatus(http.StatusOK).list()
	require.Len(t, rows, 2)
	for _, row := range rows {
		// Where it came from, which is the whole reason the panel asks this
		// endpoint.
		require.Equal(t, "upload", row["source"])
		require.Equal(t, "transaction", row["via"])
		// A listing never carries content: a household's statements must not
		// ride along in every register refresh, nor into the logs and caches
		// on the way.
		require.NotContains(t, row, "content")
		require.NotContains(t, row, "bytes")
		require.NotContains(t, row, "data")
		require.Contains(t, fmt.Sprint(row["url"]), "/content")
	}
}

func TestDocumentContentAnswersBytesNotJSON(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("july"), "statement.pdf", storetest.PDF())

	content := l.alex.get("/documents/" + fmt.Sprint(created["id"]) + "/content").
		requireStatus(http.StatusOK)
	require.Equal(t, storetest.PDF(), content.Body.Bytes())
	require.Equal(t, "application/pdf", content.Header().Get("Content-Type"))
	require.Equal(t, "nosniff", content.Header().Get("X-Content-Type-Options"))
	// Inline, so the panel's new tab shows the statement in the browser's
	// viewer; only ?download=1 asks to save it.
	disposition, params, err := mime.ParseMediaType(content.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "inline", disposition)
	require.Equal(t, "statement.pdf", params["filename"])

	saved := l.alex.get("/documents/" + fmt.Sprint(created["id"]) + "/content?download=1").
		requireStatus(http.StatusOK)
	disposition, _, err = mime.ParseMediaType(saved.Header().Get("Content-Disposition"))
	require.NoError(t, err)
	require.Equal(t, "attachment", disposition)
	require.Equal(t, "application/pdf", saved.Header().Get("Content-Type"))
}

// An upload through the general resource has to say what the file is a
// document of, and the kinds whose tables are not here yet are refused rather
// than written unverified.
func TestUploadingADocumentNeedsALinkItCanCheck(t *testing.T) {
	l := buildLedger(t)

	created := l.alex.upload("/documents", "file", "receipt.png", pngBytes(),
		map[string]string{
			"kind": string(store.DocumentLinkTransaction), "target_id": l.str("july"),
			"role": store.DocumentRoleReceipt,
		}).requireStatus(http.StatusCreated).json()
	require.Equal(t, "upload", created["source"])
	// The role decides nothing about visibility: it is still a document of
	// that row, so the row's own panel shows it.
	require.Len(t, l.alex.get("/documents?transaction_id="+l.str("july")).
		requireStatus(http.StatusOK).list(), 1)

	l.alex.upload("/documents", "file", "receipt.png", pngBytes(),
		map[string]string{"kind": "statement", "target_id": l.str("july")}).
		requireStatus(http.StatusUnprocessableEntity)

	// A bill link is checked against the bills table, so a transaction's id
	// under kind bill names a target that does not exist rather than a link
	// nothing could verify.
	l.alex.upload("/documents", "file", "receipt.png", pngBytes(),
		map[string]string{"kind": "bill", "target_id": l.str("july")}).
		requireStatus(http.StatusNotFound)

	// A merchant order is the kind nothing here can check: its documents are
	// stored by the pull that printed them.
	l.alex.upload("/documents", "file", "receipt.png", pngBytes(),
		map[string]string{"kind": "merchant_order", "target_id": l.str("july")}).
		requireStatus(http.StatusConflict)
}

func TestAnotherHouseholdsDocumentIsNotReadable(t *testing.T) {
	l := buildLedger(t)
	created := attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	bob := newClient(t).as(l.users["bob"]).inSpace(store.SpaceIDOf(l.id("other_space")))

	bob.get("/documents/" + fmt.Sprint(created["id"])).requireStatus(http.StatusNotFound)
	bob.get("/documents/" + fmt.Sprint(created["id"]) + "/content").requireStatus(http.StatusNotFound)
	bob.get("/documents?transaction_id=" + l.str("august_groceries")).
		requireStatus(http.StatusNotFound)
}

func TestAViewerCanReadDocumentsButNotUploadOne(t *testing.T) {
	l := buildLedger(t)
	attachTo(l, l.str("august_groceries"), "receipt.png", pngBytes())
	vera := l.as("vera")

	vera.get("/documents?transaction_id=" + l.str("august_groceries")).
		requireStatus(http.StatusOK)
	vera.upload("/documents", "file", "mine.png", pngBytes(),
		map[string]string{
			"kind":      string(store.DocumentLinkTransaction),
			"target_id": l.str("august_groceries"),
		}).requireStatus(http.StatusForbidden)
}
