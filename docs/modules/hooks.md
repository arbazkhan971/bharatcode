# Hooks

**Path:** `internal/hooks/`
**Status:** Implemented

## Purpose

User-defined shell hooks fired at lifecycle events: `PreToolUse`,
`PostToolUse`, `SessionStart`, `SessionEnd`, `FileEdit`, and similar. Hooks
are configured per project in `.bharatcode.json` and run in parallel with a
per-hook timeout. Each hook is a shell command; BharatCode pipes a JSON payload
describing the event over stdin and parses the hook's stdout as a JSON
decision. Decisions aggregate: any hook returning `Block` cancels the action
and the agent is told why; an `Approve` short-circuits the permission check for
that tool call; otherwise execution continues.

## Public interface

```go
type Event string // PreToolUse, PostToolUse, SessionStart, SessionEnd, FileEdit, ...

type HookDef struct {
    Event   Event
    Match   string        // glob or regex against the event payload.
    Command string        // shell command to run.
    Timeout time.Duration // per-hook deadline, default 5s.
}

type Decision struct {
    Block    bool
    Reason   string
    Approve  bool
    Continue bool
}

type Engine struct{ /* unexported */ }

func New(cfg *config.Config, sh *shell.Shell) *Engine
func (e *Engine) Fire(ctx context.Context, event Event, payload any) (Decision, error)
```

## Dependencies

Internal: `config`, `shell`.

## Acceptance criteria

- All hooks matching an event run in parallel.
- Per-hook timeout is enforced, defaulting to 5 seconds and overridable from
  configuration. Timed-out hooks log a warning and return pass-through.
- Stdin payload JSON uses snake_case fields, for example:
  `{"event":"PreToolUse","tool":"bash","args":{"command":"rm -rf /"},"session_id":"..."}`
- Stdout decision JSON parses as `{"decision":{"block":true,"reason":"..."}}`
  and `{"decision":{"approve":true}}`. Empty stdout is pass-through.
- Aggregation gives `Block` priority over `Approve`, using the first configured
  blocking hook's reason. `Approve` wins only when no hook blocks.
- Hooks run through the shared `shell.Shell` runner with user environment
  inheritance, project-root cwd detection, and `BHARATCODE_EVENT` plus
  `BHARATCODE_SESSION_ID` environment variables.

## Implementation status

Implemented in `internal/hooks/hooks.go` with focused tests in
`internal/hooks/hooks_test.go`.

Built behavior:

- Converts `config.Hook` entries into internal `HookDef` values.
- Supports `PreToolUse`, `PostToolUse`, `SessionStart`, `SessionEnd`,
  `FileEdit`, `OnError`, and `OnSession` event names.
- Runs matching hooks concurrently with a per-hook timeout.
- Pipes the marshaled JSON payload to the hook command through `shell.Shell`.
- Supports empty-match, glob-match, and slash-delimited regex-match rules.
- Matches tool events by `tool`, file edit events by `path` or `file_path`,
  session events by `session_id`, and error events by `error`.
- Parses decision envelopes leniently; empty stdout, malformed JSON, timed-out
  commands, and non-zero hook exits are logged and treated as pass-through.
- Aggregates decisions according to the module rules.

Deviation:

- `shell.Shell` does not currently expose a stdin field in `RunOpts`, so the
  hook engine uses a shell pipeline to feed JSON into the command while still
  relying on the shared shell runner for execution, timeout, cwd, environment,
  and output capture.
