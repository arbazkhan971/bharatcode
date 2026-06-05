package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// imageStyle selects how an ImageBlock is serialized for a provider that
// shares the OpenAI message-building path.
type imageStyle int

const (
	// imageStyleOpenAI emits images as image_url content parts inside the
	// message content array (OpenAI-compatible wire format).
	imageStyleOpenAI imageStyle = iota
	// imageStyleOllama emits images as bare base64 strings on the message's
	// top-level images[] array (Ollama /api/chat wire format).
	imageStyleOllama
)

type openAICompatibleProvider struct {
	name      string
	baseURL   string
	apiKeyEnv string
	models    []Model
	client    *http.Client
}

func newOpenAICompatibleProvider(name string, baseURL string, apiKeyEnv string, models []Model, client *http.Client) Provider {
	return &openAICompatibleProvider{
		name:      name,
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKeyEnv: apiKeyEnv,
		models:    append([]Model(nil), models...),
		client:    client,
	}
}

func (p *openAICompatibleProvider) Name() string {
	return p.name
}

func (p *openAICompatibleProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *openAICompatibleProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p *openAICompatibleProvider) SupportsImages() bool {
	return supportsImages(p.models)
}

func (p *openAICompatibleProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 && !modelSupportsTools(p.models, req.Model) {
		return nil, fmt.Errorf("model %q tools: %w", req.Model, ErrUnsupportedFeature)
	}
	if hasImages(req.Messages) && !modelSupportsImages(p.models, req.Model) {
		return nil, fmt.Errorf("model %q images: %w", req.Model, ErrUnsupportedFeature)
	}
	apiKey := ""
	if p.apiKeyEnv != "" {
		apiKey = os.Getenv(p.apiKeyEnv)
		if apiKey == "" {
			return nil, fmt.Errorf("reading %s: %w", p.apiKeyEnv, ErrAuth)
		}
	}

	body, err := buildOpenAIRequest(req, imageStyleOpenAI)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}
	// OpenRouter proxies models from many upstreams (Anthropic, Gemini, Grok,
	// DeepSeek), most of which are not OpenAI reasoning models and so are never
	// matched by the reasoning_effort/max_completion_tokens path in
	// buildOpenAIRequest. OpenRouter exposes a single `reasoning` object that
	// enables extended thinking for any of them, so set it here for OpenRouter
	// when a thinking budget or effort is configured. The OpenAI o-series keeps
	// its native path (reasoning_effort), so it is excluded to avoid sending two
	// competing reasoning controls.
	if isOpenRouter(p.baseURL) && !isReasoningModel(req.Model) {
		body.Reasoning = openRouterReasoning(req)
	}
	resp, err := postJSON(ctx, p.client, appendPath(p.baseURL, "/chat/completions"), apiKey, body)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

func (p *openAICompatibleProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.name, Model: model})
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		state := newToolCallState()
		var usage Usage
		err := readSSE(ctx, resp.Body, func(ev sseEvent) error {
			// The [DONE] sentinel terminates the stream; it carries no JSON and
			// must not be decoded as a chunk.
			if strings.TrimSpace(ev.Data) == "[DONE]" {
				return nil
			}
			return p.handleStreamChunk(ctx, ev.Data, state, &usage, events)
		})
		if err != nil {
			emitTerminalError(ctx, events, err)
			return
		}
		// Close any open tool calls first, then emit a single terminal EndEvent
		// carrying the usage from the provider's final stream chunk. Emitting
		// usage inline would order EndEvent before the trailing ToolUseEndEvents.
		state.endAll(ctx, events)
		send(ctx, events, EndEvent{Usage: usage})
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		// A mid-read failure is a transient transport fault (a truncated or
		// reset connection), not a permanent error; wrap it as ErrServer so the
		// failover and backoff layers retry it.
		send(ctx, events, ErrorEvent{Err: fmt.Errorf("reading provider response: %v: %w", err, ErrServer)})
		return
	}
	if err := emitOpenAIResponse(ctx, data, events); err != nil {
		send(ctx, events, ErrorEvent{Err: err})
	}
}

