package store

import (
	"context"
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/dbconv"
	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// The assistant's own rows. The API key is sealed and only
// AssistantConnectionKey reads it back; no query a response is built from
// returns it.

type AssistantConnection struct {
	ID        uuid.UUID
	Name      string
	BaseURL   string
	Model     string
	IsEnabled bool
	// AllowWrites lets the assistant propose changes.
	AllowWrites bool
	// ApplyWithoutAsking applies a proposal immediately rather than leaving a
	// card. Meaningless unless AllowWrites is on; read the two together.
	ApplyWithoutAsking bool
	// ToolCallStyle is provider.ToolCallsNative or provider.ToolCallsPrompted;
	// blank reads as native.
	ToolCallStyle string
	// HasKey says a key is stored, without saying what it is.
	HasKey    bool
	UpdatedAt time.Time
}

// AssistantAction is one change the assistant proposed. Method, Path and Body
// are the request as it will be issued against this deployment's own API, so
// the card cannot disagree with what actually runs.
type AssistantAction struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	ToolName       string
	Summary        string
	Method         string
	Path           string
	Body           map[string]any
	// ProposedBody is the model's original request, kept only when a person
	// changed a category before applying; nil means Body is both.
	ProposedBody map[string]any
	// Preview is the change with every id resolved to a name, written once by
	// the proposing tool.
	Preview map[string]any
	// GroupID joins the actions one bulk tool call proposed.
	GroupID uuid.UUID
	// ResourceID is what the request created or changed, read from its answer.
	ResourceID    uuid.UUID
	DeclineReason string
	Status        string
	Result        string
	StatusCode    int
	CreatedAt     time.Time
	DecidedAt     *time.Time
}

type AssistantConversation struct {
	ID     uuid.UUID
	UserID uuid.UUID
	// AutomationRunID is set on a thread an automation wrote. Such a thread is
	// the space's: it is left out of the history rail and any member can read
	// it and decide its cards.
	AutomationRunID uuid.UUID
	// MailID is the logged email the conversation is about, by reference only.
	MailID    uuid.UUID
	Title     string
	CreatedAt time.Time
	UpdatedAt time.Time
}

type AssistantMessage struct {
	ID             uuid.UUID
	ConversationID uuid.UUID
	Role           string
	Content        string
	ToolName       string
	ToolArguments  map[string]any
	CreatedAt      time.Time
}

// --- Connection --------------------------------------------------------------

func (s *Store) GetAssistantConnection(
	ctx context.Context, spaceID SpaceID,
) (AssistantConnection, error) {
	var (
		one    AssistantConnection
		sealed string
	)
	err := s.db.QueryRow(ctx,
		`SELECT id, name, base_url, model, api_key_encrypted, is_enabled, allow_writes,
		        apply_without_asking, tool_call_style, updated_at
		   FROM assistant_connections WHERE space_id = $1`, spaceID.UUID()).
		Scan(&one.ID, &one.Name, &one.BaseURL, &one.Model, &sealed, &one.IsEnabled,
			&one.AllowWrites, &one.ApplyWithoutAsking, &one.ToolCallStyle, &one.UpdatedAt)
	if err != nil {
		return AssistantConnection{}, wrap("store: get assistant connection", err)
	}
	one.HasKey = sealed != ""
	return one, nil
}

