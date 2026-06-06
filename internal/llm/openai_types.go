package llm

import "encoding/json"

type openAIChatRequest struct {
	Model       string          `json:"model"`
	Messages    []openAIMessage `json:"messages"`
	Tools       []openAITool    `json:"tools,omitempty"`
	Stream      bool            `json:"stream"`
	Temperature float64         `json:"temperature,omitempty"`
	MaxTokens   int             `json:"max_tokens,omitempty"`
	// MaxCompletionTokens is the output cap for OpenAI reasoning models
	// (o-series, gpt-5 reasoning), which reject the legacy max_tokens field.
	// Exactly one of MaxTokens / MaxCompletionTokens is set per request, gated
	// by model id; both are omitempty so the unused one drops out of the body.
	MaxCompletionTokens int `json:"max_completion_tokens,omitempty"`
	// StreamOptions carries streaming-only flags. It is set only on streaming
	// requests so the provider returns token usage in the final stream chunk;
	// it is omitted otherwise.
	StreamOptions *openAIStreamOptions `json:"stream_options,omitempty"`
	// ReasoningEffort is sent only for OpenAI reasoning models (o-series,
	// gpt-5 reasoning) when the request specifies one. It is omitted for
	// non-reasoning models and when empty.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Reasoning is OpenRouter's unified extended-thinking control. Unlike the
	// OpenAI-only reasoning_effort/max_completion_tokens fields, it turns on
	// reasoning across every upstream OpenRouter proxies (Anthropic, Gemini,
	// Grok, DeepSeek). It is set only for OpenRouter providers and omitted
	// otherwise so other openai_compatible backends never receive a field they
	// would reject.
	Reasoning *openAIReasoning `json:"reasoning,omitempty"`
}

// openAIReasoning is OpenRouter's reasoning request object. At most one of
// Effort ("low"/"medium"/"high"), MaxTokens (a thinking-token budget), or
// Enabled (toggle reasoning at the upstream's own default) is set per request;
// all are omitempty so the unused ones drop out of the body. An empty object
// would enable provider-default reasoning, so the builder leaves Reasoning nil
// unless a budget or effort was configured.
type openAIReasoning struct {
	Effort    string `json:"effort,omitempty"`
	MaxTokens int    `json:"max_tokens,omitempty"`
	// Enabled toggles reasoning without pinning an effort label or token budget.
	// A true value carries the "auto"/"dynamic" effort intent (let the upstream
	// size its own reasoning), which has no OpenRouter effort label (sending
	// effort:"auto" would 400); a false value carries the "none" intent (turn
	// reasoning off), which likewise has no OpenRouter effort label (sending
	// effort:"none" would 400). It is a pointer so an explicit false is emitted
	// as enabled:false rather than dropped by omitempty, which would leave the
	// upstream's default reasoning in place — the opposite of what "none" asks.
	Enabled *bool `json:"enabled,omitempty"`
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
	Content string `json:"content"`
	// ReasoningContent carries a reasoning model's visible thinking text in the
	// field name used by DeepSeek's direct API and the providers that copied it
	// (e.g. some Together/Fireworks deployments).
	ReasoningContent string `json:"reasoning_content"`
	// Reasoning is the field name OpenRouter normalizes every upstream
	// reasoning model's thinking text into (DeepSeek R1, o-series, Gemini, ...).
	// Native OpenAI/DeepSeek never set it, so reading both fields lets a single
	// reasoning model surface its thinking whether it is reached directly or via
	// OpenRouter, instead of the OpenRouter path dropping it silently.
	Reasoning string                `json:"reasoning"`
	ToolCalls []openAIToolCallDelta `json:"tool_calls"`
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
			Content string `json:"content"`
			// ReasoningContent / Reasoning carry a reasoning model's thinking text
			// in a buffered (non-streamed) completion, mirroring the two field names
			// the streaming delta reads: reasoning_content is DeepSeek's direct API
			// (and the deployments that copied it), reasoning is the field OpenRouter
			// normalizes every upstream reasoning model into. A provider that returns
			// a buffered JSON body instead of an SSE stream would otherwise drop the
			// thinking text that the stream path surfaces.
			ReasoningContent string                  `json:"reasoning_content"`
			Reasoning        string                  `json:"reasoning"`
			ToolCalls        []openAIMessageToolCall `json:"tool_calls"`
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
	// PromptTokensDetails carries the OpenAI-standard cache breakdown. Native
	// OpenAI (and spec-compliant relays such as OpenRouter, Groq, Together) do
	// not emit the flat cache fields above; they report prompt-cache hits only
	// under prompt_tokens_details.cached_tokens, which is a subset already
	// counted in PromptTokens.
	PromptTokensDetails *openAIPromptTokensDetails `json:"prompt_tokens_details,omitempty"`
}

// openAIPromptTokensDetails is the nested cache breakdown of the OpenAI Chat
// Completions usage object. Only the cached-token count is consumed; audio and
// other modality fields are ignored.
type openAIPromptTokensDetails struct {
	CachedTokens int `json:"cached_tokens"`
}

func (u openAIUsage) toUsage() Usage {
	cacheRead := u.CacheReadInputTokens
	if cacheRead == 0 {
		cacheRead = u.PromptCacheHitTokens
	}
	if cacheRead == 0 && u.PromptTokensDetails != nil {
		cacheRead = u.PromptTokensDetails.CachedTokens
	}
	cacheWrite := u.CacheCreationInputToken
	if cacheWrite == 0 {
		cacheWrite = u.PromptCacheMissTokens
	}
	// prompt_tokens is the total prompt size *including* the cached tokens
	// (OpenAI reports cache hits as a subset under prompt_tokens_details, and
	// DeepSeek's prompt_cache_hit_tokens + prompt_cache_miss_tokens sum to
	// prompt_tokens), whereas the ledger prices InputTokens and CacheReadTokens
	// additively (the Anthropic convention, where input_tokens already excludes
	// the cached portion). Subtract the cached tokens back out so they are billed
	// once at the cache rate rather than twice — once at the full input rate and
	// again at the cache rate. Clamp at zero to stay robust against a malformed
	// response where the cached count somehow exceeds the prompt total. Mirrors
	// the same correction applied on the Gemini path.
	input := u.PromptTokens - cacheRead
	if input < 0 {
		input = 0
	}
	return Usage{
		InputTokens:      input,
		OutputTokens:     u.CompletionTokens,
		CacheReadTokens:  cacheRead,
		CacheWriteTokens: cacheWrite,
	}
}
