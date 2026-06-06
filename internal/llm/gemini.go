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

// geminiSafetyCategories lists the harm categories Gemini scores on both the
// prompt and the response. The agent relaxes every category to BLOCK_NONE (see
// geminiSafetySettings) so that legitimate software-engineering content — shell
// commands, security tooling, exploit write-ups in a CTF context — is not
// silently dropped with a SAFETY finishReason. HARM_CATEGORY_CIVIC_INTEGRITY is
// intentionally omitted: it is not accepted on all Gemini model versions and a
// rejected category would 400 the whole request.
var geminiSafetyCategories = []string{
	"HARM_CATEGORY_HARASSMENT",
	"HARM_CATEGORY_HATE_SPEECH",
	"HARM_CATEGORY_SEXUALLY_EXPLICIT",
	"HARM_CATEGORY_DANGEROUS_CONTENT",
}

// geminiSafetySettings relaxes every scored harm category to BLOCK_NONE. The
// threshold BLOCK_NONE (rather than the newer OFF) is used because it is
// accepted across the whole Gemini 1.5/2.x/3 line, whereas OFF is rejected by
// older versions. Set once at init since it never varies per request.
var geminiSafetySettings = func() []geminiSafetySetting {
	settings := make([]geminiSafetySetting, 0, len(geminiSafetyCategories))
	for _, category := range geminiSafetyCategories {
		settings = append(settings, geminiSafetySetting{
			Category:  category,
			Threshold: "BLOCK_NONE",
		})
	}
	return settings
}()

// geminiMinimalThinkingBudget is the budget mapped from the "minimal" effort on
// the Gemini 2.5 family: the smallest reasoning allowance that still sits inside
// the range every 2.5 model accepts. Gemini 2.5 Pro cannot disable thinking and
// floors its budget at 128 tokens, so a budget below that risks a 400 on Pro; a
// budget comfortably above the floor (and well below the "low" 4096) expresses
// "reason as little as possible" while staying valid across the whole 2.5 line.
const geminiMinimalThinkingBudget = 512

// geminiThinkingDisabled is the sentinel geminiThinkingBudgetForEffort returns for
// the "none" effort, meaning "do not reason at all". It is distinct from 0 (which
// the builder uses for "thinking unconfigured") so the request builder can tell
// "turn thinking off" apart from "leave the model's default". The actual wire value
// for disabling is a thinkingBudget of 0, emitted by the builder only for models
// that accept it (the Gemini 2.5 Flash line); see geminiModelCanDisableThinking.
const geminiThinkingDisabled = -2

// geminiThinkingBudgetForEffort maps the provider-independent reasoning_effort
// label onto a Gemini thinkingBudget (in tokens). It lets a user opt a Gemini
// 2.5 model into native thinking with the same "minimal"/"low"/"medium"/"high"
// knob the OpenAI reasoning models use, instead of having to pick a raw token
// count. The chosen budgets sit inside the range both Flash (0–24576) and Pro
// (128–32768) accept, so the same effort is valid across the 2.5 family. The
// "auto" and "dynamic" labels map to -1, Gemini's sentinel for dynamic thinking:
// the model sizes its own reasoning per request instead of being held to a fixed
// budget. The "none" label maps to geminiThinkingDisabled, the request to turn
// reasoning off. An empty or unrecognized label returns 0, which leaves thinking
// unconfigured (the model's own default applies).
func geminiThinkingBudgetForEffort(effort string) int {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "none":
		// Parity with the OpenAI/OpenRouter reasoning knob, where "none" turns
		// reasoning off. Returned as a distinct sentinel rather than 0 so the
		// builder can disable thinking (a 0 thinkingBudget) on models that allow it
		// instead of falling through to "unconfigured" and leaving thinking on.
		return geminiThinkingDisabled
	case "minimal":
		// Parity with the OpenAI reasoning knob (where "minimal" is the fastest,
		// least-reasoning setting) and with the Gemini 3 path, which already maps
		// "minimal" to its lowest thinkingLevel. Without this case "minimal" fell
		// through to 0, leaving thinkingConfig unset so the model's own (much
		// larger) default budget applied — the opposite of the requested intent.
		return geminiMinimalThinkingBudget
	case "low":
		return 4096
	case "medium":
		return 8192
	case "high":
		return 16384
	case "auto", "dynamic":
		// -1 is Gemini's sentinel for dynamic thinking: the model decides how
		// many tokens to spend reasoning, rather than being pinned to a budget.
		return -1
	default:
		return 0
	}
}

