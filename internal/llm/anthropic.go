package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// anthropicVersion is the required value of the anthropic-version header for
// the Messages API.
const anthropicVersion = "2023-06-01"

// defaultAnthropicMaxTokens is used when a request does not set MaxTokens, as
// the Anthropic Messages API rejects requests without a max_tokens field.
const defaultAnthropicMaxTokens = 4096

type anthropicProvider struct {
	name      string
	baseURL   string
	apiKeyEnv string
	models    []Model
	client    *http.Client
	// promptCaching toggles emission of cache_control ephemeral markers on the
	// system prompt and tools. It defaults to on; Anthropic ignores the markers
	// gracefully when the selected model does not support prompt caching.
	promptCaching bool
}

func newAnthropicProvider(name string, baseURL string, apiKeyEnv string, models []Model, client *http.Client) Provider {
	return &anthropicProvider{
		name:          name,
		baseURL:       strings.TrimRight(baseURL, "/"),
		apiKeyEnv:     apiKeyEnv,
		models:        append([]Model(nil), models...),
		client:        client,
		promptCaching: true,
	}
}

func (p *anthropicProvider) Name() string {
	return p.name
}

func (p *anthropicProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *anthropicProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p *anthropicProvider) SupportsImages() bool {
	return supportsImages(p.models)
}

// betaHeader returns the value for the anthropic-beta request header given the
// target model, or "" when no beta features apply. Today the only opt-in is the
// 1M-token context window on the Claude Sonnet 4 and Opus 4.8 lines, enabled
// when the user has configured a context_window above the standard 200k for
// that model.
func (p *anthropicProvider) betaHeader(model string) string {
	if modelSupportsAnthropic1MContext(p.models, model) {
		return anthropic1MContextBeta
	}
	return ""
}

func (p *anthropicProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 && !modelSupportsTools(p.models, req.Model) {
		return nil, fmt.Errorf("model %q tools: %w", req.Model, ErrUnsupportedFeature)
	}
	if hasImages(req.Messages) && !modelSupportsImages(p.models, req.Model) {
		return nil, fmt.Errorf("model %q images: %w", req.Model, ErrUnsupportedFeature)
	}

	apiKey := ""
	if p.apiKeyEnv != "" {
		var err error
		apiKey, err = resolveAPIKey(p.apiKeyEnv, p.name)
		if err != nil {
			return nil, err
		}
	}

	body, err := p.buildAnthropicRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"Accept":            "text/event-stream",
		"anthropic-version": anthropicVersion,
	}
	if apiKey != "" {
		headers["x-api-key"] = apiKey
	}
	if beta := p.betaHeader(req.Model); beta != "" {
		headers["anthropic-beta"] = beta
	}

	resp, err := postJSONWithHeaders(ctx, p.client, p.baseURL+"/messages", headers, body)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

// CountTokens reports the prompt token count for req using Anthropic's native
// /v1/messages/count_tokens endpoint, satisfying the TokenCounter interface. It
// builds the same system/messages/tools payload Stream would send (so the system
// prompt, tools, and inline images are all counted, including the prompt-cache
// markers, which count_tokens accepts) but drops the inference-only fields the
// endpoint rejects: max_tokens, stream, and temperature. Callers should fall
// back to EstimateMessageTokens on a non-nil error, since this performs a
// network round trip.
func (p *anthropicProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	apiKey := ""
	if p.apiKeyEnv != "" {
		var err error
		apiKey, err = resolveAPIKey(p.apiKeyEnv, p.name)
		if err != nil {
			return 0, err
		}
	}

	full, err := p.buildAnthropicRequest(req)
	if err != nil {
		return 0, fmt.Errorf("building provider request: %w", err)
	}
	// count_tokens does not run inference, so the output cap, streaming flag, and
	// sampling temperature are irrelevant (and rejected by the endpoint). Carry
	// only the fields that determine prompt size; the thinking config is preserved
	// because enabling extended thinking changes the counted prompt.
	body := anthropicCountTokensRequest{
		Model:    full.Model,
		System:   full.System,
		Messages: full.Messages,
		Tools:    full.Tools,
		Thinking: full.Thinking,
	}

	headers := map[string]string{
		"Content-Type":      "application/json",
		"Accept":            "application/json",
		"anthropic-version": anthropicVersion,
	}
	if apiKey != "" {
		headers["x-api-key"] = apiKey
	}
	// count_tokens must run with the same beta surface as inference so the 1M
	// window is reflected when the prompt exceeds the standard 200k.
	if beta := p.betaHeader(req.Model); beta != "" {
		headers["anthropic-beta"] = beta
	}

	resp, err := postJSONWithHeaders(ctx, p.client, p.baseURL+"/messages/count_tokens", headers, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var out anthropicCountTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decoding provider response: %w", err)
	}
	return out.InputTokens, nil
}

