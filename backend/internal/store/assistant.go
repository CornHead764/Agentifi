package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/google/uuid"

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

// StatedConfidence is the confidence the proposal gives itself: the figure the
// model passed with it, or else the last one its summary states.
func (a AssistantAction) StatedConfidence() (float64, bool) {
	if value, ok := a.Preview["confidence"].(float64); ok && value >= 0 && value <= 1 {
		return value, true
	}
	return domain.SummaryConfidence(a.Summary)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return AssistantConnection{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return AssistantConnection{}, err
	}

	var (
		one     AssistantConnection
		sealed  string
		updated int64
	)
	err = db.Read().QueryRowContext(ctx, `SELECT id, name, base_url, model, api_key_encrypted, is_enabled, allow_writes,
		        apply_without_asking, tool_call_style, updated_at
		   FROM assistant_connections WHERE 1 = 1`).
		Scan(&one.ID, &one.Name, &one.BaseURL, &one.Model, &sealed, &one.IsEnabled,
			&one.AllowWrites, &one.ApplyWithoutAsking, &one.ToolCallStyle, &updated)
	if err != nil {
		return AssistantConnection{}, wrap("store: get assistant connection", err)
	}
	one.UpdatedAt = fromUnixMillis(updated)
	one.HasKey = sealed != ""
	return one, nil
}

// SaveAssistantConnection writes the provider settings. An empty apiKey keeps
// the stored key, so the settings form never round-trips it.
func (s *Store) SaveAssistantConnection(
	ctx context.Context, spaceID SpaceID, one *AssistantConnection, apiKey string,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

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
	err = db.Write().QueryRowContext(ctx, `INSERT INTO assistant_connections
		     (id, name, base_url, model, api_key_encrypted, is_enabled,
		      allow_writes, apply_without_asking, tool_call_style, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		 ON CONFLICT DO UPDATE
		     SET name = EXCLUDED.name, base_url = EXCLUDED.base_url,
		         model = EXCLUDED.model, is_enabled = EXCLUDED.is_enabled,
		         allow_writes = EXCLUDED.allow_writes,
		         apply_without_asking = EXCLUDED.apply_without_asking,
		         tool_call_style = EXCLUDED.tool_call_style,
		         api_key_encrypted = CASE WHEN EXCLUDED.api_key_encrypted = ''
		             THEN assistant_connections.api_key_encrypted
		             ELSE EXCLUDED.api_key_encrypted END,
		         updated_at = ?
		 RETURNING id, api_key_encrypted <> ''`, one.ID, one.Name, one.BaseURL, one.Model, sealed, one.IsEnabled, one.AllowWrites, one.ApplyWithoutAsking, style, db.Now(), db.Now(), db.Now()).
		Scan(&one.ID, &one.HasKey)
	if err == nil {
		one.UpdatedAt = fromUnixMillis(db.Now())
	}
	return wrap("store: save assistant connection", err)
}

// AssistantConnectionKey unseals the API key. The only path to it.
func (s *Store) AssistantConnectionKey(ctx context.Context, spaceID SpaceID) (string, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return "", err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return "", err
	}

	cipher, err := s.requireCipher()
	if err != nil {
		return "", err
	}
	var sealed string
	err = db.Read().QueryRowContext(ctx, `SELECT api_key_encrypted FROM assistant_connections WHERE 1 = 1`).Scan(&sealed)
	if err != nil {
		return "", wrap("store: assistant key", err)
	}
	if sealed == "" {
		return "", nil
	}
	return cipher.Open(assistantKeyContext(spaceID), sealed)
}

func (s *Store) DeleteAssistantConnection(ctx context.Context, spaceID SpaceID) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	_, err = db.Write().ExecContext(ctx, `DELETE FROM assistant_connections`)
	return wrap("store: delete assistant connection", err)
}

// --- Conversations -----------------------------------------------------------

func (s *Store) ListAssistantConversations(
	ctx context.Context, spaceID SpaceID, userID uuid.UUID,
) ([]AssistantConversation, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	return sqliteQueryAll(ctx, db.Read(), "store: list conversations", scanAssistantConversation, `SELECT `+conversationColumns+`
		   FROM assistant_conversations
		  WHERE user_id = ? AND automation_run_id IS NULL
		  ORDER BY updated_at DESC LIMIT 50`, userID)
}

const conversationColumns = `id, user_id, automation_run_id, mail_id, title, created_at, updated_at`

