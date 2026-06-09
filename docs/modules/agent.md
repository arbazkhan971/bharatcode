# agent

**Path:** `internal/agent/`
**Status:** Completed

## Purpose

The `agent` package is BharatCode's agent loop. It takes a user message, sends it (with conversation history and a system prompt) to the configured LLM provider, processes the model's tool calls by dispatching them through `internal/tools`, feeds the tool results back to the model, and repeats until the model returns a turn with no tool calls. Every other module exists to serve this loop; the loop is the product.

Three concerns are owned exclusively here. First, the loop itself: streaming, tool-call accumulation, mid-stream cancellation, retries on transient provider errors, and the stopping conditions (no tool calls, loop-detection trip, context cancellation, hard-coded step cap). Second, prompt assembly: the system prompt is built from a Go-template file plus environment data (working directory, OS, git status, agent name, list of registered tools and their schemas). Third, coordination: a `Coordinator` owns one or more named `Loop` instances (`coder`, `task`, plus any user-defined agents from `config.Agents`) so the user can invoke a read-only subagent for searches while the primary agent waits on the result.

Two cross-cutting features live here because they wrap every tool call: hook firing (via the `hooked_tool` decorator) and loop detection (a hash of `tool_name + canonicalized_args` repeated three times in a row triggers a break). Token budgeting also lives here: when the message history would exceed the model's context window, the loop drops oldest non-system messages until it fits. Smart compaction (summarize-and-replace) is a Phase 2 enhancement; v1 ships drop-oldest only.

## Public interface

