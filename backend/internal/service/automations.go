package service

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"runtime/debug"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// Running the assistant without being asked: a row arriving or a clock queues
// a run, a worker claims queued runs and executes each by opening a
// conversation and handing it to the same loop a question goes through.
//
// The queue is the runs table, so a restart loses nothing; a run in progress
// when the process died is marked abandoned at the next start. One run at a
// time per process by default, because the model is likely a single local GPU;
// Workers raises that.

// AutomationHost builds the caller a run lacks (the automation's creator, in
// its space, with their role) and returns the tool runner a question would
// get, so a run can do exactly what that person could and nothing more.
type AutomationHost interface {
	ToolsFor(ctx context.Context, spaceID store.SpaceID, userID uuid.UUID) (ToolRunner, error)
	// ContextFor renders the facts an automation asked for as the run's opening
	// message. transactionID is nil for a ledger-wide run; blind hides the row's
	// category.
	ContextFor(
		ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
		automation store.Automation, transactionID uuid.UUID, blind bool,
	) (string, error)
	// Assess votes the payee's history for one row at the automation's
	// threshold. blind hides the row's own category from the report, not from
	// the history.
	Assess(
		ctx context.Context, spaceID store.SpaceID, userID uuid.UUID,
		automation store.Automation, transactionID uuid.UUID, blind bool,
	) (CategoryAssessment, error)
	// Record stores a finished run's report where its answer is data (the
	// cash-flow forecast). An answer that does not parse fails the run, so the
	// screen keeps the last good forecast. A dry run records nothing.
	Record(
		ctx context.Context, run store.AutomationRun, automation store.Automation,
		model, output string,
	) error
}

type RunOptions struct {
	DryRun bool
	// Blind is a dry run shown the row with its category hidden, to compare its
	// choice with the row's. Implies DryRun.
	Blind bool
}

type CategoryAssessment struct {
	Verdict      domain.CategoryVerdict
	CategoryName string
	Text         string
	// Enriched marks a row something outside the ledger knows more about (an
	// Amazon order's items); the vote then does not act alone.
	Enriched bool
	// Guided marks a row with a standing household instruction; like Enriched,
	// the vote then does not act alone.
	Guided bool
}

type Automations struct {
	base
	Host AutomationHost
	// Every is how often the worker looks between kicks. Zero is thirty seconds.
	Every time.Duration
	// Workers is how many runs execute at once. Zero or one is one.
	Workers int
	Log     *slog.Logger
	Now     func() time.Time

	kick chan struct{}
}

// NewAutomations needs a store carrying the credential cipher: the runner
// reads the connection's API key.
func NewAutomations(st *store.Store, host AutomationHost) *Automations {
	return &Automations{base: newBase(st), Host: host, kick: make(chan struct{}, 1)}
}

func (a *Automations) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

func (a *Automations) log() *slog.Logger {
	if a.Log == nil {
		return slog.Default()
	}
	return a.Log
}

// Kick wakes the worker now rather than at the next tick. Never blocks.
func (a *Automations) Kick() {
	select {
	case a.kick <- struct{}{}:
	default:
	}
}

// EnqueueForTransactions fires every enabled transaction automation for the
// rows that qualify, and reports how many runs were queued. Called after the
// settle, so rules and transfer pairing have run. The store skips a row
// already queued, so a re-sync costs nothing.
func (a *Automations) EnqueueForTransactions(
	ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID,
) (int, error) {
	return a.enqueueForRows(ctx, spaceID, ids, domain.AutomationFiredByTransaction, false)
}

// EnqueueForRows fires the transaction automations for rows that arrived
// earlier (a person's request, or a merchant order matched later); FiredBy
// says which. A row already waiting is not queued twice. Zero with a nil error
// is a space with no enabled transaction automation.
func (a *Automations) EnqueueForRows(
	ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID, firedBy string, force bool,
) (int, error) {
	if firedBy == domain.AutomationFiredByTransaction {
		return 0, fmt.Errorf("automations: an arriving row is fired through EnqueueForTransactions")
	}
	return a.enqueueForRows(ctx, spaceID, ids, firedBy, force)
}

// EnsureCategoryCheck gives a space that has a writable assistant the built-in
// category check, so arriving rows get suggestions without a setup step. It
// always creates in propose mode; does nothing without a configured assistant
// that may write; and does nothing if the space has any copy of the template
// (so switching it off keeps it off) or any enabled transaction automation
// that proposes or applies changes. An observe-only automation does not count.
// Called from the firing path so a batch cannot ensure twice.
func (a *Automations) EnsureCategoryCheck(ctx context.Context, spaceID store.SpaceID) error {
	template, ok := domain.AutomationTemplateByKey(domain.AutomationTemplateCheckCategory)
	if !ok {
		return nil
	}
	automations, err := a.store.ListAutomations(ctx, spaceID)
	if err != nil {
		return err
	}
	for _, one := range automations {
		if one.TemplateKey == template.Key {
			return nil
		}
		if one.IsEnabled && one.Trigger == domain.AutomationTriggerTransaction &&
			one.Mode != domain.AutomationModeObserve {
			return nil
		}
	}

	connection, err := a.store.GetAssistantConnection(ctx, spaceID)
	if errors.Is(err, store.ErrNotFound) {
		return nil
	}
	if err != nil {
		return err
	}
	if !connection.IsEnabled || !connection.AllowWrites {
		return nil
	}

	// A run acts as its creator, so pick an owner who can write rather than
	// whoever triggered the sync.
	owner, err := a.spaceOwner(ctx, spaceID)
	if err != nil || owner == uuid.Nil {
		return err
	}

	one := store.Automation{
		CreatedBy: owner, Name: template.Name, Description: template.Description,
		IsEnabled: true, Trigger: template.Trigger, TriggerConfig: template.TriggerConfig,
		Prompt: template.Prompt, Context: template.Context,
		Mode: domain.AutomationModePropose, Tools: template.Tools,
		MaxToolRounds: defaultAutomationToolRounds, TemplateKey: template.Key,
		ConfidenceThreshold: template.ConfidenceThreshold,
	}
	if err := a.store.CreateAutomation(ctx, spaceID, &one); err != nil {
		return err
	}
	a.log().InfoContext(ctx, "automations: created the category check for this space",
		"space", spaceID.UUID(), "automation", one.ID)
	return nil
}

