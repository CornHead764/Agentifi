package store

import (
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// TestReadsAreSpaceScoped: an id from one space finds nothing in another.
func TestReadsAreSpaceScoped(t *testing.T) {
	ctx := t.Context()
	first := newSpace(t)
	second := newSpace(t)

	mine := newAccount(t, first, "Mine")
	theirs := newAccount(t, second, "Theirs")

	_, err := db(t).GetAccount(ctx, first, theirs.ID)
	require.ErrorIs(t, err, ErrNotFound)

	accounts, err := db(t).ListAccounts(ctx, first, AccountQuery{})
	require.NoError(t, err)
	require.Len(t, accounts, 1)
	require.Equal(t, mine.ID, accounts[0].ID)

	accounts, err = db(t).ListAccounts(ctx, first, AccountQuery{IDs: []uuid.UUID{theirs.ID}})
	require.NoError(t, err)
	require.Empty(t, accounts)

	category := newCategory(t, second, "Groceries", domain.CategoryExpense)
	_, err = db(t).GetCategory(ctx, first, category.ID)
	require.ErrorIs(t, err, ErrNotFound)

	tag := newTag(t, second, "vacation")
	_, err = db(t).GetTag(ctx, first, tag.ID)
	require.ErrorIs(t, err, ErrNotFound)
}

// TestWritesAreSpaceScoped: a write aimed at another space's row changes
// nothing and says so.
func TestWritesAreSpaceScoped(t *testing.T) {
	ctx := t.Context()
	first := newSpace(t)
	second := newSpace(t)
	theirs := newAccount(t, second, "Theirs")

	rename := *theirs
	rename.Name = "Stolen"
	require.ErrorIs(t, db(t).UpdateAccount(ctx, first, &rename), ErrNotFound)
	require.ErrorIs(t, db(t).DeleteAccount(ctx, first, theirs.ID), ErrNotFound)

	unchanged, err := db(t).GetAccount(ctx, second, theirs.ID)
	require.NoError(t, err)
	require.Equal(t, "Theirs", unchanged.Name)
	require.False(t, unchanged.IsDeleted)
}

// TestTransactionsAreSpaceScoped covers the ledger, including join tables that
// have no space_id and reach the tenant through their parent rows.
func TestTransactionsAreSpaceScoped(t *testing.T) {
	ctx := t.Context()
	first := newSpace(t)
	second := newSpace(t)

	account := newAccount(t, second, "Theirs")
	txn := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 1, 2),
		Amount:        domain.MustFromString("-25.00"),
		Currency:      "USD",
		StatementName: "COFFEE",
		Payee:         "Coffee",
		Source:        domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, second, txn))

	_, err := db(t).GetTransaction(ctx, first, txn.ID)
	require.ErrorIs(t, err, ErrNotFound)

	found, err := db(t).ListTransactions(ctx, first, TransactionQuery{})
	require.NoError(t, err)
	require.Empty(t, found)

	require.ErrorIs(t, db(t).DeleteTransaction(ctx, first, txn.ID), ErrNotFound)

	// The tag insert selects the tag through its own space and finds nothing.
	foreignTag := newTag(t, first, "foreign")
	require.NoError(t, db(t).SetTransactionTags(ctx, second, txn.ID, []uuid.UUID{foreignTag.ID}))
	read, err := db(t).GetTransaction(ctx, second, txn.ID)
	require.NoError(t, err)
	require.Empty(t, read.TagIDs)
}

// TestResolveSpace: an explicit choice, else the oldest accepted membership,
// and no fallback otherwise.
func TestResolveSpace(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)

	older := newSpace(t)
	newer := newSpace(t)
	accepted := time.Now().UTC()

	require.NoError(t, db(t).CreateMembership(ctx, older,
		&Membership{UserID: user.ID, Role: RoleOwner, AcceptedAt: &accepted}))
	require.NoError(t, db(t).CreateMembership(ctx, newer,
		&Membership{UserID: user.ID, Role: RoleViewer, AcceptedAt: &accepted}))

	space, membership, err := db(t).ResolveSpace(ctx, user.ID, nil)
	require.NoError(t, err)
	require.Equal(t, older, space.ID)
	require.Equal(t, RoleOwner, membership.Role)
	require.True(t, membership.Role.CanWrite())

	space, membership, err = db(t).ResolveSpace(ctx, user.ID, &newer)
	require.NoError(t, err)
	require.Equal(t, newer, space.ID)
	require.False(t, membership.Role.CanWrite(), "a viewer may read and nothing else")

	// Not found — never "the first space", and never a permission error that
	// would confirm it exists.
	stranger := newSpace(t)
	_, _, err = db(t).ResolveSpace(ctx, user.ID, &stranger)
	require.ErrorIs(t, err, ErrNotFound)
}

func TestResolveSpaceIgnoresUnacceptedAndDeleted(t *testing.T) {
	ctx := t.Context()
	user := newUser(t)
	invitedOnly := newSpace(t)
	invited := time.Now().UTC()

	require.NoError(t, db(t).CreateMembership(ctx, invitedOnly,
		&Membership{UserID: user.ID, Role: RoleMember, InvitedAt: &invited}))

	_, _, err := db(t).ResolveSpace(ctx, user.ID, nil)
	require.ErrorIs(t, err, ErrNotFound, "an outstanding invitation grants nothing")

	deleted := newSpace(t)
	accepted := time.Now().UTC()
	require.NoError(t, db(t).CreateMembership(ctx, deleted,
		&Membership{UserID: user.ID, Role: RoleOwner, AcceptedAt: &accepted}))
	space, err := db(t).GetSpace(ctx, deleted)
	require.NoError(t, err)
	space.IsDeleted = true
	require.NoError(t, db(t).UpdateSpace(ctx, &space))

	_, _, err = db(t).ResolveSpace(ctx, user.ID, nil)
	require.ErrorIs(t, err, ErrNotFound)

	spaces, err := db(t).ListSpacesForUser(ctx, user.ID)
	require.NoError(t, err)
	require.Empty(t, spaces)
}

// TestInTxRollsBack: a failed multi-statement write leaves nothing behind.
func TestInTxRollsBack(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Rollback")

	sentinel := errors.New("deliberate")
	err := db(t).InTx(ctx, func(tx *Store) error {
		txn := &Transaction{
			AccountID:     account.ID,
			Date:          domain.NewDate(2026, 2, 1),
			Amount:        domain.MustFromString("-10.00"),
			Currency:      "USD",
			StatementName: "GONE",
			Payee:         "Gone",
			Source:        domain.SourceManual,
		}
		if err := tx.CreateTransaction(ctx, spaceID, txn); err != nil {
			return err
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)

	txns, err := db(t).ListTransactions(ctx, spaceID, TransactionQuery{})
	require.NoError(t, err)
	require.Empty(t, txns, "the nested write committed despite the outer failure")
}
