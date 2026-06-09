export const meta = {
  name: 'codex-parity-impl',
  description: 'Implement the 18-task BharatCode Codex-parity plan in dependency waves with parallel, file-disjoint agents; each task is implemented, tested, adversarially reviewed, and fixed; full test suite gates each wave.',
  phases: [
    { title: 'Wave A: UX polish' },
    { title: 'Gate A' },
    { title: 'Wave B: Verification system' },
    { title: 'Gate B' },
    { title: 'Wave C: Token reduction' },
    { title: 'Gate C' },
    { title: 'Wave D: Evals + smoke' },
    { title: 'Gate D' },
    { title: 'Wave E: Docs + release' },
    { title: 'Gate E' },
    { title: 'Final report' },
  ],
}

// ---------------------------------------------------------------------------
// Shared context every agent gets: the end objective and the hard rules.
// ---------------------------------------------------------------------------
const REPO = '/Users/arbaz/bharatcode'
const CTX = `You are implementing one task of the BharatCode "Codex-parity" plan in the Go repo at ${REPO}.
END OBJECTIVE: make the TUI + conversation feel like Codex CLI — clean transcript, visible-but-quiet tool activity, concise file-aware verification-aware final answers, no claiming done without verifying.
HARD RULES:
- Match surrounding code style, comment density, and idioms. This is a mature Go codebase.
- NEVER reference Crush, FSL, clean-room, or any provenance in code/comments/metadata.
- Module path is github.com/arbazkhan971/bharatcode.
- Run \`cd ${REPO} && go build ./...\` and the relevant \`go test\` after editing; do not leave the tree non-compiling.
- Only edit the files your task owns (listed below). Do NOT touch files owned by other tasks — they run concurrently.
- Prefer extending existing plumbing over rewriting. Check what already exists first (grep before you build).
- Keep changes minimal and targeted to the task's "Done when" criteria.
EXISTING WIP (already committed, tests passing — EXTEND, do not revert): doctor.go has partial ChatGPT/provider status; run.go has a "Changed files:" block; tui.go has headless renderer gating (ShouldDisableRenderer); agentrun.go has final-summary scaffolding. For tasks touching these, read the current file first and build ON TOP of what's there.`

// JSON schemas for structured agent returns.
const IMPL_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['task', 'status', 'summary', 'filesTouched', 'testsRun', 'buildOk'],
  properties: {
    task: { type: 'string' },
    status: { type: 'string', enum: ['done', 'partial', 'blocked'] },
    summary: { type: 'string', description: 'What was implemented, concretely.' },
    filesTouched: { type: 'array', items: { type: 'string' } },
    testsRun: { type: 'string', description: 'Exact go test command(s) run and their pass/fail result.' },
    buildOk: { type: 'boolean' },
    notes: { type: 'string', description: 'Skips, follow-ups, or anything the reviewer must know.' },
  },
}

const REVIEW_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['task', 'verdict', 'findings'],
  properties: {
    task: { type: 'string' },
    verdict: { type: 'string', enum: ['approve', 'revise'] },
    findings: {
      type: 'array',
      items: {
        type: 'object',
        additionalProperties: false,
        required: ['severity', 'file', 'issue', 'fix'],
        properties: {
          severity: { type: 'string', enum: ['blocker', 'major', 'minor'] },
          file: { type: 'string' },
          issue: { type: 'string' },
          fix: { type: 'string' },
        },
      },
    },
  },
}

const FIX_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['task', 'applied', 'summary', 'buildOk', 'testsOk'],
  properties: {
    task: { type: 'string' },
    applied: { type: 'boolean', description: 'Whether review fixes were applied.' },
    summary: { type: 'string' },
    buildOk: { type: 'boolean' },
    testsOk: { type: 'boolean' },
  },
}

const GATE_SCHEMA = {
  type: 'object',
  additionalProperties: false,
  required: ['buildOk', 'testsOk', 'failingPackages', 'detail'],
  properties: {
    buildOk: { type: 'boolean' },
    testsOk: { type: 'boolean' },
    failingPackages: { type: 'array', items: { type: 'string' } },
    detail: { type: 'string' },
  },
}

