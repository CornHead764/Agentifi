package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/auth"
	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/service"
	"github.com/CornHead764/agentifi/backend/internal/store"
)

// The assistant. Dormant until a provider is configured. The API key is sealed
// by the store and has no field on any response type; `has_key` is all a
// settings screen gets.
//
// Conversations belong to the person who started them, not to the space.

func init() {
	Register(Resource{Prefix: "/assistant", Routes: func(rt *Routes) {
		rt.Read(http.MethodGet, "/", assistantStatus)
		rt.Write(http.MethodPut, "/connection", saveAssistantConnection)
		rt.Write(http.MethodDelete, "/connection", deleteAssistantConnection)
		rt.Write(http.MethodPost, "/connection/test", testAssistantConnection)

		rt.Read(http.MethodGet, "/conversations", listConversations)
		rt.Write(http.MethodPost, "/conversations", startConversation)
		rt.Read(http.MethodGet, "/conversations/{conversation_id}", readConversation)
		rt.Write(http.MethodDelete, "/conversations/{conversation_id}", deleteConversation)
		rt.Write(http.MethodPost, "/conversations/{conversation_id}/ask", askAssistant)
	}})
}

// AssistantStatusResponse says whether the feature is usable, and what it can do.
type AssistantStatusResponse struct {
	// Configured is false when no provider is set up. The page shows the form
	// rather than an empty conversation nobody can send anything to.
	Configured bool   `json:"configured"`
	IsEnabled  bool   `json:"is_enabled"`
	BaseURL    string `json:"base_url"`
	Model      string `json:"model"`
	Name       string `json:"name"`
	// HasKey says a key is stored, never what it is.
	HasKey bool `json:"has_key"`
	// AllowWrites lets the assistant propose changes as well as answer
	// questions. On for a new connection.
	AllowWrites bool `json:"allow_writes"`
	// ApplyWithoutAsking issues changes immediately rather than as cards. Only
	// meaningful with AllowWrites on, and cleared whenever that goes off.
	ApplyWithoutAsking bool `json:"apply_without_asking"`
	// ToolCallStyle is "native" or "prompted": tool calls in the API's own
	// field, or written into the prompt and parsed back out of the answer for
	// a server with no tool-call parser.
	ToolCallStyle string `json:"tool_call_style"`
	// Tools is the whole list, each entry marked, whatever the setting says,
	// so a person sees what would be switched on.
	Tools []AssistantToolResponse `json:"tools"`
}

type AssistantToolResponse struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	// Writes marks a tool that proposes a change. It never makes one: a
	// proposal is a card somebody applies.
	Writes bool `json:"writes"`
}

type AssistantConnectionUpdate struct {
	Name    string `json:"name"`
	BaseURL string `json:"base_url"`
	Model   string `json:"model"`
	// APIKey is write-only. Empty leaves whatever is stored alone, so a form
	// can be saved without the key having been sent to the browser.
	APIKey             string `json:"api_key"`
	IsEnabled          *bool  `json:"is_enabled"`
	AllowWrites        *bool  `json:"allow_writes"`
	ApplyWithoutAsking *bool  `json:"apply_without_asking"`
	// ToolCallStyle is "native" or "prompted". Empty leaves it alone.
	ToolCallStyle string `json:"tool_call_style"`
}

// ConnectionTestResponse is what a probe of the configured model found. Two
// facts, because they have different fixes: unreachable is a URL or key, and
// "reachable but drops tool calls" is the server's tool-call parser, which
// prompted tool calls work around.
type ConnectionTestResponse struct {
	Reachable bool `json:"reachable"`
	// NativeToolCalls is whether the server returned a tool call in the API's
	// own field when the model was made to call one.
	NativeToolCalls bool `json:"native_tool_calls"`
	// PromptedToolCalls is whether the same model produced a readable call
	// when asked for it in the prompt instead.
	PromptedToolCalls bool   `json:"prompted_tool_calls"`
	Detail            string `json:"detail"`
	// Recommended is the tool-call style the two results suggest.
	Recommended string `json:"recommended"`
}