func scanAssistantConversation(row scanner) (AssistantConversation, error) {
	var (
		one              AssistantConversation
		run, mail        *uuid.UUID
		created, updated int64
	)
	err := row.Scan(&one.ID, &one.UserID, &run, &mail, &one.Title, &created, &updated)
	one.CreatedAt, one.UpdatedAt = fromUnixMillis(created), fromUnixMillis(updated)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return AssistantConversation{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return AssistantConversation{}, err
	}

	one, err := scanAssistantConversation(db.Read().QueryRowContext(ctx, `SELECT `+conversationColumns+`
		   FROM assistant_conversations
		  WHERE id = ? AND (user_id = ? OR automation_run_id IS NOT NULL)`, id, userID))
	return one, wrap("store: get conversation", err)
}

func (s *Store) CreateAssistantConversation(
	ctx context.Context, spaceID SpaceID, one *AssistantConversation,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

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
	var created, updated int64
	err = db.Write().QueryRowContext(ctx, `INSERT INTO assistant_conversations (id, user_id, automation_run_id, mail_id, title, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING created_at, updated_at`, one.ID, one.UserID, run, mail, one.Title, db.Now(), db.Now()).Scan(&created, &updated)
	if err == nil {
		one.CreatedAt = fromUnixMillis(created)
		one.UpdatedAt = fromUnixMillis(updated)
	}
	return wrap("store: create conversation", err)
}

func (s *Store) AssistantConversationMail(
	ctx context.Context, spaceID SpaceID, id uuid.UUID,
) (uuid.UUID, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return uuid.Nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return uuid.Nil, err
	}

	var mail *uuid.UUID
	err = db.Read().QueryRowContext(ctx, `SELECT mail_id FROM assistant_conversations WHERE id = ?`, id).Scan(&mail)
	if err != nil || mail == nil {
		return uuid.Nil, wrap("store: conversation mail", err)
	}
	return *mail, nil
}

func (s *Store) TouchAssistantConversation(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, title string,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	_, err = db.Write().ExecContext(ctx, `UPDATE assistant_conversations
		    SET updated_at = ?,
		        title = CASE WHEN title = '' THEN ? ELSE title END
		  WHERE id = ?`, db.Now(), title, id)
	return wrap("store: touch conversation", err)
}

func (s *Store) DeleteAssistantConversation(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	return sqliteExecOne(ctx, db.Write(), "store: delete conversation", `DELETE FROM assistant_conversations WHERE user_id = ? AND id = ?`, userID, id)
}

// --- Messages ----------------------------------------------------------------

func (s *Store) ListAssistantMessages(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID,
) ([]AssistantMessage, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	return sqliteQueryAll(ctx, db.Read(), "store: list messages", func(row scanner) (AssistantMessage, error) {
		var (
			one       AssistantMessage
			arguments []byte
			created   int64
		)
		if err := row.Scan(&one.ID, &one.ConversationID, &one.Role, &one.Content,
			&one.ToolName, &arguments, &created); err != nil {
			return AssistantMessage{}, err
		}
		one.CreatedAt = fromUnixMillis(created)
		if len(arguments) > 0 {
			_ = json.Unmarshal(arguments, &one.ToolArguments)
		}
		return one, nil
	}, `SELECT id, conversation_id, role, content, tool_name, tool_arguments, created_at
		   FROM assistant_messages WHERE conversation_id = ?
		  ORDER BY created_at, rowid`, conversationID)
}

func (s *Store) AddAssistantMessage(
	ctx context.Context, spaceID SpaceID, one *AssistantMessage,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

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
	var created int64
	err = db.Write().QueryRowContext(ctx, `INSERT INTO assistant_messages
		     (id, conversation_id, role, content, tool_name, tool_arguments, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?) RETURNING created_at`, one.ID, one.ConversationID, one.Role, one.Content, one.ToolName, nullableJSON(arguments), db.Now()).Scan(&created)
	if err == nil {
		one.CreatedAt = fromUnixMillis(created)
	}
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	return sqliteQueryAll(ctx, db.Read(), "store: list actions", scanAssistantAction, `SELECT `+assistantActionColumns+`
		   FROM assistant_actions a WHERE a.conversation_id = ?
		  ORDER BY a.created_at, a.id`, conversationID)
}

// GetAssistantAction reads one, scoped to the space and to the conversation's
// owner. A card an automation proposed is the space's to decide.
func (s *Store) GetAssistantAction(
	ctx context.Context, spaceID SpaceID, userID, id uuid.UUID,
) (AssistantAction, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return AssistantAction{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return AssistantAction{}, err
	}

	row := db.Read().QueryRowContext(ctx, `SELECT `+assistantActionColumns+`
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		  WHERE a.id = ?
		    AND (c.user_id = ? OR c.automation_run_id IS NOT NULL)`, id, userID)
	one, err := scanAssistantAction(row)
	return one, wrap("store: get action", err)
}

