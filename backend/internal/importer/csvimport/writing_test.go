package csvimport

import (
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
)

// ledger is the fixture the writing tests import: two accounts, a three-level
// category, a transfer leg, a tagged split and two identical rows.
func ledger() []cells {
	first := split("Amazon Card", "Store", "STORE 123", "Shopping", " -10.00")
	first.tags = "Reimbursable"
	second := split("Amazon Card", "Store", "STORE 123", "Home:Tools", " -5.00")
	coffee := spend("Everyday Checking", "Coffee", "Food & Dining:Coffee Shops", " -4.50")
	return []cells{
		spend("Everyday Checking", "Fees", "Auto & Transport:Registration:Registration Fees", " -75.00"),
		spend("Everyday Checking", "Payment", "Amazon Card", " -50.00"),
		first, second,
		coffee, coffee,
	}
}

func writeLedger(t *testing.T, spaceID store.SpaceID, rows []cells) WriteResult {
	t.Helper()
	out := mapped(t, rows...)
	require.True(t, out.Report.OK(), out.Report.Errors)
	result, err := Write(t.Context(), db(t), spaceID, out)
	require.NoError(t, err)
	return result
}

func TestAWholeFileLandsWithItsAccountsCategoriesTagsAndSplits(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	result := writeLedger(t, spaceID, ledger())

	require.Equal(t, 2, result.AccountsCreated)
	require.Equal(t, 1, result.TagsCreated)
	require.Equal(t, 5, result.TransactionsWritten)
	require.Equal(t, 2, result.SplitsWritten)

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, txns, 5)

	total := domain.Sum(txns, func(txn store.Transaction) domain.Money { return txn.Amount })
	require.Equal(t, "-149.00", total.String())
}

// Importing the same file twice must not double anything. There is no id in
// the file, so this is the whole of the idempotency guarantee.
func TestASecondImportOfOneFileWritesNothing(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	first := writeLedger(t, spaceID, ledger())
	second := writeLedger(t, spaceID, ledger())

	require.Equal(t, 0, second.AccountsCreated)
	require.Equal(t, first.AccountsCreated, second.AccountsExisting)
	require.Equal(t, 0, second.CategoriesCreated)
	require.Equal(t, first.CategoriesCreated, second.CategoriesExisting)
	require.Equal(t, 0, second.TagsCreated)
	require.Equal(t, 0, second.TransactionsWritten)
	require.Equal(t, first.TransactionsWritten, second.TransactionsSkipped)
	require.Equal(t, 0, second.Rows())

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, txns, first.TransactionsWritten)

	accounts, err := db(t).ListAccounts(t.Context(), spaceID, store.AccountQuery{})
	require.NoError(t, err)
	require.Len(t, accounts, 2)
}

// A later export holds the same history plus what has happened since, and only
// the new rows may land.
func TestAnOverlappingFileAddsOnlyWhatIsNew(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	writeLedger(t, spaceID, ledger())

	later := append(ledger(), spend("Everyday Checking", "New", "Shopping", " -1.00"))
	second := writeLedger(t, spaceID, later)

	require.Equal(t, 1, second.TransactionsWritten)
	require.Equal(t, 5, second.TransactionsSkipped)

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, txns, 6)
}

// A row a previous import wrote and the user then deleted must not come back.
func TestADeletedRowIsNotResurrected(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	writeLedger(t, spaceID, ledger())

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.NoError(t, db(t).DeleteTransaction(t.Context(), spaceID, txns[0].ID))

	second := writeLedger(t, spaceID, ledger())
	require.Equal(t, 0, second.TransactionsWritten)

	live, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.Len(t, live, len(txns)-1)
}

// The category tree has to survive the round trip, including the middle level
// no transaction is filed against.
func TestTheCategoryHierarchyIsWrittenParentFirst(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	writeLedger(t, spaceID, ledger())

	categories, err := db(t).ListCategories(t.Context(), spaceID, false)
	require.NoError(t, err)
	byName := map[string]store.Category{}
	for _, category := range categories {
		byName[category.Name] = category
	}
	require.Equal(t, byName["Auto & Transport"].ID, byName["Registration"].ParentID)
	require.Equal(t, byName["Registration"].ID, byName["Registration Fees"].ParentID)

	// Simplifi files a transfer under the counterparty account's name, and a
	// transfer is neither income nor spending.
	require.Equal(t, domain.CategoryTransfer, byName["Amazon Card"].Kind)
}

// An account the space already holds is reused rather than duplicated.
func TestAnExistingAccountIsMatchedByNameAndNotDuplicated(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	existing := store.Account{
		Name: "everyday checking", Kind: domain.KindCash, Type: "checking", Currency: "USD",
	}
	require.NoError(t, db(t).CreateAccount(t.Context(), spaceID, &existing))

	result := writeLedger(t, spaceID, ledger())
	require.Equal(t, 1, result.AccountsCreated)
	require.Equal(t, 1, result.AccountsExisting)

	accounts, err := db(t).ListAccounts(t.Context(), spaceID, store.AccountQuery{})
	require.NoError(t, err)
	require.Len(t, accounts, 2)

	// The transactions went to the row that was already there, not to a second
	// account whose name differs only in case.
	txns, err := db(t).ListTransactions(t.Context(), spaceID,
		store.TransactionQuery{AccountIDs: []uuid.UUID{existing.ID}})
	require.NoError(t, err)
	require.Len(t, txns, 4)
}

func TestASplitsAmountsSumToItsParentInTheDatabase(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	writeLedger(t, spaceID, ledger())

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	found := false
	for _, txn := range txns {
		if len(txn.Splits) == 0 {
			continue
		}
		found = true
		require.Equal(t, "-15.00", txn.Amount.String())
		require.Equal(t, txn.Amount,
			domain.Sum(txn.Splits, func(s store.Split) domain.Money { return s.Amount }))
		require.Len(t, txn.Splits[0].TagIDs, 1)
	}
	require.True(t, found, "the fixture's split did not survive the write")
}

// A run the report refuses is never written, exactly as the IndexedDB
// importer behaves.
func TestARunWithErrorsWritesNothing(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	out := mapped(t, spend("Everyday Checking", "A", "Shopping", " twelve"))
	require.False(t, out.Report.OK())

	_, err := Write(t.Context(), db(t), spaceID, out)
	require.ErrorIs(t, err, ErrRefused)

	txns, err := db(t).ListTransactions(t.Context(), spaceID, store.TransactionQuery{})
	require.NoError(t, err)
	require.Empty(t, txns)
}

func TestTheReportNamesEveryInferenceItMade(t *testing.T) {
	out := mapped(t, ledger()...)
	rendered := Render(out)
	require.Contains(t, rendered, "Account kinds")
	require.Contains(t, rendered, "credit_card")
	require.Contains(t, rendered, "Categories that are not spending")
	require.Contains(t, strings.ToLower(rendered), "transfer")
}

// The settle runs after Write has committed, so a settle that fails must find
// its backlog on the rows, not in the slice Write returned.
func TestWrittenRowsCarryTheSettleMarkUntilSettled(t *testing.T) {
	spaceID := storetest.NewSpace(t)
	result := writeLedger(t, spaceID, ledger())

	pending, err := db(t).TransactionsNeedingSettle(t.Context(), spaceID)
	require.NoError(t, err)
	require.ElementsMatch(t, result.TransactionIDs, pending)

	require.NoError(t, db(t).ClearNeedsSettle(t.Context(), spaceID, result.TransactionIDs))
	pending, err = db(t).TransactionsNeedingSettle(t.Context(), spaceID)
	require.NoError(t, err)
	require.Empty(t, pending)
}
