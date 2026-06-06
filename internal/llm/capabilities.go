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

// gpt5ChatMarker identifies the chat-tuned members of the gpt-5 family
// (gpt-5-chat-latest, and point releases such as gpt-5.1-chat-latest), the ones
// that are NOT reasoning models: they keep the classic temperature and
// max_tokens params. isReasoningModel treats every other gpt-5 model (gpt-5,
// gpt-5-mini, gpt-5-nano, gpt-5-codex, ...) as a reasoning model and excludes
// ids carrying this marker. A substring (rather than a fixed "gpt-5-chat"
// prefix) is used so a versioned chat variant like "gpt-5.1-chat-latest" — whose
// id does not begin with "gpt-5-chat" — is still carved out instead of having
// its temperature silently dropped.
const gpt5ChatMarker = "chat"

// isReasoningModel reports whether id names an OpenAI reasoning model (the
// o-series, or a gpt-5 model other than gpt-5-chat). Reasoning models reject
// params such as temperature, so the OpenAI request builder gates those by this
// check rather than sending values the API would 400 on.
//
// OpenRouter (and other aggregators reached through the openai_compatible
// dialect) namespace model ids as "vendor/model", e.g. "openai/gpt-5" or
// "openai/o3-mini". The classification keys on the bare model id, so the vendor
// prefix is stripped first; otherwise a prefixed reasoning id would slip through
// as a chat model and the builder would send the temperature/max_tokens the API
// rejects.
func isReasoningModel(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.LastIndex(lid, "/"); idx >= 0 {
		lid = lid[idx+1:]
	}
	// The whole gpt-5 family runs a hidden reasoning pass except the chat-tuned
	// variants, so match the family by prefix and carve out any chat variant.
	// This also covers point releases such as gpt-5.5 (reasoning) and
	// gpt-5.1-chat-latest (chat) that share the family prefix.
	if strings.HasPrefix(lid, "gpt-5") && !strings.Contains(lid, gpt5ChatMarker) {
		return true
	}
	for _, prefix := range reasoningModelPrefixes {
		if lid == prefix || strings.HasPrefix(lid, prefix+"-") {
			return true
		}
	}
	return false
}