func (p *openAICompatibleProvider) handleStreamChunk(ctx context.Context, data string, state *toolCallState, usage *Usage, events chan<- Event) error {
	var chunk openAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("decoding provider stream chunk: %w", err)
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			send(ctx, events, DeltaTextEvent{Text: choice.Delta.Content})
		}
		// A reasoning model exposes its thinking under reasoning_content when
		// reached directly (DeepSeek) and under reasoning when reached via
		// OpenRouter. A single provider populates only one of the two, so prefer
		// reasoning_content and fall back to reasoning rather than emitting both,
		// which would double a relay that ever echoed the text into each field.
		if reasoning := choice.Delta.ReasoningContent; reasoning != "" {
			send(ctx, events, ThinkingEvent{Text: reasoning})
		} else if choice.Delta.Reasoning != "" {
			send(ctx, events, ThinkingEvent{Text: choice.Delta.Reasoning})
		}
		for _, call := range choice.Delta.ToolCalls {
			state.applyDelta(ctx, events, call)
		}
	}
	// With include_usage the provider sends a trailing chunk (empty choices)
	// carrying the real token counts. Record it so readResponse can emit a
	// single terminal EndEvent after the tool calls are closed out.
	if chunk.Usage != nil {
		*usage = chunk.Usage.toUsage()
	}
	return nil
}

// isOpenRouter reports whether baseURL points at OpenRouter, whose unified
// `reasoning` request parameter and cross-provider model namespace warrant a few
// OpenRouter-specific tweaks on the otherwise generic openai_compatible path.
// The match is on the host substring so a custom path prefix or trailing slash
// in the configured base_url does not defeat it.
func isOpenRouter(baseURL string) bool {
	return strings.Contains(strings.ToLower(baseURL), "openrouter.ai")
}

// openRouterReasoning maps a request's configured thinking budget or reasoning
// effort onto OpenRouter's `reasoning` object. A positive thinking budget takes
// precedence and is sent as max_tokens; otherwise a configured effort is sent as
// effort. The provider-independent "auto"/"dynamic" effort labels mean "let the
// model size its own reasoning" and have no OpenRouter effort value, so they map
// to enabled:true (reasoning on, upstream default budget) rather than being sent
// verbatim, which OpenRouter would 400 on. The effort is lowercased so a value
// like "High" matches OpenRouter's lowercase labels. When neither a budget nor
// an effort is configured it returns nil so the field is omitted and the model's
// own default applies rather than reasoning being force-enabled.
func openRouterReasoning(req Request) *openAIReasoning {
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		return &openAIReasoning{MaxTokens: req.Thinking.BudgetTokens}
	}
	switch effort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort)); effort {
	case "":
		return nil
	case "auto", "dynamic":
		return &openAIReasoning{Enabled: true}
	default:
		return &openAIReasoning{Effort: effort}
	}
}

func buildOpenAIRequest(req Request, style imageStyle) (openAIChatRequest, error) {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range message.Normalize(req.Messages) {
		converted, err := convertMessage(msg, style)
		if err != nil {
			return openAIChatRequest{}, err
		}
		messages = append(messages, converted...)
	}

	tools := make([]openAITool, 0, len(req.Tools))
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		tools = append(tools, openAITool{
			Type: "function",
			Function: openAIFunction{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
			},
		})
	}

	body := openAIChatRequest{
		Model:    req.Model,
		Messages: messages,
		Tools:    tools,
		Stream:   true,
		// Ask the provider to emit a final usage chunk so the EndEvent can
		// carry real prompt/completion token counts; without this OpenAI omits
		// usage from streamed responses entirely.
		StreamOptions: &openAIStreamOptions{IncludeUsage: true},
	}
	// Reasoning models (o-series, gpt-5 reasoning) reject temperature and the
	// legacy max_tokens field; they accept reasoning_effort and
	// max_completion_tokens instead. Non-reasoning models keep the classic
	// temperature and max_tokens params and ignore the reasoning ones. Gate all
	// of them by model id so we never send a param the API would 400 on.
	// Temperature stays unset (and thus omitted) for reasoning models,
	// preserving the prior omitempty behavior for every other model: a zero
	// temperature is omitted so the provider applies its own default.
	if isReasoningModel(req.Model) {
		body.ReasoningEffort = req.ReasoningEffort
		body.MaxCompletionTokens = req.MaxTokens
	} else {
		body.Temperature = req.Temperature
		body.MaxTokens = req.MaxTokens
	}
	return body, nil
}

