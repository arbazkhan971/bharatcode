package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// geminiProviderFor wires a single gemini provider against the given test
// server and returns it ready to Stream.
func geminiProviderFor(t *testing.T, cfg *config.Config) Provider {
	t.Helper()
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("gemini")
	require.NoError(t, err)
	return provider
}

// geminiSSE formats the chunks as an alt=sse stream the way the Generative
// Language API does: one "data: <json>" line per chunk.
func geminiSSE(chunks ...string) string {
	var b strings.Builder
	for _, c := range chunks {
		b.WriteString("data: ")
		b.WriteString(c)
		b.WriteString("\n\n")
	}
	return b.String()
}

func TestGeminiStreamsTextAndUsage(t *testing.T) {
	var gotPath, gotKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		require.Equal(t, "sse", r.URL.Query().Get("alt"))
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"Hello"}]}}]}`,
			`{"candidates":[{"content":{"role":"model","parts":[{"text":" world"}]}}],"usageMetadata":{"promptTokenCount":7,"candidatesTokenCount":3,"cachedContentTokenCount":2}}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	require.Equal(t, "/models/test-model:streamGenerateContent", gotPath)
	require.Empty(t, gotKey, "no api_key_env configured means no auth header")

	var text strings.Builder
	var usage Usage
	for _, ev := range collected {
		switch e := ev.(type) {
		case DeltaTextEvent:
			text.WriteString(e.Text)
		case EndEvent:
			usage = e.Usage
		}
	}
	require.Equal(t, "Hello world", text.String())
	require.Equal(t, 7, usage.InputTokens)
	require.Equal(t, 3, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadTokens)
}

func TestGeminiFoldsThoughtTokensIntoOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A Gemini 2.5 thinking response reports reasoning tokens in
		// thoughtsTokenCount, separately from candidatesTokenCount.
		_, _ = io.WriteString(w, geminiSSE(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]}}],"usageMetadata":{"promptTokenCount":11,"candidatesTokenCount":4,"thoughtsTokenCount":20}}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	var usage Usage
	for _, ev := range collected {
		if e, ok := ev.(EndEvent); ok {
			usage = e.Usage
		}
	}
	require.Equal(t, 11, usage.InputTokens)
	// Reasoning tokens are billed as output, so OutputTokens folds in the 20
	// thought tokens on top of the 4 visible candidate tokens.
	require.Equal(t, 24, usage.OutputTokens)
}

func TestGeminiEmitsFunctionCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(
			`{"candidates":[{"content":{"role":"model","parts":[{"functionCall":{"name":"get_weather","args":{"city":"Pune"}}}]}}]}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("weather?")},
		Tools: []Tool{{
			Name:        "get_weather",
			Description: "look up weather",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"city":{"type":"string"}}}`),
		}},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	var start *ToolUseStartEvent
	var end *ToolUseEndEvent
	for i := range collected {
		switch e := collected[i].(type) {
		case ToolUseStartEvent:
			start = &e
		case ToolUseEndEvent:
			end = &e
		}
	}
	require.NotNil(t, start, "expected a tool-use start event")
	require.NotNil(t, end, "expected a tool-use end event")
	require.Equal(t, "get_weather", start.Name)
	require.Equal(t, start.ID, end.ID, "start and end must share an id")
	require.JSONEq(t, `{"city":"Pune"}`, string(end.Input))
}

func TestGeminiRequestWireFormat(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	// A round trip: user asks, model calls a tool, tool replies, user follows up.
	history := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "weather?"}}},
		{Role: message.RoleAssistant, Content: []message.ContentBlock{
			message.ToolUseBlock{ID: "abc", Name: "get_weather", Input: json.RawMessage(`{"city":"Pune"}`)},
		}},
		{Role: message.RoleTool, Content: []message.ContentBlock{
			message.ToolResultBlock{ToolUseID: "abc", Content: "32C and sunny"},
		}},
	}

	events, err := provider.Stream(context.Background(), Request{
		Model:        "test-model",
		Messages:     history,
		SystemPrompt: "be brief",
		Tools:        []Tool{{Name: "get_weather", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody)
	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))

	// System prompt rides as system_instruction, not in contents.
	require.NotNil(t, captured.SystemInstruction)
	require.Equal(t, "be brief", captured.SystemInstruction.Parts[0].Text)

	require.Len(t, captured.Contents, 3)
	// Assistant turn maps to role "model" carrying a functionCall part.
	require.Equal(t, "model", captured.Contents[1].Role)
	require.NotNil(t, captured.Contents[1].Parts[0].FunctionCall)
	require.Equal(t, "get_weather", captured.Contents[1].Parts[0].FunctionCall.Name)

	// Tool result maps to a user-role functionResponse keyed by the call's name
	// (resolved from the tool-use id), with the string wrapped into an object.
	require.Equal(t, "user", captured.Contents[2].Role)
	fr := captured.Contents[2].Parts[0].FunctionResponse
	require.NotNil(t, fr)
	require.Equal(t, "get_weather", fr.Name)
	require.JSONEq(t, `{"result":"32C and sunny"}`, string(fr.Response))

	// Tools are declared under function_declarations.
	require.Len(t, captured.Tools, 1)
	require.Len(t, captured.Tools[0].FunctionDeclarations, 1)
	require.Equal(t, "get_weather", captured.Tools[0].FunctionDeclarations[0].Name)
}

