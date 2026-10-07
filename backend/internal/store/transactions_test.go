package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// TestSplitsAndTagsRoundTrip checks that every read path returns a
// transaction whole, split tags included.
func TestSplitsAndTagsRoundTrip(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)
	household := newCategory(t, spaceID, "Household", domain.CategoryExpense)
	weekly := newTag(t, spaceID, "weekly")
	reimbursable := newTag(t, spaceID, "reimbursable")

	txn := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 4, 1),
		EffectiveDate: domain.NewDate(2026, 4, 25),
		Amount:        domain.MustFromString("-84.00"),
		Currency:      "USD",
		StatementName: "SQ *CORNER MARKET 0042",
		Payee:         "Corner Market",
		CategoryID:    groceries.ID,
		Source:        domain.SourceManual,
		TagIDs:        []uuid.UUID{weekly.ID},
		Splits: []Split{
			{Amount: domain.MustFromString("-60.00"), CategoryID: groceries.ID, Memo: "food"},
			{
				Amount:     domain.MustFromString("-24.00"),
				CategoryID: household.ID,
				Memo:       "soap",
				TagIDs:     []uuid.UUID{reimbursable.ID},
			},
		},
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, txn))

	read, err := db(t).GetTransaction(ctx, spaceID, txn.ID)
	require.NoError(t, err)

	require.Equal(t, "SQ *CORNER MARKET 0042", read.StatementName)
	require.Equal(t, "Corner Market", read.Payee)
	require.Equal(t, domain.NewDate(2026, 4, 25), read.EffectiveDate)
	require.Equal(t, []uuid.UUID{weekly.ID}, read.TagIDs)

	require.Len(t, read.Splits, 2)
	require.Equal(t, 0, read.Splits[0].Position)
	require.Equal(t, "-60.00", read.Splits[0].Amount.String())
	require.Empty(t, read.Splits[0].TagIDs)
	require.Equal(t, "-24.00", read.Splits[1].Amount.String())
	require.Equal(t, []uuid.UUID{reimbursable.ID}, read.Splits[1].TagIDs)

	total := domain.Sum(read.Splits, func(s Split) domain.Money { return s.Amount })
	require.True(t, total.Equal(read.Amount))
}

// TestReplaceSplitsClearsOldTags checks that swapping a transaction's splits
// takes their tags with them, rather than leaving rows pointing at splits that
// no longer exist.
func TestReplaceSplitsClearsOldTags(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	tag := newTag(t, spaceID, "reimbursable")

	txn := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 4, 1),
		Amount:        domain.MustFromString("-50.00"),
		Currency:      "USD",
		StatementName: "SPLIT ME",
		Payee:         "Split me",
		Source:        domain.SourceManual,
		Splits: []Split{
			{Amount: domain.MustFromString("-50.00"), TagIDs: []uuid.UUID{tag.ID}},
		},
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, txn))

	txn.Splits = []Split{
		{Amount: domain.MustFromString("-30.00")},
		{Amount: domain.MustFromString("-20.00")},
	}
	require.NoError(t, db(t).ReplaceSplits(ctx, spaceID, txn))

	read, err := db(t).GetTransaction(ctx, spaceID, txn.ID)
	require.NoError(t, err)
	require.Len(t, read.Splits, 2)
	for _, split := range read.Splits {
		require.Empty(t, split.TagIDs)
	}
}

// TestListByDateMode checks that a query on the effective date still finds
// rows that never needed one.
func TestListByDateMode(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Card")

	// Charged in March, lands on the April statement.
	deferred := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 3, 28),
		EffectiveDate: domain.NewDate(2026, 4, 15),
		Amount:        domain.MustFromString("-40.00"),
		Currency:      "USD",
		StatementName: "MARCH CHARGE",
		Payee:         "March charge",
		Source:        domain.SourceManual,
	}
	plain := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 4, 2),
		Amount:        domain.MustFromString("-10.00"),
		Currency:      "USD",
		StatementName: "APRIL CHARGE",
		Payee:         "April charge",
		Source:        domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, deferred))
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, plain))

	april := TransactionQuery{
		From: domain.NewDate(2026, 4, 1),
		To:   domain.NewDate(2026, 4, 30),
	}
	posted, err := db(t).ListTransactions(ctx, spaceID, april)
	require.NoError(t, err)
	require.Len(t, posted, 1)
	require.Equal(t, plain.ID, posted[0].ID)

	april.DateMode = domain.DateEffective
	effective, err := db(t).ListTransactions(ctx, spaceID, april)
	require.NoError(t, err)
	require.Len(t, effective, 2, "a row with no effective date falls back to its posted date")
}

