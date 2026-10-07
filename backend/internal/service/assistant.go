package service

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/CornHead764/agentifi/backend/internal/billmail"
	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/provider"
	"github.com/CornHead764/agentifi/backend/internal/store"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// A question and an automation run share one bounded tool loop, converse, so
// what each may do is decided by the same code. Only tools in
// domain.AssistantTools can run; write tools go through ActionProposer.

// MaxToolRounds caps the model's turns, not its tool calls: several calls can
// share one round.
const MaxToolRounds = 10

// ToolRunner answers one tool call. It is built per request in the API layer
// and carries the request's space context.
type ToolRunner interface {
	Run(ctx context.Context, name string, arguments map[string]any) (any, error)
}

// ActionProposer records what a change tool would do and, when the household
// has asked for it, issues it. A ToolRunner that does not implement it can
// only read.
type ActionProposer interface {
	// Propose records the change and stops. Nothing reaches the ledger.
	Propose(
		ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
	) (any, error)
	// ApplyNow records the change and issues it, through the same path the
	// Apply button uses.
	ApplyNow(
		ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
	) (any, error)
	// Simulate records the change for a dry run, as a card with no Apply
	// button. Nothing reaches the ledger.
	Simulate(
		ctx context.Context, conversationID uuid.UUID, name string, arguments map[string]any,
	) (any, error)
}

type Assistant struct {
	base
	Tools ToolRunner
	// Mail fetches a logged email for a conversation started about one. Nil
	// answers that the email cannot be read.
	Mail func(ctx context.Context, spaceID store.SpaceID, logID uuid.UUID) (billmail.Message, error)
	// Now is nil for the real clock.
	Now func() time.Time
}

func NewAssistant(st *store.Store, tools ToolRunner) *Assistant {
	return &Assistant{base: newBase(st), Tools: tools}
}

func (a *Assistant) now() time.Time {
	if a.Now != nil {
		return a.Now()
	}
	return time.Now().UTC()
}

type AnswerResult struct {
	Answer string
	// ToolCalls names what was looked up, in order.
	ToolCalls []string
}

// writePolicy is what a change tool does when the model calls one, read from
// the connection for a question and from the automation for a run.
type writePolicy struct {
	// allowed is false for a read-only space or an observing automation.
	allowed bool
	// apply issues the change rather than leaving it as a card.
	apply bool
	// dryRun records the change as simulated. Outranks apply.
	dryRun bool
	// unattended refuses the tools that need a person present.
	unattended bool
}

func (a *Assistant) modelFor(
	ctx context.Context, spaceID store.SpaceID, connection store.AssistantConnection, model string,
) (*provider.Assistant, error) {
	key, err := a.store.AssistantConnectionKey(ctx, spaceID)
	if err != nil {
		return nil, err
	}
	if strings.TrimSpace(model) == "" {
		model = connection.Model
	}
	return &provider.Assistant{
		BaseURL: connection.BaseURL, APIKey: key, Model: model,
		ToolCallStyle: connection.ToolCallStyle,
	}, nil
}

