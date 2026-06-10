export const meta = {
  name: "parity-p1",
  description: 'P1 of the parity work: consolidate the UI event fan-in (one stream) and introduce a thin workspace/service-accessor seam so the TUI depends on an interface, not concrete deps. Sequential chain; heavy build/test runs on the ldp devbox (go1.24); each step gated green before the next.',
  phases: [{ title: 'P1: event+workspace seam' }, { title: 'Gate' }],
}

const REPO = '/Users/arbaz/bharatcode'
// Heavy build/test runs on ldp. Agents edit locally in REPO, then sync to ldp and build/test there.
const LDP_ENV = 'export PATH="$HOME/sdk/go1.24.4/bin:$PATH"; export GOPATH="$HOME/go" GOCACHE="$HOME/.cache/go-build"; cd ~/bc-work/wt'
const SYNC = `# sync local edits to ldp working copy, then build+test there:
#   cd ${REPO} && git add -A && git stash create  (or commit to a temp ref) — simplest: rsync the tree
#   rsync -a --delete --exclude .git --exclude node_modules --exclude web/out ${REPO}/ ldp:~/bc-work/wt/
#   ssh ldp '${LDP_ENV} && go build ./... && go test ./internal/<pkg>/...'
RSYNC: rsync -a --delete --exclude '.git' --exclude 'node_modules' --exclude 'web/out' --exclude 'web/node_modules' ${REPO}/ ldp:~/bc-work/wt/
REMOTE BUILD: ssh ldp '${LDP_ENV} && go build ./...'
REMOTE TEST:  ssh ldp '${LDP_ENV} && go test ./internal/app/... ./internal/tui/... ./internal/pubsub/...'`

const CTX = `You are implementing P1 of the BharatCode parity architecture work, in the Go repo at ${REPO} (module github.com/arbazkhan971/bharatcode).
P1 GOAL: give BharatCode the two foundational boundaries it lacks — (a) a CONSOLIDATED UI EVENT STREAM (today the TUI subscribes to multiple separate sources: app.Bus (pubsub.Topic[agent.Event]) AND pubsub.PermissionRequests AND ad-hoc channels — fan these into ONE stream the UI subscribes to once), and (b) a thin WORKSPACE/SERVICE-ACCESSOR seam so the TUI depends on a small interface, not the concrete deps struct.
EXISTING (extend, do not rebuild): internal/pubsub has a generic Topic[T] (Subscribe/Publish). internal/app/app.go has App + a Bus (pubsub.Topic[agent.Event]) wired at New(). internal/tui subscribes via m.deps.Bus.Subscribe() (agentrun.go:231) and pubsub.PermissionRequests.Subscribe() (tui.go:1903). This is a SURGICAL consolidation + interface extraction, NOT a from-scratch rewrite. Keep it minimal and backward-compatible.

HARD RULES:
- Match surrounding Go style/comment density. NEVER reference any external project or provenance anywhere (code/comments/commits). Frame everything as BharatCode-native.
- HEAVY BUILD/TEST RUNS ON THE ldp DEVBOX (local disk is 96% full and local go is 1.24 but we keep load off it). After editing files in ${REPO}, sync + build + test on ldp:
${SYNC}
- The tree MUST stay green (build + the affected package tests) on ldp before you report done.
- Preserve all existing behavior and passing tests; this is a refactor that adds a seam, not a behavior change.`

const STEP = {
  type:'object', additionalProperties:false,
  required:['step','status','summary','files','remoteBuildOk','remoteTestsOk'],
  properties:{
    step:{type:'string'}, status:{type:'string',enum:['done','partial','blocked']},
    summary:{type:'string'}, files:{type:'array',items:{type:'string'}},
    remoteBuildOk:{type:'boolean'}, remoteTestsOk:{type:'boolean'},
    testCmd:{type:'string'}, notes:{type:'string'},
  },
}
const RV = {
  type:'object', additionalProperties:false, required:['step','verdict','findings'],
  properties:{ step:{type:'string'}, verdict:{type:'string',enum:['approve','revise']},
    findings:{type:'array',items:{type:'object',additionalProperties:false,required:['severity','issue','fix'],
      properties:{severity:{type:'string',enum:['blocker','major','minor']},issue:{type:'string'},fix:{type:'string'}}}}},
}