// ---------------------------------------------------------------------------
// Task definitions. `files` is the ownership fence. `prompt` is the spec lifted
// from plan.md. Tasks in the same `chain` array share a file and run in order.
// ---------------------------------------------------------------------------
function task(id, title, files, prompt, doneWhen) {
  return { id, title, files, prompt, doneWhen }
}

// One task = implement -> review -> fix, returning the merged record.
async function runTask(t, phase) {
  const spec = `${CTX}

TASK ${t.id}: ${t.title}
FILES YOU OWN (edit only these): ${t.files}
WHAT TO DO:
${t.prompt}
DONE WHEN:
${t.doneWhen}

Implement it now. Build and run the relevant tests. Return the structured result.`

  const impl = await agent(spec, { label: `impl:${t.id}`, phase, schema: IMPL_SCHEMA })
  if (!impl) return { task: t.id, status: 'blocked', summary: 'impl agent died', review: null, fix: null }

  const reviewPrompt = `${CTX}

You are an ADVERSARIAL reviewer for TASK ${t.id}: ${t.title}.
The implementer reported: ${JSON.stringify(impl)}
Review ONLY files: ${t.files}
Inspect the actual current code (read the files, run \`cd ${REPO} && git diff -- ${t.files}\`). Check against DONE WHEN:
${t.doneWhen}
Look for: criteria not actually met, build/test claims that don't hold, missing tests, style mismatches, forbidden provenance references, edits leaking outside owned files, regressions. Be skeptical — default to 'revise' if a DONE-WHEN bullet is unproven. Return findings with concrete fixes.`
  const review = await agent(reviewPrompt, { label: `review:${t.id}`, phase, schema: REVIEW_SCHEMA })

  let fix = null
  if (review && review.verdict === 'revise' && review.findings && review.findings.length) {
    const fixPrompt = `${CTX}

Apply these review findings for TASK ${t.id}: ${t.title}. Edit ONLY: ${t.files}
FINDINGS:
${JSON.stringify(review.findings, null, 2)}
Apply every blocker and major; apply minors when cheap. Then \`cd ${REPO} && go build ./...\` and re-run the task's tests. Return the structured result.`
    fix = await agent(fixPrompt, { label: `fix:${t.id}`, phase, schema: FIX_SCHEMA })
  }
  return { task: t.id, status: impl.status, summary: impl.summary, impl, review, fix }
}

// Run a chain of tasks sequentially (they share a file), return all records.
async function runChain(tasks, phase) {
  const out = []
  for (const t of tasks) out.push(await runTask(t, phase))
  return out
}

// Full-suite gate. Returns structured pass/fail; logged for the user.
async function gate(label, phase) {
  const g = await agent(`${CTX}

GATE CHECK "${label}": run \`cd ${REPO} && go build ./...\` then \`cd ${REPO} && go test ./...\`.
Report whether the build is clean and ALL tests pass. List any failing packages with the first concrete error line. Do NOT fix anything — just report.`,
    { label: `gate:${label}`, phase, schema: GATE_SCHEMA })
  if (g) log(`Gate ${label}: build=${g.buildOk} tests=${g.testsOk}${g.failingPackages?.length ? ' failing=' + g.failingPackages.join(',') : ''}`)
  return g
}

// If a gate fails, spawn a repair agent to fix the regressions before continuing.
async function repairIfNeeded(g, label, phase) {
  if (!g || (g.buildOk && g.testsOk)) return g
  log(`Gate ${label} failed — dispatching repair.`)
  await agent(`${CTX}

The full suite is RED after wave "${label}". Failing: ${JSON.stringify(g.failingPackages)}. Detail: ${g.detail}
Diagnose and FIX so \`cd ${REPO} && go build ./...\` and \`cd ${REPO} && go test ./...\` are fully green. Make minimal targeted fixes. You may edit any file needed to restore green. Report what you fixed.`,
    { label: `repair:${label}`, phase })
  return await gate(label + '-recheck', phase)
}

// ===========================================================================
// WAVE A — UX polish. Disjoint chains run in parallel.
// ===========================================================================
phase('Wave A: UX polish')

