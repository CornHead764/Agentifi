package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// billedSlot is a series linked to a provider's subaccount, a filed bill with
// its statement, and the bank row that settled the bill's slot. Every figure
// is invented.
type billedSlot struct {
	space      SpaceID
	series     uuid.UUID
	subaccount *BillSubaccount
	bill       *Bill
	statement  *Document
	row        *Transaction
}

func newBilledSlot(t *testing.T) billedSlot {
	t.Helper()
	space := newSpace(t)
	connection := newBillConnection(t, space)
	subaccount := &BillSubaccount{ConnectionID: connection.ID, ExternalID: "premise-7", Label: "Electric"}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, subaccount))
	series := newSeriesRow(t, space)

	bill := &Bill{
		SubaccountID: subaccount.ID, DueOn: domain.NewDate(2026, time.September, 14),
		AmountDue: domain.MustFromString("87.00"), Currency: "USD", Status: domain.BillOpen,
		Source: BillSourceProvider, FetchedAt: time.Now().UTC(),
	}
	require.NoError(t, db(t).UpsertBill(t.Context(), space, bill, false))
	statement := newDocument(t, space, hexHash("5a"), "statement-2026-09.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: statement.ID, Kind: DocumentLinkBill, TargetID: bill.ID, Role: DocumentRoleStatement,
	}))

	account := newAccount(t, space, "Everyday Checking")
	row := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, time.September, 13),
		Amount: domain.MustFromString("-87.00"), Currency: "USD",
		StatementName: "ELECTRIC CO AUTOPAY", Payee: "Electric Co",
		SeriesID: series, SeriesDueOn: domain.NewDate(2026, time.September, 14),
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))
	return billedSlot{space, series, subaccount, bill, statement, row}
}

func receiptLinks(t *testing.T, space SpaceID, txnID uuid.UUID) []Document {
	t.Helper()
	docs, err := db(t).ListDocumentsByLink(t.Context(), space, DocumentLinkReceipt, txnID)
	require.NoError(t, err)
	return docs
}

func TestLinkingASeriesToItsBillFilesTheStatementOnThePaidRow(t *testing.T) {
	slot := newBilledSlot(t)
	require.Empty(t, receiptLinks(t, slot.space, slot.row.ID), "no bill link yet, so no receipt")

	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)

	receipts := receiptLinks(t, slot.space, slot.row.ID)
	require.Len(t, receipts, 1)
	require.Equal(t, slot.statement.ID, receipts[0].ID)

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), slot.space, slot.row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, DocumentLinkReceipt, behind[0].Via)
	require.Equal(t, DocumentRoleStatement, behind[0].Role)
	require.NotNil(t, behind[0].Receipt)
	require.Equal(t, DocumentLinkBill, behind[0].Receipt.Of)
	require.Equal(t, "Main account", behind[0].Receipt.Name)
	require.Equal(t, domain.NewDate(2026, time.September, 14), behind[0].Receipt.DueOn)

	counts, err := db(t).CountDocumentsOnTransactions(t.Context(), slot.space, []uuid.UUID{slot.row.ID})
	require.NoError(t, err)
	require.Equal(t, 1, counts[slot.row.ID])
}

func TestAStatementFiledAfterThePaymentStillReachesTheRow(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)

	// A corrected statement replaces the first: the row follows the bill.
	corrected := newDocument(t, slot.space, hexHash("5b"), "statement-2026-09-corrected.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), slot.space, DocumentLink{
		DocumentID: corrected.ID, Kind: DocumentLinkBill, TargetID: slot.bill.ID, Role: DocumentRoleStatement,
	}))

	receipts := receiptLinks(t, slot.space, slot.row.ID)
	require.Len(t, receipts, 1)
	require.Equal(t, corrected.ID, receipts[0].ID)
}