async function step(s) {
  const impl = await agent(`${CTX}

STEP ${s.id}: ${s.title}
${s.body}
After editing, SYNC TO ldp and build+test there (commands above). Return structured result with remoteBuildOk/remoteTestsOk reflecting the ldp run.`,
    { label:`p1:${s.id}`, phase:'P1: event+workspace seam', schema:STEP })
  if (!impl) return { step:s.id, status:'blocked' }
  const rv = await agent(`${CTX}

ADVERSARIAL review of STEP ${s.id}: ${s.title}. Implementer reported: ${JSON.stringify(impl)}
Read the actual changed files and verify: the consolidation/seam is correct and minimal; existing subscriptions still work; no behavior regressed; build+tests green ON ldp (re-run if unsure: ${SYNC}); no external-project references; idiomatic Go. Default 'revise' if the seam is leaky, the fan-in misses an event source, or remote tests aren't actually green.`,
    { label:`p1-review:${s.id}`, phase:'P1: event+workspace seam', schema:RV })
  if (rv && rv.verdict==='revise' && rv.findings?.length) {
    await agent(`${CTX}

Apply these P1 review findings for STEP ${s.id}: ${JSON.stringify(rv.findings)}. Re-sync to ldp, rebuild+retest there, report.`,
      { label:`p1-fix:${s.id}`, phase:'P1: event+workspace seam', schema:STEP })
  }
  return { step:s.id, status:impl.status, summary:impl.summary, files:impl.files, review:rv?.verdict }
}

phase('P1: event+workspace seam')

// Sequential chain — each step builds on the prior. They all touch core wiring
// (pubsub/app/tui) so they CANNOT be parallelized.
const results = []
for (const s of [
  { id:'P1a', title:'Design + unified event type',
    body:`Read internal/pubsub/*.go, internal/app/app.go (App, Bus, New), internal/tui/tui.go + agentrun.go (how Bus and PermissionRequests are subscribed). Introduce a single consolidated UI event abstraction: a UIEvent sum-type (or tagged struct) and one stream the UI subscribes to, that carries agent events, permission requests, and other UI-relevant notifications. Define it in pubsub (or app) WITHOUT yet migrating callers. Add focused tests. This step only ADDS the new type + a fan-in helper; it must not break existing paths.` },
  { id:'P1b', title:'Fan-in all event sources into one stream',
    body:`Wire app.New() (and Bus) so that agent events AND pubsub.PermissionRequests (and any other UI-bound channels) are fanned into the single UIEvent stream from P1a (mirror the consolidation pattern: each source feeds one broker). Use lossy publish for high-frequency token deltas and a must-deliver path for terminal/permission events if the pubsub Topic supports it (add a bounded must-deliver variant if needed). Keep the old subscriptions working in parallel for now (no caller migration yet). Test the fan-in delivers every source's events.` },
  { id:'P1c', title:'Workspace/service-accessor interface',
    body:`Introduce a small Workspace (or Session-scoped accessor) interface in a new file (e.g. internal/app/workspace.go) exposing exactly what the TUI needs from deps: the consolidated event stream Subscribe(), plus the handful of service operations the TUI calls (send prompt, permission grant/deny, session ops, current model/provider/cwd/yolo state). Implement it over the existing App/deps. Do NOT yet migrate the TUI — just provide and test the interface + impl.` },
  { id:'P1d', title:'Migrate TUI to the seam',
    body:`Migrate internal/tui to depend on the Workspace interface (P1c) and the single consolidated event stream (P1a/P1b): replace m.deps.Bus.Subscribe() and pubsub.PermissionRequests.Subscribe() (and ad-hoc channels) with one Subscribe() on the seam; route prompt-send / permission grant-deny / session ops through the interface instead of concrete deps. Keep behavior identical. Update TUI tests/mocks to the interface. Full internal/tui + internal/app + internal/pubsub tests must pass on ldp.` },
]) {
  results.push(await step(s))
}

phase('Gate')
const g = await agent(`${CTX}

GATE: sync the final tree to ldp and run the FULL suite there:
${SYNC.replace('./internal/app/... ./internal/tui/... ./internal/pubsub/...','./...')}
Report: remote build clean? full \`go test ./...\` green on ldp? list any failing packages + first error. Also confirm a provenance scan of internal/ finds no references to any external reference project (the disallowed names provided to you), excluding the legitimate charmbracelet/* deps — report provenanceClean accordingly. Do NOT fix; report only.`,
  { label:'p1-gate', phase:'Gate', schema:{type:'object',additionalProperties:false,required:['remoteBuildOk','remoteTestsOk','failing','provenanceClean'],properties:{remoteBuildOk:{type:'boolean'},remoteTestsOk:{type:'boolean'},failing:{type:'array',items:{type:'string'}},provenanceClean:{type:'boolean'},detail:{type:'string'}}}})
log(`P1 gate: build=${g?.remoteBuildOk} tests=${g?.remoteTestsOk} provenance-clean=${g?.provenanceClean}`)
return { results, gate:g }