const A_doctor = [
  task('T1', 'Expand doctor provider status',
    'internal/cmd/doctor.go internal/cmd/doctor_test.go',
    `Keep the existing ChatGPT subscription line. Add active agent/model/provider status for the default 'coder' agent. Show whether the active provider is usable now (env key set, local endpoint reachable, or ChatGPT auth present). Never print secrets.`,
    `bharatcode doctor shows "Active model", "Active provider", and "ChatGPT subscription"; missing auth gives a specific command hint; tests cover signed-in, missing-auth, env-key, and local-provider cases.`),
  task('T2', 'Add provider smoke check',
    'internal/cmd/doctor.go internal/llm',
    `Add optional \`bharatcode doctor --check-provider\` that makes one tiny non-streaming (or streamed) test request with a short timeout and reports success/failure. Default doctor must stay offline-fast and NOT make this call. Add llm-level test coverage where it fits.`,
    `Default doctor stays offline-fast; doctor --check-provider proves the configured model can answer; provider auth failures are actionable. Coordinate with T1 which runs before you on the same doctor.go — preserve its additions.`),
]

const A_tui = [
  task('T3', 'Keep headless renderer quiet',
    'internal/tui/tui.go internal/tui/tui_test.go',
    `Preserve the current BHARATCODE_HEADLESS / CI / TERM=dumb / empty-TERM quiet path (ShouldDisableRenderer). Add a smoke test asserting captured output stays below a sane byte threshold. (Docs handled separately by T17.)`,
    `Captured headless TUI emits no repeated redraw frames; normal terminal rendering unchanged; TestShouldDisableRenderer-style coverage holds.`),
  task('T4', 'Reduce normal TUI redraw churn',
    'internal/tui/tui.go internal/tui/statusbar/statusbar.go internal/tui/statusbar/statusbar_test.go',
    `Audit spinner tick frequency and status updates. Avoid rerendering unchanged frames. Cap long-running spinner/status output in captured PTYs. Preserve smooth behavior in real terminals. Runs AFTER T3 on tui.go — preserve T3's changes.`,
    `Interactive capture output is materially smaller even without BHARATCODE_HEADLESS; tool progress still visible; tests cover unchanged status frames not re-emitting.`),
]

const A_input = [
  task('T5', 'Make input submission robust',
    'internal/tui/input.go internal/tui/commands_test.go',
    `Ensure both LF (\\n) and CR (\\r) submit consistently in PTY automation. Keep multiline input behavior intact. Add tests for Enter variants.`,
    `Driving the TUI through a PTY submits with either newline form; multiline still works.`),
]

const A_run = [
  task('T6', 'Preserve changed file summaries',
    'internal/cmd/run.go internal/cmd/run_test.go',
    `Keep the deterministic "Changed files:" block for non-JSON run. Add operation labels (created/modified/deleted) when useful. Keep paths absolute. Do NOT alter NDJSON/JSON output.`,
    `bharatcode run prints concise changed-file output for file-writing tasks; repeated writes list one path once; JSON mode unchanged.`),
]

const A_summary = [
  task('T7', 'Improve TUI final summary',
    'internal/tui/agentrun.go internal/tui/agentrun_test.go internal/tui/chat/chat.go internal/tui/chat/render.go',
    `When a turn ends, append a compact local summary if files changed and the assistant did not mention them. Include exact paths and verification status. Avoid duplicating model prose when it already contains the same path. Keep the Codex-style flowing transcript already in place (do not reintroduce framed blocks).`,
    `TUI users see exact output paths without logs; empty final assistant messages still produce useful completion text.`),
]

const waveA = (await parallel([
  () => runChain(A_doctor, 'Wave A: UX polish'),
  () => runChain(A_tui, 'Wave A: UX polish'),
  () => runChain(A_input, 'Wave A: UX polish'),
  () => runChain(A_run, 'Wave A: UX polish'),
  () => runChain(A_summary, 'Wave A: UX polish'),
])).filter(Boolean).flat()

phase('Gate A')
let gA = await gate('A', 'Gate A')
gA = await repairIfNeeded(gA, 'A', 'Gate A')

// ===========================================================================
// WAVE B — Verification system. Sequential chain (shared verify.go/loop.go/tools.go).
// ===========================================================================
phase('Wave B: Verification system')

