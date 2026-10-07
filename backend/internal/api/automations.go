package api

import (
	"context"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The assistant's automations. Everything a person controls about a run is a
// field here, and everything a run did (prompt, opening message, tool calls,
// report, cards) is readable here.
//
// Not reachable in-process: an automation that edits automations is the
// assistant reaching into its own configuration.

func init() {
	Register(Resource{Prefix: "/assistant-automations", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", listAutomations)
		rt.Write(http.MethodPost, "/", createAutomation)
		rt.Read(http.MethodGet, "/templates", listAutomationTemplates)
		rt.Read(http.MethodGet, "/pending", listPendingAutomationActions)
		rt.Read(http.MethodGet, "/runs", listAllAutomationRuns)
		rt.Write(http.MethodPost, "/fire", fireAutomationsForTransactions)
		rt.Read(http.MethodGet, "/runs/{run_id}", readAutomationRun)
		rt.Read(http.MethodGet, "/{automation_id}", readAutomation)
		rt.Write(http.MethodPatch, "/{automation_id}", updateAutomation)
		rt.Write(http.MethodDelete, "/{automation_id}", deleteAutomation)
		rt.Write(http.MethodPost, "/{automation_id}/run", runAutomation)
		rt.Write(http.MethodPost, "/{automation_id}/preview", previewAutomation)
		rt.Read(http.MethodGet, "/{automation_id}/runs", listAutomationRuns)
	}})
}

// maxAutomationToolRounds is the most rounds an unattended automation may ask
// for.
const maxAutomationToolRounds = 20