type ConversationResponse struct {
	ID        uuid.UUID         `json:"id"`
	Title     string            `json:"title"`
	CreatedAt time.Time         `json:"created_at"`
	UpdatedAt time.Time         `json:"updated_at"`
	Messages  []MessageResponse `json:"messages"`
	// Mail is the email the conversation was started about, or null. Its
	// headers only: the text is fetched for the model and not kept.
	Mail *ConversationMailResponse `json:"mail"`
	// Actions are the changes proposed in this thread, in order. Beside the
	// messages rather than inside them because a card outlives its turn.
	Actions []ActionResponse `json:"actions"`
}

// ConversationMailResponse names the email a conversation is about.
type ConversationMailResponse struct {
	ID           uuid.UUID `json:"id"`
	ConnectionID uuid.UUID `json:"connection_id"`
	Sender       string    `json:"sender"`
	Subject      string    `json:"subject"`
	ReceivedAt   time.Time `json:"received_at"`
}

// StartConversationRequest is optional. MailID starts the conversation about
// one message from the mail log.
type StartConversationRequest struct {
	MailID *uuid.UUID `json:"mail_id"`
}

type MessageResponse struct {
	ID   uuid.UUID `json:"id"`
	Role string    `json:"role"`
	// Content is the text for a user or assistant turn, and the tool's answer
	// for a tool turn.
	Content string `json:"content"`
	// ToolName is set on a tool turn.
	ToolName  string         `json:"tool_name"`
	Arguments map[string]any `json:"tool_arguments"`
	CreatedAt time.Time      `json:"created_at"`
}

type AskRequest struct {
	Question string `json:"question"`
}

type AskResponse struct {
	Answer string `json:"answer"`
	// ToolCalls names what was read to answer, in order.
	ToolCalls    []string             `json:"tool_calls"`
	Conversation ConversationResponse `json:"conversation"`
}

func assistantStatus(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	everything := domain.AssistantToolsFor(true)
	tools := make([]AssistantToolResponse, 0, len(everything))
	for _, one := range everything {
		tools = append(tools, AssistantToolResponse{
			Name: one.Name, Description: one.Description, Writes: one.Writes,
		})
	}
	out := AssistantStatusResponse{Tools: tools}

	connection, err := env.DB.GetAssistantConnection(r.Context(), sp.ID())
	if err != nil {
		if isNotFound(err) {
			return writeJSON(w, http.StatusOK, out)
		}
		return err
	}
	out.Configured = true
	out.IsEnabled = connection.IsEnabled
	out.BaseURL, out.Model, out.Name = connection.BaseURL, connection.Model, connection.Name
	out.HasKey, out.AllowWrites = connection.HasKey, connection.AllowWrites
	out.ApplyWithoutAsking = connection.ApplyWithoutAsking
	out.ToolCallStyle = connection.ToolCallStyle
	if out.ToolCallStyle == "" {
		out.ToolCallStyle = provider.ToolCallsNative
	}
	return writeJSON(w, http.StatusOK, out)
}

func saveAssistantConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	var body AssistantConnectionUpdate
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	base := strings.TrimSpace(body.BaseURL)
	model := strings.TrimSpace(body.Model)
	if base == "" || model == "" {
		return errBadRequest("The assistant needs a base URL and a model")
	}
	parsed, err := url.Parse(base)
	if err != nil ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") ||
		parsed.Host == "" || parsed.User != nil {
		return errBadRequest("The base URL has to start with http:// or https:// and name a host")
	}
	// The server issues POSTs to this host with a key attached. The allowlist
	// stops any member who can save a connection from pointing it at an
	// internal service; unset leaves LAN model hosts reachable.
	if hosts := env.Live().AssistantAllowedHosts; len(hosts) > 0 {
		allowed := false
		for _, host := range hosts {
			if strings.EqualFold(parsed.Hostname(), host) {
				allowed = true
				break
			}
		}
		if !allowed {
			return errBadRequest("The assistant is restricted to these hosts: %s",
				strings.Join(hosts, ", "))
		}
	}

	// A new connection may propose changes, each one a card a person accepts,
	// so category suggestions work from the first save; applying without
	// asking stays off.
	connection := store.AssistantConnection{
		Name: strings.TrimSpace(body.Name), BaseURL: base, Model: model, IsEnabled: true,
		AllowWrites: true, ToolCallStyle: provider.ToolCallsNative,
	}
	if existing, err := env.DB.GetAssistantConnection(r.Context(), sp.ID()); err == nil {
		connection.ID = existing.ID
		connection.IsEnabled = existing.IsEnabled
		connection.AllowWrites = existing.AllowWrites
		connection.ApplyWithoutAsking = existing.ApplyWithoutAsking
		if existing.ToolCallStyle != "" {
			connection.ToolCallStyle = existing.ToolCallStyle
		}
	} else if !isNotFound(err) {
		return err
	}
	switch style := strings.TrimSpace(body.ToolCallStyle); style {
	case "":
	case provider.ToolCallsNative, provider.ToolCallsPrompted:
		connection.ToolCallStyle = style
	default:
		return errBadRequest("Tool calls have to be %q or %q", provider.ToolCallsNative,
			provider.ToolCallsPrompted)
	}
	if body.IsEnabled != nil {
		connection.IsEnabled = *body.IsEnabled
	}
	// A saved form that omits it keeps the stored value, so editing the model
	// name never switches writes on.
	if body.AllowWrites != nil {
		connection.AllowWrites = *body.AllowWrites
	}
	if body.ApplyWithoutAsking != nil {
		connection.ApplyWithoutAsking = *body.ApplyWithoutAsking
	}
	// Switching changes off clears automatic application, so turning changes
	// back on later does not silently resume unattended edits.
	if !connection.AllowWrites {
		connection.ApplyWithoutAsking = false
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	if err := sealed.SaveAssistantConnection(
		r.Context(), sp.ID(), &connection, body.APIKey); err != nil {
		return err
	}
	return assistantStatus(env, w, r, sp)
}

// testAssistantConnection asks the configured model to call one tool, both
// ways, and reports what came back. The failure it detects is silent: the
// server answers 200 and the call is simply missing from the response.
func testAssistantConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	connection, err := env.DB.GetAssistantConnection(r.Context(), sp.ID())
	if err != nil {
		if isNotFound(err) {
			return errConflict("the assistant has no model configured")
		}
		return err
	}
	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	key, err := sealed.AssistantConnectionKey(r.Context(), sp.ID())
	if err != nil {
		return err
	}

	probe := domain.AssistantTool{
		Name:        "ping",
		Description: "Answers pong. Call this tool once, with no arguments, before answering.",
		Parameters:  map[string]any{"type": "object", "properties": map[string]any{}},
	}
	messages := []provider.ChatMessage{
		{Role: "system", Content: "You are being tested. Call the tool named ping exactly once. " +
			"Do not answer in words until you have called it."},
		{Role: "user", Content: "Ping."},
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()

	out := ConnectionTestResponse{Reachable: true}
	native := &provider.Assistant{
		BaseURL: connection.BaseURL, APIKey: key, Model: connection.Model,
		ToolCallStyle: provider.ToolCallsNative,
	}
	reply, err := native.Complete(ctx, messages, []domain.AssistantTool{probe})
	switch {
	case err != nil && strings.Contains(err.Error(), "not parsing tool calls"):
		out.Detail = "The server is not turning the model's tool calls into the API's tool_calls field."
	case err != nil:
		out.Reachable = false
		out.Detail = strings.TrimPrefix(err.Error(), "provider: ")
		out.Recommended = connection.ToolCallStyle
		return writeJSON(w, http.StatusOK, out)
	case len(reply.ToolCalls) > 0:
		out.NativeToolCalls = true
	default:
		out.Detail = "The model answered in words without calling the tool."
	}

	prompted := &provider.Assistant{
		BaseURL: connection.BaseURL, APIKey: key, Model: connection.Model,
		ToolCallStyle: provider.ToolCallsPrompted,
	}
	promptedDropped := false
	if reply, err := prompted.Complete(ctx, messages, []domain.AssistantTool{probe}); err == nil &&
		len(reply.ToolCalls) > 0 {
		out.PromptedToolCalls = true
	} else if err != nil && strings.Contains(err.Error(), "not parsing tool calls") {
		promptedDropped = true
	}

	switch {
	case out.NativeToolCalls:
		out.Recommended = provider.ToolCallsNative
		out.Detail = "The server returns tool calls natively."
	case out.PromptedToolCalls:
		out.Recommended = provider.ToolCallsPrompted
		out.Detail += " Tool calls written into the prompt do come back readable, so " +
			"\"in the prompt\" is the style that works here."
	case promptedDropped:
		// A server with no tool-call parser does this: even told about tools in
		// plain text, it answers through the call channel, which the server
		// discards. Only the server can fix that.
		out.Recommended = connection.ToolCallStyle
		out.Detail += " Even with the tools described in the prompt, this model sends its " +
			"call through the server's tool channel and the server drops it, so neither " +
			"style can work until the server is started with a tool-call parser (for vLLM: " +
			"--enable-auto-tool-choice and the --tool-call-parser that matches the model)."
	default:
		out.Recommended = connection.ToolCallStyle
		out.Detail += " Neither style produced a tool call; this model may not follow " +
			"tool instructions at all."
	}
	out.Detail = strings.TrimSpace(out.Detail)
	return writeJSON(w, http.StatusOK, out)
}

