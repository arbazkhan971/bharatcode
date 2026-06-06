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
		// gpt-4-32k is a real 32k model whose id contains "gpt-4"; it must win
		// over the bare 8k family rule rather than mis-resolving to a quarter of
		// its window.
		{"gpt-4-32k", 32_768},
		{"gpt-4-32k-0613", 32_768},
		{"gpt-3.5-turbo", 16_385},
		// Azure names the GPT-3.5 family without the dot; these dot-less ids must
		// resolve to the 16k window rather than falling through to "unknown" (0).
		{"gpt-35-turbo", 16_385},
		{"gpt-35-turbo-16k", 16_385},
		{"gpt-5", 400_000},
		{"gpt-5-mini", 400_000},
		{"gpt-5.1-codex", 400_000},
		// The chat-tuned gpt-5-chat-latest ships only a 128k window; its id carries
		// the "gpt-5" marker, so the specific rule must win over the 400k family rule.
		{"gpt-5-chat-latest", 128_000},
		// Versioned chat variants (gpt-5.1-chat-latest) never contain the literal
		// "gpt-5-chat", so the gpt-5 chat pre-scan — not a substring rule — must keep
		// them at 128k instead of letting the family rule resolve them to 400k.
		{"gpt-5.1-chat-latest", 128_000},
		{"gpt-5.1-chat", 128_000},
		// The vendor-namespaced form an aggregator serves must classify the same.
		{"openai/gpt-5.1-chat-latest", 128_000},
		// o1-preview and o1-mini shipped a 128k window; the released o1 and the
		// o3/o4-mini line are 200k. The specific preview/mini rules must win over
		// the broader "o1" family rule.
		{"o1-preview", 128_000},
		{"o1-mini", 128_000},
		{"o1-2024-12-17", 200_000},
		{"o3-mini", 200_000},
		{"o4-mini", 200_000},
		// codex-mini-latest is the Codex-CLI o4-mini fine-tune; its id carries no
		// o-series marker, so its own rule must resolve it to the inherited 200k
		// window rather than letting it fall through to "unknown" (0).
		{"codex-mini-latest", 200_000},
		// A gpt-5 codex variant must still resolve to the 400k gpt-5 window, not the
		// codex-mini rule, even though its id also contains "codex".
		{"gpt-5-codex", 400_000},
		{"claude-sonnet-4-20250514", 200_000},
		{"claude-3-5-haiku", 200_000},
		{"gemini-1.5-pro-latest", 2_097_152},
		{"gemini-1.5-flash", 1_048_576},
		{"gemini-2.5-flash", 1_048_576},
		// Gemini 3 keeps the 1M window but shares no substring with the older
		// gemini rules, so it needs its own rule rather than falling through to 0.
		{"gemini-3-pro-preview", 1_048_576},
		{"gemini-3-flash", 1_048_576},
		// Google's rolling "-latest" aliases track the current Gemini 2.5
		// generation (1M window); they carry no version digit, so without their
		// own rules they would fall through to "unknown" (0).
		{"gemini-flash-latest", 1_048_576},
		{"gemini-flash-lite-latest", 1_048_576},
		{"gemini-pro-latest", 1_048_576},
		{"llama-3.1-70b", 128_000},
		// Llama 4 Scout (10M) and Maverick (1M) ship far larger windows than the
		// 128k Llama 3.x default; their ids contain "llama", so the specific
		// markers must win over the family rule.
		{"meta-llama/Llama-4-Scout-17B-16E-Instruct", 10_485_760},
		{"meta-llama/llama-4-maverick", 1_048_576},
		{"mixtral-8x7b", 32_768},
		// Mixtral 8x22B doubled the window to 64k; its id carries the "mixtral"
		// marker, so the specific rule must win over the 32k family default.
		{"open-mixtral-8x22b", 65_536},
		{"mistralai/mixtral-8x22b-instruct", 65_536},
		// Mistral Large 2 lifted the window to 128k; its id carries the "mistral"
		// marker, so the specific rule must win over the family default (32k).
		{"mistral-large-latest", 128_000},
		{"mistral-large-2411", 128_000},
		// Mistral Medium 3 and Mistral NeMo ship a 128k window; their ids carry the
		// "mistral" marker, so the specific rules must win over the family default (32k).
		{"mistral-medium-latest", 128_000},
		{"mistral-medium-2505", 128_000},
		{"open-mistral-nemo", 128_000},
		// Mistral Small 3.x (and the mistral-small-latest alias that tracks it)
		// lifted the window to 128k; the id carries the "mistral" marker, so the
		// specific rule must win over the family default (32k).
		{"mistral-small-latest", 128_000},
		{"mistral-small-2503", 128_000},
		// A bare original Mistral model with no more specific marker still
		// resolves to the 32k family default.
		{"open-mistral-7b", 32_768},
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
		// Qwen3-Max (Alibaba's flagship) ships a 256k native window, above the 128k
		// Qwen3 instruct default; its id carries "qwen3", so its rule must win.
		{"qwen3-max", 262_144},
		// Qwen3-Next (hybrid attention) and Qwen3-VL (vision-language) both ship a
		// 256k native window; their ids carry only the bare "qwen3" marker, so their
		// specific rules must win over the 131k instruct default rather than
		// undercounting their window by half.
		{"qwen3-next-80b-a3b-instruct", 262_144},
		{"qwen3-vl-235b-a22b-instruct", 262_144},
		{"qwen3-235b-a22b-instruct-2507", 131_072},
		{"qwen3-32b", 131_072},
		// Alibaba's commercial DashScope aliases carry no version digit, so they
		// must NOT fall through to the bare "qwen" rule (32k): Qwen-Turbo and
		// Qwen-Flash are 1M and Qwen-Plus is 128k. The "-latest" snapshots resolve
		// the same way (Contains-based matching). Qwen-Max keeps the 32k family
		// default — its commercial alias is the short-context tier — and is asserted
		// below.
		{"qwen-turbo", 1_000_000},
		{"qwen-turbo-latest", 1_000_000},
		{"qwen-flash", 1_000_000},
		{"qwen-plus", 131_072},
		{"qwen-plus-latest", 131_072},
		{"qwen-max", 32_768},
		// Qwen2.x ids still resolve to the conservative 32k family default.
		{"qwen2.5-72b-instruct", 32_768},
		// Qwen's QwQ reasoning and QVQ vision-reasoning lines ship a 128k window but
		// their ids carry no "qwen" marker, so without dedicated rules they would
		// fall through to "unknown" (0).
		{"qwq-32b", 131_072},
		{"qwq-32b-preview", 131_072},
		{"qvq-72b-preview", 131_072},
		// Microsoft Phi: the hosted Phi-3/3.5 line is 128k, the flagship Phi-4 (14B)
		// is 16k, but the Phi-4 refresh variants diverge — Phi-4-mini and
		// Phi-4-multimodal are 128k and Phi-4-reasoning is 32k — so the specific
		// markers must win over the bare "phi-4" family rule. The "dolphin" finetunes
		// contain the substring "phi" but must not match the specific "phi-3"/"phi-4"
		// markers (they fall through to their base window).
		{"phi-4", 16_384},
		{"microsoft/phi-4", 16_384},
		{"phi-4-mini-instruct", 128_000},
		{"phi-4-multimodal-instruct", 128_000},
		{"phi-4-reasoning", 32_768},
		{"phi-4-reasoning-plus", 32_768},
		// Phi-4-mini-reasoning is 128k: the "phi-4-mini" rule must claim it before
		// the 32k "phi-4-reasoning" rule does.
		{"phi-4-mini-reasoning", 128_000},
		{"phi-3.5-mini-instruct", 128_000},
		{"microsoft/phi-3-medium-128k-instruct", 128_000},
		{"dolphin-2.9-llama3-8b", 128_000},
		// Databricks DBRX (32k) and IBM Granite 3.x (128k).
		{"dbrx-instruct", 32_768},
		{"granite-3.3-8b-instruct", 128_000},
		// ByteDance Seed-OSS-36B ships a 512k native window. Its id shares no
		// substring with any broader family rule (notably "gpt-oss" is not a
		// substring of "seed-oss"), so the dedicated rule keeps it off the
		// "unknown" (0) fallback.
		{"seed-oss-36b-instruct", 524_288},
		{"ByteDance-Seed/Seed-OSS-36B-Instruct", 524_288},
		{"bytedance/seed-oss-36b", 524_288},
		// LG EXAONE 4.0/3.5 hosted flagship (32B) exposes a 128k window; the
		// "exaone" marker keeps it off the "unknown" (0) fallback.
		{"LGAI-EXAONE/EXAONE-4.0-32B", 128_000},
		{"exaone-3.5-32b-instruct", 128_000},
		// Reka Core/Flash/Edge (and the open-weight Reka Flash 3 refresh) expose a
		// 128k window; the "reka" marker keeps these ids off the "unknown" (0)
		// fallback.
		{"reka-flash-3", 128_000},
		{"reka-core-20240501", 128_000},
		{"rekaai/reka-flash-3", 128_000},
		// deepseek-chat (V3 non-thinking) and deepseek-reasoner (thinking) both
		// expose a 128k window on the official API.
		{"deepseek-chat", 131_072},
		{"deepseek-reasoner", 131_072},
		{"deepseek/deepseek-chat-v3.1", 131_072},
		// DeepSeek-R1-Distill models inherit their base's 131k window. The Qwen
		// variants must NOT fall through to the bare "qwen" rule (32k) and the
		// Llama variants must resolve to the same 131k as their Qwen siblings
		// rather than the "llama" family's 128k — the dedicated distill rule
		// claims both ahead of the family rules.
		{"deepseek-r1-distill-qwen-32b", 131_072},
		{"deepseek-r1-distill-qwen-1.5b", 131_072},
		{"deepseek/deepseek-r1-distill-llama-70b", 131_072},
		// MiniMax-01 and MiniMax-M1 ship a 1M native window; their ids carry no
		// broader family marker, so each resolves via the dedicated rule rather
		// than falling through to "unknown" (0).
		{"minimax/minimax-01", 1_000_000},
		{"MiniMax-M1-80k", 1_000_000},
		{"minimax-text-01", 1_000_000},
		// The MiniMax-M2 agentic-coding line ships a much smaller 204,800 window, so
		// the dedicated "minimax-m2" rule must claim it ahead of the 1M family rule —
		// both the base id and its point releases (M2.1/M2.5/M2.7), and via an
		// OpenRouter vendor prefix.
		{"minimax-m2", 204_800},
		{"MiniMax-M2", 204_800},
		{"minimax/minimax-m2.7", 204_800},
		// Baidu ERNIE 4.5 exposes a 128k window via its own rule.
		{"ernie-4.5-300b-a47b", 131_072},
		{"baidu/ernie-4.5-21b-a3b", 131_072},
		// Tencent Hunyuan exposes a 256k window via its own rule; the id carries no
		// broader family marker, so without it these fall through to 0.
		{"hunyuan-a13b-instruct", 262_144},
		{"tencent/hunyuan-large", 262_144},
		// ByteDance Doubao (Volcengine Ark): the classic pro/lite endpoints encode
		// the window in the id, so the size suffix wins over the family default; the
		// Doubao-1.5 generation reuses the suffix in both dot and dash spellings; and
		// the suffix-less Doubao-Seed line is 256k. A bare Doubao id falls back to the
		// conservative 128k family default. Without these rules every Doubao id falls
		// through to "unknown" (0).
		{"doubao-pro-256k", 262_144},
		{"doubao-lite-256k", 262_144},
		{"doubao-pro-128k", 131_072},
		{"doubao-pro-32k", 32_768},
		{"doubao-lite-32k", 32_768},
		{"doubao-pro-4k", 4_096},
		{"doubao-1.5-pro-256k", 262_144},
		{"doubao-1-5-pro-32k", 32_768},
		{"doubao-seed-1.6", 262_144},
		{"doubao-seed-1-6-flash", 262_144},
		{"doubao-1.5-vision-pro", 131_072},
		// Amazon Nova (Bedrock): Premier is 1M, Pro/Lite are 300k, Micro is 128k.
		// Premier and Micro carry the "nova" marker, so their specific rules must
		// win over the 300k family default.
		{"nova-premier-v1", 1_000_000},
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
		{"grok-code-fast-1", 256_000},
		// The grok-4-fast line lifted the window to 2M. Its id also carries the
		// "grok-4" marker, so its rule must win over grok-4's 256k rule.
		{"grok-4-fast", 2_000_000},
		{"grok-4-fast-reasoning", 2_000_000},
		// The grok-4.1-fast refresh keeps the 2M window but its dotted id breaks the
		// literal "grok-4-fast" substring, so it needs its own rule to avoid falling
		// through to grok-4's 256k.
		{"grok-4.1-fast", 2_000_000},
		{"grok-4.1-fast-non-reasoning", 2_000_000},
		// xAI's own API serves the 4.1-fast line with a dashed id ("grok-4-1-fast-...")
		// rather than the dotted OpenRouter slug, and the dashed form matches neither
		// "grok-4-fast" nor "grok-4.1-fast"; without its own rule it falls through to
		// grok-4's 256k — an ~8x undercount of the real 2M window.
		{"grok-4-1-fast-reasoning", 2_000_000},
		{"grok-4-1-fast-non-reasoning", 2_000_000},
		// Plain grok-4.1 (non-fast) carries only the bare "grok-4" marker and keeps
		// the 256k window, so it must resolve via the grok-4 rule, not grok-4.1-fast's.
		// Its dashed equivalent ("grok-4-1") likewise resolves via grok-4.
		{"grok-4.1", 256_000},
		{"grok-4-1", 256_000},
		{"sonar", 128_000},
		{"sonar-pro", 200_000},
		{"codestral-latest", 256_000},
		// Moonshot Kimi: the K2-0905 refresh, K2-Thinking, and K2.6 (spelled with
		// both a dot and a dash across providers) serve a 256k window, so their
		// specific rules must win over the 128k "kimi" family default that covers
		// the original K2 (0711) and the kimi-k1.5 line.
		{"kimi-k2-0905-preview", 262_144},
		{"kimi-k2-thinking", 262_144},
		{"kimi-k2.6", 262_144},
		{"kimi-k2-6-instruct", 262_144},
		{"kimi-k2-instruct", 128_000},
		{"kimi-k1.5", 128_000},
		// Moonshot's legacy moonshot-v1 line encodes its window in the id; each
		// variant must resolve to its own size rather than falling through to 0.
		{"moonshot-v1-8k", 8_192},
		{"moonshot-v1-32k", 32_768},
		{"moonshot-v1-128k", 131_072},
		{"moonshot-v1-auto", 131_072},
		{"gpt-oss-120b", 128_000},
		// Pixtral, Gemma, Cohere Command, GLM and Nemotron open-weight families.
		{"pixtral-large-latest", 128_000},
		{"gemma-3-27b-it", 128_000},
		// Gemma 3n (on-device variant) ships a 32k window despite carrying the
		// "gemma-3" marker, so its specific rule must win over the 128k family rule.
		{"gemma-3n-e4b-it", 32_768},
		{"gemma-3n-e2b", 32_768},
		{"gemma-2-9b-it", 8_192},
		{"command-a-03-2025", 256_000},
		{"command-r-plus", 128_000},
		{"glm-4-plus", 128_000},
		{"glm-4.5", 128_000},
		// GLM-4.6 lifted its window to 200k; its id carries the "glm" marker, so the
		// specific rule must win over the 128k family default.
		{"glm-4.6", 200_000},
		{"nemotron-4-340b", 128_000},
		// Upstage Solar (via OpenRouter, "upstage/solar-..."): Pro 3 is 128k, Pro 2
		// is 64k, and the original Pro / Mini line stays at 32k. The specific
		// "solar-pro-3"/"solar-pro-2" rules must win over the 32k "solar" family
		// default, mirroring the other version-tiered families above.
		{"upstage/solar-pro-3", 131_072},
		{"solar-pro-3", 131_072},
		{"upstage/solar-pro-2", 65_536},
		{"solar-pro-2", 65_536},
		{"solar-pro", 32_768},
		{"solar-mini", 32_768},
		// Writer Palmyra: the X5 flagship is 1M, the X4 and rest of the line 128k.
		// The specific "palmyra-x5" rule must win over the 128k family default, and
		// the family default covers X4 and the Bedrock/OpenRouter-namespaced ids.
		{"palmyra-x5", 1_000_000},
		{"writer/palmyra-x5", 1_000_000},
		{"palmyra-x4", 128_000},
		{"palmyra-med", 128_000},
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

