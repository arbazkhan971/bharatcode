# BharatCode

> **OpenCode for India** — a Go-native, MIT-licensed, open-weight-first CLI coding agent. Your code stays in India.

**🌐 Website: [bharatcode.dev](https://bharatcode.dev)** · [Docs](https://bharatcode.dev/docs) · [Install](https://bharatcode.dev/docs/installation)

<p>
  <a href="https://bharatcode.dev"><img alt="Website" src="https://img.shields.io/badge/website-bharatcode.dev-saffron?color=FF9933"></a>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.24%2B-00ADD8?logo=go&logoColor=white">
  <img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-green.svg">
  <img alt="CGO-free" src="https://img.shields.io/badge/CGO-free-success">
  <img alt="Status" src="https://img.shields.io/badge/status-active%20development-orange">
  <img alt="PRs Welcome" src="https://img.shields.io/badge/PRs-welcome-brightgreen.svg">
</p>

## Install

```sh
brew install arbazkhan971/tap/bharatcode      # Homebrew (macOS / Linux)
npm install -g bharatcode-cli                 # npm
curl -fsSL https://raw.githubusercontent.com/arbazkhan971/bharatcode/main/install.sh | sh   # script
```

See [all install methods](https://bharatcode.dev/docs/installation) (Windows, go install, source).

BharatCode is a terminal-based AI pair programmer in the same class as Claude Code, OpenCode, and Codex CLI — but built open-weight-first and data-sovereign. It runs locally, talks to whatever model you choose (fully local Ollama / LM Studio, India- and Asia-hosted inference, or frontier APIs), and keeps an INR-aware cost ledger so spend is a number you can see, not a surprise at month-end. Built for teams who need their source code to never leave the country.

---

## Why BharatCode

- 🇮🇳 **Your data stays in India.** Local-first by design. Point BharatCode at a fully local model (Ollama, LM Studio) or India/Asia-hosted inference and your source code never has to leave the country. This is a hard requirement for banks, enterprises, and DPDP-regulated teams — not a checkbox.
- 🧩 **Open-weight models, first-class.** Kimi K2, DeepSeek V3/R1, Qwen Coder and friends are supported at the env-var level — not bolted on as "custom endpoint" hacks. Open weights run **10–20x cheaper** than frontier closed models, and you can host them yourself.
- 💸 **Cost discipline, in rupees.** A `$200/mo` plan is `₹17,000+` — a real constraint at Indian scales. BharatCode ships an INR-aware cost ledger with per-session / per-day / per-month rollups and a budget gate, so cost is visible in the currency you actually pay in.

---

## Features

**Terminal UI**
- Full Bubble Tea TUI: chat with rich Markdown rendering (glamour) and syntax highlighting
- Model / agent / session pickers, dialogs, and `@`-mention fuzzy file completion
- Live INR cost footer — spend is always on screen

**Agent**
- Real agent loop: streams tool calls and results live, with step cap, loop detection, token budgeting, hook firing, and tool-panic recovery
- User-defined named agents with per-agent tool allow-lists
- Autonomous `/goal` mode — bounded iterate-to-goal autonomy
- AGENTS.md / CLAUDE.md project-instruction ingestion

**13 built-in tools**
- `view` · `edit` · `multiedit` · `write` · `ls` · `glob` · `grep` · `bash`
- `web_fetch` · `web_search` · `diagnostics` · `todo` · `job_output` / `job_kill` (background job control)

**Integrations**
- MCP client (stdio / HTTP / SSE)
- LSP integration — in-context diagnostics, correctly handles server-initiated requests
- 10 LLM providers across local and hosted inference

**Workflow & control**
- SQLite-backed sessions with `--continue` resume
- `--profile` config overlays (swap entire presets per task or repo)
- `bharatcode run --json` (NDJSON) for CI and automation; non-JSON `run` prints a `Changed files:` summary after workspace edits
- `bharatcode doctor` health check (config, providers, LSP, and ChatGPT sign-in)
- `BHARATCODE_HEADLESS=1` to force the quiet, non-rendering path for scripts and PTY captures
- Verify-before-done agent: it runs the project's own tests/build/lint and ends each change with an explicit **Verified / Failed / Skipped** status
- Permission engine: ask / allow / deny, with once / session / project / forever scopes and read-only / auto / full approval modes (plus `--yolo`)
- Shell-backed lifecycle hooks (PreToolUse, PostToolUse, SessionStart, …)
- INR cost ledger with budget enforcement

---

## Quick start

### Install

```sh
go install github.com/arbazkhan971/bharatcode@latest
```

Requires Go 1.25+. BharatCode is CGO-free (`CGO_ENABLED=0`), so it cross-compiles cleanly and ships as a single static binary.

### Run with a hosted open-weight provider

```sh
export DEEPSEEK_API_KEY=...        # or MOONSHOT_API_KEY, GROQ_API_KEY, etc.
bharatcode
```

### Run fully local (no API key, code never leaves your machine)

```sh
# with Ollama
bharatcode --provider ollama --model qwen2.5-coder:32b

# with LM Studio
bharatcode --provider lmstudio --model qwen2.5-coder-32b-instruct
```

### A few real flags

```sh
bharatcode --continue                       # resume your most recent session
bharatcode about                            # print what BharatCode is
bharatcode doctor                           # diagnose config / provider / LSP / ChatGPT sign-in
bharatcode doctor --check-provider          # make a tiny live provider request
bharatcode run --json "fix the failing test in ./pkg/auth"   # NDJSON event stream for CI
bharatcode eval --suite codex-parity        # offline Codex-parity benchmark
BHARATCODE_HEADLESS=1 bharatcode           # quiet, non-rendering TUI for PTY/CI captures
```

Every non-JSON `bharatcode run` that edits the workspace finishes by printing a
`Changed files:` block listing each path it created, modified, or deleted — so a CI
step or reviewer can see the blast radius at a glance. The agent also verifies
before it reports: after a code change it runs the project's own test, build, and
lint commands and ends the turn with an explicit **Verified**, **Failed**, or
**Skipped (&lt;reason&gt;)** status rather than claiming success on unverified work.

And inside the TUI, kick off bounded autonomous work:

```
/goal make the auth package tests pass
```

### Sign in with ChatGPT (experimental)

If you have a ChatGPT subscription, you can drive a model with it instead of an API
key. This is experimental and for personal single-account use only:

```sh
bharatcode auth chatgpt            # OAuth (PKCE) sign-in via your browser
bharatcode auth chatgpt --status   # show the signed-in account, plan, and token state
bharatcode auth chatgpt --logout   # remove the stored credentials
```

Once signed in, `bharatcode doctor` reports the ChatGPT subscription status alongside
the rest of your environment.

---

## Provider matrix

Ten providers, configured by environment variable. Local providers need no key — your code never leaves your machine.

| Provider | Env var | Open-weight | Notes |
|---|---|:---:|---|
| **Ollama** | _(none — local)_ | ✅ | Fully local; runs on your hardware |
| **LM Studio** | _(none — local)_ | ✅ | Fully local; runs on your hardware |
| **DeepSeek** | `DEEPSEEK_API_KEY` | ✅ | DeepSeek V3 / R1 |
| **Moonshot / Kimi** | `MOONSHOT_API_KEY` | ✅ | Kimi K2 |
| **Groq** | `GROQ_API_KEY` | ✅ | Llama, Qwen Coder (fast inference) |
| **Together** | `TOGETHER_API_KEY` | ✅ | Open-weight model catalog |
| **Fireworks** | `FIREWORKS_API_KEY` | ✅ | Open-weight model catalog |
| **OpenRouter** | `OPENROUTER_API_KEY` | ➖ | Aggregator — open and closed models |
| **OpenAI** | `OPENAI_API_KEY` | ❌ | Frontier closed model |
| **Anthropic** | `ANTHROPIC_API_KEY` | ❌ | Via the OpenAI-compatible path today; native Messages API on the roadmap |

Any OpenAI-compatible endpoint also works via a custom `base_url`, so self-hosted and regional inference gateways drop straight in.

---

## Architecture

BharatCode is decomposed into **19 modules** under `internal/`, arranged in five strict layers from foundation to interface (`util` / `db` / `pubsub` → core data → capabilities → agent → interface). Boundaries are enforced: each module imports only its declared dependencies, the dependency graph is a DAG, and every module is independently testable with `go test ./internal/<name>/...`. The whole binary is **CGO-free** (pure-Go SQLite via `modernc.org/sqlite`), so it builds and ships as one static executable on any platform.

- 📐 [Architecture & module map](docs/architecture.md)
- 🗺️ [Roadmap](docs/roadmap.md)
- 🎯 [Vision](docs/vision.md)

---

## How it compares

BharatCode plays in the same space as the leading terminal coding agents. The honest differentiation is openness and data sovereignty:

| | **BharatCode** | Claude Code | OpenCode | Codex CLI |
|---|:---:|:---:|:---:|:---:|
| Language | Go | (closed) | TypeScript | Rust |
| License | **MIT** | Proprietary | MIT | Apache-2.0 |
| Open-weight-first | **✅** | ❌ | Multi-provider | ❌ |
| Data residency / local-first | **✅** | ❌ | Local-capable | ❌ |
| Cost-awareness (INR ledger) | **✅** | ❌ | ❌ | ❌ |
| Provider lock-in | None | Anthropic only | None | OpenAI only |

OpenCode is the closest peer — MIT and multi-provider. BharatCode's distinct bet is to treat open-weight models and India/Asia data residency as the default, with cost surfaced in rupees.

---

## Status

**Active development.** The core is working end to end: TUI, the agent loop, all 13 built-in tools, MCP, LSP, the 10 providers, SQLite sessions with resume, the permission engine, lifecycle hooks, and the INR ledger. The project carries a comprehensive test suite (**350+ tests**) and targets Go 1.25+.

On the roadmap: native Anthropic Messages API, multimodal image input, session fork / share, and an OS-level sandbox (Seatbelt / Landlock / bwrap). See the [roadmap](docs/roadmap.md) for the full plan and priorities.

---

## Contributing

Contributions are welcome — issues, discussion, and PRs all help. If you're picking up a module, each one ships with a self-contained spec under [`docs/modules/`](docs/modules/), and contributor conventions (locked tech stack, testing standards, coding rules) live in [`AGENTS.md`](AGENTS.md). Tests should run offline; please keep `go test ./...` green and avoid introducing CGO.

---

## Acknowledgements

Built in Go with the excellent [`charmbracelet/bubbletea`](https://github.com/charmbracelet/bubbletea), [`charmbracelet/lipgloss`](https://github.com/charmbracelet/lipgloss), and [`charmbracelet/bubbles`](https://github.com/charmbracelet/bubbles) for the TUI, and [`glamour`](https://github.com/charmbracelet/glamour) for Markdown rendering.

---

## License

MIT — see [LICENSE](LICENSE).