func deleteAssistantConnection(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	if err := sp.RequireOwner(); err != nil {
		return err
	}
	if err := env.DB.DeleteAssistantConnection(r.Context(), sp.ID()); err != nil {
		return err
	}
	return writeNoContent(w)
}

func listConversations(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	rows, err := env.DB.ListAssistantConversations(r.Context(), sp.ID(), sp.UserID())
	if err != nil {
		return err
	}
	out := make([]ConversationResponse, 0, len(rows))
	for _, one := range rows {
		out = append(out, ConversationResponse{
			ID: one.ID, Title: one.Title, CreatedAt: one.CreatedAt, UpdatedAt: one.UpdatedAt,
			Messages: []MessageResponse{}, Actions: []ActionResponse{},
		})
	}
	return writeJSON(w, http.StatusOK, out)
}

func startConversation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	var body StartConversationRequest
	if r.ContentLength != 0 {
		if err := decodeBody(r, &body); err != nil {
			return err
		}
	}
	conversation := store.AssistantConversation{UserID: sp.UserID()}
	var mail *ConversationMailResponse
	if body.MailID != nil {
		row, err := env.DB.GetBillEmail(r.Context(), sp.ID(), *body.MailID)
		if err != nil {
			if isNotFound(err) {
				return errInvalid("invalid", []string{"body", "mail_id"},
					"message %s is not in this space's mail log", *body.MailID)
			}
			return err
		}
		if row.Outcome == store.EmailOutcomeOTP {
			return errConflict("%s", service.ErrMailWithheld.Error())
		}
		conversation.MailID = row.ID
		mail = conversationMail(row)
	}
	if err := env.DB.CreateAssistantConversation(r.Context(), sp.ID(), &conversation); err != nil {
		return err
	}
	return writeJSON(w, http.StatusCreated, ConversationResponse{
		ID: conversation.ID, Title: conversation.Title,
		CreatedAt: conversation.CreatedAt, UpdatedAt: conversation.UpdatedAt,
		Mail: mail, Messages: []MessageResponse{}, Actions: []ActionResponse{},
	})
}

func conversationMail(row store.BillEmail) *ConversationMailResponse {
	return &ConversationMailResponse{
		ID: row.ID, ConnectionID: row.ConnectionID, Sender: row.Sender,
		Subject: row.Subject, ReceivedAt: row.ReceivedAt,
	}
}

func readConversation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	conversation, err := conversationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	view, err := conversationView(env, r, sp, conversation)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, view)
}

func deleteConversation(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	id, err := pathUUID(r, "conversation_id", "Conversation")
	if err != nil {
		return err
	}
	return deleted(w,
		env.DB.DeleteAssistantConversation(r.Context(), sp.ID(), sp.UserID(), id), "Conversation")
}

