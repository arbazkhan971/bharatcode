# BharatCode Feature Roadmap

> Synthesized from a competitor scan (Pi coding agent) and a
> module-by-module acceptance-criteria audit of the current codebase.
> Goal: a prioritized, grounded plan that fixes UX-blocking gaps first and adopts
> the highest-leverage peer features that can be built and tested **without any
> external provider key or network access**.

---

## 1. Current state

BharatCode is far more built than its own README admits. Across 19 internal
modules there are **283 test functions** (the brief's "351 tests" likely counts table
sub-cases / parametrized rows), and the following capabilities already exist and
pass their core tests — with one exception, a hardware-sensitive `db` benchmark
assert that fails on normal machines (see P0 fast-follows):

- **TUI** (Bubble Tea) with footer, dialogs, model/agent/session pickers, `@`-mention fuzzy file completion.
- **Agent loop** with step cap, loop detection, drop-oldest token budgeting, hook firing, user-defined agents with tool allow-lists, and a built-in read-only `task` subagent.
- **13 built-in tools** (read/write/edit/multiedit, bash, grep/glob/ls, todo, diagnostics, web_search, web_fetch, job_output, job_kill).
- **MCP client** (stdio/http/sse, discovery, name-prefixed tool bridging, reconnect-with-backoff).
- **LSP client** (gopls-class diagnostics).
- **10 LLM providers** (Anthropic config stub aside: OpenAI/DeepSeek/Moonshot/Groq/Together/Fireworks/OpenRouter/Ollama/LM Studio via an `openai_compatible` path) with custom `base_url` and env-var keys.
- **SQLite sessions** with full CRUD, tree-capable schema (`messages.ParentID`), and `--continue` resume (`session.Latest()`).
- **Permission engine** (ask / allow-list / deny / `--yolo`, with once/session/project/forever scopes).
- **Shell** background jobs, **shell-backed lifecycle hooks**, and an **INR cost ledger** with per-entry rupee conversion and a budget gate.

### Audit verdict

| Severity | Modules |
|---|---|
| Complete | hooks |
| Minor gaps (mostly test-evidence / lint-unverifiable) | util, pubsub, config, message, session, filetracker, ledger, lsp, shell, permission |
| Major gaps (real missing behavior) | **db, llm, mcp, tools, agent, tui, cmd, app** |

Most "minor gaps" are untestable lint criteria (golangci-lint is not installed in
the audit environment) or under-asserting tests where the implementation is
actually correct. The **major gaps** are where real UX or correctness is missing.

### What is incomplete (the load-bearing gaps)

1. **The prompt never reaches the agent.** `internal/tui/tui.go:280` `submitInput` echoes the user's text straight back into the chat via `chat.Stream`/`FinishStream` and **never invokes `deps.Agent`**. `deps.Agent` is used only for a nil-check and `Interrupt()`. As shipped, the TUI cannot actually run the agent from the prompt line. This is the single biggest blocker.
2. **`/goal` is a stored string, not a mode.** `handleGoalCommand` (tui.go:328) sets/clears `m.goal`; nothing reads it to drive autonomous iteration.
3. **Most slash commands are placeholders.** `/sessions`, `/save`, `/model` (no side effect), settings, and the Ctrl+D diff dialog all push stub text.
4. **`bharatcode run` has no machine-readable output.** Only the last assistant text is printed; `--json`/NDJSON was explicitly deferred (cmd.md).
5. **No AGENTS.md ingestion.** The repo ships an `AGENTS.md`, but no code path loads it (or `CLAUDE.md`) into the system prompt; prompt assembly is template + env only.
6. **`db` package test suite fails on normal hardware.** `TestBenchmarkGetSessionByID` (open_test.go:368) hard-asserts mean read latency `< 200µs`; it measured 746µs on the audit runner, so `go test ./internal/db/...` fails. This blocks CI and contradicts the "all tests pass" framing.
7. **`llm` retry/backoff and Anthropic native API** are unimplemented; Anthropic is a config-only `ErrNotYetSupported` stub and sovereign providers (Sarvam/Krutrim/BharatGPT) do not exist.
8. **README is factually wrong:** it states "Implementation has not begun." Nineteen modules and 283 test functions exist.