// defaultAutomationToolRounds mirrors the create endpoint's default.
const defaultAutomationToolRounds = 6

// spaceOwner is an accepted member who owns the space, or Nil.
func (a *Automations) spaceOwner(
	ctx context.Context, spaceID store.SpaceID,
) (uuid.UUID, error) {
	members, err := a.store.ListMemberships(ctx, spaceID)
	if err != nil {
		return uuid.Nil, err
	}
	for _, member := range members {
		if member.Role.Owns() && member.IsAccepted() {
			return member.UserID, nil
		}
	}
	return uuid.Nil, nil
}

// Why firing the transaction automations would do nothing, as codes a
// client turns into the one step that fixes it.
const (
	// AutomationOffCode is an assistant that could run but has no enabled
	// automation watching transactions.
	AutomationOffCode = "automation_off"
)

// WhyNothingFires is blank when firing the transaction automations would queue
// a run that can go ahead, and otherwise the code for why not:
// AssistantUnavailableCode, ChangesOffCode or AutomationOffCode.
func (a *Automations) WhyNothingFires(ctx context.Context, spaceID store.SpaceID) (string, error) {
	// Ensure first, so the answer is whether firing would do anything.
	if err := a.EnsureCategoryCheck(ctx, spaceID); err != nil {
		return "", err
	}
	connection, err := a.store.GetAssistantConnection(ctx, spaceID)
	if errors.Is(err, store.ErrNotFound) {
		return AssistantUnavailableCode, nil
	}
	if err != nil {
		return "", err
	}
	if !connection.IsEnabled {
		return AssistantUnavailableCode, nil
	}
	automations, err := a.store.ListAutomations(ctx, spaceID)
	if err != nil {
		return "", err
	}
	firing := enabledTransactionAutomations(automations)
	if len(firing) == 0 {
		if !connection.AllowWrites {
			return ChangesOffCode, nil
		}
		return AutomationOffCode, nil
	}
	for _, one := range firing {
		if one.Mode == domain.AutomationModeObserve || connection.AllowWrites {
			return "", nil
		}
	}
	return ChangesOffCode, nil
}

// FireSuggestionBatch queues the transaction automations for rows a person
// named, as one batch whose progress and comparison are read back by its id.
// A batch that queued nothing is not kept.
func (a *Automations) FireSuggestionBatch(
	ctx context.Context, spaceID store.SpaceID, createdBy uuid.UUID, rows []store.Transaction,
	force bool,
) (store.SuggestionBatch, int, error) {
	rows = uniqueRows(rows)
	batch := store.SuggestionBatch{CreatedBy: createdBy, RowCount: len(rows)}
	if err := a.store.CreateSuggestionBatch(ctx, spaceID, &batch); err != nil {
		return store.SuggestionBatch{}, 0, err
	}
	queued, err := a.queueRows(ctx, spaceID, rows, enqueueOptions{
		firedBy: domain.AutomationFiredByManual, force: force,
		batch: batch.ID, bulk: len(rows) > domain.BulkSuggestionRows,
	})
	if err != nil {
		return store.SuggestionBatch{}, queued, err
	}
	if queued == 0 {
		return store.SuggestionBatch{}, 0, a.store.DeleteSuggestionBatch(ctx, spaceID, batch.ID)
	}
	return batch, queued, nil
}

func uniqueRows(rows []store.Transaction) []store.Transaction {
	seen := make(map[uuid.UUID]bool, len(rows))
	out := make([]store.Transaction, 0, len(rows))
	for _, row := range rows {
		if seen[row.ID] {
			continue
		}
		seen[row.ID] = true
		out = append(out, row)
	}
	return out
}

// enqueueOptions is how one set of rows is queued; batch and bulk are set by
// FireSuggestionBatch only.
type enqueueOptions struct {
	firedBy string
	force   bool
	batch   uuid.UUID
	bulk    bool
}

func (a *Automations) enqueueForRows(
	ctx context.Context, spaceID store.SpaceID, ids []uuid.UUID, firedBy string, force bool,
) (int, error) {
	rows := make([]store.Transaction, 0, len(ids))
	for _, id := range ids {
		txn, err := a.store.GetTransaction(ctx, spaceID, id)
		if errors.Is(err, store.ErrNotFound) {
			continue
		}
		if err != nil {
			return 0, err
		}
		rows = append(rows, txn)
	}
	return a.queueRows(ctx, spaceID, uniqueRows(rows),
		enqueueOptions{firedBy: firedBy, force: force})
}

