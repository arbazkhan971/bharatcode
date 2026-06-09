# BharatCode Codex-Parity Plan

**Goal:** make BharatCode's interactive TUI and conversation experience feel comparable to Codex CLI for normal day-to-day coding.

**Current baseline:** BharatCode can create working apps and use ChatGPT subscription auth, but the TUI and conversation experience are not yet Codex-like. Recent fixes improved ChatGPT visibility in `doctor`, headless renderer gating, changed-file summaries, and tool-result fallback output. Those are supporting fixes, not the end state.

**Target bar:** a user should be able to launch `bharatcode --yolo`, type naturally, watch understandable progress, and receive a concise engineering-style response with changed files, verification status, and next steps. The conversation should be clean enough that it feels like a real coding partner, not a raw terminal renderer.

## End Objective

The primary deliverable is not feature count. It is conversation quality:

- The TUI should accept input reliably, render cleanly, and avoid distracting redraw noise.
- The conversation transcript should be readable, stable, and scannable.
- Tool activity should be visible but not verbose.
- Final responses should sound like Codex CLI: direct, concrete, file-aware, and verification-aware.
- The assistant should not claim work is done until it has verified or clearly explained why verification was skipped.
- Headless/captured sessions should produce clean logs for debugging and automation.
- Setup/auth problems should appear as actionable conversation states, not confusing background failures.

All tasks below should be judged against this end objective.

## Current Gaps

These are the specific reasons BharatCode does not feel like Codex CLI yet:

- Normal interactive TUI output is still too renderer-driven; captured PTY output can be dominated by redraw frames.
- Prompt submission through PTY automation needed `\r`; plain `\n` was not reliable in testing.
- The assistant can create files but does not reliably verify them before final response.
- Final TUI responses can be too model-dependent and may omit exact paths or verification status.
- Tool activity is visible, but the transcript does not yet feel like a clean engineering conversation.
- Headless mode is cleaner now, but normal interactive mode still needs polish.
- Small tasks use too much context and too many input tokens.
- There is no Codex-parity eval that grades conversation quality, not just task completion.

The plan below exists to close these gaps.

## Phase 1: Setup And Auth Clarity

### Task 1: Expand Doctor Provider Status
**Files:** `internal/cmd/doctor.go`, `internal/cmd/doctor_test.go`

**What to do:**
- Keep the existing ChatGPT subscription line.
- Add active agent/model/provider status for the default `coder` agent.
- Show whether the active provider is usable now: env key set, local endpoint reachable, or ChatGPT auth present.
- Do not print secrets.

**Done when:**
- `bharatcode doctor` shows `Active model`, `Active provider`, and `ChatGPT subscription`.
- Missing auth gives a specific command hint.
- Tests cover signed-in, missing-auth, env-key, and local-provider cases.

**Verify:**
```sh
rtk go test ./internal/cmd -run 'TestDoctor|TestRunDoctor'
rtk bharatcode doctor
```

### Task 2: Add Provider Smoke Check
**Files:** `internal/cmd/doctor.go`, `internal/llm/*_test.go`

**What to do:**
- Add optional `bharatcode doctor --check-provider`.
- Make one tiny non-streaming or streamed test request with a short timeout.
- Report success/failure without consuming this check during normal `doctor`.

**Done when:**
- Default `doctor` stays offline-fast.
- `doctor --check-provider` proves the configured model can answer.
- Provider auth failures are actionable.

## Phase 2: Cleaner Interactive UX

### Task 3: Keep Headless Renderer Quiet
**Files:** `internal/tui/tui.go`, `internal/tui/tui_test.go`

**What to do:**
- Preserve the current `BHARATCODE_HEADLESS`, `CI`, `TERM=dumb`, empty `TERM` quiet path.
- Document `BHARATCODE_HEADLESS=1` in CLI docs.
- Add a small smoke test or script that asserts captured output stays below a sane byte threshold.

**Done when:**
- Captured headless TUI no longer emits repeated redraw frames.
- Normal terminal rendering remains unchanged.

**Verify:**
```sh
rtk go test ./internal/tui/... -run TestShouldDisableRenderer
BHARATCODE_HEADLESS=1 rtk bharatcode --yolo --project-dir /tmp/bc-smoke
```