```go
// Package agent implements BharatCode's agent loop, prompt assembly,
// and the named-agent Coordinator.
package agent

// Loop runs a single agent for a single session at a time. A Loop
// is bound to one named agent configuration (a provider, a model, a
// system prompt, a tool allow-list); the Coordinator hands out a
// fresh Loop per session. Sequential reuse of a Loop across turns
// within the same session is supported and is the expected pattern.
// Loops are safe for concurrent Interrupt calls; Run itself is not
// safe to call concurrently on the same Loop instance.
type Loop struct {
	// Unexported fields: cfg, registry, perms, sessions, hooks,
	// bus, prompt, name, interrupt.
}

// Config bundles the dependencies a Loop needs. Every field is
// required; New panics on a missing pointer.
type Config struct {
	Name         string
	Provider     llm.Provider
	Tools        *tools.Registry
	Permission   *permission.Checker
	Sessions     *session.Repo
	FileTracker  *filetracker.Tracker
	Ledger       *ledger.Recorder
	Bus          *pubsub.Topic[Event]
	Hooks        *hooks.Engine
	SystemPrompt string
	// ToolAllowList limits the tools this agent may call. nil or
	// empty means every tool in the Registry is available. Names
	// are matched verbatim against Tool.Name().
	ToolAllowList []string
	// MaxSteps caps the number of tool-call round-trips per Run.
	// Zero means the default, 50.
	MaxSteps int
}

// New constructs a Loop from cfg. It validates that every required
// dependency is non-nil and that ToolAllowList references only
// registered tools; missing tools cause New to panic.
func New(cfg Config) *Loop

// Run drives a single user turn. It loads the session's history
// from cfg.Sessions, appends userMsg, calls the LLM, dispatches
// tool calls until the model returns a turn with no tool calls or
// a stopping condition fires, and persists the resulting messages
// back to cfg.Sessions.
//
// Run returns nil on a clean turn completion (including a
// loop-detection trip, which is reported via a synthetic assistant
// message and is not an error). Run returns ctx.Err() wrapped on
// cancellation. Run returns a non-nil error only on infrastructure
// failures: provider unreachable after retries, session repo
// unwritable, internal invariants broken. User-visible problems
// (a tool failed, a model refused, the context window overflowed)
// are folded into the session as messages and Run still returns
// nil.
func (l *Loop) Run(ctx context.Context, sessionID string, userMsg message.Message) error

// Interrupt cancels an in-flight Run. It is idempotent and safe to
// call from a signal handler or from the TUI goroutine. After
// Interrupt the Loop is reusable for subsequent Run calls.
func (l *Loop) Interrupt()

// Name returns the agent's configured name (e.g., "coder", "task").
func (l *Loop) Name() string

// Coordinator manages the set of named agents available within a
// running BharatCode session. It is constructed once at app start.
type Coordinator struct {
	// Unexported fields: cfg, loops, mu.
}

// NewCoordinator builds a Coordinator from the application config.
// It instantiates a Loop for every entry in cfg.Agents plus the
// built-in "coder" and "task" agents. NewCoordinator does not
// validate provider connectivity; that happens on first use.
func NewCoordinator(cfg *config.Config, deps Dependencies) (*Coordinator, error)

// Dependencies bundles the shared collaborators the Coordinator
// hands to every Loop it creates. Storing them on the Coordinator
// avoids each Loop reaching into package-global state.
type Dependencies struct {
	Tools       *tools.Registry
	Permission  *permission.Checker
	Sessions    *session.Repo
	FileTracker *filetracker.Tracker
	Ledger      *ledger.Recorder
	Hooks       *hooks.Engine
	MCP         *mcp.Client
	Bus         *pubsub.Topic[Event]
	Providers   map[string]llm.Provider
}

// Agent returns a fresh Loop bound to the named agent's
// configuration. Each call returns a new Loop instance so concurrent
// sessions never share Run state; the underlying provider, tool
// registry, and rendered system prompt are reused. ErrUnknownAgent
// is returned when no agent of that name is configured.
func (c *Coordinator) Agent(name string) (*Loop, error)

// List returns the names of every agent the Coordinator knows
// about, in deterministic order (built-ins first, then config
// order).
func (c *Coordinator) List() []string

// Start performs eager initialization the Coordinator deferred at
// construction time (e.g., loading and rendering every agent's
// system-prompt template). It must be called before Agent.
func (c *Coordinator) Start(ctx context.Context) error

// Stop releases any resources the Coordinator or its Loops hold
// (currently no-op; reserved for future provider connection
// pooling). It is idempotent.
func (c *Coordinator) Stop(ctx context.Context) error

// Event is published on the agent bus for every significant
// transition: turn started, tool called, tool completed, loop
// detected, turn finished, run errored.
type Event struct {
	SessionID string
	AgentName string
	Kind      EventKind
	Message   *message.Message
	ToolName  string
	Err       error
}

// EventKind enumerates the agent-event variants.
type EventKind int

const (
	EventTurnStarted EventKind = iota
	EventLLMResponse
	EventToolCalled
	EventToolResult
	EventLoopDetected
	EventTurnFinished
	EventRunError
)

// ErrUnknownAgent is returned by Coordinator.Agent when no agent
// of the requested name is configured.
var ErrUnknownAgent = errors.New("unknown agent")

// ErrLoopDetected is the sentinel folded into the session as a
// synthetic message when the loop-detection heuristic trips.
var ErrLoopDetected = errors.New("loop detected: 3 identical tool calls in a row")
```

### Built-in agents

| Name | Tools | Notes |
|---|---|---|
| `coder` | full registry (read, write, execute) | The primary agent. Driven by the user's chat. |
| `task` | read-only subset (`view`, `grep`, `glob`, `ls`, `diagnostics`, `web_fetch`, `web_search`) | A subagent the `coder` can spawn for focused searches without polluting its own context. |
| user-defined | per `config.Agents[].tools` | Loaded from config; same dependency surface as built-ins. |

### Verification policy

BharatCode formalizes *when verification is required* and *when it may be
skipped* so the agent never reports unverified work as done. The policy is
encoded as data in `config.VerificationConfig` (root config field
`verification`) and rendered into the `coder` system prompt, so the config and
the prompt cannot drift.

Verification is REQUIRED before a turn may be reported done whenever the turn
produced a change in one of these classes (`config.VerificationTrigger`):

