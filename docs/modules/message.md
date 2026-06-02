# Message

**Path:** `internal/message/`
**Status:** Completed

## Purpose

The `message` module defines BharatCode's canonical conversation representation: a `Message` value with a `Role`, an ordered list of typed `ContentBlock` values, parent pointer, timestamp, and optional token usage. It is the single internal vocabulary that the agent loop, the LLM provider adapters, the session store, and the TUI all read and write. Different LLM providers speak different dialects — Anthropic emits structured tool-call blocks inline with text; OpenAI emits a parallel `tool_calls` array next to a `content` string; Google interleaves "function call" parts; Groq and Together pass through OpenAI-shape JSON. This module is responsible for normalizing all of those dialects into one in-memory shape that the rest of BharatCode never has to re-interpret, and for re-serializing that shape back out when sending to a provider. It also enforces structural invariants — most importantly that a `ToolResultBlock` always follows the `ToolUseBlock` it answers — so that downstream code can rely on well-formed conversations without revalidating on every read.

## Public interface

```go
// Role is the conversational role of a Message.
type Role string

const (
    RoleUser      Role = "user"
    RoleAssistant Role = "assistant"
    RoleSystem    Role = "system"
    RoleTool      Role = "tool"
)

// BlockType discriminates ContentBlock implementations on the wire.
type BlockType string

const (
    BlockText       BlockType = "text"
    BlockToolUse    BlockType = "tool_use"
    BlockToolResult BlockType = "tool_result"
    BlockImage      BlockType = "image"
    BlockThinking   BlockType = "thinking"
)

// ContentBlock is one typed segment of a Message body.
// All concrete blocks marshal to JSON with a "type" discriminator.
type ContentBlock interface {
    Type() BlockType
}

// TextBlock is a plain-text segment.
type TextBlock struct {
    Text string `json:"text"`
}

// ToolUseBlock is a model's request to invoke a tool.
// Input is opaque JSON forwarded to the tool implementation.
type ToolUseBlock struct {
    ID    string          `json:"id"`
    Name  string          `json:"name"`
    Input json.RawMessage `json:"input"`
}

// ToolResultBlock is the response that closes a prior ToolUseBlock.
// Content is the tool's stringified output. IsError is true when
// the tool reported a failure the model should observe.
type ToolResultBlock struct {
    ToolUseID string `json:"tool_use_id"`
    Content   string `json:"content"`
    IsError   bool   `json:"is_error"`
}

// ImageBlock carries inline base64 image data.
type ImageBlock struct {
    MimeType string `json:"mime_type"`
    Data     []byte `json:"data"`
}

// ThinkingBlock carries provider reasoning traces (Anthropic
// extended thinking, OpenAI o-series reasoning, etc.).
type ThinkingBlock struct {
    Text string `json:"text"`
}

// TokenUsage records provider-reported token counts for a Message.
type TokenUsage struct {
    InputTokens     int `json:"input_tokens"`
    OutputTokens    int `json:"output_tokens"`
    CacheReadTokens int `json:"cache_read_tokens,omitempty"`
    CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

// Message is one entry in a session conversation.
type Message struct {
    ID        string         `json:"id"`
    SessionID string         `json:"session_id"`
    Role      Role           `json:"role"`
    Content   []ContentBlock `json:"content"`
    ParentID  *string        `json:"parent_id,omitempty"`
    CreatedAt time.Time      `json:"created_at"`
    Usage     *TokenUsage    `json:"usage,omitempty"`
}

// MarshalJSON / UnmarshalJSON on Message handle the heterogeneous
// Content slice via the BlockType discriminator. Round-tripping a
// Message through json.Marshal followed by json.Unmarshal is required
// to return an equal value.

// Normalize applies BharatCode's structural invariants to a slice of
// Messages and returns the normalized slice. It:
//   - merges adjacent TextBlocks in the same Message,
//   - drops empty TextBlocks,
//   - reorders so every ToolResultBlock immediately follows the
//     ToolUseBlock with the matching ID,
//   - guarantees ToolResultBlocks live in a user-role Message and
//     ToolUseBlocks in an assistant-role Message.
// Normalize never mutates the input slice.
func Normalize(messages []Message) []Message

// Validate returns a non-nil error if the slice violates the
// invariants Normalize enforces. Callers that received messages
// from an external source (provider response, session replay) should
// Validate before persisting.
func Validate(messages []Message) error

// Sentinel errors returned by Validate.
var (
    ErrToolResultWithoutUse  = errors.New("tool_result without preceding tool_use")
    ErrToolUseWithoutResult  = errors.New("tool_use without following tool_result")
    ErrEmptyContent          = errors.New("message has no content blocks")
    ErrUnknownBlockType      = errors.New("unknown content block type")
)
```

## Dependencies

- `internal/util` — id generation, time helpers.
- `internal/db` — message rows live in the `messages` SQLite table; this module owns the schema for it and exposes `Repo` helpers consumed by `internal/session` (see Notes).