type AutomationResponse struct {
	ID            uuid.UUID                      `json:"id"`
	Name          string                         `json:"name"`
	Description   string                         `json:"description"`
	IsEnabled     bool                           `json:"is_enabled"`
	Trigger       string                         `json:"trigger"`
	TriggerConfig domain.AutomationTriggerConfig `json:"trigger_config"`
	// FilterID and Filter are which rows a transaction trigger fires on, the
	// filter inlined so the editor can reopen it; both null is every row.
	FilterID      *uuid.UUID               `json:"filter_id"`
	Filter        *FilterResponse          `json:"filter"`
	Prompt        string                   `json:"prompt"`
	Context       domain.AutomationContext `json:"context"`
	Mode          string                   `json:"mode"`
	Tools         []string                 `json:"tools"`
	Model         string                   `json:"model"`
	MaxToolRounds int                      `json:"max_tool_rounds"`
	// ConfidenceThreshold is the history-vote score at or above which the
	// automation acts without the model. Zero always asks.
	ConfidenceThreshold float64 `json:"confidence_threshold"`
	// TemplateKey is the built-in this one was started from, blank for an
	// automation written from scratch.
	TemplateKey string    `json:"template_key"`
	CreatedBy   uuid.UUID `json:"created_by"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	// The figures the list shows beside each one.
	Runs           int        `json:"runs"`
	LastRunAt      *time.Time `json:"last_run_at"`
	LastStatus     string     `json:"last_status"`
	PendingActions int        `json:"pending_actions"`
}

// AutomationUpdate is the body of a create and of a patch. On a patch, nil
// leaves a field alone; an object field given at all replaces the whole object.
type AutomationUpdate struct {
	Name          *string                         `json:"name"`
	Description   *string                         `json:"description"`
	IsEnabled     *bool                           `json:"is_enabled"`
	Trigger       *string                         `json:"trigger"`
	TriggerConfig *domain.AutomationTriggerConfig `json:"trigger_config"`
	// Conditions replaces which rows the trigger fires on, written to a filter
	// the automation owns. Absent leaves them; an empty list drops the filter.
	Conditions          *[]FilterItemWrite        `json:"conditions"`
	Prompt              *string                   `json:"prompt"`
	Context             *domain.AutomationContext `json:"context"`
	Mode                *string                   `json:"mode"`
	Tools               *[]string                 `json:"tools"`
	Model               *string                   `json:"model"`
	MaxToolRounds       *int                      `json:"max_tool_rounds"`
	ConfidenceThreshold *float64                  `json:"confidence_threshold"`
	// TemplateKey records which built-in the editor started from: honoured on
	// create, ignored on patch.
	TemplateKey *string `json:"template_key"`
}

type AutomationRunResponse struct {
	ID             uuid.UUID  `json:"id"`
	AutomationID   uuid.UUID  `json:"automation_id"`
	FiredBy        string     `json:"fired_by"`
	TransactionID  *uuid.UUID `json:"transaction_id"`
	ConversationID *uuid.UUID `json:"conversation_id"`
	// DryRun marks a run whose change tools recorded what they would have
	// done — cards marked simulated, nothing changed, nothing waiting.
	DryRun bool `json:"dry_run"`
	// Blind marks a dry run that was shown the row with its category hidden;
	// ExpectedCategoryID is what the row actually said, for the comparison.
	Blind              bool       `json:"blind"`
	ExpectedCategoryID *uuid.UUID `json:"expected_category_id"`
	// Confidence is the history vote's score, null when no vote was taken.
	// DecidedBy is "agentifi" when the history settled the row without a
	// model call, "model" when the model was asked.
	Confidence *float64 `json:"confidence"`
	DecidedBy  string   `json:"decided_by"`
	Status     string   `json:"status"`
	Subject    string   `json:"subject"`
	// Prompt is the system prompt exactly as sent, and Output the final
	// report, per run, since the automation may have been edited since.
	Prompt string `json:"prompt"`
	Output string `json:"output"`
	Error  string `json:"error"`
	// ErrorCode is "assistant_unavailable" or "changes_off" for a failure one
	// control fixes, which the page offers beside it; empty otherwise.
	ErrorCode  string     `json:"error_code"`
	ToolCalls  int        `json:"tool_calls"`
	Actions    int        `json:"actions"`
	QueuedAt   time.Time  `json:"queued_at"`
	StartedAt  *time.Time `json:"started_at"`
	FinishedAt *time.Time `json:"finished_at"`
	// Conversation is the thread the run wrote — its opening message, every
	// tool call and the cards — on the detail endpoint only.
	Conversation *ConversationResponse `json:"conversation,omitempty"`
}

type RunAutomationRequest struct {
	// TransactionID runs it about one row, now, and answers with the finished
	// run. Recent queues it for the most recent rows that would have fired it.
	TransactionID *uuid.UUID `json:"transaction_id"`
	Recent        int        `json:"recent"`
	// DryRun does everything but change anything: change tools record what
	// they would have done. Works with the connection's changes switched off.
	DryRun bool `json:"dry_run"`
	// Blind is a dry run shown each row with its category hidden, to see what
	// it would have chosen against what the row says.
	Blind bool `json:"blind"`
}

// FireAutomationsRequest is rows to run the transaction automations on now,
// as if they had just arrived: the ones named, or with AllMatching every row
// the register's query on the URL matches, as "Mark all as reviewed" reads it.
type FireAutomationsRequest struct {
	TransactionIDs []uuid.UUID `json:"transaction_ids"`
	AllMatching    bool        `json:"all_matching"`
	Force          bool        `json:"force"`
}

// FireAutomationsResponse counts what a fire queued. BatchID is the batch to
// follow, null when nothing was queued.
type FireAutomationsResponse struct {
	Rows    int        `json:"rows"`
	Queued  int        `json:"queued"`
	BatchID *uuid.UUID `json:"batch_id"`
}

type PreviewAutomationRequest struct {
	TransactionID *uuid.UUID `json:"transaction_id"`
}

// AutomationPreviewResponse is what a run would be told, rendered without
// running it.
type AutomationPreviewResponse struct {
	Prompt  string   `json:"prompt"`
	Opening string   `json:"opening"`
	Tools   []string `json:"tools"`
	// TransactionID is the row the preview was rendered against, when the
	// caller left it to the server to choose.
	TransactionID *uuid.UUID `json:"transaction_id"`
}

// PendingAutomationActionResponse is one card waiting from a run.
type PendingAutomationActionResponse struct {
	ActionResponse
	// ConversationID is the run's thread, which is what a decision on the
	// card is made against.
	ConversationID uuid.UUID `json:"conversation_id"`
	RunID          uuid.UUID `json:"run_id"`
	AutomationID   uuid.UUID `json:"automation_id"`
	Subject        string    `json:"subject"`
}

func automationResponse(
	one store.Automation, summary store.AutomationRunSummary, filters map[uuid.UUID]store.Filter,
) AutomationResponse {
	tools := one.Tools
	if tools == nil {
		tools = []string{}
	}
	var filterID *uuid.UUID
	var filter *FilterResponse
	if one.FilterID != uuid.Nil {
		filterID = &one.FilterID
		// A filter that has gone missing is drawn as one with no items, which
		// is also how the trigger reads it: it fires on nothing.
		stored, ok := filters[one.FilterID]
		if !ok {
			stored = store.Filter{ID: one.FilterID, Scope: AutomationFilterScope}
		}
		rendered := filterResponse(stored)
		filter = &rendered
	}
	return AutomationResponse{
		ID: one.ID, Name: one.Name, Description: one.Description, IsEnabled: one.IsEnabled,
		Trigger: one.Trigger, TriggerConfig: one.TriggerConfig, Prompt: one.Prompt,
		FilterID: filterID, Filter: filter,
		Context: one.Context, Mode: one.Mode, Tools: tools, Model: one.Model,
		MaxToolRounds: one.MaxToolRounds, ConfidenceThreshold: one.ConfidenceThreshold,
		TemplateKey: one.TemplateKey, CreatedBy: one.CreatedBy,
		CreatedAt: one.CreatedAt, UpdatedAt: one.UpdatedAt,
		Runs: summary.Runs, LastRunAt: summary.LastRunAt, LastStatus: summary.LastStatus,
		PendingActions: summary.PendingActions,
	}
}

func runResponse(one store.AutomationRun) AutomationRunResponse {
	out := AutomationRunResponse{
		ID: one.ID, AutomationID: one.AutomationID, FiredBy: one.FiredBy, DryRun: one.DryRun,
		Blind: one.Blind, Confidence: one.Confidence, DecidedBy: one.DecidedBy,
		Status: one.Status, Subject: one.Subject, Prompt: one.Prompt, Output: one.Output,
		Error: one.Error, ErrorCode: one.ErrorCode,
		ToolCalls: one.ToolCalls, Actions: one.Actions, QueuedAt: one.QueuedAt,
		StartedAt: one.StartedAt, FinishedAt: one.FinishedAt,
	}
	if one.TransactionID != uuid.Nil {
		id := one.TransactionID
		out.TransactionID = &id
	}
	if one.ConversationID != uuid.Nil {
		id := one.ConversationID
		out.ConversationID = &id
	}
	if one.ExpectedCategoryID != uuid.Nil {
		id := one.ExpectedCategoryID
		out.ExpectedCategoryID = &id
	}
	return out
}

// automations is the service, built once per process. serve sets
// Env.Automations and starts its worker; elsewhere one is built on first use
// and the run-now path runs inline.
func (e *Env) automations() (*service.Automations, error) {
	if e.Automations != nil {
		return e.Automations, nil
	}
	e.lazyAutomations.Do(func() {
		e.lazyAutomationsValue, e.lazyAutomationsErr = NewAutomations(e)
	})
	return e.lazyAutomationsValue, e.lazyAutomationsErr
}

// lazyAutomationsState is the once-built fallback service. On Env rather than
// package-level so two environments in one test binary do not share one.
type lazyAutomationsState struct {
	lazyAutomations      sync.Once
	lazyAutomationsValue *service.Automations
	lazyAutomationsErr   error
}

// --- Automations -------------------------------------------------------------

func listAutomations(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListAutomations(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	summaries, err := env.DB.AutomationRunSummaries(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	filters, err := automationFilters(r.Context(), env, sp, rows...)
	if err != nil {
		return err
	}
	out := make([]AutomationResponse, 0, len(rows))
	for _, one := range rows {
		out = append(out, automationResponse(one, summaries[one.ID], filters))
	}
	return writeJSON(w, http.StatusOK, out)
}

// AutomationFilterScope marks the filters this resource creates and owns.
const AutomationFilterScope = "automation"

var automationConditions = conditionOwner{scope: AutomationFilterScope, noun: "automation trigger"}

// automationFilters loads the filters the given automations point at, in one
// query when any of them has one.
func automationFilters(
	ctx context.Context, env *Env, sp auth.SpaceContext, rows ...store.Automation,
) (map[uuid.UUID]store.Filter, error) {
	out := map[uuid.UUID]store.Filter{}
	wanted := false
	for _, one := range rows {
		wanted = wanted || one.FilterID != uuid.Nil
	}
	if !wanted {
		return out, nil
	}
	filters, err := env.DB.ListFilters(ctx, sp.ID(), false)
	if err != nil {
		return nil, err
	}
	for _, filter := range filters {
		out[filter.ID] = filter
	}
	return out, nil
}

// applyAutomationConditions writes a body's conditions to the filter the
// automation owns, creating it on first use. An empty list clears the pointer
// and retires the owned filter, so the trigger fires on every row again.
func applyAutomationConditions(
	ctx context.Context, env *Env, sp auth.SpaceContext, one *store.Automation,
	conditions *[]FilterItemWrite,
) error {
	if conditions == nil {
		return nil
	}
	if len(*conditions) == 0 {
		if one.FilterID != uuid.Nil {
			if err := env.DB.DeleteFilter(ctx, sp.ID(), one.FilterID); err != nil && !isNotFound(err) {
				return err
			}
		}
		one.FilterID = uuid.Nil
		return nil
	}
	if one.FilterID != uuid.Nil {
		return automationConditions.replaceOwnedConditions(
			ctx, env, sp, one.ID, one.FilterID, *conditions)
	}
	filter, err := automationConditions.writeOwnedFilter(ctx, env, sp, one.Name, *conditions)
	if err != nil {
		return err
	}
	one.FilterID = filter.ID
	return nil
}

// oneAutomationResponse renders one automation with its filter loaded.
func oneAutomationResponse(
	ctx context.Context, env *Env, sp auth.SpaceContext, one store.Automation,
	summary store.AutomationRunSummary,
) (AutomationResponse, error) {
	filters, err := automationFilters(ctx, env, sp, one)
	if err != nil {
		return AutomationResponse{}, err
	}
	return automationResponse(one, summary, filters), nil
}

func listAutomationTemplates(_ *Env, w http.ResponseWriter, _ *http.Request, _ auth.SpaceContext) error {
	return writeJSON(w, http.StatusOK, domain.AutomationTemplates)
}

func createAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body AutomationUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	one := store.Automation{
		CreatedBy: sp.UserID(), IsEnabled: true, Mode: domain.AutomationModePropose,
		Trigger: domain.AutomationTriggerManual, MaxToolRounds: 6, Tools: []string{},
		Context:             domain.AutomationContext{Transaction: true, Categories: true},
		ConfidenceThreshold: 0.85,
	}
	if err := applyAutomationUpdate(&one, body); err != nil {
		return err
	}
	if body.TemplateKey != nil {
		if _, ok := domain.AutomationTemplateByKey(*body.TemplateKey); ok {
			one.TemplateKey = *body.TemplateKey
		}
	}
	if err := applyAutomationConditions(r.Context(), env, sp, &one, body.Conditions); err != nil {
		return err
	}
	if err := env.DB.CreateAutomation(r.Context(), sp.ID(), &one); err != nil {
		return err
	}
	response, err := oneAutomationResponse(r.Context(), env, sp, one, store.AutomationRunSummary{})
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, response)
}

func readAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	one, err := automationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	summaries, err := env.DB.AutomationRunSummaries(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	response, err := oneAutomationResponse(r.Context(), env, sp, one, summaries[one.ID])
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func updateAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	one, err := automationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	var body AutomationUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyAutomationUpdate(&one, body); err != nil {
		return err
	}
	if err := applyAutomationConditions(r.Context(), env, sp, &one, body.Conditions); err != nil {
		return err
	}
	if err := env.DB.UpdateAutomation(r.Context(), sp.ID(), &one); err != nil {
		return notFoundAs(err, "Automation")
	}
	summaries, err := env.DB.AutomationRunSummaries(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	response, err := oneAutomationResponse(r.Context(), env, sp, one, summaries[one.ID])
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, response)
}

func deleteAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "automation_id", "Automation")
	if err != nil {
		return err
	}
	return deleted(w, env.DB.DeleteAutomation(r.Context(), sp.ID(), id), "Automation")
}

// applyAutomationUpdate folds a body into an automation and refuses what does
// not hold together, on save, since an unattended run has nobody to tell.
func applyAutomationUpdate(one *store.Automation, body AutomationUpdate) error {
	if body.Name != nil {
		one.Name = strings.TrimSpace(*body.Name)
	}
	if body.Description != nil {
		one.Description = strings.TrimSpace(*body.Description)
	}
	if body.IsEnabled != nil {
		one.IsEnabled = *body.IsEnabled
	}
	if body.Trigger != nil {
		one.Trigger = strings.TrimSpace(*body.Trigger)
	}
	if body.TriggerConfig != nil {
		one.TriggerConfig = *body.TriggerConfig
	}
	if body.Prompt != nil {
		one.Prompt = strings.TrimSpace(*body.Prompt)
	}
	if body.Context != nil {
		one.Context = *body.Context
	}
	if body.Mode != nil {
		one.Mode = strings.TrimSpace(*body.Mode)
	}
	if body.Tools != nil {
		one.Tools = *body.Tools
	}
	if body.Model != nil {
		one.Model = strings.TrimSpace(*body.Model)
	}
	if body.MaxToolRounds != nil {
		one.MaxToolRounds = *body.MaxToolRounds
	}
	if body.ConfidenceThreshold != nil {
		one.ConfidenceThreshold = *body.ConfidenceThreshold
	}

	if one.Name == "" {
		return errBadRequest("An automation needs a name")
	}
	if one.Prompt == "" {
		return errBadRequest("An automation needs instructions: say what it should do")
	}
	if !domain.AutomationTriggerIsValid(one.Trigger) {
		return errBadRequest("The trigger has to be %q, %q or %q", domain.AutomationTriggerTransaction,
			domain.AutomationTriggerDaily, domain.AutomationTriggerManual)
	}
	if !domain.AutomationModeIsValid(one.Mode) {
		return errBadRequest("The mode has to be %q, %q or %q", domain.AutomationModeObserve,
			domain.AutomationModePropose, domain.AutomationModeApply)
	}
	if one.Trigger == domain.AutomationTriggerDaily {
		if _, err := time.Parse("15:04", one.TriggerConfig.At); err != nil {
			return errBadRequest("A daily automation needs a time of day such as \"06:30\"")
		}
	}
	if one.MaxToolRounds < 1 || one.MaxToolRounds > maxAutomationToolRounds {
		return errBadRequest("Tool rounds have to be between 1 and %d", maxAutomationToolRounds)
	}
	if one.Context.SimilarTransactions < 0 || one.Context.SimilarTransactions > 50 {
		return errBadRequest("Similar transactions have to be between 0 and 50")
	}
	if one.ConfidenceThreshold < 0 || one.ConfidenceThreshold > 1 {
		return errBadRequest("The confidence threshold has to be between 0 and 1")
	}
	if one.Tools == nil {
		one.Tools = []string{}
	}
	for _, name := range one.Tools {
		tool, known := domain.AssistantToolByName(name)
		if !known {
			return errBadRequest("There is no tool called %q", name)
		}
		if tool.Writes && one.Mode == domain.AutomationModeObserve {
			return errBadRequest("%q changes something, and an observing automation cannot", name)
		}
	}
	return nil
}

func automationFromPath(env *Env, r *http.Request, sp auth.SpaceContext) (store.Automation, error) {
	return fromPath(r, sp, "automation_id", "Automation", env.DB.GetAutomation)
}

// --- Running -----------------------------------------------------------------

// runAutomation fires one by hand: about one row, inline, answering with the
// finished run; about several, queued for the worker.
func runAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	automation, err := automationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	var body RunAutomationRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if _, err := env.DB.GetAssistantConnection(r.Context(), sp.ID()); err != nil {
		if isNotFound(err) {
			return errConflict("the assistant has no model configured")
		}
		return err
	}
	automations, err := env.automations()
	if err != nil {
		return err
	}

	if body.Recent > 0 {
		if body.Recent > 20 {
			body.Recent = 20
		}
		rows, err := recentFiringTransactions(env, r, sp, automation, body.Recent)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errConflict("no recent transaction would fire this automation")
		}
		out := make([]AutomationRunResponse, 0, len(rows))
		for _, txn := range rows {
			run, err := automations.EnqueueManual(r.Context(), sp.ID(), automation, txn.ID,
				service.RunOptions{DryRun: body.DryRun, Blind: body.Blind})
			if err != nil {
				return err
			}
			out = append(out, runResponse(run))
		}
		if env.Automations != nil {
			automations.Kick()
		} else {
			automations.Drain(r.Context())
		}
		return writeJSON(w, http.StatusAccepted, out)
	}

	transactionID := uuid.Nil
	if body.TransactionID != nil {
		transactionID = *body.TransactionID
	} else if automation.Trigger == domain.AutomationTriggerTransaction {
		rows, err := recentFiringTransactions(env, r, sp, automation, 1)
		if err != nil {
			return err
		}
		if len(rows) == 0 {
			return errConflict("no recent transaction would fire this automation; " +
				"pick one to run it against")
		}
		transactionID = rows[0].ID
	}
	run, err := automations.EnqueueManual(r.Context(), sp.ID(), automation, transactionID,
		service.RunOptions{DryRun: body.DryRun, Blind: body.Blind})
	if err != nil {
		return notFoundAs(err, "Transaction")
	}
	finished, err := automations.ExecuteNow(r.Context(), run)
	if err != nil {
		return err
	}
	return writeRunDetail(env, w, r, sp, finished)
}

// fireAutomationsForTransactions queues every enabled transaction automation
// for the rows given, as a sync would, except that rows that fired before are
// not refused. Queued, not awaited, as one batch however many rows: the
// workers take its runs a few at a time, behind every other run once it is
// bulk.
func fireAutomationsForTransactions(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body FireAutomationsRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if !body.AllMatching && len(body.TransactionIDs) == 0 {
		return errBadRequest("Pick at least one transaction")
	}
	if body.AllMatching && len(body.TransactionIDs) > 0 {
		return errBadRequest("Name transactions or ask for all matching ones, not both")
	}
	automations, err := env.automations()
	if err != nil {
		return err
	}
	if err := requireTransactionAutomation(r, sp, automations); err != nil {
		return err
	}
	rows, err := firedRows(env, r, sp, body)
	if err != nil {
		return err
	}
	batch, queued, err := automations.FireSuggestionBatch(r.Context(), sp.ID(), sp.UserID(), rows,
		body.Force)
	if err != nil {
		return err
	}
	out := FireAutomationsResponse{Rows: len(rows), Queued: queued}
	if batch.ID != uuid.Nil {
		out.BatchID = &batch.ID
	}
	return writeJSON(w, http.StatusAccepted, out)
}

// firedRows loads the rows a fire names. Ids are read in pages so a long
// selection is not one enormous query; a row not in this space is left out.
func firedRows(
	env *Env, r *http.Request, sp auth.SpaceContext, body FireAutomationsRequest,
) ([]store.Transaction, error) {
	if body.AllMatching {
		query, err := registerQuery(r)
		if err != nil {
			return nil, err
		}
		postings, rows, err := matchingRegister(r.Context(), env, sp, query)
		if err != nil {
			return nil, err
		}
		if query.HidePadding {
			postings, _ = domain.FoldPadding(postings)
		}
		// A split row is a posting per part.
		seen := make(map[uuid.UUID]bool, len(postings))
		out := make([]store.Transaction, 0, len(postings))
		for _, posting := range postings {
			key, err := store.ParseID(posting.Txn.ID)
			if err != nil {
				return nil, err
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			out = append(out, rows[key])
		}
		return out, nil
	}
	const page = 1000
	out := make([]store.Transaction, 0, len(body.TransactionIDs))
	for start := 0; start < len(body.TransactionIDs); start += page {
		ids := body.TransactionIDs[start:min(start+page, len(body.TransactionIDs))]
		rows, err := env.DB.ListTransactions(r.Context(), sp.ID(),
			store.TransactionQuery{IDs: ids, IncludeEstimates: true})
		if err != nil {
			return nil, err
		}
		out = append(out, rows...)
	}
	return out, nil
}

// requireTransactionAutomation says, before queueing anything, why firing
// would do nothing, with the code the client offers the fix for.
func requireTransactionAutomation(
	r *http.Request, sp auth.SpaceContext, automations *service.Automations,
) error {
	why, err := automations.WhyNothingFires(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	switch why {
	case "":
		return nil
	case service.AssistantUnavailableCode:
		return errConflictCode(why, "The assistant is not set up, or it is switched off")
	case service.ChangesOffCode:
		return errConflictCode(why, "Suggestions are off: the assistant may not propose changes")
	default:
		return errConflictCode(why, "No enabled automation suggests categories")
	}
}

// recentFiringTransactions is the newest rows this automation is about, for a
// test run. The source rule does not apply, so a ledger that came from an
// import still has rows.
func recentFiringTransactions(
	env *Env, r *http.Request, sp auth.SpaceContext, automation store.Automation, limit int,
) ([]store.Transaction, error) {
	today := domain.DateOf(env.now())
	rows, err := env.DB.ListTransactions(r.Context(), sp.ID(), store.TransactionQuery{
		From: today.AddDays(-90), To: today.AddDays(7), Limit: 300,
	})
	if err != nil {
		return nil, err
	}
	// Deleted categories included: whether a row is a transfer does not change
	// because the category naming it was retired.
	categoryRows, err := env.DB.ListCategories(r.Context(), sp.ID(), true)
	if err != nil {
		return nil, err
	}
	categories := make(map[uuid.UUID]store.Category, len(categoryRows))
	for _, category := range categoryRows {
		categories[category.ID] = category
	}

	scopes, err := env.DB.AutomationScopes(r.Context(), sp.ID(), []store.Automation{automation})
	if err != nil {
		return nil, err
	}
	config, scope := automation.TriggerConfig, scopes[automation.ID]
	out := make([]store.Transaction, 0, limit)
	for _, txn := range rows {
		facts := store.AutomationFacts(txn, categories)
		if !domain.AutomationTriggers(config, scope, facts, domain.AutomationFiredByManual) {
			continue
		}
		out = append(out, txn)
		if len(out) >= limit {
			break
		}
	}
	return out, nil
}

// previewAutomation renders what a run would be told, from the saved
// automation with the body's unsaved edits applied.
func previewAutomation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	automation, err := automationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	var body struct {
		PreviewAutomationRequest
		AutomationUpdate
	}
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	if err := applyAutomationUpdate(&automation, body.AutomationUpdate); err != nil {
		return err
	}
	automations, err := env.automations()
	if err != nil {
		return err
	}

	transactionID := uuid.Nil
	var chosen *uuid.UUID
	if body.TransactionID != nil {
		transactionID = *body.TransactionID
	} else if automation.Trigger == domain.AutomationTriggerTransaction {
		rows, err := recentFiringTransactions(env, r, sp, automation, 1)
		if err != nil {
			return err
		}
		if len(rows) > 0 {
			transactionID = rows[0].ID
			chosen = &rows[0].ID
		}
	}
	prompt, opening, err := automations.Preview(r.Context(), sp.ID(), automation, transactionID)
	if err != nil {
		return notFoundAs(err, "Transaction")
	}
	tools := domain.AutomationToolsFor(automation.Mode, automation.Tools)
	names := make([]string, 0, len(tools))
	for _, one := range tools {
		names = append(names, one.Name)
	}
	return writeJSON(w, http.StatusOK, AutomationPreviewResponse{
		Prompt: prompt, Opening: opening, Tools: names, TransactionID: chosen,
	})
}

// --- Runs --------------------------------------------------------------------

func listAutomationRuns(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	automation, err := automationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	return writeRuns(env, w, r, sp, store.AutomationRunQuery{AutomationID: automation.ID})
}

func listAllAutomationRuns(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	return writeRuns(env, w, r, sp, store.AutomationRunQuery{})
}

func writeRuns(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, query store.AutomationRunQuery,
) error {
	query.Status = strings.TrimSpace(r.URL.Query().Get("status"))
	limit, err := queryLimit(r, 50, 200)
	if err != nil {
		return err
	}
	query.Limit = limit
	rows, err := env.DB.ListAutomationRuns(r.Context(), sp.ID(), query)
	if err != nil {
		return err
	}
	out := make([]AutomationRunResponse, 0, len(rows))
	for _, one := range rows {
		out = append(out, runResponse(one))
	}
	return writeJSON(w, http.StatusOK, out)
}

func readAutomationRun(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	run, err := fromPath(r, sp, "run_id", "Run", env.DB.GetAutomationRun)
	if err != nil {
		return err
	}
	return writeRunDetail(env, w, r, sp, run)
}

func writeRunDetail(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext, run store.AutomationRun,
) error {
	out := runResponse(run)
	if run.ConversationID != uuid.Nil {
		conversation, err := env.DB.GetAssistantConversation(
			r.Context(), sp.ID(), sp.UserID(), run.ConversationID)
		if err != nil && !isNotFound(err) {
			return err
		}
		if err == nil {
			view, err := conversationView(env, r, sp, conversation)
			if err != nil {
				return err
			}
			out.Conversation = &view
		}
	}
	return writeJSON(w, http.StatusOK, out)
}

func listPendingAutomationActions(
	env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext,
) error {
	rows, err := env.DB.ListPendingAutomationActions(r.Context(), sp.ID())
	if err != nil {
		return err
	}
	out := make([]PendingAutomationActionResponse, 0, len(rows))
	for _, one := range rows {
		out = append(out, PendingAutomationActionResponse{
			ActionResponse: actionResponseFor(r.Context(), env, sp, one.Action),
			ConversationID: one.Action.ConversationID,
			RunID:          one.RunID, AutomationID: one.AutomationID, Subject: one.Subject,
		})
	}
	return writeJSON(w, http.StatusOK, out)
}