func TestGeminiConvertsImageBlockToInlineData(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := visionConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Len(t, captured.Contents, 1)

	var inline *geminiInlineData
	for i := range captured.Contents[0].Parts {
		if captured.Contents[0].Parts[i].InlineData != nil {
			inline = captured.Contents[0].Parts[i].InlineData
		}
	}
	require.NotNil(t, inline, "request must carry an inline_data image part")
	require.Equal(t, "image/png", inline.MimeType)
	require.Equal(t, base64.StdEncoding.EncodeToString(pngImageBytes), inline.Data)
}

func TestGeminiRejectsImageForNonVisionModel(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Fatal("non-vision model must not reach the provider")
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	_, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{imageMessage()},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}

func TestGeminiClassifiesStreamErrorAsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(
			`{"error":{"code":429,"status":"RESOURCE_EXHAUSTED","message":"quota exceeded"}}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	var streamErr error
	for _, ev := range collected {
		if e, ok := ev.(ErrorEvent); ok {
			streamErr = e.Err
		}
	}
	require.Error(t, streamErr)
	require.ErrorIs(t, streamErr, ErrRateLimit)
}

func TestGeminiSurfacesBlockingFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A safety-filtered response streams the partial text it managed to
		// produce and then reports finishReason SAFETY with no error object.
		_, _ = io.WriteString(w, geminiSSE(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"partial"}]},"finishReason":"SAFETY"}]}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	var text strings.Builder
	var streamErr error
	for _, ev := range collected {
		switch e := ev.(type) {
		case DeltaTextEvent:
			text.WriteString(e.Text)
		case ErrorEvent:
			streamErr = e.Err
		}
	}
	require.Equal(t, "partial", text.String(), "text emitted before the block must still surface")
	require.Error(t, streamErr, "a SAFETY finishReason must end the turn with an error")
	require.Contains(t, streamErr.Error(), "SAFETY")
}

func TestGeminiAllowsMaxTokensFinishReason(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// MAX_TOKENS is a normal completion (the output cap was reached), not a
		// block, so it must not be turned into a stream error.
		_, _ = io.WriteString(w, geminiSSE(
			`{"candidates":[{"content":{"role":"model","parts":[{"text":"done"}]},"finishReason":"MAX_TOKENS"}]}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	for _, ev := range collected {
		_, isErr := ev.(ErrorEvent)
		require.False(t, isErr, "MAX_TOKENS must not produce a terminal error")
	}
}

func TestGeminiSurfacesPromptBlock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// A prompt rejected up front returns no candidates, only a
		// promptFeedback.blockReason.
		_, _ = io.WriteString(w, geminiSSE(
			`{"promptFeedback":{"blockReason":"BLOCKLIST"}}`,
		))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	collected := collectEvents(events)

	var streamErr error
	for _, ev := range collected {
		if e, ok := ev.(ErrorEvent); ok {
			streamErr = e.Err
		}
	}
	require.Error(t, streamErr, "a blocked prompt must surface as a stream error")
	require.Contains(t, streamErr.Error(), "BLOCKLIST")
}

func TestGeminiToolResponsePassesThroughJSONObject(t *testing.T) {
	// A JSON-object result is forwarded unchanged; a bare string is wrapped.
	obj := geminiToolResponse(`{"temp":32}`, false)
	require.JSONEq(t, `{"temp":32}`, string(obj))

	wrapped := geminiToolResponse("plain text", false)
	require.JSONEq(t, `{"result":"plain text"}`, string(wrapped))

	errResp := geminiToolResponse("boom", true)
	require.JSONEq(t, `{"error":"boom"}`, string(errResp))
}

