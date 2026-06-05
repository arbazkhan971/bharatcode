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

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// openAIModelConfig builds a single-provider config of the native "openai"
// type whose one model carries the given id, so tests can exercise the
// reasoning-vs-normal gating by model id alone.
func openAIModelConfig(t *testing.T, baseURL, modelID string) *config.Config {
	t.Helper()
	cfg := testConfig("openai", config.ProviderOpenAI, baseURL)
	cfg.Providers[0].APIKeyEnv = "TEST_OPENAI_KEY"
	cfg.Providers[0].Models = []string{modelID}
	cfg.Models[0].ID = modelID
	return cfg
}

// streamOnce runs one non-streaming request against srv-backed provider and
// drains the events, returning nothing but ensuring the round-trip completed.
func streamOnce(t *testing.T, provider Provider, req Request) {
	t.Helper()
	events, err := provider.Stream(context.Background(), req)
	require.NoError(t, err)
	_ = collectEvents(events)
}

// jsonOKHandler captures the raw request body into *raw and replies with a
// minimal non-streaming OpenAI chat completion so the provider round-trips.
func jsonOKHandler(raw *[]byte) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		*raw = b
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}
}

// TestOpenAIProviderPostsToBearerEndpoint asserts the native openai provider
// targets the OpenAI chat-completions path and authenticates with a Bearer
// header carrying the key from the configured env var.
func TestOpenAIProviderPostsToBearerEndpoint(t *testing.T) {
	var gotPath, gotAuth string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, `{"choices":[{"message":{"content":"ok"}}],"usage":{"prompt_tokens":1,"completion_tokens":1}}`)
	}))
	defer server.Close()
	t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

	cfg := openAIModelConfig(t, server.URL+"/v1", "gpt-4o")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai")
	require.NoError(t, err)

	streamOnce(t, provider, Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})

	require.Equal(t, "/v1/chat/completions", gotPath)
	require.Equal(t, "Bearer sk-test-123", gotAuth)
}

// TestOpenAIProviderDefaultsToOfficialBaseURL asserts the registry fills the
// official OpenAI base URL when the provider omits base_url, so requests build
// against https://api.openai.com/v1 without a configured endpoint. The request
// is aimed at an unset API key so it fails before any network call, keeping the
// test offline while still proving the URL default.
func TestOpenAIProviderDefaultsToOfficialBaseURL(t *testing.T) {
	cfg := openAIModelConfig(t, "", "gpt-4o")
	cfg.Providers[0].APIKeyEnv = "MISSING_OPENAI_KEY_FOR_DEFAULT_URL"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai")
	require.NoError(t, err)

	got, ok := provider.(*openAICompatibleProvider)
	require.True(t, ok, "openai provider should use the openai-compatible client")
	require.Equal(t, "https://api.openai.com/v1", got.baseURL)

	// And it never reaches the network because the API key env is unset.
	_, err = provider.Stream(context.Background(), Request{Model: "gpt-4o"})
	require.ErrorIs(t, err, ErrAuth)
}

// TestOpenAIReasoningModelOmitsTemperature asserts a reasoning model (o-series)
// request omits the temperature field on the wire even when the request carries
// a non-zero temperature, since reasoning models reject the param. The
// assertion reads the captured raw body so it verifies the actual JSON, not a
// struct default.
func TestOpenAIReasoningModelOmitsTemperature(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(jsonOKHandler(&raw))
	defer server.Close()
	t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

	cfg := openAIModelConfig(t, server.URL+"/v1", "o3-mini")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai")
	require.NoError(t, err)

	streamOnce(t, provider, Request{
		Model:       "o3-mini",
		Temperature: 0.7,
		Messages:    []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})

	require.NotEmpty(t, raw, "server must have received the request")
	// Raw-wire assertion: the temperature key must be absent entirely.
	require.NotContains(t, string(raw), `"temperature"`)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	_, hasTemp := probe["temperature"]
	require.False(t, hasTemp, "reasoning model request must omit temperature")
}

