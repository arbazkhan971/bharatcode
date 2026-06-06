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
	// Gemini's promptTokenCount (7) includes the 2 cached tokens, but the ledger
	// prices InputTokens and CacheReadTokens additively, so InputTokens must carry
	// only the non-cached portion (7 - 2 = 5) to avoid billing the cached tokens
	// twice.
	require.Equal(t, 5, usage.InputTokens)
	require.Equal(t, 3, usage.OutputTokens)
	require.Equal(t, 2, usage.CacheReadTokens)
}

func TestGeminiUsageExcludesCachedTokensFromInput(t *testing.T) {
	cases := []struct {
		name      string
		meta      geminiUsageMetadata
		wantInput int
		wantCache int
	}{
		{
			name:      "subtracts cached portion from prompt total",
			meta:      geminiUsageMetadata{PromptTokenCount: 100, CachedContentTokenCount: 30, CandidatesTokenCount: 12},
			wantInput: 70,
			wantCache: 30,
		},
		{
			name:      "no cache leaves input untouched",
			meta:      geminiUsageMetadata{PromptTokenCount: 40, CandidatesTokenCount: 9},
			wantInput: 40,
			wantCache: 0,
		},
		{
			name:      "clamps at zero when cached exceeds prompt total",
			meta:      geminiUsageMetadata{PromptTokenCount: 5, CachedContentTokenCount: 9},
			wantInput: 0,
			wantCache: 9,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := tc.meta.toUsage()
			require.Equal(t, tc.wantInput, usage.InputTokens)
			require.Equal(t, tc.wantCache, usage.CacheReadTokens)
		})
	}
}

// TestGeminiUsageReasoningTokensSurfaced checks that geminiUsageMetadata.toUsage
// populates ReasoningTokens from ThoughtsTokenCount while keeping OutputTokens
// equal to the billing total (candidates + thoughts).
func TestGeminiUsageReasoningTokensSurfaced(t *testing.T) {
	cases := []struct {
		name          string
		meta          geminiUsageMetadata
		wantOutput    int
		wantReasoning int
	}{
		{
			name:          "thinking model: thoughts folded into output and surfaced as reasoning",
			meta:          geminiUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 5, ThoughtsTokenCount: 30},
			wantOutput:    35,
			wantReasoning: 30,
		},
		{
			name:          "non-thinking model: zero reasoning tokens",
			meta:          geminiUsageMetadata{PromptTokenCount: 10, CandidatesTokenCount: 8},
			wantOutput:    8,
			wantReasoning: 0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			usage := tc.meta.toUsage()
			require.Equal(t, tc.wantOutput, usage.OutputTokens)
			require.Equal(t, tc.wantReasoning, usage.ReasoningTokens)
		})
	}
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
	// ReasoningTokens surfaces the thinking breakdown without double-billing.
	require.Equal(t, 20, usage.ReasoningTokens)
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

func TestGeminiRelaxesSafetySettings(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))

	// Every scored harm category rides as BLOCK_NONE so legitimate engineering
	// content is not dropped with a SAFETY finishReason.
	require.NotEmpty(t, captured.SafetySettings, "request must carry relaxed safety settings")
	thresholds := make(map[string]string, len(captured.SafetySettings))
	for _, s := range captured.SafetySettings {
		thresholds[s.Category] = s.Threshold
	}
	for _, category := range []string{
		"HARM_CATEGORY_HARASSMENT",
		"HARM_CATEGORY_HATE_SPEECH",
		"HARM_CATEGORY_SEXUALLY_EXPLICIT",
		"HARM_CATEGORY_DANGEROUS_CONTENT",
	} {
		require.Equal(t, "BLOCK_NONE", thresholds[category], "category %s must be relaxed", category)
	}
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