func TestANewStatementUnlinksTheOneItReplaces(t *testing.T) {
	slot := newBilledSlot(t)
	bill, err := db(t).GetBill(t.Context(), slot.space, slot.bill.ID)
	require.NoError(t, err)
	require.Equal(t, slot.statement.ID, bill.DocumentID)

	corrected := newDocument(t, slot.space, hexHash("5c"), "statement-2026-09-reissued.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), slot.space, DocumentLink{
		DocumentID: corrected.ID, Kind: DocumentLinkBill, TargetID: slot.bill.ID, Role: DocumentRoleStatement,
	}))

	bill, err = db(t).GetBill(t.Context(), slot.space, slot.bill.ID)
	require.NoError(t, err)
	require.Equal(t, corrected.ID, bill.DocumentID)
	linked, err := db(t).ListDocumentsByLink(t.Context(), slot.space, DocumentLinkBill, slot.bill.ID)
	require.NoError(t, err)
	require.Len(t, linked, 1, "the replaced statement is not the bill's any more")
	require.Equal(t, corrected.ID, linked[0].ID)
	links, err := db(t).ListDocumentLinks(t.Context(), slot.space, slot.statement.ID)
	require.NoError(t, err)
	require.Empty(t, links)

	attachment := newDocument(t, slot.space, hexHash("5d"), "notice.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), slot.space, DocumentLink{
		DocumentID: attachment.ID, Kind: DocumentLinkBill, TargetID: slot.bill.ID, Role: DocumentRoleAttachment,
	}))
	bill, err = db(t).GetBill(t.Context(), slot.space, slot.bill.ID)
	require.NoError(t, err)
	require.Equal(t, corrected.ID, bill.DocumentID, "an attachment is not a statement")
}

func TestReconcilingReceiptsTwiceChangesNothingTheSecondTime(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)

	added, removed, err := db(t).ReconcileSpaceReceipts(t.Context(), slot.space)
	require.NoError(t, err)
	require.Zero(t, added)
	require.Zero(t, removed)
	require.Len(t, receiptLinks(t, slot.space, slot.row.ID), 1)
}

func TestTheSpacePassBackfillsReceiptsNoHookWrote(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)
	// The receipt links taken away: the link is gone, the facts remain.
	_, err = db(t).Pool().Exec(t.Context(),
		`DELETE FROM document_links WHERE space_id = $1 AND kind = 'receipt'`, slot.space.UUID())
	require.NoError(t, err)
	require.Empty(t, receiptLinks(t, slot.space, slot.row.ID))

	added, removed, err := db(t).ReconcileSpaceReceipts(t.Context(), slot.space)
	require.NoError(t, err)
	require.Equal(t, 1, added)
	require.Zero(t, removed)
	require.Len(t, receiptLinks(t, slot.space, slot.row.ID), 1)
}

func TestUnlinkingTheBillRemovesOnlyTheReceipt(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)
	mine := newDocument(t, slot.space, hexHash("5c"), "my-note.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), slot.space, DocumentLink{
		DocumentID: mine.ID, Kind: DocumentLinkTransaction, TargetID: slot.row.ID,
	}))

	require.NoError(t, db(t).UnlinkSeriesBill(t.Context(), slot.space, slot.series))

	require.Empty(t, receiptLinks(t, slot.space, slot.row.ID))
	behind, err := db(t).DocumentsBehindTransaction(t.Context(), slot.space, slot.row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1, "the file a person attached stays")
	require.Equal(t, mine.ID, behind[0].Document.ID)
	require.Equal(t, DocumentLinkTransaction, behind[0].Via)
}

func TestAStatementAlsoAttachedByHandIsListedOnceAndStaysRemovable(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)
	require.NoError(t, db(t).LinkDocument(t.Context(), slot.space, DocumentLink{
		DocumentID: slot.statement.ID, Kind: DocumentLinkTransaction, TargetID: slot.row.ID,
	}))

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), slot.space, slot.row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, DocumentLinkTransaction, behind[0].Via)
	require.NotNil(t, behind[0].Receipt, "and it still says where it came from")

	counts, err := db(t).CountDocumentsOnTransactions(t.Context(), slot.space, []uuid.UUID{slot.row.ID})
	require.NoError(t, err)
	require.Equal(t, 1, counts[slot.row.ID])
}

func TestADeletedRowLosesItsReceipt(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)
	require.NoError(t, db(t).DeleteTransaction(t.Context(), slot.space, slot.row.ID))
	require.Empty(t, receiptLinks(t, slot.space, slot.row.ID))
}

func TestDeletingTheConnectionReleasesTheReceiptsToo(t *testing.T) {
	slot := newBilledSlot(t)
	_, err := db(t).LinkSeriesBill(t.Context(), slot.space, slot.series, slot.subaccount.ID)
	require.NoError(t, err)
	require.NoError(t, db(t).DeleteBillConnection(t.Context(), slot.space, slot.subaccount.ConnectionID))

	links, err := db(t).ListDocumentLinks(t.Context(), slot.space, slot.statement.ID)
	require.NoError(t, err)
	require.Empty(t, links, "nothing keeps the statement out of the purge")
}

