# LLM

**Path:** `internal/llm/`
**Status:** First implementation complete

## Purpose

Provider abstraction for every LLM backend BharatCode talks to. A single Go interface fronts many wire formats: Anthropic's Messages API with XML-style tool-use blocks, OpenAI Chat Completions and Responses with JSON `tool_calls`, Ollama's chat API, OpenAI-compatible shims (Groq, Together, Fireworks, OpenRouter, LM Studio, DeepSeek, Moonshot), and stubs for sovereign Indian providers. The module normalizes streaming SSE and non-streaming responses into a uniform event stream, wraps retry and rate-limit handling with exponential backoff, reports per-call token usage upward, and delegates cost computation to the `ledger` module. It supports hot-swapping the active provider mid-session: the user picks a different model, the conversation history survives unchanged, and the next turn goes to the new backend. This is the single biggest module in BharatCode and its primary differentiator — first-class status for open-weight providers lives or dies here.

## Public interface

```go
type Provider interface {
    Name() string
    Stream(ctx context.Context, req Request) (<-chan Event, error)
    Models() []Model
    SupportsTools() bool
    SupportsImages() bool
}

type Request struct {
    Model        string
    Messages     []message.Message
    Tools        []Tool
    Temperature  float64
    MaxTokens    int
    SystemPrompt string
}

// Event is the sealed sum type emitted on the stream channel.
// Concrete variants:
//   StartEvent          — stream opened, model echoed back
//   DeltaTextEvent      — incremental assistant text
//   ToolUseStartEvent   — tool call begins (id, name)
//   ToolUseDeltaEvent   — incremental tool input JSON fragment
//   ToolUseEndEvent     — tool call complete with parsed input
//   ThinkingEvent       — reasoning trace (Anthropic extended thinking, DeepSeek R1)
//   EndEvent            — stream closed, includes Usage{InputTokens, OutputTokens, CacheRead, CacheWrite}
//   ErrorEvent          — typed error, stream is terminated
type Event interface{ isEvent() }

type Registry struct{ /* unexported */ }

func NewRegistry(cfg *config.Config) (*Registry, error)
func (r *Registry) Get(providerName string) (Provider, error)
func (r *Registry) ListModels() []Model
```

Subpackage layout — one concrete `Provider` implementation per file:

- `internal/llm/anthropic/` — Anthropic Messages API (Claude 3.5/3.7/4 Sonnet, Opus, Haiku)
- `internal/llm/openai/` — OpenAI Chat Completions and Responses API
- `internal/llm/gemini/` — Google Generative Language API (Gemini, native `generateContent` with tools, vision, and thinking)
- `internal/llm/deepseek/` — DeepSeek API (V3, R1)
- `internal/llm/moonshot/` — Moonshot Kimi K2
- `internal/llm/groq/` — Groq (OpenAI-compatible)
- `internal/llm/together/` — Together AI (OpenAI-compatible)
- `internal/llm/fireworks/` — Fireworks (OpenAI-compatible)
- `internal/llm/openrouter/` — OpenRouter (OpenAI-compatible, with provider routing headers)
- `internal/llm/ollama/` — Local Ollama (custom API)
- `internal/llm/lmstudio/` — Local LM Studio (OpenAI-compatible)
- `internal/llm/sovereign/` — Stubs for Sarvam, Krutrim, BharatGPT (return `ErrNotYetSupported`)

## Dependencies

Internal: `util`, `config`, `message`, `ledger`.
External: `net/http` (stdlib), `github.com/hashicorp/go-retryablehttp`, `encoding/json` (stdlib).

## Acceptance criteria

- Every provider passes a shared compliance test suite: streams a multi-turn conversation correctly, emits a tool call and accepts the tool result, reports `Usage` on `EndEvent`, surfaces typed errors instead of raw HTTP errors.
- Unit tests run against `httptest.NewServer` mocks — no real provider calls in `go test ./...`.
- Integration tests live behind `-tags=integration` and skip themselves when the per-provider env var (`ANTHROPIC_API_KEY`, `DEEPSEEK_API_KEY`, etc.) is missing.
- Mid-session provider switch: a fixture conversation with N turns, recorded against provider A, replays correctly against provider B without history loss or duplicate system prompts.
- Hot-swap and concurrent `Stream` calls on the same `Registry` are race-free under `go test -race`.

## Notes for the implementer