func convertMessage(msg message.Message, style imageStyle) ([]openAIMessage, error) {
	switch msg.Role {
	case message.RoleUser, message.RoleAssistant, message.RoleSystem:
		out := openAIMessage{Role: string(msg.Role)}
		var text strings.Builder
		var imageParts []openAIContentPart
		for _, block := range msg.Content {
			switch b := block.(type) {
			case message.TextBlock:
				text.WriteString(b.Text)
			case message.ThinkingBlock:
				text.WriteString(b.Text)
			case message.ImageBlock:
				encoded := base64.StdEncoding.EncodeToString(b.Data)
				switch style {
				case imageStyleOllama:
					out.Images = append(out.Images, encoded)
				default:
					imageParts = append(imageParts, openAIContentPart{
						Type: "image_url",
						ImageURL: &openAIImageURL{
							URL: fmt.Sprintf("data:%s;base64,%s", b.MimeType, encoded),
						},
					})
				}
			case message.ToolUseBlock:
				out.ToolCalls = append(out.ToolCalls, openAIMessageToolCall{
					ID:   b.ID,
					Type: "function",
					Function: openAIMessageToolFunction{
						Name:      b.Name,
						Arguments: string(b.Input),
					},
				})
			case message.ToolResultBlock:
				return []openAIMessage{{
					Role:       "tool",
					ToolCallID: b.ToolUseID,
					Content:    b.Content,
				}}, nil
			default:
				return nil, fmt.Errorf("unknown block conversion: %w", ErrUnsupportedFeature)
			}
		}
		// When OpenAI-style image parts are present, the message content must be
		// an array of typed parts (a leading text part plus each image part).
		// Otherwise the content stays a plain string. Content is left nil when
		// there is no text, so the omitempty field is omitted as before, e.g.
		// for an assistant message carrying only tool calls.
		switch {
		case len(imageParts) > 0:
			parts := make([]openAIContentPart, 0, len(imageParts)+1)
			if text.Len() > 0 {
				parts = append(parts, openAIContentPart{Type: "text", Text: text.String()})
			}
			parts = append(parts, imageParts...)
			out.Content = parts
		case text.Len() > 0:
			out.Content = text.String()
		}
		return []openAIMessage{out}, nil
	case message.RoleTool:
		var out []openAIMessage
		for _, block := range msg.Content {
			result, ok := block.(message.ToolResultBlock)
			if !ok {
				continue
			}
			out = append(out, openAIMessage{
				Role:       "tool",
				ToolCallID: result.ToolUseID,
				Content:    result.Content,
			})
		}
		return out, nil
	default:
		return nil, fmt.Errorf("role %q conversion: %w", msg.Role, ErrUnsupportedFeature)
	}
}

func emitOpenAIResponse(ctx context.Context, data []byte, events chan<- Event) error {
	var resp openAIChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decoding provider response: %w", err)
	}
	for _, choice := range resp.Choices {
		if choice.Message.Content != "" {
			send(ctx, events, DeltaTextEvent{Text: choice.Message.Content})
		}
		state := newToolCallState()
		for i, call := range choice.Message.ToolCalls {
			state.applyDelta(ctx, events, openAIToolCallDelta{
				Index: &i,
				ID:    call.ID,
				Type:  call.Type,
				Function: openAIFunctionDelta{
					Name:      call.Function.Name,
					Arguments: call.Function.Arguments,
				},
			})
		}
		state.endAll(ctx, events)
	}
	send(ctx, events, EndEvent{Usage: resp.Usage.toUsage()})
	return nil
}

func send(ctx context.Context, events chan<- Event, event Event) bool {
	select {
	case <-ctx.Done():
		return false
	case events <- event:
		return true
	}
}

// emitTerminalError delivers the final ErrorEvent for a stream that failed and
// is about to close. It must not use send: on a cancelled context send's
// ctx.Done() and the buffered channel write are both ready, so the runtime
// picks between them at random and drops the terminal error -- precisely the
// context.Canceled signal a caller needs -- roughly half the time. The events
// channel is buffered and the consumer always drains it to close, so a
// non-blocking buffered write is attempted first and almost always succeeds.
// Only when the buffer is genuinely full does the function block, and then it
// races the buffer freeing up against ctx.Done() so a cancelled stream can
// still tear down without leaking the producer goroutine.
func emitTerminalError(ctx context.Context, events chan<- Event, err error) {
	select {
	case events <- ErrorEvent{Err: err}:
		return
	default:
	}
	select {
	case events <- ErrorEvent{Err: err}:
	case <-ctx.Done():
	}
}
