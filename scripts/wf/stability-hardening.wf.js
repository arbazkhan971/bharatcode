export const meta = {
  name: 'stability-hardening',
  description: 'Implement the 12-task BharatCode stability-hardening plan using parallel git worktrees: one worktree per file-disjoint task-chain, each implemented+reviewed+fixed in isolation, merged back conflict-free between dependency waves, with full-suite gates.',
  phases: [
    { title: 'Wave 1 chains' },
    { title: 'Merge + Gate 1' },
    { title: 'Wave 2 chains' },
    { title: 'Merge + Gate 2' },
    { title: 'Final report' },
  ],
}

const REPO = '/Users/arbaz/bharatcode'
const BASE = 'feat/stability-hardening' // integration branch (created before launch)

const CTX = `You are implementing part of the BharatCode "stability hardening" plan in the Go repo (module github.com/arbazkhan971/bharatcode).
GOAL: make BharatCode release-grade for real \`bharatcode --yolo\` usage — PTY/TUI transcript acceptance, live-provider eval, provider-independent CI smoke, clean release hygiene, post-release install verification.
HARD RULES:
- Match surrounding code style, comment density, and Go idioms; this is a mature codebase.
- NEVER reference any external project, prior codebase, or provenance anywhere (code/comments/metadata).
- Keep \`go build ./...\` green and do not break existing tests. New PTY/live tests MUST be opt-in behind their env vars (BHARATCODE_TUI_PTY_SMOKE=1, BHARATCODE_LIVE_EVAL=1) so the default \`go test ./...\` stays fast and offline.
- Shell scripts: make them executable (chmod +x), POSIX/bash-correct, and take a binary path argument where the plan shows one.
- Edit ONLY the files your chain owns. Other chains run concurrently in separate worktrees.
- Prefer extending existing files over rewriting. grep/read before you build.`

const CHAIN_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['chain', 'tasks', 'buildOk', 'testsRun', 'committed'],
  properties: {
    chain: { type: 'string' },
    tasks: { type: 'array', items: {
      type: 'object', additionalProperties: false,
      required: ['id', 'status', 'summary', 'files'],
      properties: {
        id: { type: 'string' },
        status: { type: 'string', enum: ['done', 'partial', 'blocked'] },
        summary: { type: 'string' },
        files: { type: 'array', items: { type: 'string' } },
      },
    } },
    buildOk: { type: 'boolean' },
    testsRun: { type: 'string', description: 'go test / script commands run and their results' },
    committed: { type: 'boolean', description: 'whether all changes were git-added and committed in the worktree' },
    notes: { type: 'string' },
  },
}

const REVIEW_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['chain', 'verdict', 'findings'],
  properties: {
    chain: { type: 'string' },
    verdict: { type: 'string', enum: ['approve', 'revise'] },
    findings: { type: 'array', items: {
      type: 'object', additionalProperties: false,
      required: ['severity', 'file', 'issue', 'fix'],
      properties: {
        severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
        file: { type: 'string' }, issue: { type: 'string' }, fix: { type: 'string' },
      },
    } },
  },
}

const GATE_SCHEMA = {
  type: 'object', additionalProperties: false,
  required: ['buildOk', 'testsOk', 'failing', 'detail'],
  properties: {
    buildOk: { type: 'boolean' }, testsOk: { type: 'boolean' },
    failing: { type: 'array', items: { type: 'string' } }, detail: { type: 'string' },
  },
}

