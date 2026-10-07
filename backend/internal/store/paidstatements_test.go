package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// paidStatement is a medical provider's billed account with one statement
// filed, and the card row that paid it, linked to no reminder. Every figure is
// invented.
type paidStatement struct {
	space      SpaceID
	subaccount *BillSubaccount
	bill       *Bill
	statement  *Document
	row        *Transaction
}

func newPaidStatement(t *testing.T) paidStatement {
	t.Helper()
	space := newSpace(t)
	connection := &BillConnection{
		Biller: domain.BillerMyChart, Label: "Example Health", Site: "https://mychart.example-health.example/MyChart",
		CredentialSource: BillCredentialSession, AutopayRule: domain.AutopayNone, PullEnabled: true,
	}
	require.NoError(t, sealedDB(t).CreateBillConnection(t.Context(), space, connection))
	subaccount := &BillSubaccount{ConnectionID: connection.ID, ExternalID: "70001234", Label: "Guarantor account ****1234"}
	require.NoError(t, db(t).UpsertBillSubaccount(t.Context(), space, subaccount))

	bill := &Bill{
		SubaccountID: subaccount.ID, DueOn: domain.NewDate(2026, time.March, 28),
		IssuedOn:  domain.NewDate(2026, time.March, 3),
		AmountDue: domain.MustFromString("140.00"), Currency: "USD", Status: domain.BillOpen,
		Source: BillSourceProvider, FetchedAt: time.Now().UTC(),
	}
	require.NoError(t, db(t).UpsertBill(t.Context(), space, bill, false))
	statement := newDocument(t, space, hexHash("7a"), "mychart-1234-2026-03-03.pdf")
	require.NoError(t, db(t).LinkDocument(t.Context(), space, DocumentLink{
		DocumentID: statement.ID, Kind: DocumentLinkBill, TargetID: bill.ID, Role: DocumentRoleStatement,
	}))

	card := newAccount(t, space, "HSA Card")
	row := &Transaction{
		AccountID: card.ID, Date: domain.NewDate(2026, time.March, 12),
		Amount: domain.MustFromString("-140.00"), Currency: "USD",
		StatementName: "EXAMPLE HEALTH PATIENT PMT", Payee: "Example Health",
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, row))
	return paidStatement{space, subaccount, bill, statement, row}
}

func (p paidStatement) pay(t *testing.T, externalID string, on domain.Date, amount string) {
	t.Helper()
	_, err := db(t).UpsertBillPayments(t.Context(), p.space, p.subaccount.ID, []BillPayment{{
		ExternalID: externalID, PaidOn: on, Amount: domain.MustFromString(amount),
		Method: "Visa ending 0000", FetchedAt: time.Now().UTC(),
	}})
	require.NoError(t, err)
}

func TestAPaidStatementIsTheReceiptOnTheCardRowThatPaidIt(t *testing.T) {
	paid := newPaidStatement(t)
	paid.pay(t, "70001234:2026-03-10:140.00", domain.NewDate(2026, time.March, 10), "140.00")

	made, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)
	require.Equal(t, 1, made)

	behind, err := db(t).DocumentsBehindTransaction(t.Context(), paid.space, paid.row.ID)
	require.NoError(t, err)
	require.Len(t, behind, 1)
	require.Equal(t, paid.statement.ID, behind[0].Document.ID)
	require.Equal(t, DocumentLinkReceipt, behind[0].Via)
	require.Equal(t, DocumentLinkBill, behind[0].Receipt.Of)
	require.Equal(t, "Example Health", behind[0].Receipt.Name)

	settled, err := db(t).BillsSettledByTransaction(t.Context(), paid.space, paid.row.ID)
	require.NoError(t, err)
	require.Len(t, settled, 1)
	require.Equal(t, paid.bill.ID, settled[0].Bill.ID)

	again, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)
	require.Zero(t, again, "a paired payment is not paired twice")
}

func TestARowEditKeepsThePaidStatement(t *testing.T) {
	paid := newPaidStatement(t)
	paid.pay(t, "p1", domain.NewDate(2026, time.March, 10), "140.00")
	_, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)

	paid.row.Payee = "Example Health System"
	require.NoError(t, db(t).UpdateTransaction(t.Context(), paid.space, paid.row))
	require.Len(t, receiptLinks(t, paid.space, paid.row.ID), 1)
}

func TestAPaymentBeforeTheStatementFilesNoReceipt(t *testing.T) {
	paid := newPaidStatement(t)
	paid.row.Date = domain.NewDate(2026, time.March, 2)
	require.NoError(t, db(t).UpdateTransaction(t.Context(), paid.space, paid.row))
	paid.pay(t, "copay", domain.NewDate(2026, time.March, 1), "140.00")

	made, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)
	require.Equal(t, 1, made, "the row is the payment")
	require.Empty(t, receiptLinks(t, paid.space, paid.row.ID), "but it paid no statement issued yet")
}

func TestDeletingThePaidRowReleasesThePaymentAndItsReceipt(t *testing.T) {
	paid := newPaidStatement(t)
	paid.pay(t, "p1", domain.NewDate(2026, time.March, 10), "140.00")
	_, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)

	require.NoError(t, db(t).DeleteTransaction(t.Context(), paid.space, paid.row.ID))
	require.Empty(t, receiptLinks(t, paid.space, paid.row.ID))
	payments, err := db(t).ListBillPayments(t.Context(), paid.space, paid.subaccount.ID)
	require.NoError(t, err)
	require.Len(t, payments, 1)
	require.Equal(t, uuid.Nil, payments[0].TransactionID)
}

func TestAPaymentReadAgainWithAnotherAmountLosesItsRow(t *testing.T) {
	paid := newPaidStatement(t)
	paid.pay(t, "p1", domain.NewDate(2026, time.March, 10), "140.00")
	_, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)
	require.Len(t, receiptLinks(t, paid.space, paid.row.ID), 1)

	paid.pay(t, "p1", domain.NewDate(2026, time.March, 10), "42.50")
	require.Empty(t, receiptLinks(t, paid.space, paid.row.ID))
	payments, err := db(t).ListBillPayments(t.Context(), paid.space, paid.subaccount.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, payments[0].TransactionID)
}

func TestTheDailyPassKeepsAPaidStatement(t *testing.T) {
	paid := newPaidStatement(t)
	paid.pay(t, "p1", domain.NewDate(2026, time.March, 10), "140.00")
	_, err := db(t).MatchBillPayments(t.Context(), paid.space)
	require.NoError(t, err)

	added, removed, err := db(t).ReconcileSpaceReceipts(t.Context(), paid.space)
	require.NoError(t, err)
	require.Zero(t, added)
	require.Zero(t, removed)
	require.Len(t, receiptLinks(t, paid.space, paid.row.ID), 1)
}