| Trigger | Fires when |
|---|---|
| `source_edit` | a write-class tool (write, edit, multiedit, patch, rename) changed a source file |
| `generated_artifact` | a generated frontend artifact (build output, bundled asset, compiled stylesheet) was produced or changed |
| `package_manifest` | a package manifest (go.mod, package.json, pyproject.toml, Cargo.toml, …) was touched |
| `test_or_build_file` | a test file or a build/CI file (Makefile, Dockerfile, `*_test.*`, workflow YAML) was touched |

Verification may be SKIPPED only for one of these sanctioned reasons
(`config.VerificationSkipReason`); any other excuse is not a sanctioned skip:

| Skip reason | Allowed when |
|---|---|
| `no_test_command` | the project exposes no test, build, or lint command to run |
| `dependency_unavailable` | an external dependency needed to verify is unavailable (toolchain not installed, service down, credentials absent) |
| `user_opted_out` | the user explicitly asked not to run tests, the build, or the linter |

The policy is ON by default: a zero `VerificationConfig` (the value an omitted
`verification` block produces) selects the strict default set of triggers and
the standard skip reasons. `VerificationConfig.Disabled` makes verification
advisory only. The struct is intentionally pure and side-effect-free so the
policy is testable in isolation:

```go
// VerificationConfig encodes when verification is required and which skip
// reasons are sanctioned. All methods are pure.
func (v VerificationConfig) RequiresVerification(t VerificationTrigger) bool
func (v VerificationConfig) SkipAllowed(r VerificationSkipReason) bool
func (v VerificationConfig) Triggers() []VerificationTrigger
func (v VerificationConfig) SkipReasons() []VerificationSkipReason
func (v VerificationConfig) Validate() error // rejects unknown triggers/reasons
```

Because the same vocabulary drives the prompt, the `coder` template requires the
final response on any turn that changed something to end with exactly one
verification status: **Verified** (commands run and result observed),
**Failed** (verification ran but did not pass), or **Skipped (`<reason>`)**
naming one of the sanctioned skip reasons.

### Subfiles

- `internal/agent/loop.go` — the `Loop` struct, `Run`, `Interrupt`, the inner step-by-step driver.
- `internal/agent/prompts.go` — system-prompt assembly: load template, gather env data (workdir, OS, git branch, registered-tool list with descriptions and schemas), render with `text/template`.
- `internal/agent/templates/coder.md.tpl` — the default system prompt for the `coder` agent.
- `internal/agent/templates/task.md.tpl` — the default system prompt for the `task` agent.
- `internal/agent/coordinator.go` — `Coordinator`, `NewCoordinator`, `Agent`, `Start`, `Stop`, `List`.
- `internal/agent/loop_detection.go` — repeated-tool-call hashing and the break-with-synthetic-message logic.
- `internal/agent/hooked_tool.go` — a decorator that wraps every `tools.Tool` with `PreToolUse`/`PostToolUse` hook firing.
- `internal/agent/budget.go` — message-history truncation and per-turn token accounting.

## Dependencies

- `internal/util` — `util.ExpandPath` and `util/fsext.Exists` for prompt-data gathering.
- `internal/config` — agent definitions, model selection, provider configuration.
- `internal/message` — message and content types are the loop's input and output.
- `internal/session` — `session.Repo` is the persistence boundary for conversation history.
- `internal/llm` — `llm.Provider` is the LLM abstraction the loop drives.
- `internal/tools` — `tools.Registry` supplies the agent's callable functions.
- `internal/permission` — bridged into tool calls via the `hooked_tool` decorator.
- `internal/hooks` — `hooks.Engine` is invoked around every tool call.
- `internal/pubsub` — events flow on a `pubsub.Topic[Event]` to the TUI.
- `internal/filetracker` — referenced inside the system prompt to summarize files edited in the session.
- `internal/ledger` — every LLM call's usage and cost is recorded via `ledger.Recorder.Record`.
- `internal/mcp` — `mcp.Client.Tools()` is folded into the agent's effective tool list at `Coordinator.Start` time.