// queueRows queues the enabled transaction automations for each row. A forced
// call skips the trigger filter and dedup check: the person said "check this
// one" even if it has a category, is a transfer or was checked. Any queued run
// for the same row and automation is cleared first.
func (a *Automations) queueRows(
	ctx context.Context, spaceID store.SpaceID, rows []store.Transaction, opts enqueueOptions,
) (int, error) {
	if len(rows) == 0 {
		return 0, nil
	}
	if err := a.EnsureCategoryCheck(ctx, spaceID); err != nil {
		return 0, err
	}
	automations, err := a.store.ListAutomations(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	firing := enabledTransactionAutomations(automations)
	if len(firing) == 0 {
		return 0, nil
	}

	var categories map[uuid.UUID]store.Category
	var scopes map[uuid.UUID]*domain.Filter
	if opts.force {
		automationIDs := make([]uuid.UUID, 0, len(firing))
		for _, automation := range firing {
			automationIDs = append(automationIDs, automation.ID)
		}
		rowIDs := make([]uuid.UUID, 0, len(rows))
		for _, txn := range rows {
			rowIDs = append(rowIDs, txn.ID)
		}
		if err := a.store.ClearQueuedAutomationRuns(ctx, spaceID, automationIDs, rowIDs); err != nil {
			return 0, err
		}
	} else {
		// Deleted categories included: a retired category still marks a transfer.
		categoryRows, err := a.store.ListCategories(ctx, spaceID, true)
		if err != nil {
			return 0, err
		}
		categories = make(map[uuid.UUID]store.Category, len(categoryRows))
		for _, category := range categoryRows {
			categories[category.ID] = category
		}
		if scopes, err = a.store.AutomationScopes(ctx, spaceID, firing); err != nil {
			return 0, err
		}
	}

	runs := make([]store.AutomationRun, 0, len(rows)*len(firing))
	for _, txn := range rows {
		var facts domain.AutomationTransactionFacts
		if !opts.force {
			facts = store.AutomationFacts(txn, categories)
		}
		for _, automation := range firing {
			if !opts.force {
				if !domain.AutomationTriggers(automation.TriggerConfig, scopes[automation.ID], facts, opts.firedBy) {
					continue
				}
				// The store dedupes only arriving rows against their own kind; a later
				// look is deduplicated here against anything waiting, so two clicks
				// queue one run.
				if opts.firedBy != domain.AutomationFiredByTransaction {
					waiting, err := a.store.HasQueuedAutomationRun(ctx, spaceID, automation.ID, txn.ID)
					if err != nil {
						return 0, err
					}
					if waiting {
						continue
					}
				}
			}
			runs = append(runs, store.AutomationRun{
				AutomationID: automation.ID, TransactionID: txn.ID, Subject: subjectOf(txn),
				BatchID: opts.batch, ReviewedWhenQueued: txn.IsReviewed, Bulk: opts.bulk,
			})
		}
	}
	queued, err := a.store.QueueAutomationRuns(ctx, spaceID, opts.firedBy, runs)
	if queued > 0 {
		a.Kick()
	}
	return queued, err
}

func enabledTransactionAutomations(automations []store.Automation) []store.Automation {
	var firing []store.Automation
	for _, one := range automations {
		if one.IsEnabled && one.Trigger == domain.AutomationTriggerTransaction {
			firing = append(firing, one)
		}
	}
	return firing
}

// EnqueueDue queues a scheduled run for every daily automation whose window
// has passed since it last ran. A server asleep at the window still owes the
// run on waking; one created after today's window waits for tomorrow.
func (a *Automations) EnqueueDue(ctx context.Context) (int, error) {
	automations, spaces, err := a.store.ListAutomationsByTrigger(ctx, domain.AutomationTriggerDaily)
	if err != nil {
		return 0, err
	}
	now := a.now()
	queued := 0
	for i, automation := range automations {
		spaceID := spaces[i]
		space, err := a.store.GetSpace(ctx, spaceID)
		if err != nil {
			return queued, err
		}
		location, err := time.LoadLocation(space.Timezone)
		if err != nil || space.Timezone == "" {
			location = time.UTC
		}
		window := dailyWindow(automation.TriggerConfig.At)
		recent := window.MostRecent(now.In(location))

		last, err := a.store.LastAutomationRunAt(ctx, spaceID, automation.ID)
		if err != nil {
			return queued, err
		}
		if last == nil && !automation.CreatedAt.Before(recent) {
			continue
		}
		if last != nil && !last.Before(recent) {
			continue
		}
		run := store.AutomationRun{
			AutomationID: automation.ID, FiredBy: domain.AutomationFiredBySchedule,
			Subject: "the ledger on " + domain.DateOf(now.In(location)).String(),
		}
		fresh, err := a.store.QueueAutomationRun(ctx, spaceID, &run)
		if err != nil {
			return queued, err
		}
		if fresh {
			queued++
		}
	}
	if queued > 0 {
		a.Kick()
	}
	return queued, nil
}

// dailyWindow reads "HH:MM", falling back to 06:00 for anything else.
func dailyWindow(at string) SyncWindow {
	parsed, err := time.Parse("15:04", strings.TrimSpace(at))
	if err != nil {
		return SyncWindow{Hour: 6}
	}
	return SyncWindow{Hour: parsed.Hour(), Minute: parsed.Minute()}
}

// EnqueueManual queues one run somebody asked for, about one row or none.
func (a *Automations) EnqueueManual(
	ctx context.Context, spaceID store.SpaceID, automation store.Automation,
	transactionID uuid.UUID, options RunOptions,
) (store.AutomationRun, error) {
	run := store.AutomationRun{
		AutomationID: automation.ID, FiredBy: domain.AutomationFiredByManual,
		TransactionID: transactionID, DryRun: options.DryRun || options.Blind,
		Blind:   options.Blind && transactionID != uuid.Nil,
		Subject: "the ledger on " + domain.DateOf(a.now()).String(),
	}
	if transactionID != uuid.Nil {
		txn, err := a.store.GetTransaction(ctx, spaceID, transactionID)
		if err != nil {
			return store.AutomationRun{}, err
		}
		run.Subject = subjectOf(txn)
		if run.Blind {
			run.ExpectedCategoryID = txn.CategoryID
		}
	}
	if _, err := a.store.QueueAutomationRun(ctx, spaceID, &run); err != nil {
		return store.AutomationRun{}, err
	}
	return run, nil
}

func subjectOf(txn store.Transaction) string {
	payee := txn.Payee
	if strings.TrimSpace(payee) == "" {
		payee = txn.StatementName
	}
	return domain.AutomationSubject(payee, txn.Amount.String(), txn.Date.String())
}

func (a *Automations) Work(ctx context.Context) {
	if failed, err := a.store.FailAbandonedAutomationRuns(ctx); err != nil {
		a.log().Error("automations: could not settle abandoned runs", "error", err)
	} else if failed > 0 {
		a.log().Warn("automations: runs abandoned by a previous process", "count", failed)
	}

	every := a.Every
	if every <= 0 {
		every = 30 * time.Second
	}
	ticker := time.NewTicker(every)
	defer ticker.Stop()
	a.log().Info("automation worker started", "checking_every", every)

	for {
		if _, err := a.EnqueueDue(ctx); err != nil && ctx.Err() == nil {
			a.log().Error("automations: firing schedules", "error", err)
		}
		a.Drain(ctx)
		select {
		case <-ctx.Done():
			a.log().Info("automation worker stopped")
			return
		case <-ticker.C:
		case <-a.kick:
		}
	}
}

// Drain runs everything queued, Workers at a time, until the queue is empty or
// the context ends.
func (a *Automations) Drain(ctx context.Context) {
	workers := a.Workers
	if workers <= 1 {
		a.drain(ctx)
		return
	}
	var wg sync.WaitGroup
	for i := 0; i < workers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			a.drain(ctx)
		}()
	}
	wg.Wait()
}

