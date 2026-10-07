package store

import (
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func newTransactionFor(t *testing.T, spaceID SpaceID, accountID uuid.UUID, payee string) *Transaction {
	t.Helper()
	txn := &Transaction{
		AccountID:     accountID,
		Date:          domain.NewDate(2026, 9, 1),
		Amount:        domain.MustFromString("-42.00"),
		Currency:      "USD",
		StatementName: payee,
		Payee:         payee,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	return txn
}

func newDocument(t *testing.T, spaceID SpaceID, hash, filename string) *Document {
	t.Helper()
	doc := &Document{
		ContentSHA256: hash,
		ContentType:   "application/pdf",
		SizeBytes:     1234,
		Filename:      filename,
		StorageKey:    spaceID.String() + "/" + uuid.NewString() + ".pdf",
		Source:        DocumentSourceUpload,
	}
	require.NoError(t, db(t).CreateDocument(t.Context(), spaceID, doc))
	return doc
}

func hexHash(seed string) string {
	digits := strings.Repeat("0123456789abcdef", 4)
	return (seed + digits)[:64]
}

func TestADocumentIsReachableByIdAndByHash(t *testing.T) {
	space := newSpace(t)
	doc := newDocument(t, space, hexHash("aa"), "statement.pdf")

	read, err := db(t).GetDocument(t.Context(), space, doc.ID)
	require.NoError(t, err)
	require.Equal(t, "statement.pdf", read.Filename)
	// char(64) pads what it stores; the hash must read back unpadded.
	require.Equal(t, hexHash("aa"), read.ContentSHA256)

	byHash, err := db(t).GetDocumentByContentHash(t.Context(), space, hexHash("aa"))
	require.NoError(t, err)
	require.Equal(t, doc.ID, byHash.ID)
}

// A hash lookup takes no id, so it is the one that could be guessed at; it
// must stay inside its space.
func TestADocumentHashIsScopedToItsSpace(t *testing.T) {
	space, other := newSpace(t), newSpace(t)
	newDocument(t, space, hexHash("bb"), "statement.pdf")

	_, err := db(t).GetDocumentByContentHash(t.Context(), other, hexHash("bb"))
	require.ErrorIs(t, err, ErrNotFound)
}

// The folded rows carry a placeholder rather than a digest, and dedupe must
// never treat two as the same bytes.
func TestAPlaceholderHashIsNeverADuplicate(t *testing.T) {
	space := newSpace(t)
	placeholder := UnhashedDocumentHash(uuid.New())
	require.Len(t, placeholder, 64)
	require.True(t, strings.HasPrefix(placeholder, unhashedPrefix))
	newDocument(t, space, placeholder, "imported.pdf")

	_, err := db(t).GetDocumentByContentHash(t.Context(), space, placeholder)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestALinkKindOutsideTheClosedSetIsRefused(t *testing.T) {
	space := newSpace(t)
	doc := newDocument(t, space, hexHash("cc"), "statement.pdf")

	err := db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: doc.ID, Kind: "invoice", TargetID: uuid.New(),
	})
	require.ErrorContains(t, err, "not a document link kind")
}

// A polymorphic link gets no tenancy check from a foreign key.
func TestADocumentCannotBeLinkedFromAnotherSpace(t *testing.T) {
	space, other := newSpace(t), newSpace(t)
	doc := newDocument(t, space, hexHash("dd"), "statement.pdf")

	err := db(t).LinkDocument(t.Context(), other, DocumentLink{
		DocumentID: doc.ID, Kind: DocumentLinkTransaction, TargetID: uuid.New(),
	})
	require.ErrorIs(t, err, ErrNotFound)
}

func TestLinkingTheSameDocumentTwiceIsOneLink(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	txn := newTransactionFor(t, space, account.ID, "GROCER")
	doc := newDocument(t, space, hexHash("ee"), "receipt.pdf")

	link := DocumentLink{DocumentID: doc.ID, Kind: DocumentLinkTransaction, TargetID: txn.ID}
	require.NoError(t, db(t).LinkDocument(t.Context(), space, link))
	require.NoError(t, db(t).LinkDocument(t.Context(), space, link))

	links, err := db(t).ListDocumentLinks(t.Context(), space, doc.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, DocumentRoleAttachment, links[0].Role)
}

// One file, two owners: dropping one owner leaves the file.
func TestADocumentLinkedTwiceSurvivesOneUnlink(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	first := newTransactionFor(t, space, account.ID, "GROCER")
	second := newTransactionFor(t, space, account.ID, "GROCER AGAIN")
	doc := newDocument(t, space, hexHash("ff"), "receipt.pdf")

	for _, txn := range []*Transaction{first, second} {
		require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
			DocumentID: doc.ID, Kind: DocumentLinkTransaction, TargetID: txn.ID,
		}))
	}

	require.NoError(t, db(t).UnlinkDocument(
		t.Context(), space, doc.ID, DocumentLinkTransaction, first.ID))

	links, err := db(t).ListDocumentLinks(t.Context(), space, doc.ID)
	require.NoError(t, err)
	require.Len(t, links, 1)
	require.Equal(t, second.ID, links[0].TargetID)

	_, err = db(t).GetDocument(t.Context(), space, doc.ID)
	require.NoError(t, err)
}

