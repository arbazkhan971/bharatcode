package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
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

	// The system prompt is carried as the top-level system field, not a message,
	// as a structured text-block array carrying the prompt text.
	require.Len(t, captured.System, 1)
	require.Equal(t, "text", captured.System[0].Type)
	require.Equal(t, "You are concise.", captured.System[0].Text)
	require.True(t, captured.Stream)
	require.Equal(t, 256, captured.MaxTokens)
	require.Len(t, captured.Tools, 1)
	require.Equal(t, "lookup", captured.Tools[0].Name)
	require.Len(t, captured.Messages, 1)
	require.Equal(t, "user", captured.Messages[0].Role)
}

// TestAnthropicSends1MContextBeta verifies the provider opts into the 1M-token
// context window via the anthropic-beta header when a 1M-capable Sonnet 4 model
// is configured with a context_window above the standard 200k, and omits the
// header for a model left at the standard window.
func TestAnthropicSends1MContextBeta(t *testing.T) {
	cases := []struct {
		name          string
		contextWindow int
		wantBeta      string
	}{
		{name: "opted into 1M window", contextWindow: 1_000_000, wantBeta: anthropic1MContextBeta},
		{name: "standard window omits beta", contextWindow: 200_000, wantBeta: ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var gotBeta string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotBeta = r.Header.Get("anthropic-beta")
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
			cfg.Providers[0].Models = []string{"claude-sonnet-4-5"}
			cfg.Models = []config.Model{{
				ID:            "claude-sonnet-4-5",
				Provider:      "anthropic",
				ContextWindow: tc.contextWindow,
				SupportsTools: true,
			}}
			reg, err := NewRegistry(cfg)
			require.NoError(t, err)
			provider, err := reg.Get("anthropic")
			require.NoError(t, err)

			events, err := provider.Stream(context.Background(), Request{
				Model: "claude-sonnet-4-5",
				Messages: []message.Message{{
					Role:    message.RoleUser,
					Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
				}},
			})
			require.NoError(t, err)
			collectEvents(events)

			require.Equal(t, tc.wantBeta, gotBeta)
		})
	}
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

// writeMinimalAnthropicSSE writes a minimal well-formed SSE stream carrying the
// supplied usage block so tests can drive a request to completion without
// reproducing the full event sequence.
func writeMinimalAnthropicSSE(w http.ResponseWriter, usageJSON string) {
	w.Header().Set("Content-Type", "text/event-stream")
	fmt.Fprint(w, "event: message_start\n"+
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":"+usageJSON+"}}\n\n")
	fmt.Fprint(w, "event: message_stop\n"+
		"data: {\"type\":\"message_stop\"}\n\n")
}

// newAnthropicTestProvider wires a registry-backed Anthropic provider that
// points at server and authenticates with a stub key. It mirrors the setup the
// other tests in this file use.
func newAnthropicTestProvider(t *testing.T, serverURL string) Provider {
	t.Helper()
	t.Setenv("ANTHROPIC_TEST_KEY", "test-key")
	cfg := testConfig("anthropic", config.ProviderAnthropic, serverURL+"/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)
	return provider
}

// TestAnthropicOverloadedStreamErrorIsRetryable proves a mid-stream
// overloaded_error event is classified as a transient ErrServer fault. On the
// old code the error event was surfaced with no sentinel, so the failover and
// backoff layers (which retry only on ErrServer/ErrRateLimit) would give up on
// a recoverable capacity loss. The stream opens with an ordinary text delta and
// only then fails, exercising the genuine mid-stream path.
func TestAnthropicOverloadedStreamErrorIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"partial\"}}\n\n")
		fmt.Fprint(w, "event: error\n"+
			"data: {\"type\":\"error\",\"error\":{\"type\":\"overloaded_error\",\"message\":\"Overloaded\"}}\n\n")
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)

	got := collectEvents(events)
	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "an overloaded_error stream event must emit a terminal ErrorEvent")
	require.ErrorIs(t, errEv.Err, ErrServer, "overloaded_error must be classified as a retryable ErrServer")
	require.Contains(t, errEv.Err.Error(), "Overloaded", "the provider message must be preserved")
}

