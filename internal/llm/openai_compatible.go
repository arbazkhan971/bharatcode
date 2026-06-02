package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
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

	body, err := buildOpenAIRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}
	resp, err := postJSON(ctx, p.client, p.baseURL+"/chat/completions", apiKey, body)
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
		err := readSSE(ctx, resp.Body, func(ev sseEvent) error {
			if ev.Data == "[DONE]" {
				return nil
			}
			return p.handleStreamChunk(ctx, ev.Data, state, events)
		})
		if err != nil {
			send(ctx, events, ErrorEvent{Err: err})
			return
		}
		state.endAll(ctx, events)
		return
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		send(ctx, events, ErrorEvent{Err: fmt.Errorf("reading provider response: %w", err)})
		return
	}
	if err := emitOpenAIResponse(ctx, data, events); err != nil {
		send(ctx, events, ErrorEvent{Err: err})
	}
}

func (p *openAICompatibleProvider) handleStreamChunk(ctx context.Context, data string, state *toolCallState, events chan<- Event) error {
	var chunk openAIStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("decoding provider stream chunk: %w", err)
	}
	for _, choice := range chunk.Choices {
		if choice.Delta.Content != "" {
			send(ctx, events, DeltaTextEvent{Text: choice.Delta.Content})
		}
		if choice.Delta.ReasoningContent != "" {
			send(ctx, events, ThinkingEvent{Text: choice.Delta.ReasoningContent})
		}
		for _, call := range choice.Delta.ToolCalls {
			state.applyDelta(ctx, events, call)
		}
	}
	if chunk.Usage != nil {
		send(ctx, events, EndEvent{Usage: chunk.Usage.toUsage()})
	}
	return nil
}

func buildOpenAIRequest(req Request) (openAIChatRequest, error) {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}
	for _, msg := range message.Normalize(req.Messages) {
		converted, err := convertMessage(msg)
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
		Model:       req.Model,
		Messages:    messages,
		Tools:       tools,
		Stream:      true,
		Temperature: req.Temperature,
		MaxTokens:   req.MaxTokens,
	}
	return body, nil
}

func convertMessage(msg message.Message) ([]openAIMessage, error) {
	switch msg.Role {
	case message.RoleUser, message.RoleAssistant, message.RoleSystem:
		out := openAIMessage{Role: string(msg.Role)}
		var text strings.Builder
		for _, block := range msg.Content {
			switch b := block.(type) {
			case message.TextBlock:
				text.WriteString(b.Text)
			case message.ThinkingBlock:
				text.WriteString(b.Text)
			case message.ImageBlock:
				return nil, fmt.Errorf("image block conversion: %w", ErrUnsupportedFeature)
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
		out.Content = text.String()
		if out.Role == "assistant" && len(out.ToolCalls) > 0 && out.Content == "" {
			out.Content = ""
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
