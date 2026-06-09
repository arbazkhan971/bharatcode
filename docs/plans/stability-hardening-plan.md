# BharatCode Stability Hardening Plan

**Goal:** make BharatCode feel release-grade for normal `bharatcode --yolo` usage, not only unit-test stable.

**Branch:** `feat/stability-hardening`
**Estimated tasks:** 12
**Primary bar:** every release proves interactive TUI behavior, live provider behavior, clean release hygiene, and install/update paths.

## Pre-flight

- [ ] Start from a clean branch: `git checkout -b feat/stability-hardening`
- [ ] Baseline tests pass: `go test ./...`
- [ ] Current UX smoke passes: `go build -o /tmp/bharatcode . && scripts/ux-smoke.sh /tmp/bharatcode`
- [ ] Record local untracked files before edits: `git status --short`

## Phase 1: Interactive TUI Acceptance

### Task 1: Add PTY TUI Harness
**File:** `internal/tui/pty_smoke_test.go`
**Depends on:** none
**Type:** test

**What to do:**
Add an integration-style test harness that launches the compiled binary in a pseudo-terminal, sends LF and CR prompt submissions, waits for stable output, and captures the transcript. Keep it behind an opt-in env var so normal unit tests stay fast.

**Code sketch:**
```go
func TestPTYTUIAcceptsPromptAndQuits(t *testing.T) {
	if os.Getenv("BHARATCODE_TUI_PTY_SMOKE") != "1" {
		t.Skip("set BHARATCODE_TUI_PTY_SMOKE=1")
	}
	bin := buildTestBinary(t)
	transcript := runPTY(t, bin, []string{
		"hello from pty\n",
		"/quit\r",
	})
	require.Contains(t, transcript, "hello from pty")
	require.NotContains(t, transcript, "\x1b[?1049h\x1b[?1049l\x1b[?1049h")
}
```

**Done when:** `BHARATCODE_TUI_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTUI` drives the real binary without hanging.

### Task 2: Add Golden Transcript Normalizer
**File:** `internal/tui/transcript_test.go`
**Depends on:** Task 1
**Type:** test

**What to do:**
Normalize ANSI/control sequences, spinner timing, UUIDs, timestamps, and paths so TUI transcripts can be compared stably. Store expected snapshots in `internal/tui/testdata/transcripts/`.

**Code sketch:**
```go
func normalizeTranscript(s string) string {
	s = stripANSI(s)
	s = uuidRE.ReplaceAllString(s, "<uuid>")
	s = pathRE.ReplaceAllString(s, "<path>")
	return strings.TrimSpace(collapseWhitespace(s))
}
```

**Done when:** one transcript fixture proves user input, tool activity, assistant final answer, changed files, and verification status are visible.

### Task 3: Script Five Real Normal-User Apps
**File:** `scripts/tui-acceptance.sh`
**Depends on:** Tasks 1-2
**Type:** create

**What to do:**
Run five app-building prompts through `bharatcode --yolo` in a PTY: todo, calculator, notes, quiz, static HTML app. Save normalized transcripts and verify output files plus build/smoke commands.

**Code sketch:**
```bash
run_case todo "Build a tiny todo CLI app and verify it"
assert_file "$case_dir/main.go"
assert_transcript_contains "Changed files:"
assert_transcript_contains "Verification:"
```

**Done when:** `scripts/tui-acceptance.sh ./bharatcode` passes locally with a live provider.

## Phase 2: Real Provider Evaluation

### Task 4: Add Live Provider Eval Mode
**File:** `internal/cmd/eval_cmd.go`
**Depends on:** none
**Type:** modify

**What to do:**
Add `bharatcode eval --suite codex-parity --live-provider --max-tasks N`. This should run a small subset against the configured real provider and report changed files, verification command, duration, and pass/fail.

**Code sketch:**
```go
cmd.Flags().BoolVar(&liveProvider, "live-provider", false, "run selected eval tasks against the configured provider")
if liveProvider {
	return runLiveProviderEval(ctx, w, application, suiteName, maxTasks, jsonOut)
}
```

**Done when:** live eval can run one task without using the deterministic stub.

### Task 5: Add Live Eval Budget And Timeout Guard
**File:** `internal/eval/live.go`
**Depends on:** Task 4
**Type:** create

**What to do:**
Prevent accidental expensive runs. Require `BHARATCODE_LIVE_EVAL=1`, cap tasks, cap wall time, and print estimated risk before running.

**Code sketch:**
```go
if os.Getenv("BHARATCODE_LIVE_EVAL") != "1" {
	return errors.New("set BHARATCODE_LIVE_EVAL=1 to run live-provider evals")
}
ctx, cancel := context.WithTimeout(ctx, 10*time.Minute)
defer cancel()
```

**Done when:** live eval refuses to run unless explicitly enabled and exits cleanly on timeout.