// TestAnthropicRateLimitStreamErrorIsRetryable proves a mid-stream
// rate_limit_error event is classified as ErrRateLimit so the failover layer
// retries it rather than failing hard.
func TestAnthropicRateLimitStreamErrorIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":5,\"output_tokens\":0}}}\n\n")
		fmt.Fprint(w, "event: error\n"+
			"data: {\"type\":\"error\",\"error\":{\"type\":\"rate_limit_error\",\"message\":\"Rate limited\"}}\n\n")
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)

	got := collectEvents(events)
	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "a rate_limit_error stream event must emit a terminal ErrorEvent")
	require.ErrorIs(t, errEv.Err, ErrRateLimit, "rate_limit_error must be classified as a retryable ErrRateLimit")
}

// TestAnthropicInvalidRequestStreamErrorIsNotRetryable proves a terminal
// invalid_request_error is NOT tagged with a retryable sentinel, so failover
// does not loop on a request the provider will always reject.
func TestAnthropicInvalidRequestStreamErrorIsNotRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: error\n"+
			"data: {\"type\":\"error\",\"error\":{\"type\":\"invalid_request_error\",\"message\":\"bad input\"}}\n\n")
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)

	got := collectEvents(events)
	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "an invalid_request_error stream event must emit a terminal ErrorEvent")
	require.False(t, errors.Is(errEv.Err, ErrServer), "invalid_request_error must not be retryable as ErrServer")
	require.False(t, errors.Is(errEv.Err, ErrRateLimit), "invalid_request_error must not be retryable as ErrRateLimit")
}

// TestAnthropicMarksSystemAndToolsWithCacheControl asserts, against the raw
// wire bytes, that the request carries cache_control ephemeral on the system
// block and on the (last) tool when a system prompt and tools are present.
func TestAnthropicMarksSystemAndToolsWithCacheControl(t *testing.T) {
	var rawBody []byte
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		require.NoError(t, json.Unmarshal(b, &captured))
		writeMinimalAnthropicSSE(w, `{"input_tokens":5,"output_tokens":1}`)
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Tools: []Tool{
			{Name: "first", Description: "First tool.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
			{Name: "second", Description: "Second tool.", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)},
		},
		SystemPrompt: "You are concise.",
		MaxTokens:    128,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	// Raw-byte assertions: the documented ephemeral marker is literally on the
	// wire, not merely reconstructable from a symmetric round-trip.
	body := string(rawBody)
	require.Contains(t, body, `"cache_control":{"type":"ephemeral"}`)

	// Three cache breakpoints: one for the system prefix, one for the tools
	// prefix, and one rolling breakpoint on the conversation history (the last
	// block of the final message). Anthropic permits at most four.
	require.Equal(t, 3, strings.Count(body, `"cache_control"`))

	// Structural assertions: the marker sits on the system block.
	require.Len(t, captured.System, 1)
	require.Equal(t, "text", captured.System[0].Type)
	require.Equal(t, "You are concise.", captured.System[0].Text)
	require.NotNil(t, captured.System[0].CacheControl)
	require.Equal(t, "ephemeral", captured.System[0].CacheControl.Type)

	// The marker sits on the LAST tool only; the earlier tool carries none.
	require.Len(t, captured.Tools, 2)
	require.Nil(t, captured.Tools[0].CacheControl)
	require.NotNil(t, captured.Tools[1].CacheControl)
	require.Equal(t, "ephemeral", captured.Tools[1].CacheControl.Type)

	// The rolling history breakpoint sits on the last block of the final message.
	require.Len(t, captured.Messages, 1)
	last := captured.Messages[0].Content
	require.NotEmpty(t, last)
	require.NotNil(t, last[len(last)-1].CacheControl)
	require.Equal(t, "ephemeral", last[len(last)-1].CacheControl.Type)
}

