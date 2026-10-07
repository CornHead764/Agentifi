package provider

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/CornHead764/agentifi/backend/internal/domain"
	"github.com/CornHead764/agentifi/backend/internal/httpx"
	"github.com/CornHead764/agentifi/backend/internal/textutil"
)

// OpenAI chat-completions against any base URL, unstreamed: the tool loop needs
// a whole message before it can decide whether to call anything.

// Native uses the API's `tools` field. Prompted is for a server with no
// tool-call parser: the catalogue goes into the system prompt and the call is
// read back out of the answer's JSON.
const (
	ToolCallsNative   = "native"
	ToolCallsPrompted = "prompted"
)

type Assistant struct {
	BaseURL string
	APIKey  string
	Model   string
	// ToolCallStyle is ToolCallsNative or ToolCallsPrompted; blank is native.
	ToolCallStyle string
	HTTPClient    *http.Client
}

type ChatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
	// ToolCallID and Name are set on a tool result.
	ToolCallID string `json:"tool_call_id,omitempty"`
	Name       string `json:"name,omitempty"`
	// ToolCalls is set on an assistant turn that asked for one.
	ToolCalls []ToolCall `json:"tool_calls,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name string `json:"name"`
		// Arguments is a JSON string, as the API sends it.
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type Reply struct {
	Content   string
	ToolCalls []ToolCall
}

func (a *Assistant) client() *http.Client {
	if a.HTTPClient != nil {
		return a.HTTPClient
	}
	return &http.Client{Timeout: 90 * time.Second}
}

func (a *Assistant) prompted() bool { return a.ToolCallStyle == ToolCallsPrompted }

func (a *Assistant) Complete(
	ctx context.Context, messages []ChatMessage, tools []domain.AssistantTool,
) (Reply, error) {
	if strings.TrimSpace(a.BaseURL) == "" || strings.TrimSpace(a.Model) == "" {
		return Reply{}, fmt.Errorf("provider: the assistant has no model configured")
	}

	body := map[string]any{"model": a.Model}
	switch {
	case len(tools) > 0 && a.prompted():
		body["messages"] = promptedMessages(messages, tools)
	case len(tools) > 0:
		body["messages"] = messages
		encoded := make([]map[string]any, 0, len(tools))
		for _, one := range tools {
			encoded = append(encoded, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        one.Name,
					"description": one.Description,
					"parameters":  one.Parameters,
				},
			})
		}
		body["tools"] = encoded
	default:
		body["messages"] = messages
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return Reply{}, fmt.Errorf("provider: encoding the request: %w", err)
	}

	endpoint := strings.TrimSuffix(a.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint,
		bytes.NewReader(payload))
	if err != nil {
		return Reply{}, fmt.Errorf("provider: %s is not a usable endpoint: %w", endpoint, err)
	}
	request.Header.Set("Content-Type", "application/json")
	if a.APIKey != "" {
		request.Header.Set("Authorization", "Bearer "+a.APIKey)
	}

	response, err := httpx.Read(a.client(), request, 4<<20)
	if err != nil {
		return Reply{}, fmt.Errorf("provider: cannot reach the model at %s: %w", a.BaseURL, err)
	}
	if response.Status >= 400 {
		return Reply{}, fmt.Errorf("provider: the model refused (%d): %s",
			response.Status, firstLine(string(response.Body)))
	}

	var decoded struct {
		Choices []struct {
			Message struct {
				Content   string     `json:"content"`
				ToolCalls []ToolCall `json:"tool_calls"`
				// Reasoning is never shown; beside an empty answer it marks a
				// dropped tool call.
				Reasoning string `json:"reasoning"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(response.Body, &decoded); err != nil {
		return Reply{}, fmt.Errorf("provider: the model's answer was not chat-completions JSON: %w", err)
	}
	if len(decoded.Choices) == 0 {
		return Reply{}, fmt.Errorf("provider: the model returned no answer")
	}
	choice := decoded.Choices[0]

	// A server with no tool-call parser answers a model's call as an empty
	// message, flagged finish_reason "tool_calls" or carrying only reasoning.
	if strings.TrimSpace(choice.Message.Content) == "" && len(choice.Message.ToolCalls) == 0 &&
		(choice.FinishReason == "tool_calls" || strings.TrimSpace(choice.Message.Reasoning) != "") {
		return Reply{}, fmt.Errorf(
			"provider: the model prepared a tool call its server did not emit — " +
				"the endpoint is not parsing tool calls (a vLLM server needs " +
				"--enable-auto-tool-choice and --tool-call-parser for this model). " +
				"Switching the connection's tool calls to \"in the prompt\" works around it")
	}

	if len(tools) > 0 && a.prompted() {
		if calls, ok := parsePromptedCalls(choice.Message.Content); ok {
			return Reply{ToolCalls: calls}, nil
		}
	}

	return Reply{
		Content:   choice.Message.Content,
		ToolCalls: choice.Message.ToolCalls,
	}, nil
}

