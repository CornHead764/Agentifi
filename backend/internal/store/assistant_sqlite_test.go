package store_test

import (
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"testing"
	"time"
)

func TestSQLiteAssistant(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	user, stranger := uuid.New(), uuid.New()
	connection := store.AssistantConnection{Name: "Local", BaseURL: "http://localhost", Model: "test", IsEnabled: true}
	require.NoError(t, st.SaveAssistantConnection(ctx, a, &connection, ""))
	id := connection.ID
	connection.ID = uuid.Nil
	connection.Name = "Renamed"
	require.NoError(t, st.SaveAssistantConnection(ctx, a, &connection, ""))
	require.Equal(t, id, connection.ID)
	saved, err := st.GetAssistantConnection(ctx, a)
	require.NoError(t, err)
	require.Equal(t, "Renamed", saved.Name)
	require.False(t, saved.HasKey)
	require.False(t, saved.UpdatedAt.IsZero())
	_, err = st.GetAssistantConnection(ctx, b)
	require.ErrorIs(t, err, store.ErrNotFound)
	thread := store.AssistantConversation{UserID: user}
	require.NoError(t, st.CreateAssistantConversation(ctx, a, &thread))
	require.False(t, thread.CreatedAt.IsZero())
	require.NoError(t, st.TouchAssistantConversation(ctx, a, thread.ID, "Question"))
	got, err := st.GetAssistantConversation(ctx, a, user, thread.ID)
	require.NoError(t, err)
	require.Equal(t, "Question", got.Title)
	_, err = st.GetAssistantConversation(ctx, a, stranger, thread.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = st.GetAssistantConversation(ctx, b, user, thread.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	threads, err := st.ListAssistantConversations(ctx, a, user)
	require.NoError(t, err)
	require.Len(t, threads, 1)
	msg := store.AssistantMessage{ConversationID: thread.ID, Role: domain.AssistantRoleUser, Content: "Hello", ToolArguments: map[string]any{"x": "y"}}
	require.NoError(t, st.AddAssistantMessage(ctx, a, &msg))
	messages, err := st.ListAssistantMessages(ctx, a, thread.ID)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Equal(t, msg.ToolArguments, messages[0].ToolArguments)
	txn := uuid.New()
	action := store.AssistantAction{ConversationID: thread.ID, ToolName: "update_transaction", Method: "PATCH", Path: "/transactions/" + txn.String(), Body: map[string]any{"category_id": uuid.New().String()}, Preview: map[string]any{"confidence": 0.9}}
	require.NoError(t, st.CreateAssistantAction(ctx, a, &action))
	actions, err := st.ListAssistantActions(ctx, a, thread.ID)
	require.NoError(t, err)
	require.Len(t, actions, 1)
	require.Equal(t, action.Body, actions[0].Body)
	_, err = st.GetAssistantAction(ctx, a, stranger, action.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	ids, err := st.TransactionsWithSuggestion(ctx, a)
	require.NoError(t, err)
	require.True(t, ids[txn])
	pending, err := st.PendingActionsForTransactions(ctx, a, []uuid.UUID{txn})
	require.NoError(t, err)
	require.Equal(t, action.ID, pending[txn].Action.ID)
	claimed, err := st.ClaimAssistantAction(ctx, a, action.ID)
	require.NoError(t, err)
	require.Equal(t, domain.AssistantActionApplying, claimed.Status)
	require.NotNil(t, claimed.DecidedAt)
	_, err = st.ClaimAssistantAction(ctx, a, action.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	n, err := st.ReleaseStaleAssistantActions(ctx, a, thread.ID, time.Now().Add(time.Hour), "retry")
	require.NoError(t, err)
	require.EqualValues(t, 1, n)
	require.NoError(t, st.ReviseAssistantAction(ctx, a, action.ID, map[string]any{"category_id": "new"}, action.Body))
	_, err = st.ClaimAssistantAction(ctx, a, action.ID)
	require.NoError(t, err)
	require.NoError(t, st.FinishAssistantAction(ctx, a, action.ID, domain.AssistantActionApplied, "ok", 200, txn))
	action.ID = uuid.Nil
	require.NoError(t, st.CreateAssistantAction(ctx, a, &action))
	declined, err := st.DeclineSuggestionsOnRows(ctx, a, []uuid.UUID{txn}, "changed")
	require.NoError(t, err)
	require.Len(t, declined, 1)
	charge, _ := sqliteLedgerRows(t, st, a)
	correction := store.AssistantCorrection{ActionID: action.ID, TransactionID: charge, Payee: "Shop", StatementName: "SHOP", Amount: domain.MustFromString("12.34"), HasAmount: true, ToolName: "update_transaction", CorrectedBy: user}
	require.NoError(t, st.CreateAssistantCorrections(ctx, a, []store.AssistantCorrection{correction}))
	corrections, err := st.ListAssistantCorrections(ctx, a, 10)
	require.NoError(t, err)
	require.Len(t, corrections, 1)
	require.True(t, corrections[0].Amount.Equal(correction.Amount))
	require.NoError(t, st.DeleteAssistantConversation(ctx, a, user, thread.ID))
	require.NoError(t, st.DeleteAssistantConnection(ctx, a))
}

func TestSQLiteAssistantCorrectionUsesTransactionCurrency(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	space := storetest.NewHousehold(t, st)
	db, err := st.HouseholdDB(ctx, space)
	require.NoError(t, err)
	var primary string
	require.NoError(t, db.Read().QueryRowContext(ctx, `SELECT primary_currency FROM household_metadata WHERE id = 1`).Scan(&primary))
	require.Equal(t, "USD", primary)
	charge, _ := sqliteLedgerRows(t, st, space)
	_, err = db.Write().ExecContext(ctx, `UPDATE transactions SET currency = 'JPY' WHERE id = ?`, charge)
	require.NoError(t, err)
	thread := store.AssistantConversation{UserID: uuid.New()}
	require.NoError(t, st.CreateAssistantConversation(ctx, space, &thread))
	action := store.AssistantAction{ConversationID: thread.ID, ToolName: "update_transaction", Method: "PATCH", Path: "/transactions/" + charge.String()}
	require.NoError(t, st.CreateAssistantAction(ctx, space, &action))
	correction := store.AssistantCorrection{
		ID: uuid.New(), ActionID: action.ID, TransactionID: charge,
		Amount: domain.MustFromString("123"), HasAmount: true, CorrectedBy: thread.UserID,
	}
	require.NoError(t, st.CreateAssistantCorrections(ctx, space, []store.AssistantCorrection{correction}))
	var raw int64
	require.NoError(t, db.Read().QueryRowContext(ctx, `SELECT amount FROM assistant_corrections WHERE id = ?`, correction.ID).Scan(&raw))
	require.EqualValues(t, 123, raw)
	corrections, err := st.ListAssistantCorrections(ctx, space, 10)
	require.NoError(t, err)
	require.Len(t, corrections, 1)
	require.True(t, corrections[0].HasAmount)
	require.True(t, corrections[0].Amount.Equal(correction.Amount), "read JPY correction at USD scale")
	correction.ID = uuid.New()
	correction.Amount = domain.MustFromString("1.23")
	require.Error(t, st.CreateAssistantCorrections(ctx, space, []store.AssistantCorrection{correction}))
	correction.Amount = domain.MustFromString("123")
	correction.TransactionID = uuid.New()
	require.Error(t, st.CreateAssistantCorrections(ctx, space, []store.AssistantCorrection{correction}))
	_, err = db.Write().ExecContext(ctx, `UPDATE assistant_corrections SET transaction_id = ? WHERE id = ?`, uuid.New(), corrections[0].ID)
	require.NoError(t, err)
	_, err = st.ListAssistantCorrections(ctx, space, 10)
	require.ErrorContains(t, err, "without a transaction currency")
}

func TestSQLiteSuggestions(t *testing.T) {
	st := storetest.New(t)
	ctx := t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	require.NoError(t, st.DismissSuggestion(ctx, a, "signature"))
	require.NoError(t, st.DismissSuggestion(ctx, a, "signature"))
	signatures, err := st.ListDismissedSuggestions(ctx, a)
	require.NoError(t, err)
	require.Equal(t, []string{"signature"}, signatures)
	signatures, err = st.ListDismissedSuggestions(ctx, b)
	require.NoError(t, err)
	require.Empty(t, signatures)
	restored, err := st.RestoreSuggestion(ctx, a, "signature")
	require.NoError(t, err)
	require.True(t, restored)
	restored, err = st.RestoreSuggestion(ctx, a, "signature")
	require.NoError(t, err)
	require.False(t, restored)
	batch := store.SuggestionBatch{CreatedBy: uuid.New(), RowCount: 12}
	require.NoError(t, st.CreateSuggestionBatch(ctx, a, &batch))
	got, err := st.GetSuggestionBatch(ctx, a, batch.ID)
	require.NoError(t, err)
	require.Equal(t, a, got.SpaceID)
	require.Equal(t, batch.CreatedAt, got.CreatedAt)
	_, err = st.GetSuggestionBatch(ctx, b, batch.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	got, err = st.LatestSuggestionBatch(ctx, a, 10)
	require.NoError(t, err)
	require.Equal(t, batch.ID, got.ID)
	runs, err := st.SuggestionBatchRuns(ctx, a, batch.ID)
	require.NoError(t, err)
	require.Empty(t, runs)
	removed, err := st.CancelSuggestionBatch(ctx, a, batch.ID)
	require.NoError(t, err)
	require.Zero(t, removed)
	require.NoError(t, st.DismissSuggestionBatch(ctx, a, batch.ID))
	got, err = st.GetSuggestionBatch(ctx, a, batch.ID)
	require.NoError(t, err)
	require.NotNil(t, got.CancelledAt)
	require.NotNil(t, got.DismissedAt)
	require.NoError(t, st.DeleteSuggestionBatch(ctx, a, batch.ID))
}