// TestAnthropicNoSystemPromptOmitsSystemAndCacheControl asserts that with no
// system prompt the request omits the system field entirely and emits no
// system cache_control, and that with no tools the only breakpoint is the
// rolling conversation-history one on the final message.
func TestAnthropicNoSystemPromptOmitsSystemAndCacheControl(t *testing.T) {
	var rawBody []byte
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		require.NoError(t, json.Unmarshal(b, &captured))
		writeMinimalAnthropicSSE(w, `{"input_tokens":3,"output_tokens":1}`)
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		// No tools, no system prompt.
		MaxTokens: 64,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	body := string(rawBody)
	// The system key must be absent entirely (omitempty), not an empty block.
	require.NotContains(t, body, `"system"`)
	// With no system and no tools the only breakpoint is the rolling history one.
	require.Equal(t, 1, strings.Count(body, `"cache_control"`))
	require.Empty(t, captured.System)
	require.Empty(t, captured.Tools)
	require.Len(t, captured.Messages, 1)
	last := captured.Messages[0].Content
	require.NotEmpty(t, last)
	require.NotNil(t, last[len(last)-1].CacheControl)
}

// TestAnthropicMarksToolsWhenSystemAbsent asserts that tools still receive a
// cache_control marker when there is no system prompt, so the tools prefix is
// cached independently of the system prefix.
func TestAnthropicMarksToolsWhenSystemAbsent(t *testing.T) {
	var rawBody []byte
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		require.NoError(t, json.Unmarshal(b, &captured))
		writeMinimalAnthropicSSE(w, `{"input_tokens":5,"output_tokens":1}`)
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Tools: []Tool{{Name: "only", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		// No system prompt.
		MaxTokens: 64,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	body := string(rawBody)
	require.NotContains(t, body, `"system"`)
	// Two breakpoints: the tools prefix and the rolling conversation-history one;
	// there is no system block to mark.
	require.Equal(t, 2, strings.Count(body, `"cache_control"`))
	require.Empty(t, captured.System)
	require.Len(t, captured.Tools, 1)
	require.NotNil(t, captured.Tools[0].CacheControl)
	require.Equal(t, "ephemeral", captured.Tools[0].CacheControl.Type)
}

// TestAnthropicMarksOnlyFinalMessageForHistoryCache asserts that across a
// multi-turn conversation the rolling history breakpoint lands solely on the
// last block of the final message, leaving earlier turns unmarked. A single
// moving breakpoint lets the next turn read the whole prior conversation from
// cache while staying within Anthropic's four-breakpoint budget.
func TestAnthropicMarksOnlyFinalMessageForHistoryCache(t *testing.T) {
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		require.NoError(t, json.Unmarshal(b, &captured))
		writeMinimalAnthropicSSE(w, `{"input_tokens":9,"output_tokens":1}`)
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{
			{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "first"}}},
			{Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: "reply"}}},
			{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "second"}}},
		},
		// No system prompt or tools, so the only breakpoint is the history one.
		MaxTokens: 64,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.Len(t, captured.Messages, 3)
	// Earlier turns carry no breakpoint.
	for i, msg := range captured.Messages[:2] {
		for j, block := range msg.Content {
			require.Nil(t, block.CacheControl, "message %d block %d must be unmarked", i, j)
		}
	}
	// Only the last block of the final message is the rolling breakpoint.
	last := captured.Messages[2].Content
	require.NotEmpty(t, last)
	require.NotNil(t, last[len(last)-1].CacheControl)
	require.Equal(t, "ephemeral", last[len(last)-1].CacheControl.Type)
}

// TestAnthropicPopulatesCacheUsageFromResponse asserts the usage mapping carries
// cache_read_input_tokens (and cache_creation_input_tokens) from the provider
// response into the EndEvent usage fields.
func TestAnthropicPopulatesCacheUsageFromResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		writeMinimalAnthropicSSE(w, `{"input_tokens":20,"output_tokens":7,"cache_read_input_tokens":15,"cache_creation_input_tokens":3}`)
	}))
	defer server.Close()

	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		SystemPrompt: "You are concise.",
		MaxTokens:    64,
	})
	require.NoError(t, err)
	got := collectEvents(events)

	require.Equal(t, 1, countEndEvents(got))
	require.Contains(t, got, EndEvent{Usage: Usage{
		InputTokens:      20,
		OutputTokens:     7,
		CacheReadTokens:  15,
		CacheWriteTokens: 3,
	}})
}