### Task 4: Reduce Normal TUI Redraw Churn
**Files:** `internal/tui/tui.go`, `internal/tui/statusbar/statusbar.go`, `internal/tui/*_test.go`

**What to do:**
- Audit spinner tick frequency and status updates.
- Avoid rerendering unchanged frames.
- Cap long-running spinner/status output in captured PTYs.
- Preserve smooth behavior in real terminals.

**Done when:**
- Interactive capture output is materially smaller without `BHARATCODE_HEADLESS`.
- Tool progress remains visible.
- Tests cover unchanged status frames.

### Task 5: Make Input Submission Robust
**Files:** `internal/tui/input.go`, `internal/tui/commands_test.go`

**What to do:**
- Ensure both LF (`\n`) and CR (`\r`) submit consistently in PTY automation.
- Keep multiline input behavior intact.
- Add tests for Enter variants.

**Done when:**
- Driving the TUI through a PTY works with either newline form.

## Phase 3: Better Completion Output

### Task 6: Preserve Changed File Summaries
**Files:** `internal/cmd/run.go`, `internal/cmd/run_test.go`

**What to do:**
- Keep the current deterministic `Changed files:` block for non-JSON `run`.
- Add operation labels when useful: created, modified, deleted.
- Keep paths absolute.
- Do not alter NDJSON format.

**Done when:**
- `bharatcode run` prints concise changed-file output for file-writing tasks.
- Repeated writes list one path once.

### Task 7: Improve TUI Final Summary
**Files:** `internal/tui/agentrun.go`, `internal/tui/chat/*`, `internal/tui/agentrun_test.go`

**What to do:**
- When a turn ends, append a compact local summary if files changed and the assistant did not mention them.
- Include exact paths and verification status.
- Avoid duplicating model prose when it already contains the same path.

**Done when:**
- TUI users see exact output paths without needing logs.
- Empty final assistant messages still produce useful completion text.

## Phase 4: Automatic Verification

### Task 8: Define Verification Policy
**Files:** `internal/agent/templates/coder.md.tpl`, `internal/config/config.go`, `docs/modules/agent.md`

**What to do:**
- Formalize when verification is required:
  - write/edit/multiedit/patch/rename changed files
  - generated frontend artifacts
  - package manifests touched
  - tests/build files touched
- Define allowed skip reasons:
  - no test command exists
  - external dependency unavailable
  - user explicitly asked not to run tests

**Done when:**
- Agent policy is explicit and testable.
- Final response always says verified, failed, or skipped with reason.

### Task 9: Implement Verification Command Discovery
**Files:** `internal/agent/verify.go`, `internal/agent/verify_test.go`, `internal/tools/testparse.go`

**What to do:**
- Detect likely commands from repo files:
  - Go: `go test ./...`
  - Node: package scripts `test`, `build`, `lint`
  - Python: `pytest`, import smoke if no pytest
  - Rust: `cargo test`
  - static HTML: browser/DOM smoke or file-open sanity check
- Return ordered candidates with confidence and reason.

**Done when:**
- Empty single-file HTML apps get a browser/file smoke check.
- Existing projects use native test commands.

### Task 10: Wire Post-Write Verification
**Files:** `internal/agent/loop.go`, `internal/tools/tools.go`, `internal/hooks/hooks.go`, `internal/agent/*_test.go`

**What to do:**
- Use existing `VerifyNeeded` and hook plumbing.
- After write-class tools succeed, run verification before final answer when policy requires it.
- Feed verification result back to the model if it fails.
- Prevent infinite verify/fix loops with a cap.

**Done when:**
- Simple app generation verifies automatically.
- Failed verification triggers one or more fix attempts.
- Final output includes the verification command/result.

### Task 11: Browser Smoke For Frontend Artifacts
**Files:** `internal/tools/browser_smoke.go`, `internal/tools/browser_smoke_test.go`, `internal/agent/verify.go`

**What to do:**
- Add a lightweight smoke path for HTML/static frontend files.
- At minimum:
  - file loads
  - no syntax/runtime error during load
  - expected controls exist when inferable
- Prefer existing browser tooling when configured; otherwise use static DOM/JS parse where possible.

**Done when:**
- Basic HTML apps get an actual load check.
- Verification failure is concise and actionable.

