package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// defaultGeminiBaseURL is the Google Generative Language API root used when a
// gemini provider does not set base_url. The model and method are appended per
// request (for example .../v1beta/models/gemini-2.0-flash:streamGenerateContent).
const defaultGeminiBaseURL = "https://generativelanguage.googleapis.com/v1beta"

// defaultGeminiMaxTokens is the visible-answer allowance reserved on top of a
// thinking budget when a caller's explicit maxOutputTokens would otherwise leave
// no room for output after the reasoning pass. See buildGeminiRequest.
const defaultGeminiMaxTokens = 8192

// geminiProvider speaks Google's native Generative Language API
// (generateContent / streamGenerateContent) rather than the OpenAI-compatible
// shim. It maps BharatCode's provider-independent Request onto Gemini's
// contents/parts model: assistant turns become role "model", tool results
// become functionResponse parts, and inline images become inline_data parts.
type geminiProvider struct {
	name      string
	baseURL   string
	apiKeyEnv string
	models    []Model
	client    *http.Client
}

func newGeminiProvider(name string, baseURL string, apiKeyEnv string, models []Model, client *http.Client) Provider {
	if baseURL == "" {
		baseURL = defaultGeminiBaseURL
	}
	return &geminiProvider{
		name:      name,
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKeyEnv: apiKeyEnv,
		models:    append([]Model(nil), models...),
		client:    client,
	}
}

func (p *geminiProvider) Name() string {
	return p.name
}

func (p *geminiProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p *geminiProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p *geminiProvider) SupportsImages() bool {
	return supportsImages(p.models)
}

func (p *geminiProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
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

	body, err := p.buildGeminiRequest(req)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "text/event-stream",
	}
	if apiKey != "" {
		// Gemini accepts the key as the x-goog-api-key header or a ?key= query
		// param; the header keeps the secret out of request URLs and logs.
		headers["x-goog-api-key"] = apiKey
	}

	// alt=sse selects the server-sent-events transport for streamGenerateContent;
	// without it the endpoint returns a single buffered JSON array instead.
	url := fmt.Sprintf("%s/models/%s:streamGenerateContent?alt=sse", p.baseURL, req.Model)
	resp, err := postJSONWithHeaders(ctx, p.client, url, headers, body)
	if err != nil {
		return nil, err
	}

	events := make(chan Event, 16)
	go p.readResponse(ctx, resp, req.Model, events)
	return events, nil
}

// CountTokens reports the prompt token count for req using Gemini's native
// models/{model}:countTokens endpoint, satisfying the TokenCounter interface.
// It builds the same generateContent payload Stream would send (so system
// instruction, tools, and inline images are all counted) and wraps it in a
// countTokens request. Callers should fall back to EstimateMessageTokens on a
// non-nil error, since this performs a network round trip.
func (p *geminiProvider) CountTokens(ctx context.Context, req Request) (int, error) {
	apiKey := ""
	if p.apiKeyEnv != "" {
		apiKey = os.Getenv(p.apiKeyEnv)
		if apiKey == "" {
			return 0, fmt.Errorf("reading %s: %w", p.apiKeyEnv, ErrAuth)
		}
	}

	inner, err := p.buildGeminiRequest(req)
	if err != nil {
		return 0, fmt.Errorf("building provider request: %w", err)
	}
	// countTokens does not run inference, so generationConfig (temperature,
	// thinking budget, output cap) is irrelevant and dropped to keep the request
	// to the fields that affect the prompt size.
	inner.GenerationConfig = nil

	body := geminiCountTokensRequest{
		GenerateContentRequest: geminiGenerateContentRequest{
			// The countTokens generateContentRequest requires a fully qualified
			// model resource name (models/<id>), unlike the URL path segment.
			Model:         "models/" + req.Model,
			geminiRequest: inner,
		},
	}

	headers := map[string]string{
		"Content-Type": "application/json",
		"Accept":       "application/json",
	}
	if apiKey != "" {
		headers["x-goog-api-key"] = apiKey
	}

	url := fmt.Sprintf("%s/models/%s:countTokens", p.baseURL, req.Model)
	resp, err := postJSONWithHeaders(ctx, p.client, url, headers, body)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var out geminiCountTokensResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return 0, fmt.Errorf("decoding provider response: %w", err)
	}
	return out.TotalTokens, nil
}