> **Audit caveats found during verification (the audit was partly stale):**
> `sqlc.yaml` now **does** exist at repo root (audit said missing) — do not rebuild it.
> `.golangci.yml` and `scripts/sqlc-generate.sh` are **genuinely absent**.
> **Session resume already works** via `--continue` / `session.Latest()` — do not rebuild it; build fork/branch instead.

---

## 2. Features to adopt from Pi CLI / peers

Effort and "has it" reflect the audit. Priority is BharatCode's, not the peer's.

| Feature | What it gives users | BharatCode has it? | Target module | Effort | Priority |
|---|---|---|---|---|---|
| **Prompt → agent run wiring** (not a peer feature; a parity baseline) | The TUI actually runs the agent from the input line | **No** (stub echo) | internal/tui + internal/agent | medium | **P0** |
| AGENTS.md project instructions (nested merge) | Version-controlled house conventions auto-loaded; no re-explaining | No | internal/agent + internal/config | low | **P0** |
| `exec`/non-interactive `--json` (NDJSON events, `--output-last-message`) | Clean drop into scripts/CI; machine-parseable | Partial (last-text only) | internal/cmd | medium | **P0** |
| Approval modes coupled to autonomy (read-only / auto / full) + live `/permissions` | Dial autonomy per task/repo, mid-session, without restart | Partial (scopes only) | internal/permission + internal/tui | medium | **P0** |
| Profiles (named config overlays, `--profile`) | Switch whole presets (locked-down review vs full-access scripting) | No | internal/config | low | **P0** |
| Rich slash-command framework (`/diff`, `/status`, `/compact`, `/init`, `/review`) + custom Markdown prompts with `{{var}}` | Common workflows one keystroke away; teams codify recipes as plain files | No (placeholders) | internal/tui + internal/config | medium | **P0/P1** |
| Autonomous `/goal` mode (drive iteration toward a goal) | Hands-off progress on a stated objective | No (stored string) | internal/agent + internal/tui | medium | **P0** (after wiring) |
| db timing-assert fix (CI unblock) | `go test ./internal/db/...` passes on normal hardware | No (fails) | internal/db | low | **P0** |
| README/status reconciliation | Truthful project status | No (false) | docs/README | trivial | **P0** |
| Session fork / branch (`/fork`, `/clone`, tree nav) | Explore alternate solution paths from a checkpoint without losing context | Partial (schema only) | internal/session + internal/tui | medium | **P1** |
| Manual `/compact` with custom instructions | Keep long sessions in-window on the user's terms | Partial (drop-oldest only) | internal/agent | medium | **P1** |
| Steering / queued follow-up messages mid-turn | Course-correct a long run without cancelling | No | internal/tui + internal/agent | medium | **P1** |
| `/share` to gist + `/export` to HTML transcript | Hand a teammate a rendered record of what the agent did | No | internal/session + internal/cmd | low | **P1** |
| Skills / Agent Skills standard | Drop-in, model-discoverable capability packages | No | internal/agent + internal/config | medium | **P1** |
| `doctor` diagnostics + shell completions | One command surfaces misconfiguration | No | internal/cmd | low | **P1** |
| Multimodal image input (`-i`, paste) | Hand the agent a screenshot/mock instead of describing | No (rejected as unsupported) | internal/llm + internal/tui | medium | **P2** |
| OS-level sandbox (Seatbelt / Landlock / bwrap) | Real technical boundary on FS/network, not just a prompt | No | new sandbox module | high | **P2** |
| Subscription/OAuth login (Claude Pro, ChatGPT Plus, Copilot) | Use existing paid subs instead of metered API | No | internal/cmd + internal/llm | high | **P2** |
| In-process plugin/extension runtime | Users register tools/UI without a vendor release | Partial (shell hooks only) | internal/hooks/agent/tui | high | **P2** |
| Pi-style package manager (install/remove/update bundles) | Share customizations like dependencies | Partial (provider packs only) | internal/cmd | high | **P2** |
| `inline !bash / !!bash` from the prompt | Pull a command's output into context in one keystroke | Partial (`@` done, `!` missing) | internal/tui | low | **P1** |
| RPC / embeddable SDK mode | Wire the agent into editors/bots/other languages | No | internal/app + internal/cmd | medium | **P2** |
| Remote TUI over WebSocket | Drive a remote/cloud session from a local terminal | No | internal/tui + internal/app | high | **P2** |
| Image generation/editing in-CLI | Produce asset placeholders without leaving the terminal | No | internal/tools + internal/llm | high | **P2** |