## Phase 5: Token And Context Reduction

### Task 12: Add Small Task Mode
**Files:** `internal/agent/prompts.go`, `internal/agent/prompts_test.go`, `internal/agent/loop.go`

**What to do:**
- Detect simple empty-directory tasks and short file-generation prompts.
- Use a smaller system/context package:
  - no giant repo instructions unless relevant
  - no full tool docs beyond selected tools
  - concise engineering policy
- Keep normal mode for complex repo edits.

**Done when:**
- One-file app generation input tokens drop materially.
- Existing complex-task behavior is unchanged.

### Task 13: Cache Static Prompt Sections
**Files:** `internal/agent/prompts.go`, `internal/agent/cache_test.go`

**What to do:**
- Cache rendered static instructions/tool descriptions per config hash.
- Avoid reinjecting duplicate static blocks across turns when provider supports cache metadata.
- Preserve correctness for config/profile changes.

**Done when:**
- Repeated turns in one session reduce billable/reported prompt load where supported.

### Task 14: Trim Tool Descriptions Dynamically
**Files:** `internal/agent/prompts.go`, `internal/tools/tools.go`, `internal/tools/*.md`

**What to do:**
- Include full docs only for tools likely needed.
- Use short descriptions for inactive tools.
- Let the model request more tool detail if needed.

**Done when:**
- Small tasks do not carry every tool's full manual.
- Tool-call accuracy does not regress in evals.

## Phase 6: Codex-Parity Evals

### Task 15: Add Parity Eval Suite
**Files:** `internal/eval/fixtures.go`, `internal/eval/task.go`, `internal/eval/eval_test.go`, `docs/evals/codex-parity.md`

**What to do:**
- Add recurring tasks:
  - todo app
  - calculator
  - notes app
  - quiz app
  - small Go bug fix
  - small Node test fix
  - frontend build with verification
- Capture:
  - success/fail
  - changed files
  - verification run
  - tokens
  - elapsed time

**Done when:**
- `bharatcode eval run codex-parity` gives a stable quality signal.

### Task 16: Add UX Regression Script
**Files:** `scripts/ux-smoke.sh`, `scripts/README.md`

**What to do:**
- Script normal `run`, JSON `run`, headless TUI capture, and `doctor`.
- Assert:
  - no noisy redraw flood in headless mode
  - changed files printed
  - doctor shows ChatGPT status
  - command exits cleanly

**Done when:**
- One script catches the exact regressions found in user testing.

## Phase 7: Release Hardening

### Task 17: Document New Behavior
**Files:** `README.md`, `docs/install.md`, `docs/modules/cmd.md`, `web/app/docs/cli/page.tsx`

**What to do:**
- Document:
  - `bharatcode auth chatgpt`
  - `doctor` ChatGPT status
  - `BHARATCODE_HEADLESS=1`
  - changed-file summaries
  - verification policy once implemented

**Done when:**
- A new user can understand setup and automation behavior from docs.

### Task 18: Release Checklist
**Files:** `docs/release/codex-parity.md`, `.github/workflows/*`

**What to do:**
- Add release gate:
  - `go test ./...`
  - parity eval subset
  - UX smoke script
  - install method smoke for npm/brew/source where possible

**Done when:**
- A release cannot regress the main Codex-parity fixes unnoticed.

## Priority Order

1. Normal TUI redraw/input polish.
2. TUI final summaries and conversation transcript quality.
3. Automatic verification policy and command discovery.
4. Post-write verification loop.
5. Browser/static frontend smoke.
6. Small task token reduction.
7. Parity eval suite focused on conversation quality.
8. Docs and release gates.

## Success Metrics

- Basic app generation success: at least 5/5.
- Basic app verification: at least 5/5 verified or explicitly skipped with valid reason.
- `doctor` setup clarity: no false "all providers broken" impression for ChatGPT users.
- Headless TUI capture: no repeated full-screen redraw flood.
- Small one-file app token usage: reduce by at least 50% from the observed 40k-88k input-token range.
- Final output: exact changed paths present in both `run` and TUI flows.
- TUI conversation: final answer includes changed files, verification status, and concise summary without raw JSON or repeated redraw artifacts.
- TUI progress: each long-running turn shows a useful current action within 2 seconds of tool activity.
- Input: Enter submission works consistently in real terminals and PTY automation.
- Full suite remains green: `rtk go test ./...`.