func askAssistant(env *Env, w http.ResponseWriter, r *http.Request, sp auth.SpaceContext) error {
	conversation, err := conversationFromPath(env, r, sp)
	if err != nil {
		return err
	}
	var body AskRequest
	if err := decodeBody(r, &body); err != nil {
		return err
	}
	question := strings.TrimSpace(body.Question)
	if question == "" {
		return errBadRequest("Ask something")
	}

	if _, err := env.DB.GetAssistantConnection(r.Context(), sp.ID()); err != nil {
		if isNotFound(err) {
			return errConflict("the assistant has no model configured")
		}
		return err
	}

	sealed, err := sealedStore(env)
	if err != nil {
		return err
	}
	assistant := service.NewAssistant(sealed, assistantTools{env: env, sp: sp})
	assistant.Now = env.now
	assistant.Mail = func(ctx context.Context, spaceID store.SpaceID, logID uuid.UUID) (billmail.Message, error) {
		fetched, err := NewMailbox(env).FetchLogged(ctx, spaceID, logID)
		return fetched.Message, err
	}
	result, err := assistant.Ask(r.Context(), sp.ID(), conversation.ID, question)
	if err != nil {
		// The provider's own words. "the model refused (401)" tells somebody
		// their key is wrong; "internal server error" tells them nothing.
		return errBadGateway("%s", err)
	}

	view, err := conversationView(env, r, sp, conversation)
	if err != nil {
		return err
	}
	return writeJSON(w, http.StatusOK, AskResponse{
		Answer: result.Answer, ToolCalls: result.ToolCalls, Conversation: view,
	})
}

// sealedStore is the store with the credential cipher attached, the same one
// that seals a SimpleFIN access URL. Built per request so a process started
// without a key fails where somebody can see it.
func sealedStore(env *Env) (*store.Store, error) {
	cipher, err := store.NewCipher(env.Cfg.CredentialKey())
	if err != nil {
		return nil, err
	}
	return env.DB.WithCipher(cipher), nil
}

func conversationView(
	env *Env, r *http.Request, sp auth.SpaceContext, conversation store.AssistantConversation,
) (ConversationResponse, error) {
	if err := releaseStaleActions(r.Context(), env, sp, conversation.ID); err != nil {
		return ConversationResponse{}, err
	}
	messages, err := env.DB.ListAssistantMessages(r.Context(), sp.ID(), conversation.ID)
	if err != nil {
		return ConversationResponse{}, err
	}
	actions, err := env.DB.ListAssistantActions(r.Context(), sp.ID(), conversation.ID)
	if err != nil {
		return ConversationResponse{}, err
	}

	out := ConversationResponse{
		ID: conversation.ID, Title: conversation.Title,
		CreatedAt: conversation.CreatedAt, UpdatedAt: conversation.UpdatedAt,
		Messages: make([]MessageResponse, 0, len(messages)),
		Actions:  make([]ActionResponse, 0, len(actions)),
	}
	if conversation.MailID != uuid.Nil {
		row, err := env.DB.GetBillEmail(r.Context(), sp.ID(), conversation.MailID)
		if err != nil && !isNotFound(err) {
			return ConversationResponse{}, err
		}
		if err == nil {
			out.Mail = conversationMail(row)
		}
	}
	for _, one := range messages {
		out.Messages = append(out.Messages, MessageResponse{
			ID: one.ID, Role: one.Role, Content: one.Content,
			ToolName: one.ToolName, Arguments: one.ToolArguments, CreatedAt: one.CreatedAt,
		})
	}
	for _, one := range actions {
		out.Actions = append(out.Actions, actionResponseFor(r.Context(), env, sp, one))
	}
	return out, nil
}

func conversationFromPath(
	env *Env, r *http.Request, sp auth.SpaceContext,
) (store.AssistantConversation, error) {
	id, err := pathUUID(r, "conversation_id", "Conversation")
	if err != nil {
		return store.AssistantConversation{}, err
	}
	conversation, err := env.DB.GetAssistantConversation(r.Context(), sp.ID(), sp.UserID(), id)
	if err != nil {
		return store.AssistantConversation{}, notFoundAs(err, "Conversation")
	}
	return conversation, nil
}