// geminiThinkingLevelBudgetThreshold splits a numeric thinking budget into the
// two thinkingLevel buckets Gemini 3 universally accepts: a budget at or below it
// maps to "low", a larger one to "high". It sits at the "medium" 2.5-era effort
// budget so the low/medium/high effort budgets bucket as low/high/high.
const geminiThinkingLevelBudgetThreshold = 8192

// geminiThinkingLevelForEffort maps the provider-independent reasoning_effort
// label onto a Gemini 3 thinkingLevel. Gemini 3 replaced the 2.5-era numeric
// thinkingBudget with a coarse level knob, and only "low" and "high" are accepted
// across the whole Gemini 3 line (the base Gemini 3 Pro does not accept the
// "minimal"/"medium" levels some later variants added). The four effort labels
// are therefore clamped to those two to avoid a 400 on any Gemini 3 model:
// "low"/"minimal" -> "low", "medium"/"high" -> "high". An empty, "auto"/"dynamic",
// or unrecognized label returns "" so thinkingLevel is omitted and the model's
// own default level applies.
func geminiThinkingLevelForEffort(effort string) string {
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "low", "minimal":
		return "low"
	case "medium", "high":
		return "high"
	default:
		return ""
	}
}

// geminiThinkingLevel selects the Gemini 3 thinkingLevel for a request. An
// explicit numeric ThinkingConfig budget (the 2.5-era knob) takes precedence and
// is bucketed by geminiThinkingLevelBudgetThreshold, mirroring the budget path's
// precedence over reasoning_effort; a negative budget means dynamic thinking,
// which has no level equivalent, so it returns "" to leave the model's default.
// Otherwise the configured reasoning_effort drives the level. It returns "" when
// neither is configured.
func geminiThinkingLevel(req Request) string {
	if req.Thinking != nil && req.Thinking.BudgetTokens != 0 {
		if req.Thinking.BudgetTokens < 0 {
			return ""
		}
		if req.Thinking.BudgetTokens <= geminiThinkingLevelBudgetThreshold {
			return "low"
		}
		return "high"
	}
	return geminiThinkingLevelForEffort(req.ReasoningEffort)
}

// isGemini3Model reports whether id names a Gemini 3 model, which controls
// reasoning with thinkingLevel rather than the 2.5-era thinkingBudget. The match
// is the same case-insensitive substring scan used elsewhere; the rolling
// "-latest" aliases are deliberately not matched here because they currently
// resolve to the Gemini 2.5 generation (see geminiThinkingModelSubstrings).
func isGemini3Model(id string) bool {
	return strings.Contains(strings.ToLower(strings.TrimSpace(id)), "gemini-3")
}