## Conversation Quality Acceptance Test

Use this as the core end-to-end test after every major phase.

**Command:**
```sh
rm -rf /tmp/bc-codex-like
mkdir -p /tmp/bc-codex-like
rtk bharatcode --yolo --project-dir /tmp/bc-codex-like
```

**Prompt:**
```text
Build a basic single-file Pomodoro timer app in this folder. It should work by opening index.html. Include start, pause, reset, short break, and long break.
```

**Expected TUI behavior:**
- Input submits on Enter.
- The user prompt appears once, not repeatedly.
- While working, the status line shows meaningful activity, for example `write`, `bash`, `verify`, or `working`.
- Tool activity renders as compact progress, not raw JSON.
- No repeated full-screen redraw flood is visible to the user.
- Final assistant message includes:
  - `index.html`
  - exact path or a clearly accessible path
  - what was created
  - verification status
  - no vague "should work" unless verification was skipped with reason

**Expected final style:**
```text
Created index.html with a Pomodoro timer.

Verified:
- Opened the page successfully
- Start, pause, reset, short break, and long break controls respond

Changed files:
- /tmp/bc-codex-like/index.html
```

**Failure examples:**
- final answer only says `Done`
- final answer omits changed files
- raw tool JSON appears in chat
- spinner/redraw output dominates captured transcript
- no verification or skipped-verification explanation
- Enter does not submit reliably

## Verification Matrix

Use this matrix after each phase and before release. The point is to verify behavior the way a normal user experiences it, not only unit-test internals.

### 1. Doctor And Auth Verification

**Command:**
```sh
rtk go test ./internal/cmd -run 'TestDoctor|TestRunDoctor'
rtk go build -o /tmp/bharatcode-verify .
rtk /tmp/bharatcode-verify doctor
```

**Expected:**
- Output contains `BharatCode doctor`.
- Output contains `Config valid: parsed and validated`.
- If ChatGPT auth exists, output contains:
  - `ChatGPT subscription: signed in`
  - account email when available
  - plan when available
- If ChatGPT auth is missing, output contains:
  - `ChatGPT subscription: not signed in`
  - `bharatcode auth chatgpt`
- No secret values are printed.

**Manual negative test:**
```sh
tmp="$(mktemp -d)"
BHARATCODE_CHATGPT_AUTH="$tmp/missing.json" rtk /tmp/bharatcode-verify doctor
```

**Pass when:** the user can tell whether ChatGPT subscription auth is working without knowing about API keys.

### 2. Headless TUI Capture Verification

**Command:**
```sh
rtk go test ./internal/tui/... -run TestShouldDisableRenderer
rm -rf /tmp/bc-headless-verify
mkdir -p /tmp/bc-headless-verify
BHARATCODE_HEADLESS=1 rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-headless-verify
```

In a PTY automation harness, send `Ctrl-C` after 2-3 seconds.

**Expected:**
- Output does not contain thousands of repeated redraw frames.
- No full-screen alternate-buffer flood.
- Process exits cleanly on interrupt.

**Byte-threshold check:**
```sh
script -q /tmp/bc-headless.log env BHARATCODE_HEADLESS=1 /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-headless-verify
wc -c /tmp/bc-headless.log
```

**Pass when:** idle captured output remains small enough to inspect manually. Use the previous bad behavior as a regression baseline: hundreds of thousands of captured bytes in under a minute is a failure.

### 3. Normal TUI Input And Conversation Verification

**Command:**
```sh
rm -rf /tmp/bc-tui-input
mkdir -p /tmp/bc-tui-input
rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-tui-input
```

**Manual flow:**
1. Type: `Create a simple index.html saying hello`
2. Press Enter.
3. Repeat using a PTY script that sends LF (`\n`).
4. Repeat using a PTY script that sends CR (`\r`).

**Expected:**
- Prompt submits in both LF and CR automation paths.
- The app creates `/tmp/bc-tui-input/index.html`.
- The final TUI text mentions `index.html` or a changed-file summary.
- The final message states verification result or skipped-verification reason.
- The visible conversation is readable without raw JSON/tool argument dumps.

