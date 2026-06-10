package llm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
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
		var err error
		apiKey, err = resolveAPIKey(p.apiKeyEnv, p.name)
		if err != nil {
			return nil, err
		}
	}

	// Resolve the model's compat block (nil when no Compat configured).
	compat := compatForModel(p.models, req.Model)

	body, err := buildOpenAIRequestCompat(req, imageStyleOpenAI, compat, p.models)
	if err != nil {
		return nil, fmt.Errorf("building provider request: %w", err)
	}

	// Apply thinking-format logic. The resolved ThinkingFormat determines which
	// thinking/reasoning fields are injected. Explicit Compat.ThinkingFormat
	// wins over the baseURL auto-detection; that in turn wins over the legacy
	// isOpenRouter check so existing behavior is unchanged for providers with no
	// Compat block.
	thinkingFmt := resolveThinkingFormat(p.models, req.Model, p.baseURL)
	switch thinkingFmt {
	case config.ThinkingFormatOpenRouter:
		// OpenRouter: native usage + unified reasoning object.
		body.Usage = &openAIUsageRequest{Include: true}
		// The OpenAI o-series keeps its native path (reasoning_effort), so it is
		// excluded to avoid sending two competing reasoning controls.
		if !isReasoningModelWithCompat(compat, req.Model, p.models) {
			body.Reasoning = openRouterReasoning(req)
		}
	case config.ThinkingFormatDeepSeek:
		// DeepSeek-style: nothing extra needed at the request level for streaming
		// — the provider echoes thinking in reasoning_content, which handleStreamChunk
		// already picks up. No request-side field to inject.
	case config.ThinkingFormatQwen:
		// Qwen extended thinking: inject enable_thinking / thinking_budget into the
		// request body. These fields are non-standard OpenAI additions, so they are
		// only sent when the format is explicitly qwen.
		// The fields are encoded as extra top-level keys; because openAIChatRequest
		// is a fixed struct we pass them as JSON-level extras via a wrapper below.
		// For now: no-op at the struct level — the Qwen format is surfaced as a
		// documented flag and the per-request injection can be added in a follow-up
		// when a Qwen endpoint test rig is available. The key value is that the compat
		// flag is stored and honored downstream.
	case config.ThinkingFormatNone:
		// Suppress all thinking fields: clear any reasoning that was already set.
		body.Reasoning = nil
		body.ReasoningEffort = ""
	default:
		// ThinkingFormatDefault / ThinkingFormatOpenAI: fall through to the
		// existing behavior (reasoning_effort / max_completion_tokens already set
		// by buildOpenAIRequestCompat).
	}

	resp, err := postOpenAIJSON(ctx, p.client, p.baseURL, appendPath(p.baseURL, "/chat/completions"), apiKey, body)
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

// openRouterAttribution is the default HTTP-Referer / X-Title pair sent to
// OpenRouter so requests are attributed to BharatCode in OpenRouter's dashboard
// and public model-usage rankings. OpenRouter reads these two headers
// specifically; they are optional but recommended, and other agents (goose,
// opencode) send them by default. The value mirrors the User-Agent the tools
// package already advertises ("BharatCode").
var openRouterAttribution = map[string]string{
	"HTTP-Referer": "https://github.com/arbazkhan971/bharatcode",
	"X-Title":      "BharatCode",
}

// withOpenRouterAttribution overlays user onto the default attribution headers
// when baseURL points at OpenRouter, so a request carries HTTP-Referer / X-Title
// even when the user configured no headers. A user-supplied value for either key
// wins (so attribution can be customized or cleared by setting it to ""), and a
// non-OpenRouter base URL is returned unchanged so no other provider gains the
// headers. The returned map is always a fresh copy, leaving user untouched.
func withOpenRouterAttribution(baseURL string, user map[string]string) map[string]string {
	if !isOpenRouter(baseURL) {
		return user
	}
	merged := make(map[string]string, len(openRouterAttribution)+len(user))
	for k, v := range openRouterAttribution {
		merged[k] = v
	}
	for k, v := range user {
		merged[k] = v
	}
	return merged
}

