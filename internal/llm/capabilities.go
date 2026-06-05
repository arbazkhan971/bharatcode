package llm

import (
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// reasoningModelPrefixes lists the OpenAI model-id prefixes whose models run a
// hidden reasoning pass and reject the classic sampling controls (notably
// temperature). The match is case-insensitive on the model id, so callers
// configure "o1", "o3-mini", "gpt-5-reasoning", etc. and the request builder
// omits unsupported params automatically.
var reasoningModelPrefixes = []string{
	"o1",
	"o3",
	"o4",
	"gpt-5-reasoning",
}

// isReasoningModel reports whether id names an OpenAI reasoning model
// (o-series, or a gpt-5 reasoning variant). Reasoning models reject params
// such as temperature, so the OpenAI request builder gates those by this check
// rather than sending values the API would 400 on.
func isReasoningModel(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
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
// ids whose models support the native thinkingConfig (the Gemini 2.5 family).
// The Gemini provider only emits the thinkingConfig field for a configured model
// whose id matches one of these markers, so opting into a thinking budget on an
// older model (gemini-1.5, gemini-2.0) does not trigger a 400.
var geminiThinkingModelSubstrings = []string{
	"gemini-2.5",
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
	// Google Gemini — 1.5 Pro is 2M, the rest of the 1.5/2.x line is 1M.
	{"gemini-1.5-pro", 2_097_152},
	{"gemini-1.5", 1_048_576},
	{"gemini-2", 1_048_576},
	// xAI Grok — the grok-2/3/4 line exposes a 131k window.
	{"grok", 131_072},
	// Perplexity Sonar — the pro tier is 200k, the rest 128k, so the more
	// specific "sonar-pro" marker must precede the bare "sonar".
	{"sonar-pro", 200_000},
	{"sonar", 128_000},
	// Common open-weight families served via openai_compatible/ollama.
	{"llama", 128_000},
	{"mixtral", 32_768},
	// Mistral's Codestral exposes a 256k window, far larger than the rest of the
	// Mistral line, so its specific marker precedes the family one.
	{"codestral", 256_000},
	// Mistral's Pixtral vision variant exposes a 128k window and does not contain
	// the "mistral" marker, so it needs its own rule above the family one.
	{"pixtral", 128_000},
	{"mistral", 32_768},
	{"qwen", 32_768},
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
