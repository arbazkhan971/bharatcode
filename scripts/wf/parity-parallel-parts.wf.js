export const meta = {
  name: 'parity-parallel-parts',
  description: 'Fan out small file-disjoint parity parts as parallel agents, each building+testing on the ldp devbox in its own worktree. Each part is a NEW file in a distinct Go package (llm registry / json-repair / overflow / skills loader / eval polish) so they never conflict with each other or with the running foundational work. Each part: implement -> remote build+test -> adversarial review -> fix.',
  phases: [{ title: 'Parallel parts (ldp)' }, { title: 'Collect' }],
}

const REPO = '/Users/arbaz/bharatcode'
// Each part runs in its OWN ldp worktree off the P0 baseline, so parts never
// touch each other's tree and don't inherit unrelated in-progress edits.
const GO = 'export PATH="$HOME/sdk/go1.24.4/bin:$PATH"; export GOPATH="$HOME/go" GOCACHE="$HOME/.cache/go-build"'
const BASE = '843814e' // P0 baseline commit, present in ldp bare repo

const CTX = `You are implementing ONE small, self-contained part of the BharatCode quality work, in the Go repo (module github.com/arbazkhan971/bharatcode). The heavy build/test happens on the remote devbox "ldp".
HARD RULES:
- Match surrounding Go style + comment density. NEVER reference any external project or provenance anywhere (code/comments/commits). BharatCode-native only.
- Your part is a NEW FILE in one package. Do NOT edit existing files except, if strictly necessary, to add a single registration hook — prefer pure-additive new files so parts never conflict.
- Build + test on ldp in your OWN isolated worktree:
    ssh ldp '${GO}; cd ~/bc-work && git --git-dir=repo.git worktree add -f /tmp/part-<KEY> ${BASE} 2>/dev/null; cd /tmp/part-<KEY> && git checkout -B part-<KEY>'
  Then write your file(s) INTO /tmp/part-<KEY> on ldp (use ssh to cat/write, or scp), and:
    ssh ldp '${GO}; cd /tmp/part-<KEY> && go build ./... && go test ./internal/<pkg>/...'
  Alternatively author locally in ${REPO} for the new file, scp it to /tmp/part-<KEY>/<path>, and build/test there. The deliverable is the new file content + green ldp build/test for its package.
- Report the file path(s), the exact ldp test command + result, and whether build+tests passed on ldp.`

const PART = {
  type:'object', additionalProperties:false,
  required:['part','status','summary','files','remoteBuildOk','remoteTestsOk','testCmd'],
  properties:{
    part:{type:'string'}, status:{type:'string',enum:['done','partial','blocked']},
    summary:{type:'string'}, files:{type:'array',items:{type:'string'}},
    remoteBuildOk:{type:'boolean'}, remoteTestsOk:{type:'boolean'},
    testCmd:{type:'string'}, notes:{type:'string'},
  },
}
const RV = {
  type:'object', additionalProperties:false, required:['part','verdict','findings'],
  properties:{ part:{type:'string'}, verdict:{type:'string',enum:['approve','revise']},
    findings:{type:'array',items:{type:'object',additionalProperties:false,required:['severity','issue','fix'],
      properties:{severity:{type:'string',enum:['blocker','major','minor']},issue:{type:'string'},fix:{type:'string'}}}}},
}

async function runPart(p) {
  const spec = `${CTX}

PART ${p.key}: ${p.title}
TARGET FILE(S): ${p.files} (package ${p.pkg})
SPEC:
${p.spec}
DONE WHEN: ${p.done}
Worktree key for ldp: ${p.key}. Implement now; build+test on ldp; return structured result.`
  const impl = await agent(spec, { label:`part:${p.key}`, phase:'Parallel parts (ldp)', schema:PART })
  if (!impl) return { part:p.key, status:'blocked' }
  const rv = await agent(`${CTX}

ADVERSARIAL review of PART ${p.key}: ${p.title}. Implementer reported: ${JSON.stringify(impl)}
Inspect the actual file on ldp (ssh ldp 'cat /tmp/part-${p.key}/${p.files}') and verify DONE-WHEN: ${p.done}. Confirm: it's pure-additive (no unrelated edits), idiomatic Go, well-tested, build+tests ACTUALLY green on ldp (re-run if unsure), no external-project references. Default 'revise' if unproven.`,
    { label:`review:${p.key}`, phase:'Parallel parts (ldp)', schema:RV })
  if (rv && rv.verdict==='revise' && rv.findings?.length) {
    await agent(`${CTX}

Apply these findings for PART ${p.key} in /tmp/part-${p.key} on ldp: ${JSON.stringify(rv.findings)}. Rebuild+retest on ldp. Report.`,
      { label:`fix:${p.key}`, phase:'Parallel parts (ldp)', schema:PART })
  }
  return { part:p.key, status:impl.status, summary:impl.summary, files:impl.files, remoteBuildOk:impl.remoteBuildOk, remoteTestsOk:impl.remoteTestsOk, review:rv?.verdict }
}