// TestEstimatesAreHiddenUntilAskedFor checks that forecast bills stay out of
// the register, balances and reports unless the caller asks for them.
func TestEstimatesAreHiddenUntilAskedFor(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")

	posted := &Transaction{
		AccountID:     account.ID,
		Date:          domain.NewDate(2026, 8, 15),
		Amount:        domain.MustFromString("-40.00"),
		Currency:      "USD",
		StatementName: "MORTGAGE",
		Payee:         "Mortgage",
		Source:        domain.SourceManual,
	}
	// As Simplifi's import writes it: materialized on its due date, unpaid.
	forecast := &Transaction{
		AccountID:      account.ID,
		Date:           domain.NewDate(2027, 4, 1),
		Amount:         domain.MustFromString("-2345.67"),
		Currency:       "USD",
		StatementName:  "MORTGAGE",
		Payee:          "Mortgage",
		Source:         domain.SourceSimplifiImport,
		EstimateStatus: ProjectedEstimate,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, posted))
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, forecast))

	live, err := db(t).ListTransactions(ctx, spaceID, TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, live, 1, "a forecast is not what happened")
	require.Equal(t, posted.ID, live[0].ID)

	all, err := db(t).ListTransactions(ctx, spaceID, TransactionQuery{IncludeEstimates: true})
	require.NoError(t, err)
	require.Len(t, all, 2)
}

// TestDeleteReleasesTransferPair checks the trap on the schema: a leg whose
// partner is deleted must not keep pointing at it, or it stays excluded from
// profit and loss forever and can never be re-matched.
func TestDeleteReleasesTransferPair(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	checking := newAccount(t, spaceID, "Checking")
	savings := newAccount(t, spaceID, "Savings")

	// Both legs carry the same token rather than each other's ids, as the real
	// pairing does; a test modelled on ids would pass alongside a wrong query.
	pair := uuid.New()

	out := &Transaction{
		AccountID: checking.ID, Date: domain.NewDate(2026, 5, 1),
		Amount: domain.MustFromString("-500.00"), Currency: "USD",
		StatementName: "TRANSFER OUT", Payee: "Transfer", Source: domain.SourceManual,
		TransferPairID: pair,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, out))

	in := &Transaction{
		AccountID: savings.ID, Date: domain.NewDate(2026, 5, 1),
		Amount: domain.MustFromString("500.00"), Currency: "USD",
		StatementName: "TRANSFER IN", Payee: "Transfer", Source: domain.SourceManual,
		TransferPairID: pair,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, in))

	require.NoError(t, db(t).DeleteTransaction(ctx, spaceID, out.ID))

	survivor, err := db(t).GetTransaction(ctx, spaceID, in.ID)
	require.NoError(t, err)
	require.Equal(t, uuid.Nil, survivor.TransferPairID,
		"the surviving leg kept its pair token, so it is excluded from profit and loss forever and can never re-match")
}

func TestLoadPostings(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)

	categorized := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 6, 1),
		Amount: domain.MustFromString("-31.50"), Currency: "USD",
		StatementName: "MARKET", Payee: "Market", CategoryID: groceries.ID,
		Source: domain.SourceManual,
	}
	uncategorized := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 6, 2),
		Amount: domain.MustFromString("-9.00"), Currency: "USD",
		StatementName: "UNKNOWN", Payee: "Unknown", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, categorized))
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, uncategorized))

	postings, err := db(t).LoadPostings(ctx, spaceID, TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, postings, 2)

	byID := map[domain.ID]domain.Posting{}
	for _, posting := range postings {
		byID[posting.Txn.ID] = posting
		require.Equal(t, domain.ID(account.ID.String()), posting.Account.ID)
	}

	require.True(t, byID[domain.ID(categorized.ID.String())].HasCategory)
	require.Equal(t, "Groceries", byID[domain.ID(categorized.ID.String())].Category.Name)
	require.False(t, byID[domain.ID(uncategorized.ID.String())].HasCategory,
		"an uncategorized row is not a row whose category happens to be the zero value")
}

// TestBuildPostingsRejectsMissingAccount checks the error that catches a
// tenancy bug: transactions and accounts loaded with different filters would
// otherwise surface as a total that is quietly short.
func TestBuildPostingsRejectsMissingAccount(t *testing.T) {
	txns := []Transaction{{ID: uuid.New(), AccountID: uuid.New()}}
	_, err := BuildPostings(txns, nil, nil)
	require.ErrorContains(t, err, "unknown account")
}

func TestBuildPostingsRejectsMissingCategory(t *testing.T) {
	account := Account{ID: uuid.New()}
	txn := Transaction{
		ID: uuid.New(), AccountID: account.ID,
		CategoryID: uuid.New(),
	}
	_, err := BuildPostings([]Transaction{txn}, []Account{account}, nil)
	require.ErrorContains(t, err, "category")
}

