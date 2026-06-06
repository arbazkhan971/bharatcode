package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// responsesProvider stands up an httptest server that records the request path
// and body, replies with body, and returns a wired Responses provider plus
// pointers to the captured path and raw request bytes. The opt-in is exercised
// end to end through NewRegistry using the in-package openai_responses type.
func responsesProvider(t *testing.T, model string, reply string) (Provider, *string, *[]byte) {
	t.Helper()
	gotPath := new(string)
	gotBody := new([]byte)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*gotPath = r.URL.Path
		b, _ := io.ReadAll(r.Body)
		*gotBody = b
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, reply)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEST_RESPONSES_KEY", "sk-resp-123")

	cfg := testConfig("openai-responses", providerOpenAIResponses, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_RESPONSES_KEY"
	cfg.Providers[0].Models = []string{model}
	cfg.Models[0].ID = model
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai-responses")
	require.NoError(t, err)
	return provider, gotPath, gotBody
}

// cannedResponsesReply is a minimal but realistic non-streaming Responses
// payload: one message output item carrying output_text plus a usage object in
// the Responses field naming (input_tokens/output_tokens, cached under
// input_tokens_details).
const cannedResponsesReply = `{
  "id": "resp_abc123",
  "object": "response",
  "status": "completed",
  "model": "gpt-4o",
  "output": [
    {
      "type": "message",
      "id": "msg_1",
      "status": "completed",
      "role": "assistant",
      "content": [
        {"type": "output_text", "text": "Hello from ", "annotations": []},
        {"type": "output_text", "text": "BharatCode.", "annotations": []}
      ]
    }
  ],
  "usage": {
    "input_tokens": 42,
    "input_tokens_details": {"cached_tokens": 7},
    "output_tokens": 13,
    "output_tokens_details": {"reasoning_tokens": 0},
    "total_tokens": 55
  }
}`