// A chain: implement all its tasks sequentially inside ONE isolated worktree on a
// branch wt/<chain>, commit, then adversarially review and fix in the same
// worktree. The worktree branch is returned for the merge step.
async function runChain(c, phase) {
  const branch = `wt/${c.key}`
  const spec = `${CTX}

You are running in an ISOLATED GIT WORKTREE. Before doing anything, run:
  cd ${REPO} && git worktree add -B ${branch} /tmp/wt-${c.key} ${c.base}
  cd /tmp/wt-${c.key}
ALL your work happens in /tmp/wt-${c.key} on branch ${branch}. Do NOT touch ${REPO} directly.

CHAIN ${c.key}: ${c.title}
Implement these tasks IN ORDER (later tasks may depend on earlier ones in this same chain):
${c.tasks.map(t => `--- TASK ${t.id} (${t.type}) — file: ${t.file}\n${t.prompt}\nDONE WHEN: ${t.doneWhen}`).join('\n\n')}

After implementing all tasks:
  cd /tmp/wt-${c.key}
  go build ./...   (must pass)
  run the relevant \`go test\` / scripts to validate (PTY/live tests stay opt-in; don't run them in default mode)
  git add -A && git commit -m "stability(${c.key}): ${c.title}"
Return the structured result. Leave the worktree in place (the merge step removes it).`

  const impl = await agent(spec, { label: `impl:${c.key}`, phase, schema: CHAIN_SCHEMA })
  if (!impl) return { chain: c.key, branch, status: 'blocked', impl: null, review: null }

  const review = await agent(`${CTX}

ADVERSARIAL review of chain ${c.key} in worktree /tmp/wt-${c.key} (branch ${branch}).
Implementer reported: ${JSON.stringify(impl)}
cd /tmp/wt-${c.key} and inspect the actual code (git diff ${c.base}..HEAD, read the files). For EACH task verify its DONE-WHEN is truly met:
${c.tasks.map(t => `- ${t.id}: ${t.doneWhen}`).join('\n')}
Check: opt-in env gating actually present on PTY/live tests; scripts executable & take the binary arg; no forbidden provenance; build green; no edits outside owned files; default \`go test ./...\` still fast/offline. Default to 'revise' if any DONE-WHEN is unproven.`,
    { label: `review:${c.key}`, phase, schema: REVIEW_SCHEMA })

  if (review && review.verdict === 'revise' && review.findings?.length) {
    await agent(`${CTX}

Apply these review findings in worktree /tmp/wt-${c.key} (branch ${branch}). Edit only chain-${c.key} files.
FINDINGS:
${JSON.stringify(review.findings, null, 2)}
Apply all blockers+majors (minors if cheap). Then: cd /tmp/wt-${c.key} && go build ./... and re-validate, then git add -A && git commit --amend --no-edit (or a new commit). Report.`,
      { label: `fix:${c.key}`, phase, schema: CHAIN_SCHEMA })
  }
  return { chain: c.key, branch, status: impl.status, impl, review }
}

// Merge a set of completed worktree branches back into BASE in order, then remove
// the worktrees. Disjoint file sets => clean merges. Returns merge log.
async function mergeChains(chains, label, phase) {
  const branches = chains.filter(Boolean).map(c => c.branch).join(' ')
  return await agent(`${CTX}

MERGE STEP "${label}". In the MAIN repo ${REPO} (on branch ${BASE}), merge each worktree branch back IN THIS ORDER: ${branches}
For each branch B:
  cd ${REPO} && git merge --no-ff --no-edit B
The chains have disjoint file sets so merges should be clean. If a conflict DOES occur, resolve it by keeping BOTH sides' additions (these are independent features), then continue.
After all merges:
  cd ${REPO} && go build ./...   (must pass)
  git worktree remove /tmp/wt-<key> --force   for each merged chain (keys: ${chains.filter(Boolean).map(c=>c.branch.replace('wt/','')).join(', ')})
  git worktree prune
Report what merged cleanly, any conflicts resolved, and the final build status.`,
    { label: `merge:${label}`, phase })
}

async function gate(label, phase) {
  const g = await agent(`${CTX}

GATE "${label}": cd ${REPO} && git checkout ${BASE} (ensure on integration branch), then \`go build ./...\` and \`go test ./...\`. Report build clean + ALL tests pass; list failing packages with first error line. Do NOT fix — just report.`,
    { label: `gate:${label}`, phase, schema: GATE_SCHEMA })
  if (g) log(`Gate ${label}: build=${g.buildOk} tests=${g.testsOk}${g.failing?.length ? ' failing=' + g.failing.join(',') : ''}`)
  return g
}

async function repairIfNeeded(g, label, phase) {
  if (!g || (g.buildOk && g.testsOk)) return g
  log(`Gate ${label} red — dispatching repair.`)
  await agent(`${CTX}

The suite is RED on ${BASE} after ${label}. Failing: ${JSON.stringify(g.failing)}. Detail: ${g.detail}
cd ${REPO}, diagnose, and make MINIMAL fixes so \`go build ./...\` and \`go test ./...\` are fully green. Commit the fix. Report.`,
    { label: `repair:${label}`, phase })
  return await gate(label + '-recheck', phase)
}

// ===========================================================================
// WAVE 1 — four independent chains, each in its own worktree, in parallel.
// All branch off BASE (no inter-chain deps).
// ===========================================================================
phase('Wave 1 chains')

