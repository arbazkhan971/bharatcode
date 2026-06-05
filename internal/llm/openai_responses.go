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

// openAIResponsesProvider posts to OpenAI's Responses API (/v1/responses)
// instead of chat/completions. It is an opt-in alternative for OpenAI models
// that prefer the Responses request shape (top-level instructions plus an
// input-items array) and parses the Responses output[] array into BharatCode's
// event types. Streaming is not yet implemented here, so requests are sent with
// stream=false and the full response is parsed at once.
type openAIResponsesProvider struct {
	name      string
	baseURL   string
	apiKeyEnv string
	models    []Model
	client    *http.Client
}

// newOpenAIResponsesProvider builds a provider that speaks the OpenAI Responses
// API. baseURL is the API root (the provider appends /responses); apiKeyEnv
// names the env var holding the bearer token.
func newOpenAIResponsesProvider(name string, baseURL string, apiKeyEnv string, models []Model, client *http.Client) Provider {
	return &openAIResponsesProvider{
		name:      name,
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKeyEnv: apiKeyEnv,
		models:    append([]Model(nil), models...),
		client:    client,
	}
}

func (p *openAIResponsesProvider) Name() string {
	return p.name
}

func (p *openAIResponsesProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *openAIResponsesProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p *openAIResponsesProvider) SupportsImages() bool {
	return supportsImages(p.models)
}

// Stream posts a streaming Responses request and emits the parsed output as
// Start/DeltaText/Thinking/ToolUse/End events. The server-sent-event payload is
// parsed incrementally; a server that ignores stream=true and replies with a
// single JSON body is still handled via the buffered fallback in readResponse. A
// request carrying tools for a model that does not advertise tool support is
// rejected so callers do not silently lose them.
func (p *openAIResponsesProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
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

	body, err := buildResponsesRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building responses request: %w", err)
	}
	body.Stream = true
	resp, err := postJSON(ctx, p.client, appendPath(p.baseURL, "/responses"), apiKey, body)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

func (p *openAIResponsesProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.name, Model: model})

	// The default path requests stream=true, so a Responses server replies with
	// an event-stream we parse incrementally. A server that ignores the flag (or
	// an error page) replies with a single JSON body; fall back to the buffered
	// parse so neither shape is dropped.
	if strings.Contains(resp.Header.Get("Content-Type"), "text/event-stream") {
		if err := emitResponsesStream(ctx, resp.Body, events); err != nil {
			emitTerminalError(ctx, events, err)
		}
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		// A mid-read failure is a transient transport fault (a truncated or
		// reset connection), not a permanent error; wrap it as ErrServer so the
		// failover and backoff layers retry it.
		send(ctx, events, ErrorEvent{Err: fmt.Errorf("reading responses payload: %v: %w", err, ErrServer)})
		return
	}
	if err := emitResponsesResponse(ctx, data, events); err != nil {
		send(ctx, events, ErrorEvent{Err: err})
	}
}

// buildResponsesRequest maps a provider-independent Request onto the Responses
// wire shape: the system prompt becomes the top-level instructions field and
// each message becomes an input item carrying typed content parts.
func buildResponsesRequest(req Request) (responsesRequest, error) {
	body := responsesRequest{
		Model:        req.Model,
		Instructions: req.SystemPrompt,
		Stream:       false,
	}
	for _, msg := range message.Normalize(req.Messages) {
		items, err := convertResponsesItem(msg)
		if err != nil {
			return responsesRequest{}, err
		}
		body.Input = append(body.Input, items...)
	}
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		body.Tools = append(body.Tools, responsesTool{
			Type:        "function",
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		})
	}
	// Reasoning models reject temperature and accept a reasoning budget instead;
	// gate both by model id exactly as the chat/completions path does so the
	// API never sees a param it would reject. The Responses API nests the effort
	// under a "reasoning" object (not a top-level reasoning_effort). Summary
	// "auto" is requested unconditionally for reasoning models so the model's
	// hidden reasoning streams back as response.reasoning_summary_text.delta
	// events (mapped to ThinkingEvents); without it the Responses API emits no
	// summary and the reasoning is invisible, mirroring Gemini's IncludeThoughts
	// and Anthropic's thinking opt-ins. Effort is added only when configured, so
	// an empty effort is simply dropped from the object rather than sent blank.
	if isReasoningModel(req.Model) {
		body.Reasoning = &responsesReasoning{Effort: normalizeOpenAIReasoningEffort(req.ReasoningEffort, req.Model), Summary: "auto"}
	} else {
		body.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		body.MaxOutputTokens = req.MaxTokens
	}
	return body, nil
}

