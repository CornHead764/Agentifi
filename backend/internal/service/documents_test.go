package service

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The document store's two rules that only a database and a disk can prove:
// the same bytes are one file however many times they arrive, and a file
// nothing points at goes away — but only after the grace period, and only when
// nothing at all points at it.

// pngBytes is the smallest thing http.DetectContentType calls an image/png:
// the eight-byte signature is the whole sniff, and it reads no further.
func pngBytes(tail string) []byte {
	return append([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a}, []byte(tail)...)
}

func newDocuments(t *testing.T) (*Documents, string) {
	t.Helper()
	root := t.TempDir()
	docs := NewDocuments(db(t), &provider.LocalStorage{BasePath: root})
	return docs, root
}

func storedFiles(t *testing.T, root string, spaceID store.SpaceID) []string {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, spaceID.String()))
	if os.IsNotExist(err) {
		return nil
	}
	require.NoError(t, err)
	names := make([]string, 0, len(entries))
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	return names
}

func TestDocumentStoreDedupesByContentHash(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	first := newTransaction(t, space, account, on(2026, time.September, 1), "-42.00")
	second := newTransaction(t, space, account, on(2026, time.September, 2), "-42.00")
	docs, root := newDocuments(t)

	receipt := pngBytes("the same scan")
	one, created, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: receipt, Filename: "receipt.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: first.ID},
	})
	require.NoError(t, err)
	require.True(t, created)

	// The same bytes again, on a different row and under a different name.
	two, created, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: receipt, Filename: "scan-copy.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: second.ID},
	})
	require.NoError(t, err)
	require.False(t, created, "the same bytes are one document, not two")
	require.Equal(t, one.ID, two.ID)
	require.Equal(t, "receipt.png", two.Filename, "the first name stands; the file is the same file")

	// One blob on disk, two links to it.
	require.Len(t, storedFiles(t, root, space), 1)
	links, err := db(t).ListDocumentLinks(t.Context(), space, one.ID)
	require.NoError(t, err)
	require.Len(t, links, 2)

	// Different bytes are a different document, even byte-for-byte close ones.
	other, created, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("a different scan"), Filename: "receipt.png",
		Source: store.DocumentSourceUpload,
		Link:   store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: first.ID},
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, one.ID, other.ID)
	require.Len(t, storedFiles(t, root, space), 2)
}

// The dedupe is per household. Two spaces holding the same receipt hold two
// documents, because one space must never be handed a row the other created.
func TestDocumentDedupeDoesNotCrossSpaces(t *testing.T) {
	space, other := newSpace(t), newSpace(t)
	here := newAccount(t, space, "Everyday Checking")
	there := newAccount(t, other, "Everyday Checking")
	mine := newTransaction(t, space, here, on(2026, time.September, 1), "-42.00")
	theirs := newTransaction(t, other, there, on(2026, time.September, 1), "-42.00")
	docs, _ := newDocuments(t)

	receipt := pngBytes("shared bytes")
	one, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: receipt, Filename: "receipt.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: mine.ID},
	})
	require.NoError(t, err)
	two, created, err := docs.Store(t.Context(), other, DocumentUpload{
		Bytes: receipt, Filename: "receipt.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: theirs.ID},
	})
	require.NoError(t, err)
	require.True(t, created)
	require.NotEqual(t, one.ID, two.ID)
}

func TestDocumentStoreRefusesWhatABrowserWouldRender(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	txn := newTransaction(t, space, account, on(2026, time.September, 1), "-42.00")
	docs, root := newDocuments(t)

	link := store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: txn.ID}
	_, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes:    []byte("<html><script>alert(document.cookie)</script></html>"),
		Filename: "totally-a-receipt.png", Source: store.DocumentSourceUpload, Link: link,
	})
	require.ErrorIs(t, err, ErrUnsupportedDocument)

	_, _, err = docs.Store(t.Context(), space, DocumentUpload{
		Bytes: nil, Filename: "nothing.png", Source: store.DocumentSourceUpload, Link: link,
	})
	require.ErrorIs(t, err, ErrUnsupportedDocument)

	_, _, err = docs.Store(t.Context(), space, DocumentUpload{
		Bytes:    append(pngBytes("big"), make([]byte, MaxDocumentBytes)...),
		Filename: "huge.png", Source: store.DocumentSourceUpload, Link: link,
	})
	require.ErrorIs(t, err, ErrDocumentTooLarge)

	// Nothing refused reached the disk.
	require.Empty(t, storedFiles(t, root, space))
}