// TestResponsesProviderPostsToResponsesEndpoint asserts the opt-in provider
// targets /v1/responses (not /v1/chat/completions), authenticates with the
// configured bearer token, and sends the Responses request shape: top-level
// instructions from the system prompt plus an input-items array whose user
// message carries an input_text content part. These are captured-body
// assertions on the literal JSON, not struct round-trips.
func TestResponsesProviderPostsToResponsesEndpoint(t *testing.T) {
	provider, gotPath, gotBody := responsesProvider(t, "gpt-4o", cannedResponsesReply)

	events, err := provider.Stream(context.Background(), Request{
		Model:        "gpt-4o",
		SystemPrompt: "You are BharatCode.",
		Temperature:  0.5,
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi there"}},
		}},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.Equal(t, "/v1/responses", *gotPath)
	require.NotEmpty(t, *gotBody, "server must have received the request")

	var probe struct {
		Model        string `json:"model"`
		Instructions string `json:"instructions"`
		Stream       *bool  `json:"stream"`
		Input        []struct {
			Role    string `json:"role"`
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(*gotBody, &probe))

	require.Equal(t, "gpt-4o", probe.Model)
	require.Equal(t, "You are BharatCode.", probe.Instructions)
	require.NotNil(t, probe.Stream)
	require.True(t, *probe.Stream, "the provider must request a streaming response")

	require.Len(t, probe.Input, 1)
	require.Equal(t, "user", probe.Input[0].Role)
	require.Len(t, probe.Input[0].Content, 1)
	require.Equal(t, "input_text", probe.Input[0].Content[0].Type)
	require.Equal(t, "hi there", probe.Input[0].Content[0].Text)

	// The request must not carry chat/completions-only fields, proving it is a
	// genuine Responses request and not the old shape pointed at a new path.
	require.NotContains(t, string(*gotBody), `"messages"`)
	require.NotContains(t, string(*gotBody), `"max_tokens"`)
}

// TestResponsesProviderUsesBearerAndDefaultURL asserts the Authorization header
// carries the configured key and that an omitted base_url defaults to the
// official OpenAI v1 root.
func TestResponsesProviderUsesBearerAndDefaultURL(t *testing.T) {
	var gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, cannedResponsesReply)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEST_RESPONSES_KEY", "sk-resp-123")

	cfg := testConfig("openai-responses", providerOpenAIResponses, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_RESPONSES_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai-responses")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)

	require.Equal(t, "Bearer sk-resp-123", gotAuth)

	// Default base URL when base_url is empty.
	cfg2 := testConfig("openai-responses", providerOpenAIResponses, "")
	cfg2.Providers[0].APIKeyEnv = "TEST_RESPONSES_KEY"
	reg2, err := NewRegistry(cfg2)
	require.NoError(t, err)
	p2, err := reg2.Get("openai-responses")
	require.NoError(t, err)
	got, ok := p2.(*openAIResponsesProvider)
	require.True(t, ok)
	require.Equal(t, "https://api.openai.com/v1", got.baseURL)
}

// TestResponsesProviderMapsOutputAndUsage asserts the parsed Responses payload
// maps to DeltaText events for each output_text part and a terminal EndEvent
// carrying the Responses usage in BharatCode's neutral counts. The usage
// assertion is the load-bearing one: it proves the Responses field naming
// (input_tokens/output_tokens, cached under input_tokens_details) is parsed
// rather than silently mapped to zero by the chat/completions usage struct.
func TestResponsesProviderMapsOutputAndUsage(t *testing.T) {
	provider, _, _ := responsesProvider(t, "gpt-4o", cannedResponsesReply)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	// First event is Start with the provider/model identity.
	start, ok := findEvent[StartEvent](got)
	require.True(t, ok, "expected a StartEvent")
	require.Equal(t, "openai-responses", start.Provider)
	require.Equal(t, "gpt-4o", start.Model)

	// Each output_text part becomes a DeltaTextEvent in order; concatenation
	// must equal the full assistant message.
	var assembled string
	deltas := 0
	for _, ev := range got {
		if d, ok := ev.(DeltaTextEvent); ok {
			assembled += d.Text
			deltas++
		}
	}
	require.Equal(t, 2, deltas, "expected one DeltaText per output_text part")
	require.Equal(t, "Hello from BharatCode.", assembled)

	// Terminal EndEvent carries the mapped usage with non-zero, correctly named
	// token counts and cached-read tokens from input_tokens_details. input_tokens
	// (42) includes the 7 cached tokens, which are subtracted back out of the
	// non-cached input count (42 - 7 = 35) so the ledger does not bill them twice.
	end, ok := findEvent[EndEvent](got)
	require.True(t, ok, "expected an EndEvent")
	require.Equal(t, 35, end.Usage.InputTokens)
	require.Equal(t, 13, end.Usage.OutputTokens)
	require.Equal(t, 7, end.Usage.CacheReadTokens)

	// No error event on the happy path.
	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr, "happy path must not emit an ErrorEvent")
}

