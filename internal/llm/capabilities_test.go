package llm

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

func TestInferContextWindow(t *testing.T) {
	cases := []struct {
		id   string
		want int
	}{
		// Specific markers must win over the broader family prefix.
		{"gpt-4o-mini", 128_000},
		{"gpt-4-turbo", 128_000},
		{"gpt-4.1", 1_047_576},
		// GPT-4.5's id contains "gpt-4" but it must not fall through to the 8k
		// family rule.
		{"gpt-4.5-preview", 128_000},
		{"gpt-4", 8_192},
		{"gpt-3.5-turbo", 16_385},
		{"gpt-5", 400_000},
		{"o1-preview", 200_000},
		{"o3-mini", 200_000},
		{"claude-sonnet-4-20250514", 200_000},
		{"claude-3-5-haiku", 200_000},
		{"gemini-1.5-pro-latest", 2_097_152},
		{"gemini-1.5-flash", 1_048_576},
		{"gemini-2.5-flash", 1_048_576},
		{"llama-3.1-70b", 128_000},
		// Llama 4 Scout (10M) and Maverick (1M) ship far larger windows than the
		// 128k Llama 3.x default; their ids contain "llama", so the specific
		// markers must win over the family rule.
		{"meta-llama/Llama-4-Scout-17B-16E-Instruct", 10_485_760},
		{"meta-llama/llama-4-maverick", 1_048_576},
		{"mixtral-8x7b", 32_768},
		{"mistral-large", 32_768},
		// Ministral and Devstral ship a 128k window and do not contain the
		// "mistral" marker, so each needs its own rule rather than the family
		// default (32k) — or, before these rules existed, "unknown" (0).
		{"ministral-8b-latest", 128_000},
		{"ministral-3b-2410", 128_000},
		{"devstral-small-2507", 128_000},
		{"qwen2.5-coder", 32_768},
		{"deepseek-chat", 65_536},
		// Indian-built models: Sarvam's 32k flagship and Krutrim's 8k spectre line.
		{"sarvam-m", 32_768},
		{"Krutrim-spectre-v2", 8_192},
		// xAI Grok, Perplexity Sonar tiers, Codestral, Kimi and gpt-oss.
		{"grok-2-latest", 131_072},
		{"sonar", 128_000},
		{"sonar-pro", 200_000},
		{"codestral-latest", 256_000},
		{"kimi-k2-0905-preview", 200_000},
		{"gpt-oss-120b", 128_000},
		// Pixtral, Gemma, Cohere Command, GLM and Nemotron open-weight families.
		{"pixtral-large-latest", 128_000},
		{"gemma-3-27b-it", 128_000},
		{"gemma-2-9b-it", 8_192},
		{"command-a-03-2025", 256_000},
		{"command-r-plus", 128_000},
		{"glm-4-plus", 128_000},
		{"nemotron-4-340b", 128_000},
		// Case-insensitive and whitespace-tolerant.
		{"  GPT-4O  ", 128_000},
		// Unknown ids stay "unknown" (zero).
		{"some-unknown-model", 0},
		{"", 0},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			require.Equal(t, tc.want, inferContextWindow(tc.id),
				"inferContextWindow(%q)", tc.id)
		})
	}
}

// TestNewRegistryInfersContextWindow verifies the registry fills a missing
// context_window from the model-id heuristic while leaving an explicit value
// untouched.
func TestNewRegistryInfersContextWindow(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.Provider{{
			Name:    "openai",
			Type:    config.ProviderOpenAI,
			BaseURL: "https://example.invalid/v1",
			Models:  []string{"gpt-4o", "explicit-model"},
		}},
		Models: []config.Model{
			{ID: "gpt-4o", Provider: "openai"}, // no context_window → inferred
			{ID: "explicit-model", Provider: "openai", ContextWindow: 4096},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}

	reg, err := NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	byID := make(map[string]Model, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}

	require.Equal(t, 128_000, byID["gpt-4o"].ContextWindow,
		"missing context_window should be inferred from the model id")
	require.Equal(t, 4096, byID["explicit-model"].ContextWindow,
		"an explicit context_window must not be overridden")
}
