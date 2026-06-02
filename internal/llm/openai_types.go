package llm

import "encoding/json"

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	// StreamOptions carries streaming-only flags. It is set only on streaming
	// requests so the provider returns token usage in the final stream chunk;
	// it is omitted otherwise.
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	// ReasoningEffort is sent only for OpenAI reasoning models (o-series,
	// gpt-5 reasoning) when the request specifies one. It is omitted for
	// non-reasoning models and when empty.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
}

// openAIStreamOptions toggles streaming extras. IncludeUsage asks the provider
// to append a final chunk carrying prompt/completion token counts, which
// OpenAI otherwise omits from streamed responses.
type openAIStreamOptions struct {
	IncludeUsage bool `json:"include_usage"`
}

type openAIMessage struct {
	Role       string                  `json:"role"`
	Content    any                     `json:"content,omitempty"`
	ToolCallID string                  `json:"tool_call_id,omitempty"`
	ToolCalls  []openAIMessageToolCall `json:"tool_calls,omitempty"`
	// Images carries top-level base64 image data for Ollama's /api/chat,
	// which does not use OpenAI-style image_url content parts. It is omitted
	// for OpenAI-compatible providers.
	Images []string `json:"images,omitempty"`
}

// openAIContentPart is one element of a multimodal message content array. A
// text part sets Text; an image part sets Type "image_url" and ImageURL.
type openAIContentPart struct {
	Type     string          `json:"type"`
	Text     string          `json:"text,omitempty"`
	ImageURL *openAIImageURL `json:"image_url,omitempty"`
}

// openAIImageURL carries an image reference, here always an inline data URL.
type openAIImageURL struct {
	URL string `json:"url"`
}

type openAIMessageToolCall struct {
	ID       string                    `json:"id"`
	Type     string                    `json:"type"`
	Function openAIMessageToolFunction `json:"function"`
}

type openAIMessageToolFunction struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAITool struct {
	Type     string         `json:"type"`
	Function openAIFunction `json:"function"`
}

type openAIFunction struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type openAIStreamChunk struct {
	Choices []struct {
		Delta openAIStreamDelta `json:"delta"`
	} `json:"choices"`
	Usage *openAIUsage `json:"usage,omitempty"`
}

type openAIStreamDelta struct {
	Content          string                `json:"content"`
	ReasoningContent string                `json:"reasoning_content"`
	ToolCalls        []openAIToolCallDelta `json:"tool_calls"`
}

type openAIToolCallDelta struct {
	Index    *int                `json:"index"`
	ID       string              `json:"id"`
	Type     string              `json:"type"`
	Function openAIFunctionDelta `json:"function"`
}

type openAIFunctionDelta struct {
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

type openAIChatResponse struct {
	Choices []struct {
		Message struct {
			Content   string                  `json:"content"`
			ToolCalls []openAIMessageToolCall `json:"tool_calls"`
		} `json:"message"`
	} `json:"choices"`
	Usage openAIUsage `json:"usage"`
}

type openAIUsage struct {
	PromptTokens            int `json:"prompt_tokens"`
	CompletionTokens        int `json:"completion_tokens"`
	PromptCacheHitTokens    int `json:"prompt_cache_hit_tokens"`
	PromptCacheMissTokens   int `json:"prompt_cache_miss_tokens"`
	CacheReadInputTokens    int `json:"cache_read_input_tokens"`
	CacheCreationInputToken int `json:"cache_creation_input_tokens"`
}

func (u openAIUsage) toUsage() Usage {
	cacheRead := u.CacheReadInputTokens
	if cacheRead == 0 {
		cacheRead = u.PromptCacheHitTokens
	}
	cacheWrite := u.CacheCreationInputToken
	if cacheWrite == 0 {
		cacheWrite = u.PromptCacheMissTokens
	}
	return Usage{
		InputTokens:      u.PromptTokens,
		OutputTokens:     u.CompletionTokens,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}
