package store

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/domain"
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
		one              Automation
		trigger, ctx     []byte
		tools            []byte
		triggerConfig    domain.AutomationTriggerConfig
		contextOptions   domain.AutomationContext
		filterID         *uuid.UUID
		created, updated int64
	)
	err := row.Scan(&one.ID, &one.CreatedBy, &one.Name, &one.Description, &one.IsEnabled,
		&one.Trigger, &trigger, &one.Prompt, &ctx, &one.Mode, &tools, &one.Model,
		&one.MaxToolRounds, &one.ConfidenceThreshold, &one.TemplateKey,
		&one.PromptFromTemplate, &one.DescriptionFromTemplate,
		&filterID, &created, &updated)
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
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list automations", scanAutomation,
		`SELECT `+automationColumns+` FROM assistant_automations ORDER BY created_at, id`)
}

// ListAutomationsByTrigger reads every enabled automation with one trigger,
// across every live household from server.db, for the daily worker. Results
// merge by created_at and id (household enumeration breaks exact ties). Reads
// are not a cross-file snapshot; subsequent writes remain household-scoped.
func (s *Store) ListAutomationsByTrigger(
	ctx context.Context, trigger string,
) ([]Automation, []SpaceID, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return nil, nil, err
	}
	type inSpace struct {
		Automation
		space SpaceID
	}
	found := []inSpace{}
	for _, id := range ids {
		h, err := s.scoped(ctx, id)
		if err != nil {
			return nil, nil, err
		}
		db, err := h.household(ctx, id.UUID())
		if err != nil {
			return nil, nil, err
		}
		rows, err := sqliteQueryAll(ctx, db.Read(), "store: list automations by trigger", scanAutomation,
			`SELECT `+automationColumns+` FROM assistant_automations WHERE trigger = ? AND is_enabled`, trigger)
		if err != nil {
			return nil, nil, err
		}
		for _, one := range rows {
			found = append(found, inSpace{one, id})
		}
	}
	sort.SliceStable(found, func(i, j int) bool {
		if !found[i].CreatedAt.Equal(found[j].CreatedAt) {
			return found[i].CreatedAt.Before(found[j].CreatedAt)
		}
		return bytes.Compare(found[i].ID[:], found[j].ID[:]) < 0
	})
	out, spaces := make([]Automation, len(found)), make([]SpaceID, len(found))
	for i, one := range found {
		out[i], spaces[i] = one.Automation, one.space
	}
	return out, spaces, nil
}

