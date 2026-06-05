package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

func TestRegistryListsAndGetsProviders(t *testing.T) {
	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, "http://example.test/v1")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	provider, err := reg.Get("deepseek")
	require.NoError(t, err)
	require.Equal(t, "deepseek", provider.Name())
	require.True(t, provider.SupportsTools())
	require.False(t, provider.SupportsImages())

	models := reg.ListModels()
	require.Len(t, models, 1)
	require.Equal(t, "test-model", models[0].ID)

	_, err = reg.Get("missing")
	require.ErrorIs(t, err, ErrProviderNotFound)
}

func TestRegistryConcurrentAccess(t *testing.T) {
	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, "http://example.test/v1"))
	require.NoError(t, err)

	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, getErr := reg.Get("deepseek")
			require.NoError(t, getErr)
			require.Len(t, reg.ListModels(), 1)
		}()
	}
	wg.Wait()
}

func TestOpenAICompatibleStreamsTextToolThinkingAndUsage(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/v1/chat/completions", r.URL.Path)
		require.Equal(t, "Bearer test-key", r.Header.Get("Authorization"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"hello \"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"lookup\",\"arguments\":\"{\\\"q\\\":\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"bharat\\\"}\"}}]}}]}\n\n")
		fmt.Fprint(w, "data: {\"usage\":{\"prompt_tokens\":7,\"completion_tokens\":3}}\n\n")
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

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{
			{
				Role: message.RoleUser,
				Content: []message.ContentBlock{
					message.TextBlock{Text: "hi"},
				},
			},
		},
		Tools: []Tool{{
			Name:        "lookup",
			Description: "Looks up data.",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"q":{"type":"string"}}}`),
		}},
		SystemPrompt: "You are concise.",
	})
	require.NoError(t, err)

	got := collectEvents(events)
	require.IsType(t, StartEvent{}, got[0])
	require.Contains(t, got, DeltaTextEvent{Text: "hello "})
	require.Contains(t, got, ThinkingEvent{Text: "thinking"})
	require.Contains(t, got, ToolUseStartEvent{ID: "call_1", Name: "lookup"})
	require.Contains(t, got, ToolUseDeltaEvent{ID: "call_1", Delta: "{\"q\":"})
	require.Contains(t, got, ToolUseDeltaEvent{ID: "call_1", Delta: "\"bharat\"}"})
	require.Contains(t, got, ToolUseEndEvent{ID: "call_1", Name: "lookup", Input: json.RawMessage(`{"q":"bharat"}`)})
	require.Contains(t, got, EndEvent{Usage: Usage{InputTokens: 7, OutputTokens: 3}})
	require.Equal(t, "system", captured.Messages[0].Role)
	require.Equal(t, "You are concise.", captured.Messages[0].Content)
	require.True(t, captured.Stream)
	require.Len(t, captured.Tools, 1)
}

func TestOpenAICompatibleConvertsToolResultHistory(t *testing.T) {
	var captured openAIChatRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"done"}}],"usage":{"prompt_tokens":4,"completion_tokens":1}}`)
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("lmstudio", config.ProviderLMStudio, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("lmstudio")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{
			{
				Role: message.RoleAssistant,
				Content: []message.ContentBlock{
					message.ToolUseBlock{
						ID:    "call_1",
						Name:  "lookup",
						Input: json.RawMessage(`{"q":"x"}`),
					},
				},
			},
			{
				Role: message.RoleUser,
				Content: []message.ContentBlock{
					message.ToolResultBlock{
						ToolUseID: "call_1",
						Content:   "answer",
					},
				},
			},
		},
	})
	require.NoError(t, err)
	_ = collectEvents(events)

	require.Len(t, captured.Messages, 2)
	require.Equal(t, "assistant", captured.Messages[0].Role)
	require.Len(t, captured.Messages[0].ToolCalls, 1)
	require.Equal(t, "tool", captured.Messages[1].Role)
	require.Equal(t, "call_1", captured.Messages[1].ToolCallID)
}

func TestProviderErrorsAreTyped(t *testing.T) {
	// Retryable statuses (429/5xx) now flow through the backoff loop, so stub
	// the sleep to keep the test offline-fast instead of waiting real seconds.
	stubSleep(t)

	tests := []struct {
		name string
		code int
		body string
		want error
	}{
		{name: "auth", code: http.StatusUnauthorized, body: `{}`, want: ErrAuth},
		{name: "rate", code: http.StatusTooManyRequests, body: `{}`, want: ErrRateLimit},
		{name: "missing", code: http.StatusNotFound, body: `{}`, want: ErrModelNotFound},
		{name: "context", code: http.StatusBadRequest, body: `{"error":{"code":"context_length_exceeded"}}`, want: ErrContextLimit},
		{name: "server", code: http.StatusInternalServerError, body: `{}`, want: ErrServer},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.code)
				fmt.Fprint(w, tt.body)
			}))
			defer server.Close()

			reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
			require.NoError(t, err)
			provider, err := reg.Get("deepseek")
			require.NoError(t, err)

			_, err = provider.Stream(context.Background(), Request{Model: "test-model"})
			require.ErrorIs(t, err, tt.want)
		})
	}
}