// TestResponsesProviderReasoningGating asserts the reasoning-vs-normal param
// gating on the wire: a reasoning model omits temperature and forwards the
// effort nested under the Responses API "reasoning" object (not the
// chat/completions top-level reasoning_effort) alongside an "auto" summary
// request, while a normal model sends temperature and never emits a reasoning
// object.
func TestResponsesProviderReasoningGating(t *testing.T) {
	t.Run("reasoning model omits temperature and nests effort under reasoning", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "o3-mini", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:           "o3-mini",
			Temperature:     0.7,
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		require.NotContains(t, string(*gotBody), `"temperature"`)
		// The effort must be nested, never sent as a top-level chat-style field.
		require.NotContains(t, string(*gotBody), `"reasoning_effort"`)
		var probe struct {
			Reasoning *struct {
				Effort  string `json:"effort"`
				Summary string `json:"summary"`
			} `json:"reasoning"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.NotNil(t, probe.Reasoning)
		require.Equal(t, "high", probe.Reasoning.Effort)
		// An auto summary is requested so the model's reasoning streams back.
		require.Equal(t, "auto", probe.Reasoning.Summary)
	})

	t.Run("reasoning model without effort still requests an auto summary", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "o3-mini", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:    "o3-mini",
			Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		// The reasoning object is still emitted to carry the summary request, but
		// no blank effort is sent when none is configured.
		require.NotContains(t, string(*gotBody), `"effort"`)
		var probe struct {
			Reasoning *struct {
				Effort  string `json:"effort"`
				Summary string `json:"summary"`
			} `json:"reasoning"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.NotNil(t, probe.Reasoning)
		require.Empty(t, probe.Reasoning.Effort)
		require.Equal(t, "auto", probe.Reasoning.Summary)
	})

	t.Run("auto effort is dropped but the summary request remains", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "o3-mini", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:           "o3-mini",
			ReasoningEffort: "auto",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		// "auto" is a provider-independent label OpenAI does not accept, so it must
		// not reach the effort field; the reasoning object still carries the summary.
		require.NotContains(t, string(*gotBody), `"effort"`)
		var probe struct {
			Reasoning *struct {
				Effort  string `json:"effort"`
				Summary string `json:"summary"`
			} `json:"reasoning"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.NotNil(t, probe.Reasoning)
		require.Empty(t, probe.Reasoning.Effort)
		require.Equal(t, "auto", probe.Reasoning.Summary)
	})

	t.Run("normal model sends temperature and omits the reasoning object", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "gpt-4o", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:           "gpt-4o",
			Temperature:     0.7,
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		require.NotContains(t, string(*gotBody), `"reasoning"`)
		var probe struct {
			Temperature *float64 `json:"temperature"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.NotNil(t, probe.Temperature)
		require.InEpsilon(t, 0.7, *probe.Temperature, 1e-9)
	})
}

