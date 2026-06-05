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

// Stream posts a non-streaming Responses request and emits the parsed output as
// Start/DeltaText/End events. Tool calling and native streaming are followups;
// a request carrying tools is rejected so callers do not silently lose them.
func (p *openAIResponsesProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 {
		return nil, fmt.Errorf("responses api tools: %w", ErrUnsupportedFeature)
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
	resp, err := postJSON(ctx, p.client, p.baseURL+"/responses", apiKey, body)
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
		item, ok, err := convertResponsesItem(msg)
		if err != nil {
			return responsesRequest{}, err
		}
		if ok {
			body.Input = append(body.Input, item)
		}
	}
	// Reasoning models reject temperature and accept reasoning_effort instead;
	// gate both by model id exactly as the chat/completions path does so the
	// API never sees a param it would reject.
	if isReasoningModel(req.Model) {
		body.ReasoningEffort = req.ReasoningEffort
	} else {
		body.Temperature = req.Temperature
	}
	if req.MaxTokens > 0 {
		body.MaxOutputTokens = req.MaxTokens
	}
	return body, nil
}

// convertResponsesItem turns one normalized message into a Responses input
// item. The bool is false when the message carries no representable content
// (so the caller skips it). Tool blocks are not yet supported on this path.
func convertResponsesItem(msg message.Message) (responsesInputItem, bool, error) {
	switch msg.Role {
	case message.RoleUser, message.RoleAssistant, message.RoleSystem:
		// Assistant text is echoed back as output_text; every other role uses
		// input_text. This keeps multi-turn history representable without a
		// dedicated content-type per role beyond the input/output split.
		textType := "input_text"
		if msg.Role == message.RoleAssistant {
			textType = "output_text"
		}
		var parts []responsesContentPart
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
			default:
				return responsesInputItem{}, false, fmt.Errorf("responses block conversion: %w", ErrUnsupportedFeature)
			}
		}
		if len(parts) == 0 {
			return responsesInputItem{}, false, nil
		}
		return responsesInputItem{Role: string(msg.Role), Content: parts}, true, nil
	case message.RoleTool:
		// Tool results require function-call output items; defer to the
		// chat/completions path until tool calling lands here.
		return responsesInputItem{}, false, fmt.Errorf("responses tool result: %w", ErrUnsupportedFeature)
	default:
		return responsesInputItem{}, false, fmt.Errorf("role %q responses conversion: %w", msg.Role, ErrUnsupportedFeature)
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
	for _, item := range resp.Output {
		if item.Type != "message" {
			continue
		}
		for _, part := range item.Content {
			if part.Type == "output_text" && part.Text != "" {
				send(ctx, events, DeltaTextEvent{Text: part.Text})
			}
		}
	}
	send(ctx, events, EndEvent{Usage: resp.Usage.toUsage()})
	return nil
}
