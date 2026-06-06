package llm

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type ollamaProvider struct {
	name    string
	baseURL string
	models  []Model
	client  *http.Client
}

func newOllamaProvider(name string, baseURL string, models []Model, client *http.Client) Provider {
	return &ollamaProvider{
		name:    name,
		baseURL: strings.TrimRight(baseURL, "/"),
		models:  append([]Model(nil), models...),
		client:  client,
	}
}

func (p *ollamaProvider) Name() string {
	return p.name
}

func (p *ollamaProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *ollamaProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p *ollamaProvider) SupportsImages() bool {
	return supportsImages(p.models)
}

func (p *ollamaProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if len(req.Tools) > 0 && !modelSupportsTools(p.models, req.Model) {
		return nil, fmt.Errorf("model %q tools: %w", req.Model, ErrUnsupportedFeature)
	}
	if hasImages(req.Messages) && !modelSupportsImages(p.models, req.Model) {
		return nil, fmt.Errorf("model %q images: %w", req.Model, ErrUnsupportedFeature)
	}

	body, err := buildOllamaRequest(req, p.numCtx(req.Model))
	if err != nil {
		return nil, fmt.Errorf("building local provider request: %w", err)
	}
	resp, err := postJSON(ctx, p.client, p.baseURL+"/api/chat", "", body)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

func (p *ollamaProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.name, Model: model})
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 1024), 1024*1024)
	state := newToolCallState()
	for scanner.Scan() {
		select {
		case <-ctx.Done():
			send(ctx, events, ErrorEvent{Err: fmt.Errorf("reading local provider stream: %w", ctx.Err())})
			return
		default:
		}
		var chunk ollamaChunk
		if err := json.Unmarshal(scanner.Bytes(), &chunk); err != nil {
			send(ctx, events, ErrorEvent{Err: fmt.Errorf("decoding local provider stream: %w", err)})
			return
		}
		if chunk.Message.Content != "" {
			send(ctx, events, DeltaTextEvent{Text: chunk.Message.Content})
		}
		for i, call := range chunk.Message.ToolCalls {
			state.applyDelta(ctx, events, openAIToolCallDelta{
				Index: &i,
				ID:    call.ID,
				Type:  "function",
				Function: openAIFunctionDelta{
					Name:      call.Function.Name,
					Arguments: rawJSONString(call.Function.Arguments),
				},
			})
		}
		if chunk.Done {
			state.endAll(ctx, events)
			send(ctx, events, EndEvent{Usage: Usage{
				InputTokens:  chunk.PromptEvalCount,
				OutputTokens: chunk.EvalCount,
			}})
			return
		}
	}
	if err := scanner.Err(); err != nil {
		send(ctx, events, ErrorEvent{Err: fmt.Errorf("scanning local provider stream: %w", err)})
	}
}

// numCtx returns the context window (in tokens) to request from Ollama for the
// model named by id. Ollama defaults num_ctx to a small window (2k–4k tokens)
// regardless of the model's real capacity, and silently truncates any prompt
// that exceeds it — which for an agent quietly drops earlier turns and tool
// output, corrupting the conversation rather than erroring. The model's
// configured context_window (or, when unset, the family heuristic the registry
// already applies) is a known, user-controllable budget, so pass it through as
// num_ctx to size the window correctly. A user who hits local memory pressure
// can lower context_window in config to shrink the allocation. A zero/unknown
// window returns 0, which the builder omits so Ollama keeps its own default.
func (p *ollamaProvider) numCtx(id string) int {
	if model, ok := findModel(p.models, id); ok && model.ContextWindow > 0 {
		return model.ContextWindow
	}
	return inferContextWindow(id)
}

func buildOllamaRequest(req Request, numCtx int) (ollamaRequest, error) {
	openReq, err := buildOpenAIRequest(req, imageStyleOllama)
	if err != nil {
		return ollamaRequest{}, err
	}
	return ollamaRequest{
		Model:    openReq.Model,
		Messages: openReq.Messages,
		Tools:    openReq.Tools,
		Stream:   true,
		Options: ollamaOptions{
			Temperature: req.Temperature,
			NumPredict:  req.MaxTokens,
			NumCtx:      numCtx,
		},
	}, nil
}

func rawJSONString(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	return string(raw)
}

type ollamaRequest struct {
	Model    string          `json:"model"`
	Messages []openAIMessage `json:"messages"`
	Tools    []openAITool    `json:"tools,omitempty"`
	Stream   bool            `json:"stream"`
	Options  ollamaOptions   `json:"options,omitempty"`
}

type ollamaOptions struct {
	Temperature float64 `json:"temperature,omitempty"`
	NumPredict  int     `json:"num_predict,omitempty"`
	// NumCtx sizes the model's context window for this request. It is omitted
	// when zero so Ollama applies its own (small) default; otherwise it carries
	// the model's configured/inferred context_window so long agent prompts are
	// not silently truncated. See ollamaProvider.numCtx.
	NumCtx int `json:"num_ctx,omitempty"`
}

type ollamaChunk struct {
	Message struct {
		Content   string `json:"content"`
		ToolCalls []struct {
			ID       string `json:"id"`
			Function struct {
				Name      string          `json:"name"`
				Arguments json.RawMessage `json:"arguments"`
			} `json:"function"`
		} `json:"tool_calls"`
	} `json:"message"`
	Done            bool `json:"done"`
	PromptEvalCount int  `json:"prompt_eval_count"`
	EvalCount       int  `json:"eval_count"`
}