// Ask puts a question to the model and records the whole exchange.
func (a *Assistant) Ask(
	ctx context.Context, spaceID store.SpaceID, conversationID uuid.UUID, question string,
) (AnswerResult, error) {
	connection, err := a.store.GetAssistantConnection(ctx, spaceID)
	if err != nil {
		return AnswerResult{}, err
	}
	if !connection.IsEnabled {
		return AnswerResult{}, fmt.Errorf("service: the assistant is switched off")
	}
	model, err := a.modelFor(ctx, spaceID, connection, "")
	if err != nil {
		return AnswerResult{}, err
	}

	// Recorded before the model is called, so a provider outage does not lose
	// the question.
	asked := store.AssistantMessage{
		ConversationID: conversationID, Role: domain.AssistantRoleUser, Content: question,
	}
	if err := a.store.AddAssistantMessage(ctx, spaceID, &asked); err != nil {
		return AnswerResult{}, err
	}
	if err := a.store.TouchAssistantConversation(
		ctx, spaceID, conversationID, titleFrom(question)); err != nil {
		return AnswerResult{}, err
	}

	history, err := a.store.ListAssistantMessages(ctx, spaceID, conversationID)
	if err != nil {
		return AnswerResult{}, err
	}
	space, err := a.store.GetSpace(ctx, spaceID)
	if err != nil {
		return AnswerResult{}, err
	}
	messages := []provider.ChatMessage{
		{Role: "system", Content: domain.AssistantPromptFor(
			connection.AllowWrites, connection.ApplyWithoutAsking) +
			domain.AssistantFacts(domain.DateOf(a.now()), space.PrimaryCurrency)},
	}
	if mailID, err := a.store.AssistantConversationMail(ctx, spaceID, conversationID); err != nil {
		return AnswerResult{}, err
	} else if mailID != uuid.Nil {
		messages = append(messages, a.attachedMail(ctx, spaceID, mailID))
	}
	messages = append(messages, foldHistory(history)...)

	// Write tools are hidden, not just refused, when changes are off, so the
	// model does not keep offering them.
	tools := domain.AssistantToolsFor(connection.AllowWrites)
	policy := writePolicy{allowed: connection.AllowWrites, apply: connection.ApplyWithoutAsking}

	return a.converse(ctx, spaceID, conversationID, model, messages, tools, policy, MaxToolRounds)
}

// foldHistory is a conversation's record as the model reads it. Tool rows are
// dropped: their results are already in the answer that followed. Action rows
// (what happened to proposal cards) are prefixed onto the next user message,
// because several open models' chat templates refuse a mid-conversation
// system message or two user turns in a row.
func foldHistory(history []store.AssistantMessage) []provider.ChatMessage {
	var (
		out      []provider.ChatMessage
		outcomes []string
	)
	for _, one := range history {
		switch one.Role {
		case domain.AssistantRoleTool:
			continue
		case domain.AssistantRoleAction:
			outcomes = append(outcomes, "- "+one.Content)
			continue
		case domain.AssistantRoleUser:
			if len(outcomes) > 0 {
				content := "[From Agentifi, not typed by the person: what happened to your " +
					"proposals since your last answer]\n" + strings.Join(outcomes, "\n") +
					"\n\n[The person's message]\n" + one.Content
				out = append(out, provider.ChatMessage{Role: one.Role, Content: content})
				outcomes = nil
				continue
			}
		}
		out = append(out, provider.ChatMessage{Role: one.Role, Content: one.Content})
	}
	return out
}

// attachedMail is the email a conversation is about, as a context message.
// It is fetched per question because mail bodies are never stored, and goes
// in as delimited, untrusted data.
func (a *Assistant) attachedMail(
	ctx context.Context, spaceID store.SpaceID, logID uuid.UUID,
) provider.ChatMessage {
	if a.Mail == nil {
		return provider.ChatMessage{Role: "system", Content: "The person started this " +
			"conversation about an email, and it cannot be read from here. Say so if they ask about it."}
	}
	message, err := a.Mail(ctx, spaceID, logID)
	if err != nil {
		return provider.ChatMessage{Role: "system", Content: "The person started this " +
			"conversation about an email, and it could not be fetched from the mailbox (" +
			err.Error() + "). Say so if they ask about it."}
	}
	return provider.ChatMessage{Role: "system", Content: "The person started this conversation " +
		"about the email below, to ask you about it. " + UntrustedMailRule + "\n\n" +
		MailAsData(message, MailTextLimit)}
}

// unrecordedResult is what the record keeps of an Unrecorded tool's result.
const unrecordedResult = `{"note":"read for this answer and not kept"}`

