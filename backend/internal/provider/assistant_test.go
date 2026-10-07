package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/CornHead764/agentifi/backend/internal/domain"
)

// A chat-completions server answering a canned body.
func modelAnswering(t *testing.T, body string) *Assistant {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &Assistant{BaseURL: server.URL, Model: "test-model", HTTPClient: server.Client()}
}

func TestAReasoningAnswerComesThroughWithItsContent(t *testing.T) {
	model := modelAnswering(t, `{"choices":[{"message":{"content":"Hello",
		"reasoning":"The user wants a greeting."},"finish_reason":"stop"}]}`)
	reply, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "Hello", reply.Content)
}

func TestADroppedToolCallIsAnErrorNamingTheServer(t *testing.T) {
	// What a vLLM server without a tool-call parser sends: the call is in the
	// reasoning, dropped from the message, and the content is null.
	model := modelAnswering(t, `{"choices":[{"message":{"content":null,
		"reasoning":"We must call function list_accounts."},"finish_reason":"stop"}]}`)
	_, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "balance?"}}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not parsing tool calls")
}

func TestAToolCallFinishWithNoCallsIsTheSameError(t *testing.T) {
	model := modelAnswering(t, `{"choices":[{"message":{"content":""},
		"finish_reason":"tool_calls"}]}`)
	_, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "balance?"}}, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "not parsing tool calls")
}

func TestAPlainEmptyAnswerIsStillAnAnswer(t *testing.T) {
	// No reasoning, no tool-call finish: nothing marks a dropped call.
	model := modelAnswering(t, `{"choices":[{"message":{"content":""},"finish_reason":"stop"}]}`)
	reply, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "hi"}}, nil)
	require.NoError(t, err)
	require.Equal(t, "", reply.Content)
}

func TestAParsedToolCallComesThrough(t *testing.T) {
	model := modelAnswering(t, `{"choices":[{"message":{"content":"","tool_calls":[
		{"id":"c1","type":"function","function":{"name":"list_accounts","arguments":"{}"}}]},
		"finish_reason":"tool_calls"}]}`)
	reply, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "balance?"}}, nil)
	require.NoError(t, err)
	require.Len(t, reply.ToolCalls, 1)
	require.Equal(t, "list_accounts", reply.ToolCalls[0].Function.Name)
}

func promptedModel(t *testing.T, body string, seen *map[string]any) *Assistant {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if seen != nil {
			_ = json.NewDecoder(r.Body).Decode(seen)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(server.Close)
	return &Assistant{
		BaseURL: server.URL, Model: "test-model", HTTPClient: server.Client(),
		ToolCallStyle: ToolCallsPrompted,
	}
}

var pingTool = []domain.AssistantTool{{
	Name: "ping", Description: "Answers pong.",
	Parameters: map[string]any{"type": "object", "properties": map[string]any{}},
}}

func TestPromptedStyleSendsTheCatalogueInThePromptAndNoToolsField(t *testing.T) {
	seen := map[string]any{}
	model := promptedModel(t, `{"choices":[{"message":{"content":"pong"}}]}`, &seen)
	_, err := model.Complete(t.Context(), []ChatMessage{
		{Role: "system", Content: "Rules."}, {Role: "user", Content: "hi"},
	}, pingTool)
	require.NoError(t, err)
	require.NotContains(t, seen, "tools", "the tools field was sent to a server that drops it")
	messages := seen["messages"].([]any)
	system := messages[0].(map[string]any)["content"].(string)
	require.Contains(t, system, "Rules.")
	require.Contains(t, system, `"tool_call"`)
	require.Contains(t, system, "- ping: Answers pong.")
}

func TestAPromptedCallIsReadBackOutOfTheAnswer(t *testing.T) {
	for _, content := range []string{
		`{"tool_call": {"name": "ping", "arguments": {}}}`,
		"```json\n{\"tool_call\": {\"name\": \"ping\", \"arguments\": {\"a\": \"b}\"}}}\n```",
		`I will check. {"tool_call":{"name":"ping","arguments":{"n":1}}}`,
		`{"tool_calls": [{"name": "ping", "arguments": {}}]}`,
	} {
		body, _ := json.Marshal(map[string]any{
			"choices": []map[string]any{{"message": map[string]any{"content": content}}},
		})
		model := promptedModel(t, string(body), nil)
		reply, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "hi"}}, pingTool)
		require.NoError(t, err, content)
		require.Len(t, reply.ToolCalls, 1, content)
		require.Equal(t, "ping", reply.ToolCalls[0].Function.Name)
		require.True(t, json.Valid([]byte(reply.ToolCalls[0].Function.Arguments)), content)
	}
}

func TestAPromptedAnswerWithNoCallIsAnAnswer(t *testing.T) {
	// An object that is not a call — a figure the model chose to format as
	// JSON — stays an answer rather than being mistaken for one.
	model := promptedModel(t, `{"choices":[{"message":{"content":"You spent {\"groceries\": 412}."}}]}`, nil)
	reply, err := model.Complete(t.Context(), []ChatMessage{{Role: "user", Content: "hi"}}, pingTool)
	require.NoError(t, err)
	require.Empty(t, reply.ToolCalls)
	require.Contains(t, reply.Content, "412")
}

func TestPromptedToolResultsBecomePlainTurns(t *testing.T) {
	seen := map[string]any{}
	model := promptedModel(t, `{"choices":[{"message":{"content":"pong"}}]}`, &seen)
	call := ToolCall{ID: "c1", Type: "function"}
	call.Function.Name, call.Function.Arguments = "ping", "{}"
	_, err := model.Complete(t.Context(), []ChatMessage{
		{Role: "system", Content: "Rules."},
		{Role: "user", Content: "hi"},
		{Role: "assistant", ToolCalls: []ToolCall{call}},
		{Role: "tool", ToolCallID: "c1", Name: "ping", Content: `{"pong":true}`},
	}, pingTool)
	require.NoError(t, err)
	messages := seen["messages"].([]any)
	require.Len(t, messages, 4)
	for _, one := range messages {
		turn := one.(map[string]any)
		require.NotEqual(t, "tool", turn["role"], "a tool turn reached a server with no tool API")
		require.NotContains(t, turn, "tool_calls")
	}
	require.Contains(t, messages[3].(map[string]any)["content"], "Result of ping")
}
