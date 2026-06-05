package llm

import "encoding/json"

// responsesRequest is the POST /v1/responses request body. The Responses API
// lifts the system prompt to the top-level instructions field and replaces the
// chat messages array with an input-items array.
type responsesRequest struct {
	Model           string               `json:"model"`
	Instructions    string               `json:"instructions,omitempty"`
	Input           []responsesInputItem `json:"input"`
	Tools           []responsesTool      `json:"tools,omitempty"`
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

// responsesTool describes one callable function for the Responses API. Unlike
// chat/completions (which nests the schema under a "function" object), the
// Responses API uses a flat function tool: type plus name/description/parameters
// at the top level.
type responsesTool struct {
	Type        string          `json:"type"`
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

// responsesInputItem is one element of the Responses input array. It is one of
// three shapes distinguished by Type:
//   - a message item: Role plus a Content array of typed parts (Type omitted,
//     which the API treats as "message");
//   - a function_call item echoing a prior assistant tool call (CallID, Name,
//     Arguments);
//   - a function_call_output item carrying a tool result (CallID, Output).
//
// All non-message fields are omitempty so a plain message item serializes
// exactly as before.
type responsesInputItem struct {
	Type      string                 `json:"type,omitempty"`
	Role      string                 `json:"role,omitempty"`
	Content   []responsesContentPart `json:"content,omitempty"`
	CallID    string                 `json:"call_id,omitempty"`
	Name      string                 `json:"name,omitempty"`
	Arguments string                 `json:"arguments,omitempty"`
	Output    string                 `json:"output,omitempty"`
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
// holds assistant content; a "function_call" item holds a model tool call
// (CallID, Name, Arguments); other types (for example reasoning) are skipped.
type responsesOutputItem struct {
	Type      string                 `json:"type"`
	ID        string                 `json:"id"`
	Role      string                 `json:"role"`
	Status    string                 `json:"status"`
	Content   []responsesContentPart `json:"content"`
	CallID    string                 `json:"call_id"`
	Name      string                 `json:"name"`
	Arguments string                 `json:"arguments"`
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