func TestGeminiClassifiesTerminalStreamErrors(t *testing.T) {
	cases := []struct {
		name    string
		chunk   string
		wantErr error
	}{
		{
			// A bad or missing key surfaces mid-stream as UNAUTHENTICATED; it must
			// map to ErrAuth so the caller reports a credential failure rather than
			// a generic, retried error.
			name:    "unauthenticated maps to auth",
			chunk:   `{"error":{"code":401,"status":"UNAUTHENTICATED","message":"API key not valid"}}`,
			wantErr: ErrAuth,
		},
		{
			// A project without access to the model arrives as PERMISSION_DENIED,
			// which is likewise an auth condition.
			name:    "permission denied maps to auth",
			chunk:   `{"error":{"code":403,"status":"PERMISSION_DENIED","message":"caller does not have permission"}}`,
			wantErr: ErrAuth,
		},
		{
			// An unknown model id is reported as NOT_FOUND, matching the pre-stream
			// HTTP classification of a 404.
			name:    "not found maps to model not found",
			chunk:   `{"error":{"code":404,"status":"NOT_FOUND","message":"models/test-model is not found"}}`,
			wantErr: ErrModelNotFound,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, geminiSSE(tc.chunk))
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
			require.ErrorIs(t, streamErr, tc.wantErr)
		})
	}
}

func TestGeminiClassifiesContextOverflowStreamError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		// An over-budget prompt comes back mid-stream as INVALID_ARGUMENT whose
		// message names the token overflow; it must map to ErrContextLimit so the
		// compaction path can recover rather than fail the turn outright.
		_, _ = io.WriteString(w, geminiSSE(
			`{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"The input token count (1290224) exceeds the maximum number of tokens allowed (1048575)."}}`,
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
	require.ErrorIs(t, streamErr, ErrContextLimit)
}

func TestGeminiClassifiesContextOverflowHTTPError(t *testing.T) {
	stubSleep(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Gemini rejects an over-budget prompt before streaming with a 400 whose
		// body uses its own token-overflow wording, not the OpenAI/Anthropic
		// "context length" phrasing.
		w.WriteHeader(http.StatusBadRequest)
		_, _ = io.WriteString(w, `{"error":{"code":400,"status":"INVALID_ARGUMENT","message":"The input token count (1290224) exceeds the maximum number of tokens allowed (1048575)."}}`)
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	_, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
	})
	require.ErrorIs(t, err, ErrContextLimit)
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

func TestGeminiDerivesThinkingBudgetFromReasoningEffort(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// No explicit Thinking budget: the configured reasoning_effort must drive
	// thinkingConfig so a Gemini user gets parity with the OpenAI effort knob.
	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-flash",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig, "reasoning_effort must produce a generationConfig")
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "reasoning_effort must produce a thinkingConfig on a supported model")
	require.True(t, tc.IncludeThoughts)
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, 16384, *tc.ThinkingBudget, "high effort must map to the high budget")
}

func TestGeminiThinkingBudgetWinsOverReasoningEffort(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// An explicit budget takes precedence over the effort-derived one.
	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-flash",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "low",
		Thinking:        &ThinkingConfig{BudgetTokens: 2048},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig)
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, 2048, *tc.ThinkingBudget, "explicit budget must win over effort")
}

func TestGeminiMinimalEffortSendsSmallBudget(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// "minimal" must configure a real (small) thinkingBudget rather than falling
	// through to 0, which would leave thinkingConfig off and let the model's far
	// larger default thinking apply — the opposite of the requested intent.
	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-flash",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "minimal",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig)
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "minimal effort must still configure thinkingConfig")
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, geminiMinimalThinkingBudget, *tc.ThinkingBudget)
}