// TestOpenAINormalModelIncludesTemperature asserts a non-reasoning model still
// sends temperature on the wire, proving the gating is specific to reasoning
// models and does not regress normal sampling control.
func TestOpenAINormalModelIncludesTemperature(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(jsonOKHandler(&raw))
	defer server.Close()
	t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

	cfg := openAIModelConfig(t, server.URL+"/v1", "gpt-4o")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai")
	require.NoError(t, err)

	streamOnce(t, provider, Request{
		Model:       "gpt-4o",
		Temperature: 0.7,
		Messages:    []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})

	require.NotEmpty(t, raw, "server must have received the request")
	require.Contains(t, string(raw), `"temperature"`)

	var probe struct {
		Temperature *float64 `json:"temperature"`
	}
	require.NoError(t, json.Unmarshal(raw, &probe))
	require.NotNil(t, probe.Temperature, "normal model request must include temperature")
	require.InEpsilon(t, 0.7, *probe.Temperature, 1e-9)
}

// TestOpenAINormalModelOmitsUnsetTemperature pins the path the real agent loop
// hits: it builds a Request without a Temperature, so the field is the zero
// value. A normal model must then omit "temperature" entirely (omitempty), so
// the provider applies its own default sampling rather than a forced 0. This is
// the discriminating case that proves the reasoning gate did not regress the
// default behavior for non-reasoning models.
func TestOpenAINormalModelOmitsUnsetTemperature(t *testing.T) {
	var raw []byte
	server := httptest.NewServer(jsonOKHandler(&raw))
	defer server.Close()
	t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

	cfg := openAIModelConfig(t, server.URL+"/v1", "gpt-4o")
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("openai")
	require.NoError(t, err)

	// Temperature deliberately unset, mirroring the agent loop's request.
	streamOnce(t, provider, Request{
		Model:    "gpt-4o",
		Messages: []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
	})

	require.NotEmpty(t, raw, "server must have received the request")
	require.NotContains(t, string(raw), `"temperature"`)

	var probe map[string]json.RawMessage
	require.NoError(t, json.Unmarshal(raw, &probe))
	_, hasTemp := probe["temperature"]
	require.False(t, hasTemp, "unset temperature must be omitted for a normal model")
}

// TestOpenAIReasoningModelPassesReasoningEffort asserts a reasoning model
// request forwards reasoning_effort when the request sets it, and that a
// normal model never emits the field.
func TestOpenAIReasoningModelPassesReasoningEffort(t *testing.T) {
	t.Run("reasoning model forwards effort", func(t *testing.T) {
		var raw []byte
		server := httptest.NewServer(jsonOKHandler(&raw))
		defer server.Close()
		t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

		cfg := openAIModelConfig(t, server.URL+"/v1", "o1")
		reg, err := NewRegistry(cfg)
		require.NoError(t, err)
		provider, err := reg.Get("openai")
		require.NoError(t, err)

		streamOnce(t, provider, Request{
			Model:           "o1",
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})

		require.NotEmpty(t, raw)
		var probe struct {
			ReasoningEffort string   `json:"reasoning_effort"`
			Temperature     *float64 `json:"temperature"`
		}
		require.NoError(t, json.Unmarshal(raw, &probe))
		require.Equal(t, "high", probe.ReasoningEffort)
		require.Nil(t, probe.Temperature, "reasoning model must not send temperature")
	})

	t.Run("normal model omits effort", func(t *testing.T) {
		var raw []byte
		server := httptest.NewServer(jsonOKHandler(&raw))
		defer server.Close()
		t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

		cfg := openAIModelConfig(t, server.URL+"/v1", "gpt-4o")
		reg, err := NewRegistry(cfg)
		require.NoError(t, err)
		provider, err := reg.Get("openai")
		require.NoError(t, err)

		streamOnce(t, provider, Request{
			Model:           "gpt-4o",
			ReasoningEffort: "high",
			Messages:        []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})

		require.NotEmpty(t, raw)
		require.NotContains(t, string(raw), `"reasoning_effort"`)
	})
}

