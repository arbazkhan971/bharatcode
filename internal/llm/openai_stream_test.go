package llm

import (
	"context"
	"encoding/json"
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

// sseStreamProvider stands up an httptest server that replies to the
// chat-completions endpoint with the given raw SSE body and returns a wired
// openai-compatible provider plus the captured request body pointer. The body
// is sent verbatim so tests control the exact on-the-wire SSE framing.
func sseStreamProvider(t *testing.T, sse string) (Provider, *openAIChatRequest) {
	t.Helper()
	captured := &openAIChatRequest{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(captured))
		w.Header().Set("Content-Type", "text/event-stream")
		// Flush the whole canned stream in one write; the SSE scanner splits on
		// the line/blank-line framing regardless of how bytes arrive.
		fmt.Fprint(w, sse)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)
	return provider, captured
}

// streamRequest is a minimal single-tool, single-message request used by the
// streaming hardening tests.
func streamRequest() Request {
	return Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
		Tools: []Tool{{
			Name:        "lookup",
			Description: "Looks up data.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
	}
}

// findEvent returns the first event of type T, reporting whether one was found.
func findEvent[T Event](events []Event) (T, bool) {
	for _, ev := range events {
		if typed, ok := ev.(T); ok {
			return typed, true
		}
	}
	var zero T
	return zero, false
}

// TestOpenAIStreamAssemblesMultiChunkToolCall proves a tool call whose
// arguments arrive split across many small deltas (the real OpenAI streaming
// shape) accumulates into one syntactically complete ToolUseEndEvent. The
// arguments are deliberately fragmented mid-token ("{\"q" / "ery\":\"bha" /
// "rat\"}") to catch any naive whitespace- or token-boundary assumption.
func TestOpenAIStreamAssemblesMultiChunkToolCall(t *testing.T) {
	sse := strings.Join([]string{
		// First delta opens the call: id + name, no args yet.
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":""}}]}}]}`,
		"",
		// Argument fragments split across chunks, including a split inside a
		// JSON key and a JSON string value.
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"q"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"uery\":\"bha"}}]}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"function":{"arguments":"rat\"}"}}]}}]}`,
		"",
		`data: {"usage":{"prompt_tokens":11,"completion_tokens":4}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	provider, _ := sseStreamProvider(t, sse)
	events, err := provider.Stream(context.Background(), streamRequest())
	require.NoError(t, err)
	got := collectEvents(events)

	start, ok := findEvent[ToolUseStartEvent](got)
	require.True(t, ok, "expected a ToolUseStartEvent")
	require.Equal(t, "call_1", start.ID)
	require.Equal(t, "lookup", start.Name)

	end, ok := findEvent[ToolUseEndEvent](got)
	require.True(t, ok, "expected a ToolUseEndEvent")
	require.Equal(t, "call_1", end.ID)
	require.Equal(t, "lookup", end.Name)
	// The fragmented arguments must reassemble into valid, exact JSON.
	require.JSONEq(t, `{"query":"bharat"}`, string(end.Input))

	// And the concatenation of every ToolUseDeltaEvent must equal the final
	// argument string, proving no delta was dropped or duplicated.
	var assembled strings.Builder
	for _, ev := range got {
		if d, ok := ev.(ToolUseDeltaEvent); ok {
			require.Equal(t, "call_1", d.ID)
			assembled.WriteString(d.Delta)
		}
	}
	require.Equal(t, `{"query":"bharat"}`, assembled.String())
}

// TestOpenAIStreamEndUsageFromFinalChunk proves the terminal EndEvent carries
// the prompt/completion token counts from the provider's final usage chunk
// (not zero), and that EndEvent is the very last event emitted -- after the
// tool call is closed out. This pins the include_usage contract end to end.
func TestOpenAIStreamEndUsageFromFinalChunk(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hello"}}]}`,
		"",
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}]}`,
		"",
		// Final usage chunk: empty choices, populated usage -- the OpenAI shape
		// when stream_options.include_usage is set.
		`data: {"choices":[],"usage":{"prompt_tokens":42,"completion_tokens":7}}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	provider, _ := sseStreamProvider(t, sse)
	events, err := provider.Stream(context.Background(), streamRequest())
	require.NoError(t, err)
	got := collectEvents(events)

	// Exactly one EndEvent, carrying the real usage.
	require.Equal(t, 1, countEndEvents(got), "exactly one EndEvent expected")
	end, ok := findEvent[EndEvent](got)
	require.True(t, ok)
	require.Equal(t, Usage{InputTokens: 42, OutputTokens: 7}, end.Usage)

	// Ordering: EndEvent must be terminal, emitted after ToolUseEndEvent.
	require.IsType(t, EndEvent{}, got[len(got)-1], "EndEvent must be the last event")
	endIdx, toolEndIdx := -1, -1
	for i, ev := range got {
		switch ev.(type) {
		case EndEvent:
			endIdx = i
		case ToolUseEndEvent:
			toolEndIdx = i
		}
	}
	require.NotEqual(t, -1, toolEndIdx, "expected a ToolUseEndEvent")
	require.Less(t, toolEndIdx, endIdx, "ToolUseEndEvent must precede the terminal EndEvent")

	// No ErrorEvent slipped in.
	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr, "clean stream must not emit an ErrorEvent")
}