func (p *geminiProvider) readResponse(ctx context.Context, resp *http.Response, model string, events chan<- Event) {
	defer close(events)
	defer resp.Body.Close()

	send(ctx, events, StartEvent{Provider: p.name, Model: model})

	state := &geminiStreamState{}
	err := readSSE(ctx, resp.Body, func(ev sseEvent) error {
		return state.handle(ctx, ev, events)
	})
	if err != nil {
		emitTerminalError(ctx, events, err)
		return
	}
	send(ctx, events, EndEvent{Usage: state.usage})
}

// geminiStreamState accumulates usage across chunks and assigns synthetic ids to
// function calls. Gemini omits tool-call ids (responses are matched back by
// function name), but the agent loop keys tool calls by id, so each call gets a
// stable per-stream id.
type geminiStreamState struct {
	usage     Usage
	callIndex int
}

func (s *geminiStreamState) handle(ctx context.Context, ev sseEvent, events chan<- Event) error {
	data := strings.TrimSpace(ev.Data)
	if data == "" {
		return nil
	}

	var chunk geminiStreamChunk
	if err := json.Unmarshal([]byte(data), &chunk); err != nil {
		return fmt.Errorf("decoding provider stream: %w", err)
	}
	if chunk.Error != nil && chunk.Error.Message != "" {
		return classifyGeminiStreamError(chunk.Error.Status, chunk.Error.Code, chunk.Error.Message)
	}

	if chunk.UsageMetadata != nil {
		s.usage = chunk.UsageMetadata.toUsage()
	}

	// A prompt rejected by Gemini's safety filters returns no candidates and a
	// promptFeedback.blockReason instead of an error object, which would
	// otherwise end the stream with no output and no error.
	if chunk.PromptFeedback != nil && chunk.PromptFeedback.BlockReason != "" {
		return fmt.Errorf("provider blocked prompt: %s", chunk.PromptFeedback.BlockReason)
	}

	for _, cand := range chunk.Candidates {
		for _, part := range cand.Content.Parts {
			if part.FunctionCall != nil {
				id := "call_" + strconv.Itoa(s.callIndex)
				s.callIndex++
				args := json.RawMessage(part.FunctionCall.Args)
				if len(args) == 0 {
					args = json.RawMessage(`{}`)
				}
				send(ctx, events, ToolUseStartEvent{ID: id, Name: part.FunctionCall.Name})
				send(ctx, events, ToolUseEndEvent{ID: id, Name: part.FunctionCall.Name, Input: args})
				continue
			}
			if part.Text == "" {
				continue
			}
			if part.Thought {
				send(ctx, events, ThinkingEvent{Text: part.Text})
			} else {
				send(ctx, events, DeltaTextEvent{Text: part.Text})
			}
		}
		// A candidate whose generation was cut short by a safety filter, the
		// recitation guard, or a malformed call reports the reason in
		// finishReason. Any text already emitted above stays; the error stops the
		// turn so the caller does not treat a truncated answer as complete.
		if reason := strings.ToUpper(strings.TrimSpace(cand.FinishReason)); geminiBlockingFinishReasons[reason] {
			return fmt.Errorf("provider stopped generation: %s", reason)
		}
	}
	return nil
}

// geminiBlockingFinishReasons lists candidate finishReason values that mean
// Gemini aborted generation rather than completing it. STOP (normal) and
// MAX_TOKENS (hit the output cap) complete a turn and are intentionally absent,
// so only a genuinely blocked or malformed response surfaces as a stream error.
var geminiBlockingFinishReasons = map[string]bool{
	"SAFETY":                  true,
	"RECITATION":              true,
	"BLOCKLIST":               true,
	"PROHIBITED_CONTENT":      true,
	"SPII":                    true,
	"MALFORMED_FUNCTION_CALL": true,
	"OTHER":                   true,
}

