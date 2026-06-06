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

// TestProviderHeadersReachTheWire proves the per-provider Headers map configured
// on a provider (e.g. OpenRouter's HTTP-Referer / X-Title attribution) is
// injected into the outgoing request by the registry's transport layer, without
// clobbering the auth header the provider sets itself. This is the integration
// the OpenRouter default preset relies on so requests are attributed and not
// deprioritized on OpenRouter's rankings.
func TestProviderHeadersReachTheWire(t *testing.T) {
	var (
		gotReferer string
		gotTitle   string
		gotAuth    string
	)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotReferer = r.Header.Get("HTTP-Referer")
		gotTitle = r.Header.Get("X-Title")
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\n")
		fmt.Fprint(w, "data: [DONE]\n\n")
	}))
	defer server.Close()
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("openrouter", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	cfg.Providers[0].Headers = map[string]string{
		"HTTP-Referer": "https://github.com/arbazkhan971/bharatcode",
		"X-Title":      "BharatCode",
	}
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openrouter")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)
	collectEvents(events)

	require.Equal(t, "https://github.com/arbazkhan971/bharatcode", gotReferer,
		"configured HTTP-Referer header must reach the provider")
	require.Equal(t, "BharatCode", gotTitle,
		"configured X-Title header must reach the provider")
	// The custom headers are additive and must never override the auth header the
	// provider sets itself.
	require.Equal(t, "Bearer test-key", gotAuth,
		"a custom header must not clobber the Authorization the provider sets")
}

// TestDefaultConfigOpenRouterAttributionHeaders asserts the embedded default
// config ships OpenRouter's attribution headers (HTTP-Referer / X-Title) on the
// openrouter preset. OpenRouter uses these to attribute traffic and rank apps;
// omitting them risks the requests being deprioritized. Combined with
// TestProviderHeadersReachTheWire (which proves configured headers reach the
// wire), this guards the preset against a silent regression. Fully offline.
func TestDefaultConfigOpenRouterAttributionHeaders(t *testing.T) {
	cfg := config.Default()

	var found bool
	for _, prov := range cfg.Providers {
		if prov.Name != "openrouter" {
			continue
		}
		found = true
		require.Equal(t, "https://github.com/arbazkhan971/bharatcode", prov.Headers["HTTP-Referer"],
			"openrouter preset should set the HTTP-Referer attribution header")
		require.Equal(t, "BharatCode", prov.Headers["X-Title"],
			"openrouter preset should set the X-Title attribution header")
	}
	require.True(t, found, "default config should define an openrouter provider")
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

// TestOllamaRequestSetsNumCtxFromContextWindow proves the Ollama request carries
// options.num_ctx sized to the model's configured context window, so a long agent
// prompt is not silently truncated at Ollama's small default window.
func TestOllamaRequestSetsNumCtxFromContextWindow(t *testing.T) {
	var captured ollamaRequest
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.NoError(t, json.NewDecoder(r.Body).Decode(&captured))
		fmt.Fprintln(w, `{"done":true,"prompt_eval_count":1,"eval_count":1}`)
	}))
	defer server.Close()

	reg, err := NewRegistry(testConfig("ollama", config.ProviderOllama, server.URL))
	require.NoError(t, err)
	provider, err := reg.Get("ollama")
	require.NoError(t, err)

	events, err := provider.Stream(context.Background(), Request{
		Model:    "test-model",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})
	require.NoError(t, err)
	collectEvents(events)

	// testConfig configures test-model with a 128k context window.
	require.Equal(t, 128000, captured.Options.NumCtx)
}

// TestBuildOllamaRequestOmitsZeroNumCtx proves a zero context window leaves
// num_ctx off the wire so Ollama keeps its own default rather than receiving
// num_ctx:0 (which would request an empty context).
func TestBuildOllamaRequestOmitsZeroNumCtx(t *testing.T) {
	body, err := buildOllamaRequest(Request{Model: "test-model"}, 0)
	require.NoError(t, err)
	require.Equal(t, 0, body.Options.NumCtx)

	raw, err := json.Marshal(body)
	require.NoError(t, err)
	require.NotContains(t, string(raw), "num_ctx")
}

