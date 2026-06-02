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
	require.False(t, *probe.Stream, "non-streaming path must send stream:false")

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
	// token counts and cached-read tokens from input_tokens_details.
	end, ok := findEvent[EndEvent](got)
	require.True(t, ok, "expected an EndEvent")
	require.Equal(t, 42, end.Usage.InputTokens)
	require.Equal(t, 13, end.Usage.OutputTokens)
	require.Equal(t, 7, end.Usage.CacheReadTokens)

	// No error event on the happy path.
	_, hasErr := findEvent[ErrorEvent](got)
	require.False(t, hasErr, "happy path must not emit an ErrorEvent")
}

// TestResponsesProviderReasoningGating asserts the reasoning-vs-normal param
// gating mirrors the chat/completions path on the wire: a reasoning model omits
// temperature and forwards reasoning_effort, while a normal model sends
// temperature and never emits reasoning_effort.
func TestResponsesProviderReasoningGating(t *testing.T) {
	t.Run("reasoning model omits temperature and sends effort", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "o3-mini", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:           "o3-mini",
			Temperature:     0.7,
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		require.NotContains(t, string(*gotBody), `"temperature"`)
		var probe struct {
			ReasoningEffort string `json:"reasoning_effort"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.Equal(t, "high", probe.ReasoningEffort)
	})

	t.Run("normal model sends temperature and omits effort", func(t *testing.T) {
		provider, _, gotBody := responsesProvider(t, "gpt-4o", cannedResponsesReply)

		_, err := provider.Stream(context.Background(), Request{
			Model:           "gpt-4o",
			Temperature:     0.7,
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})
		require.NoError(t, err)

		require.NotContains(t, string(*gotBody), `"reasoning_effort"`)
		var probe struct {
			Temperature *float64 `json:"temperature"`
		}
		require.NoError(t, json.Unmarshal(*gotBody, &probe))
		require.NotNil(t, probe.Temperature)
		require.InEpsilon(t, 0.7, *probe.Temperature, 1e-9)
	})
}

// TestResponsesProviderRejectsTools asserts a tools request is refused with
// ErrUnsupportedFeature on this path (tool calling is a followup) rather than
// silently dropping the tools.
func TestResponsesProviderRejectsTools(t *testing.T) {
	provider, _, _ := responsesProvider(t, "gpt-4o", cannedResponsesReply)

	_, err := provider.Stream(context.Background(), Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		Tools:    []Tool{{Name: "lookup", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
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
