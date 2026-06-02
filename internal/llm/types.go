// Package llm normalizes provider chat APIs behind a small streaming
// interface used by the agent loop.
package llm

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// Sentinel errors returned by providers and registry lookups.
var (
	// ErrRateLimit indicates the provider rejected the request due to quota
	// or rate limits.
	ErrRateLimit = errors.New("rate limit exceeded")
	// ErrContextLimit indicates the prompt exceeded the model context window.
	ErrContextLimit = errors.New("context limit exceeded")
	// ErrAuth indicates provider credentials are missing or invalid.
	ErrAuth = errors.New("authentication failed")
	// ErrServer indicates a transient provider-side failure.
	ErrServer = errors.New("provider server error")
	// ErrModelNotFound indicates the requested model is unknown.
	ErrModelNotFound = errors.New("model not found")
	// ErrUnsupportedFeature indicates the request used a feature unavailable
	// for the selected provider or model.
	ErrUnsupportedFeature = errors.New("unsupported feature")
	// ErrNotYetSupported indicates a registered provider has no client yet.
	ErrNotYetSupported = errors.New("provider not yet supported")
	// ErrProviderNotFound indicates a registry lookup used an unknown name.
	ErrProviderNotFound = errors.New("provider not found")
)

// Provider streams model responses from one backend.
type Provider interface {
	Name() string
	Stream(ctx context.Context, req Request) (<-chan Event, error)
	Models() []Model
	SupportsTools() bool
	SupportsImages() bool
}

// Request is the provider-independent chat request.
type Request struct {
	Model        string
	Messages     []message.Message
	Tools        []Tool
	Temperature  float64
	MaxTokens    int
	SystemPrompt string
	// ReasoningEffort selects how much hidden reasoning an OpenAI reasoning
	// model (o-series, gpt-5 reasoning) spends before answering. Valid values
	// are provider-defined ("low", "medium", "high"). It is passed through to
	// the OpenAI request only for reasoning models and ignored otherwise.
	ReasoningEffort string
	// Thinking opts the request into Anthropic extended thinking. When set and
	// the model supports it, the Anthropic provider streams thinking deltas as
	// ThinkingEvents before the answer text. It is ignored by providers that do
	// not support extended thinking.
	Thinking *ThinkingConfig
}

// ThinkingConfig enables Anthropic extended thinking for a request. BudgetTokens
// caps how many tokens the model may spend on its visible reasoning pass; it
// must be a positive value below MaxTokens for the request to be accepted.
type ThinkingConfig struct {
	BudgetTokens int
}

// Tool describes one callable function available to the model.
type Tool struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// Model describes one model exposed by a provider.
type Model struct {
	ID                    string  `json:"id"`
	Provider              string  `json:"provider"`
	ContextWindow         int     `json:"context_window"`
	InputPricePerMTokUSD  float64 `json:"input_price_per_mtok_usd"`
	OutputPricePerMTokUSD float64 `json:"output_price_per_mtok_usd"`
	SupportsImages        bool    `json:"supports_images"`
	SupportsTools         bool    `json:"supports_tools"`
	Pending               bool    `json:"pending,omitempty"`
	// ReasoningEffort is the configured hidden-reasoning budget for an OpenAI
	// reasoning model ("low", "medium", "high"), or empty for none. The agent
	// loop copies it onto Request.ReasoningEffort for this model; providers gate
	// it by model id, so it is harmless on a non-reasoning model.
	ReasoningEffort string `json:"reasoning_effort,omitempty"`
	// ThinkingBudget is the configured extended-thinking token budget for an
	// Anthropic thinking model, or zero for none. The agent loop copies it onto
	// Request.Thinking for this model when positive; providers gate it by model
	// id, so it is harmless on a non-thinking model.
	ThinkingBudget int `json:"thinking_budget,omitempty"`
}

// Usage records provider-reported token counts.
type Usage struct {
	InputTokens      int `json:"input_tokens"`
	OutputTokens     int `json:"output_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Event is emitted by Provider.Stream.
type Event interface {
	isEvent()
}

// StartEvent indicates the provider accepted the request.
type StartEvent struct {
	Provider string
	Model    string
}

// DeltaTextEvent carries incremental assistant text.
type DeltaTextEvent struct {
	Text string
}

// ToolUseStartEvent indicates a tool call has begun.
type ToolUseStartEvent struct {
	ID   string
	Name string
}

// ToolUseDeltaEvent carries incremental tool input JSON.
type ToolUseDeltaEvent struct {
	ID    string
	Delta string
}

// ToolUseEndEvent indicates a tool call finished.
type ToolUseEndEvent struct {
	ID    string
	Name  string
	Input json.RawMessage
}

// ThinkingEvent carries provider reasoning text when available.
type ThinkingEvent struct {
	Text string
}

// EndEvent indicates the stream closed normally.
type EndEvent struct {
	Usage Usage
}

// ErrorEvent indicates the stream failed after it had started.
type ErrorEvent struct {
	Err error
}

func (StartEvent) isEvent()        {}
func (DeltaTextEvent) isEvent()    {}
func (ToolUseStartEvent) isEvent() {}
func (ToolUseDeltaEvent) isEvent() {}
func (ToolUseEndEvent) isEvent()   {}
func (ThinkingEvent) isEvent()     {}
func (EndEvent) isEvent()          {}
func (ErrorEvent) isEvent()        {}
