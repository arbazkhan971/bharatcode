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
	// Reasoning carries the reasoning controls for OpenAI reasoning models. Unlike
	// chat/completions (which takes a top-level "reasoning_effort" string), the
	// Responses API nests the effort under a "reasoning" object, so a top-level
	// reasoning_effort field is silently ignored. It is omitted for non-reasoning
	// models and when no effort is configured.
	Reasoning *responsesReasoning `json:"reasoning,omitempty"`
	// Store controls server-side response retention. The ChatGPT Codex backend
	// requires store=false; the pointer keeps the field omitted (default
	// behavior) for the standard OpenAI Responses path.
	Store *bool `json:"store,omitempty"`
	// Include requests extra output fields (for example
	// "reasoning.encrypted_content" on the Codex backend). Omitted when empty.
	Include []string `json:"include,omitempty"`
}

// responsesReasoning is the Responses API "reasoning" object. Effort is the
// per-request hidden-reasoning budget ("low", "medium", "high", and gpt-5's
// "minimal"). Summary asks the API to stream a natural-language summary of the
// model's reasoning ("auto", "concise", "detailed"); the request builder sets
// "auto" for every reasoning model so the hidden reasoning surfaces as
// response.reasoning_summary_text.delta events. Both are omitempty so an unset
// field is dropped.
type responsesReasoning struct {
	Effort  string `json:"effort,omitempty"`
	Summary string `json:"summary,omitempty"`
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
	// IncompleteDetails explains why a response carries status "incomplete". The
	// most common reason is "max_output_tokens" — the model hit the output cap
	// mid-generation — which is a completed (truncated) turn rather than a
	// failure; see emitResponsesStream.
	IncompleteDetails *responsesIncompleteDetails `json:"incomplete_details"`
}

// responsesIncompleteDetails carries the reason an incomplete response stopped
// short. "max_output_tokens" means the output-token cap was reached;
// "content_filter" means generation was filtered.
type responsesIncompleteDetails struct {
	Reason string `json:"reason"`
}

// hitMaxOutputTokens reports whether an incomplete response stopped because it
// reached the output-token cap, the one incomplete reason that completes a
// (truncated) turn rather than failing it.
func (r *responsesResponse) hitMaxOutputTokens() bool {
	return r != nil && r.IncompleteDetails != nil && r.IncompleteDetails.Reason == "max_output_tokens"
}

// responsesOutputItem is one element of the output[] array. A "message" item
// holds assistant content; a "reasoning" item holds the model's reasoning
// summary parts (Summary); a "function_call" item holds a model tool call
// (CallID, Name, Arguments).
type responsesOutputItem struct {
	Type    string                 `json:"type"`
	ID      string                 `json:"id"`
	Role    string                 `json:"role"`
	Status  string                 `json:"status"`
	Content []responsesContentPart `json:"content"`
	// Summary carries the reasoning summary parts of a "reasoning" output item
	// (each a {"type":"summary_text","text":...}). It is the buffered analogue of
	// the response.reasoning_summary_text.delta stream events; the streaming path
	// emits those as ThinkingEvents, so the buffered path mirrors it from here.
	Summary   []responsesContentPart `json:"summary"`
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
//
// input_tokens is the total prompt size *including* the cached portion
// (cached_tokens is a subset nested under input_tokens_details), whereas the
// ledger prices InputTokens and CacheReadTokens additively (the Anthropic
// convention, where input_tokens already excludes the cached portion). Subtract
// the cached tokens back out so they are billed once at the cache rate rather
// than twice. Clamp at zero against a malformed response where the cached count
// exceeds the input total. Mirrors the same correction on the chat/completions
// and Gemini paths.
func (u responsesUsage) toUsage() Usage {
	cacheRead := u.InputTokensDetails.CachedTokens
	input := u.InputTokens - cacheRead
	if input < 0 {
		input = 0
	}
	return Usage{
		InputTokens:     input,
		OutputTokens:    u.OutputTokens,
		CacheReadTokens: cacheRead,
	}
}