// SaveAssistantConnection writes the provider settings. An empty apiKey keeps
// the stored key, so the settings form never round-trips it.
func (s *Store) SaveAssistantConnection(
	ctx context.Context, spaceID SpaceID, one *AssistantConnection, apiKey string,
) error {
	sealed := ""
	if apiKey != "" {
		var err error
		if sealed, err = s.sealString(assistantKeyContext(spaceID), apiKey); err != nil {
			return err
		}
	}
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	style := one.ToolCallStyle
	if style == "" {
		style = "native"
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO assistant_connections
		     (id, space_id, name, base_url, model, api_key_encrypted, is_enabled,
		      allow_writes, apply_without_asking, tool_call_style)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10)
		 ON CONFLICT (space_id) DO UPDATE
		     SET name = EXCLUDED.name, base_url = EXCLUDED.base_url,
		         model = EXCLUDED.model, is_enabled = EXCLUDED.is_enabled,
		         allow_writes = EXCLUDED.allow_writes,
		         apply_without_asking = EXCLUDED.apply_without_asking,
		         tool_call_style = EXCLUDED.tool_call_style,
		         api_key_encrypted = CASE WHEN EXCLUDED.api_key_encrypted = ''
		             THEN assistant_connections.api_key_encrypted
		             ELSE EXCLUDED.api_key_encrypted END,
		         updated_at = now()
		 RETURNING id, api_key_encrypted <> ''`,
		one.ID, spaceID.UUID(), one.Name, one.BaseURL, one.Model, sealed, one.IsEnabled,
		one.AllowWrites, one.ApplyWithoutAsking, style).
		Scan(&one.ID, &one.HasKey)
	return wrap("store: save assistant connection", err)
}

// AssistantConnectionKey unseals the API key. The only path to it.
func (s *Store) AssistantConnectionKey(ctx context.Context, spaceID SpaceID) (string, error) {
	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	var sealed string
	err = s.db.QueryRow(ctx,
		`SELECT api_key_encrypted FROM assistant_connections WHERE space_id = $1`,
		spaceID.UUID()).Scan(&sealed)
	if err != nil {
		return "", wrap("store: assistant key", err)
	}
	if sealed == "" {
		return "", nil
	}
	return cipher.Open(assistantKeyContext(spaceID), sealed)
}

func (s *Store) DeleteAssistantConnection(ctx context.Context, spaceID SpaceID) error {
	_, err := s.db.Exec(ctx,
		`DELETE FROM assistant_connections WHERE space_id = $1`, spaceID.UUID())
	return wrap("store: delete assistant connection", err)
}

// --- Conversations -----------------------------------------------------------

func (s *Store) ListAssistantConversations(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) ([]AssistantConversation, error) {
	return queryAll(ctx, s.db, "store: list conversations", scanAssistantConversation,
		`SELECT `+conversationColumns+`
		   FROM assistant_conversations
		  WHERE space_id = $1 AND user_id = $2 AND automation_run_id IS NULL
		  ORDER BY updated_at DESC LIMIT 50`, spaceID.UUID(), userID)
}

const conversationColumns = `id, user_id, automation_run_id, mail_id, title, created_at, updated_at`

func scanAssistantConversation(row scanner) (AssistantConversation, error) {
	var (
		one       AssistantConversation
		run, mail *uuid.UUID
	)
	err := row.Scan(&one.ID, &one.UserID, &run, &mail, &one.Title, &one.CreatedAt, &one.UpdatedAt)
	one.AutomationRunID = Deref(run)
	one.MailID = Deref(mail)
	return one, err
}

// GetAssistantConversation reads one, scoped to the person who started it:
// sharing a ledger is not sharing your questions about it. A thread an
// automation wrote is readable by any member of the space.
func (s *Store) GetAssistantConversation(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (AssistantConversation, error) {
	one, err := scanAssistantConversation(s.db.QueryRow(ctx,
		`SELECT `+conversationColumns+`
		   FROM assistant_conversations
		  WHERE space_id = $1 AND id = $3 AND (user_id = $2 OR automation_run_id IS NOT NULL)`,
		spaceID.UUID(), userID, id))
	return one, wrap("store: get conversation", err)
}

func (s *Store) CreateAssistantConversation(
	ctx context.Context, spaceID SpaceID, one *AssistantConversation,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	var run, mail *uuid.UUID
	if one.AutomationRunID != uuid.Nil {
		run = &one.AutomationRunID
	}
	if one.MailID != uuid.Nil {
		mail = &one.MailID
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO assistant_conversations (id, space_id, user_id, automation_run_id, mail_id, title)
		 VALUES ($1, $2, $3, $4, $5, $6) RETURNING created_at, updated_at`,
		one.ID, spaceID.UUID(), one.UserID, run, mail, one.Title).Scan(&one.CreatedAt, &one.UpdatedAt)
	return wrap("store: create conversation", err)
}

func (s *Store) AssistantConversationMail(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
) (uuid.UUID, error) {
	var mail *uuid.UUID
	err := s.db.QueryRow(ctx,
		`SELECT mail_id FROM assistant_conversations WHERE space_id = $1 AND id = $2`,
		spaceID.UUID(), id).Scan(&mail)
	if err != nil || mail == nil {
		return uuid.Nil, wrap("store: conversation mail", err)
	}
	return *mail, nil
}

func (s *Store) TouchAssistantConversation(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, title string,
) error {
	_, err := s.db.Exec(ctx,
		`UPDATE assistant_conversations
		    SET updated_at = now(),
		        title = CASE WHEN title = '' THEN $3 ELSE title END
		  WHERE space_id = $1 AND id = $2`, spaceID.UUID(), id, title)
	return wrap("store: touch conversation", err)
}