// converse runs the tool loop over a conversation whose opening is already
// recorded, and records the answer.
func (a *Assistant) converse(
	ctx context.Context, spaceID store.SpaceID, conversationID uuid.UUID,
	model *provider.Assistant, messages []provider.ChatMessage, tools []domain.AssistantTool,
	policy writePolicy, maxRounds int,
) (AnswerResult, error) {
	if maxRounds <= 0 {
		maxRounds = MaxToolRounds
	}
	var out AnswerResult
	for round := 0; round < maxRounds; round++ {
		reply, err := model.Complete(ctx, messages, tools)
		if err != nil {
			return out, err
		}
		if len(reply.ToolCalls) == 0 {
			answer := strings.TrimSpace(reply.Content)
			if answer == "" {
				answer = "I could not work that out from the ledger."
			}
			recorded := store.AssistantMessage{
				ConversationID: conversationID,
				Role:           domain.AssistantRoleAssistant, Content: answer,
			}
			if err := a.store.AddAssistantMessage(ctx, spaceID, &recorded); err != nil {
				return out, err
			}
			out.Answer = answer
			return out, nil
		}

		messages = append(messages, provider.ChatMessage{
			Role: "assistant", ToolCalls: reply.ToolCalls,
		})
		for _, call := range reply.ToolCalls {
			result, arguments := a.runTool(ctx, conversationID, call, policy)
			out.ToolCalls = append(out.ToolCalls, call.Function.Name)

			kept := result
			if tool, known := domain.AssistantToolByName(call.Function.Name); known &&
				tool.Unrecorded && !strings.HasPrefix(result, `{"error"`) {
				kept = unrecordedResult
			}
			recorded := store.AssistantMessage{
				ConversationID: conversationID, Role: domain.AssistantRoleTool,
				ToolName: call.Function.Name, ToolArguments: arguments, Content: kept,
			}
			if err := a.store.AddAssistantMessage(ctx, spaceID, &recorded); err != nil {
				return out, err
			}
			messages = append(messages, provider.ChatMessage{
				Role: "tool", ToolCallID: call.ID, Name: call.Function.Name, Content: result,
			})
		}
	}

	out.Answer = "I looked several things up and still could not answer that. Try asking " +
		"about a narrower date range."
	recorded := store.AssistantMessage{
		ConversationID: conversationID,
		Role:           domain.AssistantRoleAssistant, Content: out.Answer,
	}
	if err := a.store.AddAssistantMessage(ctx, spaceID, &recorded); err != nil {
		return out, err
	}
	return out, nil
}

// runTool answers one call, returning the result as JSON and the arguments it
// was given. Errors come back as JSON for the model to correct on the next
// round rather than failing the request.
func (a *Assistant) runTool(
	ctx context.Context, conversationID uuid.UUID, call provider.ToolCall, policy writePolicy,
) (string, map[string]any) {
	arguments := map[string]any{}
	if raw := strings.TrimSpace(call.Function.Arguments); raw != "" && raw != "null" {
		if err := json.Unmarshal([]byte(raw), &arguments); err != nil {
			return `{"error":"those arguments were not valid JSON"}`, nil
		}
	}
	tool, known := domain.AssistantToolByName(call.Function.Name)
	if !known {
		return fmt.Sprintf(`{"error":%q}`,
			fmt.Sprintf("there is no tool called %q", call.Function.Name)), arguments
	}
	if a.Tools == nil {
		return `{"error":"no tools are available"}`, arguments
	}

	// A model can name a tool it was never offered, so the policy is enforced
	// here and not only in the catalogue.
	if tool.Attended && policy.unattended {
		return fmt.Sprintf(`{"error":%q}`,
			fmt.Sprintf("%s is only available when a person is asking", tool.Name)), arguments
	}
	run := a.Tools.Run
	if tool.Writes {
		if !policy.allowed {
			return `{"error":"this household has not switched changes on for the assistant, ` +
				`so nothing can be changed from here. Say what you would change instead."}`, arguments
		}
		proposer, able := a.Tools.(ActionProposer)
		if !able {
			return `{"error":"changes cannot be proposed here"}`, arguments
		}
		act := proposer.Propose
		switch {
		case policy.dryRun:
			act = proposer.Simulate
		case policy.apply:
			act = proposer.ApplyNow
		}
		run = func(ctx context.Context, name string, arguments map[string]any) (any, error) {
			return act(ctx, conversationID, name, arguments)
		}
	}

	result, err := run(ctx, call.Function.Name, arguments)
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error()), arguments
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return `{"error":"that answer could not be encoded"}`, arguments
	}
	return string(encoded), arguments
}

// titleFrom names a conversation after its first question.
func titleFrom(question string) string {
	title := strings.TrimSpace(strings.ReplaceAll(question, "\n", " "))
	return textutil.ClipMarked(title, 80)
}
