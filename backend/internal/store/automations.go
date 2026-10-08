package store

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/sqlitedb"
)

// The assistant's automations and their runs. The runs table is the queue — a
// run is inserted "queued" and a worker claims it — so a process that dies
// mid-run leaves a row that says so.

type Automation struct {
	ID            uuid.UUID
	CreatedBy     uuid.UUID
	Name          string
	Description   string
	IsEnabled     bool
	Trigger       string
	TriggerConfig domain.AutomationTriggerConfig
	// FilterID is which rows a transaction trigger fires on; Nil is every row.
	FilterID      uuid.UUID
	Prompt        string
	Context       domain.AutomationContext
	Mode          string
	Tools         []string
	Model         string
	MaxToolRounds int
	// TemplateKey is the built-in this was started from, blank if written from
	// scratch, so the service need not match on a name the household can edit.
	TemplateKey string
	// PromptFromTemplate and DescriptionFromTemplate mark a text the household
	// has not changed from its template's. Such a text is read from the
	// template, never from the row, so a rewritten template reaches it. Every
	// write sets them from the text it is given.
	PromptFromTemplate      bool
	DescriptionFromTemplate bool
	// ConfidenceThreshold is the vote score at or above which the payee's
	// history is acted on without the model. Zero always asks.
	ConfidenceThreshold float64
	CreatedAt           time.Time
	UpdatedAt           time.Time
}

type AutomationRun struct {
	ID             uuid.UUID
	SpaceID        SpaceID
	AutomationID   uuid.UUID
	FiredBy        string
	TransactionID  uuid.UUID
	ConversationID uuid.UUID
	// DryRun marks a run whose change tools recorded rather than proposed.
	DryRun bool
	// Blind marks a dry run shown the row with its category hidden, for
	// checking what it would have chosen. ExpectedCategoryID is what the row
	// actually said when the run was queued; Nil for an uncategorized row.
	Blind              bool
	ExpectedCategoryID uuid.UUID
	// BatchID is the suggestion batch that queued the run, Nil for none.
	// ReviewedWhenQueued is the row's review state then, which the batch's
	// comparison reads. Bulk runs wait behind every other run.
	BatchID            uuid.UUID
	ReviewedWhenQueued bool
	Bulk               bool
	// CategoryResult is what a finished run about a row concluded; empty for
	// a run still waiting, a dry or blind run, and one about no row.
	CategoryResult domain.CategoryCheckResult
	// Confidence is the model's own figure when it stated one, else the
	// history vote's score; nil when neither exists.
	Confidence *float64
	// DecidedBy is "agentifi", "model" or "" for a run that decided nothing.
	DecidedBy string
	Status    string
	Subject   string
	Prompt    string
	Output    string
	Error     string
	// ErrorCode names a failure the page can offer a fix for; see
	// AutomationOutcome.ErrorCode.
	ErrorCode  string
	ToolCalls  int
	Actions    int
	QueuedAt   time.Time
	StartedAt  *time.Time
	FinishedAt *time.Time
}

const automationColumns = `id, created_by, name, description, is_enabled, trigger, trigger_config,
	prompt, context, mode, tools, model, max_tool_rounds, confidence_threshold, template_key,
	prompt_from_template, description_from_template, filter_id, created_at, updated_at`

func scanAutomation(row scanner) (Automation, error) {
	var (
		one            Automation
		trigger, ctx   []byte
		tools          []byte
		triggerConfig  domain.AutomationTriggerConfig
		contextOptions domain.AutomationContext
		filterID       *uuid.UUID
	)
	err := row.Scan(&one.ID, &one.CreatedBy, &one.Name, &one.Description, &one.IsEnabled,
		&one.Trigger, &trigger, &one.Prompt, &ctx, &one.Mode, &tools, &one.Model,
		&one.MaxToolRounds, &one.ConfidenceThreshold, &one.TemplateKey,
		&one.PromptFromTemplate, &one.DescriptionFromTemplate,
		&filterID, &one.CreatedAt, &one.UpdatedAt)
	if err != nil {
		return Automation{}, err
	}
	if template, ok := domain.AutomationTemplateByKey(one.TemplateKey); ok {
		if one.PromptFromTemplate {
			one.Prompt = template.Prompt
		}
		if one.DescriptionFromTemplate {
			one.Description = template.Description
		}
	}
	one.FilterID = Deref(filterID)
	if len(trigger) > 0 {
		_ = json.Unmarshal(trigger, &triggerConfig)
	}
	if len(ctx) > 0 {
		_ = json.Unmarshal(ctx, &contextOptions)
	}
	one.TriggerConfig, one.Context = triggerConfig, contextOptions
	one.Tools = []string{}
	if len(tools) > 0 {
		_ = json.Unmarshal(tools, &one.Tools)
	}
	return one, nil
}