func TestDocumentsBehindATransactionAreTheOnesAttachedToIt(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	txn := newTransactionFor(t, space, account.ID, "GROCER")
	elsewhere := newTransactionFor(t, space, account.ID, "OTHER")

	mine := newDocument(t, space, hexHash("1a"), "receipt.pdf")
	theirs := newDocument(t, space, hexHash("1b"), "other.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: mine.ID, Kind: DocumentLinkTransaction, TargetID: txn.ID,
	}))
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: theirs.ID, Kind: DocumentLinkTransaction, TargetID: elsewhere.ID,
	}))

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), space, txn.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, mine.ID, behind[0].Document.ID)
	require.Equal(t, DocumentLinkTransaction, behind[0].Via)

	counts, err := db(t).CountDocumentsOnTransactions(
		t.Context(), space, []uuid.UUID{txn.ID, elsewhere.ID})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]int{txn.ID: 1, elsewhere.ID: 1}, counts)
}

func TestUnlinkedDocumentsOlderThanFindsOnlyTheOrphans(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	txn := newTransactionFor(t, space, account.ID, "GROCER")

	held := newDocument(t, space, hexHash("3a"), "held.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: held.ID, Kind: DocumentLinkTransaction, TargetID: txn.ID,
	}))
	orphan := newDocument(t, space, hexHash("3b"), "orphan.pdf")

	// A future cutoff stands in for "a week has passed".
	tomorrow := time.Now().Add(24 * time.Hour)
	found, err := db(t).UnlinkedDocumentsOlderThan(t.Context(), space, tomorrow)
	require.NoError(t, err)
	require.Len(t, found, 1)
	require.Equal(t, orphan.ID, found[0].ID)

	none, err := db(t).UnlinkedDocumentsOlderThan(t.Context(), space, time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Empty(t, none)
}

// The rows the has_attachment filter field finds are the rows the paperclip
// counts: a hand attachment or a filed receipt, in this space only.
func TestTransactionsWithDocumentsAreTheRowsThePaperclipCounts(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday Checking")
	attached := newTransactionFor(t, space, account.ID, "HARDWARE BARN")
	receipted := newTransactionFor(t, space, account.ID, "WATER UTILITY")
	bare := newTransactionFor(t, space, account.ID, "TACO STAND")
	billed := newTransactionFor(t, space, account.ID, "PHONE CO")
	split := newTransactionFor(t, space, account.ID, "WAREHOUSE CLUB")
	split.Splits = []Split{
		{Amount: domain.MustFromString("-30.00")},
		{Amount: domain.MustFromString("-12.00")},
	}
	require.NoError(t, db(t).ReplaceSplits(t.Context(), space, split))

	link := func(in SpaceID, seed string, kind DocumentLinkKind, target uuid.UUID) {
		t.Helper()
		doc := newDocument(t, in, hexHash(seed), seed+".pdf")
		require.NoError(t, db(t).LinkDocument(t.Context(), in, DocumentLink{
			DocumentID: doc.ID, Kind: kind, TargetID: target,
		}))
	}
	link(space, "c1", DocumentLinkTransaction, attached.ID)
	link(space, "c2", DocumentLinkReceipt, receipted.ID)
	link(space, "c3", DocumentLinkTransaction, split.ID)
	// A bill's own statement is a document of the bill, not of a bank row.
	link(space, "c4", DocumentLinkBill, billed.ID)
	// Another household's link naming this row's id is theirs, not the row's.
	link(newSpace(t), "c5", DocumentLinkTransaction, bare.ID)

	documented, err := db(t).TransactionsWithDocuments(t.Context(), space)
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]bool{attached.ID: true, receipted.ID: true, split.ID: true}, documented)

	counts, err := db(t).CountDocumentsOnTransactions(t.Context(), space,
		[]uuid.UUID{attached.ID, receipted.ID, bare.ID, billed.ID, split.ID})
	require.NoError(t, err)
	require.Len(t, counts, len(documented))
	for id := range documented {
		require.Equal(t, 1, counts[id])
	}
}