// TestOpenAIMaxTokensFieldSelection asserts the output cap is sent under
// max_tokens for a normal model but under max_completion_tokens for a reasoning
// model, which rejects the legacy max_tokens field.
func TestOpenAIMaxTokensFieldSelection(t *testing.T) {
	t.Run("normal model uses max_tokens", func(t *testing.T) {
		var raw []byte
		server := httptest.NewServer(jsonOKHandler(&raw))
		defer server.Close()
		t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

		cfg := openAIModelConfig(t, server.URL+"/v1", "gpt-4o")
		reg, err := NewRegistry(cfg)
		require.NoError(t, err)
		provider, err := reg.Get("openai")
		require.NoError(t, err)

		streamOnce(t, provider, Request{
			Model:     "gpt-4o",
			MaxTokens: 1234,
			Messages:  []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})

		require.NotEmpty(t, raw)
		var probe struct {
			MaxTokens           *int `json:"max_tokens"`
			MaxCompletionTokens *int `json:"max_completion_tokens"`
		}
		require.NoError(t, json.Unmarshal(raw, &probe))
		require.NotNil(t, probe.MaxTokens)
		require.Equal(t, 1234, *probe.MaxTokens)
		require.Nil(t, probe.MaxCompletionTokens, "normal model must not send max_completion_tokens")
	})

	t.Run("reasoning model uses max_completion_tokens", func(t *testing.T) {
		var raw []byte
		server := httptest.NewServer(jsonOKHandler(&raw))
		defer server.Close()
		t.Setenv("TEST_OPENAI_KEY", "sk-test-123")

		cfg := openAIModelConfig(t, server.URL+"/v1", "o3-mini")
		reg, err := NewRegistry(cfg)
		require.NoError(t, err)
		provider, err := reg.Get("openai")
		require.NoError(t, err)

		streamOnce(t, provider, Request{
			Model:     "o3-mini",
			MaxTokens: 1234,
			Messages:  []message.Message{{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "hi"}}}},
		})

		require.NotEmpty(t, raw)
		var probe struct {
			MaxTokens           *int `json:"max_tokens"`
			MaxCompletionTokens *int `json:"max_completion_tokens"`
		}
		require.NoError(t, json.Unmarshal(raw, &probe))
		require.NotNil(t, probe.MaxCompletionTokens)
		require.Equal(t, 1234, *probe.MaxCompletionTokens)
		require.Nil(t, probe.MaxTokens, "reasoning model must not send the legacy max_tokens field")
	})
}

// TestIsReasoningModel pins the model-id classification so the gating prefixes
// stay intentional: the o-series and the whole gpt-5 family are reasoning
// models; gpt-4o and the chat-tuned gpt-5-chat variant are not.
func TestIsReasoningModel(t *testing.T) {
	cases := []struct {
		id   string
		want bool
	}{
		{"o1", true},
		{"o1-mini", true},
		{"o3", true},
		{"o3-mini", true},
		{"o4-mini", true},
		{"O3-MINI", true},
		// The gpt-5 family runs a hidden reasoning pass and rejects temperature,
		// so every member except gpt-5-chat must classify as reasoning.
		{"gpt-5", true},
		{"gpt-5-mini", true},
		{"gpt-5-nano", true},
		{"gpt-5-codex", true},
		{"gpt-5-reasoning", true},
		{"gpt-5-reasoning-alpha", true},
		{"GPT-5-MINI", true},
		{"gpt-5.5", true},
		// gpt-5-chat is the one non-reasoning member of the family.
		{"gpt-5-chat", false},
		{"gpt-5-chat-latest", false},
		{"gpt-4o", false},
		{"gpt-4o-mini", false},
		{"deepseek-chat", false},
		{"o1mini", false},
		{"", false},
	}
	for _, tc := range cases {
		require.Equalf(t, tc.want, isReasoningModel(tc.id), "isReasoningModel(%q)", tc.id)
	}
}