func (s *Store) ListAutomations(ctx context.Context, spaceID SpaceID) ([]Automation, error) {
	return queryAll(ctx, s.db, "store: list automations", scanAutomation,
		`SELECT `+automationColumns+` FROM assistant_automations
		  WHERE space_id = $1 ORDER BY created_at, id`, spaceID.UUID())
}

// ListAutomationsByTrigger reads every enabled automation with one trigger,
// across every space — deliberately the only cross-space read here, for the
// once-per-process daily worker. Each row carries its space, and every
// following write is scoped to it.
func (s *Store) ListAutomationsByTrigger(
	ctx context.Context, trigger string,
) ([]Automation, []SpaceID, error) {
	type inSpace struct {
		Automation
		space uuid.UUID
	}
	found, err := queryAll(ctx, s.db, "store: list automations by trigger", func(row scanner) (inSpace, error) {
		var one inSpace
		automation, err := scanAutomation(trailing{row, []any{&one.space}})
		one.Automation = automation
		return one, err
	},
		`SELECT `+automationColumns+`, space_id FROM assistant_automations
		  WHERE trigger = $1 AND is_enabled ORDER BY created_at, id`, trigger)
	if err != nil {
		return nil, nil, err
	}
	out := make([]Automation, len(found))
	spaces := make([]SpaceID, len(found))
	for at, one := range found {
		out[at], spaces[at] = one.Automation, SpaceIDOf(one.space)
	}
	return out, spaces, nil
}

func (s *Store) GetAutomation(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Automation, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+automationColumns+` FROM assistant_automations WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id)
	one, err := scanAutomation(row)
	return one, wrap("store: get automation", err)
}

func (s *Store) CreateAutomation(ctx context.Context, spaceID SpaceID, one *Automation) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	trigger, cfg, tools, err := encodeAutomation(one)
	if err != nil {
		return wrap("store: create automation", err)
	}
	err = s.db.QueryRow(ctx,
		`INSERT INTO assistant_automations
		     (id, space_id, created_by, name, description, is_enabled, trigger, trigger_config,
		      prompt, context, mode, tools, model, max_tool_rounds, confidence_threshold,
		      template_key, filter_id, prompt_from_template, description_from_template)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16, $17,
		         $18, $19)
		 RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), one.CreatedBy, one.Name, one.Description, one.IsEnabled,
		one.Trigger, trigger, one.Prompt, cfg, one.Mode, tools, one.Model, one.MaxToolRounds,
		one.ConfidenceThreshold, one.TemplateKey, dbconv.NullUUID(one.FilterID),
		one.PromptFromTemplate, one.DescriptionFromTemplate).
		Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create automation", err)
}

func (s *Store) UpdateAutomation(ctx context.Context, spaceID SpaceID, one *Automation) error {
	trigger, cfg, tools, err := encodeAutomation(one)
	if err != nil {
		return wrap("store: update automation", err)
	}
	err = s.db.QueryRow(ctx,
		`UPDATE assistant_automations
		    SET name = $3, description = $4, is_enabled = $5, trigger = $6, trigger_config = $7,
		        prompt = $8, context = $9, mode = $10, tools = $11, model = $12,
		        max_tool_rounds = $13, confidence_threshold = $14, filter_id = $15,
		        prompt_from_template = $16, description_from_template = $17, updated_at = now()
		  WHERE space_id = $1 AND id = $2 RETURNING updated_at`,
		spaceID.UUID(), one.ID, one.Name, one.Description, one.IsEnabled, one.Trigger, trigger,
		one.Prompt, cfg, one.Mode, tools, one.Model, one.MaxToolRounds, one.ConfidenceThreshold,
		dbconv.NullUUID(one.FilterID), one.PromptFromTemplate, one.DescriptionFromTemplate).
		Scan(&one.UpdatedAt)
	return wrap("store: update automation", err)
}