// A link kind the store does not know would be a link nothing can find and the
// purge cannot see, so it is refused before any byte is written.
func TestDocumentStoreRefusesAnUnknownLinkKind(t *testing.T) {
	space := newSpace(t)
	docs, root := newDocuments(t)

	_, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("x"), Filename: "receipt.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: "statement", TargetID: uuid.New()},
	})
	require.ErrorContains(t, err, "not a document link kind")
	require.Empty(t, storedFiles(t, root, space))
}

func TestDocumentPurgeKeepsLinked(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	first := newTransaction(t, space, account, on(2026, time.September, 1), "-42.00")
	second := newTransaction(t, space, account, on(2026, time.September, 2), "-42.00")
	docs, root := newDocuments(t)

	held, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("held"), Filename: "held.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: first.ID},
	})
	require.NoError(t, err)

	// The same file on both rows. Letting go of one leaves it linked, so the
	// purge must not touch it.
	_, _, err = docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("held"), Filename: "held.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: second.ID},
	})
	require.NoError(t, err)

	orphan, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("orphan"), Filename: "orphan.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: second.ID},
	})
	require.NoError(t, err)

	stillLinked, err := docs.Unlink(
		t.Context(), space, held.ID, store.DocumentLinkTransaction, first.ID)
	require.NoError(t, err)
	require.True(t, stillLinked, "the second row still holds it")

	stillLinked, err = docs.Unlink(
		t.Context(), space, orphan.ID, store.DocumentLinkTransaction, second.ID)
	require.NoError(t, err)
	require.False(t, stillLinked)

	// Within the grace period nothing goes.
	purged, err := docs.PurgeSpace(t.Context(), space, time.Now().Add(-DocumentPurgeGrace))
	require.NoError(t, err)
	require.Zero(t, purged)
	require.Len(t, storedFiles(t, root, space), 2)

	// A week later the orphan goes and the linked one stays, bytes and all.
	purged, err = docs.PurgeSpace(t.Context(), space, time.Now().Add(time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, purged)

	_, err = db(t).GetDocument(t.Context(), space, orphan.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	kept, err := db(t).GetDocument(t.Context(), space, held.ID)
	require.NoError(t, err)

	names := storedFiles(t, root, space)
	require.Len(t, names, 1)
	require.Equal(t, filepath.Base(kept.StorageKey), names[0])
}

// Purge walks every space, which is how the scheduler calls it.
func TestDocumentPurgeCoversEverySpace(t *testing.T) {
	space := newSpace(t)
	docs, _ := newDocuments(t)
	account := newAccount(t, space, "Everyday Checking")
	txn := newTransaction(t, space, account, on(2026, time.September, 1), "-42.00")

	orphan, _, err := docs.Store(t.Context(), space, DocumentUpload{
		Bytes: pngBytes("walked"), Filename: "walked.png", Source: store.DocumentSourceUpload,
		Link: store.DocumentLink{Kind: store.DocumentLinkTransaction, TargetID: txn.ID},
	})
	require.NoError(t, err)
	_, err = docs.Unlink(t.Context(), space, orphan.ID, store.DocumentLinkTransaction, txn.ID)
	require.NoError(t, err)

	// A clock a week and an hour ahead, so the grace period has passed for
	// this document and the test does not have to age a row.
	docs.Now = func() time.Time { return time.Now().Add(DocumentPurgeGrace + time.Hour) }
	purged, err := docs.Purge(t.Context())
	require.NoError(t, err)
	require.GreaterOrEqual(t, purged, 1)

	_, err = db(t).GetDocument(t.Context(), space, orphan.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}
