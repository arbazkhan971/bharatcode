# BharatCode Vision

## One-line pitch

**The Go-native, MIT-licensed, open-weight-first CLI coding agent for the rupee economy.**

## Why BharatCode exists

The state of CLI coding agents in May 2026:

| Tool | Lang | License | Open weights first-class? |
|---|---|---|---|
| Claude Code | (closed) | Proprietary | No — Anthropic only |
| Plandex | Go | MIT | No — Anthropic/OpenAI/Google/OpenRouter only; cloud winding down |
| OpenCode (sst) | TypeScript | MIT | Yes (75+ providers via Models.dev) |
| Aider | Python | Apache-2.0 | Yes (any LLM) — but no agent loop, no MCP |
| Cline | TypeScript | Apache-2.0 | Yes — but VS Code-origin |
| Gemini CLI | TypeScript | Apache-2.0 | No — Gemini only |

Three gaps exist in this market:

1. **No MIT-licensed Go-native CLI agent.** Plandex is MIT but stalled (last commit Oct 2025); the only other active Go agent ships under a restrictive source-available license. The Go niche has no actively maintained, fully open player.
2. **No CLI agent treats open-weight providers as first-class citizens.** Kimi K2, DeepSeek V3/R1, Qwen Coder are the cost-performance Pareto frontier in 2026 — but every existing CLI treats them as "custom endpoint" hacks. They deserve env-var promotion, model-pack curation, and first-class routing.
3. **No CLI agent is built with cost discipline as a core design principle.** The #1 user complaint across HN, Reddit, and GitHub issues is unpredictable LLM spend. Claude Code's 5-hour quota burning in 19 minutes (March 2026 incident) is one of dozens of stories. Cost-conscious users have no home.

BharatCode is the answer to all three.

## What BharatCode is

A terminal-based AI coding assistant in Go, achieving comprehensive terminal-agent surface area in Phase 1:

- Full TUI (Bubble Tea v2): chat, diff view, file tree, completions, dialogs
- Agent loop with built-in tools: bash, view, edit, multiedit, write, grep, glob, ls, todo, web_fetch, web_search, diagnostics, job_output, job_kill
- Multi-provider LLM support with a clean, internally-owned provider abstraction
- Persistent sessions backed by SQLite (sqlc-generated, CGO-free)
- Permission-gated tool calls with `--yolo` escape hatch
- MCP client support (stdio, HTTP, SSE transports)
- LSP integration for in-context diagnostics
- User-defined hooks (PreToolUse, PostToolUse, etc.) matching the de facto standard
- Skills framework (directory-loaded reusable instructions)
- Multi-agent coordination ("coder", "task", etc. named agents)

## Design principles

BharatCode follows a proven layered module decomposition, with five deliberate design choices:

### 1. License: MIT

BharatCode is plain MIT. Source-available licenses common among competing agents are incompatible with many enterprise procurement processes and prevent downstream commercial forks. Plain MIT is the same license Plandex chose. This is a real wedge for enterprises and a real benefit for the open-source community.

### 2. First-class open-weight providers

Existing CLI agents offer first-class env-var support (`ANTHROPIC_API_KEY`, `OPENAI_API_KEY`, `GROQ_API_KEY`, `DEEPSEEK_API_KEY`) for premium providers and Groq/DeepSeek. Moonshot (Kimi), Together, Fireworks, OpenRouter are reachable only via custom-endpoint config.

BharatCode promotes the full open-weight matrix to first-class status:

- `MOONSHOT_API_KEY` → Kimi K2 (cheap, long context)
- `DEEPSEEK_API_KEY` → DeepSeek V3 and R1 (best price/perf for code)
- `TOGETHER_API_KEY` → Together AI (Llama, Qwen, Mixtral)
- `FIREWORKS_API_KEY` → Fireworks (Llama, Qwen, Mixtral)
- `GROQ_API_KEY` → Groq (ultra-fast Llama/Qwen)
- `OPENROUTER_API_KEY` → OpenRouter (200+ models meta-provider)
- `OLLAMA_HOST` → Local Ollama
- `LMSTUDIO_HOST` → Local LM Studio
- `ANTHROPIC_API_KEY` → Claude (premium fallback)
- `OPENAI_API_KEY` → GPT (premium fallback)