// DeleteAutomation removes an automation, its runs, and the conversations the
// runs wrote. The runs cascade; the conversations do not (the run points at
// them), so they go by hand.
func (s *Store) DeleteAutomation(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	if _, err := s.db.Exec(ctx,
		`DELETE FROM assistant_conversations
		  WHERE space_id = $1 AND automation_run_id IN
		        (SELECT id FROM assistant_automation_runs WHERE automation_id = $2)`,
		spaceID.UUID(), id); err != nil {
		return wrap("store: delete automation", err)
	}
	return s.execOne(ctx, "store: delete automation",
		`DELETE FROM assistant_automations WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
}

func AutomationFacts(txn Transaction, categories map[uuid.UUID]Category) domain.AutomationTransactionFacts {
	posting := domain.Posting{Txn: DomainTransaction(txn)}
	if category, ok := categories[txn.CategoryID]; ok && txn.CategoryID != uuid.Nil {
		posting.Category, posting.HasCategory = DomainCategory(category), true
	}
	return domain.AutomationTransactionFacts{
		Source:     txn.Source,
		IsTransfer: IsTransfer(txn, categories),
		IsDeleted:  txn.IsDeleted,
		Posting:    posting,
		Facets:     domain.Facets(Facets(txn)),
	}
}

// AutomationScopes reads the filter of every automation that has one, keyed by
// automation id; an absent automation fires on every row. A missing filter
// comes back empty rather than absent, and domain.AutomationTriggers fires an
// empty scope on nothing, so a vanished condition does not widen to the ledger.
func (s *Store) AutomationScopes(
	ctx context.Context, spaceID SpaceID, automations []Automation,
) (map[uuid.UUID]*domain.Filter, error) {
	out := map[uuid.UUID]*domain.Filter{}
	wanted := false
	for _, one := range automations {
		if one.FilterID != uuid.Nil {
			wanted = true
			break
		}
	}
	if !wanted {
		return out, nil
	}
	filters, err := s.ListFilters(ctx, spaceID, true)
	if err != nil {
		return nil, err
	}
	byID := make(map[uuid.UUID]Filter, len(filters))
	for _, filter := range filters {
		byID[filter.ID] = filter
	}
	for _, one := range automations {
		if one.FilterID == uuid.Nil {
			continue
		}
		scope := domain.Filter{}
		if filter, ok := byID[one.FilterID]; ok && !filter.IsDeleted {
			scope = DomainFilter(filter)
		}
		out[one.ID] = &scope
	}
	return out, nil
}

func encodeAutomation(one *Automation) (trigger, cfg, tools json.RawMessage, err error) {
	if trigger, err = json.Marshal(one.TriggerConfig); err != nil {
		return nil, nil, nil, err
	}
	if cfg, err = json.Marshal(one.Context); err != nil {
		return nil, nil, nil, err
	}
	if one.Tools == nil {
		one.Tools = []string{}
	}
	template, ok := domain.AutomationTemplateByKey(one.TemplateKey)
	one.PromptFromTemplate = ok && domain.SameAutomationText(one.Prompt, template.Prompt)
	one.DescriptionFromTemplate = ok && domain.SameAutomationText(one.Description, template.Description)
	if tools, err = json.Marshal(one.Tools); err != nil {
		return nil, nil, nil, err
	}
	return trigger, cfg, tools, nil
}

// --- Runs --------------------------------------------------------------------

const automationRunColumns = `id, space_id, automation_id, fired_by, transaction_id, conversation_id,
	dry_run, blind, expected_category_id, confidence, decided_by, status, subject, prompt, output,
	error, error_code, tool_calls, actions, queued_at, started_at, finished_at, batch_id,
	reviewed_when_queued, bulk, category_result`

func scanAutomationRun(row scanner) (AutomationRun, error) {
	var (
		one          AutomationRun
		spaceID      uuid.UUID
		transaction  *uuid.UUID
		conversation *uuid.UUID
		expected     *uuid.UUID
		batch        *uuid.UUID
		result       string
	)
	err := row.Scan(&one.ID, &spaceID, &one.AutomationID, &one.FiredBy, &transaction,
		&conversation, &one.DryRun, &one.Blind, &expected, &one.Confidence, &one.DecidedBy,
		&one.Status, &one.Subject,
		&one.Prompt, &one.Output, &one.Error, &one.ErrorCode,
		&one.ToolCalls, &one.Actions, &one.QueuedAt, &one.StartedAt, &one.FinishedAt, &batch,
		&one.ReviewedWhenQueued, &one.Bulk, &result)
	if err != nil {
		return AutomationRun{}, err
	}
	one.SpaceID = SpaceIDOf(spaceID)
	one.TransactionID = Deref(transaction)
	one.ConversationID = Deref(conversation)
	one.ExpectedCategoryID = Deref(expected)
	one.BatchID = Deref(batch)
	one.CategoryResult = domain.CategoryCheckResult(result)
	return one, nil
}

// ClearQueuedAutomationRun removes a queued run so a fresh one can take its
// place. Returns nil when there was nothing to clear.
func (s *Store) ClearQueuedAutomationRun(
	ctx context.Context, spaceID SpaceID, automationID, transactionID uuid.UUID,
) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM assistant_automation_runs
		  WHERE space_id = $1 AND automation_id = $2 AND transaction_id = $3 AND status = $4`,
		spaceID.UUID(), automationID, transactionID, domain.AutomationRunQueued)
	return wrap("store: clear queued automation run", err)
}

