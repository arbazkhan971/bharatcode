package cmd

import (
	"context"
	"encoding/json"
	"os"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/stretchr/testify/require"
)

// modelsDevFixture is a trimmed Models.dev api.json payload covering the
// fields BharatCode consumes: provider id/name/api/env, and per-model
// id, tool_call, attachment/modalities, limit.context, and cost.
const modelsDevFixture = `{
  "anthropic": {
    "id": "anthropic",
    "name": "Anthropic",
    "npm": "@ai-sdk/anthropic",
    "api": "https://api.anthropic.com",
    "env": ["ANTHROPIC_API_KEY"],
    "models": {
      "claude-opus": {
        "id": "claude-opus",
        "name": "Claude Opus",
        "attachment": true,
        "tool_call": true,
        "modalities": {"input": ["text", "image"], "output": ["text"]},
        "limit": {"context": 200000, "output": 8192},
        "cost": {"input": 15.0, "output": 75.0}
      }
    }
  },
  "groq": {
    "id": "groq",
    "name": "Groq",
    "npm": "@ai-sdk/openai-compatible",
    "api": "https://api.groq.com/openai/v1",
    "env": ["GROQ_API_KEY", "GROQ_TOKEN"],
    "models": {
      "llama-70b": {
        "id": "llama-70b",
        "name": "Llama 70B",
        "attachment": false,
        "tool_call": true,
        "modalities": {"input": ["text"], "output": ["text"]},
        "limit": {"context": 131072, "output": 32768},
        "cost": {"input": 0.59, "output": 0.79}
      },
      "whisper": {
        "id": "whisper",
        "name": "Whisper",
        "attachment": false,
        "tool_call": false,
        "modalities": {"input": ["audio"], "output": ["text"]},
        "limit": {"context": 0, "output": 0},
        "cost": {"input": 0.0, "output": 0.0}
      }
    }
  }
}`

func TestMergeProviderRegistryProducesProvidersAndModels(t *testing.T) {
	cfg := &config.Config{}

	summary, err := mergeProviderRegistry(cfg, []byte(modelsDevFixture))
	require.NoError(t, err)
	require.Equal(t, 2, summary.Providers)
	require.Equal(t, 3, summary.Models)

	require.Len(t, cfg.Providers, 2)
	// Providers are appended in stable (sorted) id order.
	anthropic := cfg.Providers[0]
	require.Equal(t, "anthropic", anthropic.Name)
	require.Equal(t, config.ProviderAnthropic, anthropic.Type)
	require.Equal(t, "https://api.anthropic.com", anthropic.BaseURL)
	require.Equal(t, "ANTHROPIC_API_KEY", anthropic.APIKeyEnv)
	require.Equal(t, []string{"claude-opus"}, anthropic.Models)

	groq := cfg.Providers[1]
	require.Equal(t, "groq", groq.Name)
	require.Equal(t, config.ProviderOpenAICompatible, groq.Type)
	require.Equal(t, "https://api.groq.com/openai/v1", groq.BaseURL)
	require.Equal(t, "GROQ_API_KEY", groq.APIKeyEnv)
	require.Equal(t, []string{"llama-70b", "whisper"}, groq.Models)

	// Verify a representative model carries pricing, context and capabilities.
	byKey := map[string]config.Model{}
	for _, m := range cfg.Models {
		byKey[m.Provider+"/"+m.ID] = m
	}
	opus := byKey["anthropic/claude-opus"]
	require.Equal(t, 200000, opus.ContextWindow)
	require.InDelta(t, 15.0, opus.InputPricePerMTokUSD, 1e-9)
	require.InDelta(t, 75.0, opus.OutputPricePerMTokUSD, 1e-9)
	require.True(t, opus.SupportsImages)
	require.True(t, opus.SupportsTools)

	llama := byKey["groq/llama-70b"]
	require.Equal(t, 131072, llama.ContextWindow)
	require.InDelta(t, 0.59, llama.InputPricePerMTokUSD, 1e-9)
	require.False(t, llama.SupportsImages)
	require.True(t, llama.SupportsTools)

	whisper := byKey["groq/whisper"]
	require.False(t, whisper.SupportsTools)
	require.False(t, whisper.SupportsImages)
}