func (p *anthropicProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.name, Model: model})

	state := newAnthropicStreamState()
	err := readSSE(ctx, resp.Body, func(ev sseEvent) error {
		return state.handle(ctx, ev, events)
	})
	if err != nil {
		emitTerminalError(ctx, events, err)
		return
	}
	state.finish(ctx, events)
}

// anthropicStreamState tracks usage accumulated across split SSE events and the
// content block currently being streamed.
type anthropicStreamState struct {
	usage       Usage
	blockType   string
	blockID     string
	blockName   string
	blockInput  strings.Builder
	blockActive bool
	ended       bool
}

func newAnthropicStreamState() *anthropicStreamState {
	return &anthropicStreamState{}
}

func (s *anthropicStreamState) handle(ctx context.Context, ev sseEvent, events chan<- Event) error {
	name := ev.Name
	if name == "" {
		// Fall back to the embedded type field when no SSE event line is set.
		var probe struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal([]byte(ev.Data), &probe); err == nil {
			name = probe.Type
		}
	}

	switch name {
	case "message_start":
		var chunk anthropicMessageStart
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return fmt.Errorf("decoding provider message_start: %w", err)
		}
		s.usage.InputTokens = chunk.Message.Usage.InputTokens
		s.usage.OutputTokens = chunk.Message.Usage.OutputTokens
		s.usage.CacheReadTokens = chunk.Message.Usage.CacheReadInputTokens
		s.usage.CacheWriteTokens = chunk.Message.Usage.CacheCreationInputTokens
	case "content_block_start":
		var chunk anthropicContentBlockStart
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return fmt.Errorf("decoding provider content_block_start: %w", err)
		}
		s.blockType = chunk.ContentBlock.Type
		s.blockID = chunk.ContentBlock.ID
		s.blockName = chunk.ContentBlock.Name
		s.blockInput.Reset()
		s.blockActive = true
		switch s.blockType {
		case "tool_use":
			send(ctx, events, ToolUseStartEvent{ID: s.blockID, Name: s.blockName})
		case "redacted_thinking":
			// Redacted thinking carries an encrypted data payload with no
			// human-readable text and no deltas. It is intentionally skipped: it
			// is never surfaced as a ThinkingEvent so encrypted reasoning does
			// not leak into the UI, and the block is dropped at content_block_stop.
		}
	case "content_block_delta":
		var chunk anthropicContentBlockDelta
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return fmt.Errorf("decoding provider content_block_delta: %w", err)
		}
		switch chunk.Delta.Type {
		case "text_delta":
			if chunk.Delta.Text != "" {
				send(ctx, events, DeltaTextEvent{Text: chunk.Delta.Text})
			}
		case "thinking_delta":
			if chunk.Delta.Thinking != "" {
				send(ctx, events, ThinkingEvent{Text: chunk.Delta.Thinking})
			}
		case "input_json_delta":
			if chunk.Delta.PartialJSON != "" {
				s.blockInput.WriteString(chunk.Delta.PartialJSON)
				send(ctx, events, ToolUseDeltaEvent{ID: s.blockID, Delta: chunk.Delta.PartialJSON})
			}
		}
	case "content_block_stop":
		if s.blockActive && s.blockType == "tool_use" {
			input := json.RawMessage(s.blockInput.String())
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			if !json.Valid(input) {
				input = json.RawMessage(fmt.Sprintf("%q", s.blockInput.String()))
			}
			send(ctx, events, ToolUseEndEvent{
				ID:    s.blockID,
				Name:  s.blockName,
				Input: input,
			})
		}
		s.blockActive = false
		s.blockType = ""
		s.blockID = ""
		s.blockName = ""
		s.blockInput.Reset()
	case "message_delta":
		var chunk anthropicMessageDelta
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err != nil {
			return fmt.Errorf("decoding provider message_delta: %w", err)
		}
		if chunk.Usage.OutputTokens > 0 {
			s.usage.OutputTokens = chunk.Usage.OutputTokens
		}
		if chunk.Usage.InputTokens > 0 {
			s.usage.InputTokens = chunk.Usage.InputTokens
		}
		if chunk.Usage.CacheReadInputTokens > 0 {
			s.usage.CacheReadTokens = chunk.Usage.CacheReadInputTokens
		}
		if chunk.Usage.CacheCreationInputTokens > 0 {
			s.usage.CacheWriteTokens = chunk.Usage.CacheCreationInputTokens
		}
	case "message_stop":
		s.emitEnd(ctx, events)
	case "error":
		var chunk anthropicErrorEvent
		if err := json.Unmarshal([]byte(ev.Data), &chunk); err == nil && chunk.Error.Message != "" {
			return classifyAnthropicStreamError(chunk.Error.Type, chunk.Error.Message)
		}
		return fmt.Errorf("provider stream error: %s: %w", strings.TrimSpace(ev.Data), ErrServer)
	}
	return nil
}