## Acceptance criteria

1. `go test ./internal/agent/...` passes on linux, darwin, and windows runners.
2. `go test -race ./internal/agent/...` passes with no data-race reports.
3. `go vet ./internal/agent/...` and `golangci-lint run ./internal/agent/...` are clean.
4. **End-to-end mock-provider test.** A test wires a stub `llm.Provider` that scripts three turns: turn 1 calls `view` on `testdata/hello.txt`; turn 2 calls `edit` on the same file; turn 3 returns no tool calls. `Loop.Run` drives the loop, the stub `tools.Registry` records both tool calls in order, and the resulting `session.Repo` contains six messages (user, assistant+tool, tool result, assistant+tool, tool result, assistant final).
5. **Interrupt mid-stream.** A test starts `Loop.Run` against a stub provider that streams indefinitely, calls `Loop.Interrupt()` from another goroutine after 50ms, and asserts that `Run` returns `context.Canceled` (or a wrap of it) within 200ms and that no further tool calls fire.
6. **Loop detection trips on three identical calls.** A test scripts a stub provider that always responds with `bash {command: "echo x"}`. After the third identical call the loop publishes `Event{Kind: EventLoopDetected}`, appends a synthetic assistant message containing `ErrLoopDetected.Error()` to the session, and returns nil. The fourth identical call never reaches the registry.
7. **Loop detection does not trip on near-identical calls.** A test with three `bash` calls whose `command` differs only by a trailing newline asserts the loop does **not** trip; canonicalization strips trailing whitespace from string args before hashing.
8. **Ledger entries.** A test with a stub `ledger.Recorder` asserts one `Record` call per LLM turn, with `InputTokens`, `OutputTokens`, `Model`, and `Provider` populated.
9. **Token budgeting: drop-oldest preserves system prompt and most recent user message.** Given a session history of 200 messages totaling ~200K tokens and a model with an 8K context window (configured via the stub provider's `Info().ContextWindow`), `Loop.Run` truncates the history before sending. Assert: the system prompt is present; the most recent user message is present; messages dropped are removed from the in-memory request only, not from `session.Repo` on disk.
10. **Step cap.** A stub provider that always returns one tool call hits `MaxSteps` (default 50). The loop appends a synthetic message ("step limit reached") and returns nil; the registry recorded exactly 50 tool calls.
11. **Hook firing.** A test with a recording `hooks.Engine` asserts `PreToolUse` fires before each tool's `Run` and `PostToolUse` fires after, with the tool name and arguments present in the hook payload. A `PreToolUse` returning a "block" decision causes the tool to short-circuit to `Result{IsError: true}` and the agent sees the block reason.
12. **Coordinator built-ins.** `NewCoordinator` with a minimal config produces `coder` and `task` agents. `Coordinator.List()` returns `["coder", "task"]` in that order with no extra entries.
13. **Coordinator user-defined agents.** A config with `agents: [{name: "reviewer", tools: ["view", "grep"]}]` produces a third agent whose Loop refuses to call any tool outside the allow list. A test attempting a `bash` call inside `reviewer` asserts the call short-circuits to `Result{IsError: true, Content: "tool not allowed for agent: reviewer"}`.
14. **MCP folding.** When `Dependencies.MCP` reports two bridged tools (`filesystem__read_file`, `filesystem__write_file`), `Coordinator.Start` adds them to the agent's effective registry. A test confirms `coder` can call `filesystem__read_file` and that the bridged tool's `Run` is invoked.
15. **System prompt assembly.** A test renders the `coder` template and asserts the output contains: the working directory, the OS name, the current git branch (or "(not a git repo)"), and at least one tool description from the registered tools. Rendering is deterministic given a fixed environment.
16. **Provider retry on transient error.** A stub provider returning 503 on the first two attempts and 200 on the third succeeds without surfacing an error to the caller. A test asserts exactly three provider calls and that the mock clock advanced by ~500ms before attempt 2 and ~1s before attempt 3 (the first two entries of the 500ms, 1s, 2s, 4s, 8s backoff sequence). Jitter is disabled in tests via a `noJitter` flag on the retry helper.
17. **Provider hard failure.** A stub provider returning 500 five times causes `Run` to return a wrapped error matching `errors.Is(err, llm.ErrProvider)`. The session repo records a synthetic assistant message describing the failure.
18. **Concurrent Interrupt safety.** A test calling `Interrupt` 100 times from 100 goroutines while `Run` is not in flight does not panic, deadlock, or leave the Loop in an unusable state.
19. **No tool panic crashes the agent.** A stub tool whose `Run` panics is caught by the `hooked_tool` decorator (or by the loop itself); the panic becomes `Result{IsError: true}` plus an `EventRunError`, and the loop continues to the next step.
20. **Determinism of agent listing.** Running `NewCoordinator` 100 times with the same config produces the same `List()` output every time; ordering is built-ins first (`coder`, `task`), then config order.

## Notes for the implementer

- **Loop detection canonicalization.** Hash inputs are: `tool_name + "\x00" + canonicalJSON(args)`. Canonicalization means: re-serialize the JSON with sorted keys, strip trailing whitespace from string values, lowercase booleans. Three consecutive identical hashes trip the break. Reset the counter on the first non-matching call. Implement with a small ring buffer of two prior hashes plus the candidate.
- **Token budgeting v1 is drop-oldest.** Walk the message history newest-to-oldest, summing token estimates from `llm.Provider.EstimateTokens` (or a fallback `len(content)/4`). When the running sum exceeds `Info().ContextWindow - reservedForResponse` (reserve 4 KiB), stop. Drop everything older. Always keep the system message and the latest user message regardless of budget; if those two alone exceed the window, log an `slog.Error` and return a wrapped `ErrContextOverflow` (define this sentinel in `llm`, not here).
- **Loop step cap.** Default 50, configurable via `Config.MaxSteps`. A "step" is one LLM call plus the tool calls it triggers; not one tool call. Tools called in parallel within a single LLM response count as one step.
- **Streaming.** Provider responses are streamed (`llm.Provider.Stream`). Accumulate text and tool-call deltas into a buffer; emit `EventLLMResponse` events on a debounced cadence (every 50ms or every 256 bytes) so the TUI can render incremental output without overload.
- **hooked_tool decorator.** Lives in `hooked_tool.go`. Wraps a `tools.Tool` to fire `PreToolUse` before `Run` and `PostToolUse` after. A `PreToolUse` hook returning `Block` short-circuits the underlying `Run` and the wrapper returns `Result{IsError: true, Content: "blocked by hook: " + reason}`. The decorator also applies per-agent allow-list filtering — if the agent disallows the tool, the wrapper short-circuits before `PreToolUse` fires.
- **Coordinator.Start vs NewCoordinator.** Keep `NewCoordinator` cheap (no I/O, no template parsing). Move all expensive work (parse templates, render once per agent, validate provider configs, fold in MCP tools) into `Start`. This lets the TUI render its first frame fast and surface coordinator errors as a TUI dialog rather than a startup crash.
- **System-prompt templates.** Go's `text/template` with custom funcs `humanBytes`, `shortPath`, and `now`. Templates live in `internal/agent/templates/` and are loaded via `embed.FS`. **Write BharatCode's templates from scratch** so the prompt prose is original to the project. Each template renders with a `PromptData` struct holding: `Workdir`, `OS`, `Arch`, `GitBranch`, `AgentName`, `Tools []ToolInfo`, `FileTrackerSummary string`.
- **Interrupt mechanics.** `Interrupt` cancels an internal `context.Context` held on the Loop and replaced at each `Run` call. Use `context.WithCancel(ctx)` inside `Run`, store the cancel func on the Loop guarded by a mutex, and call it from `Interrupt`. After `Run` returns, clear the stored cancel.
- **Retries.** Provider transient errors (5xx, 429, network timeouts) retry up to 5 attempts with exponential backoff (500ms, 1s, 2s, 4s, 8s) plus ±20% jitter. Use a passed-in `clock` interface (test-injectable) so tests don't sleep.
- **Hooks fired by this module:**
  - `SessionStart` — at the top of `Run` for the first turn of a session.
  - `UserPromptSubmit` — after appending the user message, before the first LLM call.
  - `PreToolUse` — fired by `hooked_tool` decorator before each tool's `Run`.
  - `PostToolUse` — fired by `hooked_tool` decorator after each tool's `Run`.
  - `Stop` — when the loop exits cleanly (no tool calls).
  - `SubagentStop` — when a `task`-agent or other subagent completes a child `Run`.
- **Loop in-flight invariant.** A `Loop` instance serves at most one `Run` at a time. `Run` acquires a `runMu sync.Mutex` for its entire duration; a second concurrent `Run` call on the same Loop panics with a clear message (this is a programming error, not a runtime condition — see "Coordinator concurrency" below for why callers never hit this in practice). The `Interrupt` path does not need the mutex.
- **Ledger accounting.** Each LLM call records: `InputTokens`, `OutputTokens`, `CachedInputTokens` (if reported), `Model`, `Provider`, `SessionID`, `AgentName`, `DurationMS`, `Cost.USDMicros`. The `ledger` module converts to INR using its own configured rate; this module only supplies USD via the provider's pricing table.
- **Coordinator concurrency.** Multiple sessions may use the same Coordinator concurrently. Each call to `Coordinator.Agent(name)` returns a **fresh Loop instance** bound to the named agent's config but with its own `runMu` and `Interrupt` state. The Coordinator caches the rendered system prompt and the resolved tool allow-list per agent name (cheap, immutable) so per-session Loop construction stays sub-millisecond. This makes the "Run-concurrent-on-same-Loop panics" invariant strictly an invariant for callers within a single session; cross-session concurrency is handled by separate Loop instances and is fully supported.
- **Future enhancement docked but not built.** Smart compaction (summarize-and-replace older messages) is Phase 2. Reserve the hook point: `budget.go` exposes a `Compactor interface { Compact(ctx, messages) ([]Message, error) }` that drop-oldest implements trivially. Phase 2 swaps in a real compactor.
- **No package-global state.** Every dependency is on `Config` or `Dependencies`. The only package-level variables are sentinel errors (`ErrUnknownAgent`, `ErrLoopDetected`) and the `embed.FS` for templates.

## Implementation status

First pass implemented in `internal/agent/`:

- Agent event types and sentinels.
- `Loop.Run`, `Interrupt`, assistant/tool-message persistence, streamed provider event accumulation, tool dispatch through the live tools API, stop-on-final-response, loop detection, step cap, and context cancellation.
- Hooked tool wrapper for allow-list checks plus `PreToolUse` and `PostToolUse`.
- Drop-oldest in-memory request budgeting using provider model context windows and a conservative JSON-size token estimate.
- System prompt rendering from embedded templates with working directory, platform, git branch, and tool descriptions.
- `Coordinator` with deterministic built-ins, user-defined agents, provider/model resolution, and MCP tool snapshots folded into an effective tool source.
- Tests for the scripted provider/tool loop, interrupt cancellation, loop detection, coordinator built-in order, and prompt rendering.

Known deviations and follow-ups:

- The current ledger API is `ledger.Ledger`, not the recorder interface described above, so the loop records through `Ledger.Record` when usage is present.
- The live provider interface does not expose token estimation or retry classification yet; v1 uses a fallback estimate and leaves provider retry/backoff for a follow-up once that API exists.
- Hook lifecycle events are limited to the hook constants currently present in `internal/hooks`.
- MCP folding is implemented through an immutable combined tool source rather than mutating `tools.Registry`.