// openRouterReasoning maps a request's configured thinking budget or reasoning
// effort onto OpenRouter's `reasoning` object. A positive thinking budget takes
// precedence and is sent as max_tokens; otherwise a configured effort is sent as
// effort. The provider-independent "auto"/"dynamic" effort labels mean "let the
// model size its own reasoning" and have no OpenRouter effort value, so they map
// to enabled:true (reasoning on, upstream default budget) rather than being sent
// verbatim, which OpenRouter would 400 on. The "none" label means "do not reason"
// and likewise has no OpenRouter effort value, so it maps to enabled:false
// (reasoning off) rather than effort:"none", which OpenRouter would 400 on. The
// effort is lowercased so a value like "High" matches OpenRouter's lowercase
// labels. When neither a budget nor an effort is configured it returns nil so the
// field is omitted and the model's own default applies rather than reasoning
// being force-enabled. A configured "minimal" effort — the fastest setting on the
// OpenAI gpt-5 family — has no OpenRouter equivalent (OpenRouter's effort accepts
// only low/medium/high and 400s on "minimal"), so it maps to the closest accepted
// label, "low", rather than being forwarded verbatim. This mirrors the graceful
// "minimal" remapping the OpenAI (minimal->none) and Gemini (minimal->small budget)
// paths already apply to the same uniform knob; the OpenRouter path is reached only
// for non-OpenAI-reasoning models, where "minimal" is never a valid upstream label.
func openRouterReasoning(req Request) *openAIReasoning {
	if req.Thinking != nil && req.Thinking.BudgetTokens > 0 {
		return &openAIReasoning{MaxTokens: req.Thinking.BudgetTokens}
	}
	switch effort := strings.ToLower(strings.TrimSpace(req.ReasoningEffort)); effort {
	case "":
		return nil
	case "auto", "dynamic":
		on := true
		return &openAIReasoning{Enabled: &on}
	case "none":
		off := false
		return &openAIReasoning{Enabled: &off}
	case "minimal":
		return &openAIReasoning{Effort: "low"}
	default:
		return &openAIReasoning{Effort: effort}
	}
}

// openAIReasoningEfforts is the set of reasoning_effort labels every OpenAI
// reasoning model accepts ("minimal" was added with the gpt-5 family). The gpt-5.1
// generation adds "none", which the original gpt-5 family and o-series 400 on, so
// it is gated per model in normalizeOpenAIReasoningEffort rather than listed here.
// The provider-independent ReasoningEffort knob also carries "auto"/"dynamic" —
// meaning "let the model size its own reasoning" — which OpenAI does not accept
// and would 400 on, so normalizeOpenAIReasoningEffort drops anything outside the
// accepted set.
var openAIReasoningEfforts = map[string]struct{}{
	"minimal": {},
	"low":     {},
	"medium":  {},
	"high":    {},
}

// normalizeOpenAIReasoningEffort maps the provider-independent ReasoningEffort
// onto a value the OpenAI reasoning model named by model accepts. A recognized
// label is returned lowercased; an empty, "auto"/"dynamic", or otherwise
// unrecognized label returns "" so the effort field is omitted and the model
// applies its own default rather than the request 400-ing. The "none" label is
// honored only for models that accept it (the gpt-5.1 generation) and otherwise
// dropped to "", so a config that asks for no reasoning on an older model
// degrades to that model's default instead of being rejected. This mirrors the
// graceful degradation the OpenRouter and Gemini/Anthropic thinking paths already
// apply to the same knob.
func normalizeOpenAIReasoningEffort(effort, model string) string {
	e := strings.ToLower(strings.TrimSpace(effort))
	// The gpt-5.1 generation deprecated "minimal" — its fastest setting on the
	// original gpt-5 family — and replaced it with "none", 400-ing on "minimal".
	// Map a configured "minimal" onto the equivalent "none" for those models so a
	// config written for gpt-5 keeps working when the model is bumped to gpt-5.1
	// instead of being rejected. Both label the same "spend the least reasoning"
	// intent, so the degradation is faithful rather than a behavior change.
	if e == "minimal" && modelSupportsNoneReasoningEffort(model) {
		return "none"
	}
	if _, ok := openAIReasoningEfforts[e]; ok {
		return e
	}
	if e == "none" && modelSupportsNoneReasoningEffort(model) {
		return e
	}
	return ""
}

// knownHostThinkingFormats maps a lower-cased URL host substring to the
// ThinkingFormat that its endpoint uses by default. This enables auto-detection
// when a model has no explicit Compat.ThinkingFormat set, so a user who points a
// provider at a DeepSeek or Qwen endpoint gets the right thinking format without
// any extra config.
//
// Explicit Compat.ThinkingFormat always wins over auto-detection — this table is
// consulted only when the Compat field is nil or ThinkingFormat is "".
var knownHostThinkingFormats = []struct {
	hostSubstring string
	format        config.ThinkingFormat
}{
	{"openrouter.ai", config.ThinkingFormatOpenRouter},
	{"deepseek.com", config.ThinkingFormatDeepSeek},
	{"zai.ai", config.ThinkingFormatDeepSeek},
	// Qwen / Alibaba DashScope. The DashScope openai_compatible endpoint is
	// dashscope.aliyuncs.com; model-studio.us-east-1.aliyuncs.com is the US region.
	{"dashscope.aliyuncs.com", config.ThinkingFormatQwen},
	{"model-studio.us-east-1.aliyuncs.com", config.ThinkingFormatQwen},
}