func (s *Store) GetAutomation(ctx context.Context, spaceID SpaceID, id uuid.UUID) (Automation, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return Automation{}, err
	}
	one, err := scanAutomation(db.Read().QueryRowContext(ctx,
		`SELECT `+automationColumns+` FROM assistant_automations WHERE id = ?`, id))
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	now := db.Now()
	_, err = db.Write().ExecContext(ctx,
		`INSERT INTO assistant_automations
		     (id, created_by, name, description, is_enabled, trigger, trigger_config,
		      prompt, context, mode, tools, model, max_tool_rounds, confidence_threshold,
		      template_key, filter_id, prompt_from_template, description_from_template, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		one.ID, one.CreatedBy, one.Name, one.Description, one.IsEnabled,
		one.Trigger, string(trigger), one.Prompt, string(cfg), one.Mode, string(tools), one.Model, one.MaxToolRounds,
		one.ConfidenceThreshold, one.TemplateKey, NullUUID(one.FilterID),
		one.PromptFromTemplate, one.DescriptionFromTemplate, now, now)
	if err == nil {
		one.CreatedAt, one.UpdatedAt = fromUnixMillis(now), fromUnixMillis(now)
	}
	return wrap("store: create automation", err)
}

func (s *Store) UpdateAutomation(ctx context.Context, spaceID SpaceID, one *Automation) error {
	trigger, cfg, tools, err := encodeAutomation(one)
	if err != nil {
		return wrap("store: update automation", err)
	}
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	var updated int64
	err = db.Write().QueryRowContext(ctx,
		`UPDATE assistant_automations
		    SET name = ?, description = ?, is_enabled = ?, trigger = ?, trigger_config = ?,
		        prompt = ?, context = ?, mode = ?, tools = ?, model = ?,
		        max_tool_rounds = ?, confidence_threshold = ?, filter_id = ?,
		        prompt_from_template = ?, description_from_template = ?, updated_at = ?
		  WHERE id = ? RETURNING updated_at`,
		one.Name, one.Description, one.IsEnabled, one.Trigger, string(trigger),
		one.Prompt, string(cfg), one.Mode, string(tools), one.Model, one.MaxToolRounds, one.ConfidenceThreshold,
		NullUUID(one.FilterID), one.PromptFromTemplate, one.DescriptionFromTemplate, db.Now(), one.ID).
		Scan(&updated)
	if err == nil {
		one.UpdatedAt = fromUnixMillis(updated)
	}
	return wrap("store: update automation", err)
}

// DeleteAutomation removes an automation, its runs, and the conversations the
// runs wrote. The runs cascade; the conversations do not (the run points at
// them), so they go by hand.
func (s *Store) DeleteAutomation(ctx context.Context, spaceID SpaceID, id uuid.UUID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	return h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		if _, err := db.Write().ExecContext(ctx,
			`DELETE FROM assistant_conversations WHERE automation_run_id IN
			        (SELECT id FROM assistant_automation_runs WHERE automation_id = ?)`, id); err != nil {
			return wrap("store: delete automation", err)
		}
		return sqliteExecOne(ctx, db.Write(), "store: delete automation",
			`DELETE FROM assistant_automations WHERE id = ?`, id)
	})
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
			scope = s.DomainFilter(filter)
		}
		out[one.ID] = &scope
	}
	return out, nil
}

func encodeAutomation(one *Automation) (trigger, cfg, tools []byte, err error) {
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

const automationRunColumns = `id, automation_id, fired_by, transaction_id, conversation_id,
	dry_run, blind, expected_category_id, confidence, decided_by, status, subject, prompt, output,
	error, error_code, tool_calls, actions, queued_at, started_at, finished_at, batch_id,
	reviewed_when_queued, bulk, category_result`

func scanAutomationRun(row scanner, spaceID SpaceID) (AutomationRun, error) {
	var (
		one               AutomationRun
		queued            int64
		started, finished sql.Null[int64]
		transaction       *uuid.UUID
		conversation      *uuid.UUID
		expected          *uuid.UUID
		batch             *uuid.UUID
		result            string
	)
	err := row.Scan(&one.ID, &one.AutomationID, &one.FiredBy, &transaction,
		&conversation, &one.DryRun, &one.Blind, &expected, &one.Confidence, &one.DecidedBy,
		&one.Status, &one.Subject,
		&one.Prompt, &one.Output, &one.Error, &one.ErrorCode,
		&one.ToolCalls, &one.Actions, &queued, &started, &finished, &batch,
		&one.ReviewedWhenQueued, &one.Bulk, &result)
	if err != nil {
		return AutomationRun{}, err
	}
	one.SpaceID = spaceID
	one.QueuedAt, one.StartedAt, one.FinishedAt = fromUnixMillis(queued), nullableTime(started), nullableTime(finished)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`DELETE FROM assistant_automation_runs WHERE automation_id = ? AND transaction_id = ? AND status = ?`,
		automationID, transactionID, domain.AutomationRunQueued)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	automationIn, args := InArgs(automationIDs)
	transactionIn, transactionArgs := InArgs(transactionIDs)
	args = append(args, transactionArgs...)
	args = append(args, domain.AutomationRunQueued)
	_, err = db.Write().ExecContext(ctx,
		`DELETE FROM assistant_automation_runs WHERE automation_id IN `+automationIn+
			` AND transaction_id IN `+transactionIn+` AND status = ?`, args...)
	return wrap("store: clear queued automation runs", err)
}

// HasQueuedAutomationRun reports whether a run of this automation about this
// row is waiting to start, whoever fired it.
func (s *Store) HasQueuedAutomationRun(
	ctx context.Context, spaceID SpaceID, automationID, transactionID uuid.UUID,
) (bool, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	var exists bool
	err = db.Read().QueryRowContext(ctx,
		`SELECT EXISTS (SELECT 1 FROM assistant_automation_runs
		     WHERE automation_id = ? AND transaction_id = ? AND status = ?)`,
		automationID, transactionID, domain.AutomationRunQueued).Scan(&exists)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return false, err
	}
	var queued int64
	err = db.Write().QueryRowContext(ctx,
		`INSERT INTO assistant_automation_runs
		     (id, automation_id, fired_by, transaction_id, status, subject, dry_run,
		      blind, expected_category_id, queued_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO NOTHING RETURNING queued_at`,
		one.ID, one.AutomationID, one.FiredBy, transaction, one.Status,
		one.Subject, one.DryRun, one.Blind, expected, db.Now()).Scan(&queued)
	if err == nil {
		one.QueuedAt = fromUnixMillis(queued)
	}
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, wrap("store: queue automation run", err)
}

// QueueAutomationRuns inserts runs about rows in household write transactions.
// Dropped duplicates are as QueueAutomationRun drops them. Dry and blind runs
// go through QueueAutomationRun. Each chunk commits independently.
func (s *Store) QueueAutomationRuns(
	ctx context.Context, spaceID SpaceID, firedBy string, runs []AutomationRun,
) (int, error) {
	if len(runs) == 0 {
		return 0, nil
	}
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	queued := 0
	const chunk = 5000
	for start := 0; start < len(runs); start += chunk {
		added := 0
		err := h.InTx(ctx, func(tx *Store) error {
			db, err := tx.household(ctx, spaceID.UUID())
			if err != nil {
				return err
			}
			now := db.Now()
			for _, one := range runs[start:min(start+chunk, len(runs))] {
				result, err := db.Write().ExecContext(ctx, `INSERT INTO assistant_automation_runs
					(id, automation_id, fired_by, transaction_id, status, subject, batch_id,
					 reviewed_when_queued, bulk, queued_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
					ON CONFLICT DO NOTHING`, uuid.New(), one.AutomationID, firedBy,
					one.TransactionID, domain.AutomationRunQueued, one.Subject,
					NullUUID(one.BatchID), one.ReviewedWhenQueued, one.Bulk, now)
				if err != nil {
					return err
				}
				n, err := result.RowsAffected()
				if err != nil {
					return err
				}
				added += int(n)
			}
			return nil
		})
		if err != nil {
			return queued, wrap("store: queue automation runs", err)
		}
		queued += added
	}
	return queued, nil
}

// ClaimQueuedAutomationRun peeks the oldest queued run in every live household
// from server.db, then conditionally claims the best candidate in its file's
// write transaction. A lost race restarts the sweep; no run can be claimed twice.
// Ordering is bulk last, queued_at, id, household id. This is oldest-first among
// the observed candidates, not a cross-file snapshot: concurrent inserts can
// arrive after a file was peeked. Continuous non-bulk traffic can starve bulk.
func (s *Store) ClaimQueuedAutomationRun(ctx context.Context) (AutomationRun, error) {
	for {
		if err := ctx.Err(); err != nil {
			return AutomationRun{}, err
		}
		ids, err := s.householdIDs(ctx)
		if err != nil {
			return AutomationRun{}, err
		}
		var best AutomationRun
		for _, id := range ids {
			h, err := s.scoped(ctx, id)
			if err != nil {
				return AutomationRun{}, err
			}
			db, err := h.household(ctx, id.UUID())
			if err != nil {
				return AutomationRun{}, err
			}
			one := AutomationRun{SpaceID: id}
			var queued int64
			err = db.Read().QueryRowContext(ctx,
				`SELECT id, bulk, queued_at FROM assistant_automation_runs
				 WHERE status = ? ORDER BY bulk, queued_at, id LIMIT 1`,
				domain.AutomationRunQueued).Scan(&one.ID, &one.Bulk, &queued)
			one.QueuedAt = fromUnixMillis(queued)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return AutomationRun{}, wrap("store: peek automation run", err)
			}
			if best.ID == uuid.Nil || automationRunBefore(one, best) {
				best = one
			}
		}
		if best.ID == uuid.Nil {
			return AutomationRun{}, wrap("store: claim automation run", sql.ErrNoRows)
		}
		one, err := s.ClaimAutomationRun(ctx, best.SpaceID, best.ID)
		if errors.Is(err, ErrNotFound) {
			continue
		}
		return one, err
	}
}

func automationRunBefore(a, b AutomationRun) bool {
	if a.Bulk != b.Bulk {
		return !a.Bulk
	}
	if !a.QueuedAt.Equal(b.QueuedAt) {
		return a.QueuedAt.Before(b.QueuedAt)
	}
	if a.ID != b.ID {
		return bytes.Compare(a.ID[:], b.ID[:]) < 0
	}
	aSpace, bSpace := a.SpaceID.UUID(), b.SpaceID.UUID()
	return bytes.Compare(aSpace[:], bSpace[:]) < 0
}

// ClaimAutomationRun marks one queued run as running, for the caller that
// wants to execute a specific run now. ErrNotFound if it is not queued.
func (s *Store) ClaimAutomationRun(ctx context.Context, spaceID SpaceID, id uuid.UUID) (AutomationRun, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return AutomationRun{}, err
	}
	var one AutomationRun
	err = h.InTx(ctx, func(tx *Store) error {
		db, err := tx.household(ctx, spaceID.UUID())
		if err != nil {
			return err
		}
		one, err = scanAutomationRun(db.Write().QueryRowContext(ctx,
			`UPDATE assistant_automation_runs SET status = ?, started_at = ?
			  WHERE id = ? AND status = ? RETURNING `+automationRunColumns,
			domain.AutomationRunRunning, db.Now(), id, domain.AutomationRunQueued), spaceID)
		return err
	})
	return one, wrap("store: claim automation run", err)
}

// FailAbandonedAutomationRuns marks every run still "running" as failed. Called
// once at worker start: such a run belonged to a process that is gone.
// Files commit independently; an error returns the count already committed.
func (s *Store) FailAbandonedAutomationRuns(ctx context.Context) (int, error) {
	ids, err := s.householdIDs(ctx)
	if err != nil {
		return 0, err
	}
	total := 0
	for _, id := range ids {
		h, err := s.scoped(ctx, id)
		if err != nil {
			return total, err
		}
		db, err := h.household(ctx, id.UUID())
		if err != nil {
			return total, err
		}
		tag, err := db.Write().ExecContext(ctx,
			`UPDATE assistant_automation_runs SET status = ?, error = ?, finished_at = ? WHERE status = ?`,
			domain.AutomationRunFailed, "the server restarted while this run was in progress",
			db.Now(), domain.AutomationRunRunning)
		if err != nil {
			return total, wrap("store: fail abandoned runs", err)
		}
		n, err := tag.RowsAffected()
		if err != nil {
			return total, wrap("store: fail abandoned runs", err)
		}
		total += int(n)
	}
	return total, nil
}

// StartAutomationRun records the thread and the prompt a run is working with.
func (s *Store) StartAutomationRun(
	ctx context.Context, spaceID SpaceID, id, conversationID uuid.UUID, prompt string,
) error {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE assistant_automation_runs SET conversation_id = ?, prompt = ? WHERE id = ?`,
		conversationID, prompt, id)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return err
	}
	_, err = db.Write().ExecContext(ctx,
		`UPDATE assistant_automation_runs
		    SET status = ?, output = ?, error = ?, tool_calls = ?, actions = ?,
		        confidence = ?, decided_by = ?, error_code = ?, category_result = ?, finished_at = ?
		  WHERE id = ?`,
		outcome.Status, outcome.Output, outcome.Error, outcome.ToolCalls,
		outcome.Actions, outcome.Confidence, outcome.DecidedBy, outcome.ErrorCode,
		string(outcome.CategoryResult), db.Now(), id)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	query := `SELECT ` + automationRunColumns + ` FROM assistant_automation_runs WHERE 1 = 1`
	args := []any{}
	if q.AutomationID != uuid.Nil {
		args = append(args, q.AutomationID)
		query += ` AND automation_id = ?`
	}
	if q.Status != "" {
		args = append(args, q.Status)
		query += ` AND status = ?`
	}
	query += ` ORDER BY queued_at DESC, id DESC`
	limit := q.Limit
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	args = append(args, limit)
	query += ` LIMIT ?`
	return sqliteQueryAll(ctx, db.Read(), "store: list automation runs",
		func(row scanner) (AutomationRun, error) { return scanAutomationRun(row, spaceID) }, query, args...)
}