func TestGeminiOmitsThinkingConfigForEffortOnUnsupportedModel(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	// gemini-2.0-flash predates thinkingConfig; an effort label must not smuggle
	// a thinkingConfig onto it any more than an explicit budget would.
	cfg := geminiThinkingConfigFor(t, "gemini-2.0-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.0-flash",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	if captured.GenerationConfig != nil {
		require.Nil(t, captured.GenerationConfig.ThinkingConfig, "unsupported model must not carry a thinkingConfig")
	}
}

func TestGeminiNoneEffortDisablesThinkingOnFlash(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// "none" must turn reasoning off on the Flash line by pinning thinkingBudget to
	// 0 — not fall through to "unconfigured", which would leave the model's default
	// thinking on, the opposite of the requested intent.
	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-flash",
		Messages:        []message.Message{textMsg("answer")},
		ReasoningEffort: "none",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig, "none must produce a generationConfig that disables thinking")
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "none must configure thinkingConfig to disable thinking")
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, 0, *tc.ThinkingBudget, "none must pin the thinkingBudget to 0 to disable thinking")
	require.False(t, tc.IncludeThoughts, "a disabled thinking pass must not request thought summaries")
}

func TestGeminiNoneEffortLeavesProDefault(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	// Gemini 2.5 Pro cannot disable thinking (its budget floors at 128 and a 0 is a
	// 400), so "none" must degrade to the model's default rather than smuggle a 0
	// budget onto it.
	cfg := geminiThinkingConfigFor(t, "gemini-2.5-pro", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-pro",
		Messages:        []message.Message{textMsg("answer")},
		ReasoningEffort: "none",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	if captured.GenerationConfig != nil {
		require.Nil(t, captured.GenerationConfig.ThinkingConfig, "Pro must not carry a 0 thinkingBudget, which it 400s on")
	}
}

func TestGeminiThinkingBudgetForEffort(t *testing.T) {
	require.Equal(t, 0, geminiThinkingBudgetForEffort(""))
	require.Equal(t, 0, geminiThinkingBudgetForEffort("bogus"))
	require.Equal(t, geminiMinimalThinkingBudget, geminiThinkingBudgetForEffort("minimal"), "minimal maps to the smallest valid 2.5 budget")
	require.Equal(t, geminiMinimalThinkingBudget, geminiThinkingBudgetForEffort("MINIMAL"), "minimal match must be case-insensitive")
	require.Greater(t, geminiMinimalThinkingBudget, 128, "minimal budget must clear the Gemini 2.5 Pro floor")
	require.Less(t, geminiMinimalThinkingBudget, 4096, "minimal budget must sit below the low budget")
	require.Equal(t, 4096, geminiThinkingBudgetForEffort("low"))
	require.Equal(t, 8192, geminiThinkingBudgetForEffort("medium"))
	require.Equal(t, 16384, geminiThinkingBudgetForEffort("HIGH"), "match must be case-insensitive")
	require.Equal(t, -1, geminiThinkingBudgetForEffort("auto"), "auto must select dynamic thinking")
	require.Equal(t, -1, geminiThinkingBudgetForEffort("Dynamic"), "dynamic must select dynamic thinking, case-insensitive")
	require.Equal(t, geminiThinkingDisabled, geminiThinkingBudgetForEffort("none"), "none must map to the disable-thinking sentinel, not 0")
	require.Equal(t, geminiThinkingDisabled, geminiThinkingBudgetForEffort("NONE"), "none match must be case-insensitive")
	require.NotEqual(t, 0, geminiThinkingDisabled, "the disable sentinel must differ from the unconfigured 0")
}

func TestGeminiModelCanDisableThinking(t *testing.T) {
	// The Gemini 2.5 Flash line (and its rolling aliases) accepts a 0 budget.
	require.True(t, geminiModelCanDisableThinking("gemini-2.5-flash"))
	require.True(t, geminiModelCanDisableThinking("gemini-2.5-flash-lite"))
	require.True(t, geminiModelCanDisableThinking("GEMINI-2.5-FLASH"), "match must be case-insensitive")
	require.True(t, geminiModelCanDisableThinking("gemini-flash-latest"))
	require.True(t, geminiModelCanDisableThinking("gemini-flash-lite-latest"))
	// Gemini 2.5 Pro cannot disable thinking (its budget floors at 128).
	require.False(t, geminiModelCanDisableThinking("gemini-2.5-pro"))
	require.False(t, geminiModelCanDisableThinking("gemini-pro-latest"))
	// Gemini 3 uses thinkingLevel, not a numeric budget, so it is not matched.
	require.False(t, geminiModelCanDisableThinking("gemini-3-pro-preview"))
	// A flash model on an older generation that predates thinkingConfig.
	require.False(t, geminiModelCanDisableThinking("gemini-2.0-flash"))
}

func TestGeminiThinkingLevelForEffort(t *testing.T) {
	require.Equal(t, "", geminiThinkingLevelForEffort(""))
	require.Equal(t, "", geminiThinkingLevelForEffort("bogus"))
	require.Equal(t, "low", geminiThinkingLevelForEffort("low"))
	require.Equal(t, "low", geminiThinkingLevelForEffort("minimal"), "minimal clamps to the universally-supported low level")
	require.Equal(t, "high", geminiThinkingLevelForEffort("medium"), "medium clamps up to high so the base Gemini 3 Pro never 400s")
	require.Equal(t, "high", geminiThinkingLevelForEffort("HIGH"), "match must be case-insensitive")
	require.Equal(t, "", geminiThinkingLevelForEffort("auto"), "dynamic thinking has no level equivalent")
}

// TestGeminiEmitsThinkingLevelForGemini3 asserts a Gemini 3 model carries a
// thinkingLevel (the Gemini 3 reasoning knob) derived from reasoning_effort, and
// never the legacy thinkingBudget, which Gemini 3 rejects with a 400.
func TestGeminiEmitsThinkingLevelForGemini3(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-3-pro-preview", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-3-pro-preview",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "high",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig, "reasoning_effort must produce a generationConfig")
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "a Gemini 3 model must carry a thinkingConfig")
	require.True(t, tc.IncludeThoughts)
	require.Equal(t, "high", tc.ThinkingLevel, "high effort must map to the high thinkingLevel")
	require.Nil(t, tc.ThinkingBudget, "Gemini 3 must not carry a thinkingBudget (it 400s)")

	// The serialized body must omit thinkingBudget entirely: sending both
	// thinkingLevel and thinkingBudget is itself a 400.
	require.NotContains(t, string(rawBody), "thinkingBudget")
	require.Contains(t, string(rawBody), `"thinkingLevel":"high"`)
}