Each provider ships with a **curated model pack**: which models exist, their context window, their input/output pricing (in USD and INR), their tool-call dialect, their image support. The pack is auto-updated by a `bharatcode update-providers` command querying [models.dev](https://models.dev) or the provider's own discovery endpoint.

### 3. INR-aware cost ledger

Every TUI session shows a persistent footer:

```
session 4b2a · in 12,453 · out 3,201 · cost $0.018 · ₹1.51 · budget ₹500/mo (0.3% used)
```

Cost is tracked per session, per day, per month, in both USD and INR (configurable currency). A configurable budget cap (`max_inr_per_session`, `max_inr_per_day`, `max_inr_per_month`) triggers a confirmation dialog before exceeding. This is a small feature in code (~one module, ~500 LOC) but a meaningful product differentiator.

### 4. Internally-owned abstractions

BharatCode uses the public Go ecosystem (Bubble Tea, Cobra, sqlc, mcp-go) and owns its LLM provider abstraction internally rather than depending on an external LLM-abstraction SDK. Owning the abstraction keeps the locked stack self-contained and avoids coupling the agent to a third-party provider layer.

### 5. Sovereign-AI adapter stubs (deferred, but documented)

The Indian AI ecosystem (Sarvam, Krutrim, BharatGPT, Hanooman) has no production-grade coding-tuned model as of May 2026. We do not promise something we cannot deliver. We do, however, document **adapter stubs** in `internal/llm/sovereign/` (one file per model family) that can be enabled the day each provider ships a coding-tuned variant. This is a strategic placeholder, not a current feature.

## What BharatCode is not

- Not a VS Code extension (use Cline for that).
- Not a self-hosted Copilot replacement (use Tabby for that).
- Not a full-stack autonomous SWE platform (use OpenHands for that).
- Not a hosted SaaS — fully local CLI, you bring your own keys.
- Not a Claude Code clone — different license, different model story, different cost philosophy.

## Audience

- Indian developers paying out-of-pocket for AI tooling (rupee-sensitive).
- Open-weight enthusiasts who want production-grade Kimi/DeepSeek/Qwen support, not a custom-endpoint workaround.
- Go developers who want to contribute to a CLI agent without learning Rust or TypeScript (Cline/OpenCode).
- Enterprises that want a MIT-licensed CLI agent they can fork.

## Phase 1 scope

**Feature parity with leading terminal coding agents.** A comprehensive terminal-agent feature set, implemented in BharatCode. 18 modules. Realistic timeline if built by AI coding agents (Gemini CLI, Claude Code) with human review: **3-4 months wall-clock, ~$500-2000 in API spend** depending on how much rework happens.

If the AI-agent-driven build proves faster or slower, we update this number honestly. We do not promise 90 days when we have no evidence for it.

## Phase 2 (future, post-parity)

Not committed to. Examples of what might come next:

- INR-aware cost optimizer that auto-routes prompts by difficulty (easy → Groq/Kimi, hard → Claude/GPT)
- Headless `--json` mode for CI usage (parity with Gemini CLI's stream-JSON)
- Plan/Act toggle (read-only "plan" agent followed by approval-gated "act" agent), borrowed from OpenCode and Cline
- Cumulative diff sandbox (borrowed from Plandex)
- `.bharatrules` per-project policy file (borrowed from Cline's `.clinerules`)
- Sovereign-AI adapter enablement when Sarvam/Krutrim/BharatGPT ship coding models

Phase 2 is explicitly out of scope for this document. Ship parity first, then earn the right to invent.

## Non-goals

- Not solving the model-training problem. We are a client, not an LLM developer.
- Not building MCP. We consume the protocol via `mcp-go`.
- Not maintaining a model registry. We rely on [models.dev](https://models.dev) and provider discovery.
- Not building a desktop or web app. CLI only. (TUI is still terminal.)

## Success criteria for Phase 1

BharatCode is "done" with Phase 1 when:

1. A new user can `go install`, set `DEEPSEEK_API_KEY`, run `bharatcode`, and have a productive coding session within 5 minutes.
2. Every planned terminal-agent feature has a matching BharatCode module with passing tests.
3. The INR cost ledger displays in the TUI and enforces budget caps.
4. The 18 module specs in `docs/modules/` all have an "Implementation status: complete" section.
5. Documentation is sufficient for an AI coding agent to extend the system without human guidance for routine changes.

That is the bar. Anything beyond is Phase 2.