// TestAnthropicCachingDisabledOmitsCacheControl asserts that when prompt caching
// is turned off the request carries no cache_control markers at all.
func TestAnthropicCachingDisabledOmitsCacheControl(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		writeMinimalAnthropicSSE(w, `{"input_tokens":4,"output_tokens":1}`)
	}))
	defer server.Close()

	// Build through the registry so the model (with tool support) is wired, then
	// flip caching off on the concrete provider. The registry always enables it.
	provider := newAnthropicTestProvider(t, server.URL)
	p := provider.(*anthropicProvider)
	p.promptCaching = false

	events, err := p.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Tools:        []Tool{{Name: "only", InputSchema: json.RawMessage(`{"type":"object","properties":{}}`)}},
		SystemPrompt: "You are concise.",
		MaxTokens:    64,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotContains(t, string(rawBody), `"cache_control"`)
}

// newAnthropicThinkingProvider wires a registry-backed Anthropic provider whose
// model id is recognized as extended-thinking capable, so the request builder
// emits the thinking field when a request opts in.
func newAnthropicThinkingProvider(t *testing.T, serverURL string) Provider {
	t.Helper()
	t.Setenv("ANTHROPIC_TEST_KEY", "test-key")
	cfg := testConfig("anthropic", config.ProviderAnthropic, serverURL+"/v1")
	cfg.Providers[0].Models = []string{"claude-sonnet-4-20250514"}
	cfg.Models[0].ID = "claude-sonnet-4-20250514"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)
	return provider
}

// TestAnthropicStreamsThinkingThenText drives a canned SSE stream that emits
// thinking_delta blocks before text_delta blocks and asserts the thinking text
// surfaces as ThinkingEvents, the answer as DeltaTextEvents, and that the two
// kinds arrive in source order with thinking strictly before text.
func TestAnthropicStreamsThinkingThenText(t *testing.T) {
	var captured anthropicRequest
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		require.NoError(t, json.Unmarshal(b, &captured))

		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10,\"output_tokens\":1}}}\n\n")
		// Thinking content block: two thinking deltas, then a signature delta the
		// provider must ignore (it is not human-readable reasoning).
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\",\"thinking\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"Let me \"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"reason.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"abc123\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Answer text block.
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"The answer \"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"is 42.\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		fmt.Fprint(w, "event: message_delta\n"+
			"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":8}}\n\n")
		fmt.Fprint(w, "event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	provider := newAnthropicThinkingProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "what is the answer"}},
		}},
		Thinking:  &ThinkingConfig{BudgetTokens: 1024},
		MaxTokens: 2048,
	})
	require.NoError(t, err)
	got := collectEvents(events)

	// Thinking deltas became ThinkingEvents in order; the signature_delta is not
	// surfaced as reasoning text.
	var thinking []string
	for _, ev := range got {
		if te, ok := ev.(ThinkingEvent); ok {
			thinking = append(thinking, te.Text)
		}
	}
	require.Equal(t, []string{"Let me ", "reason."}, thinking)

	// Answer text became DeltaTextEvents in order.
	require.Equal(t, []string{"The answer ", "is 42."}, textDeltaSequence(got))

	// Ordering across kinds: every ThinkingEvent precedes every DeltaTextEvent,
	// matching the source stream (thinking block before text block).
	lastThinkingIdx, firstTextIdx := -1, -1
	for i, ev := range got {
		switch ev.(type) {
		case ThinkingEvent:
			lastThinkingIdx = i
		case DeltaTextEvent:
			if firstTextIdx == -1 {
				firstTextIdx = i
			}
		}
	}
	require.NotEqual(t, -1, lastThinkingIdx, "expected at least one ThinkingEvent")
	require.NotEqual(t, -1, firstTextIdx, "expected at least one DeltaTextEvent")
	require.Less(t, lastThinkingIdx, firstTextIdx, "all thinking events must precede the answer text")

	// The thinking-enabled request carries the thinking field on the wire with
	// type "enabled" and the requested budget. Assert both the parsed struct and
	// the raw bytes so the documented shape is literally sent.
	require.NotNil(t, captured.Thinking)
	require.Equal(t, "enabled", captured.Thinking.Type)
	require.Equal(t, 1024, captured.Thinking.BudgetTokens)
	require.Contains(t, string(rawBody), `"thinking":{"type":"enabled","budget_tokens":1024}`)

	// Start first, End last, with merged usage.
	require.IsType(t, StartEvent{}, got[0])
	require.IsType(t, EndEvent{}, got[len(got)-1])
	require.Equal(t, 1, countEndEvents(got))
	require.Contains(t, got, EndEvent{Usage: Usage{InputTokens: 10, OutputTokens: 8}})
}