// TestGeminiThinkingLevelFromBudgetForGemini3 asserts a numeric thinking budget
// configured against a Gemini 3 model is bucketed into a thinkingLevel rather
// than sent as a (rejected) thinkingBudget, with the explicit budget winning over
// an effort label as it does on the Gemini 2.5 path.
func TestGeminiThinkingLevelFromBudgetForGemini3(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-3-pro-preview", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-3-pro-preview",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "high",
		Thinking:        &ThinkingConfig{BudgetTokens: 2048},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig)
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc)
	require.Equal(t, "low", tc.ThinkingLevel, "a 2048-token budget buckets to the low level and wins over effort")
	require.Nil(t, tc.ThinkingBudget)
}

// TestGeminiDynamicThinkingOmitsLevelForGemini3 asserts a dynamic-thinking
// request ("auto") leaves a Gemini 3 model's thinkingConfig off entirely: there
// is no thinkingLevel equivalent of the 2.5-era dynamic sentinel.
func TestGeminiDynamicThinkingOmitsLevelForGemini3(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-3-pro-preview", server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-3-pro-preview",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "auto",
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	if captured.GenerationConfig != nil {
		require.Nil(t, captured.GenerationConfig.ThinkingConfig, "dynamic thinking must not pin a Gemini 3 thinkingLevel")
	}
}

