# scripts

Developer and CI helper scripts. Each script resolves the repository root from
its own location, so it can be run from anywhere.

## ux-smoke.sh

UX regression checks for the CLI and TUI. It guards the rough edges found in
manual user testing so a refactor that reintroduces them fails fast:

- **No redraw flood** — a captured/headless TUI session must not repaint the
  whole screen many times a second. The script drives the TUI with a
  non-interactive stdout (quiet-redraw path) and fully headless (no renderer),
  lets each idle briefly, and asserts the captured output stays small.
- **Changed files printed** — a non-interactive `run` that edits the workspace
  must print a `Changed files:` summary.
- **doctor ChatGPT status** — `doctor` must report the ChatGPT sign-in state
  when a chatgpt provider is enabled.
- **Clean exit** — `version`, `doctor`, and `run --json` exit 0 and never hang.

Usage:

```bash
go build -o bharatcode .
scripts/ux-smoke.sh ./bharatcode      # or omit the arg to use ./bharatcode
```

The binary path is the first argument and defaults to `./bharatcode` in the
repo root. The redraw-flood, doctor-ChatGPT, and clean-exit checks are offline
and always run. The `run --json` framing and `Changed files:` checks need a
usable provider (API key, local server, or ChatGPT sign-in); when none is
reachable they are **skipped**, not failed, so the suite is green on a
provider-less CI host. Set `UX_SMOKE_REQUIRE_PROVIDER=1` to promote provider
unavailability to a failure on a host that is expected to have one configured.
If a file-writing run does create a file but omits the `Changed files:` summary,
the script always fails.

Exit codes: `0` all checks passed (or were skipped), `1` a check failed, `2`
the binary argument was missing or not executable.

## post-release-smoke.sh

Post-release install verification. Given the release tag, it confirms the same
version is reachable from every install channel:

- **GitHub release + assets** — `gh release view <tag>` exists and carries the
  per-OS/arch archives plus `checksums.txt`.
- **npm** — `npm view bharatcode-cli version` matches the tag.
- **Homebrew** — installing the tapped formula reports the matching version.
- **Installed binary** — `bharatcode version` on `PATH` reports the same version.

Usage:

```bash
scripts/post-release-smoke.sh v0.2.7
```

Versions compare on their numeric core, so the GitHub tag (`v0.2.7`) and the
bare npm/brew form (`0.2.7`) match without normalizing. Each check reaches a
remote through an optional tool (`gh`, `npm`, `brew`, the installed binary); a
missing/unauthenticated/offline tool **skips** that check with a clear note
rather than failing, so `bash -n` and a dry run stay clean. Set
`POSTREL_REQUIRE_ALL=1` to promote those skips to failures on a release runner
that is expected to have every tool configured.

Exit codes: `0` all checks passed (or were skipped), `1` a check failed, `2`
the version argument was missing.

## sqlc-generate.sh

Regenerates the type-safe Go database layer from SQL via `sqlc generate`,
using the repo-root `sqlc.yaml`. Requires `sqlc` on `PATH`.

```bash
scripts/sqlc-generate.sh
```

## validate-defaults.go

Loads the embedded default config and validates it, exiting non-zero if the
shipped defaults are invalid. Run as a standalone program (it is in
`package main`, so it is excluded from the normal build):

```bash
go run scripts/validate-defaults.go
```

## docs-workflow.js

Workflow definition that builds the end-to-end `/docs` section of the Next.js
marketing site under `web/`. Consumed by the workflow runner, not run directly.
