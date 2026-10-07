package store

import (
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

func cardAccount(t *testing.T, spaceID SpaceID, name string) *Account {
	t.Helper()
	account := &Account{
		Name: name, Kind: domain.KindCreditCard, Type: "credit_card", Currency: "USD",
		IncludeInNetWorth: true,
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, account))
	return account
}

func movedRow(t *testing.T, spaceID SpaceID, account *Account, amount string, category uuid.UUID) *Transaction {
	t.Helper()
	txn := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 3, 4),
		Amount: domain.MustFromString(amount), Currency: "USD",
		StatementName: "ONLINE TRANSFER", Payee: "Transfer",
		CategoryID: category, Source: domain.SourceSync,
	}
	require.NoError(t, db(t).CreateTransaction(t.Context(), spaceID, txn))
	return txn
}

func markedCategory(t *testing.T, spaceID SpaceID, marker string) Category {
	t.Helper()
	var found []Category
	all, err := db(t).ListCategories(t.Context(), spaceID, false)
	require.NoError(t, err)
	for _, one := range all {
		if one.KnownCategoryID == marker {
			found = append(found, one)
		}
	}
	require.Len(t, found, 1, marker)
	return found[0]
}

type filing struct {
	categoryID uuid.UUID
	fromPair   bool
}

func filingOf(t *testing.T, id uuid.UUID) filing {
	t.Helper()
	var category *uuid.UUID
	var out filing
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT category_id, category_from_pair FROM transactions WHERE id = $1`, id).
		Scan(&category, &out.fromPair))
	if category != nil {
		out.categoryID = *category
	}
	return out
}

func TestPairingFilesTheLegsAndReleasingTakesTheFilingBack(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	checking := newAccount(t, spaceID, "Checking")
	card := cardAccount(t, spaceID, "Card")
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)

	out, in := movedRow(t, spaceID, checking, "-120.00", uuid.Nil),
		movedRow(t, spaceID, card, "120.00", groceries.ID)
	pairID, ok, err := db(t).PairTransactions(ctx, spaceID, out.ID, in.ID, false)
	require.NoError(t, err)
	require.True(t, ok)

	payment := markedCategory(t, spaceID, domain.KnownCategoryCreditCardPayment).ID
	require.Equal(t, filing{payment, true}, filingOf(t, out.ID))
	require.Equal(t, filing{groceries.ID, false}, filingOf(t, in.ID), "already categorized")

	released, err := db(t).UnlinkTransferPair(ctx, spaceID, pairID)
	require.NoError(t, err)
	require.Equal(t, 2, released)
	require.Equal(t, filing{}, filingOf(t, out.ID), "back to uncategorized")
	require.Equal(t, filing{groceries.ID, false}, filingOf(t, in.ID))
}

func TestACategoryAPersonSetsOnAPairedLegSurvivesTheRelease(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	checking := newAccount(t, spaceID, "Checking")
	savings := newAccount(t, spaceID, "Savings")
	gifts := newCategory(t, spaceID, "Gifts", domain.CategoryExpense)

	out, in := movedRow(t, spaceID, checking, "-75.00", uuid.Nil),
		movedRow(t, spaceID, savings, "75.00", uuid.Nil)
	pairID, ok, err := db(t).PairTransactions(ctx, spaceID, out.ID, in.ID, true)
	require.NoError(t, err)
	require.True(t, ok)
	transfer := markedCategory(t, spaceID, domain.KnownCategoryTransfer).ID
	require.Equal(t, filing{transfer, true}, filingOf(t, out.ID))

	row, err := db(t).GetTransaction(ctx, spaceID, out.ID)
	require.NoError(t, err)
	row.CategoryID = gifts.ID
	require.NoError(t, db(t).UpdateTransaction(ctx, spaceID, &row))
	require.Equal(t, filing{gifts.ID, false}, filingOf(t, out.ID), "any other write is a person's")

	// Choosing Transfer by hand is a choice too.
	row.CategoryID = transfer
	require.NoError(t, db(t).UpdateTransaction(ctx, spaceID, &row))
	require.Equal(t, filing{transfer, false}, filingOf(t, out.ID))

	_, err = db(t).UnlinkTransferPair(ctx, spaceID, pairID)
	require.NoError(t, err)
	require.Equal(t, filing{transfer, false}, filingOf(t, out.ID))
	require.Equal(t, filing{}, filingOf(t, in.ID))
}

func TestReleasingAnOrphanOrDeletingALegTakesTheFilingBack(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	checking := newAccount(t, spaceID, "Checking")
	savings := newAccount(t, spaceID, "Savings")

	out, in := movedRow(t, spaceID, checking, "-60.00", uuid.Nil),
		movedRow(t, spaceID, savings, "60.00", uuid.Nil)
	_, ok, err := db(t).PairTransactions(ctx, spaceID, out.ID, in.ID, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, db(t).DeleteTransaction(ctx, spaceID, out.ID))
	require.Equal(t, filing{}, filingOf(t, in.ID), "the survivor is ordinary money again")

	again, partner := movedRow(t, spaceID, checking, "-15.00", uuid.Nil),
		movedRow(t, spaceID, savings, "15.00", uuid.Nil)
	_, ok, err = db(t).PairTransactions(ctx, spaceID, again.ID, partner.ID, false)
	require.NoError(t, err)
	require.True(t, ok)
	require.NoError(t, db(t).ReleaseTransferLegs(ctx, spaceID, []uuid.UUID{partner.ID}))
	require.Equal(t, filing{}, filingOf(t, partner.ID))
}

func TestAProtectedCategoryIsNotDeleted(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	byMarker, err := db(t).EnsureTransferCategories(ctx, spaceID)
	require.NoError(t, err)
	system := &Category{Name: "Opening Balance", Kind: domain.CategoryExpense, IsEditable: false}
	require.NoError(t, db(t).CreateCategory(ctx, spaceID, system))
	plain := newCategory(t, spaceID, "Hobbies", domain.CategoryExpense)

	for _, id := range []uuid.UUID{byMarker[domain.KnownCategoryTransfer],
		byMarker[domain.KnownCategoryCreditCardPayment], system.ID} {
		require.ErrorIs(t, db(t).DeleteCategory(ctx, spaceID, id), ErrCategoryProtected)
		_, err := db(t).PruneCategories(ctx, spaceID, []uuid.UUID{id})
		require.ErrorIs(t, err, ErrCategoryProtected)
	}
	require.NoError(t, db(t).DeleteCategory(ctx, spaceID, plain.ID))
}

func TestRestoringTheDefaultsKeepsARenamedTransfer(t *testing.T) {
	ctx := t.Context()
	space := &Space{Name: t.Name()}
	require.NoError(t, db(t).CreateSeededSpace(ctx, space))
	transfer := markedCategory(t, space.ID, domain.KnownCategoryTransfer)
	transfer.Name = "Moving Money"
	require.NoError(t, db(t).UpdateCategory(ctx, space.ID, &transfer))

	_, err := db(t).SeedCategories(ctx, space.ID, domain.DefaultCategories)
	require.NoError(t, err)
	require.Equal(t, transfer.ID, markedCategory(t, space.ID, domain.KnownCategoryTransfer).ID)
	markedCategory(t, space.ID, domain.KnownCategoryCreditCardPayment)
}