func TestGeminiDynamicThinkingBudgetFromReasoningEffort(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// "auto" maps to Gemini's dynamic-thinking sentinel (-1): thinkingConfig must
	// be emitted with a -1 budget so the model sizes its own reasoning, and the
	// caller's maxOutputTokens cap must be left untouched (there is no fixed
	// budget to reserve room beyond).
	events, err := provider.Stream(context.Background(), Request{
		Model:           "gemini-2.5-flash",
		Messages:        []message.Message{textMsg("think")},
		ReasoningEffort: "auto",
		MaxTokens:       1000,
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig, "auto effort must produce a generationConfig")
	tc := captured.GenerationConfig.ThinkingConfig
	require.NotNil(t, tc, "auto effort must produce a thinkingConfig on a supported model")
	require.True(t, tc.IncludeThoughts)
	require.NotNil(t, tc.ThinkingBudget)
	require.Equal(t, -1, *tc.ThinkingBudget, "auto must send the dynamic-thinking sentinel")
	require.Equal(t, 1000, captured.GenerationConfig.MaxOutputTokens,
		"dynamic thinking must not lift the maxOutputTokens cap")
}

func TestGeminiLiftsMaxOutputTokensAboveThinkingBudget(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// A cap below the thinking budget would leave no room for a visible answer.
	events, err := provider.Stream(context.Background(), Request{
		Model:     "gemini-2.5-flash",
		Messages:  []message.Message{textMsg("think")},
		MaxTokens: 1000,
		Thinking:  &ThinkingConfig{BudgetTokens: 4096},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig)
	require.Equal(t, 4096+defaultGeminiMaxTokens, captured.GenerationConfig.MaxOutputTokens,
		"maxOutputTokens must be lifted above the thinking budget")
}

func TestGeminiKeepsMaxOutputTokensAboveThinkingBudget(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := geminiThinkingConfigFor(t, "gemini-2.5-flash", server.URL)
	provider := geminiProviderFor(t, cfg)

	// A cap already comfortably above the budget is left untouched.
	events, err := provider.Stream(context.Background(), Request{
		Model:     "gemini-2.5-flash",
		Messages:  []message.Message{textMsg("think")},
		MaxTokens: 16384,
		Thinking:  &ThinkingConfig{BudgetTokens: 4096},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.NotNil(t, captured.GenerationConfig)
	require.Equal(t, 16384, captured.GenerationConfig.MaxOutputTokens,
		"an explicit cap above the budget must be preserved")
}

// TestGeminiDefaultMaxOutputTokensIsModelAware verifies that a request leaving
// MaxTokens unset receives a model-aware maxOutputTokens default rather than
// relying on the Gemini API's conservative 8192-token fallback. Long responses
// from the Gemini 2.5 family (which supports 65536 output tokens) would
// otherwise be silently truncated.
func TestGeminiDefaultMaxOutputTokensIsModelAware(t *testing.T) {
	cases := []struct {
		name          string
		modelID       string
		reqMaxTokens  int
		wantMaxOutput int
	}{
		{
			name:          "gemini-2.5-flash defaults to 65536",
			modelID:       "gemini-2.5-flash",
			reqMaxTokens:  0,
			wantMaxOutput: 65_536,
		},
		{
			name:          "gemini-2.5-flash-lite defaults to 32768",
			modelID:       "gemini-2.5-flash-lite",
			reqMaxTokens:  0,
			wantMaxOutput: 32_768,
		},
		{
			name:          "explicit MaxTokens always wins",
			modelID:       "gemini-2.5-flash",
			reqMaxTokens:  1000,
			wantMaxOutput: 1000,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var rawBody []byte
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				rawBody, _ = io.ReadAll(r.Body)
				w.Header().Set("Content-Type", "text/event-stream")
				_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
			}))
			defer server.Close()

			cfg := geminiThinkingConfigFor(t, tc.modelID, server.URL)
			provider := geminiProviderFor(t, cfg)

			events, err := provider.Stream(context.Background(), Request{
				Model:     tc.modelID,
				Messages:  []message.Message{textMsg("hello")},
				MaxTokens: tc.reqMaxTokens,
			})
			require.NoError(t, err)
			_ = collectEvents(events)

			var captured geminiRequest
			require.NoError(t, json.Unmarshal(rawBody, &captured))
			require.NotNil(t, captured.GenerationConfig,
				"GenerationConfig must always be set for maxOutputTokens")
			require.Equal(t, tc.wantMaxOutput, captured.GenerationConfig.MaxOutputTokens,
				"maxOutputTokens mismatch for model %q", tc.modelID)
		})
	}
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

func TestSanitizeGeminiSchemaStripsUnsupportedKeys(t *testing.T) {
	// A schema as a typical JSON Schema generator emits it: a top-level "$schema"
	// and an "additionalProperties": false, plus the same keys buried in a nested
	// object property and inside an anyOf branch. Gemini rejects every one.
	raw := json.RawMessage(`{
		"$schema": "https://json-schema.org/draft/2020-12/schema",
		"$id": "urn:tool",
		"type": "object",
		"additionalProperties": false,
		"properties": {
			"city": {"type": "string", "description": "city name"},
			"opts": {
				"type": "object",
				"additionalProperties": false,
				"patternProperties": {"^x-": {"type": "string"}},
				"properties": {"unit": {"type": "string"}}
			},
			"either": {
				"anyOf": [
					{"type": "string"},
					{"type": "object", "additionalProperties": false, "properties": {"n": {"type": "number"}}}
				]
			}
		},
		"required": ["city"]
	}`)

	got := sanitizeGeminiSchema(raw)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(got, &decoded))

	// Top-level metadata and constraint keys are gone...
	require.NotContains(t, decoded, "$schema")
	require.NotContains(t, decoded, "$id")
	require.NotContains(t, decoded, "additionalProperties")

	// ...while the supported structure is preserved verbatim.
	require.Equal(t, "object", decoded["type"])
	require.Equal(t, []any{"city"}, decoded["required"])
	props := decoded["properties"].(map[string]any)
	require.Contains(t, props, "city")

	// Nested object: unsupported keys stripped, real properties kept.
	opts := props["opts"].(map[string]any)
	require.NotContains(t, opts, "additionalProperties")
	require.NotContains(t, opts, "patternProperties")
	require.Contains(t, opts["properties"].(map[string]any), "unit")

	// anyOf branch is recursed into as well.
	branches := props["either"].(map[string]any)["anyOf"].([]any)
	require.NotContains(t, branches[1].(map[string]any), "additionalProperties")
	require.Contains(t, branches[1].(map[string]any)["properties"].(map[string]any), "n")
}