// ClearQueuedAutomationRuns is ClearQueuedAutomationRun for every pairing of
// these automations with these rows.
func (s *Store) ClearQueuedAutomationRuns(
	ctx context.Context, spaceID SpaceID, automationIDs, transactionIDs []uuid.UUID,
) error {
	if len(automationIDs) == 0 || len(transactionIDs) == 0 {
		return nil
	}
	_, err := s.db.Exec(ctx,
		`DELETE FROM assistant_automation_runs
		  WHERE space_id = $1 AND automation_id IN (SELECT value FROM json_each($2))
		    AND transaction_id IN (SELECT value FROM json_each($3))
		    AND status = $4`,
		spaceID.UUID(), automationIDs, transactionIDs, domain.AutomationRunQueued)
	return wrap("store: clear queued automation runs", err)
}

// HasQueuedAutomationRun reports whether a run of this automation about this
// row is waiting to start, whoever fired it.
func (s *Store) HasQueuedAutomationRun(
	ctx context.Context, spaceID SpaceID, automationID, transactionID uuid.UUID,
) (bool, error) {
	var exists bool
	err := s.db.QueryRow(ctx,
		`SELECT EXISTS (
		    SELECT 1 FROM assistant_automation_runs
		     WHERE space_id = $1 AND automation_id = $2 AND transaction_id = $3
		       AND status = $4)`,
		spaceID.UUID(), automationID, transactionID, domain.AutomationRunQueued).Scan(&exists)
	return exists, wrap("store: queued automation run", err)
}

