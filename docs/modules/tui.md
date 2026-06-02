# tui

**Path:** `internal/tui/`
**Status:** First-pass implemented

## Purpose

The `tui` module is BharatCode's full-screen interactive surface. It is a Bubble Tea v2 program that hosts the entire user-facing experience: a streaming chat panel with markdown-rendered assistant messages, a multiline input area with history and completions, a diff viewer that reads `edit`/`multiedit`/`write` tool results, a collapsible project file tree, modal dialogs (permission prompts, model switch, agent switch, settings, session picker, error display), a top header, a bottom status bar showing model and agent identity, and the BharatCode-signature INR ledger footer that updates after every LLM call. The module is the largest single sub-system in the codebase (target ~5000–8000 LOC across all subcomponents) and is the layer most users will perceive as "BharatCode" itself.

It exists to keep the interactive concerns — input routing, focus, screen layout, modal stacking, animated streaming, theme tokens, and re-render budgeting — strictly separated from the agent loop, the session store, and the LLM transport, all of which are passed in via the `Dependencies` struct. The TUI owns nothing persistent; every state mutation that needs to outlive the process is delegated through the injected dependencies. This separation is what lets `cmd run` and future headless front-ends share the same agent core without dragging Bubble Tea into a non-TTY path.

## Public interface

Only two top-level symbols are exported from `internal/tui/`. Everything else is package-private to keep the surface minimal and to forbid downstream packages from coupling to internal model types.

```go
// Package tui is the Bubble Tea v2 program for BharatCode's
// interactive terminal interface. Construct a Dependencies value,
// call Run, and block until the user exits.
package tui

// Run launches the TUI and blocks until the program exits, the
// context is cancelled, or a fatal render error occurs. It returns
// nil on a clean user-initiated quit, ctx.Err() if cancelled, or a
// wrapped error otherwise. Run installs and restores terminal
// alt-screen, mouse capture, and bracketed paste; callers do not
// configure these themselves.
func Run(ctx context.Context, deps Dependencies) error

// Dependencies is the full set of services the TUI consumes. All
// fields are required and must be non-nil; Run returns an error
// before entering the event loop if any field is nil. The TUI never
// constructs these itself — they are wired by internal/app and
// passed in from internal/cmd.
type Dependencies struct {
    // Agent is the agent loop that processes user prompts and emits
    // streamed message events on Bus.
    Agent *agent.Loop

    // Sessions is the session repository used for save, restore,
    // and the /sessions picker.
    Sessions *session.Repo

    // Cfg is the merged user + project configuration. The TUI reads
    // theme, keymap overrides, and initial model/agent from here.
    Cfg *config.Config

    // Bus is the in-process event bus the TUI subscribes to for
    // message streams, permission requests, ledger updates, and
    // file-tracker change notifications.
    Bus *pubsub.Bus

    // Permission is the tool-permission checker. The TUI calls
    // Permission.Request via a pubsub channel and renders a modal
    // until the user answers; --yolo short-circuits this path.
    Permission *permission.Checker

    // Ledger is the INR/USD cost ledger. The TUI reads cumulative
    // counts after every LLM response to refresh the footer and
    // displays a warning modal when a budget cap is approached.
    Ledger *ledger.Ledger

    // FileTracker reports per-session file changes; the file-tree
    // subcomponent highlights modified entries.
    FileTracker *filetracker.Tracker

    // Logger is the slog logger the TUI uses for diagnostics. Must
    // not write to stdout in TTY mode (would corrupt the screen).
    Logger *slog.Logger
}
```

### Subcomponents (internal — one Go package each under `internal/tui/`)

Each subcomponent is a stateful struct with imperative methods, not its own Bubble Tea model. The single top-level model in `internal/tui/` owns `Update`/`View`/`Init` and dispatches to subcomponents.