// TestResponsesProviderRejectsToolsWhenUnsupported asserts a tools request is
// refused with ErrUnsupportedFeature when the selected model does not advertise
// tool support, rather than silently dropping the tools — mirroring the
// chat/completions gate.
func TestResponsesProviderRejectsToolsWhenUnsupported(t *testing.T) {
	provider, _, _ := responsesProvider(t, "gpt-4o", cannedResponsesReply)
	// responsesProvider's testConfig advertises tool support by default; flip it
	// off on the wired registry's model to exercise the gate.
	got := provider.(*openAIResponsesProvider)
	got.models[0].SupportsTools = false

	_, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		Tools:    []Tool{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}

// TestResponsesProviderSendsFlatFunctionTools asserts tool definitions are sent
// as flat Responses function tools ({"type":"function","name":...,"parameters":...})
// rather than the chat/completions nested {"function":{...}} shape, and that a
// missing schema defaults to an empty object schema.
func TestResponsesProviderSendsFlatFunctionTools(t *testing.T) {
	provider, _, gotBody := responsesProvider(t, "gpt-4o", cannedResponsesReply)

	_, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		Tools: []Tool{
			{Name: "lookup", Description: "look something up", InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`)},
			{Name: "noschema"},
		},
	})
	require.NoError(t, err)

	var probe struct {
		Tools []struct {
			Type        string          `json:"type"`
			Name        string          `json:"name"`
			Description string          `json:"description"`
			Parameters  json.RawMessage `json:"parameters"`
		} `json:"tools"`
	}
	require.NoError(t, json.Unmarshal(*gotBody, &probe))
	require.Len(t, probe.Tools, 2)
	require.Equal(t, "function", probe.Tools[0].Type)
	require.Equal(t, "lookup", probe.Tools[0].Name)
	require.Equal(t, "look something up", probe.Tools[0].Description)
	require.JSONEq(t, `{"type":"object","properties":{"q":{"type":"string"}}}`, string(probe.Tools[0].Parameters))
	// A tool with no schema defaults to an empty-object schema.
	require.Equal(t, "noschema", probe.Tools[1].Name)
	require.JSONEq(t, `{"type":"object","properties":{}}`, string(probe.Tools[1].Parameters))

	// Flat shape: the nested chat/completions key must not appear.
	require.NotContains(t, string(*gotBody), `"function":{`)
}

// TestResponsesProviderEmitsToolCall asserts a function_call output item is
// parsed into ToolUseStart/End events carrying the call_id, name, and arguments,
// so the agent loop can dispatch the tool.
func TestResponsesProviderEmitsToolCall(t *testing.T) {
	reply := `{
	  "id": "resp_tc",
	  "object": "response",
	  "status": "completed",
	  "model": "gpt-4o",
	  "output": [
	    {"type": "reasoning", "id": "rs_1", "summary": []},
	    {"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Looking that up."}]},
	    {"type": "function_call", "id": "fc_1", "call_id": "call_abc", "name": "lookup", "arguments": "{\"q\":\"weather\"}", "status": "completed"}
	  ],
	  "usage": {"input_tokens": 10, "output_tokens": 5, "total_tokens": 15}
	}`
	provider, _, _ := responsesProvider(t, "gpt-4o", reply)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "weather?"}}}},
		Tools:    []Tool{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	// The assistant text precedes the tool call.
	text, ok := findEvent[DeltaTextEvent](got)
	require.True(t, ok)
	require.Equal(t, "Looking that up.", text.Text)

	start, ok := findEvent[ToolUseStartEvent](got)
	require.True(t, ok, "expected a ToolUseStartEvent")
	require.Equal(t, "call_abc", start.ID, "the model addresses the call by call_id")
	require.Equal(t, "lookup", start.Name)

	end, ok := findEvent[ToolUseEndEvent](got)
	require.True(t, ok, "expected a ToolUseEndEvent")
	require.Equal(t, "call_abc", end.ID)
	require.Equal(t, "lookup", end.Name)
	require.JSONEq(t, `{"q":"weather"}`, string(end.Input))

	// EndEvent still terminates the stream after the tool call.
	_, hasEnd := findEvent[EndEvent](got)
	require.True(t, hasEnd)
}

// TestResponsesProviderEmitsBufferedReasoning asserts the buffered (non-streaming)
// path surfaces a reasoning output item's summary_text parts as ThinkingEvents,
// ahead of the assistant text, mirroring the streaming path's handling of
// response.reasoning_summary_text.delta. A server that ignores stream=true and
// replies with a single JSON body must not silently drop the reasoning.
func TestResponsesProviderEmitsBufferedReasoning(t *testing.T) {
	reply := `{
	  "id": "resp_reason",
	  "object": "response",
	  "status": "completed",
	  "model": "o4-mini",
	  "output": [
	    {"type": "reasoning", "id": "rs_1", "summary": [
	      {"type": "summary_text", "text": "Weighing the "},
	      {"type": "summary_text", "text": "options."}
	    ]},
	    {"type": "message", "id": "msg_1", "role": "assistant", "content": [{"type": "output_text", "text": "Answer."}]}
	  ],
	  "usage": {"input_tokens": 9, "output_tokens": 4, "total_tokens": 13}
	}`
	provider, _, _ := responsesProvider(t, "o4-mini", reply)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "o4-mini",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	// Each summary_text part becomes a ThinkingEvent; concatenation is the full
	// reasoning summary.
	var thinking string
	thinks := 0
	for _, ev := range got {
		if th, ok := ev.(ThinkingEvent); ok {
			thinking += th.Text
			thinks++
		}
	}
	require.Equal(t, 2, thinks, "expected one ThinkingEvent per summary_text part")
	require.Equal(t, "Weighing the options.", thinking)

	// The reasoning summary precedes the assistant text in emission order.
	var thinkIdx, textIdx = -1, -1
	for i, ev := range got {
		switch e := ev.(type) {
		case ThinkingEvent:
			if thinkIdx == -1 {
				thinkIdx = i
			}
		case DeltaTextEvent:
			if e.Text == "Answer." && textIdx == -1 {
				textIdx = i
			}
		}
	}
	require.NotEqual(t, -1, thinkIdx, "expected a ThinkingEvent")
	require.NotEqual(t, -1, textIdx, "expected the assistant DeltaTextEvent")
	require.Less(t, thinkIdx, textIdx, "reasoning must be emitted before the answer text")
}

// TestResponsesRequestEncodesToolCallRoundTrip asserts a multi-turn transcript
// (assistant tool call followed by a tool result) serializes to the Responses
// function_call / function_call_output input items, so the model sees its own
// prior call and the result on the next turn.
func TestResponsesRequestEncodesToolCallRoundTrip(t *testing.T) {
	provider, _, gotBody := responsesProvider(t, "gpt-4o", cannedResponsesReply)

	_, err := provider.Stream(context.Background(), Request{
		Model: "gpt-4o",
		Tools: []Tool{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
		Messages: []message.Message{
			{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "weather?"}}},
			{Role: message.RoleAssistant, Content: []message.ContentBlock{
				message.TextBlock{Text: "Checking."},
				message.ToolUseBlock{ID: "call_abc", Name: "lookup", Input: json.RawMessage(`{"q":"weather"}`)},
			}},
			{Role: message.RoleTool, Content: []message.ContentBlock{
				message.ToolResultBlock{ToolUseID: "call_abc", Content: "sunny"},
			}},
		},
	})
	require.NoError(t, err)

	var probe struct {
		Input []struct {
			Type      string `json:"type"`
			Role      string `json:"role"`
			CallID    string `json:"call_id"`
			Name      string `json:"name"`
			Arguments string `json:"arguments"`
			Output    string `json:"output"`
			Content   []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"input"`
	}
	require.NoError(t, json.Unmarshal(*gotBody, &probe))
	require.Len(t, probe.Input, 4, "user msg, assistant msg, function_call, function_call_output")

	// [0] user message.
	require.Equal(t, "user", probe.Input[0].Role)
	require.Equal(t, "input_text", probe.Input[0].Content[0].Type)

	// [1] assistant message text (output_text), then [2] the function_call item.
	require.Equal(t, "assistant", probe.Input[1].Role)
	require.Equal(t, "output_text", probe.Input[1].Content[0].Type)
	require.Equal(t, "Checking.", probe.Input[1].Content[0].Text)

	require.Equal(t, "function_call", probe.Input[2].Type)
	require.Equal(t, "call_abc", probe.Input[2].CallID)
	require.Equal(t, "lookup", probe.Input[2].Name)
	require.JSONEq(t, `{"q":"weather"}`, probe.Input[2].Arguments)

	// [3] the tool result as a function_call_output addressed by the same call_id.
	require.Equal(t, "function_call_output", probe.Input[3].Type)
	require.Equal(t, "call_abc", probe.Input[3].CallID)
	require.Equal(t, "sunny", probe.Input[3].Output)
}

// TestResponsesProviderSurfacesFailedStatus asserts a 200 reply that carries a
// non-null error object (a logical failure, e.g. a content filter) maps to an
// ErrorEvent rather than a silent empty EndEvent, so the parsed error payload
// is load-bearing.
func TestResponsesProviderSurfacesFailedStatus(t *testing.T) {
	failed := `{"id":"resp_x","object":"response","status":"failed","model":"gpt-4o","output":[],"error":{"code":"content_filter","message":"blocked by policy"}}`
	provider, _, _ := responsesProvider(t, "gpt-4o", failed)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "a failed-status reply must emit an ErrorEvent")
	require.ErrorIs(t, errEv.Err, ErrServer)
	require.Contains(t, errEv.Err.Error(), "blocked by policy")

	// And no terminal EndEvent should follow the surfaced error.
	_, hasEnd := findEvent[EndEvent](got)
	require.False(t, hasEnd, "no EndEvent after a surfaced failure")
}