// QueueAutomationRun inserts a run and reports whether it was new. A
// transaction-fired duplicate is dropped by the unique index rather than
// refused, so two servers settling one batch do not both run it.
func (s *Store) QueueAutomationRun(
	ctx context.Context, spaceID SpaceID, one *AutomationRun,
) (bool, error) {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	one.SpaceID = spaceID
	one.Status = domain.AutomationRunQueued
	// A blind run changes nothing by definition, whatever the caller said.
	if one.Blind {
		one.DryRun = true
	}
	var transaction, expected *uuid.UUID
	if one.TransactionID != uuid.Nil {
		transaction = &one.TransactionID
	}
	if one.ExpectedCategoryID != uuid.Nil {
		expected = &one.ExpectedCategoryID
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO assistant_automation_runs
		     (id, space_id, automation_id, fired_by, transaction_id, status, subject, dry_run,
		      blind, expected_category_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT DO NOTHING
		 RETURNING queued_at`,
		one.ID, spaceID.UUID(), one.AutomationID, one.FiredBy, transaction, one.Status,
		one.Subject, one.DryRun, one.Blind, expected).Scan(&one.QueuedAt)
	if errors.Is(err, sqlitedb.ErrNoRows) {
		return false, nil
	}
	return err == nil, wrap("store: queue automation run", err)
}

// QueueAutomationRuns inserts runs about rows, all fired the same way, in one
// statement, and reports how many were new. Dropped duplicates are as
// QueueAutomationRun drops them. Dry and blind runs go through
// QueueAutomationRun.
func (s *Store) QueueAutomationRuns(
	ctx context.Context, spaceID SpaceID, firedBy string, runs []AutomationRun,
) (int, error) {
	const chunk = 5000
	queued := 0
	for start := 0; start < len(runs); start += chunk {
		part := runs[start:min(start+chunk, len(runs))]
		ids := make([]uuid.UUID, len(part))
		automations := make([]uuid.UUID, len(part))
		transactions := make([]uuid.UUID, len(part))
		subjects := make([]string, len(part))
		batches := make([]*uuid.UUID, len(part))
		reviewed := make([]bool, len(part))
		bulk := make([]bool, len(part))
		for i, one := range part {
			ids[i] = uuid.New()
			automations[i], transactions[i], subjects[i] = one.AutomationID, one.TransactionID, one.Subject
			if one.BatchID != uuid.Nil {
				batch := one.BatchID
				batches[i] = &batch
			}
			reviewed[i], bulk[i] = one.ReviewedWhenQueued, one.Bulk
		}
		tag, err := s.db.Exec(ctx,
			`INSERT INTO assistant_automation_runs
			     (id, space_id, automation_id, fired_by, transaction_id, status, subject, batch_id,
			      reviewed_when_queued, bulk)
			 SELECT i.value, $1, a.value, $2, t.value, $3, j.value, b.value, r.value, k.value
			   FROM json_each($4) i
			   JOIN json_each($5) a ON a.key = i.key
			   JOIN json_each($6) t ON t.key = i.key
			   JOIN json_each($7) j ON j.key = i.key
			   JOIN json_each($8) b ON b.key = i.key
			   JOIN json_each($9) r ON r.key = i.key
			   JOIN json_each($10) k ON k.key = i.key
			  WHERE true
			 ON CONFLICT DO NOTHING`,
			spaceID.UUID(), firedBy, domain.AutomationRunQueued, ids, automations, transactions,
			subjects, batches, reviewed, bulk)
		if err != nil {
			return queued, wrap("store: queue automation runs", err)
		}
		queued += int(tag.RowsAffected())
	}
	return queued, nil
}

// ClaimQueuedAutomationRun takes the oldest queued run across every space and
// marks it running, any bulk run after every other; ErrNotFound when the
// queue is empty. One statement, so two workers never take the same run.
func (s *Store) ClaimQueuedAutomationRun(ctx context.Context) (AutomationRun, error) {
	row := s.db.QueryRow(ctx,
		`UPDATE assistant_automation_runs
		    SET status = $1, started_at = now()
		  WHERE id = (SELECT id FROM assistant_automation_runs
		               WHERE status = $2 ORDER BY bulk, queued_at, id LIMIT 1)
		 RETURNING `+automationRunColumns,
		domain.AutomationRunRunning, domain.AutomationRunQueued)
	one, err := scanAutomationRun(row)
	return one, wrap("store: claim automation run", err)
}

// ClaimAutomationRun marks one queued run as running, for the caller that
// wants to execute a specific run now. ErrNotFound if it is not queued.
func (s *Store) ClaimAutomationRun(ctx context.Context, spaceID SpaceID, id uuid.UUID) (AutomationRun, error) {
	row := s.db.QueryRow(ctx,
		`UPDATE assistant_automation_runs
		    SET status = $3, started_at = now()
		  WHERE space_id = $1 AND id = $2 AND status = $4
		 RETURNING `+automationRunColumns,
		spaceID.UUID(), id, domain.AutomationRunRunning, domain.AutomationRunQueued)
	one, err := scanAutomationRun(row)
	return one, wrap("store: claim automation run", err)
}

// FailAbandonedAutomationRuns marks every run still "running" as failed. Called
// once at worker start: such a run belonged to a process that is gone.
func (s *Store) FailAbandonedAutomationRuns(ctx context.Context) (int, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE assistant_automation_runs
		    SET status = $1, error = $2, finished_at = now()
		  WHERE status = $3`,
		domain.AutomationRunFailed,
		"the server restarted while this run was in progress", domain.AutomationRunRunning)
	if err != nil {
		return 0, wrap("store: fail abandoned runs", err)
	}
	return int(tag.RowsAffected()), nil
}

// StartAutomationRun records the thread and the prompt a run is working with.
func (s *Store) StartAutomationRun(
	ctx context.Context, spaceID SpaceID, id, conversationID uuid.UUID, prompt string,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE assistant_automation_runs SET conversation_id = $3, prompt = $4
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, conversationID, prompt)
	return wrap("store: start automation run", err)
}