// TestSetTransactionBalancesLeavesAllocationsAlone checks the narrow write on
// the register's balance column: it must not rewrite splits, split tags or
// tags, since a back-dated insert moves thousands of rows.
func TestSetTransactionBalancesLeavesAllocationsAlone(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)
	weekly := newTag(t, spaceID, "weekly")

	first := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 5, 1),
		Amount: domain.MustFromString("-40.00"), Currency: "USD",
		StatementName: "ONE", Payee: "One", Source: domain.SourceManual,
		TagIDs: []uuid.UUID{weekly.ID},
		Splits: []Split{{
			Amount: domain.MustFromString("-40.00"), CategoryID: groceries.ID,
			TagIDs: []uuid.UUID{weekly.ID},
		}},
	}
	second := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 5, 2),
		Amount: domain.MustFromString("-10.00"), Currency: "USD",
		StatementName: "TWO", Payee: "Two", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, first))
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, second))
	// The split's creation stamp is what tells a rewritten row from an
	// untouched one: replaceSplits reuses the id it was handed, so the id alone
	// cannot say whether the row was deleted and reinserted.
	born := splitBornAt(t, first.Splits[0].ID)

	require.NoError(t, db(t).SetTransactionBalances(ctx, spaceID, map[uuid.UUID]domain.Money{
		first.ID:  domain.MustFromString("460.00"),
		second.ID: domain.MustFromString("450.00"),
	}))

	read, err := db(t).GetTransaction(ctx, spaceID, first.ID)
	require.NoError(t, err)
	require.True(t, read.HasBalance)
	require.Equal(t, "460.00", read.Balance.String())
	require.Equal(t, []uuid.UUID{weekly.ID}, read.TagIDs)
	require.Len(t, read.Splits, 1)
	require.Equal(t, []uuid.UUID{weekly.ID}, read.Splits[0].TagIDs)
	require.Equal(t, born, splitBornAt(t, read.Splits[0].ID),
		"the split was deleted and reinserted to write one column on its parent")

	other, err := db(t).GetTransaction(ctx, spaceID, second.ID)
	require.NoError(t, err)
	require.Equal(t, "450.00", other.Balance.String())
}

// TestSetTransactionBalancesStaysInItsSpace checks that a row id from another
// household is not reachable through the batched write.
func TestSetTransactionBalancesStaysInItsSpace(t *testing.T) {
	ctx := t.Context()
	mine := newSpace(t)
	theirs := newSpace(t)
	account := newAccount(t, theirs, "Their Checking")

	stranger := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 5, 1),
		Amount: domain.MustFromString("-40.00"), Currency: "USD",
		StatementName: "NOT YOURS", Payee: "Not Yours", Source: domain.SourceManual,
	}
	require.NoError(t, db(t).CreateTransaction(ctx, theirs, stranger))

	require.NoError(t, db(t).SetTransactionBalances(ctx, mine, map[uuid.UUID]domain.Money{
		stranger.ID: domain.MustFromString("999.00"),
	}))
	require.NoError(t, db(t).SetTransactionsReviewed(ctx, mine, []uuid.UUID{stranger.ID}, true))

	read, err := db(t).GetTransaction(ctx, theirs, stranger.ID)
	require.NoError(t, err)
	require.False(t, read.HasBalance)
	require.False(t, read.IsReviewed)
}

// TestSetTransactionsReviewedLeavesAllocationsAlone is the same property for
// the review queue's bulk button, which acts on a whole query at a time.
func TestSetTransactionsReviewedLeavesAllocationsAlone(t *testing.T) {
	ctx := t.Context()
	spaceID := newSpace(t)
	account := newAccount(t, spaceID, "Checking")
	groceries := newCategory(t, spaceID, "Groceries", domain.CategoryExpense)
	weekly := newTag(t, spaceID, "weekly")

	txn := &Transaction{
		AccountID: account.ID, Date: domain.NewDate(2026, 5, 1),
		Amount: domain.MustFromString("-40.00"), Currency: "USD",
		StatementName: "ONE", Payee: "One", Source: domain.SourceManual,
		TagIDs: []uuid.UUID{weekly.ID},
		Splits: []Split{{
			Amount: domain.MustFromString("-40.00"), CategoryID: groceries.ID,
			TagIDs: []uuid.UUID{weekly.ID},
		}},
	}
	require.NoError(t, db(t).CreateTransaction(ctx, spaceID, txn))
	born := splitBornAt(t, txn.Splits[0].ID)

	require.NoError(t, db(t).SetTransactionsReviewed(ctx, spaceID, []uuid.UUID{txn.ID}, true))
	read, err := db(t).GetTransaction(ctx, spaceID, txn.ID)
	require.NoError(t, err)
	require.True(t, read.IsReviewed)
	require.Equal(t, []uuid.UUID{weekly.ID}, read.TagIDs)
	require.Len(t, read.Splits, 1)
	require.Equal(t, []uuid.UUID{weekly.ID}, read.Splits[0].TagIDs)
	require.Equal(t, born, splitBornAt(t, read.Splits[0].ID),
		"the split was deleted and reinserted to write one column on its parent")

	require.NoError(t, db(t).SetTransactionsReviewed(ctx, spaceID, []uuid.UUID{txn.ID}, false))
	read, err = db(t).GetTransaction(ctx, spaceID, txn.ID)
	require.NoError(t, err)
	require.False(t, read.IsReviewed)
}

func splitBornAt(t *testing.T, splitID uuid.UUID) time.Time {
	t.Helper()
	var born time.Time
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT created_at FROM transaction_splits WHERE id = $1`, splitID).Scan(&born))
	return born
}
