package store_test

import (
	"errors"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/storetest"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

func sqliteAutomation(t *testing.T, st *store.Store, space store.SpaceID) store.Automation {
	t.Helper()
	one := store.Automation{CreatedBy: uuid.New(), Name: "Daily", IsEnabled: true,
		Trigger: domain.AutomationTriggerDaily, Prompt: "Review", Mode: domain.AutomationModeObserve,
		Tools: []string{"read"}, MaxToolRounds: 6}
	require.NoError(t, st.CreateAutomation(t.Context(), space, &one))
	return one
}

func TestSQLiteAutomationQueueOrderingAndIsolation(t *testing.T) {
	st, ctx := storetest.New(t), t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	aa, ab := sqliteAutomation(t, st, a), sqliteAutomation(t, st, b)
	all, spaces, err := st.ListAutomationsByTrigger(ctx, domain.AutomationTriggerDaily)
	require.NoError(t, err)
	require.Len(t, all, 2)
	require.ElementsMatch(t, []store.SpaceID{a, b}, spaces)
	_, err = st.GetAutomation(ctx, b, aa.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	require.NoError(t, st.FinishAutomationRun(ctx, b, uuid.New(), store.AutomationOutcome{Status: domain.AutomationRunFailed}))
	last, err := st.LastAutomationRunAt(ctx, a, aa.ID)
	require.NoError(t, err)
	require.Nil(t, last)
	queue := func(space store.SpaceID, automation store.Automation, at int64, bulk bool) store.AutomationRun {
		one := store.AutomationRun{AutomationID: automation.ID, FiredBy: domain.AutomationFiredBySchedule}
		fresh, err := st.QueueAutomationRun(ctx, space, &one)
		require.NoError(t, err)
		require.True(t, fresh)
		db, err := st.HouseholdDB(ctx, space)
		require.NoError(t, err)
		_, err = db.Write().ExecContext(ctx, `UPDATE assistant_automation_runs SET queued_at = ?, bulk = ? WHERE id = ?`, at, bulk, one.ID)
		require.NoError(t, err)
		one.QueuedAt, one.Bulk = time.UnixMilli(at).UTC(), bulk
		return one
	}
	bulk := queue(a, aa, 1, true)
	newer := queue(a, aa, 30, false)
	oldest := queue(b, ab, 10, false)
	tieA, tieB := queue(a, aa, 20, false), queue(b, ab, 20, false)
	ties := []store.AutomationRun{tieA, tieB}
	sort.Slice(ties, func(i, j int) bool { return ties[i].ID.String() < ties[j].ID.String() })
	_, err = st.GetAutomationRun(ctx, a, oldest.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	_, err = st.ClaimAutomationRun(ctx, a, oldest.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
	for _, want := range []store.AutomationRun{oldest, ties[0], ties[1], newer, bulk} {
		got, err := st.ClaimQueuedAutomationRun(ctx)
		require.NoError(t, err)
		require.Equal(t, want.ID, got.ID)
		require.Equal(t, want.SpaceID, got.SpaceID)
		require.Equal(t, domain.AutomationRunRunning, got.Status)
		require.NotNil(t, got.StartedAt)
		_, err = st.ClaimAutomationRun(ctx, got.SpaceID, got.ID)
		require.ErrorIs(t, err, store.ErrNotFound)
	}
	_, err = st.ClaimQueuedAutomationRun(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)
	count, err := st.FailAbandonedAutomationRuns(ctx)
	require.NoError(t, err)
	require.Equal(t, 5, count)
	rows, err := st.ListAutomationRuns(ctx, a, store.AutomationRunQuery{AutomationID: aa.ID, Status: domain.AutomationRunFailed, Limit: 2})
	require.NoError(t, err)
	require.Len(t, rows, 2)
	require.NotNil(t, rows[0].FinishedAt)
	last, err = st.LastAutomationRunAt(ctx, a, aa.ID)
	require.NoError(t, err)
	require.Equal(t, time.UnixMilli(30).UTC(), *last)
	summaries, err := st.AutomationRunSummaries(ctx, a)
	require.NoError(t, err)
	require.Equal(t, 3, summaries[aa.ID].Runs)
	require.Equal(t, domain.AutomationRunFailed, summaries[aa.ID].LastStatus)
	require.NoError(t, st.DeleteAutomation(ctx, a, aa.ID))
	rows, err = st.ListAutomationRuns(ctx, a, store.AutomationRunQuery{})
	require.NoError(t, err)
	require.Empty(t, rows)
	_, err = st.GetAutomation(ctx, b, ab.ID)
	require.NoError(t, err)
}

func TestSQLiteAutomationConcurrentClaims(t *testing.T) {
	st, ctx := storetest.New(t), t.Context()
	spaces := []store.SpaceID{storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)}
	const total = 40
	for _, space := range spaces {
		automation := sqliteAutomation(t, st, space)
		for i := 0; i < total/2; i++ {
			one := store.AutomationRun{AutomationID: automation.ID, FiredBy: domain.AutomationFiredByManual, Blind: true}
			fresh, err := st.QueueAutomationRun(ctx, space, &one)
			require.NoError(t, err)
			require.True(t, fresh)
			require.True(t, one.DryRun)
		}
	}
	claimed := make(chan store.AutomationRun, total)
	failures := make(chan error, 8)
	start := make(chan struct{})
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			for {
				one, err := st.ClaimQueuedAutomationRun(ctx)
				if errors.Is(err, store.ErrNotFound) {
					return
				}
				if err != nil {
					failures <- err
					return
				}
				claimed <- one
			}
		}()
	}
	close(start)
	wg.Wait()
	close(claimed)
	close(failures)
	for err := range failures {
		require.NoError(t, err)
	}
	seen := map[uuid.UUID]bool{}
	for one := range claimed {
		require.False(t, seen[one.ID], "duplicate claim %s", one.ID)
		seen[one.ID] = true
		require.Equal(t, domain.AutomationRunRunning, one.Status)
		require.NotNil(t, one.StartedAt)
	}
	require.Len(t, seen, total)
}

func TestSQLiteAutomationRunLifecycleAndBulk(t *testing.T) {
	st, ctx := storetest.New(t), t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	automation := sqliteAutomation(t, st, a)
	account := &store.Account{Name: "Cash", Kind: domain.KindCash, Type: "checking", Currency: "USD"}
	require.NoError(t, st.CreateAccount(ctx, a, account))
	txn := &store.Transaction{AccountID: account.ID, Date: domain.DateOf(time.Now()),
		Amount: domain.MustFromString("-12.34"), Currency: "USD", Source: domain.Source("manual")}
	require.NoError(t, st.CreateTransaction(ctx, a, txn))
	run := store.AutomationRun{AutomationID: automation.ID, TransactionID: txn.ID,
		FiredBy: domain.AutomationFiredByTransaction, Subject: "Purchase"}
	fresh, err := st.QueueAutomationRun(ctx, a, &run)
	require.NoError(t, err)
	require.True(t, fresh)
	duplicate := run
	duplicate.ID = uuid.Nil
	fresh, err = st.QueueAutomationRun(ctx, a, &duplicate)
	require.NoError(t, err)
	require.False(t, fresh)
	exists, err := st.HasQueuedAutomationRun(ctx, a, automation.ID, txn.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, st.ClearQueuedAutomationRun(ctx, b, automation.ID, txn.ID))
	exists, err = st.HasQueuedAutomationRun(ctx, a, automation.ID, txn.ID)
	require.NoError(t, err)
	require.True(t, exists)
	require.NoError(t, st.ClearQueuedAutomationRuns(ctx, a, nil, []uuid.UUID{txn.ID}))
	require.NoError(t, st.ClearQueuedAutomationRuns(ctx, a, []uuid.UUID{automation.ID}, []uuid.UUID{txn.ID}))
	exists, err = st.HasQueuedAutomationRun(ctx, a, automation.ID, txn.ID)
	require.NoError(t, err)
	require.False(t, exists)
	bulk := store.AutomationRun{AutomationID: automation.ID, TransactionID: txn.ID,
		Subject: "Bulk purchase", ReviewedWhenQueued: true, Bulk: true}
	count, err := st.QueueAutomationRuns(ctx, a, domain.AutomationFiredByTransaction, []store.AutomationRun{bulk, bulk})
	require.NoError(t, err)
	require.Equal(t, 1, count)
	rows, err := st.ListAutomationRuns(ctx, a, store.AutomationRunQuery{})
	require.NoError(t, err)
	require.Len(t, rows, 1)
	require.True(t, rows[0].Bulk)
	require.True(t, rows[0].ReviewedWhenQueued)
	require.Equal(t, txn.ID, rows[0].TransactionID)
	claimed, err := st.ClaimAutomationRun(ctx, a, rows[0].ID)
	require.NoError(t, err)
	conversation := store.AssistantConversation{UserID: uuid.New(), AutomationRunID: claimed.ID, Title: "Review"}
	require.NoError(t, st.CreateAssistantConversation(ctx, a, &conversation))
	require.NoError(t, st.StartAutomationRun(ctx, a, claimed.ID, conversation.ID, "Check purchase"))
	action := store.AssistantAction{ConversationID: conversation.ID, ToolName: "categorize",
		Method: "PATCH", Path: "/transactions", Summary: "Choose a category",
		Body: map[string]any{"category": "Food"}}
	require.NoError(t, st.CreateAssistantAction(ctx, a, &action))
	count, err = st.CountAssistantActions(ctx, a, conversation.ID)
	require.NoError(t, err)
	require.Equal(t, 1, count)
	count, err = st.CountAssistantActions(ctx, b, conversation.ID)
	require.NoError(t, err)
	require.Zero(t, count)
	pending, err := st.ListPendingAutomationActions(ctx, a)
	require.NoError(t, err)
	require.Len(t, pending, 1)
	require.Equal(t, claimed.ID, pending[0].RunID)
	require.Equal(t, automation.ID, pending[0].AutomationID)
	require.Equal(t, action.ID, pending[0].Action.ID)
	require.Equal(t, action.CreatedAt, pending[0].Action.CreatedAt)
	pending, err = st.ListPendingAutomationActions(ctx, b)
	require.NoError(t, err)
	require.Empty(t, pending)
	confidence := 0.8
	require.NoError(t, st.FinishAutomationRun(ctx, a, claimed.ID, store.AutomationOutcome{
		Status: domain.AutomationRunSucceeded, Output: "Categorized", ToolCalls: 2, Actions: 1,
		Confidence: &confidence, DecidedBy: "model", CategoryResult: domain.CategoryCheckResult("confirmed")}))
	got, err := st.GetAutomationRun(ctx, a, claimed.ID)
	require.NoError(t, err)
	require.Equal(t, conversation.ID, got.ConversationID)
	require.Equal(t, "Check purchase", got.Prompt)
	require.Equal(t, "Categorized", got.Output)
	require.Equal(t, &confidence, got.Confidence)
	require.Equal(t, domain.CategoryCheckResult("confirmed"), got.CategoryResult)
	require.NotNil(t, got.FinishedAt)
	summaries, err := st.AutomationRunSummaries(ctx, a)
	require.NoError(t, err)
	require.Equal(t, 1, summaries[automation.ID].PendingActions)
	require.NoError(t, st.DeleteAutomation(ctx, a, automation.ID))
	_, err = st.GetAssistantConversation(ctx, a, conversation.UserID, conversation.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestSQLiteAutomationScopesAndTransactions(t *testing.T) {
	st, ctx := storetest.New(t), t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	filter := store.Filter{Name: "Food", QueryText: "market"}
	require.NoError(t, st.CreateFilter(ctx, a, &filter))
	automation := sqliteAutomation(t, st, a)
	automation.FilterID = filter.ID
	require.NoError(t, st.UpdateAutomation(ctx, a, &automation))
	scopes, err := st.AutomationScopes(ctx, a, []store.Automation{automation})
	require.NoError(t, err)
	require.Contains(t, scopes, automation.ID)
	missing := automation
	missing.ID, missing.FilterID = uuid.New(), uuid.New()
	scopes, err = st.AutomationScopes(ctx, a, []store.Automation{missing})
	require.NoError(t, err)
	require.Equal(t, &domain.Filter{}, scopes[missing.ID])
	h, err := st.ForHousehold(ctx, a)
	require.NoError(t, err)
	sentinel := errors.New("rollback")
	var run store.AutomationRun
	err = h.InTx(ctx, func(tx *store.Store) error {
		run = store.AutomationRun{AutomationID: automation.ID, FiredBy: domain.AutomationFiredByManual}
		fresh, err := tx.QueueAutomationRun(ctx, a, &run)
		if err != nil {
			return err
		}
		if !fresh {
			return errors.New("not queued")
		}
		if _, err := tx.ClaimAutomationRun(ctx, a, run.ID); err != nil {
			return err
		}
		if _, err := tx.GetAutomation(ctx, b, automation.ID); err == nil {
			return errors.New("cross-household access succeeded")
		}
		return sentinel
	})
	require.ErrorIs(t, err, sentinel)
	_, err = st.GetAutomationRun(ctx, a, run.ID)
	require.ErrorIs(t, err, store.ErrNotFound)
}

func TestSQLiteAutomationSpecificClaimExclusive(t *testing.T) {
	st, ctx := storetest.New(t), t.Context()
	a, b := storetest.NewHousehold(t, st), storetest.NewHousehold(t, st)
	aa, ab := sqliteAutomation(t, st, a), sqliteAutomation(t, st, b)
	sharedID := uuid.New()
	for _, entry := range []struct {
		space      store.SpaceID
		automation store.Automation
	}{{a, aa}, {b, ab}} {
		run := store.AutomationRun{ID: sharedID, AutomationID: entry.automation.ID, FiredBy: domain.AutomationFiredByManual}
		fresh, err := st.QueueAutomationRun(ctx, entry.space, &run)
		require.NoError(t, err)
		require.True(t, fresh)
		db, err := st.HouseholdDB(ctx, entry.space)
		require.NoError(t, err)
		_, err = db.Write().ExecContext(ctx, `UPDATE assistant_automation_runs SET queued_at = 100 WHERE id = ?`, sharedID)
		require.NoError(t, err)
	}
	first, err := st.ClaimQueuedAutomationRun(ctx)
	require.NoError(t, err)
	spaces := []store.SpaceID{a, b}
	sort.Slice(spaces, func(i, j int) bool { return spaces[i].UUID().String() < spaces[j].UUID().String() })
	require.Equal(t, spaces[0], first.SpaceID)
	results := make(chan error, 16)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := st.ClaimAutomationRun(ctx, spaces[1], sharedID)
			results <- err
		}()
	}
	wg.Wait()
	close(results)
	success := 0
	for err := range results {
		if err == nil {
			success++
		} else {
			require.ErrorIs(t, err, store.ErrNotFound)
		}
	}
	require.Equal(t, 1, success)
	_, err = st.ClaimQueuedAutomationRun(ctx)
	require.ErrorIs(t, err, store.ErrNotFound)
}