type AutomationOutcome struct {
	Status string
	Output string
	Error  string
	// ErrorCode is set only for a failure one control fixes: the assistant not
	// set up or switched off, or changes switched off. Empty otherwise.
	ErrorCode string
	ToolCalls int
	Actions   int
	// Confidence is the model's stated figure or the vote's score, nil for neither.
	Confidence *float64
	DecidedBy  string
	// Settled is a run that reached an answer about the row's category
	// without proposing anything: a paired transfer leg, a payee whose history
	// says transfer, a category the history already confirms. Only an
	// unsettled run with no actions leaves the row undetermined. Not stored.
	Settled bool
	// Agrees is a settled run whose answer was the row's own category. Not
	// stored.
	Agrees bool
	// CategoryResult is stored; see AutomationRun.CategoryResult.
	CategoryResult domain.CategoryCheckResult
}

func (s *Store) FinishAutomationRun(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, outcome AutomationOutcome,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE assistant_automation_runs
		    SET status = $3, output = $4, error = $5, tool_calls = $6, actions = $7,
		        confidence = $8, decided_by = $9, error_code = $10, category_result = $11,
		        finished_at = now()
		  WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id, outcome.Status, outcome.Output, outcome.Error, outcome.ToolCalls,
		outcome.Actions, outcome.Confidence, outcome.DecidedBy, outcome.ErrorCode,
		string(outcome.CategoryResult))
	return wrap("store: finish automation run", err)
}

type AutomationRunQuery struct {
	AutomationID uuid.UUID
	Status       string
	Limit        int
}

func (s *Store) ListAutomationRuns(
	ctx context.Context, spaceID SpaceID, q AutomationRunQuery,
) ([]AutomationRun, error) {
	sql := `SELECT ` + automationRunColumns + ` FROM assistant_automation_runs WHERE space_id = $1`
	args := []any{spaceID.UUID()}
	if q.AutomationID != uuid.Nil {
		args = append(args, q.AutomationID)
		sql += ` AND automation_id = $2`
	}
	if q.Status != "" {
		args = append(args, q.Status)
		sql += ` AND status = $` + strconv.Itoa(len(args))
	}
	sql += ` ORDER BY queued_at DESC, id DESC`
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	sql += ` LIMIT $` + strconv.Itoa(len(args))

	return queryAll(ctx, s.db, "store: list automation runs", scanAutomationRun, sql, args...)
}

