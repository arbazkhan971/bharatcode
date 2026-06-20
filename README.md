# BharatCode

> A terminal-based AI coding agent for Indian developers. **Your code stays in India.**

**🌐 Website: [bharatcode.dev](https://bharatcode.dev)** · [Docs](https://bharatcode.dev/docs) · [Schema](https://bharatcode.dev/schema.json)

<p>
  <a href="https://bharatcode.dev"><img alt="Website" src="https://img.shields.io/badge/website-bharatcode.dev-saffron?color=FF9933"></a>
  <a href="https://github.com/arbazkhan971/bharatcode/releases"><img alt="Latest Release" src="https://img.shields.io/github/release/arbazkhan971/bharatcode"></a>
  <a href="https://github.com/arbazkhan971/bharatcode/actions"><img alt="Build Status" src="https://github.com/arbazkhan971/bharatcode/actions/workflows/build.yml/badge.svg"></a>
  <img alt="Go" src="https://img.shields.io/badge/Go-1.26%2B-00ADD8?logo=go&logoColor=white">
  <img alt="License: MIT" src="https://img.shields.io/badge/License-MIT-green.svg">
</p>

BharatCode is your coding bestie in the terminal. It wires your tools, your code, and
your workflows into the LLM of your choice — and because you can point it at a fully
local model (Ollama, LM Studio), your source never has to leave your machine.

---

## Install

Install with Go:

```sh
go install github.com/arbazkhan971/bharatcode@latest
```

Or build from source:

```sh
git clone https://github.com/arbazkhan971/bharatcode
cd bharatcode
go build -o bharatcode .
```

Pre-built [binaries and packages][releases] are also published for Linux, macOS,
Windows, FreeBSD, OpenBSD, and NetBSD on each release.

[releases]: https://github.com/arbazkhan971/bharatcode/releases

---

## Why BharatCode

- 🇮🇳 **Your code stays in India.** Point BharatCode at a fully local model — Ollama,
  LM Studio, or any self-hosted OpenAI-compatible gateway — and your source code never
  leaves your machine. No mandatory cloud round-trip, no vendor lock-in.
- 🧩 **Bring your own model.** Choose from a wide range of providers, or add your own
  via OpenAI- and Anthropic-compatible APIs. Switch models mid-session without losing
  your context.
- 🪶 **MIT-licensed and single-binary.** BharatCode is open source under MIT and ships
  as one static, dependency-free executable that runs in every terminal you already use.

---

## Features

- **Multi-Model:** choose from a wide range of LLMs, or add your own via OpenAI- or
  Anthropic-compatible APIs. The provider/model catalog is sourced from
  [Catwalk](https://github.com/charmbracelet/catwalk) and kept up to date automatically.
- **Flexible:** switch LLMs mid-session while preserving context.
- **Session-Based:** maintain multiple work sessions and contexts per project, persisted
  in a local SQLite store you can resume at any time.
- **LSP-Enhanced:** BharatCode talks to language servers for real, in-context
  diagnostics and references — just like you do.
- **Extensible via MCP:** add capabilities through Model Context Protocol servers over
  `stdio`, `http`, and `sse` transports, including MCP resources.
- **Agent Skills:** discover and load reusable [Agent Skills](https://agentskills.io)
  from disk, and expose them as commands.
- **Permission-aware:** BharatCode asks before it acts; you allowlist tools you trust, or
  run with `--yolo` when you really mean it.
- **Works Everywhere:** first-class support in every terminal on macOS, Linux, Windows
  (PowerShell and WSL), Android, FreeBSD, OpenBSD, and NetBSD.

### Built-in tools

The agent ships with a practical toolbox out of the box:

- **Files:** `view` · `ls` · `glob` · `grep` · `edit` · `multiedit` · `write`
- **Shell & jobs:** `bash` · `job_output` · `job_kill` (background job control)
- **Web & code search:** `web_fetch` · `web_search` · `download` · `sourcegraph`
- **Language servers:** `lsp_diagnostics` · `lsp_references` · `lsp_restart`
- **Orchestration:** `agent` (sub-agents) · `todos` · `read_mcp_resource` · `list_mcp_resources`

Don't want a tool? Disable it via `options.disabled_tools` in your config.

---

## Quick start

### Install

```sh
go install github.com/arbazkhan971/bharatcode@latest
```

Requires Go 1.26+ (see `go.mod`). BharatCode builds and ships as a single static binary.

### Run with a hosted provider

Grab an API key for your preferred provider and just start BharatCode — you'll be
prompted to enter the key on first run. You can also set it in the environment:

```sh
export ANTHROPIC_API_KEY=...     # or OPENAI_API_KEY, DEEPSEEK_API_KEY, OPENROUTER_API_KEY, GROQ_API_KEY, ...
bharatcode
```

### Run fully local (no API key, code never leaves your machine)

Add a local provider to `bharatcode.json` and BharatCode auto-discovers the models:

```json
{
  "$schema": "https://bharatcode.dev/schema.json",
  "providers": {
    "ollama": {
      "name": "Ollama",
      "base_url": "http://localhost:11434/v1/",
      "type": "ollama"
    }
  }
}
```

Then just run `bharatcode`. Local provider types include `ollama`, `lmstudio`,
`litellm`, and `omlx`.

### A few real commands

```sh
bharatcode                                  # launch the TUI
bharatcode --continue                       # resume your most recent session
bharatcode --yolo                           # skip permission prompts (dangerous)
bharatcode run "fix the failing test in ./pkg/auth"   # one-shot, non-interactive
bharatcode run -q -C "now run the linter"   # quiet, continue the last session
bharatcode logs --follow                    # tail logs in real time
bharatcode update-providers                 # refresh the Catwalk provider/model list
```

---

## Configuration

BharatCode runs great with no configuration. When you want to customize it, drop a
`bharatcode.json` in your project or `$HOME/.config/bharatcode/`, with the JSON schema
wired up for editor autocompletion:

```json
{
  "$schema": "https://bharatcode.dev/schema.json",
  "lsp": {
    "go": { "command": "gopls" },
    "typescript": { "command": "typescript-language-server", "args": ["--stdio"] }
  },
  "mcp": {
    "filesystem": {
      "type": "stdio",
      "command": "node",
      "args": ["/path/to/mcp-server.js"]
    }
  },
  "permissions": {
    "allowed_tools": ["view", "ls", "grep", "edit"]
  }
}
```

BharatCode also respects `.gitignore` and an optional `.bharatcodeignore` so it never
reads files you'd rather keep out of context. See the [docs](https://bharatcode.dev/docs)
for LSP, MCP, hooks, skills, custom providers, and more.

---

## Acknowledgements

BharatCode stands on the excellent Charm libraries: it's built with
[`bubbletea`](https://github.com/charmbracelet/bubbletea) for the TUI runtime,
[`lipgloss`](https://github.com/charmbracelet/lipgloss) for styling,
[`bubbles`](https://github.com/charmbracelet/bubbles) for the components, and
[`glamour`](https://github.com/charmbracelet/glamour) for Markdown rendering. The
provider and model catalog is powered by
[Catwalk](https://github.com/charmbracelet/catwalk).

---

## Contributing

Contributions are welcome — issues, discussion, and PRs all help. Please keep
`go test ./...` green before opening a pull request.

---

## License

MIT — see [LICENSE.md](LICENSE.md).