// classifyGeminiStreamError maps a Gemini mid-stream error object onto a
// retryable sentinel so the failover and backoff layers can recover. Gemini
// reports transient capacity loss with status UNAVAILABLE and quota exhaustion
// with RESOURCE_EXHAUSTED; other statuses are returned without a retryable
// sentinel so they are not retried.
func classifyGeminiStreamError(status string, code int, msg string) error {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RESOURCE_EXHAUSTED":
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrRateLimit)
	case "UNAVAILABLE", "INTERNAL", "DEADLINE_EXCEEDED":
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrServer)
	case "INVALID_ARGUMENT":
		// A prompt that overflows the model context window comes back as
		// INVALID_ARGUMENT whose message names the token overflow. Surface it as
		// ErrContextLimit so the agent's compaction/overflow path can recover
		// instead of treating it as an unrecoverable bad request.
		if mentionsContextLimit(msg) {
			return fmt.Errorf("provider stream error: %s: %w", msg, ErrContextLimit)
		}
	}
	if code == http.StatusTooManyRequests {
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrRateLimit)
	}
	if code >= 500 {
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrServer)
	}
	return fmt.Errorf("provider stream error: %s", msg)
}

func (p *geminiProvider) buildGeminiRequest(req Request) (geminiRequest, error) {
	contents, err := buildGeminiContents(req.Messages)
	if err != nil {
		return geminiRequest{}, err
	}

	out := geminiRequest{Contents: contents}

	if req.SystemPrompt != "" {
		out.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDecl, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := tool.InputSchema
			if len(schema) == 0 {
				schema = json.RawMessage(`{"type":"object"}`)
			}
			decls = append(decls, geminiFunctionDecl{
				Name:        tool.Name,
				Description: tool.Description,
				Parameters:  schema,
			})
		}
		out.Tools = []geminiTool{{FunctionDeclarations: decls}}
	}

	if req.Temperature > 0 || req.MaxTokens > 0 {
		out.GenerationConfig = &geminiGenerationConfig{
			Temperature:     req.Temperature,
			MaxOutputTokens: req.MaxTokens,
		}
	}

	// Native extended thinking is opt-in per request and only emitted for a
	// Gemini 2.5 model that supports thinkingConfig. Like Anthropic's path, an
	// unsupported thinking request is silently dropped rather than rejected: the
	// support check is an approximate model-id heuristic, so degrading gracefully
	// beats 400-ing a valid request on a false negative. IncludeThoughts asks the
	// API for thought summaries, which the stream surfaces as ThinkingEvents.
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 && modelSupportsGeminiThinking(p.models, req.Model) {
		if out.GenerationConfig == nil {
			out.GenerationConfig = &geminiGenerationConfig{}
		}
		out.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
			IncludeThoughts: true,
			ThinkingBudget:  &req.Thinking.BudgetTokens,
		}
		// On Gemini 2.5 the thinking tokens are carved out of the same
		// maxOutputTokens allowance and billed as output, so a positive cap at or
		// below the budget leaves no room for a visible answer (the model spends
		// the whole allowance reasoning and the candidate comes back empty or
		// truncated). Lift the cap to the budget plus a full default allowance,
		// mirroring the Anthropic path. A zero cap is left untouched so the model's
		// own (much larger) default applies.
		if out.GenerationConfig.MaxOutputTokens > 0 && out.GenerationConfig.MaxOutputTokens <= req.Thinking.BudgetTokens {
			out.GenerationConfig.MaxOutputTokens = req.Thinking.BudgetTokens + defaultGeminiMaxTokens
		}
	}

	return out, nil
}