func (s *Store) GetAutomationRun(ctx context.Context, spaceID SpaceID, id uuid.UUID) (AutomationRun, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+automationRunColumns+` FROM assistant_automation_runs
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id)
	one, err := scanAutomationRun(row)
	return one, wrap("store: get automation run", err)
}

// LastAutomationRunAt is when an automation was most recently queued by its
// schedule, or nil if never. The daily trigger compares it with the window.
func (s *Store) LastAutomationRunAt(
	ctx context.Context, spaceID SpaceID, automationID uuid.UUID,
) (*time.Time, error) {
	var at *time.Time
	err := s.db.QueryRow(ctx,
		`SELECT max(queued_at) FROM assistant_automation_runs
		  WHERE space_id = $1 AND automation_id = $2 AND fired_by = $3`,
		spaceID.UUID(), automationID, domain.AutomationFiredBySchedule).Scan(&at)
	return at, wrap("store: last automation run", err)
}

// AutomationRunSummary is what a list of automations shows beside each one.
type AutomationRunSummary struct {
	Runs           int
	LastRunAt      *time.Time
	LastStatus     string
	PendingActions int
}

// AutomationRunSummaries reads the per-automation figures in one query each.
func (s *Store) AutomationRunSummaries(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID]AutomationRunSummary, error) {
	out := map[uuid.UUID]AutomationRunSummary{}
	type summaryOf struct {
		id uuid.UUID
		AutomationRunSummary
	}
	summaries, err := queryAll(ctx, s.db, "store: automation summaries", func(row scanner) (summaryOf, error) {
		var one summaryOf
		err := row.Scan(&one.id, &one.Runs, &one.LastRunAt, &one.LastStatus)
		return one, err
	},
		`SELECT r.automation_id, count(*), max(r.queued_at),
		        (SELECT l.status FROM assistant_automation_runs l
		          WHERE l.space_id = r.space_id AND l.automation_id = r.automation_id
		          ORDER BY l.queued_at DESC LIMIT 1)
		   FROM assistant_automation_runs r WHERE r.space_id = $1 GROUP BY r.automation_id`,
		spaceID.UUID())
	if err != nil {
		return nil, err
	}
	for _, one := range summaries {
		out[one.id] = one.AutomationRunSummary
	}

	pending, err := queryAll(ctx, s.db, "store: automation summaries", scanPair[uuid.UUID, int],
		`SELECT r.automation_id, count(*)
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		   JOIN assistant_automation_runs r ON r.id = c.automation_run_id
		  WHERE a.space_id = $1 AND a.status = $2
		  GROUP BY r.automation_id`, spaceID.UUID(), domain.AssistantActionPending)
	if err != nil {
		return nil, err
	}
	for _, one := range pending {
		summary := out[one.first]
		summary.PendingActions = one.second
		out[one.first] = summary
	}
	return out, nil
}

// PendingAutomationAction is one card waiting from a run, with the run it
// came from — the review queue's row.
type PendingAutomationAction struct {
	Action       AssistantAction
	RunID        uuid.UUID
	AutomationID uuid.UUID
	Subject      string
}

// ListPendingAutomationActions is every card still waiting from any run in the
// space, oldest first.
func (s *Store) ListPendingAutomationActions(
	ctx context.Context, spaceID SpaceID,
) ([]PendingAutomationAction, error) {
	return queryAll(ctx, s.db, "store: pending automation actions", func(row scanner) (PendingAutomationAction, error) {
		var one PendingAutomationAction
		action, err := scanAssistantAction(trailing{row,
			[]any{&one.RunID, &one.AutomationID, &one.Subject}})
		one.Action = action
		return one, err
	},
		`SELECT `+assistantActionColumns+`, r.id, r.automation_id, r.subject
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		   JOIN assistant_automation_runs r ON r.id = c.automation_run_id
		  WHERE a.space_id = $1 AND a.status = $2
		  ORDER BY a.created_at, a.id LIMIT 200`, spaceID.UUID(), domain.AssistantActionPending)
}

func (s *Store) CountAssistantActions(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID,
) (int, error) {
	var count int
	err := s.db.QueryRow(ctx,
		`SELECT count(*) FROM assistant_actions WHERE space_id = $1 AND conversation_id = $2`,
		spaceID.UUID(), conversationID).Scan(&count)
	return count, wrap("store: count actions", err)
}
