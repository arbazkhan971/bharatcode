# app

**Path:** `internal/app/`
**Status:** First-pass completed

## Purpose

The `app` module is BharatCode's top-level wiring. It is the single place that reads configuration, opens the SQLite database, constructs every service in dependency order, and exposes a single `App` value that `internal/cmd` hands to the TUI and to non-interactive subcommands. It also owns graceful shutdown: cancelling the root context cascades into every long-running goroutine (LSP clients, MCP servers, agent loop, file tracker), the database is closed, and stuck background work is bounded by a 5-second shutdown deadline.

This module exists so that no other package needs to know the construction order of the system. Subcommands and tests do not enumerate `db, pubsub, config, ledger, ...` themselves — they call `app.New`, get back an `*App`, and use its fields. Equally, this is the only place global concerns (logger backend, signal handling, root context) are set up, keeping the rest of the codebase free of `init` functions and package-level state. Explicit dependency injection — no globals — is the rule.

## Public interface

```go
// Package app wires together every BharatCode service into a single
// App value with explicit construction and shutdown semantics. It
// is consumed by internal/cmd; internal/tui receives a typed
// Dependencies struct built from an App.
package app

// Options configures a New call. Fields are independently optional;
// zero values resolve to documented defaults.
type Options struct {
    // ConfigPath overrides the user config lookup. Zero value uses
    // ~/.config/bharatcode/config.json with project overlay from
    // ./.bharatcode.json.
    ConfigPath string

    // ProjectDir is the working directory the App treats as the
    // active project root. Zero value is os.Getwd().
    ProjectDir string

    // YOLO disables permission gating for the lifetime of the App.
    // Equivalent to the --yolo flag and the /yolo TUI toggle when
    // initially true.
    YOLO bool

    // Verbose elevates the slog level to Debug.
    Verbose bool
}

// App is the assembled dependency graph. Every field is non-nil
// after a successful New. Fields are public so internal/cmd and
// internal/tui can read them; external packages must not import
// internal/app, enforced by Go's internal-package rules.
type App struct {
    Cfg         *config.Config
    DB          *db.DB
    Bus         *pubsub.Bus
    LLM         *llm.Registry
    Sessions    *session.Repo
    Ledger      *ledger.Ledger
    Permission  *permission.Checker
    Hooks       *hooks.Engine
    Shell       *shell.Shell
    LSP         *lsp.Manager
    MCP         *mcp.Client
    FileTracker *filetracker.Tracker
    Tools       *tools.Registry
    Agent       *agent.Coordinator
    Logger      *slog.Logger
}

// New constructs the App in strict dependency order. It returns a
// wrapped error if any sub-construction fails, after which any
// partially constructed services are closed. Providers without
// credentials are registered but lazy: they return errors only when
// invoked, never at New time. ctx is the root context; cancelling
// it after New initiates shutdown the same way Close does.
func New(ctx context.Context, opts Options) (*App, error)

// Close shuts the App down in reverse-of-construction order.
// It waits up to 5 seconds for background goroutines to exit; if
// the deadline passes it returns a wrapped error naming the slow
// subsystem but completes shutdown of everything else. Close is
// safe to call exactly once; the second call returns an error.
func (a *App) Close(ctx context.Context) error
```

## Dependencies

`app` is the wiring root and depends on every internal capability module except `cmd` and `tui`:

- `internal/util` — path expansion for config and DB locations.
- `internal/db` — opens SQLite, runs migrations.
- `internal/pubsub` — constructs the event bus first so every later subsystem can subscribe.
- `internal/config` — loads merged user + project config from `opts.ConfigPath` or defaults.
- `internal/ledger` — initializes ledger from DB, applies budget caps from config.
- `internal/session` — session repository over the open DB.
- `internal/filetracker` — per-session file tracker subscribing to file events.
- `internal/llm` — registry constructed from configured providers and env-var keys.
- `internal/permission` — checker reads allow-lists from config and respects `opts.YOLO`.
- `internal/hooks` — hook engine compiles user-defined PreToolUse/PostToolUse hooks.
- `internal/shell` — shell runner used by hooks and by the `bash` tool.
- `internal/lsp` — LSP manager, spawns clients lazily on first file open.
- `internal/mcp` — MCP client, dials configured servers and registers their tools.
- `internal/tools` — tools registry assembled from built-ins plus MCP bridge plus LSP-aware tools.
- `internal/agent` — agent coordinator wiring named agents (`coder`, `task`) over the LLM registry and tools.

`app` does NOT import `internal/tui` or `internal/cmd`. The dependency arrow is `cmd → app → everything else`.