func (s *Store) CreateAssistantAction(
	ctx context.Context, spaceID SpaceID, one *AssistantAction,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

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
	var created int64
	err = db.Write().QueryRowContext(ctx, `INSERT INTO assistant_actions
		     (id, conversation_id, tool_name, summary, method, path, body, status,
		      preview, group_id, created_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING created_at`, one.ID, one.ConversationID, one.ToolName, one.Summary, one.Method, one.Path, body, one.Status, preview, pgconvDocumentUUID(one.GroupID), db.Now()).
		Scan(&created)
	if err == nil {
		one.CreatedAt = fromUnixMillis(created)
	}
	return wrap("store: create action", err)
}

// optionalJSON encodes a map for a nullable jsonb column: nil stays SQL null.
func optionalJSON(value map[string]any) (any, error) {
	if value == nil {
		return nil, nil
	}
	data, err := json.Marshal(value)
	if err != nil {
		return nil, err
	}
	return string(data), nil
}

// ClaimAssistantAction moves a card into "applying" before its request is
// issued, making Apply idempotent: the update names the states a card may be
// applied from (pending, or failed for a retry), so of two concurrent presses
// exactly one moves the row and the other gets ErrNotFound.
func (s *Store) ClaimAssistantAction(ctx context.Context, spaceID SpaceID, id uuid.UUID) (AssistantAction, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return AssistantAction{}, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return AssistantAction{}, err
	}
	one, err := scanAssistantAction(db.Write().QueryRowContext(ctx, `UPDATE assistant_actions SET status = ?, result = '', status_code = 0, decided_at = ? WHERE id = ? AND status IN (?, ?) RETURNING `+strings.ReplaceAll(assistantActionColumns, "a.", ""), domain.AssistantActionApplying, db.Now(), id, domain.AssistantActionPending, domain.AssistantActionFailed))
	return one, wrap("store: claim action", err)
}

// ReleaseStaleAssistantActions marks cards claimed before the cutoff and never
// finished (the process died) as failed, so they can be retried or declined.
// Returns how many it released.
func (s *Store) ReleaseStaleAssistantActions(
	ctx context.Context, spaceID SpaceID, conversationID uuid.UUID, before time.Time, result string,
) (int64, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return 0, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return 0, err
	}

	tag, err := db.Write().ExecContext(ctx, `UPDATE assistant_actions SET status = ?, result = ?, status_code = 0
		  WHERE conversation_id = ? AND status = ? AND decided_at < ?`, domain.AssistantActionFailed, result, conversationID, domain.AssistantActionApplying, unixMillis(before))
	if err != nil {
		return 0, wrap("store: release stale actions", err)
	}
	n, err := tag.RowsAffected()
	return n, wrap("store: release stale actions", err)
}

func (s *Store) FinishAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, status, result string, code int,
	resource uuid.UUID,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	return sqliteExecOne(ctx, db.Write(), "store: finish action", `UPDATE assistant_actions
		    SET status = ?, result = ?, status_code = ?, resource_id = ?, decided_at = ?
		  WHERE id = ? AND status = ?`, status, result, code, pgconvDocumentUUID(resource), db.Now(), id, domain.AssistantActionApplying)
}

// DeclineAssistantAction settles a pending or failed card nobody will apply,
// keeping why.
func (s *Store) DeclineAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, reason string,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	return sqliteExecOne(ctx, db.Write(), "store: decline action", `UPDATE assistant_actions
		    SET status = ?, decline_reason = ?, decided_at = ?
		  WHERE id = ? AND status IN (?, ?)`, domain.AssistantActionDiscarded, reason, db.Now(), id, domain.AssistantActionPending, domain.AssistantActionFailed)
}

// DeclineRowSuggestions settles every suggestion still waiting on one row,
// keeping why, and returns them as settled.
func (s *Store) DeclineRowSuggestions(
	ctx context.Context, spaceID SpaceID, transactionID uuid.UUID, reason string,
) ([]AssistantAction, error) {
	return s.DeclineSuggestionsOnRows(ctx, spaceID, []uuid.UUID{transactionID}, reason)
}