// geminiModelCanDisableThinking reports whether the Gemini 2.5 model named by id
// accepts a thinkingBudget of 0 to turn reasoning off. Only the Flash line —
// gemini-2.5-flash, gemini-2.5-flash-lite, and the rolling gemini-flash-latest /
// gemini-flash-lite-latest aliases that resolve to it — allows it. Gemini 2.5 Pro
// cannot disable thinking (its budget floors at 128 and a 0 is a 400), so it is
// excluded. Gemini 3 controls reasoning with thinkingLevel rather than a numeric
// budget and is handled on its own path, so it is not matched here. The match is
// the same case-insensitive substring scan used by the other capability checks.
func geminiModelCanDisableThinking(id string) bool {
	lid := strings.ToLower(strings.TrimSpace(id))
	if !strings.Contains(lid, "flash") {
		return false
	}
	return strings.Contains(lid, "gemini-2.5") ||
		strings.Contains(lid, "gemini-flash-latest") ||
		strings.Contains(lid, "gemini-flash-lite-latest")
}

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
// sentinel so the failover and backoff layers can react. Gemini reports
// transient capacity loss with status UNAVAILABLE and quota exhaustion with
// RESOURCE_EXHAUSTED (both retryable), and terminal conditions such as a bad
// key or unknown model with UNAUTHENTICATED/NOT_FOUND. This mirrors the
// pre-stream classifyHTTPError mapping so the same failure is classified the
// same whether it arrives before or during the stream; an unrecognized status
// is returned without a sentinel so it is not retried.
func classifyGeminiStreamError(status string, code int, msg string) error {
	switch strings.ToUpper(strings.TrimSpace(status)) {
	case "RESOURCE_EXHAUSTED":
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrRateLimit)
	case "UNAVAILABLE", "INTERNAL", "DEADLINE_EXCEEDED":
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrServer)
	case "UNAUTHENTICATED", "PERMISSION_DENIED":
		// A missing/invalid key or a project without access to the model arrives
		// mid-stream with these statuses; map them to ErrAuth so the caller
		// surfaces a credential error rather than a generic, retried failure.
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrAuth)
	case "NOT_FOUND":
		// An unknown or unavailable model id is reported as NOT_FOUND; surface it
		// as ErrModelNotFound to match the pre-stream HTTP classification.
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrModelNotFound)
	case "INVALID_ARGUMENT":
		// A prompt that overflows the model context window comes back as
		// INVALID_ARGUMENT whose message names the token overflow. Surface it as
		// ErrContextLimit so the agent's compaction/overflow path can recover
		// instead of treating it as an unrecoverable bad request.
		if mentionsContextLimit(msg) {
			return fmt.Errorf("provider stream error: %s: %w", msg, ErrContextLimit)
		}
	}
	// Some relays omit the status string and carry only the HTTP code, so fall
	// back to the same code-based mapping classifyHTTPError uses.
	switch {
	case code == http.StatusTooManyRequests:
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrRateLimit)
	case code == http.StatusUnauthorized || code == http.StatusForbidden:
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrAuth)
	case code == http.StatusNotFound:
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrModelNotFound)
	case code >= 500:
		return fmt.Errorf("provider stream error: %s: %w", msg, ErrServer)
	}
	return fmt.Errorf("provider stream error: %s", msg)
}