// TestInferAnthropicMaxOutput verifies the per-model output-cap heuristic that
// drives the Anthropic provider's default max_tokens, including the specific
// markers that must win over their broader family prefix and the unknown-id
// fallthrough to zero.
func TestInferAnthropicMaxOutput(t *testing.T) {
	cases := []struct {
		id   string
		want int
	}{
		// Opus 4.5 lifted its output cap to 64k, and every Opus release since holds
		// there, so the "claude-opus-4" family now defaults to 64k while the two
		// legacy 32k models (4.0 and 4.1) are carved out specifically.
		{"claude-opus-4-5", 64_000},
		{"claude-opus-4-5-20251101", 64_000},
		// Newer Opus point releases inherit the 64k family default rather than
		// falling through to the legacy 32k cap.
		{"claude-opus-4-6", 64_000},
		{"claude-opus-4-7", 64_000},
		{"claude-opus-4-8", 64_000},
		{"claude-opus-4-8-20260101", 64_000},
		// The legacy 32k Opus models stay at 32k via their specific markers: the
		// 4.0 dated id, the 4.0 "-0" alias, and 4.1 (alias and dated forms).
		{"claude-opus-4-20250514", 32_000},
		{"claude-opus-4-0", 32_000},
		{"claude-opus-4-1", 32_000},
		{"claude-opus-4-1-20250805", 32_000},
		{"claude-sonnet-4-20250514", 64_000},
		{"claude-sonnet-4-5", 64_000},
		{"claude-haiku-4-5", 64_000},
		{"claude-3-7-sonnet-20250219", 64_000},
		// The 3.5 line (Sonnet and Haiku) caps at 8k output.
		{"claude-3-5-sonnet-20241022", 8_192},
		{"claude-3-5-haiku-20241022", 8_192},
		// Case-insensitive and whitespace-tolerant.
		{"  CLAUDE-SONNET-4-5  ", 64_000},
		// Older Claude 3 and unknown ids fall through to zero, leaving the caller on
		// the conservative flat default.
		{"claude-3-opus-20240229", 0},
		{"gpt-4o", 0},
		{"", 0},
	}

	for _, tc := range cases {
		t.Run(tc.id, func(t *testing.T) {
			require.Equal(t, tc.want, inferAnthropicMaxOutput(tc.id),
				"inferAnthropicMaxOutput(%q)", tc.id)
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
		{ID: "gemini-flash-latest"},
		{ID: "gemini-pro-latest"},
	}

	cases := []struct {
		id   string
		want bool
	}{
		{"gemini-2.5-flash", true},
		{"gemini-3-pro-preview", true},
		// Rolling "-latest" aliases resolve to the thinking-capable Gemini 2.5
		// generation.
		{"gemini-flash-latest", true},
		{"gemini-pro-latest", true},
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

// TestModelSupportsAnthropic1MContext verifies the 1M-context beta is gated on
// both a 1M-capable Claude Sonnet 4 id and an opted-in context_window above the
// standard 200k window.
func TestModelSupportsAnthropic1MContext(t *testing.T) {
	cases := []struct {
		name   string
		models []Model
		id     string
		want   bool
	}{
		{
			name:   "sonnet 4 with 1M window opts in",
			models: []Model{{ID: "claude-sonnet-4-5", ContextWindow: 1_000_000}},
			id:     "claude-sonnet-4-5",
			want:   true,
		},
		{
			name:   "sonnet 4 base id with 1M window opts in",
			models: []Model{{ID: "claude-sonnet-4-20250514", ContextWindow: 1_000_000}},
			id:     "claude-sonnet-4-20250514",
			want:   true,
		},
		{
			name:   "sonnet 4 at standard window stays off",
			models: []Model{{ID: "claude-sonnet-4-5", ContextWindow: 200_000}},
			id:     "claude-sonnet-4-5",
			want:   false,
		},
		{
			name:   "legacy opus is not 1M-capable even with large window",
			models: []Model{{ID: "claude-opus-4-1", ContextWindow: 1_000_000}},
			id:     "claude-opus-4-1",
			want:   false,
		},
		{
			name:   "opus 4.8 with 1M window opts in",
			models: []Model{{ID: "claude-opus-4-8", ContextWindow: 1_000_000}},
			id:     "claude-opus-4-8",
			want:   true,
		},
		{
			name:   "opus 4.8 at standard window stays off",
			models: []Model{{ID: "claude-opus-4-8", ContextWindow: 200_000}},
			id:     "claude-opus-4-8",
			want:   false,
		},
		{
			name:   "unknown model id is off",
			models: []Model{{ID: "claude-sonnet-4-5", ContextWindow: 1_000_000}},
			id:     "claude-sonnet-4-5-missing",
			want:   false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, modelSupportsAnthropic1MContext(tc.models, tc.id))
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
