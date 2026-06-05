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
		// o1-preview and o1-mini shipped a 128k window; the released o1 and the
		// o3/o4-mini line are 200k. The specific preview/mini rules must win over
		// the broader "o1" family rule.
		{"o1-preview", 128_000},
		{"o1-mini", 128_000},
		{"o1-2024-12-17", 200_000},
		{"o3-mini", 200_000},
		{"o4-mini", 200_000},
		{"claude-sonnet-4-20250514", 200_000},
		{"claude-3-5-haiku", 200_000},
		{"gemini-1.5-pro-latest", 2_097_152},
		{"gemini-1.5-flash", 1_048_576},
		{"gemini-2.5-flash", 1_048_576},
		// Gemini 3 keeps the 1M window but shares no substring with the older
		// gemini rules, so it needs its own rule rather than falling through to 0.
		{"gemini-3-pro-preview", 1_048_576},
		{"gemini-3-flash", 1_048_576},
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
		// Magistral (Mistral's reasoning line) is 128k and shares no marker with
		// the other Mistral rules, so it resolves via its own rule, not the family
		// default (32k) and not "unknown" (0).
		{"magistral-small-2506", 128_000},
		{"magistral-medium-latest", 128_000},
		{"qwen2.5-coder", 32_768},
		// Qwen3 lifted the window past the 32k Qwen2.x default: Qwen3-Coder is 256k
		// and the Qwen3 2507 instruct line 128k. Both ids carry the "qwen" marker, so
		// the specific rules must win over the family default rather than fall to 32k.
		{"qwen3-coder-480b-a35b-instruct", 262_144},
		{"qwen3-235b-a22b-instruct-2507", 131_072},
		{"qwen3-32b", 131_072},
		// Qwen2.x ids still resolve to the conservative 32k family default.
		{"qwen2.5-72b-instruct", 32_768},
		// Microsoft Phi: the hosted Phi-3/3.5 line is 128k, Phi-4 is 16k. The
		// "dolphin" finetunes contain the substring "phi" but must not match the
		// specific "phi-3"/"phi-4" markers (they fall through to their base window).
		{"phi-4", 16_384},
		{"phi-3.5-mini-instruct", 128_000},
		{"microsoft/phi-3-medium-128k-instruct", 128_000},
		{"dolphin-2.9-llama3-8b", 128_000},
		// Databricks DBRX (32k) and IBM Granite 3.x (128k).
		{"dbrx-instruct", 32_768},
		{"granite-3.3-8b-instruct", 128_000},
		{"deepseek-chat", 65_536},
		// Amazon Nova (Bedrock): Pro/Lite are 300k, Micro is 128k.
		{"nova-pro-v1", 300_000},
		{"nova-lite-v1", 300_000},
		{"nova-micro-v1", 128_000},
		// AI21 Jamba 1.5 (Bedrock) is 256k.
		{"jamba-1.5-large", 256_000},
		// Indian-built models: Sarvam's 32k flagship and Krutrim's 8k spectre line.
		{"sarvam-m", 32_768},
		{"Krutrim-spectre-v2", 8_192},
		// xAI Grok, Perplexity Sonar tiers, Codestral, Kimi and gpt-oss.
		{"grok-2-latest", 131_072},
		{"grok-3", 131_072},
		// Grok 4 and the grok-code coding line ship a 256k window; their ids carry
		// the "grok" marker, so the specific rules must win over the 131k family rule.
		{"grok-4", 256_000},
		{"grok-4-fast", 256_000},
		{"grok-code-fast-1", 256_000},
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
		{"glm-4.5", 128_000},
		// GLM-4.6 lifted its window to 200k; its id carries the "glm" marker, so the
		// specific rule must win over the 128k family default.
		{"glm-4.6", 200_000},
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

// TestModelSupportsGeminiThinking verifies the native-thinking gate recognizes
// the Gemini 2.5 family and the Gemini 3 line while rejecting older models and
// ids absent from the configured catalog.
func TestModelSupportsGeminiThinking(t *testing.T) {
	models := []Model{
		{ID: "gemini-2.5-flash"},
		{ID: "gemini-3-pro-preview"},
		{ID: "gemini-2.0-flash"},
	}

	cases := []struct {
		id   string
		want bool
	}{
		{"gemini-2.5-flash", true},
		{"gemini-3-pro-preview", true},
		// Pre-2.5 models do not support the native thinkingConfig.
		{"gemini-2.0-flash", false},
		// An id absent from the catalog is never thinking-capable, even if its
		// name matches a marker.
		{"gemini-3-flash", false},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			require.Equal(t, tc.want, modelSupportsGeminiThinking(models, tc.id),
				"modelSupportsGeminiThinking(%q)", tc.id)
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