// TestResponsesProviderMissingKey asserts the provider fails fast with ErrAuth
// when the configured API-key env var is unset, before any network call.
func TestResponsesProviderMissingKey(t *testing.T) {
	cfg := testConfig("openai-responses", providerOpenAIResponses, "https://api.openai.com/v1")
	cfg.Providers[0].APIKeyEnv = "MISSING_RESPONSES_KEY_FOR_TEST"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai-responses")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.ErrorIs(t, err, ErrAuth)
}

// streamingResponsesProvider stands up an httptest server that replies with an
// event-stream body (Content-Type text/event-stream) so the provider exercises
// its incremental SSE path rather than the buffered fallback. sse is the raw
// event-stream body; each event must already be framed as "data: <json>\n\n".
func streamingResponsesProvider(t *testing.T, model, sse string) Provider {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, sse)
	}))
	t.Cleanup(server.Close)
	t.Setenv("TEST_RESPONSES_KEY", "sk-resp-123")

	cfg := testConfig("openai-responses", providerOpenAIResponses, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_RESPONSES_KEY"
	cfg.Providers[0].Models = []string{model}
	cfg.Models[0].ID = model
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai-responses")
	require.NoError(t, err)
	return provider
}

// TestResponsesProviderStreamsText asserts the SSE path emits each
// output_text.delta as a DeltaTextEvent in order, maps reasoning deltas to
// ThinkingEvents, and carries the usage from the terminal response.completed
// envelope on the EndEvent.
func TestResponsesProviderStreamsText(t *testing.T) {
	sse := "data: {\"type\":\"response.created\",\"response\":{\"id\":\"resp_1\"}}\n\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"thinking...\"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello from \"}\n\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"BharatCode.\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":42,\"input_tokens_details\":{\"cached_tokens\":7},\"output_tokens\":13,\"total_tokens\":55}}}\n\n" +
		"data: [DONE]\n\n"
	provider := streamingResponsesProvider(t, "gpt-4o", sse)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	var text string
	for _, ev := range got {
		if d, ok := ev.(DeltaTextEvent); ok {
			text += d.Text
		}
	}
	require.Equal(t, "Hello from BharatCode.", text)

	think, ok := findEvent[ThinkingEvent](got)
	require.True(t, ok, "reasoning deltas must surface as ThinkingEvents")
	require.Equal(t, "thinking...", think.Text)

	end, ok := findEvent[EndEvent](got)
	require.True(t, ok, "expected a terminal EndEvent")
	// input_tokens (42) includes the 7 cached tokens; they are subtracted back
	// out (42 - 7 = 35) so the cache reads are not double-billed.
	require.Equal(t, 35, end.Usage.InputTokens)
	require.Equal(t, 13, end.Usage.OutputTokens)
	require.Equal(t, 7, end.Usage.CacheReadTokens)
}