// classifyAnthropicStreamError maps an Anthropic mid-stream error event onto a
// retryable sentinel so the failover and backoff layers can recover. Anthropic
// surfaces transient capacity loss as overloaded_error and other server faults
// as api_error; both wrap ErrServer. A rate_limit_error wraps ErrRateLimit.
// Any other (terminal) error type, such as invalid_request_error, is returned
// without a retryable sentinel so it is not retried.
func classifyAnthropicStreamError(errType, message string) error {
	switch strings.ToLower(strings.TrimSpace(errType)) {
	case "overloaded_error", "api_error":
		return fmt.Errorf("provider stream error: %s: %w", message, ErrServer)
	case "rate_limit_error":
		return fmt.Errorf("provider stream error: %s: %w", message, ErrRateLimit)
	default:
		// An over-budget prompt is reported as a (terminal, non-retryable)
		// invalid_request_error whose message reads "prompt is too long: N
		// tokens > M maximum". Tag it as ErrContextLimit so the agent's
		// compaction path can recover the turn instead of failing it outright,
		// mirroring how the HTTP 400 path classifies the same wording.
		if mentionsContextLimit(message) {
			return fmt.Errorf("provider stream error: %s: %w", message, ErrContextLimit)
		}
		return fmt.Errorf("provider stream error: %s", message)
	}
}

// finish emits a terminal EndEvent if message_stop was not observed, so the
// caller always receives final usage even when the stream closes early.
func (s *anthropicStreamState) finish(ctx context.Context, events chan<- Event) {
	s.emitEnd(ctx, events)
}

func (s *anthropicStreamState) emitEnd(ctx context.Context, events chan<- Event) {
	if s.ended {
		return
	}
	s.ended = true
	send(ctx, events, EndEvent{Usage: s.usage})
}