// TestOllamaNumCtxFallsBackToInference proves that when a configured model omits
// an explicit context_window, the provider falls back to the family heuristic
// (here gpt-4o's 128k) rather than sending no num_ctx.
func TestOllamaNumCtxFallsBackToInference(t *testing.T) {
	// A configured model with no explicit context_window still resolves to the
	// family heuristic keyed on its id.
	p := &ollamaProvider{
		models: []Model{{ID: "gpt-4o", Provider: "ollama"}},
	}
	require.Equal(t, 128000, p.numCtx("gpt-4o"))
	// An id matching no known family stays at 0 so num_ctx is omitted.
	require.Equal(t, 0, p.numCtx("totally-unknown-model"))
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

// TestDefaultConfigProviderModelsHaveCatalogEntries asserts that every model id
// each provider lists in the embedded default config resolves to a catalog
// entry exposed by the registry. The two are linked only by convention — a
// provider's Models slice is a list of ids, and NewRegistry attaches metadata
// (context window, pricing, tool/image support) only for ids that also appear
// in the top-level Models catalog. A provider that names a model with no
// matching catalog entry therefore wires up with no usable metadata for it, and
// the model never surfaces in ListModels. config.Validate does not catch this
// (it only checks the reverse direction: catalog -> provider), so this test
// guards the default config against that silent gap. It is fully offline.
func TestDefaultConfigProviderModelsHaveCatalogEntries(t *testing.T) {
	cfg := config.Default()

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	catalog := make(map[string]bool)
	for _, m := range reg.ListModels() {
		catalog[m.ID] = true
	}

	var checked int
	for _, prov := range cfg.Providers {
		if prov.Disabled {
			continue
		}
		for _, id := range prov.Models {
			require.Truef(t, catalog[id],
				"provider %q lists model %q with no matching catalog entry; add it to the top-level \"models\" array",
				prov.Name, id)
			checked++
		}
	}

	// Guard against a vacuous pass: the default config is expected to list
	// models on its providers, so the loop must have asserted on at least one.
	require.Greater(t, checked, 0, "default config providers should list models")
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

// TestDefaultConfigCoherePreset asserts the embedded default config ships a
// Cohere provider wired to Cohere's OpenAI-compatibility endpoint, and that its
// Command A model surfaces in the registry catalog with the right window and
// capabilities. Cohere's /compatibility/v1 dialect speaks the OpenAI wire
// format, so it rides the openai_compatible client; this guards the preset
// against a silent regression in either the provider block or its catalog
// entry. Fully offline: Default() unmarshals embedded bytes and NewRegistry
// only constructs structs.
func TestDefaultConfigCoherePreset(t *testing.T) {
	cfg := config.Default()

	var prov *config.Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == "cohere" {
			prov = &cfg.Providers[i]
			break
		}
	}
	require.NotNil(t, prov, "default config should define a cohere provider")
	require.Equal(t, config.ProviderOpenAICompatible, prov.Type)
	require.Equal(t, "https://api.cohere.ai/compatibility/v1", prov.BaseURL,
		"cohere preset should target Cohere's OpenAI-compatibility endpoint")
	require.Equal(t, "COHERE_API_KEY", prov.APIKeyEnv)

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	provider, err := reg.Get("cohere")
	require.NoError(t, err)
	_, ok := provider.(*openAICompatibleProvider)
	require.True(t, ok, "cohere should resolve to the openai_compatible client")

	var model Model
	for _, m := range reg.ListModels() {
		if m.ID == "command-a-03-2025" {
			model = m
			break
		}
	}
	require.Equal(t, "command-a-03-2025", model.ID, "Command A should appear in the catalog")
	require.Equal(t, "cohere", model.Provider)
	require.Equal(t, 256_000, model.ContextWindow, "Command A serves a 256k context window")
	require.True(t, model.SupportsTools, "Command A supports tool calling")
	require.False(t, model.SupportsImages, "Command A is text-only")
}

// TestDefaultConfigMoonshotPreset asserts the embedded default config ships a
// Moonshot provider wired to Moonshot's OpenAI-compatibility endpoint, and that
// its Kimi K2 model surfaces in the registry catalog with the right window and
// capabilities. The K2-Instruct "0905" refresh doubled the original K2's 128k
// window to 256k (262,144 tokens), and the catalog must reflect that so the
// agent's compaction/overflow budgets match what the model actually serves
// rather than undercounting it. This guards the catalog entry against silently
// regressing to a smaller window. Fully offline: Default() unmarshals embedded
// bytes and NewRegistry only constructs structs.
func TestDefaultConfigMoonshotPreset(t *testing.T) {
	cfg := config.Default()

	var prov *config.Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == "moonshot" {
			prov = &cfg.Providers[i]
			break
		}
	}
	require.NotNil(t, prov, "default config should define a moonshot provider")
	require.Equal(t, config.ProviderOpenAICompatible, prov.Type)
	require.Equal(t, "https://api.moonshot.cn/v1", prov.BaseURL,
		"moonshot preset should target Moonshot's OpenAI-compatibility endpoint")
	require.Equal(t, "MOONSHOT_API_KEY", prov.APIKeyEnv)

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	provider, err := reg.Get("moonshot")
	require.NoError(t, err)
	_, ok := provider.(*openAICompatibleProvider)
	require.True(t, ok, "moonshot should resolve to the openai_compatible client")

	var model Model
	for _, m := range reg.ListModels() {
		if m.ID == "kimi-k2-0905-preview" {
			model = m
			break
		}
	}
	require.Equal(t, "kimi-k2-0905-preview", model.ID, "Kimi K2 0905 should appear in the catalog")
	require.Equal(t, "moonshot", model.Provider)
	require.Equal(t, 262_144, model.ContextWindow,
		"Kimi K2 0905 serves a 256k (262,144-token) context window")
	require.True(t, model.SupportsTools, "Kimi K2 supports tool calling")
	require.False(t, model.SupportsImages, "Kimi K2 0905 is text-only")

	// The catalog's explicit window must not undercut what the family heuristic
	// would infer for the same id — a mismatch is the signature of a stale value.
	require.GreaterOrEqual(t, model.ContextWindow, inferContextWindow(model.ID),
		"catalog window for Kimi K2 0905 should not undercount the inferred family window")
}

