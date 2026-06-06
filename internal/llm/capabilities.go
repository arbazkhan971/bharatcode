package llm

import (
	"strconv"
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
// gpt-5.1 generation, where it replaced the now-deprecated "minimal" as the
// fastest, no-reasoning setting, and every later point release of the family
// (gpt-5.2, gpt-5.5, ...) carries the same vocabulary: they accept "none" and
// 400 on "minimal". The original gpt-5 family ("gpt-5", "gpt-5-mini", with no
// dotted minor version) and the o-series accept only minimal/low/medium/high and
// 400 on "none", so they stay unmatched. The classification keys on the bare
// model id, so an aggregator's "vendor/model" prefix (e.g. "openai/gpt-5.5") is
// stripped first, mirroring isReasoningModel; the match is the "gpt-5." family
// prefix plus a leading minor version of 1 or greater so a future generation is
// covered without a per-release edit (the failure mode the prior gpt-5.1-only
// match caused: gpt-5.5, the shipped codex default, fell through and had a
// "minimal" config passed to it verbatim, which 400s).
func modelSupportsNoneReasoningEffort(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
	if idx := strings.LastIndex(lid, "/"); idx >= 0 {
		lid = lid[idx+1:]
	}
	const prefix = "gpt-5."
	if !strings.HasPrefix(lid, prefix) {
		return false
	}
	// Read the leading run of digits after "gpt-5." as the minor version. A
	// dotted minor of 1 or more is a 5.1-or-later generation; anything else
	// (no digits, or a 5.0 that never shipped) is not.
	rest := lid[len(prefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return false
	}
	minor, err := strconv.Atoi(rest[:end])
	if err != nil {
		return false
	}
	return minor >= 1
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
	// Claude 4 family. The Opus line shipped a 32k output cap on 4.0 and 4.1, then
	// lifted it to 64k on 4.5 and held there for every Opus release since (4.6,
	// 4.7, 4.8, ...). Match the two legacy 32k models specifically so the broad
	// "claude-opus-4" rule can default the rest of the line — including future
	// point releases — to 64k, mirroring how the Sonnet 4 and Haiku 4 families
	// default to 64k below. Without this, a newer Opus id (e.g. claude-opus-4-8)
	// matches no specific rule, falls through to 32k, and silently truncates long
	// answers at half the model's real output budget.
	{"claude-opus-4-0", 32_000},        // Opus 4.0 alias
	{"claude-opus-4-20250514", 32_000}, // Opus 4.0 dated id
	{"claude-opus-4-1", 32_000},        // Opus 4.1 (alias and dated ids)
	{"claude-opus-4", 64_000},          // Opus 4.5 and every release since
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
// context-1m beta. The Claude Sonnet 4 line (claude-sonnet-4 and
// claude-sonnet-4-5) has offered it since the beta launched, and the Claude
// Opus 4.8 generation joined it. The marker for Opus is deliberately the
// specific "claude-opus-4-8" rather than the bare "claude-opus-4": the earlier
// Opus 4.0/4.1/4.5 releases stayed at the standard 200k window, so a broad
// family marker would wrongly unlock the beta — and bill long-context premium
// rates — for ids that 400 on it. The Haiku line stays at 200k, so it carries
// no marker.
var anthropic1MContextSubstrings = []string{
	"claude-sonnet-4",
	"claude-opus-4-8",
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
	// variants ship only a 128k window, but they are carved out by the gpt-5 chat
	// pre-scan in inferContextWindow rather than a substring rule here: a versioned
	// id like "gpt-5.1-chat-latest" never contains the literal "gpt-5-chat", so a
	// substring rule would miss it and the family rule below would resolve it to
	// more than 3x its real window.
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
	// OpenAI's codex-mini (codex-mini-latest) is the Codex-CLI model, a fine-tune of
	// o4-mini that inherits its 200k window. Its id carries neither an o-series marker
	// nor the "gpt-5" prefix, so without this rule it falls through to "unknown" (0).
	// It is ordered after the gpt-5 family above so a "gpt-5-codex" id still resolves
	// to the 400k gpt-5 window rather than matching here.
	{"codex-mini", 200_000},
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
	// The grok-4.1-fast refresh keeps the 2M window but its id ("grok-4.1-fast")
	// contains neither the literal "grok-4-fast" substring (the dotted version digit
	// breaks it) nor anything but the bare "grok-4" marker, so without its own rule
	// it would fall through to grok-4's 256k — an ~8x undercount. It must precede
	// grok-4 for the same reason grok-4-fast does. The id is spelled two ways across
	// providers: the dotted "grok-4.1-fast" (OpenRouter's "x-ai/grok-4.1-fast" slug)
	// and the dashed "grok-4-1-fast" (the form xAI's own API serves, e.g.
	// "grok-4-1-fast-reasoning"); neither substring contains the other, so both get a
	// rule — without the dashed one the native xAI ids undercount to grok-4's 256k.
	{"grok-4-fast", 2_000_000},
	{"grok-4.1-fast", 2_000_000},
	{"grok-4-1-fast", 2_000_000},
	{"grok-4", 256_000},
	{"grok-code", 256_000},
	{"grok", 131_072},
	// Perplexity Sonar — the pro tier is 200k, the rest 128k, so the more
	// specific "sonar-pro" marker must precede the bare "sonar".
	{"sonar-pro", 200_000},
	{"sonar", 128_000},
	// DeepSeek-R1-Distill models fine-tune a Qwen or Llama base on R1 reasoning
	// traces and inherit the base's 131k window (this is how the official repo and
	// aggregators such as OpenRouter list them). Every distill id carries its base
	// marker — "deepseek-r1-distill-qwen-32b", "deepseek-r1-distill-llama-70b" —
	// so without this rule the Qwen variants fall through to the bare "qwen" rule
	// (32k, a 4x undercount) and the Llama variants to "llama" (128k), resolving
	// siblings inconsistently. The "deepseek-r1-distill" marker is specific enough
	// to claim both, so it precedes the llama and qwen family rules below.
	{"deepseek-r1-distill", 131_072},
	// Common open-weight families served via openai_compatible/ollama. The
	// Llama 4 line lifted the window far above the 128k Llama 3.x default —
	// Scout to 10M and Maverick to 1M — and both ids carry the "llama" marker,
	// so the specific "llama-4-scout"/"llama-4" rules must precede the family
	// one to avoid falling through to 128k.
	{"llama-4-scout", 10_485_760},
	{"llama-4", 1_048_576},
	{"llama", 128_000},
	// Mixtral 8x22B (open-mixtral-8x22b) doubled the window to 64k, twice the 32k
	// of the original 8x7B. Its id carries the "mixtral" marker, so this specific
	// rule must precede the family one to avoid resolving it to half its real
	// window.
	{"mixtral-8x22b", 65_536},
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
	// The Qwen3-Next hybrid-attention line (qwen3-next-80b-a3b) and the Qwen3-VL
	// vision-language line (qwen3-vl-235b-a22b) both ship a 256k native window,
	// twice the 128k of the Qwen3 2507 instruct refresh. Their ids carry only the
	// bare "qwen3" marker, so without these specific rules they fall through to the
	// 131k rule below — a 2x undercount that would compact long context far sooner
	// than the model requires. They must precede "qwen3" for the same reason
	// "qwen3-max"/"qwen3-coder" do.
	{"qwen3-next", 262_144},
	{"qwen3-vl", 262_144},
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
	// served by hosted providers ship the long-context 128k variant, while the
	// flagship Phi-4 (14B) shipped only a 16k window. The Phi-4 refresh lineup is
	// not uniform, though: Phi-4-mini and Phi-4-multimodal both ship a 128k window
	// (8x the flagship) and Phi-4-reasoning/-reasoning-plus a 32k window, so the
	// specific markers must precede the bare "phi-4" family rule to avoid resolving
	// those variants to a fraction of their real window. Phi-4-mini-reasoning is
	// 128k, so the "phi-4-mini" marker is ordered before "phi-4-reasoning" to claim
	// it first rather than letting the 32k reasoning rule undercount it. The
	// "phi-3"/"phi-4" markers are deliberately specific: a bare "phi" rule would
	// also match unrelated ids such as the "dolphin" finetunes, which carry their
	// base model's (Mistral/Llama) window instead.
	{"phi-4-mini", 128_000},
	{"phi-4-multimodal", 128_000},
	{"phi-4-reasoning", 32_768},
	{"phi-4", 16_384},
	{"phi-3", 128_000},
	// Databricks DBRX exposes a 32k window.
	{"dbrx", 32_768},
	// IBM Granite — the shipping Granite 3.x instruct line exposes a 128k window.
	{"granite", 128_000},
	// ByteDance Seed-OSS — the open-weight Seed-OSS-36B line ships a 512k native
	// context window, its headline feature and one of the largest among open
	// models. Its id ("seed-oss-36b-instruct") carries no broader family marker
	// above — "gpt-oss" is not a substring of "seed-oss" — so without this rule it
	// falls through to "unknown" (0) when a user adds it (commonly via OpenRouter
	// or Fireworks) without an explicit context_window, badly undercounting a
	// window that is its main reason for use.
	{"seed-oss", 524_288},
	// LG EXAONE — the hosted EXAONE 4.0 flagship (the 32B model served via
	// OpenRouter/Together/Fireworks) exposes a 128k window, as does the EXAONE
	// 3.5 32B. The "exaone" marker carries no broader family marker above, so
	// without this rule the id falls through to "unknown" (0) when a user adds
	// it without an explicit context_window.
	{"exaone", 128_000},
	// Reka — the Reka Core/Flash/Edge line (and the open-weight Reka Flash 3
	// refresh) exposes a 128k window. The "reka" marker carries no broader
	// family marker above, so without this rule the id falls through to
	// "unknown" (0) when a user adds it (commonly via OpenRouter) without an
	// explicit context_window.
	{"reka", 128_000},
	// Google Gemma open-weight line — Gemma 3 lifted the window to 128k while
	// Gemma 1/2 shipped 8k, so the specific "gemma-3" marker precedes the family.
	// Gemma 3n, the on-device variant (gemma-3n-e2b, gemma-3n-e4b), is the
	// exception to the line above: it ships only a 32k window. Its id carries the
	// "gemma-3" marker, so its specific rule must precede the "gemma-3" family rule
	// to avoid resolving to 128k — a 4x overcount that would let the agent grow
	// context well past the model's real limit before the request is rejected.
	{"gemma-3n", 32_768},
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
	// Moonshot's legacy moonshot-v1 line encodes its window directly in the id —
	// moonshot-v1-8k / -32k / -128k (and the moonshot-v1-auto router, which sizes
	// up to the 128k tier). These ids carry neither the "kimi" marker nor any other
	// family marker above, so without these rules they fall through to "unknown"
	// (0) when a user configures the Moonshot provider (which ships in the default
	// catalog) with one of them. The windows are
	// the 1024-based token counts Moonshot's API documents (128k == 128*1024),
	// mirroring how the gpt-4-32k and gemma 8k rules above resolve their explicit
	// size suffixes.
	{"moonshot-v1-8k", 8_192},
	{"moonshot-v1-32k", 32_768},
	{"moonshot-v1-128k", 131_072},
	{"moonshot-v1-auto", 131_072},
	// Moonshot Kimi — the modern K2 line serves a 256k window. The K2-Instruct
	// "0905" refresh doubled the original K2's 128k window to 256k, and both the
	// K2-Thinking reasoning model and the K2.6 release hold there. Every such id
	// carries the bare "kimi" marker, so these specific rules must precede the
	// family one to avoid falling through to the 128k default — an undercount that
	// would let the agent compact long context far sooner than the model requires.
	// The K2.6 id is spelled both with a dot ("kimi-k2.6") and a dash
	// ("kimi-k2-6") across providers, so both forms get a rule. The original kimi-k2
	// (the "0711" release) and the kimi-k1.5 line stayed at 128k, which the family
	// rule covers.
	{"kimi-k2-0905", 262_144},
	{"kimi-k2-thinking", 262_144},
	{"kimi-k2.6", 262_144},
	{"kimi-k2-6", 262_144},
	{"kimi", 128_000},
	// DeepSeek — the deepseek-chat (V3 non-thinking) and deepseek-reasoner
	// (thinking) models served by the official API both expose a 128k window,
	// up from the 64k the earlier releases shipped.
	{"deepseek", 131_072},
	// MiniMax — the MiniMax-01 and MiniMax-M1 lines ship a 1M native context
	// window (one of the largest among open-weight models). The newer MiniMax-M2
	// agentic-coding line (M2 and its point releases M2.1/M2.5/M2.7) is far
	// smaller at 204,800 tokens, and every M2 id carries the "minimax-m2" marker,
	// so its specific rule must precede the family one to avoid resolving the M2
	// line to nearly 5x its real window — an over-report that would let the agent
	// grow context past the model's true limit before the API rejects it. The
	// family ids carry no broader family marker above, so without these rules they
	// fall through to "unknown" (0) when a user adds them without an explicit
	// context_window.
	{"minimax-m2", 204_800},
	{"minimax", 1_000_000},
	// Baidu ERNIE — the ERNIE 4.5 family exposes a 128k window. Its id carries no
	// broader marker above, so this rule keeps it from falling through to 0.
	{"ernie", 131_072},
	// Tencent Hunyuan — the modern Hunyuan line (Hunyuan-A13B, Hunyuan-Large, and
	// the Hunyuan-TurboS/T1 reasoning models) ships a 256k native context window,
	// its headline long-context feature. The id carries no broader family marker
	// above, so without this rule it falls through to "unknown" (0) when a user
	// adds it (commonly via OpenRouter or the Tencent API) without an explicit
	// context_window.
	{"hunyuan", 262_144},
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
	// Aggregators (OpenRouter, ...) namespace ids as "vendor/model"; strip the
	// vendor prefix so the gpt-5 chat carve-out below keys on the bare id, mirroring
	// isReasoningModel.
	if idx := strings.LastIndex(lid, "/"); idx >= 0 {
		lid = lid[idx+1:]
	}
	// The gpt-5 chat-tuned variants ship a 128k window while the rest of the
	// reasoning gpt-5 family exposes 400k. Detect them the same way isReasoningModel
	// does — the gpt-5 family prefix plus the "chat" marker — so versioned ids such
	// as "gpt-5.1-chat-latest", which no "gpt-5-chat" substring rule would match,
	// resolve to their real window instead of the family's 400k.
	if strings.HasPrefix(lid, "gpt-5") && strings.Contains(lid, gpt5ChatMarker) {
		return 128_000
	}
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
