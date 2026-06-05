package llm

import (
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// reasoningModelPrefixes lists the OpenAI model-id prefixes whose models run a
// hidden reasoning pass and reject the classic sampling controls (notably
// temperature). The match is case-insensitive on the model id, so callers
// configure "o1", "o3-mini", etc. and the request builder omits unsupported
// params automatically. The gpt-5 family is handled separately in
// isReasoningModel so its one non-reasoning variant can be carved out.
var reasoningModelPrefixes = []string{
	"o1",
	"o3",
	"o4",
}

// gpt5ChatPrefix names the chat-tuned gpt-5 variant (gpt-5-chat-latest), the
// one member of the gpt-5 family that is NOT a reasoning model: it keeps the
// classic temperature and max_tokens params. isReasoningModel treats every
// other gpt-5 model (gpt-5, gpt-5-mini, gpt-5-nano, gpt-5-codex, ...) as a
// reasoning model and excludes ids carrying this prefix.
const gpt5ChatPrefix = "gpt-5-chat"

// isReasoningModel reports whether id names an OpenAI reasoning model (the
// o-series, or a gpt-5 model other than gpt-5-chat). Reasoning models reject
// params such as temperature, so the OpenAI request builder gates those by this
// check rather than sending values the API would 400 on.
func isReasoningModel(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
	// The whole gpt-5 family runs a hidden reasoning pass except gpt-5-chat, so
	// match the family by prefix and carve out the chat variant. This also
	// covers point releases such as gpt-5.5 that share the prefix.
	if strings.HasPrefix(lid, "gpt-5") && !strings.HasPrefix(lid, gpt5ChatPrefix) {
		return true
	}
	for _, prefix := range reasoningModelPrefixes {
		if lid == prefix || strings.HasPrefix(lid, prefix+"-") {
			return true
		}
	}
	return false
}

// thinkingModelSubstrings lists case-insensitive markers in Anthropic model ids
// whose models support extended thinking (Claude 3.7 Sonnet and the Claude 4
// families). The Anthropic provider only emits the thinking request field for a
// configured model whose id matches one of these markers, so a caller that opts
// into thinking against a non-thinking model does not trigger a 400.
var thinkingModelSubstrings = []string{
	"claude-3-7-sonnet",
	"claude-sonnet-4",
	"claude-opus-4",
	"claude-haiku-4",
}

// modelSupportsThinking reports whether the configured model named by id is an
// Anthropic model that supports extended thinking. The match is on the model id
// so callers configure ids such as "claude-sonnet-4-20250514" and the request
// builder gates the thinking field automatically.
func modelSupportsThinking(models []Model, id string) bool {
	if _, ok := findModel(models, id); !ok {
		return false
	}
	lid := strings.ToLower(strings.TrimSpace(id))
	for _, marker := range thinkingModelSubstrings {
		if strings.Contains(lid, marker) {
			return true
		}
	}
	return false
}

// geminiThinkingModelSubstrings lists case-insensitive markers in Gemini model
// ids whose models support the native thinkingConfig (the Gemini 2.5 family and
// the Gemini 3 line, which reasons by default). The Gemini provider only emits
// the thinkingConfig field for a configured model whose id matches one of these
// markers, so opting into a thinking budget on an older model (gemini-1.5,
// gemini-2.0) does not trigger a 400.
var geminiThinkingModelSubstrings = []string{
	"gemini-2.5",
	"gemini-3",
}

// modelSupportsGeminiThinking reports whether the configured model named by id is
// a Gemini model that supports the native thinkingConfig. The match is on the
// model id so callers configure ids such as "gemini-2.5-flash" and the request
// builder gates the thinkingConfig field automatically.
func modelSupportsGeminiThinking(models []Model, id string) bool {
	if _, ok := findModel(models, id); !ok {
		return false
	}
	lid := strings.ToLower(strings.TrimSpace(id))
	for _, marker := range geminiThinkingModelSubstrings {
		if strings.Contains(lid, marker) {
			return true
		}
	}
	return false
}

// contextWindowRule maps a case-insensitive model-id substring to the context
// window (in tokens) of the family it identifies. inferContextWindow walks the
// rules in order and returns the first match, so more specific markers must
// precede the broader ones that would also match them (e.g. "gemini-1.5-pro"
// before "gemini-1.5", "gpt-4o" before "gpt-4"). The values track each family's
// published maximum input window as of the catalog in defaults/config.json.
var contextWindowRules = []struct {
	substring string
	window    int
}{
	// OpenAI
	{"gpt-4.1", 1_047_576},
	// GPT-4.5 ships a 128k window. Its id contains the "gpt-4" marker, so its
	// specific rule must precede the bare "gpt-4" family rule (8k) below, just
	// as "gpt-4.1" does.
	{"gpt-4.5", 128_000},
	{"gpt-4o", 128_000},
	{"gpt-4-turbo", 128_000},
	{"gpt-4", 8_192},
	{"gpt-3.5", 16_385},
	{"gpt-5", 400_000},
	{"o1", 200_000},
	{"o3", 200_000},
	{"o4", 200_000},
	// OpenAI open-weight gpt-oss models ship a 128k window. None of the broader
	// "gpt" markers is a prefix of "gpt-oss", so placement here is for grouping.
	{"gpt-oss", 128_000},
	// Anthropic — every shipping Claude model exposes a 200k window.
	{"claude", 200_000},
	// Google Gemini — 1.5 Pro is 2M, the rest of the 1.5/2.x line is 1M, and the
	// Gemini 3 line keeps the 1M window. "gemini-3" shares no substring with the
	// older rules, so without its own rule it would fall through to "unknown" (0).
	{"gemini-1.5-pro", 2_097_152},
	{"gemini-1.5", 1_048_576},
	{"gemini-2", 1_048_576},
	{"gemini-3", 1_048_576},
	// xAI Grok — Grok 4 and the grok-code coding line lifted the window to 256k,
	// while the grok-2/3 line stays at 131k. Both ids carry the "grok" marker, so
	// the specific "grok-4"/"grok-code" rules must precede the family one to avoid
	// falling through to 131k.
	{"grok-4", 256_000},
	{"grok-code", 256_000},
	{"grok", 131_072},
	// Perplexity Sonar — the pro tier is 200k, the rest 128k, so the more
	// specific "sonar-pro" marker must precede the bare "sonar".
	{"sonar-pro", 200_000},
	{"sonar", 128_000},
	// Common open-weight families served via openai_compatible/ollama. The
	// Llama 4 line lifted the window far above the 128k Llama 3.x default —
	// Scout to 10M and Maverick to 1M — and both ids carry the "llama" marker,
	// so the specific "llama-4-scout"/"llama-4" rules must precede the family
	// one to avoid falling through to 128k.
	{"llama-4-scout", 10_485_760},
	{"llama-4", 1_048_576},
	{"llama", 128_000},
	{"mixtral", 32_768},
	// Mistral's Codestral exposes a 256k window, far larger than the rest of the
	// Mistral line, so its specific marker precedes the family one.
	{"codestral", 256_000},
	// Mistral's Pixtral vision variant exposes a 128k window and does not contain
	// the "mistral" marker, so it needs its own rule above the family one.
	{"pixtral", 128_000},
	// Mistral's Ministral edge line (3B/8B) and Devstral coding line both ship a
	// 128k window. Neither id contains the "mistral" marker ("ministral",
	// "devstral"), so without these rules they fall through to "unknown" (0)
	// instead of the family default.
	{"ministral", 128_000},
	{"devstral", 128_000},
	// Magistral is Mistral's reasoning line (Small/Medium); its id carries
	// neither the "mistral" marker nor any other family marker above, so it
	// needs its own rule to avoid falling through to "unknown" (0).
	{"magistral", 128_000},
	{"mistral", 32_768},
	// Alibaba Qwen — the Qwen3 generation lifted the window well past the 32k
	// Qwen2.x default: the Qwen3-Coder line ships a 256k native window (extendable
	// to 1M) and the Qwen3 2507 instruct refresh a 128k window, while the original
	// Qwen3 release and the whole Qwen2.x line stay at 32k. All carry the "qwen"
	// marker, so the specific "qwen3-coder"/"qwen3" rules must precede the family
	// one to avoid falling through to 32k.
	{"qwen3-coder", 262_144},
	{"qwen3", 131_072},
	{"qwen", 32_768},
	// Microsoft Phi open-weight line — Phi-3/Phi-3.5 (mini, small, medium, MoE) as
	// served by hosted providers ship the long-context 128k variant, while Phi-4
	// shipped a 16k window. The "phi-3"/"phi-4" markers are deliberately specific:
	// a bare "phi" rule would also match unrelated ids such as the "dolphin"
	// finetunes, which carry their base model's (Mistral/Llama) window instead.
	{"phi-4", 16_384},
	{"phi-3", 128_000},
	// Databricks DBRX exposes a 32k window.
	{"dbrx", 32_768},
	// IBM Granite — the shipping Granite 3.x instruct line exposes a 128k window.
	{"granite", 128_000},
	// Google Gemma open-weight line — Gemma 3 lifted the window to 128k while
	// Gemma 1/2 shipped 8k, so the specific "gemma-3" marker precedes the family.
	{"gemma-3", 128_000},
	{"gemma", 8_192},
	// Cohere Command — Command A exposes a 256k window, the Command R/R+ tier
	// 128k, so the specific marker precedes the family one.
	{"command-a", 256_000},
	{"command", 128_000},
	// Zhipu GLM-4 and Nvidia Nemotron both expose a 128k window.
	{"glm", 128_000},
	{"nemotron", 128_000},
	// Moonshot Kimi K2 exposes a 200k window.
	{"kimi", 200_000},
	{"deepseek", 65_536},
	// Amazon Nova (commonly served via Bedrock) — Nova Pro and Nova Lite both
	// expose a 300k window while Nova Micro is 128k, so the specific "nova-micro"
	// marker must precede the family one.
	{"nova-micro", 128_000},
	{"nova", 300_000},
	// AI21 Jamba 1.5 (Large/Mini, also Bedrock-served) exposes a 256k window.
	{"jamba", 256_000},
	// Indian-built models served via openai_compatible. Sarvam's flagship (sarvam-m)
	// ships a 32k window; Krutrim's spectre line an 8k window. Neither id contains a
	// broader family marker above, so without these rules they fall through to
	// "unknown" (0) when a user adds them without an explicit context_window.
	{"sarvam", 32_768},
	{"krutrim", 8_192},
}

// inferContextWindow guesses a model's context window from its id when the
// catalog does not specify one. It exists so a user who adds a model to their
// config without a context_window still gets a sensible budget for compaction
// and overflow checks instead of zero (which the agent loop reads as "unknown").
// The match is a case-insensitive substring scan over contextWindowRules; an
// unrecognized id returns 0, preserving the prior "unknown" behavior.
func inferContextWindow(id string) int {
	lid := strings.ToLower(strings.TrimSpace(id))
	for _, rule := range contextWindowRules {
		if strings.Contains(lid, rule.substring) {
			return rule.window
		}
	}
	return 0
}

func modelSupportsTools(models []Model, id string) bool {
	model, ok := findModel(models, id)
	return ok && model.SupportsTools
}

func modelSupportsImages(models []Model, id string) bool {
	model, ok := findModel(models, id)
	return ok && model.SupportsImages
}

func findModel(models []Model, id string) (Model, bool) {
	for _, model := range models {
		if model.ID == id {
			return model, true
		}
	}
	return Model{}, false
}

func hasImages(messages []message.Message) bool {
	for _, msg := range messages {
		for _, block := range msg.Content {
			if _, ok := block.(message.ImageBlock); ok {
				return true
			}
		}
	}
	return false
}
