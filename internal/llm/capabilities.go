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