const B_chain = [
  task('T8', 'Define verification policy',
    'internal/agent/templates/coder.md.tpl internal/config/config.go docs/modules/agent.md',
    `Formalize when verification is REQUIRED: write/edit/multiedit/patch/rename changed files; generated frontend artifacts; package manifests touched; tests/build files touched. Define allowed SKIP reasons: no test command exists; external dependency unavailable; user explicitly asked not to run tests. Encode this so it is explicit and testable (config struct + agent template policy + doc).`,
    `Agent policy is explicit and testable; final response always says verified, failed, or skipped-with-reason.`),
  task('T9', 'Implement verification command discovery',
    'internal/agent/verify.go internal/agent/verify_test.go internal/tools/testparse.go',
    `Create verify.go. Detect likely commands from repo files: Go \`go test ./...\`; Node package scripts test/build/lint; Python pytest (or import smoke if no pytest); Rust \`cargo test\`; static HTML browser/DOM smoke or file-open sanity. Return ordered candidates with confidence and reason. Reuse testparse.go where helpful.`,
    `Empty single-file HTML apps get a browser/file smoke check candidate; existing projects use native test commands; thorough unit tests.`),
  task('T10', 'Wire post-write verification',
    'internal/agent/loop.go internal/tools/tools.go internal/hooks/hooks.go internal/agent/loop_test.go',
    `Use existing VerifyNeeded and hook plumbing. After write-class tools succeed, run verification (via T9's verify.go discovery) before the final answer when policy (T8) requires it. Feed a failed verification result back to the model. Cap verify/fix iterations to prevent infinite loops.`,
    `Simple app generation verifies automatically; failed verification triggers one or more fix attempts; final output includes the verification command/result. Preserve T8 config + T9 verify.go APIs.`),
  task('T11', 'Browser smoke for frontend artifacts',
    'internal/tools/browser_smoke.go internal/tools/browser_smoke_test.go',
    `Create browser_smoke.go: a lightweight smoke path for HTML/static frontend files — file loads; no syntax/runtime error during load; expected controls exist when inferable. Prefer existing browser tooling when configured; otherwise static DOM/JS parse. Expose a function verify.go (T9) can call; do NOT edit verify.go yourself (T9/T10 own it) — instead provide a clean exported API and note the wiring needed.`,
    `Basic HTML apps get an actual load check; verification failure is concise and actionable.`),
]
const waveB = await runChain(B_chain, 'Wave B: Verification system')

phase('Gate B')
let gB = await gate('B', 'Gate B')
gB = await repairIfNeeded(gB, 'B', 'Gate B')

// ===========================================================================
// WAVE C — Token reduction. Sequential chain (shared prompts.go).
// ===========================================================================
phase('Wave C: Token reduction')

const C_chain = [
  task('T12', 'Add small task mode',
    'internal/agent/prompts.go internal/agent/prompts_test.go internal/agent/loop.go',
    `Detect simple empty-directory tasks and short file-generation prompts. Use a smaller system/context package: no giant repo instructions unless relevant; no full tool docs beyond selected tools; concise engineering policy. Keep normal mode for complex repo edits. (loop.go also touched by T10 in a prior wave — preserve those changes.)`,
    `One-file app generation input tokens drop materially; complex-task behavior unchanged; tests cover the small-task detection.`),
  task('T13', 'Cache static prompt sections',
    'internal/agent/prompts.go internal/agent/cache_test.go',
    `Cache rendered static instructions/tool descriptions per config hash. Avoid reinjecting duplicate static blocks across turns when the provider supports cache metadata. Preserve correctness across config/profile changes. Runs AFTER T12 on prompts.go — preserve its small-task mode.`,
    `Repeated turns in one session reduce billable/reported prompt load where supported; cache invalidates on config change (tested).`),
  task('T14', 'Trim tool descriptions dynamically',
    'internal/agent/prompts.go internal/tools/tools.go',
    `Include full docs only for tools likely needed; short descriptions for inactive tools; let the model request more tool detail if needed. Runs AFTER T12/T13 on prompts.go and after T10 on tools.go — preserve their changes.`,
    `Small tasks do not carry every tool's full manual; tool-call accuracy preserved (no test regressions).`),
]
const waveC = await runChain(C_chain, 'Wave C: Token reduction')