func (p *anthropicProvider) buildAnthropicRequest(req Request) (anthropicRequest, error) {
	messages, err := buildAnthropicMessages(req.Messages)
	if err != nil {
		return anthropicRequest{}, err
	}

	tools := make([]anthropicTool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, anthropicTool{
			Name:        tool.Name,
			Description: tool.Description,
			InputSchema: schema,
		})
	}

	// System is carried as a structured text-block array so a cache_control
	// marker can be attached. An empty prompt yields a nil slice, which is
	// omitted from the request entirely.
	var system []anthropicSystemBlock
	if req.SystemPrompt != "" {
		system = []anthropicSystemBlock{{Type: "text", Text: req.SystemPrompt}}
	}

	if p.promptCaching {
		// Mark a single cache breakpoint on the last system block and a single
		// breakpoint on the last tool. Each marker caches everything up to and
		// including it, so one marker per prefix is enough and keeps us well
		// under Anthropic's max of 4 cache breakpoints.
		if n := len(system); n > 0 {
			system[n-1].CacheControl = ephemeralCacheControl()
		}
		if n := len(tools); n > 0 {
			tools[n-1].CacheControl = ephemeralCacheControl()
		}
		// Mark a rolling breakpoint on the last block of the final message so the
		// whole conversation prefix is cached, not just the static system/tools
		// prefix. On the next turn the appended content extends the prefix and the
		// previously cached history is read back instead of re-billed at full
		// price, which is the dominant cost in a long multi-turn agent loop. This
		// is the third of Anthropic's four permitted breakpoints (system + tools +
		// history); a marker on a prefix below the model's minimum cacheable size
		// is ignored by the API rather than rejected, so a short first turn is safe.
		if n := len(messages); n > 0 {
			if blocks := messages[n-1].Content; len(blocks) > 0 {
				blocks[len(blocks)-1].CacheControl = ephemeralCacheControl()
			}
		}
	}

	maxTokens := req.MaxTokens
	if maxTokens <= 0 {
		// When the caller leaves max_tokens unset, default to the model's full
		// output allowance so long answers from the modern Claude line (32k–64k
		// output) are not silently truncated. Unknown ids fall back to the
		// conservative flat default.
		if maxTokens = inferAnthropicMaxOutput(req.Model); maxTokens <= 0 {
			maxTokens = defaultAnthropicMaxTokens
		}
	}

	out := anthropicRequest{
		Model:       req.Model,
		System:      system,
		Messages:    messages,
		Tools:       tools,
		MaxTokens:   maxTokens,
		Temperature: req.Temperature,
		Stream:      true,
	}

	// Extended thinking is opt-in per request and only emitted when the model
	// supports it. Unlike tools and images (which error on an unsupported model),
	// an unsupported thinking request is silently dropped: the support check is an
	// approximate model-id heuristic, so degrading gracefully is safer than
	// rejecting a valid request on a heuristic false negative. Anthropic requires
	// the default sampling temperature while thinking is enabled, so the
	// temperature override is dropped to avoid a 400.
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 && modelSupportsThinking(p.models, req.Model) {
		out.Thinking = &anthropicThinking{Type: "enabled", BudgetTokens: req.Thinking.BudgetTokens}
		out.Temperature = 0
		// Anthropic requires max_tokens to be strictly greater than the thinking
		// budget, since the budget is carved out of the same output allowance: the
		// thinking tokens are billed as output and counted against max_tokens. A
		// caller that asks for a large thinking budget but leaves max_tokens at the
		// modest default (or sets it below the budget) would otherwise get a 400.
		// Lift the cap to the budget plus a full default allowance so there is room
		// for a visible answer after the model finishes thinking.
		if out.MaxTokens <= req.Thinking.BudgetTokens {
			out.MaxTokens = req.Thinking.BudgetTokens + defaultAnthropicMaxTokens
		}
	}

	return out, nil
}

// ephemeralCacheControl returns the cache_control marker Anthropic uses to open
// an ephemeral prompt-cache breakpoint on the carrying content block.
func ephemeralCacheControl() *anthropicCacheControl {
	return &anthropicCacheControl{Type: "ephemeral"}
}

func buildAnthropicMessages(history []message.Message) ([]anthropicMessage, error) {
	out := make([]anthropicMessage, 0, len(history))
	for _, msg := range message.Normalize(history) {
		switch msg.Role {
		case message.RoleSystem:
			// System content is carried as the top-level system field; skip it
			// in the messages array.
			continue
		case message.RoleUser, message.RoleAssistant, message.RoleTool:
			blocks, err := convertAnthropicBlocks(msg.Content)
			if err != nil {
				return nil, err
			}
			if len(blocks) == 0 {
				continue
			}
			role := "user"
			if msg.Role == message.RoleAssistant {
				role = "assistant"
			}
			out = append(out, anthropicMessage{Role: role, Content: blocks})
		default:
			return nil, fmt.Errorf("role %q conversion: %w", msg.Role, ErrUnsupportedFeature)
		}
	}
	return out, nil
}

func convertAnthropicBlocks(blocks []message.ContentBlock) ([]anthropicContentBlock, error) {
	out := make([]anthropicContentBlock, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case message.TextBlock:
			out = append(out, anthropicContentBlock{Type: "text", Text: b.Text})
		case message.ThinkingBlock:
			out = append(out, anthropicContentBlock{Type: "text", Text: b.Text})
		case message.ToolUseBlock:
			input := b.Input
			if len(input) == 0 {
				input = json.RawMessage(`{}`)
			}
			out = append(out, anthropicContentBlock{
				Type:  "tool_use",
				ID:    b.ID,
				Name:  b.Name,
				Input: input,
			})
		case message.ToolResultBlock:
			out = append(out, anthropicContentBlock{
				Type:      "tool_result",
				ToolUseID: b.ToolUseID,
				Content:   b.Content,
				IsError:   b.IsError,
			})
		case message.ImageBlock:
			out = append(out, anthropicContentBlock{
				Type: "image",
				Source: &anthropicImageSource{
					Type:      "base64",
					MediaType: b.MimeType,
					Data:      base64.StdEncoding.EncodeToString(b.Data),
				},
			})
		default:
			return nil, fmt.Errorf("unknown block conversion: %w", ErrUnsupportedFeature)
		}
	}
	return out, nil
}