// orderWithInvoice is a merchant order with a stored invoice and a bank row to
// match it to. Every figure is invented.
type orderWithInvoice struct {
	space   SpaceID
	account *MerchantAccount
	order   *MerchantOrder
	invoice *Document
	row     *Transaction
}

func newOrderWithInvoice(t *testing.T) orderWithInvoice {
	t.Helper()
	space := newSpace(t)
	account := &MerchantAccount{Merchant: domain.MerchantAmazon, Label: "Household"}
	require.NoError(t, db(t).CreateMerchantAccount(t.Context(), space, account))
	order := &MerchantOrder{
		MerchantAccountID: account.ID, Merchant: domain.MerchantAmazon, OrderNumber: "111-0000000-4242424",
		OrderedOn: domain.NewDate(2026, time.September, 2), Total: domain.MustFromString("42.00"),
		Currency: "USD", Source: "pull",
	}
	_, err := db(t).UpsertMerchantOrder(t.Context(), space, order)
	require.NoError(t, err)
	invoice := newDocument(t, space, hexHash("6a"), "invoice-111-0000000-4242424.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: invoice.ID, Kind: DocumentLinkMerchantOrder, TargetID: order.ID, Role: DocumentRoleInvoice,
	}))
	bank := newAccount(t, space, "Rewards Card")
	row := newTransactionFor(t, space, bank.ID, "AMZN MKTP US")
	return orderWithInvoice{space, account, order, invoice, row}
}

func TestMatchingAnOrderFilesItsInvoiceAndUnmatchingRemovesIt(t *testing.T) {
	o := newOrderWithInvoice(t)
	require.NoError(t, db(t).SetMerchantMatch(t.Context(), o.space, &MerchantMatch{
		TransactionID: o.row.ID, OrderID: o.order.ID, Amount: domain.MustFromString("42.00"),
		Basis: "total", Confidence: 1,
	}))

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), o.space, o.row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, o.invoice.ID, behind[0].Document.ID)
	require.Equal(t, DocumentLinkReceipt, behind[0].Via)
	require.Equal(t, DocumentRoleInvoice, behind[0].Role)
	require.Equal(t, DocumentLinkMerchantOrder, behind[0].Receipt.Of)
	require.Equal(t, "Amazon", behind[0].Receipt.Name)
	require.Equal(t, "111-0000000-4242424", behind[0].Receipt.OrderNumber)

	require.NoError(t, db(t).DeleteMerchantMatchForOrder(t.Context(), o.space, o.row.ID, o.order.ID))
	require.Empty(t, receiptLinks(t, o.space, o.row.ID))
	links, err := db(t).ListDocumentLinks(t.Context(), o.space, o.invoice.ID)
	require.NoError(t, err)
	require.Len(t, links, 1, "the invoice stays on its order")
}

func TestARefundMatchedToAnOrderGetsNoInvoice(t *testing.T) {
	o := newOrderWithInvoice(t)
	refund := &MerchantRefund{
		Merchant: domain.MerchantAmazon, MerchantAccountID: o.account.ID, OrderNumber: o.order.OrderNumber,
		Quantity: 1, RefundedOn: domain.NewDate(2026, time.September, 9),
		Amount: domain.MustFromString("12.00"),
	}
	_, err := db(t).UpsertMerchantRefund(t.Context(), o.space, refund)
	require.NoError(t, err)
	require.NoError(t, db(t).AddMerchantMatch(t.Context(), o.space, &MerchantMatch{
		TransactionID: o.row.ID, OrderID: o.order.ID, RefundID: &refund.ID,
		Amount: domain.MustFromString("12.00"), Basis: "refund", Confidence: 1,
	}))
	require.Empty(t, receiptLinks(t, o.space, o.row.ID))
}