// TestResponsesProviderStreamsToolCall asserts a streamed function call —
// announced by output_item.added then filled in by function_call_arguments.delta
// chunks — is assembled into ToolUseStart/End events carrying the call_id, name,
// and concatenated arguments, so the agent loop can dispatch it.
func TestResponsesProviderStreamsToolCall(t *testing.T) {
	sse := "data: {\"type\":\"response.output_item.added\",\"output_index\":0,\"item\":{\"type\":\"function_call\",\"id\":\"fc_1\",\"call_id\":\"call_abc\",\"name\":\"lookup\",\"arguments\":\"\"}}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"{\\\"q\\\":\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"output_index\":0,\"delta\":\"\\\"weather\\\"}\"}\n\n" +
		"data: {\"type\":\"response.function_call_arguments.done\",\"output_index\":0,\"arguments\":\"{\\\"q\\\":\\\"weather\\\"}\"}\n\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"usage\":{\"input_tokens\":10,\"output_tokens\":5,\"total_tokens\":15}}}\n\n"
	provider := streamingResponsesProvider(t, "gpt-4o", sse)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "weather?"}}}},
		Tools:    []Tool{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	start, ok := findEvent[ToolUseStartEvent](got)
	require.True(t, ok, "expected a ToolUseStartEvent")
	require.Equal(t, "call_abc", start.ID, "the model addresses the call by call_id")
	require.Equal(t, "lookup", start.Name)

	end, ok := findEvent[ToolUseEndEvent](got)
	require.True(t, ok, "expected a ToolUseEndEvent")
	require.Equal(t, "call_abc", end.ID)
	require.Equal(t, "lookup", end.Name)
	require.JSONEq(t, `{"q":"weather"}`, string(end.Input))

	_, hasEnd := findEvent[EndEvent](got)
	require.True(t, hasEnd, "EndEvent terminates the stream after the tool call")
}