const wave1 = [
  { key: 'pty', title: 'PTY/TUI transcript acceptance', base: BASE, tasks: [
    { id: 'T1', type: 'test', file: 'internal/tui/pty_smoke_test.go',
      prompt: `Add an integration PTY harness that launches the COMPILED binary in a pseudo-terminal, sends LF and CR prompt submissions, waits for stable output, captures the transcript. Behind opt-in env BHARATCODE_TUI_PTY_SMOKE=1 (t.Skip otherwise). Include helpers buildTestBinary(t) and runPTY(t,bin,inputs). Assert transcript contains sent text and does NOT contain a repeated alt-screen flap.`,
      doneWhen: `BHARATCODE_TUI_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTUI drives the real binary without hanging; default go test skips it.` },
    { id: 'T2', type: 'test', file: 'internal/tui/transcript_test.go (+ internal/tui/testdata/transcripts/)',
      prompt: `Add a transcript normalizer: strip ANSI/control seqs, normalize spinner timing, UUIDs, timestamps, paths; collapse whitespace. Store an expected snapshot under internal/tui/testdata/transcripts/. Prove user input, tool activity, assistant final answer, changed files, and verification status are visible in one fixture. Keep deterministic and offline.`,
      doneWhen: `one transcript fixture proves the five visibility elements; normalizeTranscript is unit-tested deterministically.` },
    { id: 'T3', type: 'create', file: 'scripts/tui-acceptance.sh',
      prompt: `Create scripts/tui-acceptance.sh that runs five app prompts (todo, calculator, notes, quiz, static HTML) through the binary in a PTY, saves normalized transcripts, asserts output files exist and transcript contains "Changed files:" and "Verification:". Takes binary path arg. Executable. It may require a live provider to fully pass — guard provider-dependent asserts so the script is structurally runnable/lintable offline (bash -n clean).`,
      doneWhen: `scripts/tui-acceptance.sh ./bharatcode is structurally valid (bash -n passes) and runs the five cases; provider-dependent asserts are clearly gated.` },
  ] },
  { key: 'eval', title: 'Live-provider eval mode', base: BASE, tasks: [
    { id: 'T4', type: 'modify', file: 'internal/cmd/eval_cmd.go',
      prompt: `Add flags --live-provider (bool) and --max-tasks (int) to \`bharatcode eval\`. When --live-provider, run a small subset against the configured real provider via a new runLiveProviderEval(...) and report changed files, verification command, duration, pass/fail. Reuse existing eval wiring; do not break the deterministic stub path.`,
      doneWhen: `live eval can run one task without the deterministic stub; flags parse and --help shows them.` },
    { id: 'T5', type: 'create', file: 'internal/eval/live.go',
      prompt: `Create internal/eval/live.go implementing runLiveProviderEval guards: require env BHARATCODE_LIVE_EVAL=1 (else clear error), cap tasks (max-tasks), cap wall time via context.WithTimeout (~10m), and print an estimated-risk line before running. Exit cleanly on timeout.`,
      doneWhen: `live eval refuses to run unless BHARATCODE_LIVE_EVAL=1; exits cleanly on timeout; unit test covers the gate + cap.` },
    { id: 'T6', type: 'modify', file: 'internal/eval/report.go',
      prompt: `Add a LiveReport struct (provider, model, task, passed, changed files, verification status, tokens, duration) and write JSONL into .bharatcode/evals/live/. Ensure a failed live eval leaves enough artifacts to debug without rerunning. Add a unit test for the JSONL writer.`,
      doneWhen: `a failed live eval persists a debuggable JSONL artifact; writer is unit-tested.` },
  ] },
  { key: 'ux', title: 'Provider-deterministic UX smoke', base: BASE, tasks: [
    { id: 'T7', type: 'modify', file: 'scripts/ux-smoke.sh',
      prompt: `Add a fake local provider mode so run --json and changed-files checks do NOT need ChatGPT/API. Default UX_SMOKE_PROVIDER_MODE=fake starts a fake provider and points BHARATCODE_CONFIG at a fake config; live-provider checks become an optional extra. Keep existing assertions. bash -n clean; executable.`,
      doneWhen: `CI can assert JSON framing and changed-files behavior without external auth; live checks still optionally available.` },
  ] },
  { key: 'ignore', title: 'Artifact ignore rules', base: BASE, tasks: [
    { id: 'T10', type: 'modify', file: '.gitignore',
      prompt: `Add ignore rules for local browser logs, screenshots, generated site captures, local Playwright state, and temp binary outputs (.playwright-mcp/, *-live.jpeg, tui-*.png, /bharatcode, /dist/, etc.) while preserving intentional assets/ that are tracked. Do not ignore anything already committed intentionally.`,
      doneWhen: `git status --short shows only intentional source changes after local testing; no tracked file becomes ignored.` },
  ] },
]

const w1results = (await parallel(wave1.map(c => () =>
  runChain(c, 'Wave 1 chains').then(r => ({ ...r, _c: c }))
))).filter(Boolean)

phase('Merge + Gate 1')
await mergeChains(w1results, 'wave1', 'Merge + Gate 1')
let g1 = await gate('wave1', 'Merge + Gate 1')
g1 = await repairIfNeeded(g1, 'wave1', 'Merge + Gate 1')