// promptedToolInstructions repeats "the object alone" because small models
// need telling twice.
const promptedToolInstructions = `

## Tools

You have tools. To use one, reply with ONLY this JSON object and nothing else —
no prose before or after it:

{"tool_call": {"name": "<tool name>", "arguments": {<the arguments>}}}

The next message will be that tool's result. You may then call another tool the
same way, or answer. When you answer, write plain text with no JSON object in it.
Never invent a tool result; wait for it. The tools are:
`

// promptedMessages puts the catalogue in the system prompt and turns every tool
// turn into a plain one.
func promptedMessages(messages []ChatMessage, tools []domain.AssistantTool) []ChatMessage {
	catalogue := &strings.Builder{}
	catalogue.WriteString(promptedToolInstructions)
	for _, one := range tools {
		schema, _ := json.Marshal(one.Parameters)
		fmt.Fprintf(catalogue, "\n- %s: %s\n  arguments schema: %s\n",
			one.Name, one.Description, schema)
	}

	out := make([]ChatMessage, 0, len(messages)+1)
	system := false
	for _, one := range messages {
		switch {
		case one.Role == "system" && !system:
			system = true
			out = append(out, ChatMessage{Role: "system", Content: one.Content + catalogue.String()})
		case one.Role == "assistant" && len(one.ToolCalls) > 0:
			for _, call := range one.ToolCalls {
				out = append(out, ChatMessage{Role: "assistant", Content: promptedCallText(call)})
			}
		case one.Role == "tool":
			out = append(out, ChatMessage{
				Role:    "user",
				Content: fmt.Sprintf("Result of %s:\n%s", one.Name, one.Content),
			})
		default:
			out = append(out, ChatMessage{Role: one.Role, Content: one.Content})
		}
	}
	if !system {
		out = append([]ChatMessage{{Role: "system", Content: catalogue.String()}}, out...)
	}
	return out
}

func promptedCallText(call ToolCall) string {
	arguments := json.RawMessage(call.Function.Arguments)
	if !json.Valid(arguments) {
		arguments = json.RawMessage(`{}`)
	}
	encoded, _ := json.Marshal(map[string]any{
		"tool_call": map[string]any{"name": call.Function.Name, "arguments": arguments},
	})
	return string(encoded)
}

// parsePromptedCalls takes the first balanced object that decodes to the call
// shape, wherever it sits (fenced, or after a sentence); otherwise the content
// is an answer.
func parsePromptedCalls(content string) ([]ToolCall, bool) {
	text := strings.TrimSpace(content)
	for start := strings.IndexByte(text, '{'); start >= 0; {
		end := balancedObjectEnd(text, start)
		if end < 0 {
			return nil, false
		}
		if calls, ok := decodePromptedCall(text[start : end+1]); ok {
			return calls, true
		}
		next := strings.IndexByte(text[start+1:], '{')
		if next < 0 {
			return nil, false
		}
		start += 1 + next
	}
	return nil, false
}

// balancedObjectEnd skips strings so a brace inside an argument does not close
// the call early.
func balancedObjectEnd(text string, start int) int {
	depth, inString, escaped := 0, false, false
	for i := start; i < len(text); i++ {
		c := text[i]
		switch {
		case escaped:
			escaped = false
		case inString && c == '\\':
			escaped = true
		case c == '"':
			inString = !inString
		case inString:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
}

func decodePromptedCall(object string) ([]ToolCall, bool) {
	var shape struct {
		Call *struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_call"`
		Calls []struct {
			Name      string          `json:"name"`
			Arguments json.RawMessage `json:"arguments"`
		} `json:"tool_calls"`
	}
	if err := json.Unmarshal([]byte(object), &shape); err != nil {
		return nil, false
	}
	if shape.Call != nil {
		shape.Calls = append(shape.Calls, *shape.Call)
	}
	out := make([]ToolCall, 0, len(shape.Calls))
	for i, one := range shape.Calls {
		if strings.TrimSpace(one.Name) == "" {
			continue
		}
		call := ToolCall{ID: fmt.Sprintf("prompted-%d", i+1), Type: "function"}
		call.Function.Name = one.Name
		call.Function.Arguments = "{}"
		if len(one.Arguments) > 0 && json.Valid(one.Arguments) {
			call.Function.Arguments = string(one.Arguments)
		}
		out = append(out, call)
	}
	return out, len(out) > 0
}

func firstLine(text string) string {
	return textutil.ClipMarked(textutil.FirstLine(text), httpx.ExcerptRunes)
}