Tool-call dialect normalization is the hardest part of the module — budget half the implementation time for it. Anthropic emits structured `tool_use` content blocks with `id`, `name`, and an `input` object that streams as partial JSON deltas. OpenAI emits a `tool_calls` array on the assistant message with `id`, `function.name`, and `function.arguments` as a JSON string that also streams incrementally. Ollama emits OpenAI-shaped `tool_calls` but sometimes inline as text — detect and salvage. DeepSeek R1 emits separate `reasoning_content` ahead of `content`; surface it as `ThinkingEvent`. Normalize everything to internal `ToolUseStartEvent`/`ToolUseDeltaEvent`/`ToolUseEndEvent` so the agent loop never sees a wire format.

Map provider failures to typed errors so callers can branch on intent, not on string-matching HTTP bodies:

- `ErrRateLimit` — HTTP 429, or provider-specific `rate_limit_exceeded`. Retry with exponential backoff (base 1s, max 32s, jitter, cap 5 attempts). Respect `Retry-After` when present.
- `ErrContextLimit` — input too long. Never retry; bubble up to agent so it can compact or summarize.
- `ErrAuth` — 401/403. Never retry. The key is wrong; retrying just burns latency.
- `ErrServer` — 5xx. Retry once after 2s, then fail.
- `ErrModelNotFound` — 404 on the model id. Never retry.
- `ErrUnsupportedFeature` — provider does not support tools/images/thinking for the requested model. Surface to the agent so it can degrade.

`retryablehttp` handles the transport-level retries; the typed-error layer sits above it and decides whether to even hand the request to the retrying client. Authentication and "model not found" errors must short-circuit before any retry.

Streaming reads must be cancellable: the channel returned by `Stream` is closed when `ctx` is cancelled or the HTTP body EOFs. Goroutines that read from the upstream HTTP body must select on `ctx.Done()`. SSE parsers go in `internal/llm/sse/` (shared helper, ~100 LOC) — do not depend on a third-party SSE library; stdlib `bufio.Scanner` with a custom split function is enough.

The `Registry` is constructed once at app startup from `config.Config` and is read-mostly. `Get` is called per turn; `ListModels` is called by the model-picker dialog and the `bharatcode models` subcommand. Both must be safe for concurrent callers.

Curated model packs (context window, pricing in USD and INR, tool-call support, image support) live as Go structs in each provider subpackage, kept terse and machine-updatable by `bharatcode update-providers` (a future command, not part of this module). The `ledger` module reads pricing off `Model` to compute INR cost on each `EndEvent`.

The sovereign stubs return `ErrNotYetSupported` from `Stream` but advertise themselves in `Models()` with a `Pending: true` flag so the UI can show them as "coming soon" rather than hide them. When Sarvam, Krutrim, or BharatGPT ships a coding-tuned variant, only the stub file changes — the registry plumbing is already there.

## Implementation status

Built in this pass:

- Public thin interface in `internal/llm`: `Provider`, `Request`, `Tool`,
  `Model`, typed stream `Event` variants, `Usage`, and sentinel errors for
  provider failure classes.
- Thread-safe `Registry` that constructs providers from `config.Config`,
  returns providers by name, and lists configured models.
- OpenAI-compatible chat client used for OpenAI, DeepSeek, Moonshot, Groq,
  Together, Fireworks, OpenRouter, and LM Studio config entries.
- Ollama chat client for local `/api/chat` streaming JSON-line responses.
- SSE helper for OpenAI-compatible streaming and JSON-line handling for local
  responses.
- Message conversion for text, assistant tool calls, and tool-result history.
- Streaming normalization for text deltas, reasoning deltas, tool-call
  start/delta/end events, usage, and typed HTTP errors.
- `httptest.NewServer` coverage for registry behavior, OpenAI-compatible
  streaming, tool-result history conversion, typed provider errors,
  unsupported feature checks, missing API keys, Ollama streaming, unsupported
  providers, and cancellation.

Deliberate first-pass deviations:

- Concrete providers live in the root `internal/llm` package rather than the
  subpackage layout listed above. This keeps the initial registry and shared
  conversion logic small; subpackages can be split out once Anthropic and
  provider-specific headers are implemented.
- Anthropic is registered as an `ErrNotYetSupported` provider stub. Sovereign
  provider stubs are not wired yet because the current `config.ProviderType`
  enum has no sovereign provider type.
- Retry plumbing uses `retryablehttp` for transport errors, but status-code
  retries are kept conservative so typed HTTP classification remains reliable
  in the first implementation. Provider-specific retry policy tuning remains
  future work.
- Image conversion is capability-checked and rejected as
  `ErrUnsupportedFeature`; multimodal wire formatting is not implemented yet.