func (p *geminiProvider) buildGeminiRequest(req Request) (geminiRequest, error) {
	contents, err := buildGeminiContents(req.Messages)
	if err != nil {
		return geminiRequest{}, err
	}

	out := geminiRequest{Contents: contents, SafetySettings: geminiSafetySettings}

	if req.SystemPrompt != "" {
		out.SystemInstruction = &geminiContent{
			Parts: []geminiPart{{Text: req.SystemPrompt}},
		}
	}

	if len(req.Tools) > 0 {
		decls := make([]geminiFunctionDecl, 0, len(req.Tools))
		for _, tool := range req.Tools {
			schema := sanitizeGeminiSchema(tool.InputSchema)
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

	// Native extended thinking is opt-in per request and only emitted for a model
	// that supports thinkingConfig. The budget/level comes from an explicit
	// ThinkingConfig when set, otherwise from the configured reasoning_effort (the
	// uniform knob OpenAI reasoning models use), so a Gemini user gets parity
	// without having to hand-tune a token count. Like Anthropic's path, an
	// unsupported thinking request is silently dropped rather than rejected: the
	// support check is an approximate model-id heuristic, so degrading gracefully
	// beats 400-ing a valid request on a false negative. IncludeThoughts asks the
	// API for thought summaries, which the stream surfaces as ThinkingEvents.
	//
	// The Gemini 3 line replaced the 2.5-era numeric thinkingBudget with a coarse
	// thinkingLevel knob, and sending a thinkingBudget to a Gemini 3 model is a
	// 400 — so the two generations take different fields, gated by the model id.
	if modelSupportsGeminiThinking(p.models, req.Model) {
		if isGemini3Model(req.Model) {
			// Gemini 3: select a thinkingLevel. An empty level (nothing configured,
			// or a dynamic request that has no level equivalent) leaves thinkingConfig
			// off so the model's own default level applies. There is no maxOutputTokens
			// reservation: Gemini 3 manages its own reasoning allowance.
			if level := geminiThinkingLevel(req); level != "" {
				if out.GenerationConfig == nil {
					out.GenerationConfig = &geminiGenerationConfig{}
				}
				out.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
					IncludeThoughts: true,
					ThinkingLevel:   level,
				}
			}
		} else {
			// Gemini 2.5: a positive budget pins reasoning to a token cap; -1 selects
			// dynamic thinking (the model sizes its own reasoning). Both configure
			// thinkingConfig; only an unset (0) budget leaves it off.
			budget := 0
			if req.Thinking != nil && req.Thinking.BudgetTokens != 0 {
				budget = req.Thinking.BudgetTokens
			} else {
				budget = geminiThinkingBudgetForEffort(req.ReasoningEffort)
			}
			if budget == geminiThinkingDisabled {
				// "none": turn reasoning off by pinning the budget to 0, but only on
				// the Flash line that accepts it. Gemini 2.5 Pro cannot disable
				// thinking and 400s on a 0 budget, so there "none" is left as the
				// model's default (thinkingConfig omitted), mirroring the graceful
				// degradation the other thinking paths apply to unsupported requests.
				// No maxOutputTokens reservation is needed: a disabled pass consumes
				// no thinking tokens.
				if geminiModelCanDisableThinking(req.Model) {
					if out.GenerationConfig == nil {
						out.GenerationConfig = &geminiGenerationConfig{}
					}
					disabled := 0
					out.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{ThinkingBudget: &disabled}
				}
			} else if budget != 0 {
				if out.GenerationConfig == nil {
					out.GenerationConfig = &geminiGenerationConfig{}
				}
				out.GenerationConfig.ThinkingConfig = &geminiThinkingConfig{
					IncludeThoughts: true,
					ThinkingBudget:  &budget,
				}
				// On Gemini 2.5 the thinking tokens are carved out of the same
				// maxOutputTokens allowance and billed as output, so a positive cap at
				// or below the budget leaves no room for a visible answer (the model
				// spends the whole allowance reasoning and the candidate comes back
				// empty or truncated). Lift the cap to the budget plus a full default
				// allowance, mirroring the Anthropic path. A zero cap is left untouched
				// so the model's own (much larger) default applies. Dynamic thinking
				// (-1) has no fixed budget to reserve room beyond, so the cap is left
				// as configured.
				if budget > 0 && out.GenerationConfig.MaxOutputTokens > 0 && out.GenerationConfig.MaxOutputTokens <= budget {
					out.GenerationConfig.MaxOutputTokens = budget + defaultGeminiMaxTokens
				}
			}
		}
	}

	return out, nil
}

// geminiUnsupportedSchemaKeys names JSON Schema keywords that the Gemini
// generateContent function-declaration parameters field rejects with a 400
// ("Unknown name ..."). Gemini accepts only an OpenAPI 3.0 Schema subset, so a
// tool whose InputSchema carries any of these — as schemas emitted by common
// JSON Schema generators routinely do (a top-level "$schema", an
// "additionalProperties": false on every object) — fails the whole request.
// Each key here is metadata or a constraint Gemini ignores anyway, so dropping
// it is safe and changes no accepted-request semantics. "$ref"/"$defs" are
// deliberately absent because deletion would leave a dangling reference; they
// are instead inlined by resolveGeminiSchemaRefs (run before this strip pass),
// which replaces each local $ref with a copy of its target and drops the now
// unused definition containers.
var geminiUnsupportedSchemaKeys = map[string]struct{}{
	"$schema":               {},
	"$id":                   {},
	"$comment":              {},
	"additionalProperties":  {},
	"patternProperties":     {},
	"unevaluatedProperties": {},
}

// geminiSupportedStringFormats names the only "format" values Gemini's
// generateContent Schema accepts on a STRING-typed property: "enum" and
// "date-time". Any other format (the JSON Schema string formats common in
// hand-written and generated tool schemas — "uri", "email", "uuid", "hostname",
// "ipv4", "date", ...) is rejected with a 400 ("Invalid JSON payload ... format"),
// so sanitizeGeminiSchema drops the field rather than fail the whole request.
// Number/integer formats ("int32", "int64", "float", "double") live on a
// differently-typed node and are left untouched.
var geminiSupportedStringFormats = map[string]struct{}{
	"enum":      {},
	"date-time": {},
}

// sanitizeGeminiSchema returns raw with every Gemini-unsupported JSON Schema
// keyword (see geminiUnsupportedSchemaKeys) recursively removed, so a tool
// schema written for the OpenAI/Anthropic dialects is accepted by Gemini's
// stricter parameters field instead of 400-ing the request. It recurses
// generically into every nested value, so keywords buried in "properties",
// "items", or an "anyOf" branch are stripped too. A schema that does not parse
// as JSON is returned unchanged: the sanitizer never makes a request worse than
// the raw passthrough it replaced.
func sanitizeGeminiSchema(raw json.RawMessage) json.RawMessage {
	if len(raw) == 0 {
		return raw
	}
	var decoded any
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return raw
	}
	// Inline local $ref/$defs first so the inlined subtrees also pass through the
	// unsupported-key strip below, then drop the keys Gemini rejects.
	cleaned, err := json.Marshal(stripGeminiSchemaKeys(resolveGeminiSchemaRefs(decoded)))
	if err != nil {
		return raw
	}
	return cleaned
}