func TestOpenAICompatibleRejectsUnsupportedTools(t *testing.T) {
	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, "http://example.test/v1")
	cfg.Models[0].SupportsTools = false
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{
		Model: "test-model",
		Tools: []Tool{{Name: "lookup"}},
	})
	require.ErrorIs(t, err, ErrUnsupportedFeature)
}

func TestOpenAICompatibleMissingAPIKey(t *testing.T) {
	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, "http://example.test/v1")
	cfg.Providers[0].APIKeyEnv = "MISSING_TEST_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	_, err = provider.Stream(context.Background(), Request{Model: "test-model"})
	require.ErrorIs(t, err, ErrAuth)
}

func TestOllamaStreamsJSONLines(t *testing.T) {
	var captured ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/chat", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		fmt.Fprintln(w, `{"message":{"content":"local "},"done":false}`)
		fmt.Fprintln(w, `{"message":{"tool_calls":[{"id":"call_local","function":{"name":"lookup","arguments":{"q":"x"}}}]},"done":false}`)
		fmt.Fprintln(w, `{"done":true,"prompt_eval_count":5,"eval_count":2}`)
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("ollama", config.ProviderOllama, server.URL))
	require.NoError(t, err)
	provider, err := reg.Get("ollama")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		Tools:    []Tool{{Name: "lookup"}},
	})
	require.NoError(t, err)

	got := collectEvents(events)
	require.Contains(t, got, DeltaTextEvent{Text: "local "})
	require.Contains(t, got, ToolUseStartEvent{ID: "call_local", Name: "lookup"})
	require.Contains(t, got, ToolUseEndEvent{ID: "call_local", Name: "lookup", Input: json.RawMessage(`{"q":"x"}`)})
	require.Contains(t, got, EndEvent{Usage: Usage{InputTokens: 5, OutputTokens: 2}})
	require.Equal(t, "test-model", captured.Model)
	require.Len(t, captured.Tools, 1)
}

func TestAnthropicProviderIsRegistered(t *testing.T) {
	cfg := testConfig("anthropic", config.ProviderAnthropic, "")
	// Point at a missing API key env so Stream fails before any network call,
	// keeping the test offline while still proving the registered provider is
	// the real Anthropic client rather than the not-yet-supported stub.
	cfg.Providers[0].APIKeyEnv = "MISSING_ANTHROPIC_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)
	require.Equal(t, "anthropic", provider.Name())
	require.True(t, provider.SupportsTools())

	_, err = provider.Stream(context.Background(), Request{Model: "test-model"})
	require.ErrorIs(t, err, ErrAuth)
	require.NotErrorIs(t, err, ErrNotYetSupported)
}

// TestNativeGeminiProviderIsRegistered proves a provider configured with the
// native "gemini" type resolves to the geminiProvider client (Google's
// generateContent dialect) rather than the openai_compatible shim, and that the
// configured Gemini 2.5 model is recognized as thinking-capable. Stream is
// pointed at a missing API-key env so it fails offline with ErrAuth before any
// network call, which also confirms the native path enforces the key.
func TestNativeGeminiProviderIsRegistered(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:      "gemini",
			Type:      config.ProviderGemini,
			APIKeyEnv: "MISSING_GEMINI_API_KEY",
			Models:    []string{"gemini-2.5-flash"},
		}},
		Models: []config.Model{{
			ID:             "gemini-2.5-flash",
			Provider:       "gemini",
			ContextWindow:  1000000,
			SupportsImages: true,
			SupportsTools:  true,
			ThinkingBudget: 8192,
		}},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	provider, err := reg.Get("gemini")
	require.NoError(t, err)
	_, ok := provider.(*geminiProvider)
	require.True(t, ok, "native gemini type should resolve to the geminiProvider client")
	require.Equal(t, "gemini", provider.Name())
	require.True(t, provider.SupportsTools())
	require.True(t, provider.SupportsImages())

	// The configured 2.5 model must be recognized as thinking-capable so the
	// native thinkingConfig is emitted for it.
	require.True(t, modelSupportsGeminiThinking(provider.Models(), "gemini-2.5-flash"))

	_, err = provider.Stream(context.Background(), Request{Model: "gemini-2.5-flash"})
	require.ErrorIs(t, err, ErrAuth)
	require.NotErrorIs(t, err, ErrNotYetSupported)
}

func TestStreamHonorsCancelledContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		if f, ok := w.(http.Flusher); ok {
			fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"a\"}}]}\n\n")
			f.Flush()
		}
		<-r.Context().Done()
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1"))
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Stream(ctx, Request{Model: "test-model"})
	require.NoError(t, err)
	require.IsType(t, StartEvent{}, <-events)
	require.IsType(t, DeltaTextEvent{}, <-events)
	cancel()

	require.Eventually(t, func() bool {
		for event := range events {
			if errEvent, ok := event.(ErrorEvent); ok {
				return errors.Is(errEvent.Err, context.Canceled)
			}
		}
		return true
	}, time.Second, 10*time.Millisecond)
}

// TestDefaultConfigBuildsHealthyRegistry asserts the embedded default
// config is healthy without coupling to specific provider names. It
// covers (a) the embedded defaults parse and validate, (b) building the
// registry from them registers every provider without error, and (c)
// every openai_compatible provider resolves to the openAICompatibleProvider
// client. This is name-agnostic on purpose: any newly added
// openai_compatible provider (google-gemini, xai, mistral, cerebras,
// perplexity, etc.) is covered the moment it lands in the default config,
// so the assertion neither hardcodes names nor races whoever edits the
// config. It is fully offline: Default() unmarshals embedded bytes and
// NewRegistry constructs structs, neither touches the network.
func TestDefaultConfigBuildsHealthyRegistry(t *testing.T) {
	cfg := config.Default()

	// (a) The embedded default config parses and validates.
	require.NoError(t, config.Validate(cfg))

	// (b) The provider registry builds from the default config.
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	// (c) Every openai_compatible provider resolves to the
	// openai_compatible client. Only assert the forward direction:
	// the openai and lmstudio types also construct
	// *openAICompatibleProvider, so an exclusive assertion would be
	// wrong (see NewRegistry's type switch).
	var openAICompatibleCount int
	for _, prov := range cfg.Providers {
		if prov.Disabled {
			continue
		}
		provider, getErr := reg.Get(prov.Name)
		require.NoErrorf(t, getErr, "provider %q should be registered", prov.Name)
		require.Equalf(t, prov.Name, provider.Name(), "registered provider name should round-trip for %q", prov.Name)

		if prov.Type == config.ProviderOpenAICompatible {
			_, ok := provider.(*openAICompatibleProvider)
			require.Truef(t, ok, "provider %q should resolve to an openai_compatible client", prov.Name)
			openAICompatibleCount++
		}
	}

	// Guard against a vacuous pass: the default config is expected to
	// carry openai_compatible providers, so the loop above must have
	// asserted on at least one.
	require.Greater(t, openAICompatibleCount, 0, "default config should register openai_compatible providers")
}

// TestRegistryInfersGLMContextWindow proves a GLM-4.6 model added without an
// explicit context_window still gets a real budget from the family heuristic
// (200k, the window GLM-4.6 lifted to) rather than zero ("unknown"). This is the
// integration the Z.AI default preset relies on: NewRegistry falls back to
// inferContextWindow when ContextWindow is unset, so a user who adds glm-4.6 to a
// z.ai-style openai_compatible provider gets correct compaction/overflow budgets.
func TestRegistryInfersGLMContextWindow(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:    "zai",
			Type:    config.ProviderOpenAICompatible,
			BaseURL: "https://api.z.ai/api/paas/v4",
			Models:  []string{"glm-4.6"},
		}},
		Models: []config.Model{{
			ID:            "glm-4.6",
			Provider:      "zai",
			SupportsTools: true,
			// ContextWindow intentionally omitted to exercise the heuristic.
		}},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	require.Len(t, models, 1)
	require.Equal(t, "glm-4.6", models[0].ID)
	require.Equal(t, 200_000, models[0].ContextWindow)
}

func collectEvents(events <-chan Event) []Event {
	var out []Event
	for event := range events {
		out = append(out, event)
	}
	return out
}

func testConfig(providerName string, typ config.ProviderType, baseURL string) *config.Config {
	return &config.Config{
		Providers: []config.Provider{{
			Name:    providerName,
			Type:    typ,
			BaseURL: baseURL,
			Models:  []string{"test-model"},
		}},
		Models: []config.Model{{
			ID:                    "test-model",
			Provider:              providerName,
			ContextWindow:         128000,
			InputPricePerMTokUSD:  0.1,
			OutputPricePerMTokUSD: 0.2,
			SupportsTools:         true,
		}},
		Ledger: config.LedgerConfig{
			Currency:   "INR",
			UsdInrRate: 83.5,
		},
	}
}
