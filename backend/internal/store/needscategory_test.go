package store

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// splitRow writes splits over a -42.00 row, one per category id; uuid.Nil
// leaves that part unfiled.
func splitRow(t *testing.T, space SpaceID, txn *Transaction, categories ...uuid.UUID) {
	t.Helper()
	share, exact := domain.MustFromString("-42.00").DivInt(len(categories))
	require.True(t, exact)
	txn.Splits = nil
	for i, category := range categories {
		txn.Splits = append(txn.Splits, Split{Position: i, Amount: share, CategoryID: category})
	}
	txn.CategoryID = uuid.Nil
	require.NoError(t, db(t).UpdateTransaction(t.Context(), space, txn))
}

func TestNeedsCategorySQLAsksASplitRowPerSplit(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday")
	gifts := newCategory(t, space, "Gifts", domain.CategoryExpense)
	household := newCategory(t, space, "Household", domain.CategoryExpense)

	bare := newTransactionFor(t, space, account.ID, "Corner Kiosk")
	filed := newTransactionFor(t, space, account.ID, "Pottery Barn Outlet")
	filed.CategoryID = household.ID
	require.NoError(t, db(t).UpdateTransaction(t.Context(), space, filed))
	splitFiled := newTransactionFor(t, space, account.ID, "Payment to Uncle Ned")
	splitRow(t, space, splitFiled, gifts.ID, household.ID, gifts.ID)
	splitPartial := newTransactionFor(t, space, account.ID, "Payment to Aunt Bea")
	splitRow(t, space, splitPartial, gifts.ID, uuid.Nil)
	leg := newTransactionFor(t, space, account.ID, "Savings Transfer")
	_, err := db(t).Pool().Exec(t.Context(),
		`UPDATE transactions SET transfer_pair_id = $2 WHERE id = $1`, leg.ID, uuid.New())
	require.NoError(t, err)

	needing, err := queryAll(t.Context(), db(t).db, "test: needs category", scanValue[uuid.UUID],
		`SELECT t.id FROM transactions t WHERE t.space_id = $1 AND `+needsCategorySQL,
		space.UUID())
	require.NoError(t, err)
	require.ElementsMatch(t, []uuid.UUID{bare.ID, splitPartial.ID}, needing,
		"an unsplit row with no category, and a split with a part unfiled")

	for _, txn := range []*Transaction{bare, filed, splitFiled, splitPartial, leg} {
		read, err := db(t).GetTransaction(t.Context(), space, txn.ID)
		require.NoError(t, err)
		require.Equal(t, read.ID == bare.ID || read.ID == splitPartial.ID,
			DomainTransaction(read).IsUncategorized(), "the SQL and the domain agree on %s", read.Payee)
	}
}

func TestAFullyFiledSplitRowTakesNoUndeterminedMark(t *testing.T) {
	space := newSpace(t)
	account := newAccount(t, space, "Everyday")
	gifts := newCategory(t, space, "Gifts", domain.CategoryExpense)
	automation := &Automation{
		CreatedBy: newUser(t).ID, Name: "Check", IsEnabled: true,
		Trigger: domain.AutomationTriggerTransaction, Prompt: "Check the category.",
		Mode: domain.AutomationModePropose,
	}
	require.NoError(t, db(t).CreateAutomation(t.Context(), space, automation))

	splitFiled := newTransactionFor(t, space, account.ID, "Payment to Uncle Ned")
	splitRow(t, space, splitFiled, gifts.ID, gifts.ID)
	splitPartial := newTransactionFor(t, space, account.ID, "Payment to Aunt Bea")
	splitRow(t, space, splitPartial, gifts.ID, uuid.Nil)

	for _, txn := range []*Transaction{splitFiled, splitPartial} {
		run := &AutomationRun{
			AutomationID: automation.ID, FiredBy: "manual", TransactionID: txn.ID, Subject: txn.Payee,
		}
		_, err := db(t).QueueAutomationRun(t.Context(), space, run)
		require.NoError(t, err)
		require.NoError(t, db(t).MarkCategoryUndetermined(t.Context(), space, txn.ID,
			time.Now().UTC(), "Could not guess.", run.ID))
	}

	checked, _, _ := categoryCheckMark(t, splitFiled.ID)
	require.False(t, checked, "every split is filed, so nothing is undetermined")
	checked, _, _ = categoryCheckMark(t, splitPartial.ID)
	require.True(t, checked, "a split with a part unfiled still waits for a category")
}

func categoryCheckMark(t *testing.T, id uuid.UUID) (bool, string, uuid.UUID) {
	t.Helper()
	var checked bool
	var note string
	var runID *uuid.UUID
	require.NoError(t, db(t).Pool().QueryRow(t.Context(),
		`SELECT category_checked_at IS NOT NULL, category_check_note, category_check_run_id
		   FROM transactions WHERE id = $1`, id).Scan(&checked, &note, &runID))
	if runID == nil {
		return checked, note, uuid.Nil
	}
	return checked, note, *runID
}