func TestReceiptsNeverCrossASpace(t *testing.T) {
	o := newOrderWithInvoice(t)
	require.NoError(t, db(t).SetMerchantMatch(t.Context(), o.space, &MerchantMatch{
		TransactionID: o.row.ID, OrderID: o.order.ID, Amount: domain.MustFromString("42.00"),
		Basis: "total", Confidence: 1,
	}))

	// Another household's reconcile over this household's row writes and
	// removes nothing.
	other := newSpace(t)
	added, removed, err := db(t).ReconcileReceipts(t.Context(), other, []uuid.UUID{o.row.ID})
	require.NoError(t, err)
	require.Zero(t, added)
	require.Zero(t, removed)
	require.Len(t, receiptLinks(t, o.space, o.row.ID), 1)

	// A document of the other space linked, by a bug, to this space's order
	// is not filed on this space's row.
	stray := newDocument(t, other, hexHash("6b"), "stray.pdf")
	_, err = db(t).Pool().Exec(t.Context(),
		`INSERT INTO document_links (document_id, space_id, kind, target_id, role)
		 VALUES ($1, $2, 'merchant_order', $3, 'invoice')`, stray.ID, other.UUID(), o.order.ID)
	require.NoError(t, err)
	_, _, err = db(t).ReconcileSpaceReceipts(t.Context(), o.space)
	require.NoError(t, err)
	receipts := receiptLinks(t, o.space, o.row.ID)
	require.Len(t, receipts, 1)
	require.Equal(t, o.invoice.ID, receipts[0].ID)

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), other, o.row.ID)
	require.NoError(t, err)
	require.Empty(t, behind)
}

func TestDeletingAMerchantAccountReleasesItsInvoicesAndReceipts(t *testing.T) {
	o := newOrderWithInvoice(t)
	require.NoError(t, db(t).SetMerchantMatch(t.Context(), o.space, &MerchantMatch{
		TransactionID: o.row.ID, OrderID: o.order.ID, Amount: domain.MustFromString("42.00"),
		Basis: "total", Confidence: 1,
	}))
	require.NoError(t, db(t).DeleteMerchantAccount(t.Context(), o.space, o.account.ID))

	links, err := db(t).ListDocumentLinks(t.Context(), o.space, o.invoice.ID)
	require.NoError(t, err)
	require.Empty(t, links)
}

func TestAnInvoiceFiledAfterTheMatchStillReachesTheRow(t *testing.T) {
	o := newOrderWithInvoice(t)
	require.NoError(t, db(t).UnlinkDocument(t.Context(), o.space, o.invoice.ID, DocumentLinkMerchantOrder, o.order.ID))
	require.NoError(t, db(t).SetMerchantMatch(t.Context(), o.space, &MerchantMatch{
		TransactionID: o.row.ID, OrderID: o.order.ID, Amount: domain.MustFromString("42.00"),
		Basis: "total", Confidence: 1,
	}))
	require.Empty(t, receiptLinks(t, o.space, o.row.ID))

	require.NoError(t, db(t).LinkDocument(t.Context(), o.space, DocumentLink{
		DocumentID: o.invoice.ID, Kind: DocumentLinkMerchantOrder, TargetID: o.order.ID, Role: DocumentRoleInvoice,
	}))
	receipts := receiptLinks(t, o.space, o.row.ID)
	require.Len(t, receipts, 1)
	require.Equal(t, o.invoice.ID, receipts[0].ID)

	require.NoError(t, db(t).UnlinkDocument(t.Context(), o.space, o.invoice.ID, DocumentLinkMerchantOrder, o.order.ID))
	require.Empty(t, receiptLinks(t, o.space, o.row.ID), "an invoice taken off its order leaves the row too")
}

func TestTheOrdersWithAnInvoiceOnFileAreTheOnesAPullNeedNotPrint(t *testing.T) {
	o := newOrderWithInvoice(t)
	bare := &MerchantOrder{
		MerchantAccountID: o.account.ID, Merchant: domain.MerchantAmazon, OrderNumber: "111-0000000-5353535",
		OrderedOn: domain.NewDate(2026, time.September, 4), Total: domain.MustFromString("9.00"),
		Currency: "USD", Source: "pull",
	}
	_, err := db(t).UpsertMerchantOrder(t.Context(), o.space, bare)
	require.NoError(t, err)

	since := domain.NewDate(2026, time.August, 1)
	numbers, err := db(t).MerchantOrdersWithInvoiceDocument(t.Context(), o.space, o.account.ID, since)
	require.NoError(t, err)
	require.Equal(t, []string{"111-0000000-4242424"}, numbers)

	later, err := db(t).MerchantOrdersWithInvoiceDocument(t.Context(), o.space, o.account.ID,
		domain.NewDate(2026, time.September, 3))
	require.NoError(t, err)
	require.Empty(t, later, "an order before the window is not asked about")

	other, err := db(t).MerchantOrdersWithInvoiceDocument(t.Context(), newSpace(t), o.account.ID, since)
	require.NoError(t, err)
	require.Empty(t, other, "another household sees none of this one's orders")
}