- `internal/tui/chat/` — message list, streaming renderer, glamour-rendered markdown, chroma-highlighted code blocks; exposes `Append(msg message.Message)`, `Stream(id string, delta string)`, `Render(width int) string`.
- `internal/tui/input/` — multiline input with persistent history (per-project, on-disk), inline completions, slash-command parsing; bubbles `textarea` underneath.
- `internal/tui/diff/` — unified-by-default diff viewer with optional side-by-side mode, used by the chat panel when rendering `edit`/`multiedit`/`write` tool results.
- `internal/tui/filetree/` — collapsible tree of project files, modified entries styled distinctly; lazy-loaded per directory to avoid stat'ing a whole monorepo.
- `internal/tui/dialog/` — modal stack with push/pop/contains; concrete dialogs: `permission.go`, `model_picker.go`, `agent_picker.go`, `settings.go`, `sessions.go`, `error.go`, `budget_warning.go`.
- `internal/tui/completions/` — completion engine and popup for slash commands, `@`-mentions of file paths, and tool names; filterable, keyboard-navigated.
- `internal/tui/statusbar/` — single-line bottom bar: `model · agent · session · uptime`.
- `internal/tui/ledger/` — the INR cost footer (BharatCode signature). Renders `session <id> · in <tokens> · out <tokens> · cost $<usd> · ₹<inr> · budget ₹<cap>/mo (<pct>% used)`. Pct ≥ 80 styled in warn color; ≥ 100 in error color.
- `internal/tui/notification/` — desktop notifications when the terminal loses focus; wraps the chosen notification library behind a `Notifier` interface so tests can substitute a noop.
- `internal/tui/styles/` — lipgloss theme: light and dark variants, shared color tokens, named text styles. No subcomponent inlines hex colors.

### Keyboard model (defaults — overridable by `Cfg.Keymap`)

| Key | Action |
|---|---|
| `Ctrl+C` | Cancel in-flight LLM stream; if no stream and prompt is empty, quit. |
| `Tab` | Trigger completion if pending; otherwise cycle focus chat ↔ input. |
| `Ctrl+K` | Open command palette. |
| `Ctrl+P` | Switch model (remapped from `Ctrl+M` — see Notes for the implementer). |
| `Ctrl+A` | Switch agent (coder, task). |
| `Ctrl+S` | Open settings dialog. |
| `Ctrl+D` | View diff of last edit. |
| `Esc` | Dismiss the topmost modal; if none, clear pending completion. |
| `/` at start of empty input | Enter slash command. |

### Slash commands

| Command | Effect |
|---|---|
| `/help` | List commands with one-line descriptions. |
| `/clear` | Clear visible chat (does not delete the session). |
| `/sessions` | Open session picker dialog. |
| `/model` | Open model picker dialog. |
| `/agent` | Open agent picker dialog. |
| `/goal [text|clear]` | Show, set, or clear the current session goal. |
| `/budget` | Show current ledger + open budget settings. |
| `/yolo` | Toggle permission bypass; updates statusbar indicator. |
| `/save` | Persist session immediately (auto-save still runs on every turn). |
| `/quit` | Exit cleanly. |

## Dependencies

- `internal/util` — path formatting, `Truncate`, `HumanDuration`, `HumanBytes`.
- `internal/config` — theme, keymap, initial model/agent.
- `internal/session` — session list, switch, save.
- `internal/message` — `Message`, `ContentBlock` types rendered by chat.
- `internal/agent` — `agent.Loop` to drive the conversation.
- `internal/ledger` — read cumulative counts for the footer; subscribe to ledger updates via `Bus`.
- `internal/permission` — render permission requests blocked by the modal.
- `internal/pubsub` — subscribe to message stream, permission, ledger, filetracker topics.
- `internal/llm` — type-only: model identity, provider metadata for the model picker.
- `internal/filetracker` — modified-file set for the file tree.

External (locked stack):

- `github.com/charmbracelet/bubbletea/v2` — program runtime.
- `github.com/charmbracelet/lipgloss/v2` — styling primitives.
- `github.com/charmbracelet/bubbles/v2` — `textarea`, `viewport`, `key`, `help`.
- `github.com/charmbracelet/glamour/v2` — markdown rendering inside assistant messages.

External (not yet in `AGENTS.md` §2 locked stack — see Notes):

- `github.com/alecthomas/chroma/v2` — inline syntax highlighting for fenced code blocks.
- A desktop notification library, candidate: `github.com/gen2brain/beeep`.

## Acceptance criteria

Each numbered item is testable. Manual-run items name a `make` target the implementer adds.