// DeclineSuggestionsOnRows is DeclineRowSuggestions for many rows in one
// statement.
func (s *Store) DeclineSuggestionsOnRows(
	ctx context.Context, spaceID SpaceID, transactionIDs []uuid.UUID, reason string,
) ([]AssistantAction, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	if len(transactionIDs) == 0 {
		return nil, nil
	}
	keys := make([]string, 0, len(transactionIDs))
	for _, id := range transactionIDs {
		keys = append(keys, id.String())
	}
	placeholders, values := inArgs(keys)
	args := []any{domain.AssistantActionDiscarded, reason, db.Now(), domain.AssistantActionPending}
	args = append(args, values...)
	return sqliteQueryAll(ctx, db.Write(), "store: decline row suggestions", scanAssistantAction,
		`UPDATE assistant_actions AS a SET status = ?, decline_reason = ?, decided_at = ? WHERE a.status = ? AND `+rowSuggestionCondition+` AND `+assistantSubject+` IN `+placeholders+` RETURNING `+strings.ReplaceAll(assistantActionColumns, "a.", ""), args...)
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
		created         int64
		decided         sql.Null[int64]
	)
	err := row.Scan(&one.ID, &one.ConversationID, &one.ToolName, &one.Summary,
		&one.Method, &one.Path, &body, &proposed, &one.Status, &one.Result, &one.StatusCode,
		&created, &decided, &preview, &group, &resource, &one.DeclineReason)
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
	one.CreatedAt, one.DecidedAt = fromUnixMillis(created), nullableTime(decided)
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	currency, err := h.primaryCurrency()
	if err != nil {
		return err
	}
	for i := range rows {
		one := &rows[i]
		if one.ID == uuid.Nil {
			one.ID = uuid.New()
		}
		var amount any
		if one.HasAmount {
			minor, err := toMinor(one.Amount.Decimal(), currency)
			if err != nil {
				return err
			}
			amount = minor
		}
		_, err := db.Write().ExecContext(ctx, `INSERT INTO assistant_corrections
			     (id, action_id, transaction_id, statement_name, payee, amount,
			      tool_name, memo, proposed_category_id, chosen_category_id, corrected_by, created_at)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`, one.ID, one.ActionID, pgconvDocumentUUID(one.TransactionID), one.StatementName, one.Payee, amount, one.ToolName, one.Memo, pgconvDocumentUUID(one.ProposedCategoryID), pgconvDocumentUUID(one.ChosenCategoryID), one.CorrectedBy, db.Now())
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
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	currency, err := h.primaryCurrency()
	if err != nil {
		return nil, err
	}
	if limit <= 0 {
		limit = 100
	}
	return sqliteQueryAll(ctx, db.Read(), "store: list corrections", func(row scanner) (AssistantCorrection, error) {
		var (
			one           AssistantCorrection
			transactionID *uuid.UUID
			amount        sql.Null[int64]
			created       int64
			proposed      *uuid.UUID
			chosen        *uuid.UUID
		)
		if err := row.Scan(&one.ID, &one.ActionID, &transactionID, &one.StatementName,
			&one.Payee, &amount, &one.ToolName, &one.Memo, &proposed, &chosen,
			&one.CorrectedBy, &created); err != nil {
			return AssistantCorrection{}, err
		}
		one.TransactionID = Deref(transactionID)
		one.CreatedAt = fromUnixMillis(created)
		one.HasAmount = amount.Valid
		if amount.Valid {
			value, err := fromMinor(amount.V, currency)
			if err != nil {
				return AssistantCorrection{}, err
			}
			one.Amount = domain.FromDecimal(value)
		}
		one.ProposedCategoryID = Deref(proposed)
		one.ChosenCategoryID = Deref(chosen)
		return one, nil
	}, `SELECT id, action_id, transaction_id, statement_name, payee, amount, tool_name, memo,
		        proposed_category_id, chosen_category_id, corrected_by, created_at
		   FROM assistant_corrections WHERE 1 = 1
		  ORDER BY created_at DESC, id LIMIT ?`, limit)
}

// ReviseAssistantAction replaces a pending card's body, keeping the model's
// original beside it. Only the body: method and path are what the tool
// decided the change was, and letting them change would turn a reviewed card
// into a blank cheque against the API.
func (s *Store) ReviseAssistantAction(
	ctx context.Context, spaceID SpaceID, id uuid.UUID, body, proposed map[string]any,
) error {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return err
	}

	encoded, err := json.Marshal(body)
	if err != nil {
		return wrap("store: revise action", err)
	}
	encodedProposed, err := json.Marshal(proposed)
	if err != nil {
		return wrap("store: revise action", err)
	}
	return sqliteExecOne(ctx, db.Write(), "store: revise action", `UPDATE assistant_actions SET body = ?, proposed_body = ?
		  WHERE id = ? AND status IN (?, ?)`, string(encoded), string(encodedProposed), id, domain.AssistantActionPending, domain.AssistantActionFailed)
}