// TestOpenAIStreamRequestSetsIncludeUsage proves the request actually asks the
// provider for usage by setting stream_options.include_usage=true on the wire.
// Without this flag OpenAI omits usage from streamed responses, so EndEvent
// would always report zero tokens.
func TestOpenAIStreamRequestSetsIncludeUsage(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":1,\"completion_tokens\":1}}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), streamRequest())
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody, "server must have received the request body")
	// Raw-wire assertion on the nested object.
	require.Contains(t, string(rawBody), `"stream_options":{"include_usage":true}`)

	var probe struct {
		StreamOptions *struct {
			IncludeUsage bool `json:"include_usage"`
		} `json:"stream_options"`
	}
	require.NoError(t, json.Unmarshal(rawBody, &probe))
	require.NotNil(t, probe.StreamOptions, "request must carry stream_options")
	require.True(t, probe.StreamOptions.IncludeUsage, "include_usage must be true")
}

// TestOpenAIStreamToleratesNoiseLines proves the SSE parser survives the
// real-world framing noise a stream can contain: keep-alive comment lines
// (": ping"), blank keep-alive lines, multi-line data: fields that the spec
// says must be joined with newlines, and a [DONE] sentinel padded with
// surrounding whitespace. The content delta is itself split across two data:
// lines within one event to exercise the join.
func TestOpenAIStreamToleratesNoiseLines(t *testing.T) {
	// One logical event whose JSON object is split across two data: lines at a
	// structural boundary; per the SSE spec the parser joins them with a
	// newline before decoding, and JSON treats that newline as insignificant
	// whitespace between tokens. The content value carries an escaped newline so
	// the decoded text is observable and unambiguous.
	multiLineEvent := "data: {\"choices\":[{\"delta\":" + "\ndata: {\"content\":\"line1\\nline2\"}}]}"

	sse := strings.Join([]string{
		": ping", // keep-alive comment, ignored
		"",       // blank keep-alive line, no pending data -> no-op
		multiLineEvent,
		"",
		":", // bare comment colon
		`data: {"choices":[{"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"lookup","arguments":"{\"q\":\"x\"}"}}]}}]}`,
		"",
		`data: {"choices":[],"usage":{"prompt_tokens":3,"completion_tokens":2}}`,
		"",
		"data: [DONE]  ", // sentinel with trailing whitespace in the value
		"",
	}, "\n")

	provider, _ := sseStreamProvider(t, sse)
	events, err := provider.Stream(context.Background(), streamRequest())
	require.NoError(t, err)
	got := collectEvents(events)

	// The two joined data: lines reassemble into one valid JSON object whose
	// content decodes to "line1\nline2".
	text, ok := findEvent[DeltaTextEvent](got)
	require.True(t, ok, "expected a DeltaTextEvent")
	require.Equal(t, "line1\nline2", text.Text)

	// The tool call still assembles correctly despite the noise.
	end, ok := findEvent[ToolUseEndEvent](got)
	require.True(t, ok, "expected a ToolUseEndEvent")
	require.JSONEq(t, `{"q":"x"}`, string(end.Input))

	// Usage flows through and the stream ends cleanly with no error.
	require.Equal(t, 1, countEndEvents(got))
	endEv, _ := findEvent[EndEvent](got)
	require.Equal(t, Usage{InputTokens: 3, OutputTokens: 2}, endEv.Usage)
	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr, "noise lines must not produce an ErrorEvent")
}

// TestOpenAIStreamDoneSentinelTerminatesWithoutUsage proves a stream that ends
// with [DONE] but never sent a usage chunk still terminates cleanly with a
// single zero-usage EndEvent rather than hanging or erroring -- the sentinel
// alone is enough to close the stream.
func TestOpenAIStreamDoneSentinelTerminatesWithoutUsage(t *testing.T) {
	sse := strings.Join([]string{
		`data: {"choices":[{"delta":{"content":"hi"}}]}`,
		"",
		"data: [DONE]",
		"",
	}, "\n")

	provider, _ := sseStreamProvider(t, sse)
	events, err := provider.Stream(context.Background(), streamRequest())
	require.NoError(t, err)
	got := collectEvents(events)

	require.Equal(t, 1, countEndEvents(got))
	end, _ := findEvent[EndEvent](got)
	require.Equal(t, Usage{}, end.Usage, "no usage chunk means a zero-usage EndEvent")
	require.IsType(t, EndEvent{}, got[len(got)-1], "EndEvent must be terminal")
	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr)
}
