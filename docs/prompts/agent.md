# Agent prompts

Copy-paste prompts for AI coding agents (Gemini CLI, Codex, Claude Code) building BharatCode. Each prompt is self-contained — the agent does not need other context.

## /goal — single-module build

Paste this into the agent. Replace `<MODULE>` with the module name (e.g., `util`, `db`, `llm`).

```
/goal Build the <MODULE> module of BharatCode.

Read these first, in order:
1. AGENTS.md (hard rules, locked stack)
2. docs/architecture.md (where this module sits in the DAG)
3. docs/modules/<MODULE>.md (your spec)
4. docs/modules/*.md for every module listed under your "Dependencies" section

Then implement it under internal/<MODULE>/ following the spec exactly.

Constraints:
- Build only from the specs in this repo; they are your only source.
- Locked stack per AGENTS.md — no deviations without an ADR in docs/decisions/.
- Tests required for every public function. Use testify/require, t.TempDir, t.Setenv, httptest.
- gofumpt -w . and go test ./... must pass before commit.
- Commit with semantic message: feat(<MODULE>): <one-line summary>.
- Update docs/modules/<MODULE>.md with an "## Implementation status" section listing what was built.

When done, report: files created, test pass count, line count, deviations.
```

## /parallel — multi-module fan-out

Run multiple agents concurrently on independent modules from the same build wave (see `docs/architecture.md#build-order`). Open one terminal per module. In each terminal:

```
/goal <MODULE-NAME>
```

Example — Wave 2 of the build order (all 4 modules have no inter-dependencies):

```
Terminal A: /goal db
Terminal B: /goal pubsub
Terminal C: /goal config
Terminal D: (wait — depends on db, pubsub)
```

Wave 3 (after Wave 2 completes):

```
Terminal A: /goal message
Terminal B: /goal ledger
Terminal C: /goal lsp
Terminal D: /goal shell
Terminal E: /goal permission
```

Rule: never start a module before every entry in its `Dependencies` section is committed to main.

## /review — module review

After a module is built, run a second agent to review:

```
/goal Review the <MODULE> module against its spec.

Read docs/modules/<MODULE>.md, then read every file under internal/<MODULE>/.

Check:
1. Every public type and function in the spec exists with the documented signature.
2. Every numbered acceptance criterion has a corresponding test.
3. Dependencies match the spec exactly (no extras, no missing).
4. Locked stack respected (AGENTS.md §2). No unauthorized imports.
5. go test ./internal/<MODULE>/... passes.
6. gofumpt -d ./internal/<MODULE>/ reports no diff.

Report: pass/fail per check, with file:line evidence for any fail.
```

## /next — pick the next available module

```
/goal Look at the build order in docs/architecture.md and find the lowest-numbered
module whose dependencies are all committed to main but is not yet implemented.
Then run /goal <that-module>.
```

## Tips for fan-out runs

- Keep each terminal in its own git worktree or branch if you fear conflicts. Specs are independent, so module file collisions are rare — but `go.mod` and `go.sum` will conflict if two agents add deps simultaneously. Serialize that one merge.
- Capture each agent's final commit SHA in a tracking file (`docs/build-log.md`) so you can replay or attribute.
- After every 3-5 module commits, run `/review` on all of them in batch to catch drift early.

## Naming convention for ADRs

When an agent must deviate (e.g., needs a library not in the locked stack):

```
docs/decisions/YYYY-MM-DD-<short-slug>.md
```

Contents: question, options considered, choice, reasoning, scope of impact. One page max.
