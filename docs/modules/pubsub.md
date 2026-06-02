# pubsub

**Path:** `internal/pubsub/`
**Status:** Completed

## Purpose

The `pubsub` package is BharatCode's in-process event bus. Producers (the agent loop, LSP manager, shell runner, hook engine, permission gate) emit events; consumers (the TUI, the file tracker, the cost ledger) subscribe and react. The bus is type-safe via Go generics — each topic carries a single payload type, validated at compile time — and per-topic, each topic owning its own subscriber set, mutex, and buffered channels.

It exists so that no module imports another module just to push an event at it. The agent loop does not import the TUI; it publishes a `MessageEvent` and the TUI subscribes. This keeps the dependency graph a tree, not a web, and makes it trivial to replace the TUI with a `--json` headless writer in Phase 2.

Delivery is non-blocking. If a subscriber's buffer is full, the event is dropped for that subscriber, a `slog.Warn` is logged, and a per-topic drop counter is incremented. This lossy-by-design delivery is a well-established pattern for streaming event buses and is the right choice for streaming token deltas where only the latest state matters. The single exception is `PermissionRequests`, which is request/response: the payload carries a reply channel, so the producer blocks on the *reply*, not on the publish.

## Public interface

```go
// Package pubsub provides a generic, in-process, per-topic event bus.
// Topics are typed: each Topic[T] carries one payload type, validated
// at compile time. Publish is non-blocking and lossy; subscribers
// with full buffers drop events with a warn log.
package pubsub

import (
    "context"
    "sync/atomic"
)

// Topic is a typed, buffered, fan-out event channel. Each Topic owns
// its own subscriber set; a subscriber receives every event published
// after it subscribes and before it unsubscribes. The zero value is
// not usable; construct topics with NewTopic.
type Topic[T any] struct {
    // Unexported fields: name, bufferSize, subs map, mutex, closed
    // flag, drop counter.
}

// NewTopic constructs a Topic with the given name (used in logs) and
// per-subscriber buffer size. A bufferSize of zero is treated as
// DefaultBufferSize (1024). Panics if name is empty.
func NewTopic[T any](name string, bufferSize int) *Topic[T]

// Publish delivers event to every active subscriber. Delivery is
// non-blocking: if a subscriber's channel is full, the event is
// dropped for that subscriber, a slog.Warn is logged with the topic
// name, and the topic's drop counter is incremented. Publish is safe
// to call from multiple goroutines and never blocks longer than the
// time to acquire the topic's read lock. If the topic has been closed
// Publish is a no-op. The provided ctx is used only to skip delivery
// when ctx.Done has fired before any subscriber is reached.
func (t *Topic[T]) Publish(ctx context.Context, event T)

// Subscribe registers a new subscriber and returns a receive-only
// channel that delivers every subsequent Publish, plus a cancel
// function that unregisters the subscriber and closes the channel.
// The cancel function is idempotent and safe to call from any
// goroutine. The returned channel is buffered to the topic's
// configured size. After Close, Subscribe returns an already-closed
// channel and a no-op cancel.
func (t *Topic[T]) Subscribe() (events <-chan T, cancel func())

// Close marks the topic as closed, unregisters every subscriber, and
// closes every subscriber's channel. Subsequent Publish calls are
// no-ops and subsequent Subscribe calls return an already-closed
// channel. Close is idempotent.
func (t *Topic[T]) Close()

// Name returns the topic's name as passed to NewTopic.
func (t *Topic[T]) Name() string

// SubscriberCount returns the number of currently registered
// subscribers. Intended for tests and diagnostics.
func (t *Topic[T]) SubscriberCount() int

// DropCount returns the cumulative number of events dropped because
// a subscriber's channel was full. The counter is monotonically
// non-decreasing for the lifetime of the topic.
func (t *Topic[T]) DropCount() uint64

// DefaultBufferSize is the per-subscriber channel capacity used when
// NewTopic is called with bufferSize == 0. Sized to absorb a long
// streaming assistant turn (~one token-delta event per token) under
// a slow TUI render stall.
const DefaultBufferSize = 1024
```

