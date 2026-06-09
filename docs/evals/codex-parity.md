# Codex-parity eval suite

The `codex-parity` suite measures how close BharatCode's agent loop feels to
Codex CLI on recurring, end-to-end work: building small apps and fixing bugs,
with the Codex-shaped discipline of **verifying before claiming done**. It runs
fully offline against a scripted stub provider, so it needs no API keys or
network access and produces a deterministic quality signal suitable for CI.

The suite lives in `internal/eval` (`task.go`, `fixtures.go`) and is exercised by
`internal/eval/eval_test.go`.

## Running

Run the suite through the CLI for the standard offline pass/fail report:

```sh
bharatcode eval --suite codex-parity
bharatcode eval --suite codex-parity --json
```

The CLI path uses the same built-in suite runner as the other evals, so it
reports task pass/fail, steps, recoveries, and aggregate pass percentage without
requiring API keys or network access.

The detailed parity metrics are also available through the Go API:

```go
report, err := eval.Runner{MaxSteps: 12}.RunCodexParity(ctx)
```

`RunCodexParity` runs `eval.CodexParitySuite()` and returns a `ParityReport`
whose per-task rows carry task / success / changed files / verification /
tokens / elapsed. `report` is JSON-serialisable, so callers can marshal it for a
machine-readable signal to diff in CI.

The CLI currently prints the standard `Report`. The parity-specific columns
(`changed_files`, `verification`, `total_tokens`, `elapsed_ns`) are exposed by
`RunCodexParity` and can be wired into a dedicated report format later if CI
needs those exact fields.

## Tasks

Each task seeds a tiny fixture, scripts the agent through the Codex shape (read
context → make the edit(s) → run a verification command → report), and checks
that the agent both edited a file *and* verified its work.

| Task             | What it builds / fixes                          | Verification     |
| ---------------- | ----------------------------------------------- | ---------------- |
| `todo-app`       | Small todo CLI app (add/list/done)              | `go build ./...` |
| `calculator`     | Calculator supporting `+ - * /`                 | `go build ./...` |
| `notes-app`      | Notes app (create/list/delete)                  | `go build ./...` |
| `quiz-app`       | Quiz app (ask questions, score answers)         | `go build ./...` |
| `go-bug-fix`     | Repair a failing Go `sum()` helper              | `go test ./...`  |
| `node-test-fix`  | Repair a failing Node `sum()` test              | `npm test`       |
| `frontend-build` | Fix a broken import so the frontend build works | `npm run build`  |

## Captured metrics

Per task the suite records a `ParityMetrics` row:

- **task / task_name** — stable ID and human-readable title.
- **passed / reason** — pass-fail and a one-line explanation. A task only passes
  when the agent edited at least one file *and* ran a build/test command.
- **changed_files** — ordered, de-duplicated list of paths the agent wrote or
  edited (derived from `edit`/`write`/`multiedit` tool inputs).
- **verified / verification** — whether the agent ran a verification command and,
  if so, which one (`go build`, `go test`, `npm test`, `npm run build`, …).
- **input_tokens / output_tokens / total_tokens** — scripted token usage.
- **steps** — provider turns taken.
- **elapsed_ns** — wall-clock time for the task run.

`ParityReport` aggregates these into `passed`, `failed`, `pass_percent`,
`verified`, `total_tokens`, and `total_elapsed_ns`.

## Why it is stable

Token totals and changed-file lists are derived from the task scripts rather than
sampled from a live model, so two runs return identical metrics. This is what
lets the suite gate CI: any regression in the agent loop that drops a file edit
or skips verification flips a task to `FAIL` deterministically.

## CLI wiring

`CodexParitySuite()` is registered in `eval.BuiltinSuites()`, so
`bharatcode eval --list` includes `codex-parity` and
`bharatcode eval --suite codex-parity` runs it through the standard offline
suite runner. A future enhancement can add a parity-specific CLI report that
calls `RunCodexParity` directly when the release gate needs the detailed
changed-file, verification, token, and elapsed-time fields.