type anthropicRequest struct {
	Model       string                 `json:"model"`
	System      []anthropicSystemBlock `json:"system,omitempty"`
	Messages    []anthropicMessage     `json:"messages"`
	Tools       []anthropicTool        `json:"tools,omitempty"`
	MaxTokens   int                    `json:"max_tokens"`
	Temperature float64                `json:"temperature,omitempty"`
	Thinking    *anthropicThinking     `json:"thinking,omitempty"`
	Stream      bool                   `json:"stream"`
}

// anthropicCountTokensRequest is the body of a /v1/messages/count_tokens call.
// It mirrors the inference request's prompt-shaping fields (system, messages,
// tools, thinking) but omits the inference-only fields (max_tokens, stream,
// temperature) the endpoint does not accept.
type anthropicCountTokensRequest struct {
	Model    string                 `json:"model"`
	System   []anthropicSystemBlock `json:"system,omitempty"`
	Messages []anthropicMessage     `json:"messages"`
	Tools    []anthropicTool        `json:"tools,omitempty"`
	Thinking *anthropicThinking     `json:"thinking,omitempty"`
}

// anthropicCountTokensResponse carries the prompt token total reported by the
// count_tokens endpoint.
type anthropicCountTokensResponse struct {
	InputTokens int `json:"input_tokens"`
}

// anthropicThinking enables extended thinking on a Messages request. The only
// supported type is "enabled"; budget_tokens caps the visible reasoning pass.
type anthropicThinking struct {
	Type         string `json:"type"`
	BudgetTokens int    `json:"budget_tokens"`
}

// anthropicSystemBlock is one entry of the structured system-prompt array. The
// array form (rather than a bare string) lets a cache_control marker attach to
// the system prompt.
type anthropicSystemBlock struct {
	Type         string                 `json:"type"`
	Text         string                 `json:"text"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicCacheControl marks a content block as a prompt-cache breakpoint. The
// only supported type is "ephemeral".
type anthropicCacheControl struct {
	Type string `json:"type"`
}

type anthropicMessage struct {
	Role    string                  `json:"role"`
	Content []anthropicContentBlock `json:"content"`
}

type anthropicContentBlock struct {
	Type      string                `json:"type"`
	Text      string                `json:"text,omitempty"`
	ID        string                `json:"id,omitempty"`
	Name      string                `json:"name,omitempty"`
	Input     json.RawMessage       `json:"input,omitempty"`
	ToolUseID string                `json:"tool_use_id,omitempty"`
	Content   string                `json:"content,omitempty"`
	IsError   bool                  `json:"is_error,omitempty"`
	Source    *anthropicImageSource `json:"source,omitempty"`
	// CacheControl marks this block as a prompt-cache breakpoint, used to cache
	// the conversation prefix on the last block of the final message. It is omitted
	// unless prompt caching is enabled and this is the rolling history breakpoint.
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

// anthropicImageSource carries inline base64 image data for an image content
// block in the Anthropic Messages API.
type anthropicImageSource struct {
	Type      string `json:"type"`
	MediaType string `json:"media_type"`
	Data      string `json:"data"`
}

type anthropicTool struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description,omitempty"`
	InputSchema  json.RawMessage        `json:"input_schema"`
	CacheControl *anthropicCacheControl `json:"cache_control,omitempty"`
}

type anthropicUsage struct {
	InputTokens              int `json:"input_tokens"`
	OutputTokens             int `json:"output_tokens"`
	CacheReadInputTokens     int `json:"cache_read_input_tokens"`
	CacheCreationInputTokens int `json:"cache_creation_input_tokens"`
}

type anthropicMessageStart struct {
	Message struct {
		Usage anthropicUsage `json:"usage"`
	} `json:"message"`
}

type anthropicContentBlockStart struct {
	Index        int `json:"index"`
	ContentBlock struct {
		Type string `json:"type"`
		ID   string `json:"id"`
		Name string `json:"name"`
	} `json:"content_block"`
}

type anthropicContentBlockDelta struct {
	Index int `json:"index"`
	Delta struct {
		Type        string `json:"type"`
		Text        string `json:"text"`
		Thinking    string `json:"thinking"`
		PartialJSON string `json:"partial_json"`
	} `json:"delta"`
}

type anthropicMessageDelta struct {
	Usage anthropicUsage `json:"usage"`
}

type anthropicErrorEvent struct {
	Error struct {
		Type    string `json:"type"`
		Message string `json:"message"`
	} `json:"error"`
}