1. `go test ./internal/tui/...` passes with `-race` on linux, darwin, windows.
2. `go vet ./internal/tui/...` and `golangci-lint run ./internal/tui/...` are clean.
3. `Run(ctx, deps)` with any `nil` field on `deps` returns a non-nil error before installing the alt-screen. Verified by `TestRun_NilDependency_RejectsEarly`.
4. `Run(ctx, deps)` with a cancelled `ctx` returns `ctx.Err()` and restores the terminal (no leftover alt-screen, mouse capture off). Verified by `TestRun_CancelledContext_RestoresTerminal`.
5. `TestChat_StreamRender_NoFlicker` — feeding 100 deltas to `chat.Stream` produces ≤101 distinct render outputs (one per delta plus the final) and never reflows the entire message list (assert via a render-region counter the implementer adds).
6. `TestDiff_UnifiedHunk_Renders` — golden-file test for a synthetic edit diff matches `testdata/diff_unified.txt`.
7. `TestPermissionDialog_BlocksInput` — while a permission modal is on the stack, keystrokes routed to input do nothing; pressing the modal's "yes" key publishes a `permission.Granted` event and pops the modal.
8. `TestLedgerFooter_UpdatesOnPubsubEvent` — publishing a `ledger.Update` event on `Bus` causes the next `View()` to include the new tokens/cost values.
9. `TestLedgerFooter_BudgetWarnStyling` — when monthly usage ≥ 80%, the footer's percent segment renders in the `warn` style; ≥ 100% in `error`.
10. `TestResize_RedrawsLayout` — a `tea.WindowSizeMsg` of 120×40 → 80×24 produces a layout whose computed rectangles sum to the new viewport with no overlap or gap (asserted via a `layout` invariant probe).
11. `TestMinimumSize_BelowFloor_GracefulFallback` — for a viewport below 80×24, `View()` returns a single-line message `terminal too small (need 80x24)` and the program does not panic.
12. `TestSlashCommand_Help_ListsAll` — typing `/help` and pressing Enter renders a non-empty list including every command in the Slash commands table above.
13. `TestKeymap_CtrlP_OpensModelPicker` — pressing `Ctrl+P` from idle pushes the model picker dialog onto the stack.
14. `TestStatusbar_FieldsPresent` — the rendered status bar contains the current `model`, `agent`, `session` short id, and an uptime that monotonically increases between two ticks.
15. `TestStyles_NoHardcodedHex` — a static check over `internal/tui/` (excluding `styles/`) asserts no string literal matches `/#[0-9a-fA-F]{3,6}/`. Implemented as a Go test that walks the package.
16. `TestNotification_NoopWhenTerminalFocused` — with the `Notifier` test double, focus-gained events suppress notifications and focus-lost events emit them.
17. Manual: `make tui-smoke` runs the binary against a recorded stub agent (`internal/tui/testdata/stub_agent.json`) and produces no panic or unexpected output for a 30-second scripted session.

## Notes for the implementer