**Already have it — do not rebuild** (table as "done", not roadmap): MCP client,
built-in web search/fetch, custom model providers / OpenAI-compatible endpoints,
multi-provider registry, subagents/parallel tasks, notify-class hooks, `@`-mention
fuzzy file completion, and **session resume (`--continue`)**.

---

## 3. Priority roadmap

### P0 — highest leverage, buildable and testable now (no API key / network)

Every P0 item is verifiable offline. The agent package already ships a
`scriptProvider` (loop_test.go) and the cmd tests use `httptest` stubs, so
prompt→run wiring, autonomous `/goal`, and `--json` exec can all be driven by a
fake provider with zero credentials.

1. **Wire `submitInput` → `deps.Agent.Run`.** *(internal/tui + internal/agent — medium)*
   The product cannot run the agent from the prompt today (tui.go:280 echoes input). Stream agent events into the chat view, route `Ctrl+C` to `Interrupt()`. **Rationale:** every other feature is cosmetic until this exists. **Test offline:** `scriptProvider` + `tea.WithoutRenderer`, assert tool calls and final text reach the chat model.

2. **AGENTS.md nested-merge ingestion.** *(internal/agent + internal/config — low)*
   Load `~/.bharatcode/AGENTS.md` then repo-root→cwd, concatenate root-first (nested overrides), cap at a byte budget, inject into the system prompt. Add `/init` to scaffold one. **Rationale:** persistent house conventions; the repo already ships an `AGENTS.md` that is currently ignored. **Test offline:** pure file load + golden system-prompt assembly; no provider needed.

3. **`--json` / NDJSON for `bharatcode run` (+ `--output-last-message`).** *(internal/cmd — medium)*
   Emit newline-delimited JSON events and a final-message writer. **Rationale:** unblocks CI/automation; the run plumbing exists and only serialization is missing. **Test offline:** `httptest` stub provider + parse the NDJSON stream.

4. **Approval modes (read-only / auto / full) + live `/permissions` toggle, and fix two permission bugs.** *(internal/permission + internal/tui — medium)*
   Add the autonomy trichotomy on top of existing scopes, switchable mid-session. While here, fix the audited correctness bugs: **deny-stickiness** (a session Allow currently overrides a project Deny, permission.go:140) and **prefix over-match** (`matchPattern` uses `HasPrefix`, so `bash:echo` matches `bash:echox`, permission.go:343). **Rationale:** safety/autonomy control is core UX and the two bugs silently broaden permissions. **Test offline:** table-driven `Check` tests; no network. *(Note: OS-level sandboxing is explicitly **out** of P0 — high effort, deferred to P2.)*

5. **Config profiles / `--profile`.** *(internal/config — low)*
   Named overlays (e.g. `~/.bharatcode/<profile>.json`) on top of the existing global+project merge, selected by flag. **Rationale:** one flag swaps an entire preset (locked-down review vs full-access scripting); cheap on the existing merge machinery. **Test offline:** overlay precedence unit tests.