// resolveGeminiSchemaRefs inlines local JSON Schema references in an
// already-decoded tool schema so the result carries no "$ref", "$defs", or
// "definitions" — keywords Gemini's function-declaration parameters field does
// not support. Common JSON Schema generators factor shared or nested object
// types into a "$defs"/"definitions" container and reference them by a local
// "#/$defs/Name" pointer; sending such a schema to Gemini 400s, so each pointer
// is replaced by a copy of its target and the now-unused containers are dropped.
//
// Only local pointers into the root's own definition containers are resolved. A
// reference that targets something else (an external URL, or a name with no
// matching definition) is left untouched rather than guessed at, so the schema
// is never made worse than the raw passthrough. A recursive reference — a
// definition that reaches itself — cannot be expressed as a finite inlined tree,
// so it collapses to a permissive {"type":"object"} to keep resolution
// terminating instead of looping.
func resolveGeminiSchemaRefs(root any) any {
	obj, ok := root.(map[string]any)
	if !ok {
		return root
	}
	defs := collectGeminiSchemaDefs(obj)
	if len(defs) == 0 {
		return root
	}
	resolved := inlineGeminiSchemaRefs(obj, defs, nil)
	if m, ok := resolved.(map[string]any); ok {
		// The containers are fully inlined now; Gemini rejects them, so remove them.
		delete(m, "$defs")
		delete(m, "definitions")
	}
	return resolved
}

// collectGeminiSchemaDefs indexes the root schema's "$defs" and "definitions"
// containers by the JSON Pointer key a local $ref uses to reach them (e.g.
// "$defs/Address"), so inlineGeminiSchemaRefs can look a reference up directly.
func collectGeminiSchemaDefs(root map[string]any) map[string]any {
	defs := make(map[string]any)
	for _, container := range []string{"$defs", "definitions"} {
		sub, ok := root[container].(map[string]any)
		if !ok {
			continue
		}
		for name, schema := range sub {
			defs[container+"/"+name] = schema
		}
	}
	return defs
}

// inlineGeminiSchemaRefs walks a decoded schema, replacing every resolvable
// local $ref with a deep copy of its target (recursing into the copy so nested
// references are inlined too) and recursing into all other object values and
// array elements. active tracks the references currently being expanded along
// the path so a cycle is detected and collapsed rather than followed forever.
func inlineGeminiSchemaRefs(node any, defs map[string]any, active map[string]bool) any {
	switch v := node.(type) {
	case map[string]any:
		if ref, ok := v["$ref"].(string); ok {
			key := strings.TrimPrefix(ref, "#/")
			target, found := defs[key]
			if !found {
				// External or unknown reference: leave it as-is.
				return v
			}
			if active[key] {
				// Recursive reference: collapse to a permissive object so inlining
				// terminates.
				return map[string]any{"type": "object"}
			}
			next := make(map[string]bool, len(active)+1)
			for k := range active {
				next[k] = true
			}
			next[key] = true
			return inlineGeminiSchemaRefs(deepCopyJSONValue(target), defs, next)
		}
		for key, child := range v {
			v[key] = inlineGeminiSchemaRefs(child, defs, active)
		}
		return v
	case []any:
		for i := range v {
			v[i] = inlineGeminiSchemaRefs(v[i], defs, active)
		}
		return v
	default:
		return node
	}
}