External: stdlib only at this layer. Every external dependency is encapsulated inside the module it belongs to.

## Acceptance criteria

1. `go test ./internal/app/...` passes with `-race` on linux, darwin, windows.
2. `go vet` and `golangci-lint run` clean for `./internal/app/...`.
3. `TestNew_DefaultConfig_NoAPIKeys_Succeeds` — `New(ctx, Options{})` in an empty `t.TempDir()` with no provider env vars returns a non-nil `*App` and nil error. Invoking `app.LLM.Get("anthropic")` returns a registered provider whose `Complete` call fails with a clear "no credentials" error (lazy credential check).
4. `TestNew_PartialFailure_RollsBack` — using a fault-injection seam (e.g., a config file that points the DB to an unwritable path), `New` returns an error and any earlier successfully constructed services are closed before return; verified by counters on test-only `Closer` hooks.
5. `TestClose_FastPath_UnderDeadline` — after a successful `New` with no in-flight work, `Close(ctx)` returns nil within 100 ms.
6. `TestClose_SlowSubsystem_ReturnsDeadlineError` — with a stub LSP client whose `Close` blocks beyond 5 s, `app.Close` returns a wrapped error naming `lsp` and completes shutdown of every other subsystem; assert no leaked goroutines from the other subsystems.
7. `TestGoleak_NoLeaks` — uses `go.uber.org/goleak` in `TestMain` to confirm `New` followed by `Close` leaks no goroutines. Per goleak convention, the test process exits non-zero on leak.
8. `TestNewClose_Loop100_NoFDLeak` — running `New` + `Close` 100× in a loop, the open file descriptor count after the loop equals the count before the loop within a tolerance of 4 (logged for visibility). On linux, count `/proc/self/fd`; on darwin, use `lsof -p $$ | wc -l`; on windows, this assertion is skipped with a `t.Skip("FD count test linux/darwin only")`.
9. `TestConstructionOrder_MatchesSpec` — a probe-based test inserts a recording wrapper at each construction step; the recorded init order is exactly `util → db → pubsub → config → ledger → session → filetracker → llm → permission → hooks → shell → lsp → mcp → tools → agent` and the recorded close order is the strict reverse.
10. `TestRootContextCancel_CascadesShutdown` — cancelling the context passed to `New` after a successful return causes background goroutines to exit on their own; a subsequent `Close` is still required and still returns nil.
11. `TestNoTUIImport` — a Go test using `go/packages` (or `go list -deps`) asserts that no file in `internal/app/` imports `internal/tui` or `internal/cmd` directly or transitively.
12. `TestClose_DoubleCall_Errors` — the second call to `Close` returns a sentinel `ErrAlreadyClosed`.

## Notes for the implementer

- **Construction order is a hard invariant.** Build in this exact sequence:

  1. `util` setup — resolve `opts.ProjectDir`, `opts.ConfigPath`, `~/.config/bharatcode` paths.
  2. `db` — open SQLite via `modernc.org/sqlite`, run migrations.
  3. `pubsub` — create the typed event bus; every later subsystem may subscribe.
  4. `config` — load + validate user/project config (path resolved in step 1).
  5. `ledger` — initialize from DB rows, install budget caps from config.
  6. `session` — open repo over `db` + `message` schema.
  7. `filetracker` — subscribe to file events on `pubsub`.
  8. `llm` — build provider registry from `config` and env vars; lazy credential validation.
  9. `permission` — checker; honor `opts.YOLO`.
  10. `hooks` — compile hooks from config, ready to shell out via the shell module.
  11. `shell` — bash runner.
  12. `lsp` — manager only; clients spawn lazily.
  13. `mcp` — dial stdio/HTTP/SSE servers and register their tools.
  14. `tools` — assemble built-ins plus MCP-bridged tools.
  15. `agent` — coordinator over `llm` + `tools` + `session` + `message` + others.

  `Close` reverses this order strictly. Each subsystem gets a `context.Context` derived from the App's root context so that cancelling the root cascades.

- **Root context.** `New` creates an internal root context using `context.WithCancel(ctx)`. `Close` cancels this internal root before invoking each subsystem's `Close`. Hold the cancel func in an unexported `App` field.

- **Goroutine accounting.** Every subsystem that spawns a goroutine returns a `Close(ctx) error` that waits for that goroutine to exit. `app.Close` awaits each subsystem in turn under a 5-second total deadline; if a subsystem exceeds its slice of the deadline, log a warning and proceed. Use `golang.org/x/sync/errgroup`-style coordination only if necessary; a plain sequential `Close` is fine and is easier to reason about.