// drain's claim locks the row it takes, so two drains never hold one run.
func (a *Automations) drain(ctx context.Context) {
	for ctx.Err() == nil {
		run, err := a.store.ClaimQueuedAutomationRun(ctx)
		if errors.Is(err, store.ErrNotFound) {
			return
		}
		if err != nil {
			a.log().Error("automations: claiming a run", "error", err)
			return
		}
		a.Execute(ctx, run)
	}
}

// ExecuteNow claims and executes one specific queued run, for a caller that
// wants the result now.
func (a *Automations) ExecuteNow(ctx context.Context, run store.AutomationRun) (store.AutomationRun, error) {
	claimed, err := a.store.ClaimAutomationRun(ctx, run.SpaceID, run.ID)
	if err != nil {
		return store.AutomationRun{}, err
	}
	a.Execute(ctx, claimed)
	return a.store.GetAutomationRun(ctx, run.SpaceID, run.ID)
}

// Execute never returns an error: whatever goes wrong is recorded on the run.
func (a *Automations) Execute(ctx context.Context, run store.AutomationRun) {
	defer func() {
		if recovered := recover(); recovered != nil {
			a.log().Error("automations: run panicked", "run", run.ID,
				"panic", fmt.Sprint(recovered), "stack", string(debug.Stack()))
			_ = a.store.FinishAutomationRun(context.WithoutCancel(ctx), run.SpaceID, run.ID,
				store.AutomationOutcome{
					Status: domain.AutomationRunFailed,
					Error:  fmt.Sprintf("the run crashed: %v", recovered),
				})
		}
	}()

	needsCategory := false
	if run.TransactionID != uuid.Nil && !run.DryRun && !run.Blind {
		// Read before the run, which may file the category it is judged on.
		if txn, err := a.store.GetTransaction(ctx, run.SpaceID, run.TransactionID); err == nil {
			needsCategory = store.DomainTransaction(txn).IsUncategorized()
		}
	}
	outcome := a.execute(ctx, run)
	// The thread was created inside execute, so re-read the run for it.
	if fresh, readErr := a.store.GetAutomationRun(ctx, run.SpaceID, run.ID); readErr == nil &&
		fresh.ConversationID != uuid.Nil {
		outcome.Actions, _ = a.store.CountAssistantActions(ctx, run.SpaceID, fresh.ConversationID)
	}
	if run.TransactionID != uuid.Nil && !run.DryRun && !run.Blind {
		outcome.CategoryResult = domain.CategoryCheckResultOf(domain.CategoryCheckOutcome{
			Status: outcome.Status, Actions: outcome.Actions, Settled: outcome.Settled,
			Agrees: outcome.Agrees, NeedsCategory: needsCategory,
		})
	}
	if outcome.Status == domain.AutomationRunFailed {
		a.log().Warn("automation run failed", "run", run.ID, "error", outcome.Error)
	}
	// A context that survives shutdown: a finished run must be recorded, or it
	// comes back as "abandoned".
	if err := a.store.FinishAutomationRun(context.WithoutCancel(ctx), run.SpaceID, run.ID,
		outcome); err != nil {
		a.log().Error("automations: settling a run", "run", run.ID, "error", err)
	}
	a.recordCategoryCheck(context.WithoutCancel(ctx), run, outcome)
}