// modelSupportsNoneReasoningEffort reports whether the OpenAI model named by id
// accepts the reasoning_effort value "none". OpenAI introduced "none" with the
// gpt-5.1 generation (where it replaces the now-deprecated "minimal" as the
// fastest, no-reasoning setting); the original gpt-5 family and the o-series
// accept only minimal/low/medium/high and 400 on "none". The classification keys
// on the bare model id, so an aggregator's "vendor/model" prefix (e.g.
// "openai/gpt-5.1-codex") is stripped first, mirroring isReasoningModel. Future
// numbered generations (gpt-5.2, ...) are intentionally not matched here: until
// their effort vocabulary is confirmed, "none" is dropped for them rather than
// risking a 400.
func modelSupportsNoneReasoningEffort(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.LastIndex(lid, "/"); idx >= 0 {
		lid = lid[idx+1:]
	}
	return strings.HasPrefix(lid, "gpt-5.1")
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

// anthropicMaxOutputRules maps a case-insensitive Anthropic model-id substring to
// the model's maximum output-token allowance. inferAnthropicMaxOutput walks the
// rules in order and returns the first match, so more specific markers must
// precede the broader ones that would also match them (e.g. "claude-opus-4-5"
// before "claude-opus-4"). The values track each model's published max output as
// of the catalog in defaults/config.json; an unrecognized id returns 0, letting
// the caller fall back to the conservative defaultAnthropicMaxTokens.
//
// This drives the default max_tokens the Anthropic provider sends when a caller
// leaves it unset: the flat 4096 fallback truncated long answers from the modern
// Claude line, which serves 32k–64k output tokens.
var anthropicMaxOutputRules = []struct {
	substring string
	maxOutput int
}{
	// Claude 4 family. Opus 4 and 4.1 cap at 32k output; Opus 4.5 lifted that to
	// 64k, so its specific marker must precede the broader "claude-opus-4" rule.
	{"claude-opus-4-5", 64_000},
	{"claude-opus-4", 32_000},
	{"claude-sonnet-4", 64_000},
	{"claude-haiku-4", 64_000},
	// Claude 3.7 Sonnet serves up to 64k output tokens.
	{"claude-3-7-sonnet", 64_000},
	// The Claude 3.5 line (Sonnet and Haiku) caps at 8k output tokens. The
	// "claude-3-5" marker covers both without a per-variant rule.
	{"claude-3-5", 8_192},
}

// inferAnthropicMaxOutput returns the maximum output-token allowance for the
// Anthropic model named by id, or 0 when the id matches no known family. The
// match is a case-insensitive substring scan over anthropicMaxOutputRules,
// mirroring inferContextWindow. The Anthropic provider uses this to pick a
// sensible default max_tokens so a request that leaves MaxTokens unset is not
// silently capped at the conservative 4096 fallback.
func inferAnthropicMaxOutput(id string) int {
	lid := strings.ToLower(strings.TrimSpace(id))
	for _, rule := range anthropicMaxOutputRules {
		if strings.Contains(lid, rule.substring) {
			return rule.maxOutput
		}
	}
	return 0
}

// anthropic1MContextSubstrings lists case-insensitive markers in Anthropic
// model ids whose models can serve the 1M-token context window behind the
// context-1m beta. Only the Claude Sonnet 4 line (claude-sonnet-4 and
// claude-sonnet-4-5) offers it today; the Opus and Haiku lines stay at the
// standard 200k window, so enabling the beta for them would be a no-op at best.
var anthropic1MContextSubstrings = []string{
	"claude-sonnet-4",
}

// anthropic1MContextThreshold is the standard Claude context window. A configured
// 1M-capable model whose context_window exceeds it is read as a request to use
// the larger window, which modelSupportsAnthropic1MContext unlocks via the
// context-1m beta header.
const anthropic1MContextThreshold = 200_000

// anthropic1MContextBeta is the anthropic-beta token that opts a request into the
// 1M-token context window on the Claude Sonnet 4 line.
const anthropic1MContextBeta = "context-1m-2025-08-07"

// modelSupportsAnthropic1MContext reports whether the configured model named by
// id is a 1M-capable Claude model the user has opted into the larger window for,
// by setting a context_window above the standard 200k. The beta is gated on that
// explicit config rather than enabled by default because the portion of a request
// above 200k input tokens bills at a premium long-context rate, so a user must
// ask for it. The model-id match is an approximate substring scan, mirroring the
// other Anthropic capability checks.
func modelSupportsAnthropic1MContext(models []Model, id string) bool {
	model, ok := findModel(models, id)
	if !ok || model.ContextWindow <= anthropic1MContextThreshold {
		return false
	}
	lid := strings.ToLower(strings.TrimSpace(id))
	for _, marker := range anthropic1MContextSubstrings {
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
	// Rolling "-latest" aliases resolve to the current Gemini 2.5 generation,
	// which supports the native thinkingConfig. They carry no version digit, so
	// the numbered markers above do not match them; list them explicitly so a
	// thinking budget configured against an alias id is honored rather than
	// silently dropped.
	"gemini-flash-latest",
	"gemini-flash-lite-latest",
	"gemini-pro-latest",
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
	// gpt-4-32k is the 32k-window variant of the original GPT-4. Its id
	// contains the "gpt-4" marker, so this specific rule must precede the bare
	// "gpt-4" family rule (8k) to avoid mis-resolving to a quarter of its real
	// window.
	{"gpt-4-32k", 32_768},
	{"gpt-4", 8_192},
	{"gpt-3.5", 16_385},
	// Azure OpenAI deployments name the GPT-3.5 family without the dot
	// ("gpt-35-turbo", "gpt-35-turbo-16k"), which the dotted "gpt-3.5" rule
	// above does not match. The current Azure default ships a 16k window, so
	// this rule keeps the dot-less ids from falling through to "unknown" (0).
	{"gpt-35", 16_385},
	// The reasoning gpt-5 family (gpt-5, gpt-5-mini, gpt-5-nano, gpt-5-codex,
	// and point releases such as gpt-5.1) exposes a 400k window. The chat-tuned
	// variant gpt-5-chat-latest is the exception: it ships only a 128k window.
	// Its id carries the "gpt-5" marker, so this specific rule must precede the
	// family one to avoid resolving the chat model to more than 3x its real
	// window — the same carve-out pattern as gpt-4.5 before gpt-4.
	{"gpt-5-chat", 128_000},
	{"gpt-5", 400_000},
	// OpenAI o-series. The released o1 (and o3/o4-mini) expose a 200k window, but
	// the earlier o1-preview and o1-mini shipped only 128k. Both carry the "o1"
	// marker, so their specific rules must precede the family one to avoid falling
	// through to 200k.
	{"o1-preview", 128_000},
	{"o1-mini", 128_000},
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
	// Google's rolling "-latest" aliases (gemini-flash-latest,
	// gemini-flash-lite-latest, gemini-pro-latest) track the current Gemini 2.5
	// generation, all of which expose a 1M window. Their ids carry a version
	// digit (the alias is the whole point), so they share no substring with the
	// numbered rules above and would otherwise fall through to "unknown" (0).
	{"gemini-flash-latest", 1_048_576},
	{"gemini-flash-lite-latest", 1_048_576},
	{"gemini-pro-latest", 1_048_576},
	// xAI Grok — Grok 4 and the grok-code coding line lifted the window to 256k,
	// while the grok-2/3 line stays at 131k. Both ids carry the "grok" marker, so
	// the specific "grok-4"/"grok-code" rules must precede the family one to avoid
	// falling through to 131k. The grok-4-fast line lifted the window again to 2M;
	// its id also carries the "grok-4" marker, so its rule must precede grok-4's.
	{"grok-4-fast", 2_000_000},
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
	// Mistral Large 2 (mistral-large-latest) lifted the window to 128k, far
	// above the 32k of the original Large and the Mistral 7B/8x7B line. Its id
	// carries the "mistral" marker, so this specific rule must precede the
	// family one to avoid falling through to 32k.
	{"mistral-large", 128_000},
	// Mistral Medium 3 (mistral-medium-latest) and Mistral NeMo
	// (open-mistral-nemo) both ship a 128k window, four times the 32k Mistral
	// 7B/8x7B family default. Both ids carry the "mistral" marker, so these
	// specific rules must precede the family one to avoid falling through to 32k.
	{"mistral-medium", 128_000},
	{"mistral-nemo", 128_000},
	// Mistral Small 3 / 3.1 / 3.2 (mistral-small-2501/2503/2506, and the
	// mistral-small-latest alias that now tracks them) ship a 128k window, four
	// times the 32k of the original Mistral Small 2402. The id carries the
	// "mistral" marker, so this specific rule must precede the family one to
	// avoid resolving the current Small line to a quarter of its real window.
	{"mistral-small", 128_000},
	{"mistral", 32_768},
	// Alibaba Qwen — the Qwen3 generation lifted the window well past the 32k
	// Qwen2.x default: the Qwen3-Max flagship and Qwen3-Coder line ship a 256k
	// native window (extendable to 1M) and the Qwen3 2507 instruct refresh a 128k
	// window, while the original Qwen3 release and the whole Qwen2.x line stay at
	// 32k. All carry the "qwen" marker, so the specific "qwen3-max"/"qwen3-coder"/
	// "qwen3" rules must precede the family one to avoid falling through to 32k.
	{"qwen3-max", 262_144},
	{"qwen3-coder", 262_144},
	{"qwen3", 131_072},
	{"qwen", 32_768},
	// Qwen's QwQ reasoning line and QVQ vision-reasoning line both ship a 128k
	// window, but their ids ("qwq-32b", "qvq-72b-preview") carry neither the
	// "qwen" marker nor any other family marker above, so without these rules
	// they fall through to "unknown" (0) when a user adds them — commonly via
	// OpenRouter or a local runtime — without an explicit context_window.
	{"qwq", 131_072},
	{"qvq", 131_072},
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
	// LG EXAONE — the hosted EXAONE 4.0 flagship (the 32B model served via
	// OpenRouter/Together/Fireworks) exposes a 128k window, as does the EXAONE
	// 3.5 32B. The "exaone" marker carries no broader family marker above, so
	// without this rule the id falls through to "unknown" (0) when a user adds
	// it without an explicit context_window.
	{"exaone", 128_000},
	// Google Gemma open-weight line — Gemma 3 lifted the window to 128k while
	// Gemma 1/2 shipped 8k, so the specific "gemma-3" marker precedes the family.
	{"gemma-3", 128_000},
	{"gemma", 8_192},
	// Cohere Command — Command A exposes a 256k window, the Command R/R+ tier
	// 128k, so the specific marker precedes the family one.
	{"command-a", 256_000},
	{"command", 128_000},
	// Zhipu GLM-4 and Nvidia Nemotron both expose a 128k window. GLM-4.6 lifted
	// its window to 200k, so its specific marker must precede the family rule to
	// avoid falling through to 128k.
	{"glm-4.6", 200_000},
	{"glm", 128_000},
	{"nemotron", 128_000},
	// Moonshot Kimi K2 exposes a 200k window.
	{"kimi", 200_000},
	// DeepSeek — the deepseek-chat (V3 non-thinking) and deepseek-reasoner
	// (thinking) models served by the official API both expose a 128k window,
	// up from the 64k the earlier releases shipped.
	{"deepseek", 131_072},
	// MiniMax — the MiniMax-01 and MiniMax-M1 lines ship a 1M native context
	// window (one of the largest among open-weight models). Their ids carry no
	// broader family marker above, so without this rule they fall through to
	// "unknown" (0) when a user adds them without an explicit context_window.
	{"minimax", 1_000_000},
	// Baidu ERNIE — the ERNIE 4.5 family exposes a 128k window. Its id carries no
	// broader marker above, so this rule keeps it from falling through to 0.
	{"ernie", 131_072},
	// Amazon Nova (commonly served via Bedrock) — Nova Pro and Nova Lite both
	// expose a 300k window while Nova Micro is 128k, and Nova Premier lifted the
	// window to 1M. The "nova-premier" and "nova-micro" ids both carry the "nova"
	// marker, so their specific rules must precede the family one to avoid falling
	// through to 300k.
	{"nova-premier", 1_000_000},
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