// geminiThinkingConfigFor builds a gemini provider whose single model carries
// the given id, used to exercise the model-id gate on thinkingConfig.
func geminiThinkingConfigFor(t *testing.T, modelID, baseURL string) *config.Config {
	t.Helper()
	return &config.Config{
		Providers: []config.Provider{{
			Name:    "gemini",
			Type:    config.ProviderGemini,
			BaseURL: baseURL,
			Models:  []string{modelID},
		}},
		Models: []config.Model{{
			ID:            modelID,
			Provider:      "gemini",
			ContextWindow: 1000000,
			SupportsTools: true,
		}},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}
}

func TestGeminiEmitsThinkingConfigForSupportedModel(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gemini-2.5-flash",
		Messages: []message.Message{textMsg("think")},
		Thinking: &ThinkingConfig{BudgetTokens: 2048},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig, "thinking budget must produce a generationConfig")
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "supported model must carry a thinkingConfig")
	require.True(t, tc.IncludeThoughts, "thinking must request thought summaries")
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, 2048, *tc.ThinkingBudget)
}

func TestGeminiOmitsThinkingConfigForUnsupportedModel(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	// gemini-2.0-flash predates thinkingConfig; the budget must be dropped rather
	// than sent (which the API would reject).
	cfg := geminiThinkingConfigFor(t, "gemini-2.0-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "gemini-2.0-flash",
		Messages: []message.Message{textMsg("think")},
		Thinking: &ThinkingConfig{BudgetTokens: 2048},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	if captured.GenerationConfig != nil {
		require.Nil(t, captured.GenerationConfig.ThinkingConfig, "unsupported model must not carry a thinkingConfig")
	}
}

func TestGeminiCountTokensUsesNativeEndpoint(t *testing.T) {
	t.Setenv("GEMINI_TEST_KEY", "secret")

	var gotPath, gotKey string
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotKey = r.Header.Get("x-goog-api-key")
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"totalTokens":42,"totalBillableCharacters":99}`)
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	cfg.Providers[0].APIKeyEnv = "GEMINI_TEST_KEY"
	provider := geminiProviderFor(t, cfg)

	counter, ok := provider.(TokenCounter)
	require.True(t, ok, "gemini provider must satisfy TokenCounter")

	n, err := counter.CountTokens(context.Background(), Request{
		Model:        "test-model",
		Messages:     []message.Message{textMsg("count me")},
		SystemPrompt: "be brief",
		Tools:        []Tool{{Name: "get_weather", InputSchema: json.RawMessage(`{"type":"object"}`)}},
	})
	require.NoError(t, err)
	require.Equal(t, 42, n)

	// The native countTokens method and the request key both ride through.
	require.Equal(t, "/models/test-model:countTokens", gotPath)
	require.Equal(t, "secret", gotKey)

	// The body wraps a generateContentRequest carrying the fully qualified model
	// resource name plus the same contents, system instruction, and tools the
	// stream path would send (so the count reflects the real prompt).
	var captured geminiCountTokensRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Equal(t, "models/test-model", captured.GenerateContentRequest.Model)
	require.Len(t, captured.GenerateContentRequest.Contents, 1)
	require.NotNil(t, captured.GenerateContentRequest.SystemInstruction)
	require.Equal(t, "be brief", captured.GenerateContentRequest.SystemInstruction.Parts[0].Text)
	require.Len(t, captured.GenerateContentRequest.Tools, 1)
	// generationConfig is irrelevant to counting and must be dropped.
	require.Nil(t, captured.GenerateContentRequest.GenerationConfig)
}

func TestGeminiCountTokensRequiresAPIKey(t *testing.T) {
	t.Setenv("GEMINI_TEST_KEY", "")

	cfg := testConfig("gemini", config.ProviderGemini, "http://example.invalid")
	cfg.Providers[0].APIKeyEnv = "GEMINI_TEST_KEY"
	provider := geminiProviderFor(t, cfg)

	counter, ok := provider.(TokenCounter)
	require.True(t, ok)

	_, err := counter.CountTokens(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.ErrorIs(t, err, ErrAuth)
}

func textMsg(text string) message.Message {
	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}