// ===========================================================================
// WAVE 2 — chains that depend on wave-1 outputs. Branch off BASE (now contains
// wave-1 merges), each in its own worktree, in parallel where independent.
// Chain R (T9->T11) and Chain X (T12) are sequential (X needs R's file present),
// so X runs after R; C is independent and runs alongside.
// ===========================================================================
phase('Wave 2 chains')

const chainC = { key: 'ci', title: 'TUI acceptance in CI nightly', base: BASE, tasks: [
  { id: 'T8', type: 'config', file: '.github/workflows/ci.yml',
    prompt: `Add a scheduled nightly job (cron) for PTY acceptance running BHARATCODE_TUI_PTY_SMOKE=1 go test ./internal/tui -run TestPTYTUI (from chain pty, now merged). Keep PR CI fast — nightly + before release branches only. Do NOT touch release.yml.`,
    doneWhen: `nightly CI job exists and is valid YAML; PR path unchanged/fast.` },
] }

const chainR = { key: 'preflight', title: 'Release preflight + dirty-worktree guard', base: BASE, tasks: [
  { id: 'T9', type: 'create', file: 'scripts/release-preflight.sh',
    prompt: `Create scripts/release-preflight.sh running all pre-tag checks in order: require clean tracked tree, go test ./..., go build, codex-parity eval subset, UX smoke (scripts/ux-smoke.sh from chain ux, merged), optional TUI acceptance, npm shim check (node --check npm/bin/bharatcode.js), version/tag consistency (npm/package.json vs latest tag). Takes no required args (build its own temp binary). Executable, bash -n clean.`,
    doneWhen: `releases do not depend on memory/manual ordering; bash -n passes; running it executes the offline checks.` },
  { id: 'T11', type: 'modify', file: 'scripts/release-preflight.sh',
    prompt: `Extend release-preflight.sh: FAIL if tracked files are dirty (git diff --quiet) or if untracked files exist outside an allowlist (git ls-files --others --exclude-standard filtered). Clear failure messages.`,
    doneWhen: `a release cannot accidentally include or ignore unknown artifacts; the guard is exercised by a quick self-test in the script comments or a dry-run.` },
] }

const chainX = { key: 'postrel', title: 'Post-release install verification', base: BASE, tasks: [
  { id: 'T12', type: 'create', file: 'scripts/post-release-smoke.sh',
    prompt: `Create scripts/post-release-smoke.sh <version like v0.2.7>: verify the GitHub release assets exist (gh release view), npm view bharatcode-cli version matches, and Homebrew install reports the same version; finally bharatcode version matches. Network/tool-dependent asserts must degrade gracefully (skip with a clear note if gh/brew/npm absent) so bash -n and a dry-run are clean. Executable.`,
    doneWhen: `post-release smoke confirms GitHub, npm, and Homebrew report the same version; bash -n passes; missing tools skip cleanly.` },
] }

// C and R are independent (distinct files) -> parallel. X depends on R's file, so
// run X after R completes within its own thunk.
const w2results = (await parallel([
  () => runChain(chainC, 'Wave 2 chains').then(r => ({ ...r, _c: chainC })),
  () => runChain(chainR, 'Wave 2 chains').then(r => ({ ...r, _c: chainR })),
  () => runChain(chainX, 'Wave 2 chains').then(r => ({ ...r, _c: chainX })),
])).filter(Boolean)

phase('Merge + Gate 2')
await mergeChains(w2results, 'wave2', 'Merge + Gate 2')
let g2 = await gate('wave2', 'Merge + Gate 2')
g2 = await repairIfNeeded(g2, 'wave2', 'Merge + Gate 2')

// ===========================================================================
// FINAL REPORT
// ===========================================================================
phase('Final report')
const all = [...w1results, ...w2results].map(r => ({ chain: r.chain, status: r.status, tasks: r.impl?.tasks, review: r.review?.verdict }))
const report = await agent(`${CTX}

All 12 tasks implemented across two worktree waves and merged into ${BASE}. Chain records:
${JSON.stringify(all, null, 2)}
Gates: wave1=${JSON.stringify(g1)} wave2=${JSON.stringify(g2)}
Do a FINAL verification in ${REPO} on ${BASE}: \`go build ./...\` && \`go test ./...\` (must be green); \`bash -n\` each new script; confirm \`git worktree list\` shows no leftover /tmp/wt-* worktrees (prune if any).
Write a concise engineering report: per-task done/partial/blocked one-liners, final build/test result, any human follow-up, net-new files, and confirmation that PTY/live tests are opt-in (default suite stays fast/offline). Be honest about partials.`,
  { label: 'final-report', phase: 'Final report' })

return { chains: all, gates: { g1, g2 }, report }