// TestAnthropicThinkingBudgetLiftsMaxTokens asserts that enabling extended
// thinking with a budget at or above the resolved max_tokens lifts the cap above
// the budget, since Anthropic requires max_tokens to be strictly greater than
// thinking.budget_tokens (the budget is carved out of the same output allowance)
// and would otherwise reject the request with a 400.
func TestAnthropicThinkingBudgetLiftsMaxTokens(t *testing.T) {
	t.Run("default max_tokens below budget is lifted", func(t *testing.T) {
		var captured anthropicRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(b, &captured))
			writeMinimalAnthropicSSE(w, `{"input_tokens":4,"output_tokens":1}`)
		}))
		defer server.Close()

		provider := newAnthropicThinkingProvider(t, server.URL)
		// No explicit MaxTokens, so it resolves to defaultAnthropicMaxTokens, which
		// is below this budget and must be lifted above it.
		events, err := provider.Stream(context.Background(), Request{
			Model: "claude-sonnet-4-20250514",
			Messages: []message.Message{{
				Role:    message.RoleUser,
				Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
			}},
			Thinking: &ThinkingConfig{BudgetTokens: 8192},
		})
		require.NoError(t, err)
		_ = collectEvents(events)

		require.Equal(t, 8192, captured.Thinking.BudgetTokens)
		require.Equal(t, 8192+defaultAnthropicMaxTokens, captured.MaxTokens)
		require.Greater(t, captured.MaxTokens, captured.Thinking.BudgetTokens)
	})

	t.Run("explicit max_tokens above budget is preserved", func(t *testing.T) {
		var captured anthropicRequest
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, err := io.ReadAll(r.Body)
			require.NoError(t, err)
			require.NoError(t, json.Unmarshal(b, &captured))
			writeMinimalAnthropicSSE(w, `{"input_tokens":4,"output_tokens":1}`)
		}))
		defer server.Close()

		provider := newAnthropicThinkingProvider(t, server.URL)
		events, err := provider.Stream(context.Background(), Request{
			Model: "claude-sonnet-4-20250514",
			Messages: []message.Message{{
				Role:    message.RoleUser,
				Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
			}},
			Thinking:  &ThinkingConfig{BudgetTokens: 1024},
			MaxTokens: 4096,
		})
		require.NoError(t, err)
		_ = collectEvents(events)

		// The caller's cap already exceeds the budget, so it is left untouched.
		require.Equal(t, 4096, captured.MaxTokens)
	})
}

// TestAnthropicSkipsRedactedThinking asserts a redacted_thinking content block
// is dropped: it produces no ThinkingEvent (its payload is encrypted) while a
// following text block still streams normally.
func TestAnthropicSkipsRedactedThinking(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "event: message_start\n"+
			"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":6,\"output_tokens\":1}}}\n\n")
		// Redacted thinking block: encrypted data, no human-readable deltas.
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"redacted_thinking\",\"data\":\"EncRyPtEdBytes==\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n")
		// Visible answer block follows.
		fmt.Fprint(w, "event: content_block_start\n"+
			"data: {\"type\":\"content_block_start\",\"index\":1,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n")
		fmt.Fprint(w, "event: content_block_delta\n"+
			"data: {\"type\":\"content_block_delta\",\"index\":1,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n")
		fmt.Fprint(w, "event: content_block_stop\n"+
			"data: {\"type\":\"content_block_stop\",\"index\":1}\n\n")
		fmt.Fprint(w, "event: message_stop\n"+
			"data: {\"type\":\"message_stop\"}\n\n")
	}))
	defer server.Close()

	provider := newAnthropicThinkingProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "claude-sonnet-4-20250514",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Thinking:  &ThinkingConfig{BudgetTokens: 512},
		MaxTokens: 1024,
	})
	require.NoError(t, err)
	got := collectEvents(events)

	// No ThinkingEvent: the encrypted block never surfaces as reasoning text.
	for _, ev := range got {
		_, isThinking := ev.(ThinkingEvent)
		require.False(t, isThinking, "redacted_thinking must not produce a ThinkingEvent")
	}

	// The visible answer still streams normally after the redacted block.
	require.Equal(t, []string{"hi"}, textDeltaSequence(got))
	require.Equal(t, 1, countEndEvents(got))
}