func buildGeminiContents(history []message.Message) ([]geminiContent, error) {
	normalized := message.Normalize(history)

	// Gemini matches a functionResponse back to its functionCall by name, not by
	// id, so build an id->name index from assistant tool-use blocks first.
	toolNames := make(map[string]string)
	for _, msg := range normalized {
		for _, block := range msg.Content {
			if b, ok := block.(message.ToolUseBlock); ok {
				toolNames[b.ID] = b.Name
			}
		}
	}

	out := make([]geminiContent, 0, len(normalized))
	for _, msg := range normalized {
		switch msg.Role {
		case message.RoleSystem:
			// System content is carried as the top-level system_instruction field.
			continue
		case message.RoleUser, message.RoleAssistant, message.RoleTool:
			parts, err := convertGeminiParts(msg.Content, toolNames)
			if err != nil {
				return nil, err
			}
			if len(parts) == 0 {
				continue
			}
			// Gemini's only content roles are "user" and "model"; tool results are
			// carried as functionResponse parts inside a user-role content.
			role := "user"
			if msg.Role == message.RoleAssistant {
				role = "model"
			}
			out = append(out, geminiContent{Role: role, Parts: parts})
		default:
			return nil, fmt.Errorf("role %q conversion: %w", msg.Role, ErrUnsupportedFeature)
		}
	}
	return out, nil
}

func convertGeminiParts(blocks []message.ContentBlock, toolNames map[string]string) ([]geminiPart, error) {
	out := make([]geminiPart, 0, len(blocks))
	for _, block := range blocks {
		switch b := block.(type) {
		case message.TextBlock:
			if b.Text == "" {
				continue
			}
			out = append(out, geminiPart{Text: b.Text})
		case message.ThinkingBlock:
			// Reasoning traces are not replayed to Gemini; the API rejects thought
			// parts on input and reconstructs its own reasoning each turn.
			continue
		case message.ToolUseBlock:
			args := b.Input
			if len(args) == 0 {
				args = json.RawMessage(`{}`)
			}
			out = append(out, geminiPart{
				FunctionCall: &geminiFunctionCall{Name: b.Name, Args: args},
			})
		case message.ToolResultBlock:
			name := toolNames[b.ToolUseID]
			out = append(out, geminiPart{
				FunctionResponse: &geminiFunctionResponse{
					Name:     name,
					Response: geminiToolResponse(b.Content, b.IsError),
				},
			})
		case message.ImageBlock:
			out = append(out, geminiPart{
				InlineData: &geminiInlineData{
					MimeType: b.MimeType,
					Data:     base64.StdEncoding.EncodeToString(b.Data),
				},
			})
		default:
			return nil, fmt.Errorf("unknown block conversion: %w", ErrUnsupportedFeature)
		}
	}
	return out, nil
}

// geminiToolResponse wraps a tool result string in the JSON object Gemini's
// functionResponse.response field requires. A result that is already a JSON
// object is passed through unchanged; anything else (a bare string, array, or
// number) is wrapped under "result", and an error result under "error", so the
// model still receives a well-formed object.
func geminiToolResponse(content string, isError bool) json.RawMessage {
	if !isError {
		trimmed := strings.TrimSpace(content)
		if strings.HasPrefix(trimmed, "{") && json.Valid([]byte(trimmed)) {
			return json.RawMessage(trimmed)
		}
	}
	key := "result"
	if isError {
		key = "error"
	}
	wrapped, err := json.Marshal(map[string]string{key: content})
	if err != nil {
		return json.RawMessage(`{}`)
	}
	return wrapped
}

type geminiRequest struct {
	Contents          []geminiContent         `json:"contents"`
	SystemInstruction *geminiContent          `json:"system_instruction,omitempty"`
	Tools             []geminiTool            `json:"tools,omitempty"`
	GenerationConfig  *geminiGenerationConfig `json:"generationConfig,omitempty"`
}

// geminiCountTokensRequest is the body of a models/{model}:countTokens call.
// The endpoint counts a full generateContent payload (so system instruction and
// tools are reflected), which it accepts under the generateContentRequest field.
type geminiCountTokensRequest struct {
	GenerateContentRequest geminiGenerateContentRequest `json:"generateContentRequest"`
}