phase('Parallel parts (ldp)')
const parts = [
  { key:'registry', pkg:'internal/llm', title:'Provider registry pattern', files:'internal/llm/registry.go (+registry_test.go)',
    spec:`Add a provider registry so providers are looked up by key from a concurrent map instead of hardcoded if/switch chains. Define: a Handler/Factory type matching how BharatCode constructs an llm.Provider, a Register(name, factory) and Lookup(name) backed by a sync.Map (or mutex+map), and a Registered() lister. Read internal/llm/provider*.go and registry-like code first to match the real Provider interface. Pure-additive: the registry should be adoptable by app wiring later WITHOUT changing existing provider code now.`,
    done:`registry.go compiles in package llm on ldp; registry_test.go covers register/lookup/duplicate/missing; go test ./internal/llm/... green on ldp.` },
  { key:'jsonrepair', pkg:'internal/llm', title:'Streaming tool-call JSON repair', files:'internal/llm/jsonrepair.go (+jsonrepair_test.go)',
    spec:`Add RepairToolCallJSON(raw string) (string, bool) that sanitizes/repairs imperfect tool-call argument JSON streamed from models before json.Unmarshal: escape raw control chars (0x00-0x1f) inside strings, fix invalid backslash escapes, strip unpaired UTF-16 surrogates, and (best-effort) close obviously-truncated JSON. Return the repaired string and whether a repair was applied. Pure standard-library Go.`,
    done:`jsonrepair.go compiles in package llm on ldp; table-driven test covers control chars, bad escapes, surrogates, valid-passthrough; go test ./internal/llm/... green on ldp.` },
  { key:'overflow', pkg:'internal/llm', title:'Context-overflow detection', files:'internal/llm/overflow.go (+overflow_test.go)',
    spec:`Add IsContextOverflow(err error) bool (and a string variant) that recognizes provider context-window-exceeded errors via a set of case-insensitive patterns ("context length", "maximum context", "prompt is too long", "exceeds the context window", "too many tokens", etc.). Keep the pattern list as a package var so it's extendable. Pure-additive, no provider edits.`,
    done:`overflow.go compiles in package llm on ldp; test covers several real provider error strings + negative cases; go test ./internal/llm/... green on ldp.` },
  { key:'skillloader', pkg:'internal/skills', title:'Formal SKILL.md loader', files:'internal/skills/loader.go (+loader_test.go)',
    spec:`Add LoadSkills(root string) ([]Skill, []Diagnostic, error) that walks a directory for SKILL.md (and *.md) files, parses YAML/TOML-ish frontmatter (name, description, optional disable-model-invocation), validates name ([a-z0-9-]+, matching dir where applicable), honors .gitignore-style skips if cheap, and returns non-fatal Diagnostics for malformed entries instead of failing. Read internal/skills/skills.go first to reuse/extend the existing Skill type rather than duplicating it; if a Skill type exists, add the loader around it.`,
    done:`loader.go compiles in package skills on ldp; test uses a temp dir with valid + malformed SKILL.md and asserts parsed skills + diagnostics; go test ./internal/skills/... green on ldp.` },
  { key:'evalpolish', pkg:'internal/eval', title:'Live-eval report hardening', files:'internal/eval/livereport.go (+livereport_test.go)',
    spec:`Add a small pure-additive hardening to the live-eval reporting: a LiveReport helper that aggregates per-task results (provider, model, task, passed, changed files, verify status, tokens, duration) and writes/append JSONL deterministically, plus a summary roll-up (counts pass/fail, total tokens, total duration). Read internal/eval/*.go (report.go, live.go if present) to match existing types and EXTEND them; do not duplicate. Keep additive.`,
    done:`livereport.go compiles in package eval on ldp; test covers append + summary roll-up; go test ./internal/eval/... green on ldp.` },
]
const results = (await parallel(parts.map(p => () => runPart(p)))).filter(Boolean)

phase('Collect')
const summary = await agent(`${CTX}

All parallel parts are done. Records: ${JSON.stringify(results, null, 2)}
For EACH part with remoteBuildOk && remoteTestsOk, the new file(s) live on ldp at /tmp/part-<key>/<path> on branch part-<key>. Produce a concise collection report: which parts are green and ready to merge into feat/parity (list their files + branch), which are partial/blocked and why. Also, for each green part, print the exact new file path so the orchestrator can pull it. Do NOT merge anything yourself — just report the green parts and their ldp branch/paths.`,
  { label:'collect', phase:'Collect' })
return { results, summary }