6. **Autonomous `/goal` mode.** *(internal/agent + internal/tui — medium; sequenced after #1)*
   Make `/goal <text>` drive bounded autonomous iteration (run → observe → continue until goal met or step cap), surfacing progress in the TUI, instead of merely storing a string. **Rationale:** flagship differentiator and on the brief's example list; depends on #1's wiring. **Test offline:** `scriptProvider` returning a deterministic tool sequence; assert iteration stops at goal/cap.

**P0 fast-follows (low effort, bundle with the above):**
- **db CI unblock** *(internal/db — low):* relax/remove the hard `mean < 200µs` assert (open_test.go:368) — make it a logged benchmark, not a pass/fail gate — so `go test ./internal/db/...` is green on normal hardware.
- **README / status reconciliation** *(docs — trivial):* see §5.
- **Add `.golangci.yml`** (with sqlc-generated dirs in `skip-files`) and **`scripts/sqlc-generate.sh`** — both still genuinely absent; their absence is why ~8 modules' lint criteria are "unverifiable."

### P1 — high value, next after the P0 foundation

- **Rich slash-command framework + custom Markdown prompts** *(tui + config, medium):* turn the placeholder commands into real `/diff` (wire to edit/write tool results), `/status`, `/compact`, and a Markdown-prompt registry with `{{var}}` interpolation invokable as `/name`. Offline-testable.
- **Session fork / branch** *(session + tui, medium):* the schema already carries `ParentID`; add `/fork`, `/clone`, tree navigation, and `--fork` resume. Offline-testable. **(Resume itself is already done — do not rebuild.)**
- **Manual `/compact`** *(agent, medium):* implement the reserved `Compactor` seam (summarize-and-replace, preserving the original) on top of today's drop-oldest. Offline-testable with a stub summarizer.
- **Steering / queued follow-up messages** *(tui + agent, medium):* queue a message delivered after the current turn instead of cancel-and-restart.
- **`/share` (gist) + `/export` (HTML)** *(session + cmd, low):* render a transcript; gist upload needs `gh`/network so test the HTML export offline and gate the share.
- **`inline !bash / !!bash`** *(tui, low):* run a shell command and feed (or not) its output into the prompt.
- **Skills (Agent Skills standard)** *(agent + config, medium):* model-discoverable capability packages that keep the base prompt lean.
- **`doctor` diagnostics + shell completions** *(cmd, low):* one command to surface misconfiguration.
- **`llm` retry/backoff + Anthropic native Messages API** *(llm):* implement the documented exponential backoff / `Retry-After` contract and the Anthropic dialect. *(Lands here, not P0, because end-to-end verification wants real provider behavior; the backoff logic itself is unit-testable with a mock clock.)*

### P2 — strategic, higher effort or needs network / real providers

- **OS-level sandbox** (Seatbelt / Landlock / bwrap) — real FS/network boundary; new module, high effort.
- **Multimodal image input** — needs a vision-capable provider to validate end to end.
- **Subscription/OAuth login** (Claude Pro / ChatGPT Plus / Copilot) — OAuth flows + per-provider transports; needs live auth.
- **In-process plugin/extension runtime** — large architectural addition beyond shell hooks.
- **Pi-style package manager** for extensions/skills/prompts/themes.
- **RPC mode / embeddable SDK** — the agent core is already decoupled from Bubble Tea, but no transport exists.
- **Remote TUI over WebSocket**, **image generation/editing in-CLI** — both high effort, niche.
- **Sovereign providers** (Sarvam / Krutrim / BharatGPT) — strategically on-brand for the rupee economy, but needs the provider APIs and keys to verify.

---

## 4. Why P0 is shaped this way

- **(a) Fixes audit gaps that block core UX:** items 1 (prompt→agent), 6 (autonomous `/goal`), and the db CI unblock + README fix directly remove the things that make the product non-functional or untrustworthy today.
- **(b) High-impact, zero-credential features:** AGENTS.md, `--json` exec, approval modes, and profiles are all implementable and **testable with a fake/scripted provider or pure file I/O** — no live key, no network. That is precisely why they are P0 and why anything requiring real provider calls (Anthropic native API, multimodal, OAuth login, sovereign providers) is deliberately pushed to P1/P2.

---

## 5. README / status reconciliation (P0 doc fix)

`README.md` currently states:

> This repository currently contains the vision document and module-by-module
> specifications. **Implementation has not begun.**

This is false. Replace the Status section to reflect reality:

- 19 modules implemented under `internal/`; **283 test functions** passing on darwin (cross-platform CI pending), with one known exception: a hardware-sensitive `db` benchmark assert that fails on slower machines — addressed in P0.
- Working: TUI shell, agent loop, 13 tools, MCP, LSP, 10 LLM providers, SQLite sessions + `--continue` resume, permissions, lifecycle hooks, INR cost ledger.
- **Known limitations to state honestly:** the TUI prompt does not yet drive the agent (in progress, P0 #1); `/goal` stores a string rather than driving autonomy; Anthropic uses the OpenAI-compatible path (native Messages API pending); `bharatcode run` has no `--json` yet; AGENTS.md is not yet ingested.
- Update the quick-start to reflect that `bharatcode` runs today (with the P0 wiring caveat), not "when implemented."

Keeping the README honest is itself a P0 deliverable: contributors and users are
currently told nothing is built, which actively misrepresents a substantial,
mostly-working codebase.