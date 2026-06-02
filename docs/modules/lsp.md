# LSP

**Path:** `internal/lsp/`
**Status:** Completed

## Purpose

Minimal Language Server Protocol client that auto-discovers and starts language servers based on the files present in the project, then surfaces diagnostics (errors and warnings) so the agent can inject them into prompts. Supported servers: `gopls` for Go, `tsserver` for TypeScript and JavaScript, `pyright` for Python, `rust-analyzer` for Rust, and `clangd` for C and C++. One server per language per project. Lifecycle is session-scoped: a server starts the first time a relevant file is opened, stays alive for the duration of the BharatCode session, and is terminated cleanly on app shutdown. The module is deliberately thin — diagnostics in, diagnostics out — and does not attempt to be a general LSP toolkit.

## Public interface

```go
type Manager struct{ /* unexported */ }

func NewManager(cfg *config.Config, bus *pubsub.Topic[Diagnostic]) *Manager
func (m *Manager) Diagnostics(ctx context.Context, path string) ([]Diagnostic, error)
func (m *Manager) Shutdown(ctx context.Context) error

type Diagnostic struct {
    Path     string
    Range    Range
    Severity Severity
    Message  string
    Source   string
}

type Range struct {
    Start, End Position
}

type Position struct {
    Line, Character int
}

type Severity int // Error, Warning, Information, Hint
```

## Dependencies

Internal: `util`, `config`, `pubsub`.
External: stdlib `os/exec` (spawning), stdlib `encoding/json` (JSON-RPC framing).

## Acceptance criteria

- A project containing `*.go` files auto-starts `gopls` on first `Diagnostics` call for that path.
- A project containing `*.ts` files auto-starts `tsserver`.
- `Shutdown` terminates every running server within 5s; processes do not linger after BharatCode exits.
- `Diagnostics` returns within 500ms p99 on a warm server (server already initialized, file already opened).
- Diagnostics published to the `pubsub.Topic[Diagnostic]` are deduplicated by `(path, range, message)` to avoid spamming the TUI.
- Missing language server binary surfaces a single warning per language and gracefully degrades — the agent keeps running without diagnostics for that language.

## Notes for the implementer

Implement LSP 3.17 JSON-RPC over stdio directly. Do not pull in `go.lsp.dev/jsonrpc2` or `gopls/internal/lsp` — a minimal client is roughly 600 LOC and the heavy deps drag in test infrastructure we do not want. Frame messages with `Content-Length` headers; one goroutine per server pumps stdout into a response router keyed by request id.

Default lifecycle: send `initialize` with the project root as `rootUri`, wait for `initialized`, then send `textDocument/didOpen` lazily on the first request that touches a given file. Send `textDocument/didChange` from the `filetracker` module's file-edit events. Use `textDocument/diagnostic` (LSP 3.17 pull model) where the server advertises support; fall back to `textDocument/publishDiagnostics` (push model) otherwise — `tsserver` is push-only at time of writing.

Discovery uses simple file-extension globbing relative to project root, capped at the first hit per language to avoid walking large monorepos. Cache discovery results for the session; do not re-glob on every call.

Process management: spawn with `os/exec.CommandContext` so context cancellation kills the child; on Unix set `Setpgid: true` so child processes (some servers fork helpers) die with the parent. On shutdown, send `shutdown` and `exit` LSP messages first, then a 2s timer, then SIGKILL.

## Implementation status

- **Status:** Completed.
- **Files created:**
  - `internal/lsp/types.go`
  - `internal/lsp/protocol.go`
  - `internal/lsp/diagnostics.go`
  - `internal/lsp/client.go`
  - `internal/lsp/manager.go`
  - `internal/lsp/manager_test.go`
- **Built:** Session-scoped manager, stdio JSON-RPC framing, lazy document open, pull diagnostics, push diagnostics fallback, per-language missing-server warnings, cached discovery, process-group cleanup on Unix, and diagnostic publish deduplication by `(path, range, message)`.
- **Tests:** Covered pull diagnostics, push fallback, deduplicated publication, missing-server graceful degradation, and unsupported-extension no-op behavior with an in-process fake LSP server.
- **Deviations:** `didChange` wiring from `filetracker` is not included because the current public constructor accepts only config plus the diagnostic topic; callers can request fresh diagnostics after edits until an integration event contract lands.