// recordCategoryCheck writes the verdict back onto the row: a run that
// proposed or applied nothing and settled nothing marks the transaction, one
// that did either clears the mark, since the register's filter cannot reach
// runs. Actions and Settled decide, not status, because a skip can be an
// answer or a shrug. Dry, blind and failed
// runs change nothing on the row, so an outage never reads as "could not
// place it". A genuine outcome stamps category_check_run_id so the register
// can open the run.
func (a *Automations) recordCategoryCheck(
	ctx context.Context, run store.AutomationRun, outcome store.AutomationOutcome,
) {
	if run.TransactionID == uuid.Nil || run.DryRun || run.Blind {
		return
	}
	if outcome.Status == domain.AutomationRunFailed {
		return
	}
	var err error
	if outcome.Actions == 0 && !outcome.Settled {
		err = a.store.MarkCategoryUndetermined(ctx, run.SpaceID, run.TransactionID, a.now(),
			outcome.Output, run.ID)
	} else {
		err = a.store.ClearCategoryCheck(ctx, run.SpaceID, run.TransactionID, run.ID)
	}
	if err != nil {
		a.log().Error("automations: recording a category check", "run", run.ID, "error", err)
	}
}

func failed(err error) store.AutomationOutcome {
	return store.AutomationOutcome{Status: domain.AutomationRunFailed, Error: err.Error()}
}

// ChangesOffCode is a run refused because the automation changes things and
// the household has changes switched off.
const ChangesOffCode = "changes_off"

func failedWith(code string, err error) store.AutomationOutcome {
	outcome := failed(err)
	outcome.ErrorCode = code
	return outcome
}