func (s *Store) DeleteAssistantConversation(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) error {
	return s.execOne(ctx, "store: delete conversation",
		`DELETE FROM assistant_conversations WHERE space_id = $1 AND user_id = $2 AND id = $3`,
		spaceID.UUID(), userID, id)
}

// --- Messages ----------------------------------------------------------------

func (s *Store) ListAssistantMessages(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID,
) ([]AssistantMessage, error) {
	return queryAll(ctx, s.db, "store: list messages", func(row scanner) (AssistantMessage, error) {
		var (
			one       AssistantMessage
			arguments []byte
		)
		if err := row.Scan(&one.ID, &one.ConversationID, &one.Role, &one.Content,
			&one.ToolName, &arguments, &one.CreatedAt); err != nil {
			return AssistantMessage{}, err
		}
		if len(arguments) > 0 {
			_ = json.Unmarshal(arguments, &one.ToolArguments)
		}
		return one, nil
	},
		`SELECT id, conversation_id, role, content, tool_name, tool_arguments, created_at
		   FROM assistant_messages WHERE space_id = $1 AND conversation_id = $2
		  ORDER BY created_at, id`, spaceID.UUID(), conversationID)
}

func (s *Store) AddAssistantMessage(
	ctx context.Context, spaceID SpaceID, one *AssistantMessage,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	var arguments []byte
	if len(one.ToolArguments) > 0 {
		encoded, err := json.Marshal(one.ToolArguments)
		if err != nil {
			return wrap("store: add message", err)
		}
		arguments = encoded
	}
	err := s.db.QueryRow(ctx,
		`INSERT INTO assistant_messages
		     (id, space_id, conversation_id, role, content, tool_name, tool_arguments)
		 VALUES ($1, $2, $3, $4, $5, $6, $7) RETURNING created_at`,
		one.ID, spaceID.UUID(), one.ConversationID, one.Role, one.Content,
		one.ToolName, arguments).Scan(&one.CreatedAt)
	return wrap("store: add message", err)
}

var AssistantRoles = map[string]bool{
	domain.AssistantRoleUser:      true,
	domain.AssistantRoleAssistant: true,
	domain.AssistantRoleTool:      true,
	domain.AssistantRoleAction:    true,
}

// --- Proposed actions --------------------------------------------------------
//
// Nothing here applies a proposal; applying is issuing the request, which
// belongs to the API layer.

func (s *Store) ListAssistantActions(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID,
) ([]AssistantAction, error) {
	return queryAll(ctx, s.db, "store: list actions", scanAssistantAction,
		`SELECT `+assistantActionColumns+`
		   FROM assistant_actions a WHERE a.space_id = $1 AND a.conversation_id = $2
		  ORDER BY a.created_at, a.id`, spaceID.UUID(), conversationID)
}

