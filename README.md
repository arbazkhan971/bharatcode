# BharatCode

> The Go-native, MIT-licensed, open-weight-first CLI coding agent.
> Built in India for the rupee economy.

BharatCode is a terminal-based AI pair programmer built in Go. It is positioned as the **first-class home for open-weight models** (Kimi K2, DeepSeek V3/R1, Qwen Coder, Llama, plus Anthropic/OpenAI as premium fallbacks), with a cost-conscious philosophy aimed at the rupee economy.

## Status

**Phase 1 — feature parity.** Building a feature-complete TUI coding agent at parity with leading terminal coding agents, but MIT-licensed, with first-class open-weight provider support, and built from scratch.

Implementation is well underway: **19 modules** are implemented under `internal/`, with the test suite passing on darwin (cross-platform CI is pending). Working subsystems today:

- TUI shell (Bubble Tea)
- Agent loop
- 13 built-in tools (view, edit, multiedit, write, ls, glob, grep, bash, web fetch/search, diagnostics, todo, and job control)
- MCP client
- LSP integration
- 10 LLM providers
- SQLite session store with `--continue` resume
- Permission engine
- Lifecycle hooks
- INR cost ledger

**Known limitations (in active development, P0):** the TUI prompt → agent wiring, autonomous `/goal`, `--json` run mode, `AGENTS.md` ingestion, and the Anthropic-native API are not yet complete.

## Quick start

```sh
# Install
go install github.com/arbazkhan971/bharatcode@latest

# Run with a provider
export DEEPSEEK_API_KEY=...
bharatcode

# Or with a local model
bharatcode --provider ollama --model qwen2.5-coder:32b
```

## Documentation

- **[Vision](docs/vision.md)** — what BharatCode is, why it exists, and how it positions against leading terminal coding agents
- **[Architecture](docs/architecture.md)** — module map and dependency graph
- **[Module specs](docs/modules/)** — one document per module, with public interface, dependencies, and acceptance criteria
- **[AGENTS.md](AGENTS.md)** — instructions for AI coding agents (Gemini CLI, Codex, Claude Code) building this project

## Building this project

This project is being built with AI coding agents (Gemini CLI first, then Codex and Claude Code). Every module has a self-contained spec in `docs/modules/` so any agent can pick up any module independently.

If you are an AI agent, start with [AGENTS.md](AGENTS.md).

## License

MIT — see [LICENSE](LICENSE).