### Standard topics

Each standard topic is declared as a package-level `var` so other packages can subscribe without constructing their own. All are typed; the payload struct lives in the topic's owning module and is referenced here only by its qualified name.

```go
package pubsub

// Topics declared here are the canonical bus endpoints used by the
// rest of BharatCode. New cross-module event flows should add a topic
// here rather than spinning up an ad-hoc *Topic instance in the
// producer.

var (
    // MessageEvents carries assistant/user/tool messages produced
    // by the agent loop. Subscribers: TUI chat view.
    // Payload type: message.Event (defined in internal/message).
    MessageEvents = NewTopic[MessagePayload]("messages", 0)

    // ToolCallEvents carries tool-call start/end records.
    // Subscribers: TUI tool-call panel, ledger cost accumulator.
    // Payload type: tools.CallEvent (defined in internal/tools).
    ToolCallEvents = NewTopic[ToolCallPayload]("tool_calls", 0)

    // LSPDiagnosticEvents carries diagnostics emitted by language
    // servers. Subscribers: TUI status bar, agent context builder.
    // Payload type: lsp.DiagnosticEvent.
    LSPDiagnosticEvents = NewTopic[LSPDiagnosticPayload]("lsp_diagnostics", 256)

    // ShellJobEvents carries lifecycle events for background shell
    // jobs (started, stdout chunk, stderr chunk, exited).
    // Subscribers: TUI background-jobs panel.
    // Payload type: shell.JobEvent.
    ShellJobEvents = NewTopic[ShellJobPayload]("shell_jobs", 0)

    // PermissionRequests is request/response, not fan-out. The agent
    // publishes a PermissionRequest carrying a Reply channel; the
    // TUI subscribes, prompts the user, and sends the decision back
    // on Reply. There is exactly one subscriber (the TUI in
    // interactive mode, or the --yolo auto-approver in headless
    // mode). The bus is reused so headless mode can swap subscribers
    // without changing producer code.
    // Payload type: PermissionRequest (defined below).
    PermissionRequests = NewTopic[PermissionRequest]("permissions", 16)

    // LedgerUpdateEvents carries every appended ledger entry plus a
    // running session/day/month total. Subscribers: TUI footer.
    // Payload type: ledger.UpdateEvent.
    LedgerUpdateEvents = NewTopic[LedgerUpdatePayload]("ledger", 64)
)

// PermissionRequest is the payload type for PermissionRequests. The
// agent fills Tool, Args, Reason and a fresh Reply channel, then
// publishes. The handler sends exactly one PermissionDecision on
// Reply and the agent blocks on <-Reply.
type PermissionRequest struct {
    Tool   string
    Args   map[string]any
    Reason string
    Reply  chan PermissionDecision // buffered with cap 1
}

// PermissionDecision is the reply value sent back on
// PermissionRequest.Reply.
type PermissionDecision struct {
    Approved bool
    Remember bool // remember decision for this session
    Reason   string
}
```

The payload type aliases (`MessagePayload`, `ToolCallPayload`, etc.) are defined as `any`-typed placeholder structs in `pubsub/payloads.go` for now. As each producer module is implemented its real type replaces the placeholder; the topic variable's type parameter is updated in lockstep. This keeps the bus compilable before downstream modules exist.

## Dependencies

- `internal/util` — `util.Truncate` formats payload summaries in `slog` drop-warnings without flooding the log.
- stdlib only otherwise: `context`, `sync`, `sync/atomic`, `log/slog`.
- External: none in production code. Tests use `go.uber.org/goleak` (test-only) — declared as a test dependency in `go.mod` and never imported from non-test files.

## Acceptance criteria

