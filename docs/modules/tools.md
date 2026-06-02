# tools

**Path:** `internal/tools/`
**Status:** Completed

## Purpose

The `tools` package is BharatCode's catalogue of agent-callable functions. It defines the `Tool` interface that every callable unit — built-in or MCP-bridged — implements, the `Result` value the agent loop consumes, and the `Registry` that maps tool names to implementations at session start. Each built-in tool is a single Go file in this package: a struct holding its dependencies, a `Run` method that does one thing, a JSON schema describing its arguments, and a `.md` description file embedded via `go:embed` that becomes the LLM-facing docstring.

This module is the orchestrator of everything beneath the agent layer. `bash` calls `shell` (and `permission` and `hooks`). `edit` and `write` call `filetracker`. `diagnostics` calls `lsp`. `web_fetch` calls `net/http`. The agent itself does not know how any of these work; it knows only that it can call `Registry.Get(name)` and `tool.Run(ctx, args)`. That is the boundary this package enforces.

Tool prose — the embedded `.md` files — is the LLM's primary interface to BharatCode. A vague description produces a confused agent that calls the wrong tool or supplies bad arguments. A precise description, written from the model's perspective ("when to use this, what each argument means, what success looks like"), produces the opposite. The descriptions matter as much as the code; they are reviewed with equal care.

## Public interface

```go
// Package tools defines the Tool interface and the Registry that
// maps tool names to implementations. It also provides the built-in
// tools (bash, view, edit, multiedit, write, grep, glob, ls, todo,
// diagnostics, web_fetch, web_search, job_output, job_kill).
package tools

// Tool is the unit of agent capability. Implementations must be safe
// for concurrent Run calls; the agent loop may invoke the same tool
// from multiple goroutines for parallel sub-tasks.
type Tool interface {
	// Name returns the canonical tool name as referenced by the LLM
	// in tool-call requests. It must match the regex
	// [a-z][a-z0-9_]{0,63} and be unique within a Registry.
	Name() string

	// Description returns the markdown text the LLM sees when
	// deciding whether to call this tool. It is the contents of the
	// tool's embedded .md file with any go-template placeholders
	// (such as the working directory) already expanded.
	Description() string

	// Schema returns the JSON Schema (draft 2020-12) for the tool's
	// arguments. The returned bytes must be valid JSON; callers may
	// pass them verbatim to the LLM provider.
	Schema() json.RawMessage

	// Run executes the tool. args is the JSON-encoded argument
	// object the LLM produced; implementations are responsible for
	// unmarshaling it into a typed struct and returning
	// Result{IsError: true} on parse failure rather than panicking.
	// The returned error is reserved for infrastructure-level
	// failures (context canceled, internal bug); user-visible
	// tool failures live in Result.IsError + Result.Content.
	Run(ctx context.Context, args json.RawMessage) (Result, error)
}

// Result is the value returned to the agent loop after a tool runs.
// Content is the text the LLM sees as the tool's output. IsError
// marks the call as failed (the LLM is expected to retry or recover).
// StopTurn instructs the agent loop to end the current turn after
// this tool call regardless of subsequent tool calls in the same
// LLM response; it is used by tools such as the user-confirmation
// dialog. Metadata carries side-channel data for the TUI (file
// diffs, command output, job IDs) and is never shown to the LLM.
type Result struct {
	Content  string         `json:"content"`
	IsError  bool           `json:"is_error,omitempty"`
	StopTurn bool           `json:"stop_turn,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

// Dependencies is the bag of injected collaborators every built-in
// tool may need. NewRegistry wires these into each tool at
// construction time so the tools themselves do not reach into
// package-global state.
type Dependencies struct {
	Config      *config.Config
	Permission  *permission.Checker
	Shell       *shell.Engine
	Hooks       *hooks.Engine
	LSP         *lsp.Manager
	FileTracker *filetracker.Tracker
	Bus         *pubsub.Topic[message.Event]
	WorkDir     string
}

// Registry is the name-to-Tool lookup the agent loop consults on
// every tool call. It is populated once at session start and is
// safe for concurrent reads thereafter; Register is not safe to
// call concurrently with Get or List.
type Registry struct {
	// Unexported fields: tools map and mu.
}

// NewRegistry returns a Registry pre-populated with every built-in
// tool, each wired with the supplied dependencies. MCP-bridged
// tools are added later by the app wiring code via Register.
func NewRegistry(deps Dependencies) *Registry