// TestResponsesProviderStreamCompletesOnMaxOutputTokens asserts a terminal
// response.incomplete whose reason is "max_output_tokens" completes the turn:
// the partial text already streamed is preserved, a normal EndEvent carrying the
// usage follows, and no ErrorEvent is raised. Hitting the output cap is a
// truncated-but-valid completion (matching the chat path's finish_reason
// "length"), so retrying it would only discard the partial answer.
func TestResponsesProviderStreamCompletesOnMaxOutputTokens(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial answer\"}\n\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"max_output_tokens\"},\"usage\":{\"input_tokens\":20,\"output_tokens\":16,\"total_tokens\":36}}}\n\n" +
		"data: [DONE]\n\n"
	provider := streamingResponsesProvider(t, "gpt-4o", sse)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr, "hitting the output cap is a completed turn, not an error")

	var text string
	for _, ev := range got {
		if d, ok := ev.(DeltaTextEvent); ok {
			text += d.Text
		}
	}
	require.Equal(t, "partial answer", text, "the partial text must be preserved")

	end, ok := findEvent[EndEvent](got)
	require.True(t, ok, "an incomplete-by-length stream still ends with an EndEvent")
	require.Equal(t, 20, end.Usage.InputTokens)
	require.Equal(t, 16, end.Usage.OutputTokens)
}

// TestResponsesProviderStreamSurfacesOtherIncomplete asserts an incomplete
// response for a reason other than the output cap (here content_filter) stays a
// surfaced ErrorEvent wrapping ErrServer, and that no EndEvent follows it — only
// the max_output_tokens reason is treated as a normal completion.
func TestResponsesProviderStreamSurfacesOtherIncomplete(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"response.incomplete\",\"response\":{\"status\":\"incomplete\",\"incomplete_details\":{\"reason\":\"content_filter\"}}}\n\n"
	provider := streamingResponsesProvider(t, "gpt-4o", sse)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "a non-length incomplete must surface an ErrorEvent")
	require.ErrorIs(t, errEv.Err, ErrServer)

	_, hasEnd := findEvent[EndEvent](got)
	require.False(t, hasEnd, "no EndEvent after a surfaced incomplete failure")
}

// TestResponsesProviderStreamSurfacesFailure asserts a terminal
// response.failed event is surfaced as an ErrorEvent (wrapping ErrServer for the
// retry layer) carrying the reported message, and that no EndEvent follows it.
func TestResponsesProviderStreamSurfacesFailure(t *testing.T) {
	sse := "data: {\"type\":\"response.output_text.delta\",\"delta\":\"partial\"}\n\n" +
		"data: {\"type\":\"response.failed\",\"response\":{\"status\":\"failed\",\"error\":{\"code\":\"server_error\",\"message\":\"model overloaded\"}}}\n\n"
	provider := streamingResponsesProvider(t, "gpt-4o", sse)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	got := collectEvents(events)

	errEv, ok := findEvent[ErrorEvent](got)
	require.True(t, ok, "a failed stream must surface an ErrorEvent")
	require.ErrorIs(t, errEv.Err, ErrServer)
	require.Contains(t, errEv.Err.Error(), "model overloaded")

	_, hasEnd := findEvent[EndEvent](got)
	require.False(t, hasEnd, "no EndEvent after a surfaced failure")
}