// GetAssistantAction reads one, scoped to the space and to the conversation's
// owner. A card an automation proposed is the space's to decide.
func (s *Store) GetAssistantAction(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (AssistantAction, error) {
	row := s.db.QueryRow(ctx,
		`SELECT `+assistantActionColumns+`
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		  WHERE a.space_id = $1 AND a.id = $3
		    AND (c.user_id = $2 OR c.automation_run_id IS NOT NULL)`,
		spaceID.UUID(), userID, id)
	one, err := scanAssistantAction(row)
	return one, wrap("store: get action", err)
}

func (s *Store) CreateAssistantAction(
	ctx context.Context, spaceID SpaceID, one *AssistantAction,
) error {
	if one.ID == uuid.Nil {
		one.ID = uuid.New()
	}
	if one.Status == "" {
		one.Status = domain.AssistantActionPending
	}
	body, err := optionalJSON(one.Body)
	if err != nil {
		return wrap("store: create action", err)
	}
	preview, err := optionalJSON(one.Preview)
	if err != nil {
		return wrap("store: create action", err)
	}
	err = s.db.QueryRow(ctx,
		`INSERT INTO assistant_actions
		     (id, space_id, conversation_id, tool_name, summary, method, path, body, status,
		      preview, group_id)
		 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11) RETURNING created_at`,
		one.ID, spaceID.UUID(), one.ConversationID, one.ToolName, one.Summary,
		one.Method, one.Path, body, one.Status, preview, dbconv.NullUUID(one.GroupID)).
		Scan(&one.CreatedAt)
	return wrap("store: create action", err)
}

// optionalJSON encodes a map for a nullable jsonb column: nil stays SQL null.
func optionalJSON(value map[string]any) ([]byte, error) {
	if value == nil {
		return nil, nil
	}
	return json.Marshal(value)
}

// ClaimAssistantAction moves a card into "applying" before its request is
// issued, making Apply idempotent: the update names the states a card may be
// applied from (pending, or failed for a retry), so of two concurrent presses
// exactly one moves the row and the other gets ErrNotFound.
func (s *Store) ClaimAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
) (AssistantAction, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE assistant_actions
		    SET status = $3, result = '', status_code = 0, decided_at = now()
		  WHERE space_id = $1 AND id = $2 AND status IN ($4, $5)`,
		spaceID.UUID(), id, domain.AssistantActionApplying,
		domain.AssistantActionPending, domain.AssistantActionFailed)
	if err != nil {
		return AssistantAction{}, wrap("store: claim action", err)
	}
	if tag.RowsAffected() == 0 {
		return AssistantAction{}, ErrNotFound
	}
	row := s.db.QueryRow(ctx,
		`SELECT `+assistantActionColumns+` FROM assistant_actions a
		  WHERE a.space_id = $1 AND a.id = $2`, spaceID.UUID(), id)
	one, err := scanAssistantAction(row)
	return one, wrap("store: claim action", err)
}

// ReleaseStaleAssistantActions marks cards claimed before the cutoff and never
// finished (the process died) as failed, so they can be retried or declined.
// Returns how many it released.
func (s *Store) ReleaseStaleAssistantActions(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID, before time.Time, result string,
) (int64, error) {
	tag, err := s.db.Exec(ctx,
		`UPDATE assistant_actions SET status = $4, result = $5, status_code = 0
		  WHERE space_id = $1 AND conversation_id = $2 AND status = $6 AND decided_at < $3`,
		spaceID.UUID(), conversationID, before, domain.AssistantActionFailed, result,
		domain.AssistantActionApplying)
	if err != nil {
		return 0, wrap("store: release stale actions", err)
	}
	return tag.RowsAffected(), nil
}

func (s *Store) FinishAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, status, result string, code int,
	resource uuid.UUID,
) error {
	return s.execOne(ctx, "store: finish action",
		`UPDATE assistant_actions
		    SET status = $3, result = $4, status_code = $5, resource_id = $6, decided_at = now()
		  WHERE space_id = $1 AND id = $2 AND status = $7`,
		spaceID.UUID(), id, status, result, code, dbconv.NullUUID(resource),
		domain.AssistantActionApplying)
}

// DeclineAssistantAction settles a pending or failed card nobody will apply,
// keeping why.
func (s *Store) DeclineAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, reason string,
) error {
	return s.execOne(ctx, "store: decline action",
		`UPDATE assistant_actions
		    SET status = $3, decline_reason = $4, decided_at = now()
		  WHERE space_id = $1 AND id = $2 AND status IN ($5, $6)`,
		spaceID.UUID(), id, domain.AssistantActionDiscarded, reason,
		domain.AssistantActionPending, domain.AssistantActionFailed)
}

// DeclineRowSuggestions settles every suggestion still waiting on one row,
// keeping why, and returns them as settled.
func (s *Store) DeclineRowSuggestions(
	ctx context.Context, spaceID SpaceID, transactionID uuid.UUID, reason string,
) ([]AssistantAction, error) {
	rows, err := s.db.Query(ctx,
		`UPDATE assistant_actions a
		    SET status = $4, decline_reason = $5, decided_at = now()
		  WHERE a.space_id = $1 AND a.status = $2
		    AND `+rowSuggestionCondition+`
		    AND split_part(a.path, '/', 3) = $3
		RETURNING `+assistantActionColumns,
		spaceID.UUID(), domain.AssistantActionPending, transactionID.String(),
		domain.AssistantActionDiscarded, reason)
	if err != nil {
		return nil, wrap("store: decline row suggestions", err)
	}
	out, err := collect(rows, scanAssistantAction)
	return out, wrap("store: decline row suggestions", err)
}

// assistantActionColumns is what scanAssistantAction reads, aliased "a".
const assistantActionColumns = `a.id, a.conversation_id, a.tool_name, a.summary, a.method,
	a.path, a.body, a.proposed_body, a.status, a.result, a.status_code, a.created_at,
	a.decided_at, a.preview, a.group_id, a.resource_id, a.decline_reason`

func scanAssistantAction(row scanner) (AssistantAction, error) {
	var (
		one             AssistantAction
		body, proposed  []byte
		preview         []byte
		group, resource *uuid.UUID
	)
	err := row.Scan(&one.ID, &one.ConversationID, &one.ToolName, &one.Summary,
		&one.Method, &one.Path, &body, &proposed, &one.Status, &one.Result, &one.StatusCode,
		&one.CreatedAt, &one.DecidedAt, &preview, &group, &resource, &one.DeclineReason)
	if err != nil {
		return AssistantAction{}, err
	}
	if len(body) > 0 {
		_ = json.Unmarshal(body, &one.Body)
	}
	if len(proposed) > 0 {
		_ = json.Unmarshal(proposed, &one.ProposedBody)
	}
	if len(preview) > 0 {
		_ = json.Unmarshal(preview, &one.Preview)
	}
	one.GroupID = Deref(group)
	one.ResourceID = Deref(resource)
	return one, nil
}

// --- Corrections -------------------------------------------------------------
//
// What somebody chose instead when they changed a category on a card, one row
// per category changed. A log shown to the model on later runs about the same
// payee, not a rule. The wording is copied onto the row so a correction stays
// readable when the transaction is renamed or deleted.

type AssistantCorrection struct {
	ID            uuid.UUID
	ActionID      uuid.UUID
	TransactionID uuid.UUID
	StatementName string
	Payee         string
	// Amount is the row's, or the split's own for a category inside a split.
	Amount    domain.Money
	HasAmount bool
	ToolName  string
	// Memo is the split's memo, which on an Amazon row names the item.
	Memo string
	// Either id may be Nil: a row left uncategorized, or a category set on
	// one the model left empty.
	ProposedCategoryID uuid.UUID
	ChosenCategoryID   uuid.UUID
	CorrectedBy        uuid.UUID
	CreatedAt          time.Time
}

func (s *Store) CreateAssistantCorrections(
	ctx context.Context, spaceID SpaceID, rows []AssistantCorrection,
) error {
	for i := range rows {
		one := &rows[i]
		if one.ID == uuid.Nil {
			one.ID = uuid.New()
		}
		_, err := s.db.Exec(ctx,
			`INSERT INTO assistant_corrections
			     (id, space_id, action_id, transaction_id, statement_name, payee, amount,
			      tool_name, memo, proposed_category_id, chosen_category_id, corrected_by)
			 VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12)`,
			one.ID, spaceID.UUID(), one.ActionID, dbconv.NullUUID(one.TransactionID),
			one.StatementName, one.Payee, dbconv.NullMoney(one.Amount, one.HasAmount),
			one.ToolName, one.Memo, dbconv.NullUUID(one.ProposedCategoryID),
			dbconv.NullUUID(one.ChosenCategoryID), one.CorrectedBy)
		if err != nil {
			return wrap("store: create correction", err)
		}
	}
	return nil
}

// ListAssistantCorrections is the space's most recent corrections, newest
// first; the caller picks the relevant ones by payee tokens.
func (s *Store) ListAssistantCorrections(
	ctx context.Context, spaceID SpaceID, limit int,
) ([]AssistantCorrection, error) {
	if limit <= 0 {
		limit = 100
	}
	return queryAll(ctx, s.db, "store: list corrections", func(row scanner) (AssistantCorrection, error) {
		var (
			one           AssistantCorrection
			transactionID *uuid.UUID
			amount        dbconv.Number
			proposed      *uuid.UUID
			chosen        *uuid.UUID
		)
		if err := row.Scan(&one.ID, &one.ActionID, &transactionID, &one.StatementName,
			&one.Payee, &amount, &one.ToolName, &one.Memo, &proposed, &chosen,
			&one.CorrectedBy, &one.CreatedAt); err != nil {
			return AssistantCorrection{}, err
		}
		one.TransactionID = Deref(transactionID)
		value, present, err := dbconv.ReadNullMoney(amount, "assistant_corrections.amount")
		one.Amount, one.HasAmount = value, present
		one.ProposedCategoryID = Deref(proposed)
		one.ChosenCategoryID = Deref(chosen)
		return one, err
	},
		`SELECT id, action_id, transaction_id, statement_name, payee, amount, tool_name, memo,
		        proposed_category_id, chosen_category_id, corrected_by, created_at
		   FROM assistant_corrections WHERE space_id = $1
		  ORDER BY created_at DESC, id LIMIT $2`, spaceID.UUID(), limit)
}

// ReviseAssistantAction replaces a pending card's body, keeping the model's
// original beside it. Only the body: method and path are what the tool
// decided the change was, and letting them change would turn a reviewed card
// into a blank cheque against the API.
func (s *Store) ReviseAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, body, proposed map[string]any,
) error {
	encoded, err := json.Marshal(body)
	if err != nil {
		return wrap("store: revise action", err)
	}
	encodedProposed, err := json.Marshal(proposed)
	if err != nil {
		return wrap("store: revise action", err)
	}
	return s.execOne(ctx, "store: revise action",
		`UPDATE assistant_actions SET body = $3, proposed_body = $4
		  WHERE space_id = $1 AND id = $2 AND status IN ($5, $6)`,
		spaceID.UUID(), id, encoded, encodedProposed, domain.AssistantActionPending,
		domain.AssistantActionFailed)
}

// rowSuggestionCondition is which actions on assistant_actions `a` are drawn
// as a suggestion on a row. Shared by the two readers below so the filter and
// the cell cannot disagree.
const rowSuggestionCondition = `a.path LIKE '/transactions/%'
		    AND a.tool_name <> 'mark_reviewed'
		    AND (a.tool_name NOT IN ('update_transaction', 'update_transactions')
		         OR a.body ? 'category_id')`

// TransactionsWithSuggestion is every row in the space with a suggestion
// waiting on it, for the filter field that asks.
func (s *Store) TransactionsWithSuggestion(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID]bool, error) {
	subjects, err := queryAll(ctx, s.db, "store: transactions with a suggestion", scanValue[string],
		`SELECT DISTINCT split_part(a.path, '/', 3)
		   FROM assistant_actions a
		  WHERE a.space_id = $1 AND a.status = $2
		    AND `+rowSuggestionCondition,
		spaceID.UUID(), domain.AssistantActionPending)
	if err != nil {
		return nil, err
	}
	out := map[uuid.UUID]bool{}
	for _, subject := range subjects {
		if id, err := uuid.Parse(subject); err == nil {
			out[id] = true
		}
	}
	return out, nil
}

// PendingActionFor is the change waiting on one transaction, with the run it
// came from.
type PendingActionFor struct {
	Action AssistantAction
	// RunID is the proposing automation run; Nil for a conversation's card.
	RunID uuid.UUID
}

// PendingActionsForTransactions is the pending card for each row, in one query.
// The transaction is read from the request path (`/transactions/{id}` or
// `/transactions/{id}/splits`), since the request is the proposal. Where two
// cards wait on one row, the newest wins. An update that names no category is
// not a row suggestion: the cell would draw it as "Uncategorized".
func (s *Store) PendingActionsForTransactions(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID,
) (map[uuid.UUID]PendingActionFor, error) {
	out := map[uuid.UUID]PendingActionFor{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, id.String())
	}
	rows, err := s.db.Query(ctx,
		`SELECT a.id, a.conversation_id, a.tool_name, a.summary, a.method, a.path, a.body,
		        a.proposed_body, a.status, a.result, a.status_code, a.created_at, a.decided_at,
		        c.automation_run_id, split_part(a.path, '/', 3) AS subject
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		  WHERE a.space_id = $1 AND a.status = $2
		    AND `+rowSuggestionCondition+`
		    AND split_part(a.path, '/', 3) = ANY($3)
		  ORDER BY a.created_at, a.id`,
		spaceID.UUID(), domain.AssistantActionPending, keys)
	if err != nil {
		return nil, wrap("store: pending actions for transactions", err)
	}
	type pendingOn struct {
		subject string
		PendingActionFor
	}
	pending, err := collect(rows, func(row scanner) (pendingOn, error) {
		var (
			one      pendingOn
			body     []byte
			proposed []byte
			runID    *uuid.UUID
		)
		action := &one.Action
		if err := row.Scan(&action.ID, &action.ConversationID, &action.ToolName,
			&action.Summary, &action.Method, &action.Path, &body, &proposed, &action.Status,
			&action.Result, &action.StatusCode, &action.CreatedAt, &action.DecidedAt,
			&runID, &one.subject); err != nil {
			return pendingOn{}, err
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &action.Body)
		}
		if len(proposed) > 0 {
			_ = json.Unmarshal(proposed, &action.ProposedBody)
		}
		one.RunID = Deref(runID)
		return one, nil
	})
	if err != nil {
		return nil, wrap("store: pending actions for transactions", err)
	}
	for _, one := range pending {
		if txnID, err := uuid.Parse(one.subject); err == nil {
			out[txnID] = one.PendingActionFor
		}
	}
	return out, nil
}