// Register adds t to the registry under t.Name(). It panics if a
// tool with the same name is already registered; the call site is a
// programming error, not a runtime condition.
func (r *Registry) Register(t Tool)

// Get returns the tool registered under name. The boolean is false
// when no such tool exists; callers should surface this to the LLM
// as Result{IsError: true, Content: "unknown tool: " + name}.
func (r *Registry) Get(name string) (Tool, bool)

// List returns every registered tool in deterministic order
// (lexicographic by Name). The agent loop uses this list to build
// the tool-spec section of the system prompt.
func (r *Registry) List() []Tool
```

### Built-in tools

One Go file per tool under `internal/tools/`. Each file owns a struct, a constructor (`newXTool(deps Dependencies) *XTool`), the `Tool` interface methods, and an embedded `.md` (or `.md.tpl` for templated descriptions).

| File | Name | Args | Notes |
|---|---|---|---|
| `bash.go` | `bash` | `{command, timeout?, cwd?, background?}` | Routes via `permission` → `hooks.PreToolUse` → `shell.Run` → `hooks.PostToolUse`. Background mode returns a `job_id` in Metadata. |
| `view.go` | `view` | `{path, offset?, limit?}` | Reads a text or image file. Lines are numbered. Images return a base64 image part in Metadata. |
| `edit.go` | `edit` | `{path, old_string, new_string, replace_all?}` | Single string replacement. Emits a `filetracker` entry. Fails when `old_string` is not unique unless `replace_all=true`. |
| `multiedit.go` | `multiedit` | `{path, edits: [{old, new, replace_all?}, ...]}` | Sequential edits in one atomic file rewrite. Either every edit applies or none do. |
| `write.go` | `write` | `{path, content}` | Overwrite or create. Refuses unless the file has been viewed in this session (anti-foot-gun). |
| `grep.go` | `grep` | `{pattern, path?, include?, output_mode?}` | Shells out to `rg`; falls back to a pure-Go regex walk when `rg` is missing. |
| `glob.go` | `glob` | `{pattern, path?}` | Uses `doublestar` patterns via Go stdlib `path/filepath.Match` with manual `**` handling. |
| `ls.go` | `ls` | `{path, ignore?}` | Lists one directory. Respects `.gitignore` by default. |
| `todo.go` | `todo` | `{action, items?}` | In-session todo list. Actions: `add`, `update`, `delete`, `list`. State lives in `pubsub` so the TUI can render it. |
| `diagnostics.go` | `diagnostics` | `{path?}` | Returns LSP diagnostics for one file or the whole workspace. |
| `web_fetch.go` | `web_fetch` | `{url, prompt?}` | Fetches a URL and converts the HTML body to markdown. |
| `web_search.go` | `web_search` | `{query}` | Queries DuckDuckGo or Brave (provider chosen by `config.WebSearch.Provider`). |
| `job_output.go` | `job_output` | `{job_id}` | Reads accumulated stdout/stderr from a background bash job. |
| `job_kill.go` | `job_kill` | `{job_id}` | Sends SIGTERM, then SIGKILL after 5s. |

## Dependencies

- `internal/util` — `util.ExpandPath`, `util/fsext.Exists`, `util/fsext.AtomicWrite`.
- `internal/message` — `message.Event` for streaming tool output to the TUI.
- `internal/config` — workspace root, web-search provider, model-specific tool defaults.
- `internal/permission` — every mutating or executing tool calls `Checker.Check` before running.
- `internal/shell` — `bash`, `job_output`, and `job_kill` go through the shell engine.
- `internal/hooks` — fired by the agent's `hooked_tool` decorator; this package supplies the raw tools.
- `internal/lsp` — `diagnostics` and `view` (for inline diagnostics) query the LSP manager.
- `internal/filetracker` — `edit`, `multiedit`, and `write` register changes for the session.
- `internal/pubsub` — `todo` and long-running tools publish progress events.
- External (locked stack): stdlib `net/http`, `github.com/hashicorp/go-retryablehttp` for `web_fetch`.
- External (proposed addition, see Notes): `github.com/JohannesKaufmann/html-to-markdown/v2` for `web_fetch`'s HTML-to-markdown conversion. Not currently in AGENTS.md §2; must be added there or recorded in `docs/decisions/` before implementation.

## Acceptance criteria

1. `go test ./internal/tools/...` passes on linux, darwin, and windows runners.
2. `go test -race ./internal/tools/...` passes with no data-race reports.
3. `go vet ./internal/tools/...` and `golangci-lint run ./internal/tools/...` are clean.
4. Every public type and function has a Go doc comment ending in a period; `go doc ./internal/tools` is clean of "undocumented" warnings.
5. Each built-in tool has a `*_test.go` file with table-driven unit tests covering at minimum: happy path, malformed JSON args, missing required args, permission denial, and one tool-specific edge case (e.g. `edit` rejects a non-unique `old_string` without `replace_all`).
6. `bash` invokes `permission.Checker.Check` exactly once per call (assert via mock counter) and fires no shell command when permission is denied.
7. `bash` with `background: true` returns a `job_id` in `Result.Metadata["job_id"]`, the underlying job is reachable via `job_output` in a subsequent call, and `job_kill` terminates it within 5 seconds.
8. `view` rejects paths outside `Dependencies.WorkDir` unless the path is absolute and explicitly listed in `config.AllowedReadPaths`; the rejection produces `Result{IsError: true}` and does not return a Go error.
9. `edit` creates a `filetracker` entry containing the pre-image and post-image; the entry survives a `git diff`-style render in a unit test.
10. `multiedit` is atomic: a failure on the third of four edits leaves the file unchanged on disk (assert via SHA256 before and after).
11. `write` refuses to overwrite an existing file that has not been viewed in this session (`filetracker.HasViewed(path) == false`); the test scaffolds an unviewed file and asserts the refusal.
12. `grep` prefers the `rg` binary when present (assert by stubbing `exec.LookPath`); when `rg` is absent, the pure-Go fallback walks the workspace and returns the same shape of result for a fixture pattern.
13. `glob` matches `**/*.go` against a fixture tree and returns the expected file list in lexicographic order.
14. `ls` honors a `.gitignore` containing `node_modules/` by excluding that directory from output.
15. `todo` round-trips an `add` then `list` action; the listed items survive across calls in the same `Dependencies.Bus` instance.
16. `diagnostics` returns the LSP diagnostics from a stub `lsp.Manager` for a fixture file containing a known syntax error.
17. `web_fetch` against an `httptest.NewServer` serving HTML returns a markdown body that contains the expected headings and link text, with all `<script>` and `<style>` content stripped.
18. `web_search` with a stub provider returns at least one result struct with `Title`, `URL`, and `Snippet` populated.
19. **No tool panics on any input.** A fuzz test (`go test -fuzz=Fuzz -fuzztime=10s ./internal/tools/...`) feeds random bytes as `args` to each tool and asserts the return is `(Result{IsError: true}, nil)` or `(_, ctx.Err())`, never a panic.
20. Each built-in tool's embedded `.md` file is at least 200 bytes and at most 4 KiB, contains a clear "When to call this tool" section, and is freshly written for BharatCode. A `go test` step `grep -L "BharatCode\|workspace\|argument" internal/tools/*.md` returns empty (every description references at least one BharatCode-specific concept).

## Notes for the implementer

- **External-dependency flag.** This module needs `github.com/JohannesKaufmann/html-to-markdown/v2` (for `web_fetch`) and the `rg` binary (preferred path for `grep`). Neither is in AGENTS.md §2. Before writing `web_fetch.go`, either add `html-to-markdown` to §2 or write `docs/decisions/YYYY-MM-DD-html-to-markdown.md` justifying the addition. For `rg`, document the fallback explicitly: detect via `exec.LookPath("rg")` at registry construction, store the result on the `grep` tool, and use the pure-Go regex walker when absent. Do **not** install `rg` as part of `go install` flows.
- **Tool descriptions are original.** Write each `.md` from scratch: open with one sentence stating when to call the tool, then a bullet list of arguments with types and meaning, then a short "what success looks like" paragraph, then a list of failure cases. Tool names (`bash`, `view`, etc.) follow common conventions for prompt portability; the prose must be original.
- The `Tool` interface is intentionally tiny. Resist adding methods. New capabilities should go through `Result.Metadata` (free-form map) or a separate optional interface that callers type-assert (e.g., `Streamer interface { Stream(ctx) <-chan Result }` for tools that emit incremental output). Streaming is out of scope for v1 but the door is left open here.
- All file paths handed to tools must pass through `util.ExpandPath`. Reject paths that escape `Dependencies.WorkDir` via `..` after cleaning (`filepath.Rel` followed by a check for a leading `..`). The `view` acceptance criterion above codifies the exception.
- Tools never log to stderr. Use `slog` with a `slog.With("tool", t.Name())` child logger held on the tool struct. The TUI surfaces events; logs go to the on-disk log file.
- `Result.Content` is the LLM-visible text. Keep it under 32 KiB by default; truncate with a trailing `\n…[truncated N bytes]` for `bash`, `view`, and `grep` outputs. The truncation limit is configurable via `config.MaxToolOutputBytes`.
- `StopTurn` is rare. Use it only for tools that meaningfully end the agent's turn (a user-confirmation dialog that the agent must wait for, for example). The standard tools above do not set it.
- The agent loop wraps every tool with `hooked_tool.go` (defined in `internal/agent/`) to fire hooks. Do **not** call hooks from inside this package; that would double-fire.
- `permission.Check` returns one of `Allow`, `Deny`, `Ask`. `Ask` blocks until the TUI surfaces a dialog; for headless mode (config `headless: true`) `Ask` is treated as `Deny`. Tool implementations do not need to know which branch fired — `Checker.Check` blocks until a decision is final.
- Each tool's JSON schema lives in a `schemaXxx = json.RawMessage(`{ ... }`)` package-level variable, with the schema validated by a `TestSchemas` init test that calls `jsonschema.MetaSchemaDraft202012.Validate` on every registered tool. (Pull `github.com/santhosh-tekuri/jsonschema/v6` only if not already present in go.mod; otherwise hand-roll a minimal validator. Document the choice in a decision record.)
- Job IDs returned by `bash` background mode are opaque strings produced by `internal/shell`. Do not interpret them in this package.
- Image handling in `view`: PNG, JPEG, GIF, WEBP up to 5 MiB. Larger images return `Result{IsError: true, Content: "image too large"}`. Encode as base64 in `Result.Metadata["image"]` with `Result.Metadata["mime_type"]` set.
- `web_search` provider selection lives in `config.WebSearch.Provider` (`"duckduckgo"` or `"brave"`). Brave requires `BRAVE_API_KEY`; if missing and `brave` is selected, fall back to DuckDuckGo with a one-line `slog.Warn`.
- Acceptance #19 (no panics) is non-negotiable. Wrap every `Run` body in a deferred `recover()` that converts a panic into `Result{IsError: true, Content: "internal tool panic: " + r}` plus a `slog.Error` with the stack trace. A panic in a tool must not crash the agent.

## Implementation status

Implemented in `internal/tools/`:

- Shared `Tool`, `Result`, `Dependencies`, and `Registry` types with
  deterministic listing and panic-safe registry wrappers.
- Built-in tools: `bash`, `view`, `edit`, `multiedit`, `write`, `grep`,
  `glob`, `ls`, `todo`, `diagnostics`, `web_fetch`, `web_search`,
  `job_output`, and `job_kill`.
- Embedded markdown descriptions for every built-in tool.
- Workspace path checks, read-before-write tracking, filetracker write records,
  background shell job inspection and cancellation, pure-Go grep fallback,
  recursive globbing, `.gitignore`-aware directory listing, in-session todos,
  LSP diagnostics formatting, HTML fetch stripping, and DuckDuckGo HTML result
  parsing.
- Unit tests for registry behavior, shell/job tools, filesystem tools,
  search/list/web/todo tools, and diagnostics source integration.

Deliberate deviations and notes:

- `web_fetch` uses a small stdlib HTML-to-markdown reducer instead of adding
  `github.com/JohannesKaufmann/html-to-markdown/v2`; this avoids a new locked
  dependency while preserving script/style stripping and basic heading/link
  conversion.
- The current `config.Config` shape does not expose `AllowedReadPaths`,
  `MaxToolOutputBytes`, or `WebSearch` fields. The implementation uses
  reflection-based compatibility hooks where practical and otherwise defaults
  to workspace-only reads, a 32 KiB output cap, and DuckDuckGo HTML search.
- `todo` state is keyed by the existing tool-call pubsub topic because the
  current pubsub package does not yet define a dedicated todo topic.
- `go test -race ./internal/tools` passes. `gofumpt` and `golangci-lint` are
  not installed in this environment, so formatting used `gofmt` and linting
  could not be executed.
