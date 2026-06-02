# AGENTS.md — Instructions for AI Coding Agents

You are an AI coding agent (Gemini CLI, Codex, Claude Code, or similar) tasked with building **BharatCode**, a Go-based CLI coding agent. This file is the canonical entry point. Read it fully before doing anything else.

## What BharatCode is

A terminal-based AI coding assistant in Go, providing the full surface of a modern terminal coding agent. Full TUI (Bubble Tea), agent loop with tools, MCP support, LSP integration, multi-provider LLM support, persistent sessions, permission-gated tool calls.

**Positioning:** Go-native, MIT-licensed, open-weight-first. First-class support for Moonshot (Kimi K2), DeepSeek (V3+R1), Together, Fireworks, Groq, OpenRouter, plus local Ollama/LM Studio, plus Anthropic and OpenAI as premium fallbacks.

## Hard rules — read these carefully

### 1. Locked technology stack

Do not deviate without updating this document first.

| Concern | Library | Why |
|---|---|---|
| TUI framework | `github.com/charmbracelet/bubbletea` (v2) | SOTA Go TUI |
| TUI styling | `github.com/charmbracelet/lipgloss` (v2) | Companion to Bubble Tea |
| TUI components | `github.com/charmbracelet/bubbles` (v2) | Standard widgets |
| Markdown rendering | `github.com/charmbracelet/glamour` (v2) | Best Go markdown renderer |
| CLI framework | `github.com/spf13/cobra` | Standard Go CLI |
| Config | `github.com/spf13/viper` | Standard Go config |
| Storage | SQLite via `modernc.org/sqlite` (pure Go, no CGO) + `github.com/sqlc-dev/sqlc` codegen | Type-safe queries, CGO-free |
| LSP client | Custom thin client over stdio (LSP 3.17 spec) | Avoid heavy dependency |
| MCP client | `github.com/mark3labs/mcp-go` | Mature Go MCP SDK |
| HTTP | stdlib `net/http` + `github.com/hashicorp/go-retryablehttp` | Resilient HTTP |
| Logging | `log/slog` (stdlib) | Modern Go logging |
| Testing | stdlib `testing` + `github.com/stretchr/testify/require` | Standard Go test stack |
| LLM abstraction | **Custom thin interface in `internal/llm/`** (no external LLM-abstraction SDK) | Own the abstraction internally |

CGO is disabled. Builds with `CGO_ENABLED=0`.

### 2. Module isolation

The architecture in `docs/architecture.md` describes 18 modules. Each module has its own spec at `docs/modules/<name>.md` with:

- **Purpose** — one-paragraph what and why
- **Public interface** — exported types and functions
- **Dependencies** — other BharatCode modules it imports
- **Acceptance criteria** — testable conditions for "module is done"

Build modules in the order listed in `docs/architecture.md#build-order`. Do not work on a module whose dependencies are not yet built.

### 3. Coding conventions

- Go 1.23+ (using stdlib `slog`, `errors.Is/As`, `context`).
- Format with `gofumpt -w .` before every commit.
- Lint with `golangci-lint run`.
- All exported identifiers documented (Go doc comments ending in `.`).
- All errors wrapped with `fmt.Errorf("doing X: %w", err)`.
- `context.Context` as first parameter for any I/O or long-running operation.
- Interfaces defined in consuming packages, kept small (1-3 methods).
- Snake_case JSON tags.
- Octal file permissions (`0o644`, `0o755`).
- Log message capitalized, no trailing period.
- Comments capitalized, ending in period, wrapped at 80 columns.
- Semantic commits: `feat:`, `fix:`, `refactor:`, `docs:`, `test:`, `chore:`.
- Conventional commit body: WHY, not WHAT. Diff already shows what.

### 4. Testing requirements

- Every module ships with `*_test.go` files.
- Use `t.TempDir()`, `t.Setenv()`, `testify/require`.
- Table-driven tests where logic varies by input.
- Mock provider endpoints with `httptest.NewServer` — never hit real APIs in unit tests.
- Integration tests (real provider) gated behind `-tags=integration` and require API keys via env.
- Run before commit: `go test ./...` must pass.

### 5. Workflow per module

For each module you build:

1. Read `docs/modules/<name>.md` fully.
2. Read its dependencies' specs (listed in the file).
3. Implement the module under `internal/<name>/`.
4. Write tests for every public function.
5. Run `gofumpt -w .` and `go test ./...`.
6. Commit with a semantic message: `feat(<module>): <one-line summary>`.
7. Update `docs/modules/<name>.md` with a `## Implementation status` section listing what was built and any deviations from spec.

### 6. When in doubt

Open an issue on the repo, or write a `docs/decisions/YYYY-MM-DD-<topic>.md` file describing the question, options considered, and your recommendation. Do not silently deviate from the spec.

## Reading order

1. This file (`AGENTS.md`).
2. `README.md` — public-facing overview.
3. `docs/vision.md` — the why.
4. `docs/architecture.md` — the module map and build order.
5. `docs/modules/<your-first-module>.md` — start building.

## Project layout (target)

```
.
├── AGENTS.md              # This file
├── README.md
├── LICENSE
├── go.mod
├── go.sum
├── main.go                # Entry: calls internal/cmd.Execute()
├── docs/
│   ├── vision.md
│   ├── architecture.md
│   ├── modules/           # One spec per module
│   └── decisions/         # ADRs
├── internal/
│   ├── app/               # Top-level wiring (DB, config, agent, LSP, MCP)
│   ├── cmd/               # Cobra CLI commands (root, run, login, models, sessions, stats)
│   ├── config/            # bharatcode.json loading, validation, provider config
│   ├── llm/               # LLM provider abstraction (Anthropic, OpenAI, DeepSeek, Moonshot, Groq, Together, OpenRouter, Ollama, …)
│   ├── agent/             # Agent loop, prompts, coordinator
│   ├── tools/             # Built-in tools (bash, view, edit, grep, glob, ls, todo, web_fetch, …)
│   ├── mcp/               # MCP client and tool bridge
│   ├── permission/        # Tool permission gating
│   ├── lsp/               # LSP manager and clients
│   ├── session/           # Session CRUD
│   ├── message/           # Message and content types
│   ├── db/                # SQLite schema, sqlc-generated queries, migrations
│   ├── tui/               # Bubble Tea TUI (chat, diff, file tree, completions, dialogs)
│   ├── hooks/             # User shell hook engine
│   ├── shell/             # Bash command execution with background jobs
│   ├── pubsub/            # Internal event bus
│   ├── filetracker/       # Per-session file change tracking
│   ├── ledger/            # INR-aware cost ledger and budget enforcement (BharatCode-exclusive)
│   └── util/              # Small utilities (paths, strings, formatting)
└── scripts/               # Dev scripts (sqlc generate, etc.)
```

## You are not done until

- `go build .` produces a single binary.
- `go test ./...` passes.
- `golangci-lint run` is clean.
- `bharatcode --help` prints usage.
- The module you built has a spec in `docs/modules/<name>.md` with an `Implementation status` section.
- Your commit message is a semantic commit and explains WHY.