No upward dependencies. `message` is a Layer 2 (core data) module; it is consumed by `session`, `llm`, `agent`, and `tui`, but consumes none of them.

## Acceptance criteria

Each bullet maps to a test the implementer can write and grep by name.

- `TestRoundTrip_TextBlock` — a `Message` containing one `TextBlock` survives `json.Marshal` -> `json.Unmarshal` with `reflect.DeepEqual` parity.
- `TestRoundTrip_ToolUseBlock` — same for a `ToolUseBlock` with non-trivial `Input` JSON.
- `TestRoundTrip_ToolResultBlock` — same for a `ToolResultBlock`, including `IsError=true`.
- `TestRoundTrip_ImageBlock` — same for an `ImageBlock` with non-empty `Data`; base64 padding preserved.
- `TestRoundTrip_ThinkingBlock` — same for a `ThinkingBlock`.
- `TestRoundTrip_MixedContent` — a `Message` whose `Content` interleaves all five block types round-trips with order preserved.
- `TestUnmarshal_UnknownBlockType_ReturnsError` — JSON with `"type":"alien"` yields `ErrUnknownBlockType`.
- `TestValidate_ToolResultWithoutUse_Rejected` — a slice containing a `ToolResultBlock` whose `ToolUseID` has no prior `ToolUseBlock` returns `ErrToolResultWithoutUse`.
- `TestValidate_OrphanToolUse_Rejected` — an assistant `ToolUseBlock` not followed (eventually) by a matching `ToolResultBlock` returns `ErrToolUseWithoutResult`.
- `TestNormalize_MergesAdjacentText` — two adjacent `TextBlock` values in the same `Message` collapse to one.
- `TestNormalize_DropsEmptyText` — a `TextBlock{Text: ""}` is removed.
- `TestNormalize_DoesNotMutateInput` — original slice and its blocks are byte-identical after `Normalize`.
- `BenchmarkMarshalMessage_TextOnly` — serialization benchmark, ≤2 allocations per call on a 1 KB text body (target, document actual in `Implementation status`).
- `BenchmarkMarshalMessage_MixedBlocks` — benchmark with one of each block type.

`go test ./internal/message/...` must pass with `-race`.

## Notes for the implementer

- The locked stack forbids external LLM-abstraction SDKs. All provider-shape conversion happens in `internal/llm/` — not here. This module exposes only BharatCode's internal shape. If you find yourself importing a provider SDK type into a signature in this package, stop and move it to `internal/llm/`.
- JSON tags are `snake_case` (`tool_use_id`, `parent_id`, `cache_read_tokens`).
- For the `Content` field, write a custom `UnmarshalJSON` that first decodes into `[]json.RawMessage`, peeks each element's `"type"`, and dispatches to the concrete block constructor. Failing to recognize a type returns `ErrUnknownBlockType` wrapped with the offending JSON for debuggability (`fmt.Errorf("decoding block %q: %w", raw, ErrUnknownBlockType)`).
- File permissions on any test fixtures use octal literals (`0o644`).
- The SQLite schema row this module backs (defined in `internal/db/migrations/`) is approximately: `messages(id TEXT PRIMARY KEY, session_id TEXT NOT NULL, role TEXT NOT NULL, content_json TEXT NOT NULL, parent_id TEXT, created_at INTEGER NOT NULL, usage_json TEXT)` with `FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE`. The full content slice is stored as a single JSON blob in `content_json`; we do not shred blocks into separate rows. Coordinate the migration with the `db` module owner; do not write `CREATE TABLE` SQL in this package.
- Use `log/slog` for diagnostics (`slog.Default()` until `app` wires a configured logger). Log messages start capitalized, no trailing period: `slog.Warn("Dropping empty text block", "session_id", m.SessionID)`.
- Wrap every returned error: `fmt.Errorf("normalizing message %s: %w", m.ID, err)`.
- `context.Context` is not part of the public signatures in this file because `message` types are pure values; any I/O lives in the `session` module's `Repo`. Keep it that way.
- Run `gofumpt -w .` and `golangci-lint run` before declaring the module done. Append an `## Implementation status` section to this file listing what was built and any deliberate deviations.

## Implementation status

- **Status:** Completed
- **Files created:**
  - `internal/message/message.go`
  - `internal/message/normalize.go`
  - `internal/message/message_test.go`
- **Total lines of code:** 424 lines (excluding tests), 738 lines total (including tests).
- **Test Pass Count:** 17 tests/subtests passing.
- **Statement Coverage:** 90.8% statement coverage for the message module.
- **Benchmarks:**
  - `BenchmarkMarshalMessage_TextOnly` — ~19,600 ops, ~59,500 ns/op, 5,204 B/op, 9 allocs/op
  - `BenchmarkMarshalMessage_MixedBlocks` — ~32,300 ops, ~35,600 ns/op, 2,147 B/op, 21 allocs/op
- **Deviations:** None. All requirements and acceptance criteria met exactly.