// execute asks the household's history first: when it settles the row, the
// automation acts or skips without a model call. Only an unsettled row goes
// to the model, with the vote in front of it.
func (a *Automations) execute(ctx context.Context, run store.AutomationRun) store.AutomationOutcome {
	if a.Host == nil {
		return failed(fmt.Errorf("automations cannot run in this process"))
	}
	automation, err := a.store.GetAutomation(ctx, run.SpaceID, run.AutomationID)
	if err != nil {
		return failed(fmt.Errorf("the automation no longer exists"))
	}
	connection, err := a.store.GetAssistantConnection(ctx, run.SpaceID)
	if errors.Is(err, store.ErrNotFound) {
		return failedWith(AssistantUnavailableCode, fmt.Errorf("the assistant has no model configured"))
	}
	if err != nil {
		return failed(err)
	}
	if !connection.IsEnabled {
		return failedWith(AssistantUnavailableCode, fmt.Errorf("the assistant is switched off"))
	}
	// The connection's switch outranks the automation's mode: changes off means
	// nothing is written. A dry run writes nothing anyway.
	if automation.Mode != domain.AutomationModeObserve && !connection.AllowWrites && !run.DryRun {
		return failedWith(ChangesOffCode, fmt.Errorf("this automation would %s changes, but "+
			"changes are switched off for the assistant. Turn them on, or set the automation "+
			"to observe only", automation.Mode))
	}
	var subject *store.Transaction
	if run.TransactionID != uuid.Nil {
		txn, err := a.store.GetTransaction(ctx, run.SpaceID, run.TransactionID)
		if err != nil {
			return failed(fmt.Errorf("the transaction this run was about no longer exists"))
		}
		if run.Blind {
			txn.CategoryID = uuid.Nil
		}
		subject = &txn
	}

	tools, err := a.Host.ToolsFor(ctx, run.SpaceID, automation.CreatedBy)
	if err != nil {
		return failed(err)
	}
	policy := writePolicy{
		unattended: true,
		allowed:    automation.Mode != domain.AutomationModeObserve,
		apply:      automation.Mode == domain.AutomationModeApply,
		dryRun:     run.DryRun || run.Blind,
	}

	var confidence *float64
	if subject != nil && automation.Context.SimilarTransactions > 0 {
		assessment, err := a.Host.Assess(ctx, run.SpaceID, automation.CreatedBy, automation,
			subject.ID, run.Blind)
		if err != nil {
			return failed(fmt.Errorf("assessing the payee's history: %w", err))
		}
		verdict := assessment.Verdict
		if verdict.Kind != domain.CategoryVerdictNoHistory {
			score := verdict.Confidence
			confidence = &score
		}
		// A confident vote skips the model, unless an attached order or a guidance
		// note knows what the vote cannot; otherwise a note would do nothing on the
		// payee it was written for.
		if (assessment.Enriched || assessment.Guided) &&
			verdict.Kind == domain.CategoryVerdictConfident {
			verdict.Kind = domain.CategoryVerdictUncertain
			assessment.Verdict = verdict
		}
		if outcome, decided := a.decideFromHistory(ctx, run, automation, *subject, assessment,
			tools, policy); decided {
			if outcome.Confidence == nil {
				outcome.Confidence = confidence
			}
			outcome.DecidedBy = domain.AutomationDecidedByAgentifi
			return outcome
		}
	}

	opening, err := a.Host.ContextFor(ctx, run.SpaceID, automation.CreatedBy, automation,
		run.TransactionID, run.Blind)
	if err != nil {
		return failed(fmt.Errorf("gathering the context: %w", err))
	}
	if strings.TrimSpace(opening) == "" {
		opening = "Begin."
	}
	assistant := &Assistant{base: a.base, Tools: tools, Now: a.Now}
	model, err := assistant.modelFor(ctx, run.SpaceID, connection, automation.Model)
	if err != nil {
		return failed(err)
	}
	space, err := a.store.GetSpace(ctx, run.SpaceID)
	if err != nil {
		return failed(err)
	}
	prompt := domain.AutomationPrompt(
		automation.Mode, automation.Prompt, domain.DateOf(a.now()), space.PrimaryCurrency)
	if run.DryRun || run.Blind {
		prompt += domain.AutomationDryRunPrompt
	}
	conversation, err := a.openThread(ctx, run, automation, prompt, opening)
	if err != nil {
		return failed(err)
	}

	messages := []provider.ChatMessage{
		{Role: "system", Content: prompt},
		{Role: "user", Content: opening},
	}
	result, err := assistant.converse(ctx, run.SpaceID, conversation.ID, model, messages,
		domain.AutomationToolsFor(automation.Mode, automation.Tools), policy,
		automation.MaxToolRounds)
	outcome := store.AutomationOutcome{
		Status: domain.AutomationRunSucceeded, Output: result.Answer,
		ToolCalls: len(result.ToolCalls), Confidence: confidence,
		DecidedBy: domain.AutomationDecidedByModel,
	}
	if err != nil {
		outcome.Status, outcome.Error = domain.AutomationRunFailed, err.Error()
		return outcome
	}
	if stated, ok := domain.StatedConfidence(result.Answer); ok {
		outcome.Confidence = &stated
	}
	if subject != nil {
		filed, agrees, err := a.fileStatedCategory(ctx, run, automation, *subject, conversation.ID,
			result.Answer, tools, policy)
		if err != nil {
			outcome.Status, outcome.Error = domain.AutomationRunFailed, err.Error()
			return outcome
		}
		if filed != "" {
			outcome.Output += "\n\n" + filed
		}
		outcome.Settled, outcome.Agrees = agrees, agrees
	}
	if !run.DryRun && !run.Blind {
		if err := a.Host.Record(ctx, run, automation, model.Model, result.Answer); err != nil {
			outcome.Status, outcome.Error = domain.AutomationRunFailed, err.Error()
		}
	}
	return outcome
}

// decideFromHistory acts on a verdict the history settled and reports whether
// it did.
func (a *Automations) decideFromHistory(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	subject store.Transaction, assessment CategoryAssessment, tools ToolRunner, policy writePolicy,
) (store.AutomationOutcome, bool) {
	verdict := assessment.Verdict
	// The pair id, not store.IsTransfer: a row filed under a transfer category
	// has a category, and whether it is the right one is this automation's
	// question.
	if subject.TransferPairID != uuid.Nil {
		return store.AutomationOutcome{
			Status:  domain.AutomationRunSkipped,
			Output:  "Skipped: one leg of a paired transfer, which the pairing files.",
			Settled: true,
		}, true
	}
	// A split row's categories are on its splits, so the history's one
	// category for a whole row is no answer for it. With every part filed
	// there is nothing to decide, unless the order behind it is in view: the
	// items can re-file a row split by them. Otherwise the model can propose
	// a split.
	if len(subject.Splits) > 0 {
		if assessment.Enriched || store.DomainTransaction(subject).IsUncategorized() {
			return store.AutomationOutcome{}, false
		}
		return store.AutomationOutcome{
			Status:  domain.AutomationRunSkipped,
			Output:  "Skipped: split across categories, every part filed.",
			Settled: true,
		}, true
	}
	account, err := a.store.GetAccount(ctx, run.SpaceID, subject.AccountID)
	if err != nil {
		return failed(err), true
	}
	// A reviewed category outranks the history, so only the model, told it
	// stands, may propose another. History that agrees settles the row below.
	if subject.CategoryID != uuid.Nil &&
		domain.ReviewedCategoryStands(subject.IsReviewed, account.Kind) &&
		(verdict.Kind != domain.CategoryVerdictConfident ||
			subject.CategoryID.String() != verdict.CategoryID) {
		return store.AutomationOutcome{}, false
	}
	switch {
	case verdict.Kind == domain.CategoryVerdictTransfer:
		why := fmt.Sprintf("%d of %d past rows for this payee are transfer legs",
			verdict.Transfers, verdict.Total)
		return a.decideTransfer(ctx, run, automation, subject, assessment,
			domain.TransferCategoryFor(account.Kind), verdict.Confidence, why, tools, policy)

	case verdict.Kind != domain.CategoryVerdictConfident &&
		domain.LooksLikeCardPayment(account.Kind, subject.Amount, subject.StatementName):
		return a.decideTransfer(ctx, run, automation, subject, assessment,
			domain.KnownCategoryCreditCardPayment, domain.CardPaymentConfidence,
			"money in on a credit card under a payment's wording, matched to no transfer",
			tools, policy)
	}
	switch verdict.Kind {

	case domain.CategoryVerdictConfident:
		evidence := fmt.Sprintf("%d of %d past rows, confidence %.2f",
			verdict.Agreeing, verdict.Voters, verdict.Confidence)
		if subject.CategoryID.String() == verdict.CategoryID {
			return store.AutomationOutcome{
				Status: domain.AutomationRunSkipped,
				Output: fmt.Sprintf("Already %s; history agrees (%s).",
					assessment.CategoryName, evidence),
				Settled: true,
				Agrees:  true,
			}, true
		}
		if automation.Mode == domain.AutomationModeObserve {
			return store.AutomationOutcome{
				Status: domain.AutomationRunSucceeded,
				Output: fmt.Sprintf("History: %s (%s). Row: %s. Observe only, nothing proposed.",
					assessment.CategoryName, evidence, currentCategory(run, subject)),
				Settled: true,
			}, true
		}
		summary := fmt.Sprintf("%s, per payee history (%s).",
			assessment.CategoryName, evidence)
		switch {
		case verdict.RefundOf != "":
			summary = fmt.Sprintf("%s, the category of the purchase of the same amount from "+
				"this merchant that this credit gives back.", assessment.CategoryName)
		case verdict.IsRefund:
			summary = fmt.Sprintf("%s, as this merchant's purchases say for a credit coming "+
				"back from it (%d of %d categorized purchases, confidence %.2f).",
				assessment.CategoryName, verdict.Agreeing, verdict.Voters, verdict.Confidence)
		}
		outcome, err := a.actFromHistory(ctx, run, automation, subject, assessment, summary,
			tools, policy)
		if err != nil {
			return failed(err), true
		}
		return outcome, true
	}
	return store.AutomationOutcome{}, false
}