// TestAnthropicOmitsThinkingForUnsupportedModel asserts the thinking field is
// not sent when the configured model is not thinking-capable, even though the
// caller opted in, so the request does not 400 on a non-thinking model.
func TestAnthropicOmitsThinkingForUnsupportedModel(t *testing.T) {
	var rawBody []byte
	var captured anthropicRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, err := io.ReadAll(r.Body)
		require.NoError(t, err)
		rawBody = b
		require.NoError(t, json.Unmarshal(b, &captured))
		writeMinimalAnthropicSSE(w, `{"input_tokens":3,"output_tokens":1}`)
	}))
	defer server.Close()

	// newAnthropicTestProvider registers the model id "test-model", which the
	// thinking-capability check does not recognize.
	provider := newAnthropicTestProvider(t, server.URL)
	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Thinking:  &ThinkingConfig{BudgetTokens: 1024},
		MaxTokens: 64,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.Nil(t, captured.Thinking)
	require.NotContains(t, string(rawBody), `"thinking"`)
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

func TestAnthropicCountTokensUsesNativeEndpoint(t *testing.T) {
	t.Setenv("ANTHROPIC_TEST_KEY", "secret")

	var gotPath, gotKey, gotVersion string
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-api-key")
		gotVersion = r.Header.Get("anthropic-version")
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"input_tokens":57}`)
	}))
	defer server.Close()

	cfg := testConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	counter, ok := provider.(TokenCounter)
	require.True(t, ok, "anthropic provider must satisfy TokenCounter")

	n, err := counter.CountTokens(context.Background(), Request{
		Model:        "test-model",
		Messages:     []message.Message{textMsg("count me")},
		SystemPrompt: "be brief",
		Tools:        []Tool{{Name: "get_weather", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		// MaxTokens and Temperature are inference-only and must be dropped.
		MaxTokens:   4096,
		Temperature: 0.7,
	})
	require.NoError(t, err)
	require.Equal(t, 57, n)

	// The native count_tokens method and auth headers ride through.
	require.Equal(t, "/v1/messages/count_tokens", gotPath)
	require.Equal(t, "secret", gotKey)
	require.Equal(t, anthropicVersion, gotVersion)

	// The body carries the same system, messages, and tools the stream path would
	// send, but none of the inference-only fields the endpoint rejects.
	var captured map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Contains(t, captured, "system")
	require.Contains(t, captured, "messages")
	require.Contains(t, captured, "tools")
	require.NotContains(t, captured, "max_tokens")
	require.NotContains(t, captured, "stream")
	require.NotContains(t, captured, "temperature")

	var req anthropicCountTokensRequest
	require.NoError(t, json.Unmarshal(rawBody, &req))
	require.Equal(t, "test-model", req.Model)
	require.Len(t, req.System, 1)
	require.Equal(t, "be brief", req.System[0].Text)
	require.Len(t, req.Messages, 1)
	require.Len(t, req.Tools, 1)
	require.Equal(t, "get_weather", req.Tools[0].Name)
}

func TestAnthropicCountTokensRequiresAPIKey(t *testing.T) {
	t.Setenv("ANTHROPIC_TEST_KEY", "")

	cfg := testConfig("anthropic", config.ProviderAnthropic, "http://example.invalid/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	counter, ok := provider.(TokenCounter)
	require.True(t, ok)

	_, err = counter.CountTokens(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.ErrorIs(t, err, ErrAuth)
}