func TestSanitizeGeminiSchemaDropsUnsupportedStringFormats(t *testing.T) {
	// Gemini's Schema accepts only "enum" and "date-time" as the "format" of a
	// STRING property; the other JSON Schema string formats generators routinely
	// emit ("uri", "email", "uuid", ...) 400 the whole request. They must be
	// dropped, while the two supported string formats and all number/integer
	// formats survive.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"homepage": {"type": "string", "format": "uri"},
			"contact": {"type": "string", "format": "email"},
			"created": {"type": "string", "format": "date-time"},
			"choice": {"type": "string", "format": "enum", "enum": ["a", "b"]},
			"count": {"type": "integer", "format": "int64"},
			"ratio": {"type": "number", "format": "double"},
			"nested": {
				"type": "object",
				"properties": {"id": {"type": "string", "format": "uuid"}}
			}
		}
	}`)

	got := sanitizeGeminiSchema(raw)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(got, &decoded))
	props := decoded["properties"].(map[string]any)

	// Unsupported string formats are gone, but the property itself stays.
	require.NotContains(t, props["homepage"].(map[string]any), "format")
	require.Equal(t, "string", props["homepage"].(map[string]any)["type"])
	require.NotContains(t, props["contact"].(map[string]any), "format")

	// Supported string formats survive unchanged.
	require.Equal(t, "date-time", props["created"].(map[string]any)["format"])
	require.Equal(t, "enum", props["choice"].(map[string]any)["format"])

	// Numeric formats live on non-string nodes and are left untouched.
	require.Equal(t, "int64", props["count"].(map[string]any)["format"])
	require.Equal(t, "double", props["ratio"].(map[string]any)["format"])

	// The strip recurses into nested objects.
	nestedID := props["nested"].(map[string]any)["properties"].(map[string]any)["id"].(map[string]any)
	require.NotContains(t, nestedID, "format")
}

func TestSanitizeGeminiSchemaInlinesRefs(t *testing.T) {
	// A schema as generators commonly emit it: a shared object factored into
	// "$defs" and referenced by a local "#/$defs/Name" pointer, including from
	// inside an array's "items" and referenced twice. Gemini rejects $ref/$defs,
	// so the sanitizer must inline the target and drop the containers.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"home": {"$ref": "#/$defs/Address"},
			"stops": {"type": "array", "items": {"$ref": "#/$defs/Address"}}
		},
		"$defs": {
			"Address": {
				"type": "object",
				"additionalProperties": false,
				"properties": {"city": {"type": "string"}}
			}
		}
	}`)

	got := sanitizeGeminiSchema(raw)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(got, &decoded))

	// The definition container is gone and no $ref survives anywhere.
	require.NotContains(t, decoded, "$defs")
	require.NotContains(t, string(got), "$ref")

	props := decoded["properties"].(map[string]any)

	// Both reference sites are inlined to a full copy of Address, and the
	// unsupported "additionalProperties" carried by the definition is stripped.
	home := props["home"].(map[string]any)
	require.Equal(t, "object", home["type"])
	require.NotContains(t, home, "additionalProperties")
	require.Contains(t, home["properties"].(map[string]any), "city")

	item := props["stops"].(map[string]any)["items"].(map[string]any)
	require.Contains(t, item["properties"].(map[string]any), "city")

	// The two inlined copies are independent values, not a shared map.
	require.NotSame(t, &home, &item)
}