// decideTransfer files a row the check finds to be a transfer that no pair
// matched under the transfer category marker names, through the check's mode.
func (a *Automations) decideTransfer(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	subject store.Transaction, assessment CategoryAssessment, marker string, confidence float64,
	why string, tools ToolRunner, policy writePolicy,
) (store.AutomationOutcome, bool) {
	byMarker, err := a.store.EnsureTransferCategories(ctx, run.SpaceID)
	if err != nil {
		return failed(err), true
	}
	category, err := a.store.GetCategory(ctx, run.SpaceID, byMarker[marker])
	if err != nil {
		return failed(err), true
	}
	evidence := fmt.Sprintf("%s, confidence %.2f", why, confidence)
	outcome := store.AutomationOutcome{Confidence: &confidence, Settled: true}
	switch {
	case subject.CategoryID == category.ID:
		outcome.Status = domain.AutomationRunSkipped
		outcome.Output = fmt.Sprintf("Already %s (%s).", category.Name, evidence)
		outcome.Agrees = true
		return outcome, true
	case automation.Mode == domain.AutomationModeObserve:
		outcome.Status = domain.AutomationRunSucceeded
		outcome.Output = fmt.Sprintf("A transfer: %s (%s). Row: %s. Observe only, nothing proposed.",
			category.Name, evidence, currentCategory(run, subject))
		return outcome, true
	}
	assessment.Verdict.CategoryID = category.ID.String()
	assessment.CategoryName = category.Name
	acted, err := a.actFromHistory(ctx, run, automation, subject, assessment,
		fmt.Sprintf("%s, a transfer (%s).", category.Name, evidence), tools, policy)
	if err != nil {
		return failed(err), true
	}
	acted.Confidence = &confidence
	return acted, true
}

// actFromHistory records the history's change and reports it in the thread.
func (a *Automations) actFromHistory(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	subject store.Transaction, assessment CategoryAssessment, summary string,
	tools ToolRunner, policy writePolicy,
) (store.AutomationOutcome, error) {
	prompt := "Decided by Agentifi from the payee's history; the model was not asked."
	conversation, err := a.openThread(ctx, run, automation, prompt, assessment.Text)
	if err != nil {
		return store.AutomationOutcome{}, err
	}
	verb, err := fileCategory(ctx, conversation.ID, subject, assessment.Verdict.CategoryID, summary,
		tools, policy)
	if err != nil {
		return store.AutomationOutcome{}, err
	}
	report := fmt.Sprintf("%s: %s Row was %s.", verb, summary, currentCategory(run, subject))
	note := store.AssistantMessage{
		ConversationID: conversation.ID, Role: domain.AssistantRoleAssistant, Content: report,
	}
	if err := a.store.AddAssistantMessage(ctx, run.SpaceID, &note); err != nil {
		return store.AutomationOutcome{}, err
	}
	return store.AutomationOutcome{Status: domain.AutomationRunSucceeded, Output: report}, nil
}