func TestMergeProviderRegistryDoesNotClobberUserDefined(t *testing.T) {
	// A user-defined provider that shares the "groq" name but has custom
	// settings, and a user-defined model under it, must survive untouched.
	cfg := &config.Config{
		Providers: []config.Provider{
			{
				Name:      "groq",
				Type:      config.ProviderOpenAICompatible,
				BaseURL:   "https://my-proxy.internal/groq",
				APIKeyEnv: "MY_GROQ_KEY",
				Models:    []string{"my-custom-model"},
			},
		},
		Models: []config.Model{
			{
				ID:                    "my-custom-model",
				Provider:              "groq",
				ContextWindow:         8000,
				InputPricePerMTokUSD:  1.23,
				OutputPricePerMTokUSD: 4.56,
			},
		},
	}

	summary, err := mergeProviderRegistry(cfg, []byte(modelsDevFixture))
	require.NoError(t, err)

	// groq already existed -> only anthropic is a new provider.
	require.Equal(t, 1, summary.Providers)
	// groq's two registry models are new; the user's custom model is not
	// re-added. anthropic contributes one more. Total new = 3.
	require.Equal(t, 3, summary.Models)

	// The existing groq provider entry is preserved verbatim, not overwritten.
	var groq config.Provider
	for _, p := range cfg.Providers {
		if p.Name == "groq" {
			groq = p
		}
	}
	require.Equal(t, "https://my-proxy.internal/groq", groq.BaseURL)
	require.Equal(t, "MY_GROQ_KEY", groq.APIKeyEnv)
	require.Equal(t, []string{"my-custom-model"}, groq.Models)

	// The user's custom model is preserved with its original pricing.
	var custom config.Model
	found := false
	for _, m := range cfg.Models {
		if m.Provider == "groq" && m.ID == "my-custom-model" {
			custom = m
			found = true
		}
	}
	require.True(t, found)
	require.InDelta(t, 1.23, custom.InputPricePerMTokUSD, 1e-9)
	require.Equal(t, 8000, custom.ContextWindow)

	// New registry models were still appended alongside the custom one.
	require.Contains(t, modelKeys(cfg), "groq/llama-70b")
	require.Contains(t, modelKeys(cfg), "anthropic/claude-opus")
}

func TestMergeProviderRegistryRejectsMalformedPayload(t *testing.T) {
	original := &config.Config{
		Providers: []config.Provider{{Name: "deepseek", Type: config.ProviderOpenAICompatible}},
		Models:    []config.Model{{ID: "deepseek-chat", Provider: "deepseek"}},
	}
	// Snapshot to prove the malformed merge is a no-op.
	want := cloneConfigJSON(t, original)

	for name, payload := range map[string][]byte{
		"empty":        []byte(""),
		"not-json":     []byte("this is not json"),
		"empty-object": []byte("{}"),
		"truncated":    []byte(`{"groq": {"id": "groq", "models":`),
	} {
		t.Run(name, func(t *testing.T) {
			cfg := cloneConfigFromJSON(t, want)
			summary, err := mergeProviderRegistry(cfg, payload)
			require.Error(t, err)
			require.Equal(t, providerUpdateSummary{}, summary)
			// Existing packs are intact: the config is byte-identical.
			require.JSONEq(t, string(want), string(cloneConfigJSON(t, cfg)))
		})
	}
}

func TestUpdateProvidersCmdPersistsAndMerges(t *testing.T) {
	// Stub the network+parse stage so we exercise the command wiring
	// (load -> merge -> persist) deterministically and offline.
	oldUpdate := updateProviders
	updateProviders = func(_ context.Context, _ string, cfg *config.Config) (providerUpdateSummary, error) {
		return mergeProviderRegistry(cfg, []byte(modelsDevFixture))
	}
	defer func() { updateProviders = oldUpdate }()

	configPath := writeConfig(t, defaultTestConfig())

	stdout, stderr, err := executeRoot(t, "--config", configPath, "update-providers")
	require.NoError(t, err)
	require.Empty(t, stderr)
	require.Equal(t, "Updated 2 providers, 3 models\n", stdout)

	// The config on disk now carries the merged providers and models in
	// addition to the user's original deepseek pack.
	raw, err := os.ReadFile(configPath)
	require.NoError(t, err)
	var persisted config.Config
	require.NoError(t, json.Unmarshal(raw, &persisted))

	names := map[string]bool{}
	for _, p := range persisted.Providers {
		names[p.Name] = true
	}
	require.True(t, names["deepseek"], "user provider preserved")
	require.True(t, names["anthropic"], "registry provider added")
	require.True(t, names["groq"], "registry provider added")

	require.Contains(t, modelKeys(&persisted), "deepseek/deepseek-chat")
	require.Contains(t, modelKeys(&persisted), "anthropic/claude-opus")
}

func TestUpdateProvidersCmdFailureLeavesConfigIntact(t *testing.T) {
	// A failing fetch/parse must not rewrite the config on disk.
	oldUpdate := updateProviders
	updateProviders = func(_ context.Context, _ string, _ *config.Config) (providerUpdateSummary, error) {
		return providerUpdateSummary{}, errFetchBoom
	}
	defer func() { updateProviders = oldUpdate }()

	configPath := writeConfig(t, defaultTestConfig())
	before, err := os.ReadFile(configPath)
	require.NoError(t, err)

	stdout, stderr, err := executeRoot(t, "--config", configPath, "update-providers")
	require.Error(t, err)
	require.Empty(t, stdout)
	require.Contains(t, stderr, "boom")

	after, err := os.ReadFile(configPath)
	require.NoError(t, err)
	require.Equal(t, string(before), string(after), "config must be byte-identical after a failed update")
}

var errFetchBoom = fetchBoom{}

type fetchBoom struct{}

func (fetchBoom) Error() string { return "fetching provider registry: boom" }

func modelKeys(cfg *config.Config) []string {
	keys := make([]string, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		keys = append(keys, m.Provider+"/"+m.ID)
	}
	return keys
}

func cloneConfigJSON(t *testing.T, cfg *config.Config) []byte {
	t.Helper()
	data, err := json.Marshal(cfg)
	require.NoError(t, err)
	return data
}

func cloneConfigFromJSON(t *testing.T, data []byte) *config.Config {
	t.Helper()
	var cfg config.Config
	require.NoError(t, json.Unmarshal(data, &cfg))
	return &cfg
}