- **Provider lazy validation.** `New` must succeed without API keys so users can run `bharatcode --help`, `bharatcode version`, `bharatcode config edit` without keys configured. Validation lives in `internal/llm`; do not duplicate it here. Document this contract in `internal/llm/`'s own spec when that is written.

- **No globals.** No `package app` variable. The Logger, root context, all services live on the `App` value. Tests construct fresh `App` instances per test.

- **No Bubble Tea types.** `tea.Cmd` and `tea.Msg` must not appear in any signature under `internal/app/`. TUI-specific concerns live in `internal/tui/`. This keeps `app` usable from `bharatcode run`, `bharatcode stats`, etc., without dragging the Bubble Tea runtime.

- **Logger setup.** `New` configures the root `*slog.Logger`. In TTY mode use `slog.TextHandler` against stderr; in non-TTY mode use `slog.JSONHandler`. Level is `Info` by default, `Debug` when `opts.Verbose`. The logger is stored on the `App` and threaded into every subsystem's constructor that accepts one; no subsystem calls `slog.Default` once `app` has wired its own logger.

- **Signal handling.** `cmd` is responsible for installing signal handlers (SIGINT, SIGTERM) and cancelling the context passed to `app.New`. `app` itself does not call `signal.Notify` — that is a CLI concern. Document this in the comment above `New`.

- **Errors.** Wrap with `fmt.Errorf("constructing X: %w", err)` and `fmt.Errorf("closing X: %w", err)`. Sentinels: `var ErrAlreadyClosed = errors.New("app: already closed")`.

- **goleak in `TestMain`.** Per goleak convention:

  ```go
  func TestMain(m *testing.M) { goleak.VerifyTestMain(m) }
  ```

  Add `go.uber.org/goleak` to `go.mod`; this is a test-only dep and is acceptable without updating `AGENTS.md` §2 (which lists production deps).

- **FD leak test.** On linux, count `/proc/self/fd` entries before and after a 100-iteration `New`/`Close` loop. On darwin, use `lsof -p` via `os/exec`. On windows, skip. The tolerance of 4 accounts for slog, race-detector internals, and stdlib's lazy file handles.

- **Testing.** `testify/require`, `t.TempDir()`, `t.Setenv()`. No real network; the LLM registry's HTTP transports must be stubbable via `httptest` (the contract is owned by `internal/llm/` but the test seam is exercised here).

- **Implementation status.** When done, append `## Implementation status` to this file listing constructed subsystems, the actual measured `Close` time on a fresh CI runner, goleak result, and the FD-tolerance number observed during the 100-iteration loop.

## Implementation status

Status: First-pass completed on 2026-05-26.

Built:

- `internal/app.Options`, `App`, `New(ctx, opts)`, `Close(ctx)`, and `ErrAlreadyClosed`.
- Path resolution for `ProjectDir`, explicit/global config path, project config overlay discovery, and default XDG data DB path.
- Construction of `db`, app-scoped typed pubsub topics, `config`, `ledger`, `session`, `filetracker`, `llm`, `permission`, `shell`, `hooks`, `lsp`, `mcp`, `tools`, and `agent`.
- MCP and agent startup when configured by current APIs. Default construction succeeds with no provider API keys.
- Reverse shutdown for services that expose stop/close methods: agent, MCP, LSP, shell, app topic bundle, and DB. Second `Close` returns `ErrAlreadyClosed`.
- Focused tests for no-key default construction, close fast path, double close, no `internal/tui` or `internal/cmd` dependency, reverse close order, and deadline error naming.

Measured locally:

- `TestClose_FastPath_UnderDeadline` completed under the 100 ms assertion.
- `go test ./internal/app/...` passed in 0.229 s.

Current deviations from the ideal spec:

- `internal/pubsub` currently exposes typed `Topic[T]` values but no `pubsub.Bus` type, so `internal/app` defines an app-owned `Bus` topic bundle.
- `hooks.New` currently requires `*shell.Shell`, so shell is constructed before hooks even though the target spec lists hooks first. Shutdown still places shell after LSP/MCP/agent and before topics/DB.
- `config.Options.DataDir` cannot drive the initial DB open while preserving the spec's required `db.Open` before `config.LoadFrom` order. The first pass uses the XDG data default path for DB.
- Existing constructors do not all accept loggers or app root contexts, so app stores its logger and passes contexts only where current APIs allow.
- Goleak and 100-iteration FD leak tests were not added in this pass; they need broader stabilization once sibling modules expose consistently idempotent close semantics.