// convertResponsesItem turns one normalized message into zero or more Responses
// input items. Text/image content becomes a single message item; tool calls and
// tool results each become their own top-level item (function_call /
// function_call_output) — in the Responses API these are not message content
// parts. message.Normalize relabels tool-result blocks onto the user role, so
// the block switch handles every block type regardless of the message role.
func convertResponsesItem(msg message.Message) ([]responsesInputItem, error) {
	switch msg.Role {
	case message.RoleUser, message.RoleAssistant, message.RoleSystem, message.RoleTool:
		// Assistant text is echoed back as output_text; every other role uses
		// input_text. This keeps multi-turn history representable without a
		// dedicated content-type per role beyond the input/output split.
		textType := "input_text"
		role := string(msg.Role)
		if msg.Role == message.RoleAssistant {
			textType = "output_text"
		}
		var parts []responsesContentPart
		var calls []responsesInputItem
		var results []responsesInputItem
		for _, block := range msg.Content {
			switch b := block.(type) {
			case message.TextBlock:
				parts = append(parts, responsesContentPart{Type: textType, Text: b.Text})
			case message.ThinkingBlock:
				parts = append(parts, responsesContentPart{Type: textType, Text: b.Text})
			case message.ImageBlock:
				encoded := base64.StdEncoding.EncodeToString(b.Data)
				parts = append(parts, responsesContentPart{
					Type:     "input_image",
					ImageURL: fmt.Sprintf("data:%s;base64,%s", b.MimeType, encoded),
				})
			case message.ToolUseBlock:
				args := string(b.Input)
				if args == "" {
					args = "{}"
				}
				calls = append(calls, responsesInputItem{
					Type:      "function_call",
					CallID:    b.ID,
					Name:      b.Name,
					Arguments: args,
				})
			case message.ToolResultBlock:
				results = append(results, responsesInputItem{
					Type:   "function_call_output",
					CallID: b.ToolUseID,
					Output: b.Content,
				})
			default:
				return nil, fmt.Errorf("responses block conversion: %w", ErrUnsupportedFeature)
			}
		}
		var items []responsesInputItem
		if len(parts) > 0 {
			// A message carrying only tool results has no role-bearing message
			// item; the RoleTool label is not a valid Responses message role, so
			// only emit a message item when there is text/image content to carry.
			if msg.Role == message.RoleTool {
				role = string(message.RoleUser)
			}
			items = append(items, responsesInputItem{Role: role, Content: parts})
		}
		// Function-call and result items follow the message item so the
		// assistant's text and its calls keep their original transcript order.
		items = append(items, calls...)
		items = append(items, results...)
		return items, nil
	default:
		return nil, fmt.Errorf("role %q responses conversion: %w", msg.Role, ErrUnsupportedFeature)
	}
}

// emitResponsesResponse parses a non-streaming Responses payload and emits the
// assembled assistant text as DeltaText events followed by a terminal EndEvent
// carrying the mapped usage. Start is emitted by the caller.
func emitResponsesResponse(ctx context.Context, data []byte, events chan<- Event) error {
	var resp responsesResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decoding responses payload: %w", err)
	}
	// A 200 reply can still report a logical failure via a non-null error
	// object (status "failed"/"incomplete"); surface it instead of emitting an
	// empty, zero-usage EndEvent that would look like a successful empty reply.
	if resp.Error != nil {
		msg := resp.Error.Message
		if msg == "" {
			msg = resp.Error.Code
		}
		return fmt.Errorf("responses api %s: %s: %w", resp.Status, msg, ErrServer)
	}
	state := newToolCallState()
	for i, item := range resp.Output {
		switch item.Type {
		case "message":
			for _, part := range item.Content {
				if part.Type == "output_text" && part.Text != "" {
					send(ctx, events, DeltaTextEvent{Text: part.Text})
				}
			}
		case "function_call":
			// The Responses output carries the complete call in one item; replay
			// it through the shared tool-call state so the emitted events (and the
			// empty/invalid-argument normalization) match the chat path exactly.
			// The model addresses the call by call_id in later turns, so that is
			// the ID we surface rather than the item's own id.
			idx := i
			state.applyDelta(ctx, events, openAIToolCallDelta{
				Index: &idx,
				ID:    item.CallID,
				Type:  "function",
				Function: openAIFunctionDelta{
					Name:      item.Name,
					Arguments: item.Arguments,
				},
			})
		}
	}
	state.endAll(ctx, events)
	send(ctx, events, EndEvent{Usage: resp.Usage.toUsage()})
	return nil
}

