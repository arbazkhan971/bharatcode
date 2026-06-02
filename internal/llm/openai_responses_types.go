package llm

// responsesRequest is the POST /v1/responses request body. The Responses API
// lifts the system prompt to the top-level instructions field and replaces the
// chat messages array with an input-items array.
type responsesRequest struct {
	Model           string               `json:"model"`
	Instructions    string               `json:"instructions,omitempty"`
	Input           []responsesInputItem `json:"input"`
	Stream          bool                 `json:"stream"`
	Temperature     float64              `json:"temperature,omitempty"`
	MaxOutputTokens int                  `json:"max_output_tokens,omitempty"`
	// ReasoningEffort is sent only for OpenAI reasoning models when the request
	// specifies one; it is omitted for non-reasoning models and when empty.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// Store controls server-side response retention. The ChatGPT Codex backend
	// requires store=false; the pointer keeps the field omitted (default
	// behavior) for the standard OpenAI Responses path.
	Store *bool `json:"store,omitempty"`
	// Include requests extra output fields (for example
	// "reasoning.encrypted_content" on the Codex backend). Omitted when empty.
	Include []string `json:"include,omitempty"`
}

// responsesInputItem is one element of the Responses input array. A message
// item carries a role plus a content array of typed parts.
type responsesInputItem struct {
	Role    string                 `json:"role"`
	Content []responsesContentPart `json:"content"`
}

// responsesContentPart is one typed content element. Text input uses
// "input_text", assistant text uses "output_text", and images use
// "input_image" with an inline data URL.
type responsesContentPart struct {
	Type     string `json:"type"`
	Text     string `json:"text,omitempty"`
	ImageURL string `json:"image_url,omitempty"`
}

// responsesResponse is the non-streaming POST /v1/responses reply. The
// generated content lives in the output[] array; usage carries token counts.
type responsesResponse struct {
	ID     string                 `json:"id"`
	Object string                 `json:"object"`
	Status string                 `json:"status"`
	Model  string                 `json:"model"`
	Output []responsesOutputItem  `json:"output"`
	Usage  responsesUsage         `json:"usage"`
	Error  *responsesErrorPayload `json:"error"`
}

// responsesOutputItem is one element of the output[] array. A "message" item
// holds assistant content; other types (reasoning, tool calls) are skipped.
type responsesOutputItem struct {
	Type    string                 `json:"type"`
	ID      string                 `json:"id"`
	Role    string                 `json:"role"`
	Status  string                 `json:"status"`
	Content []responsesContentPart `json:"content"`
}

// responsesErrorPayload carries a non-null error object from a failed response.
type responsesErrorPayload struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// responsesUsage is the Responses API usage object. Its field names differ from
// chat/completions: input_tokens/output_tokens rather than
// prompt_tokens/completion_tokens, with cached input tokens nested under
// input_tokens_details.
type responsesUsage struct {
	InputTokens        int `json:"input_tokens"`
	OutputTokens       int `json:"output_tokens"`
	TotalTokens        int `json:"total_tokens"`
	InputTokensDetails struct {
		CachedTokens int `json:"cached_tokens"`
	} `json:"input_tokens_details"`
	OutputTokensDetails struct {
		ReasoningTokens int `json:"reasoning_tokens"`
	} `json:"output_tokens_details"`
}

// toUsage maps the Responses usage object onto BharatCode's provider-neutral
// Usage. Cached input tokens populate CacheReadTokens; the Responses API does
// not report a cache-write count.
func (u responsesUsage) toUsage() Usage {
	return Usage{
		InputTokens:     u.InputTokens,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: u.InputTokensDetails.CachedTokens,
	}
}