func TestSanitizeGeminiSchemaInlinesLegacyDefinitions(t *testing.T) {
	// The legacy draft-07 "definitions" container is resolved just like "$defs".
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"id": {"$ref": "#/definitions/Id"}},
		"definitions": {"Id": {"type": "string"}}
	}`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sanitizeGeminiSchema(raw), &decoded))

	require.NotContains(t, decoded, "definitions")
	id := decoded["properties"].(map[string]any)["id"].(map[string]any)
	require.Equal(t, "string", id["type"])
}

func TestSanitizeGeminiSchemaCollapsesRecursiveRef(t *testing.T) {
	// A self-referential definition cannot be inlined into a finite tree, so it
	// collapses to a permissive object and resolution still terminates.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"node": {"$ref": "#/$defs/Node"}},
		"$defs": {
			"Node": {
				"type": "object",
				"properties": {"child": {"$ref": "#/$defs/Node"}}
			}
		}
	}`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sanitizeGeminiSchema(raw), &decoded))

	require.NotContains(t, decoded, "$defs")
	node := decoded["properties"].(map[string]any)["node"].(map[string]any)
	// One level of Node is inlined; its recursive "child" collapses to {"type":"object"}.
	child := node["properties"].(map[string]any)["child"].(map[string]any)
	require.Equal(t, "object", child["type"])
	require.NotContains(t, child, "properties")
}