// fileStatedCategory files the category a model's answer names when the model
// proposed nothing itself: an answer that says "Water (id …), Confidence:
// 0.92" in prose is that proposal, and dropping it leaves the row
// undetermined with no card. It reports what it filed, or "" for nothing, and
// whether the answer named the row's own category.
func (a *Automations) fileStatedCategory(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	subject store.Transaction, conversationID uuid.UUID, answer string,
	tools ToolRunner, policy writePolicy,
) (string, bool, error) {
	// One category named in prose is a whole row's; on a split row it would
	// re-file every split, the ones a person filed included.
	if len(subject.Splits) > 0 || !policy.allowed || !automation.Context.Categories ||
		!slices.ContainsFunc(domain.AutomationToolsFor(automation.Mode, automation.Tools),
			func(tool domain.AssistantTool) bool { return tool.Name == "update_transaction" }) {
		return "", false, nil
	}
	confidence, stated := domain.StatedConfidence(answer)
	if !stated {
		return "", false, nil
	}
	if count, err := a.store.CountAssistantActions(ctx, run.SpaceID, conversationID); err != nil ||
		count > 0 {
		return "", false, err
	}
	categories, err := a.store.ListCategories(ctx, run.SpaceID, false)
	if err != nil {
		return "", false, err
	}
	known := make(map[string]bool, len(categories))
	names := make(map[string]string, len(categories))
	for _, category := range categories {
		if !store.DomainCategory(category).CanBeSuggested() {
			continue
		}
		known[category.ID.String()] = true
		names[category.ID.String()] = category.Name
	}
	categoryID, named := domain.StatedCategory(answer, known)
	if !named {
		return "", false, nil
	}
	if categoryID == subject.CategoryID.String() {
		return "", true, nil
	}
	summary := fmt.Sprintf("%s, as the model's answer names it (confidence %.2f)",
		names[categoryID], confidence)
	verb, err := fileCategory(ctx, conversationID, subject, categoryID, summary, tools, policy)
	if err != nil {
		return "", false, err
	}
	return fmt.Sprintf("%s: %s.", verb, summary), false, nil
}

// fileCategory records a category for the row through the same proposer a
// model's tool call reaches, so the card and the mode's rules are the same,
// and says what became of it.
func fileCategory(
	ctx context.Context, conversationID uuid.UUID, subject store.Transaction,
	categoryID, summary string, tools ToolRunner, policy writePolicy,
) (string, error) {
	proposer, able := tools.(ActionProposer)
	if !able {
		return "", fmt.Errorf("changes cannot be proposed here")
	}
	act, verb := proposer.Propose, "Proposed"
	switch {
	case policy.dryRun:
		act, verb = proposer.Simulate, "Would have proposed"
	case policy.apply:
		act, verb = proposer.ApplyNow, "Applied"
	}
	result, err := act(ctx, conversationID, "update_transaction", map[string]any{
		"transaction_id": subject.ID.String(),
		"category_id":    categoryID,
		"summary":        summary,
	})
	if err != nil {
		return "", err
	}
	// A refund waits as a card even where changes apply unasked.
	if held, _ := result.(map[string]any); held["proposed"] == true {
		verb = "Proposed"
	}
	return verb, nil
}

func currentCategory(run store.AutomationRun, subject store.Transaction) string {
	switch {
	case run.Blind && run.ExpectedCategoryID != uuid.Nil:
		return "categorized as " + run.ExpectedCategoryID.String() +
			", which was hidden from this run"
	case run.Blind:
		return "uncategorized, which was hidden from this run"
	case len(subject.Splits) > 0:
		return fmt.Sprintf("split %d ways", len(subject.Splits))
	case subject.CategoryID == uuid.Nil:
		return "uncategorized"
	}
	return "categorized as " + subject.CategoryID.String()
}

func (a *Automations) openThread(
	ctx context.Context, run store.AutomationRun, automation store.Automation,
	prompt, opening string,
) (store.AssistantConversation, error) {
	conversation := store.AssistantConversation{
		UserID: automation.CreatedBy, AutomationRunID: run.ID,
		Title: titleFrom(automation.Name + " · " + run.Subject),
	}
	if err := a.store.CreateAssistantConversation(ctx, run.SpaceID, &conversation); err != nil {
		return store.AssistantConversation{}, err
	}
	if err := a.store.StartAutomationRun(ctx, run.SpaceID, run.ID, conversation.ID, prompt); err != nil {
		return store.AssistantConversation{}, err
	}
	first := store.AssistantMessage{
		ConversationID: conversation.ID, Role: domain.AssistantRoleUser, Content: opening,
	}
	if err := a.store.AddAssistantMessage(ctx, run.SpaceID, &first); err != nil {
		return store.AssistantConversation{}, err
	}
	return conversation, nil
}

// Preview renders a run's system prompt and opening message without running
// it, through the same functions the run uses.
func (a *Automations) Preview(
	ctx context.Context, spaceID store.SpaceID, automation store.Automation, transactionID uuid.UUID,
) (prompt, opening string, err error) {
	if a.Host == nil {
		return "", "", fmt.Errorf("automations cannot run in this process")
	}
	opening, err = a.Host.ContextFor(ctx, spaceID, automation.CreatedBy, automation, transactionID,
		false)
	if err != nil {
		return "", "", err
	}
	if strings.TrimSpace(opening) == "" {
		opening = "Begin."
	}
	space, err := a.store.GetSpace(ctx, spaceID)
	if err != nil {
		return "", "", err
	}
	return domain.AutomationPrompt(
		automation.Mode, automation.Prompt, domain.DateOf(a.now()), space.PrimaryCurrency), opening, nil
}