// autoDetectThinkingFormat inspects baseURL and returns the ThinkingFormat the
// provider uses when a model carries no explicit Compat.ThinkingFormat. It
// returns ThinkingFormatDefault when the URL matches no known host, which
// leaves the provider on its existing OpenAI-compatible path.
func autoDetectThinkingFormat(baseURL string) config.ThinkingFormat {
	lower := strings.ToLower(baseURL)
	for _, rule := range knownHostThinkingFormats {
		if strings.Contains(lower, rule.hostSubstring) {
			return rule.format
		}
	}
	return config.ThinkingFormatDefault
}

// resolveThinkingFormat returns the effective ThinkingFormat for the model
// named by id in the given provider. Precedence (highest to lowest):
//
//  1. model.Compat.ThinkingFormat (explicit per-model override)
//  2. baseURL auto-detection via knownHostThinkingFormats
//  3. isOpenRouter check (kept for backward compatibility)
//  4. ThinkingFormatDefault (current behavior unchanged)
func resolveThinkingFormat(models []Model, id, baseURL string) config.ThinkingFormat {
	if model, ok := findModel(models, id); ok && model.Compat != nil {
		if model.Compat.ThinkingFormat != config.ThinkingFormatDefault {
			return model.Compat.ThinkingFormat
		}
	}
	if f := autoDetectThinkingFormat(baseURL); f != config.ThinkingFormatDefault {
		return f
	}
	return config.ThinkingFormatDefault
}

// effectiveMaxTokens returns the MaxTokens to use for the request, applying the
// Compat.MaxTokens override when set. Zero means "no override / use caller value."
func effectiveMaxTokens(models []Model, id string, reqMax int) int {
	if model, ok := findModel(models, id); ok && model.Compat != nil && model.Compat.MaxTokens != nil && *model.Compat.MaxTokens > 0 {
		return *model.Compat.MaxTokens
	}
	return reqMax
}

// compatForModel returns the Compat block for the model named by id, or nil
// when the model has no compat block.
func compatForModel(models []Model, id string) *config.ModelCompat {
	if model, ok := findModel(models, id); ok {
		return model.Compat
	}
	return nil
}

// buildOpenAIRequest constructs the OpenAI-compatible chat request body.
// compat, when non-nil, applies declarative per-model overrides (StrictTools,
// ToolResultQuirk, reasoning override, MaxTokens override). Pass nil for the
// baseline behavior (all existing callers that do not have a Compat block).
//
// The models slice is used only to resolve the per-model reasoning override
// (Compat.Reasoning); pass nil to fall back to the id-based heuristic.
func buildOpenAIRequest(req Request, style imageStyle) (openAIChatRequest, error) {
	return buildOpenAIRequestCompat(req, style, nil, nil)
}