func (s *Store) GetAutomationRun(ctx context.Context, spaceID SpaceID, id uuid.UUID) (AutomationRun, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return AutomationRun{}, err
	}
	one, err := scanAutomationRun(db.Read().QueryRowContext(ctx,
		`SELECT `+automationRunColumns+` FROM assistant_automation_runs WHERE id = ?`, id), spaceID)
	return one, wrap("store: get automation run", err)
}

// LastAutomationRunAt is when an automation was most recently queued by its
// schedule, or nil if never. The daily trigger compares it with the window.
func (s *Store) LastAutomationRunAt(
	ctx context.Context, spaceID SpaceID, automationID uuid.UUID,
) (*time.Time, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	var at sql.Null[int64]
	err = db.Read().QueryRowContext(ctx,
		`SELECT max(queued_at) FROM assistant_automation_runs WHERE automation_id = ? AND fired_by = ?`,
		automationID, domain.AutomationFiredBySchedule).Scan(&at)
	return nullableTime(at), wrap("store: last automation run", err)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]AutomationRunSummary{}
	type summaryOf struct {
		id uuid.UUID
		AutomationRunSummary
	}
	summaries, err := sqliteQueryAll(ctx, db.Read(), "store: automation summaries", func(row scanner) (summaryOf, error) {
		var one summaryOf
		var at sql.Null[int64]
		err := row.Scan(&one.id, &one.Runs, &at, &one.LastStatus)
		one.LastRunAt = nullableTime(at)
		return one, err
	},
		`SELECT r.automation_id, count(*), max(r.queued_at),
		        (SELECT status FROM assistant_automation_runs latest
		          WHERE latest.automation_id = r.automation_id ORDER BY queued_at DESC, id DESC LIMIT 1)
		   FROM assistant_automation_runs r GROUP BY r.automation_id`)
	if err != nil {
		return nil, err
	}
	for _, one := range summaries {
		out[one.id] = one.AutomationRunSummary
	}

	pending, err := sqliteQueryAll(ctx, db.Read(), "store: automation summaries", scanPair[uuid.UUID, int],
		`SELECT r.automation_id, count(*)
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		   JOIN assistant_automation_runs r ON r.id = c.automation_run_id
		  WHERE a.status = ?
		  GROUP BY r.automation_id`, domain.AssistantActionPending)
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
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	return sqliteQueryAll(ctx, db.Read(), "store: pending automation actions", func(row scanner) (PendingAutomationAction, error) {
		var one PendingAutomationAction
		action, err := scanAssistantAction(scanExtra{row,
			[]any{&one.RunID, &one.AutomationID, &one.Subject}})
		one.Action = action
		return one, err
	},
		`SELECT `+assistantActionColumns+`, r.id, r.automation_id, r.subject
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		   JOIN assistant_automation_runs r ON r.id = c.automation_run_id
		  WHERE a.status = ?
		  ORDER BY a.created_at, a.id LIMIT 200`, domain.AssistantActionPending)
}

func (s *Store) CountAssistantActions(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID,
) (int, error) {
	db, err := s.householdDB(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	var count int
	err = db.Read().QueryRowContext(ctx,
		`SELECT count(*) FROM assistant_actions WHERE conversation_id = ?`, conversationID).Scan(&count)
	return count, wrap("store: count actions", err)
}
