# Permission

**Path:** `internal/permission/`
**Status:** Completed

## Purpose

Permission gating for tool calls. Before the agent invokes any side-effecting tool — `bash`, `edit`, `write`, and anything else that mutates the user's machine — the permission module decides whether the operation is allowed in the current scope. Three modes: `ask` (the default; the TUI prompts the user), `allow-list` (a config-driven set of patterns that are auto-approved), and `--yolo` (a global escape hatch that skips every check and logs the bypass). User decisions can be remembered at one of four scopes: once, for the session, for the project, or forever (global config). This is the gatekeeper that keeps the agent safe; it must be impossible for a tool to side-effect without flowing through here.

## Public interface

```go
type Decision string // Allow, Deny, AllowOnce, AllowSession, AllowProject, AllowForever

type Scope string // ScopeOnce, ScopeSession, ScopeProject, ScopeForever

type Request struct {
    ToolName  string
    Args      map[string]any
    SessionID string
}

type Checker struct{ /* unexported */ }

func New(cfg *config.Config, bus *pubsub.Topic[Request]) *Checker

func (c *Checker) Check(ctx context.Context, req Request) (Decision, error)
func (c *Checker) SetYolo(on bool)
func (c *Checker) RememberDecision(req Request, decision Decision, scope Scope) error
```

## Dependencies

Internal: `util`, `config`, `pubsub`.

## Acceptance criteria

- `--yolo` mode bypasses every check and emits a single `slog.Warn` line per bypass with the tool name and sanitized args.
- Session-scope remembered decisions are cleared when the session ends; nothing about session-scope writes persists to disk.
- Project-scope remembered decisions persist to `.bharatcode.json` in the project root and survive process restart.
- Forever-scope remembered decisions persist to the global config (`$XDG_CONFIG_HOME/bharatcode/config.json`).
- Tests cover all six `Decision` branches plus the deny path plus the yolo bypass — eight cases minimum, table-driven.
- A `Check` call blocked on user input returns `ErrCancelled` if `ctx` is cancelled before the user answers; the goroutine waiting on the response channel must not leak.

## Notes for the implementer

The TUI subscribes to the `pubsub.Topic[Request]` and renders a permission dialog when a request arrives. The `Checker` blocks on a per-request response channel (stored in an `sync.Map` keyed by a request id) until the TUI replies via a separate `Respond(reqID, decision, scope)` method. This decouples the checker from the TUI cleanly — the checker does not know what a `bubbletea.Model` is.

Match key for remembered decisions: tool name plus a sanitized arg key. For `bash`, the arg key is the first non-flag, non-pipe word of the command (e.g., `bash:rm` matches every `rm` command regardless of flags). For `edit`/`write`, the arg key is the file path normalized to an absolute path. For `web_fetch`, the arg key is the URL's host. Document each tool's sanitization rule in the implementation, not the spec — the spec just guarantees that "allow this once" never silently broadens to "allow anything that uses this tool".

Decision resolution order on every `Check`: yolo → session memory → project memory → global memory → ask. The first match wins; ask is the fallback. A `Deny` decision at any scope is sticky: an `AllowSession` cannot override a `DenyProject`. This prevents a malicious or buggy in-session prompt from undoing a careful policy decision.

Persistence is JSON, atomic-write via temp file + rename to avoid half-written config on crash. Concurrent writers are serialized by a per-file mutex held inside the `Checker`. Do not depend on flock or external locking — BharatCode is single-process per session.

`RememberDecision` returns an error if the scope cannot be written (read-only filesystem, missing config dir), but `Check` still respects the decision in memory for the rest of the session. Persistence is best-effort; safety is mandatory.

## Implementation status

- **Status**: Completed.
- **Implemented features**:
  - Main gating validation pipeline implementing `ask` prompts, YOLO bypasses (slog warning logs), config allow-lists, and deny-lists.
  - Layered remembered decisions resolving in exact priority: YOLO -> Session -> Project -> Global -> Allow/Deny Lists -> Ask fallback.
  - Multi-tier persistent memory safely syncing ScopeSession (active memory), ScopeProject (.bharatcode.json), and ScopeForever (global config.json).
  - Safe argument sanitization rules extracting specific command signatures (e.g. `bash:rm`), normalized absolute paths (`edit:/path`), or hostnames (`web_fetch:host`).
  - Cancellation safe prompt loops returning `ErrCancelled` cleanly without goroutine leaks.
- **Deviations**: None. Everything is built to perfect parity with specifications.