- **Bubble Tea v2 model.** One `Update`/`View`/`Init` lives in `internal/tui/`. Subcomponents are stateful structs with imperative methods (`Append`, `Render(width) string`, `HandleKey`). They do not implement `tea.Model` and do not participate in the message loop. The top-level `Update` is a giant `switch msg.(type)` that dispatches.
- **Never do I/O or expensive work in `Update`.** Any LLM stream subscription, file read, glamour render of a large markdown block, or notification fire must be a `tea.Cmd`. State only mutates inside `Update`; commands return messages that drive the next mutation.
- **Re-render budget.** Cache rendered message items in the chat list and invalidate on data change; the streaming-markdown subcomponent must reuse the cached prefix and re-render only the appended delta. Target ≤ 16 ms per `View()` call on a 200-message session; document actuals in the `Implementation status` section.
- **`Ctrl+M` collision with Enter.** On most terminals `Ctrl+M` is the same byte as `CR`/`Enter` (0x0D), so the original keyboard plan from the task is unworkable. The accepted default is `Ctrl+P` for "switch model" (see the Keyboard model table). If you remap, update both this spec and `docs/decisions/` with an ADR per `AGENTS.md` §7.
- **External deps not yet locked.** `chroma/v2` and a notification library (`beeep` candidate) are not in `AGENTS.md` §2. Before importing either, add a row to the locked-stack table (chroma is broadly used in the Go TUI ecosystem and is a low-risk add; beeep is more invasive — cross-platform CGO-free desktop notifications are a small landscape, validate the choice in an ADR).
- **Glamour configuration.** Use a glamour `TermRenderer` with width set per call (terminal width changes on resize). The renderer is expensive; construct one per chat-item cache miss, not per `View()`.
- **No screen-buffer pipeline.** A low-level screen-buffer / `Draw(scr, rect)` render model is out of scope. BharatCode renders by composing strings; layout is computed in a `layout` struct of `int` rectangles and components are sized by passing `width` (and sometimes `height`) into their `Render` methods.
- **Focus model.** Two focus states are sufficient for Phase 1: `focusInput` and `focusChat`. `Tab` cycles between them; dialogs always steal focus while on the stack.
- **Permission flow.** The permission checker publishes a `permission.Requested` event on `Bus`. The TUI subscribes, pushes a modal, and on user response publishes `permission.Granted{ID, Decision}`. The checker resolves the original blocked call. The TUI does not call `Permission.Decide` directly; it only renders.
- **Ledger footer redraw.** Subscribe to a `ledger.Updated` topic on `Bus`. Each event carries the new totals (`InputTokens`, `OutputTokens`, `USD`, `INR`, `MonthlyBudgetINR`, `MonthlyUsedINR`). Avoid recomputing on every frame — cache and only re-render when the event fires or the model changes.
- **Notifications.** The `Notifier` interface lives in `internal/tui/notification/`; a real implementation guarded by a build tag for each OS, and a noop used in tests. Hook to a "terminal lost focus" signal (`tea.FocusLostMsg`) and emit on `agent.Done` events when not focused.
- **Logging.** The TUI must not write to stdout/stderr after the alt-screen is installed; route logs through `deps.Logger` which `internal/app` configures to a file in non-TTY mode or `slog.Discard` while the TUI is up. Log messages start capitalized, no trailing period.
- **Errors.** Wrap with `fmt.Errorf("doing X in tui: %w", err)`. Fatal render errors propagate up via the `tea.Quit` path so `Run` can return a wrapped error.
- **File permissions.** Any on-disk artifact (history file, draft file) uses `0o600` (user-only) since it may contain prompt contents.
- **Testing.** Use `tea.Program.Send` against a programmatically constructed model in tests; do not spawn a real terminal. `testify/require`, `t.TempDir()`, table-driven where input varies. The `bubbletea/v2` `tea.Program` constructor exposes a `tea.WithInput`/`tea.WithOutput` for headless test wiring.
- **Implementation status.** When done, append `## Implementation status` to this file listing built subcomponents, deviations, benchmark numbers for chat-stream and `View()`, and an explicit note on whether `chroma` and the notification library were added to `AGENTS.md` §2.

## Implementation status

First-pass TUI implementation is present under `internal/tui/`.

Built subcomponents:

- Top-level Bubble Tea v2 model and `Run(ctx, deps)` entrypoint with early
  dependency validation and cancelled-context fast return.
- Cached chat stream renderer with append/stream/finish/clear support.
- Unified diff renderer with a golden test fixture.
- Modal stack with permission and generic text dialogs; modal focus blocks input.
- Status bar with model, agent, session, uptime, and YOLO indicator.
- INR ledger footer with token/cost totals and warn/error budget styling.
- Focus-aware notification wrapper with a noop implementation.
- Centralized lipgloss theme tokens in `internal/tui/styles`.
- Resize layout calculation with minimum-size fallback.
- Slash-command handling for `/help`, `/clear`, `/sessions`, `/model`,
  `/agent`, `/goal`, `/budget`, `/yolo`, `/save`, and `/quit`.

Deviations and current limits:

- The current worktree does not yet expose the spec's `pubsub.Bus` type, so
  `Dependencies.Bus` is wired to the existing typed `*pubsub.Topic[agent.Event]`.
  Permission prompts subscribe through the existing global permission topic.
- The existing ledger event payload in `internal/pubsub` is still a placeholder,
  so tests drive footer updates through the top-level model message used by the
  Bubble Tea loop. The footer is ready to consume real `ledger.Summary` values
  once the cross-module event is finalized.
- `github.com/charmbracelet/bubbletea/v2` and
  `github.com/charmbracelet/lipgloss/v2` are pinned to beta versions that still
  use the locked import paths in `AGENTS.md`. The stable v2 module line has
  moved to a vanity import path and currently requires a newer Go toolchain.
- Markdown rendering currently falls back to plain wrapped text; `glamour/v2`
  was not imported because the current stable v2 module requires Go 1.25+.
- No `chroma` dependency was added.
- No desktop notification library was added; notifications use the local
  `Notifier` interface and noop implementation.
- Project file tree, completion popup, session picker data binding, model
  switching side effects, real diff extraction from edit tool results, and
  full agent-run submission wiring remain future work.

Measured locally with `go test ./internal/tui/...` on this worktree:

- `TestStreamRender_NoFlicker` fed 100 deltas and produced at most one cached
  render miss per delta plus final render.
- The focused TUI package test run completed in under one second on the local
  container; no separate benchmark file has been added yet.