func TestSanitizeGeminiSchemaCollapsesRecursiveRefIsOrderStable(t *testing.T) {
	// Regression: inlining once recursed into the live "$defs" container, mutating
	// the shared definition object in place. Because Go randomizes map iteration
	// order, a $ref consumer reached after the container would deep-copy an
	// already-expanded definition and the recursive child would keep a "properties"
	// level instead of collapsing. Sanitizing the same schema many times exercises
	// many iteration orders; every result must be identical and fully collapsed.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"node": {"$ref": "#/$defs/Node"}},
		"$defs": {
			"Node": {
				"type": "object",
				"properties": {"child": {"$ref": "#/$defs/Node"}}
			}
		}
	}`)

	for i := 0; i < 200; i++ {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(sanitizeGeminiSchema(raw), &decoded))
		node := decoded["properties"].(map[string]any)["node"].(map[string]any)
		child := node["properties"].(map[string]any)["child"].(map[string]any)
		require.Equalf(t, "object", child["type"], "iteration %d", i)
		require.NotContainsf(t, child, "properties", "iteration %d", i)
	}
}

func TestSanitizeGeminiSchemaSharedDefInlinesIndependently(t *testing.T) {
	// A single definition referenced from two sibling sites must inline a fresh
	// copy at each; the earlier aliasing bug let the first inline mutate the shared
	// definition so the second site (visited in a different map order) saw an
	// already-expanded copy. Both recursive children must collapse identically.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {
			"a": {"$ref": "#/$defs/Node"},
			"b": {"$ref": "#/$defs/Node"}
		},
		"$defs": {
			"Node": {
				"type": "object",
				"properties": {"child": {"$ref": "#/$defs/Node"}}
			}
		}
	}`)

	for i := 0; i < 200; i++ {
		var decoded map[string]any
		require.NoError(t, json.Unmarshal(sanitizeGeminiSchema(raw), &decoded))
		props := decoded["properties"].(map[string]any)
		for _, site := range []string{"a", "b"} {
			node := props[site].(map[string]any)
			child := node["properties"].(map[string]any)["child"].(map[string]any)
			require.Equalf(t, "object", child["type"], "site %q iteration %d", site, i)
			require.NotContainsf(t, child, "properties", "site %q iteration %d", site, i)
		}
	}
}

func TestSanitizeGeminiSchemaLeavesUnresolvableRef(t *testing.T) {
	// A reference with no matching definition is left untouched rather than
	// guessed at, so the sanitizer never makes a request worse than the input.
	raw := json.RawMessage(`{
		"type": "object",
		"properties": {"x": {"$ref": "#/$defs/Missing"}},
		"$defs": {"Other": {"type": "string"}}
	}`)

	var decoded map[string]any
	require.NoError(t, json.Unmarshal(sanitizeGeminiSchema(raw), &decoded))

	x := decoded["properties"].(map[string]any)["x"].(map[string]any)
	require.Equal(t, "#/$defs/Missing", x["$ref"])
}

func TestSanitizeGeminiSchemaPassesThroughInvalidAndEmpty(t *testing.T) {
	// A non-JSON schema is returned byte-for-byte rather than dropped, so the
	// sanitizer never makes a request worse than the raw passthrough it replaced.
	bad := json.RawMessage(`{not json`)
	require.Equal(t, string(bad), string(sanitizeGeminiSchema(bad)))

	// An empty schema stays empty so the caller's default-object fallback applies.
	require.Len(t, sanitizeGeminiSchema(nil), 0)
}

func TestGeminiRequestSanitizesToolSchema(t *testing.T) {
	var rawBody []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawBody, _ = io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, geminiSSE(`{"candidates":[{"content":{"role":"model","parts":[{"text":"ok"}]}}]}`))
	}))
	defer server.Close()

	cfg := testConfig("gemini", config.ProviderGemini, server.URL)
	provider := geminiProviderFor(t, cfg)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{textMsg("hi")},
		Tools: []Tool{{
			Name:        "get_weather",
			InputSchema: json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","additionalProperties":false,"properties":{"city":{"type":"string"}}}`),
		}},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.NotEmpty(t, rawBody)
	var captured geminiRequest
	require.NoError(t, json.Unmarshal(rawBody, &captured))
	require.Len(t, captured.Tools, 1)
	require.Len(t, captured.Tools[0].FunctionDeclarations, 1)

	var params map[string]any
	require.NoError(t, json.Unmarshal(captured.Tools[0].FunctionDeclarations[0].Parameters, &params))
	require.NotContains(t, params, "$schema")
	require.NotContains(t, params, "additionalProperties")
	require.Contains(t, params["properties"].(map[string]any), "city")
}

func textMsg(text string) message.Message {
	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}