**Pass when:** normal users and automation can submit reliably.

### 4. Run Changed-Files Verification

**Command:**
```sh
rm -rf /tmp/bc-run-files
mkdir -p /tmp/bc-run-files
rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-run-files run --quiet \
  "Create a single-file HTML todo app at index.html."
```

**Expected:**
- Stdout includes the assistant response.
- Stdout includes:
  - `Changed files:`
  - `/tmp/bc-run-files/index.html`
- The path is absolute.
- JSON mode does not include the human `Changed files:` block:
```sh
rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-run-files-json run --json \
  "Create index.html"
```

**Pass when:** non-JSON output is useful to humans and JSON remains machine-safe NDJSON.

### 5. Tool-Result Fallback Verification

**Unit commands:**
```sh
rtk go test ./internal/cmd -run TestRunOutputLastMessageFallsBackToToolResult
rtk go test ./internal/tui -run TestTurnNotifyBodyFromMessagesFallsBackToToolResult
```

**Expected:**
- If assistant final prose is empty but a tool result exists, output/notification fallback uses the tool result.
- File paths or verification details from the tool result are not lost.

**Pass when:** a simple file-writing run never ends with a blank or useless final message.

### 6. Automatic Verification Verification

Run after Tasks 8-11 are implemented.

**Static HTML command:**
```sh
rm -rf /tmp/bc-verify-html
mkdir -p /tmp/bc-verify-html
rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-verify-html run --quiet \
  "Build a single-file calculator app in index.html. Verify it works."
```

**Expected:**
- Output mentions `Verified` or `Verification`.
- Output names the verification method, for example browser smoke or static load check.
- If verification fails, the agent fixes and reruns before final response.

**Go project command:**
```sh
tmp=/tmp/bc-verify-go
rm -rf "$tmp"
mkdir -p "$tmp"
cat > "$tmp/go.mod" <<'EOF'
module example.com/verify

go 1.22
EOF
cat > "$tmp/calc.go" <<'EOF'
package verify

func Add(a, b int) int { return a - b }
EOF
cat > "$tmp/calc_test.go" <<'EOF'
package verify

import "testing"

func TestAdd(t *testing.T) {
	if Add(2, 3) != 5 {
		t.Fatal("bad add")
	}
}
EOF
rtk /tmp/bharatcode-verify --yolo --project-dir "$tmp" run --quiet \
  "Fix the failing test."
```

**Expected:**
- Agent runs or reports `go test ./...`.
- Final output says tests passed.
- `rtk go test ./...` passes in `$tmp`.

**Pass when:** verification is automatic for both generated apps and real project fixes.

### 7. Token Reduction Verification

Run after Tasks 12-14 are implemented.

**Command:**
```sh
rm -rf /tmp/bc-token-small
mkdir -p /tmp/bc-token-small
rtk /tmp/bharatcode-verify --yolo --project-dir /tmp/bc-token-small run --quiet \
  "Create a single-file todo app in index.html."
```

**Expected:**
- Stderr summary reports materially fewer input tokens than the earlier observed 40k-88k range.
- Target: less than 25k input tokens for simple empty-directory single-file app tasks.

**Pass when:** simple tasks no longer carry full complex-repo context.

### 8. Parity Eval Verification

Run after Task 15 is implemented.

**Command:**
```sh
rtk bharatcode eval run codex-parity
```

**Expected report fields:**
- task name
- success/fail
- files changed
- verification command/result
- input/output tokens
- elapsed time
- failure reason when any

**Pass when:** the eval suite gives a stable signal and catches regressions in the same flows tested manually here.

### 9. Release Gate Verification

**Command:**
```sh
rtk go test ./...
rtk go build -o /tmp/bharatcode-release-check .
rtk /tmp/bharatcode-release-check doctor
rtk scripts/ux-smoke.sh /tmp/bharatcode-release-check
```

**Expected:**
- Full tests pass.
- Build succeeds.
- Doctor is actionable.
- UX smoke covers:
  - headless TUI capture
  - `run` changed files
  - JSON run remains valid NDJSON
  - basic app verification

**Pass when:** a release cannot ship with the exact regressions found during this Codex-comparison exercise.
