package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// Invented lunch purchases deducted from pay, each with the income row a
// padding mail rule writes beside it.

func mailRow(t *testing.T, space SpaceID, accountID uuid.UUID, externalID, amount string) *Transaction {
	t.Helper()
	txn := &Transaction{
		AccountID: accountID, ExternalID: externalID, Date: domain.NewDate(2026, 9, 14),
		Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: "Lunch", Payee: "Lunch", Source: domain.SourceEmail,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), space, txn))
	return txn
}

func reread(t *testing.T, space SpaceID, id uuid.UUID) Transaction {
	t.Helper()
	row, err := db(t).GetTransaction(t.Context(), space, id)
	require.NoError(t, err)
	return row
}

func TestAPadLinksToItsPurchaseOnceAndOnlyInItsSpace(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Lunch Card")
	purchase := mailRow(t, space, account.ID, "mail:<a1@lunch.example.invalid>", "-6.50")
	pad := mailRow(t, space, account.ID, "mail:<a1@lunch.example.invalid>:income", "6.50")

	require.NoError(t, db(t).LinkPadding(t.Context(), space, pad.ID, purchase.ID))
	require.NoError(t, db(t).LinkPadding(t.Context(), space, pad.ID, purchase.ID), "idempotent")
	require.Equal(t, purchase.ID, reread(t, space, pad.ID).PaddedTxnID)
	require.Equal(t, uuid.Nil, reread(t, space, purchase.ID).PaddedTxnID)

	padding, err := db(t).PaddingOf(t.Context(), space, []uuid.UUID{purchase.ID, pad.ID})
	require.NoError(t, err)
	require.Equal(t, map[uuid.UUID]uuid.UUID{purchase.ID: pad.ID}, padding)

	second := mailRow(t, space, account.ID, "mail:<a2@lunch.example.invalid>:income", "6.50")
	require.NoError(t, db(t).LinkPadding(t.Context(), space, second.ID, purchase.ID))
	require.Equal(t, uuid.Nil, reread(t, space, second.ID).PaddedTxnID, "a purchase has one pad")

	other := newSpace(t)
	foreign := mailRow(t, other, newAccount(t, other, "Elsewhere").ID, "mail:<a3@lunch.example.invalid>", "-6.50")
	require.NoError(t, db(t).LinkPadding(t.Context(), space, second.ID, foreign.ID))
	require.Equal(t, uuid.Nil, reread(t, space, second.ID).PaddedTxnID, "another space's row is not linked")
	padding, err = db(t).PaddingOf(t.Context(), other, []uuid.UUID{purchase.ID})
	require.NoError(t, err)
	require.Empty(t, padding)
}

func TestDeletingAPurchaseDeletesItsPad(t *testing.T) {
	space := newSpace(t)
	lunchCard := newAccount(t, space, "Lunch Card")
	payroll := newAccount(t, space, "Payroll")
	purchase := mailRow(t, space, lunchCard.ID, "mail:<b1@lunch.example.invalid>", "-8.50")
	pad := mailRow(t, space, payroll.ID, "mail:<b1@lunch.example.invalid>:income", "8.50")
	require.NoError(t, db(t).LinkPadding(t.Context(), space, pad.ID, purchase.ID))

	require.NoError(t, db(t).DeleteTransaction(t.Context(), space, purchase.ID))
	gone := reread(t, space, pad.ID)
	require.True(t, gone.IsDeleted, "the pad does not outlive its purchase")
	require.Equal(t, uuid.Nil, gone.PaddedTxnID)
	require.Equal(t, "mail:<b1@lunch.example.invalid>:income", gone.ExternalID,
		"kept, so a re-read of the mail finds it rather than posting it again")
}

func TestDeletingAPadLeavesItsPurchase(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Lunch Card")
	purchase := mailRow(t, space, account.ID, "mail:<c1@lunch.example.invalid>", "-3.00")
	pad := mailRow(t, space, account.ID, "mail:<c1@lunch.example.invalid>:income", "3.00")
	require.NoError(t, db(t).LinkPadding(t.Context(), space, pad.ID, purchase.ID))

	require.NoError(t, db(t).DeleteTransaction(t.Context(), space, pad.ID))
	require.False(t, reread(t, space, purchase.ID).IsDeleted)
	padding, err := db(t).PaddingOf(t.Context(), space, []uuid.UUID{purchase.ID})
	require.NoError(t, err)
	require.Empty(t, padding, "the purchase is not padded")
}

func TestRetiringAPurchaseAsADuplicateReleasesItsPad(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Lunch Card")
	purchase := mailRow(t, space, account.ID, "mail:<d1@lunch.example.invalid>", "-5.00")
	pad := mailRow(t, space, account.ID, "mail:<d1@lunch.example.invalid>:income", "5.00")
	require.NoError(t, db(t).LinkPadding(t.Context(), space, pad.ID, purchase.ID))

	require.NoError(t, db(t).RetireDuplicate(t.Context(), space, purchase.ID))
	kept := reread(t, space, pad.ID)
	require.False(t, kept.IsDeleted, "the survivor is still the money the pad restores")
	require.Equal(t, uuid.Nil, kept.PaddedTxnID)
}