// geminiGenerateContentRequest embeds geminiRequest so its contents, system
// instruction, and tool fields flatten into the JSON, adding only the fully
// qualified model resource name that countTokens requires.
type geminiGenerateContentRequest struct {
	Model string `json:"model"`
	geminiRequest
}

// geminiCountTokensResponse carries the prompt token total reported by the
// countTokens endpoint. Other fields (billable characters, per-modality detail)
// are ignored.
type geminiCountTokensResponse struct {
	TotalTokens int `json:"totalTokens"`
}

type geminiContent struct {
	Role  string       `json:"role,omitempty"`
	Parts []geminiPart `json:"parts"`
}

type geminiPart struct {
	Text             string                  `json:"text,omitempty"`
	Thought          bool                    `json:"thought,omitempty"`
	InlineData       *geminiInlineData       `json:"inline_data,omitempty"`
	FunctionCall     *geminiFunctionCall     `json:"functionCall,omitempty"`
	FunctionResponse *geminiFunctionResponse `json:"functionResponse,omitempty"`
}

type geminiInlineData struct {
	MimeType string `json:"mime_type"`
	Data     string `json:"data"`
}

type geminiFunctionCall struct {
	Name string          `json:"name"`
	Args json.RawMessage `json:"args,omitempty"`
}

type geminiFunctionResponse struct {
	Name     string          `json:"name"`
	Response json.RawMessage `json:"response"`
}

type geminiTool struct {
	FunctionDeclarations []geminiFunctionDecl `json:"function_declarations"`
}

type geminiFunctionDecl struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	Parameters  json.RawMessage `json:"parameters,omitempty"`
}

type geminiGenerationConfig struct {
	Temperature     float64               `json:"temperature,omitempty"`
	MaxOutputTokens int                   `json:"maxOutputTokens,omitempty"`
	ThinkingConfig  *geminiThinkingConfig `json:"thinkingConfig,omitempty"`
}

// geminiThinkingConfig controls native extended thinking on Gemini 2.5 models.
// IncludeThoughts requests thought summaries in the stream. ThinkingBudget caps
// the tokens spent reasoning; it is a pointer so an explicit zero (which disables
// thinking on Flash) is distinguishable from "unset", though the request builder
// only sets a positive budget today.
type geminiThinkingConfig struct {
	IncludeThoughts bool `json:"includeThoughts,omitempty"`
	ThinkingBudget  *int `json:"thinkingBudget,omitempty"`
}

type geminiStreamChunk struct {
	Candidates []struct {
		Content      geminiContent `json:"content"`
		FinishReason string        `json:"finishReason"`
	} `json:"candidates"`
	UsageMetadata  *geminiUsageMetadata  `json:"usageMetadata"`
	PromptFeedback *geminiPromptFeedback `json:"promptFeedback"`
	Error          *geminiErrorBody      `json:"error"`
}

// geminiPromptFeedback carries the verdict of Gemini's input safety filters.
// A non-empty BlockReason (for example SAFETY or BLOCKLIST) means the prompt was
// rejected before any candidate was generated.
type geminiPromptFeedback struct {
	BlockReason string `json:"blockReason"`
}

type geminiUsageMetadata struct {
	PromptTokenCount        int `json:"promptTokenCount"`
	CandidatesTokenCount    int `json:"candidatesTokenCount"`
	CachedContentTokenCount int `json:"cachedContentTokenCount"`
	// ThoughtsTokenCount counts tokens spent on native reasoning by a Gemini 2.5
	// thinking model. The API reports these separately and excludes them from
	// CandidatesTokenCount, yet bills them as output, so they are folded into
	// OutputTokens below to keep token accounting and cost estimates accurate.
	ThoughtsTokenCount int `json:"thoughtsTokenCount"`
}

func (u geminiUsageMetadata) toUsage() Usage {
	return Usage{
		InputTokens:     u.PromptTokenCount,
		OutputTokens:    u.CandidatesTokenCount + u.ThoughtsTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
	}
}

type geminiErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