// deepCopyJSONValue returns an independent copy of a decoded JSON value so a
// definition referenced from more than one place can be inlined into each site
// without the copies sharing (and mutating) the same nested maps.
func deepCopyJSONValue(node any) any {
	switch v := node.(type) {
	case map[string]any:
		cp := make(map[string]any, len(v))
		for k, val := range v {
			cp[k] = deepCopyJSONValue(val)
		}
		return cp
	case []any:
		cp := make([]any, len(v))
		for i, val := range v {
			cp[i] = deepCopyJSONValue(val)
		}
		return cp
	default:
		return node
	}
}

// stripGeminiSchemaKeys walks an already-decoded JSON value, deleting
// unsupported keys from every object it contains and recursing into the
// remaining values and into array elements. It additionally drops a "format"
// that Gemini rejects on a STRING-typed node (see geminiSupportedStringFormats),
// which would otherwise 400 the whole request.
func stripGeminiSchemaKeys(node any) any {
	switch v := node.(type) {
	case map[string]any:
		if t, ok := v["type"].(string); ok && t == "string" {
			if f, ok := v["format"].(string); ok {
				if _, supported := geminiSupportedStringFormats[f]; !supported {
					delete(v, "format")
				}
			}
		}
		for key := range v {
			if _, bad := geminiUnsupportedSchemaKeys[key]; bad {
				delete(v, key)
				continue
			}
			v[key] = stripGeminiSchemaKeys(v[key])
		}
		return v
	case []any:
		for i := range v {
			v[i] = stripGeminiSchemaKeys(v[i])
		}
		return v
	default:
		return node
	}
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
	SafetySettings    []geminiSafetySetting   `json:"safetySettings,omitempty"`
}

// geminiSafetySetting overrides the block threshold for one harm category. See
// geminiSafetySettings for why the agent sends BLOCK_NONE across the board.
type geminiSafetySetting struct {
	Category  string `json:"category"`
	Threshold string `json:"threshold"`
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
	// ThinkingLevel is the Gemini 3 reasoning knob ("low"/"high"), set instead of
	// ThinkingBudget for the Gemini 3 line, which rejects thinkingBudget. Exactly
	// one of ThinkingLevel / ThinkingBudget is populated per request, gated by the
	// model generation; both are omitempty so the unused one drops out of the body
	// (sending both is itself a 400).
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
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
	// PromptTokenCount is the total prompt size. Unlike Anthropic's input_tokens
	// (which excludes cached tokens), Gemini folds CachedContentTokenCount into
	// this total, so toUsage subtracts the cached portion back out to get the
	// non-cached input — see below.
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
	// Gemini's promptTokenCount is the total prompt size *including* the cached
	// tokens, whereas the ledger prices InputTokens and CacheReadTokens
	// additively (the Anthropic convention, where input_tokens already excludes
	// the cached portion). Subtract the cached tokens back out of InputTokens so
	// they are billed once at the cache rate rather than twice — once at the full
	// input rate and again at the cache rate. Clamp at zero to stay robust against
	// a malformed response where the cached count somehow exceeds the prompt total.
	input := u.PromptTokenCount - u.CachedContentTokenCount
	if input < 0 {
		input = 0
	}
	return Usage{
		InputTokens:     input,
		OutputTokens:    u.CandidatesTokenCount + u.ThoughtsTokenCount,
		CacheReadTokens: u.CachedContentTokenCount,
	}
}

type geminiErrorBody struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
	Status  string `json:"status"`
}