// responsesStreamEvent is one event from a streaming Responses reply. The event
// kind lives in the JSON "type" field rather than the SSE event name, so a
// single struct covers every kind: text/reasoning deltas (Delta), function-call
// lifecycle (Item on output_item.added, Delta on
// function_call_arguments.delta), the terminal envelope (Response on
// response.completed/failed/incomplete), and a top-level error event
// (Code/Message when Type == "error").
type responsesStreamEvent struct {
	Type        string `json:"type"`
	Delta       string `json:"delta"`
	OutputIndex int    `json:"output_index"`
	Item        struct {
		Type      string `json:"type"`
		ID        string `json:"id"`
		CallID    string `json:"call_id"`
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"item"`
	Code     string             `json:"code"`
	Message  string             `json:"message"`
	Response *responsesResponse `json:"response"`
}

// emitResponsesStream parses a streaming Responses event-stream and emits
// DeltaText/Thinking/ToolUse events as they arrive, followed by a terminal
// EndEvent carrying the mapped usage. Start is emitted by the caller. Tool calls
// are replayed through the shared toolCallState keyed by output_index so the
// emitted events (and the empty/invalid-argument normalization) match the chat
// and buffered paths exactly; the model addresses each call by call_id in later
// turns, so that is the ID surfaced rather than the item's own id.
func emitResponsesStream(ctx context.Context, body io.Reader, events chan<- Event) error {
	state := newToolCallState()
	var usage Usage
	err := readSSE(ctx, body, func(ev sseEvent) error {
		data := strings.TrimSpace(ev.Data)
		if data == "" || data == "[DONE]" {
			return nil
		}
		var e responsesStreamEvent
		if jerr := json.Unmarshal([]byte(data), &e); jerr != nil {
			// Keep-alives and any non-JSON comment lines carry no event payload.
			return nil
		}
		switch e.Type {
		case "response.output_text.delta":
			if e.Delta != "" {
				send(ctx, events, DeltaTextEvent{Text: e.Delta})
			}
		case "response.reasoning_text.delta", "response.reasoning_summary_text.delta":
			if e.Delta != "" {
				send(ctx, events, ThinkingEvent{Text: e.Delta})
			}
		case "response.output_item.added":
			if e.Item.Type == "function_call" {
				idx := e.OutputIndex
				state.applyDelta(ctx, events, openAIToolCallDelta{
					Index: &idx,
					ID:    e.Item.CallID,
					Type:  "function",
					Function: openAIFunctionDelta{
						Name:      e.Item.Name,
						Arguments: e.Item.Arguments,
					},
				})
			}
		case "response.function_call_arguments.delta":
			if e.Delta != "" {
				idx := e.OutputIndex
				state.applyDelta(ctx, events, openAIToolCallDelta{
					Index:    &idx,
					Function: openAIFunctionDelta{Arguments: e.Delta},
				})
			}
		case "response.completed":
			if e.Response != nil {
				usage = e.Response.Usage.toUsage()
			}
		case "response.failed", "response.incomplete":
			return responsesStreamError(e.Type, e.Response)
		case "error":
			msg := e.Message
			if msg == "" {
				msg = e.Code
			}
			if msg == "" {
				msg = "stream error"
			}
			return fmt.Errorf("responses api: %s: %w", msg, ErrServer)
		}
		return nil
	})
	if err != nil {
		return err
	}
	// Close any open tool calls first, then emit a single terminal EndEvent so
	// the trailing ToolUseEndEvents are not ordered after End.
	state.endAll(ctx, events)
	send(ctx, events, EndEvent{Usage: usage})
	return nil
}

// responsesStreamError builds the error for a terminal failed/incomplete stream
// event, preferring the response object's reported status and error message.
func responsesStreamError(kind string, resp *responsesResponse) error {
	status := strings.TrimPrefix(kind, "response.")
	msg := "stream " + status
	if resp != nil {
		if resp.Status != "" {
			status = resp.Status
		}
		if resp.Error != nil {
			if resp.Error.Message != "" {
				msg = resp.Error.Message
			} else if resp.Error.Code != "" {
				msg = resp.Error.Code
			}
		}
	}
	return fmt.Errorf("responses api %s: %s: %w", status, msg, ErrServer)
}