phase('Gate C')
let gC = await gate('C', 'Gate C')
gC = await repairIfNeeded(gC, 'C', 'Gate C')

// ===========================================================================
// WAVE D — Evals + UX smoke. Disjoint, parallel.
// ===========================================================================
phase('Wave D: Evals + smoke')

const D_eval = [task('T15', 'Add parity eval suite',
  'internal/eval/fixtures.go internal/eval/task.go internal/eval/eval_test.go docs/evals/codex-parity.md',
  `Add recurring tasks: todo app, calculator, notes app, quiz app, small Go bug fix, small Node test fix, frontend build with verification. Capture per task: success/fail, changed files, verification run, tokens, elapsed time. Wire a \`bharatcode eval run codex-parity\` entry point if one does not exist (check internal/eval and internal/cmd first).`,
  `bharatcode eval run codex-parity gives a stable quality signal; report includes task/success/files/verification/tokens/elapsed.`)]

const D_smoke = [task('T16', 'Add UX regression script',
  'scripts/ux-smoke.sh scripts/README.md',
  `Create scripts/ux-smoke.sh that exercises normal run, JSON run, headless TUI capture, and doctor, asserting: no noisy redraw flood in headless mode; changed files printed; doctor shows ChatGPT status; clean exit. Make it executable and documented in scripts/README.md.`,
  `One script catches the exact regressions found in user testing; it runs and asserts (use a built binary path argument).`)]

const waveD = (await parallel([
  () => runChain(D_eval, 'Wave D: Evals + smoke'),
  () => runChain(D_smoke, 'Wave D: Evals + smoke'),
])).filter(Boolean).flat()

phase('Gate D')
let gD = await gate('D', 'Gate D')
gD = await repairIfNeeded(gD, 'D', 'Gate D')

// ===========================================================================
// WAVE E — Docs + release. Disjoint, parallel.
// ===========================================================================
phase('Wave E: Docs + release')

const E_docs = [task('T17', 'Document new behavior',
  'README.md docs/install.md docs/modules/cmd.md web/app/docs/cli/page.tsx',
  `Document: bharatcode auth chatgpt; doctor ChatGPT status; BHARATCODE_HEADLESS=1; changed-file summaries; verification policy (now implemented in Wave B). Keep web docs page consistent with CLI docs. (If web/app/docs/cli/page.tsx path differs, find the actual CLI docs page under web/ and update it.)`,
  `A new user can understand setup and automation behavior from docs.`)]

const E_release = [task('T18', 'Release checklist',
  'docs/release/codex-parity.md .github/workflows/ci.yml',
  `Add a release gate doc docs/release/codex-parity.md and wire CI: go test ./...; parity eval subset (T15); UX smoke script (T16); install-method smoke for npm/brew/source where possible. Add steps to .github/workflows/ci.yml — do NOT touch release.yml (just changed for npm token auth). Keep CI green and fast.`,
  `A release cannot regress the main Codex-parity fixes unnoticed.`)]

const waveE = (await parallel([
  () => runChain(E_docs, 'Wave E: Docs + release'),
  () => runChain(E_release, 'Wave E: Docs + release'),
])).filter(Boolean).flat()

phase('Gate E')
let gE = await gate('E', 'Gate E')
gE = await repairIfNeeded(gE, 'E', 'Gate E')

// ===========================================================================
// FINAL REPORT
// ===========================================================================
phase('Final report')
const all = [...waveA, ...waveB, ...waveC, ...waveD, ...waveE]
const report = await agent(`${CTX}

All 18 tasks have been implemented across 5 waves. Per-task records:
${JSON.stringify(all, null, 2)}
Gate results: A=${JSON.stringify(gA)} B=${JSON.stringify(gB)} C=${JSON.stringify(gC)} D=${JSON.stringify(gD)} E=${JSON.stringify(gE)}
Run \`cd ${REPO} && go build ./... && go test ./...\` ONE final time to confirm the whole tree is green.
Then write a concise engineering report: per-task status (done/partial/blocked) in one line each, the final build/test result, any tasks needing human follow-up, and the net new files. Be honest about partials and anything unverified.`,
  { label: 'final-report', phase: 'Final report' })

return { tasks: all, gates: { gA, gB, gC, gD, gE }, report }