1. `go test ./internal/pubsub/...` passes on linux, darwin, and windows runners.
2. `go test -race -count=50 ./internal/pubsub/...` passes — race-free Subscribe/Publish/Cancel/Close interleavings.
3. `go test -cover ./internal/pubsub/...` reports ≥ 95% statement coverage.
4. A `TestMain` in `pubsub_test.go` calls `goleak.VerifyTestMain(m)` from `go.uber.org/goleak`; no goroutine leak detected across the test binary.
5. `golangci-lint run ./internal/pubsub/...` is clean.
6. A test `TestPublishFanOut` registers N=10 subscribers, publishes 100 events, and asserts every subscriber receives exactly 100 events in order.
7. A test `TestSubscribeAfterPublish` registers a subscriber after one Publish has fired, publishes once more, and asserts the late subscriber sees only the second event (no replay).
8. A test `TestCancelStopsDelivery` cancels a subscriber, publishes 10 more events, and asserts no further reads on the cancelled channel block or panic; the channel is observed closed via `_, ok := <-ch; require.False(t, ok)`.
9. A test `TestSlowSubscriberDrops` registers one subscriber with bufferSize=4, publishes 100 events without reading, asserts `DropCount() == 96`, and asserts no producer goroutine is blocked (verify via `time.AfterFunc` deadlock guard, not via reading the channel).
10. A test `TestCloseIdempotent` calls `Close()` twice and asserts the second call is a no-op.
11. A test `TestPublishAfterClose` calls `Close()`, then `Publish(...)`, and asserts no panic, no goroutine leak, and `DropCount()` unchanged.
12. A test `TestPermissionRequestReply` exercises the `PermissionRequests` topic: subscriber receives a `PermissionRequest`, sends a `PermissionDecision` on `Reply`, producer's `<-req.Reply` unblocks with the decision.
13. A benchmark `BenchmarkPublish_1Subscriber` reports zero allocations per op when the payload is a value type that fits in a struct (no boxing surprises from generics).

## Notes for the implementer

- Use generics. The type parameter `T any` on `Topic[T]` is mandatory; do not collapse to a `Topic[any]` "convenience" wrapper. Type safety is the whole point.
- The subscriber set is a `map[*chan T]struct{}` keyed by the channel pointer. The cancel closure captures that pointer and deletes the entry under the topic's `sync.Mutex`. Do NOT key the map on a `chan T` value — Go disallows it (channels are not comparable as map keys… they are, but pointer keys are clearer and avoid GC retention pitfalls; pick the pointer form and comment why).
- Publish acquires `sync.RWMutex.RLock()` so multiple goroutines publish in parallel. Subscribe/Cancel/Close take the write lock.
- The drop counter is `sync/atomic.Uint64` so `DropCount()` reads concurrently without locks.
- The drop-warning log line MUST include the topic name and a *truncated* payload summary (use `util.Truncate(fmt.Sprintf("%+v", event), 200)`). Verbose payloads in hot drop paths blow up the log.
- For `PermissionRequests`, the producer side constructs `req := PermissionRequest{Reply: make(chan PermissionDecision, 1)}` (buffer 1 so the subscriber's send never blocks if the producer has moved on). Document this contract in the `PermissionRequest` doc comment.
- Tests live in `pubsub_test.go` and use `testify/require`. The goleak invocation is the standard pattern:
  ```go
  func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
  ```
- Standard-topic `var` declarations sit in `pubsub/topics.go` and use placeholder payload types in `pubsub/payloads.go`. Each producer module replaces its placeholder type as it lands; until then the placeholders compile and downstream subscribers can stub against them.
- `slog` is configured by `internal/cmd` at process start; do not call `slog.SetDefault` from this package.
- Errors wrap with `fmt.Errorf("...: %w", err)`; though this package returns no errors, any future addition must follow the convention (per AGENTS.md §4).

## Implementation status

- **Status:** Completed
- **Files created:**
  - `internal/pubsub/payloads.go`
  - `internal/pubsub/topics.go`
  - `internal/pubsub/pubsub.go`
  - `internal/pubsub/pubsub_test.go`
- **Total lines of code:** ~460 lines (including tests)
- **Test Pass Count:** 11 passing tests/subtests, plus 1 benchmark
- **Statement Coverage:** 100.0% statement coverage
- **Deviations:** None.
