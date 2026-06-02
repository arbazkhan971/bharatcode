package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

func TestAnthropicStreamsTextToolAndUsage(t *testing.T) {
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/messages", r.URL.Path)
		require.Equal(t, "test-key", r.Header.Get("x-api-key"))
		require.Equal(t, anthropicVersion, r.Header.Get("anthropic-version"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))

		w.Header().Set("Content-Type", "text/event-stream")
		// Usage input + cache tokens arrive in message_start.
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":12,\"output_tokens\":1,\"cache_read_input_tokens\":4,\"cache_creation_input_tokens\":2}}}\n\n")
		// Text content block.
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hello \"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"there\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Tool-use content block; input arrives as partial_json deltas.
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"tool_use\",\"id\":\"toolu_1\",\"name\":\"lookup\",\"input\":{}}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"q\\\":\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"bharat\\\"}\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		// output_tokens finalized in message_delta.
		fmt.Fprint(w, "event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"tool_use\"},\"usage\":{\"output_tokens\":9}}\n\n")
		fmt.Fprint(w, "event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_TEST_KEY", "test-key")

	cfg := testConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{
			{
				Role:    message.RoleUser,
				Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
			},
		},
		Tools: []Tool{{
			Name:        "lookup",
			Description: "Looks up data.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		SystemPrompt: "You are concise.",
		MaxTokens:    256,
	})
	require.NoError(t, err)

	got := collectEvents(events)

	// Assert the ordered text deltas: hello then there.
	textDeltas := textDeltaSequence(got)
	require.Equal(t, []string{"hello ", "there"}, textDeltas)

	// Assert the tool call: start, accumulated input, and the assembled end event.
	require.Contains(t, got, ToolUseStartEvent{ID: "toolu_1", Name: "lookup"})
	require.Contains(t, got, ToolUseDeltaEvent{ID: "toolu_1", Delta: "{\"q\":"})
	require.Contains(t, got, ToolUseDeltaEvent{ID: "toolu_1", Delta: "\"bharat\"}"})
	require.Contains(t, got, ToolUseEndEvent{ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{"q":"bharat"}`)})

	// Assert exactly one EndEvent carrying merged usage (input+cache from
	// message_start, output from message_delta).
	require.Equal(t, 1, countEndEvents(got))
	require.Contains(t, got, EndEvent{Usage: Usage{
		InputTokens:      12,
		OutputTokens:     9,
		CacheReadTokens:  4,
		CacheWriteTokens: 2,
	}})

	// Ordering: StartEvent first, EndEvent last.
	require.IsType(t, StartEvent{}, got[0])
	require.IsType(t, EndEvent{}, got[len(got)-1])

	// The system prompt is carried as the top-level system field, not a message.
	require.Equal(t, "You are concise.", captured.System)
	require.True(t, captured.Stream)
	require.Equal(t, 256, captured.MaxTokens)
	require.Len(t, captured.Tools, 1)
	require.Equal(t, "lookup", captured.Tools[0].Name)
	require.Len(t, captured.Messages, 1)
	require.Equal(t, "user", captured.Messages[0].Role)
}

func TestAnthropicConvertsToolResultHistory(t *testing.T) {
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":3,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()
	t.Setenv("ANTHROPIC_TEST_KEY", "test-key")

	cfg := testConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{
			{
				Role: message.RoleAssistant,
				Content: []message.ContentBlock{
					message.ToolUseBlock{ID: "toolu_1", Name: "lookup", Input: json.RawMessage(`{"q":"x"}`)},
				},
			},
			{
				Role: message.RoleUser,
				Content: []message.ContentBlock{
					message.ToolResultBlock{ToolUseID: "toolu_1", Content: "answer"},
				},
			},
		},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.Len(t, captured.Messages, 2)
	require.Equal(t, "assistant", captured.Messages[0].Role)
	require.Len(t, captured.Messages[0].Content, 1)
	require.Equal(t, "tool_use", captured.Messages[0].Content[0].Type)
	require.Equal(t, "toolu_1", captured.Messages[0].Content[0].ID)
	require.JSONEq(t, `{"q":"x"}`, string(captured.Messages[0].Content[0].Input))

	// tool_result lives in a user-role message.
	require.Equal(t, "user", captured.Messages[1].Role)
	require.Len(t, captured.Messages[1].Content, 1)
	require.Equal(t, "tool_result", captured.Messages[1].Content[0].Type)
	require.Equal(t, "toolu_1", captured.Messages[1].Content[0].ToolUseID)
	require.Equal(t, "answer", captured.Messages[1].Content[0].Content)
}

func textDeltaSequence(events []Event) []string {
	var out []string
	for _, ev := range events {
		if d, ok := ev.(DeltaTextEvent); ok {
			out = append(out, d.Text)
		}
	}
	return out
}

func countEndEvents(events []Event) int {
	n := 0
	for _, ev := range events {
		if _, ok := ev.(EndEvent); ok {
			n++
		}
	}
	return n
}
