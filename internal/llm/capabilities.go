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