### Task 6: Persist Live Eval Reports
**File:** `internal/eval/report.go`
**Depends on:** Tasks 4-5
**Type:** modify

**What to do:**
Write JSONL reports into `.bharatcode/evals/live/` with provider, model, task, pass/fail, changed files, verification status, tokens, and duration.

**Code sketch:**
```go
type LiveReport struct {
	Provider string `json:"provider"`
	Model string `json:"model"`
	Task string `json:"task"`
	Passed bool `json:"passed"`
}
```

**Done when:** a failed live eval leaves enough artifacts to debug without rerunning.

## Phase 3: Stronger UX And Release Gates

### Task 7: Make UX Smoke Provider-Deterministic
**File:** `scripts/ux-smoke.sh`
**Depends on:** none
**Type:** modify

**What to do:**
Add a fake local provider mode for `run --json` and changed-files checks so CI does not depend on ChatGPT/API availability. Keep live-provider checks as an optional extra.

**Code sketch:**
```bash
if [[ "${UX_SMOKE_PROVIDER_MODE:-fake}" == "fake" ]]; then
	start_fake_provider
	export BHARATCODE_CONFIG="$fake_config"
fi
```

**Done when:** CI can assert JSON framing and changed-files behavior without external auth.

### Task 8: Add TUI Acceptance To CI Nightly
**File:** `.github/workflows/ci.yml`
**Depends on:** Tasks 1-3
**Type:** config

**What to do:**
Add a scheduled/nightly job for PTY acceptance. Keep PR CI fast; run full TUI acceptance nightly and before release branches.

**Code sketch:**
```yaml
on:
  schedule:
    - cron: "0 21 * * *"
jobs:
  tui-acceptance:
    steps:
      - run: BHARATCODE_TUI_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTUI
```

**Done when:** nightly CI catches interactive regressions.

### Task 9: Add Release Preflight Script
**File:** `scripts/release-preflight.sh`
**Depends on:** Tasks 3, 7
**Type:** create

**What to do:**
One command should run all pre-tag checks: clean tracked diff, tests, build, codex-parity, UX smoke, optional TUI acceptance, npm shim checks, version/tag consistency.

**Code sketch:**
```bash
require_clean_tracked_tree
go test ./...
go build -o /tmp/bharatcode-release .
scripts/ux-smoke.sh /tmp/bharatcode-release
node --check npm/bin/bharatcode.js
```

**Done when:** releases do not depend on memory or manual command ordering.

## Phase 4: Worktree And Artifact Hygiene

### Task 10: Add Artifact Ignore Rules
**File:** `.gitignore`
**Depends on:** none
**Type:** modify

**What to do:**
Ignore local browser logs, screenshots, generated site captures, local Playwright state, and temporary binary outputs while preserving intentional assets.

**Code sketch:**
```gitignore
.playwright-mcp/
*-live.jpeg
tui-*.png
/bharatcode
/dist/
```

**Done when:** `git status --short` shows only intentional source changes after local testing.

### Task 11: Add Dirty Worktree Release Guard
**File:** `scripts/release-preflight.sh`
**Depends on:** Task 9
**Type:** modify

**What to do:**
Fail release preflight if tracked files are dirty or if untracked files exist outside an allowlist.

**Code sketch:**
```bash
git diff --quiet || fail "tracked files are dirty"
unexpected="$(git ls-files --others --exclude-standard)"
[[ -z "$unexpected" ]] || fail "unexpected untracked files:\n$unexpected"
```

**Done when:** a release cannot accidentally include or ignore unknown artifacts.

### Task 12: Add Post-Release Install Verification
**File:** `scripts/post-release-smoke.sh`
**Depends on:** Task 9
**Type:** create

**What to do:**
After a tag publishes, verify GitHub release assets, npm install, and Homebrew install all report the same version.

**Code sketch:**
```bash
version="${1:?version like v0.2.7}"
gh release view "$version"
npm view bharatcode-cli version | grep "${version#v}"
brew update && brew install arbazkhan971/tap/bharatcode
bharatcode version | grep "${version#v}"
```

**Done when:** post-release smoke confirms the published distribution channels.

## Post-flight

- [ ] `go test ./...`
- [ ] `scripts/ux-smoke.sh ./bharatcode`
- [ ] `scripts/tui-acceptance.sh ./bharatcode`
- [ ] `BHARATCODE_LIVE_EVAL=1 bharatcode eval --suite codex-parity --live-provider --max-tasks 2`
- [ ] `scripts/release-preflight.sh`
- [ ] Nightly TUI acceptance is green

## Success Metric

BharatCode is “best” when every release has:

- deterministic unit and offline eval coverage,
- provider-independent CI smoke coverage,
- live-provider acceptance coverage,
- real PTY/TUI transcript acceptance,
- clean release hygiene,
- post-release install verification across GitHub, npm, and Homebrew.