// rowSuggestionCondition is which actions on assistant_actions `a` are drawn
// as a suggestion on a row. Shared by the two readers below so the filter and
// the cell cannot disagree.
// UUID subjects have a fixed width; keep the suffix outside the subject.
const assistantSubject = `substr(a.path, 15, 36)`

const rowSuggestionCondition = `a.path LIKE '/transactions/%'
		    AND a.tool_name <> 'mark_reviewed'
		    AND (a.tool_name NOT IN ('update_transaction', 'update_transactions')
		         OR json_type(a.body, '$.category_id') IS NOT NULL)`

// TransactionsWithSuggestion is every row in the space with a suggestion
// waiting on it, for the filter field that asks.
func (s *Store) TransactionsWithSuggestion(
	ctx context.Context, spaceID SpaceID,
) (map[uuid.UUID]bool, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	subjects, err := sqliteQueryAll(ctx, db.Read(), "store: transactions with a suggestion", scanValue[string], `SELECT DISTINCT `+assistantSubject+`
		   FROM assistant_actions a
		  WHERE a.status = ?
		    AND `+rowSuggestionCondition, domain.AssistantActionPending)
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
	// Confidence and DecidedBy are that run's, as it recorded them: nil and ""
	// for a conversation's card or a run still going.
	Confidence *float64
	DecidedBy  string
}

// PendingActionsForTransactions is the pending card for each row, in one query.
// The transaction is read from the request path (`/transactions/{id}` or
// `/transactions/{id}/splits`), since the request is the proposal. Where two
// cards wait on one row, the newest wins. An update that names no category is
// not a row suggestion: the cell would draw it as "Uncategorized".
func (s *Store) PendingActionsForTransactions(
	ctx context.Context, spaceID SpaceID, ids []uuid.UUID,
) (map[uuid.UUID]PendingActionFor, error) {
	h, err := s.scoped(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	db, err := h.household(ctx, spaceID.UUID())
	if err != nil {
		return nil, err
	}

	out := map[uuid.UUID]PendingActionFor{}
	if len(ids) == 0 {
		return out, nil
	}
	keys := make([]string, 0, len(ids))
	for _, id := range ids {
		keys = append(keys, id.String())
	}
	placeholders, values := inArgs(keys)
	args := append([]any{domain.AssistantActionPending}, values...)
	type pendingOn struct {
		subject string
		PendingActionFor
	}

	pending, err := sqliteQueryAll(ctx, db.Read(), "store: pending actions for transactions", func(row scanner) (pendingOn, error) {
		var (
			one      pendingOn
			body     []byte
			proposed []byte
			preview  []byte
			runID    *uuid.UUID
			created  int64
			decided  sql.Null[int64]
		)
		action := &one.Action
		if err := row.Scan(&action.ID, &action.ConversationID, &action.ToolName,
			&action.Summary, &action.Method, &action.Path, &body, &proposed, &action.Status,
			&action.Result, &action.StatusCode, &created, &decided,
			&runID, &one.subject, &preview, &one.Confidence, &one.DecidedBy); err != nil {
			return pendingOn{}, err
		}
		if len(body) > 0 {
			_ = json.Unmarshal(body, &action.Body)
		}
		if len(proposed) > 0 {
			_ = json.Unmarshal(proposed, &action.ProposedBody)
		}
		if len(preview) > 0 {
			_ = json.Unmarshal(preview, &action.Preview)
		}
		action.CreatedAt, action.DecidedAt = fromUnixMillis(created), nullableTime(decided)
		one.RunID = Deref(runID)
		return one, nil
	}, `SELECT a.id, a.conversation_id, a.tool_name, a.summary, a.method, a.path, a.body,
		        a.proposed_body, a.status, a.result, a.status_code, a.created_at, a.decided_at,
		        c.automation_run_id, `+assistantSubject+` AS subject,
		        a.preview, r.confidence, COALESCE(r.decided_by, '')
		   FROM assistant_actions a
		   JOIN assistant_conversations c ON c.id = a.conversation_id
		   LEFT JOIN assistant_automation_runs r
		     ON r.id = c.automation_run_id
		  WHERE a.status = ?
		    AND `+rowSuggestionCondition+`
		    AND `+assistantSubject+` IN `+placeholders+`
		  ORDER BY a.created_at, a.id`, args...)
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