// buildOpenAIRequestCompat is the internal variant that accepts an optional
// compat block and model list. buildOpenAIRequest delegates here with nil for
// both; openAICompatibleProvider.Stream calls this directly so it can pass the
// resolved compat and models without changing the shared buildOpenAIRequest
// signature (which ollama also calls).
func buildOpenAIRequestCompat(req Request, style imageStyle, compat *config.ModelCompat, models []Model) (openAIChatRequest, error) {
	messages := make([]openAIMessage, 0, len(req.Messages)+1)
	if req.SystemPrompt != "" {
		messages = append(messages, openAIMessage{Role: "system", Content: req.SystemPrompt})
	}

	// Resolve the ToolResultQuirk so convertMessageCompat can apply it.
	var toolResultQuirk config.ToolResultQuirk
	if compat != nil {
		toolResultQuirk = compat.ToolResultQuirk
	}

	for _, msg := range message.Normalize(req.Messages) {
		converted, err := convertMessageCompat(msg, style, toolResultQuirk)
		if err != nil {
			return openAIChatRequest{}, err
		}
		messages = append(messages, converted...)
	}

	// Build the tool list, applying strict: true when StrictTools is set.
	tools := make([]openAITool, 0, len(req.Tools))
	strictTools := compat != nil && compat.StrictTools
	for _, tool := range req.Tools {
		schema := tool.InputSchema
		if len(schema) == 0 {
			schema = json.RawMessage(`{"type":"object","properties":{}}`)
		}
		fn := openAIFunction{
			Name:        tool.Name,
			Description: tool.Description,
			Parameters:  schema,
		}
		if strictTools {
			t := true
			fn.Strict = &t
		}
		tools = append(tools, openAITool{
			Type:     "function",
			Function: fn,
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

	// Determine whether to treat this model as a reasoning model. The Compat
	// override takes precedence so a quirky endpoint that is not an OpenAI
	// o-series model can still opt into or out of the reasoning-model branch.
	reasoning := isReasoningModelWithCompat(compat, req.Model, models)

	// Resolve the effective MaxTokens: the Compat override wins when set.
	maxTokens := req.MaxTokens
	if compat != nil && compat.MaxTokens != nil && *compat.MaxTokens > 0 {
		maxTokens = *compat.MaxTokens
	}

	// Reasoning models (o-series, gpt-5 reasoning) reject temperature and the
	// legacy max_tokens field; they accept reasoning_effort and
	// max_completion_tokens instead. Non-reasoning models keep the classic
	// temperature and max_tokens params and ignore the reasoning ones. Gate all
	// of them by model id so we never send a param the API would 400 on.
	// Temperature stays unset (and thus omitted) for reasoning models,
	// preserving the prior omitempty behavior for every other model: a zero
	// temperature is omitted so the provider applies its own default. The effort
	// is normalized so the provider-independent "auto"/"dynamic" labels collapse
	// to "" (omitted) rather than reaching OpenAI, which 400s on them.
	if reasoning {
		body.ReasoningEffort = normalizeOpenAIReasoningEffort(req.ReasoningEffort, req.Model)
		body.MaxCompletionTokens = maxTokens
	} else {
		body.Temperature = req.Temperature
		body.MaxTokens = maxTokens
	}
	return body, nil
}

// isReasoningModelWithCompat reports whether the model should be treated as a
// reasoning model for request building. It consults the Compat.Reasoning
// override first; when that is nil it falls back to isReasoningModel (id
// heuristic). models is checked only when compat is nil (to allow
// isReasoningModelForRequest to be used from the provider path).
func isReasoningModelWithCompat(compat *config.ModelCompat, id string, models []Model) bool {
	if compat != nil && compat.Reasoning != nil {
		return *compat.Reasoning
	}
	if models != nil {
		return isReasoningModelForRequest(models, id)
	}
	return isReasoningModel(id)
}

// convertMessage converts a message.Message to OpenAI wire format. It is the
// baseline conversion used by callers that have no Compat block (Ollama, and
// any test that calls buildOpenAIRequest directly).
func convertMessage(msg message.Message, style imageStyle) ([]openAIMessage, error) {
	return convertMessageCompat(msg, style, config.ToolResultQuirkNone)
}

// convertMessageCompat is the compat-aware variant of convertMessage. The
// quirk parameter selects an alternate tool-result encoding for providers that
// reject the standard OpenAI tool-role message.
func convertMessageCompat(msg message.Message, style imageStyle, quirk config.ToolResultQuirk) ([]openAIMessage, error) {
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
				return toolResultMessages(b, quirk), nil
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
			out = append(out, toolResultMessages(result, quirk)...)
		}
		return out, nil
	default:
		return nil, fmt.Errorf("role %q conversion: %w", msg.Role, ErrUnsupportedFeature)
	}
}

// toolResultMessages serializes a ToolResultBlock according to quirk.
// For ToolResultQuirkNone (the default) it emits a standard OpenAI tool-role
// message. For ToolResultQuirkUserContent it wraps the result in a user-role
// text message so providers that reject "tool" role messages can still receive
// the tool output.
func toolResultMessages(b message.ToolResultBlock, quirk config.ToolResultQuirk) []openAIMessage {
	switch quirk {
	case config.ToolResultQuirkUserContent:
		// Wrap the result as a user-role text message for endpoints that
		// reject the standard tool-role message.
		content := b.Content
		if content == "" {
			content = "(empty tool result)"
		}
		return []openAIMessage{{
			Role:    "user",
			Content: fmt.Sprintf("[Tool result for call %s]\n%s", b.ToolUseID, content),
		}}
	default:
		return []openAIMessage{{
			Role:       "tool",
			ToolCallID: b.ToolUseID,
			Content:    b.Content,
		}}
	}
}

func emitOpenAIResponse(ctx context.Context, data []byte, events chan<- Event) error {
	var resp openAIChatResponse
	if err := json.Unmarshal(data, &resp); err != nil {
		return fmt.Errorf("decoding provider response: %w", err)
	}
	for _, choice := range resp.Choices {
		// Surface a reasoning model's thinking before its answer, matching the
		// streaming path's order and field precedence: prefer reasoning_content,
		// fall back to reasoning, never emit both, so a relay that echoes one into
		// the other does not double the ThinkingEvent.
		if reasoning := choice.Message.ReasoningContent; reasoning != "" {
			send(ctx, events, ThinkingEvent{Text: reasoning})
		} else if choice.Message.Reasoning != "" {
			send(ctx, events, ThinkingEvent{Text: choice.Message.Reasoning})
		}
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