// TestDefaultConfigOpenAIPreset asserts the embedded default config ships an
// OpenAI provider with the current model lineup — gpt-4o, gpt-4o-mini, gpt-4.1,
// and the reasoning models o3 and o4-mini — and that each surfaces in the
// registry catalog with correct context windows and capabilities. The context
// windows are also cross-checked against the family heuristic so a stale catalog
// value never silently undercounts the real window. Fully offline.
func TestDefaultConfigOpenAIPreset(t *testing.T) {
	cfg := config.Default()

	var prov *config.Provider
	for i := range cfg.Providers {
		if cfg.Providers[i].Name == "openai" {
			prov = &cfg.Providers[i]
			break
		}
	}
	require.NotNil(t, prov, "default config should define an openai provider")
	require.Equal(t, config.ProviderOpenAI, prov.Type)
	require.Equal(t, "OPENAI_API_KEY", prov.APIKeyEnv)

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	provider, err := reg.Get("openai")
	require.NoError(t, err)
	_, ok := provider.(*openAICompatibleProvider)
	require.True(t, ok, "openai should resolve to the openai_compatible client")

	catalog := make(map[string]Model)
	for _, m := range reg.ListModels() {
		catalog[m.ID] = m
	}

	cases := []struct {
		id            string
		contextWindow int
		supportsImg   bool
		supportsTools bool
	}{
		{"gpt-4o", 128_000, true, true},
		{"gpt-4o-mini", 128_000, true, true},
		// The gpt-4.1 family (gpt-4.1, gpt-4.1-mini, gpt-4.1-nano) all expose a
		// ~1M context window; the catalog values must not undercount the family
		// heuristic. gpt-4.1-nano is the cheapest tier ($0.10/$0.40 per MTok),
		// useful for fast, low-cost completions where the full 1M context is still
		// needed.
		{"gpt-4.1", 1_047_576, true, true},
		{"gpt-4.1-mini", 1_047_576, true, true},
		{"gpt-4.1-nano", 1_047_576, true, true},
		// o3 and o4-mini are reasoning models with a 200k context window.
		{"o3", 200_000, true, true},
		{"o4-mini", 200_000, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			m, found := catalog[tc.id]
			require.Truef(t, found, "model %q should appear in the registry catalog", tc.id)
			require.Equal(t, "openai", m.Provider)
			require.Equal(t, tc.contextWindow, m.ContextWindow,
				"model %q context window mismatch", tc.id)
			require.Equal(t, tc.supportsImg, m.SupportsImages,
				"model %q supports_images mismatch", tc.id)
			require.Equal(t, tc.supportsTools, m.SupportsTools,
				"model %q supports_tools mismatch", tc.id)
			// The catalog window must not undercount the family heuristic for the
			// same id: a lower catalog value is the signature of a stale entry.
			inferred := inferContextWindow(tc.id)
			if inferred > 0 {
				require.GreaterOrEqual(t, m.ContextWindow, inferred,
					"catalog window for %q should not undercount the inferred family window", tc.id)
			}
		})
	}

	// The reasoning models must be classified as such so the request builder
	// omits unsupported params (temperature, max_tokens) for them.
	require.True(t, isReasoningModel("o3"), "o3 must be classified as a reasoning model")
	require.True(t, isReasoningModel("o4-mini"), "o4-mini must be classified as a reasoning model")
	require.False(t, isReasoningModel("gpt-4o"), "gpt-4o must not be classified as a reasoning model")
	require.False(t, isReasoningModel("gpt-4.1"), "gpt-4.1 must not be classified as a reasoning model")
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
