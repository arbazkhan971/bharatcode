# cmd

**Path:** `internal/cmd/`
**Status:** First-pass implemented

## Purpose

The `cmd` module is BharatCode's Cobra-rooted command tree. Most users will invoke the binary with no subcommand and drop into the TUI, but the CLI also exposes a headless single-prompt mode for scripts and CI, OAuth login for providers that support it, model and provider management, session inspection, usage statistics, budget configuration, model-pack refresh, config editing, and a version stamp. `cmd` is the layer between `main.go` and the rest of the system: it parses flags, builds an `*app.App` via `app.New`, hands control to either `tui.Run` or a non-interactive subcommand handler, then calls `app.Close` and exits.

This module exists to keep flag parsing, help text, exit codes, table formatting, and OS-keyring access out of the agent core and the TUI. Subcommands are the canonical surface for scriptable usage and the integration point CI tooling will target; their stability matters even when the TUI evolves.

## Public interface

```go
// Package cmd is the Cobra command tree for the bharatcode binary.
// main.go calls Execute; nothing else in the codebase imports cmd.
package cmd

// Execute parses os.Args, runs the matched subcommand, and exits
// with the appropriate status. It is the single entry point called
// from main.go. Execute writes errors to stderr with a red "Error:"
// prefix and exits non-zero on failure. It blocks for the lifetime
// of the chosen subcommand and never returns a value.
func Execute()

// Each subcommand is constructed by an unexported factory and
// attached to the root in init. The factories below are documented
// because tests construct them in isolation.

func newRootCmd() *cobra.Command       // default: opens TUI in cwd
func newRunCmd() *cobra.Command        // bharatcode run "<prompt>"
func newLoginCmd() *cobra.Command      // bharatcode login <provider>
func newLogoutCmd() *cobra.Command     // bharatcode logout <provider>
func newAuthCmd() *cobra.Command       // bharatcode auth chatgpt (OAuth/PKCE, experimental)
func newModelsCmd() *cobra.Command     // bharatcode models [switch <id>]
func newSessionsCmd() *cobra.Command   // bharatcode sessions <list|show|delete>
func newStatsCmd() *cobra.Command      // bharatcode stats
func newBudgetCmd() *cobra.Command     // bharatcode budget set --month <inr>
func newUpdateProvidersCmd() *cobra.Command // bharatcode update-providers
func newConfigCmd() *cobra.Command     // bharatcode config edit
func newVersionCmd() *cobra.Command    // bharatcode version
```

### Subcommand surface

| Command | Purpose | Output contract |
|---|---|---|
| `bharatcode` (no args) | Open TUI in current directory. | Process replaced by TUI; exit status is TUI's. |
| `bharatcode run "<prompt>"` | Headless single-prompt run, prints assistant response, exits. Flags: `--model`, `--agent`, `--yolo`, future `--json`. | Assistant text on stdout; stderr empty on success; exit 0. |
| `bharatcode login <provider>` | Interactive OAuth flow for providers that support it (Anthropic, OpenAI). Stores token in OS keyring. | Prints `Logged in to <provider>` to stdout; exit 0. |
| `bharatcode logout <provider>` | Remove stored token from keyring. | Prints `Logged out of <provider>` to stdout; exit 0 even if no token. |
| `bharatcode auth chatgpt` | Experimental "Sign in with ChatGPT" OAuth (PKCE): opens a browser, runs a loopback callback server, and stores subscription tokens so the `chatgpt` provider can use the user's own plan. Flags: `--status`, `--logout`. | Prints `Signed in to ChatGPT as <account> on the <plan> plan` and the access-token state; `--logout` prints `Signed out of ChatGPT.`; exit 0. |
| `bharatcode models` | Print all configured providers and their models. | Aligned table: `PROVIDER  MODEL  CONTEXT  INPUT$/MTOK  OUTPUT$/MTOK`. |
| `bharatcode models switch <model>` | Set default model in user config. | Prints `Default model set to <id>`; exit 0. |
| `bharatcode sessions list` | List sessions in current project. | Table: `ID  TITLE  UPDATED  MESSAGES  COST(₹)`; empty project prints `no sessions` to stderr, exit 0. |
| `bharatcode sessions show <id>` | Print session transcript. | Plain-text transcript on stdout. |
| `bharatcode sessions delete <id>` | Delete session. | Prints `Deleted session <id>`; exit 0. |
| `bharatcode stats` | Usage statistics by provider/model and by day/month. | Two tables, `--since` flag accepts `30d` / `month` / `all`. |
| `bharatcode budget set --month <inr>` | Set monthly budget cap. | Prints `Monthly budget set to ₹<n>`. |
| `bharatcode update-providers` | Refresh model packs from models.dev. | Prints `Updated N providers, M models` summary. |
| `bharatcode config edit` | Open the user config in `$EDITOR`. | Exits with editor's exit code. |
| `bharatcode version` | Print version. | Single line `bharatcode <semver> (<commit>)`; exit 0. |

Every subcommand registers `--help` automatically via Cobra and supports `--verbose` and `--config <path>` inherited from the root.

## Dependencies

- `internal/util` — `ShortPath`, `HumanBytes`, `HumanDuration` for output formatting.
- `internal/config` — load, validate, mutate user/project config (used by `models switch`, `budget set`, `config edit`).
- `internal/session` — list, show, delete sessions in non-TUI subcommands.
- `internal/ledger` — `stats` queries cumulative spend.
- `internal/agent` — `run` invokes a headless single-turn agent call.
- `internal/llm` — `models` and `update-providers` enumerate and refresh provider packs.
- `internal/tui` — root command hands control to `tui.Run`.
- `internal/app` — constructs the shared dependency graph passed to TUI and subcommands.

External (locked stack):

- `github.com/spf13/cobra` — command framework.
- `github.com/spf13/viper` — config binding for flag overrides (optional usage; only where it pays for itself).

External (not yet in `AGENTS.md` §2 locked stack — see Notes):

- `github.com/zalando/go-keyring` — secure token storage for `login`/`logout`. macOS Keychain, Linux Secret Service, Windows Credential Manager.

## Acceptance criteria

1. `go test ./internal/cmd/...` passes with `-race` on linux, darwin, windows.
2. `go vet` and `golangci-lint run` clean for `./internal/cmd/...`.
3. `TestRoot_NoArgs_StartsTUI` — invoking the root with no subcommand calls into a test seam that records "tui.Run invoked" without actually opening a terminal.
4. `TestRun_PrintsAssistantToStdout` — `bharatcode run "hello"` with a stub LLM (httptest) prints the stubbed assistant text to stdout and nothing to stderr; exit 0.
5. `TestRun_StdinPiping` — `echo "hello" | bharatcode run` (no positional prompt) reads stdin and behaves identically.
6. `TestLogin_StoresTokenInKeyring` — with a fake keyring backend, `login` writes a token under service `bharatcode` and account `<provider>`; subsequent `logout` removes it.
7. `TestLogin_KeyringUnavailable_GracefulError` — when the keyring backend returns an error, the command exits 1 with stderr `Error: keyring unavailable: <cause>` and the config is unchanged.
8. `TestModels_TableFormat` — output matches a golden file under `testdata/models_table.txt`, columns aligned with at least two spaces between fields.
9. `TestSessions_List_Empty_PrintsNoSessions` — in a project with no sessions, `sessions list` writes `no sessions` to stderr and exits 0.
10. `TestSessions_Show_NotFound_Exit1` — `sessions show bogus-id` exits 1 with stderr `Error: session bogus-id not found`.
11. `TestStats_NoSessions_ExitsCleanly` — `stats` against an empty ledger exits 0 with stdout `no usage recorded`.
12. `TestBudget_Set_PersistsToConfig` — `budget set --month 500` writes `ledger.budget_inr_per_month = 500` into the user config file.
13. `TestUpdateProviders_NetworkFailure_Exit1` — when the upstream registry returns 5xx, the command exits 1 with a clear error and does not overwrite existing packs.
14. `TestVersion_Format` — output matches regex `^bharatcode v\d+\.\d+\.\d+(-[a-z0-9.-]+)? \([0-9a-f]{7,}\)\n$`.
15. `TestHelpText_AllSubcommands` — `bharatcode <cmd> --help` for every subcommand prints non-empty usage including a `Usage:` line and an `Examples:` section where applicable.
16. `TestErrorPrefix_RedAndConsistent` — every non-success exit path produces stderr beginning with `Error:` (red when stderr is a TTY, plain otherwise via lipgloss render-off when `os.Getenv("NO_COLOR") != ""` or stderr is not a TTY).
17. `bharatcode --help` exits 0 and lists every subcommand in the table above.

## Notes for the implementer

- **One file per command.** `root.go`, `run.go`, `login.go`, `logout.go`, `models.go`, `sessions.go`, `stats.go`, `budget.go`, `update_providers.go`, `config.go`, `version.go`. Shared helpers in `cmd_util.go`.
- **`app.App` construction.** Every subcommand that touches services calls `app.New(ctx, opts)` near the top of its run function and `defer app.Close(ctx)` immediately after. Root command does the same and passes the app to `tui.Run`.
- **OS keyring not yet locked.** `zalando/go-keyring` is the proposed library because it is the maintained cross-platform Go option and has no CGO requirement on Linux when the Secret Service runs over D-Bus. Before importing, add a row to `AGENTS.md` §2 or open an ADR per `AGENTS.md` §7. Wrap it behind a `Keyring` interface (`Get(service, account) (string, error)`, `Set`, `Delete`) so tests can use an in-memory fake.
- **Verbose and config flags.** Add `--verbose` and `--config <path>` as persistent flags on the root. `--verbose` raises the `slog` level to `Debug`; `--config` overrides the default lookup path before `app.New`.
- **Table output.** Use a small aligned-string helper (compute max width per column, pad with `strings.Repeat`). Adding `olekukonko/tablewriter` is a Phase 2 question; do not pull it in for Phase 1. Tables must round-trip to a non-TTY pipe cleanly (no ANSI when not a TTY).
- **Error output.** Construct a single `printError(msg string)` helper that writes `Error: <msg>` styled red via lipgloss when `os.Stderr` is a TTY and `NO_COLOR` is unset, plain otherwise. Every command exits via this path on failure.
- **JSON mode.** `run --json` is documented as a future flag in `docs/vision.md`. Do not implement now; reject the flag with a clear "not yet supported" error if it is set in Phase 1.
- **OAuth flows.** Only Anthropic and OpenAI publish documented OAuth endpoints in 2026; other providers use API keys via env vars. `login` opens a browser via `os/exec` to the provider's auth URL, runs a localhost callback listener bound to `127.0.0.1:0` (ephemeral port), captures the code, exchanges for a token, and stores it. If browser launch fails, print the URL and instruct the user to paste it.
- **Stats query.** Implement aggregations directly against the ledger DB via `internal/ledger` helpers; do not add new SQL in `internal/cmd/`. Periods are `today`, `7d`, `30d`, `month`, `all`.
- **Exit codes.** Convention: `0` success, `1` user error (bad flags, not found, validation), `2` internal error (DB corruption, panic recovery), `3` network error (provider unreachable in `update-providers`). Document in each command's `--help`.
- **`config edit`.** Resolve the editor from `$EDITOR`, falling back to `vi` on unix and `notepad.exe` on windows. Lock the config file via `flock`-style advisory locking (`internal/util/fsext` can grow a helper if needed) so a concurrent `bharatcode` session does not stomp the edit.
- **Logging.** All subcommands route diagnostic logs through `app.App.Logger`; user-visible output goes through `cmd.Stdout` / `cmd.Stderr` (assigned from `os.Stdout` / `os.Stderr`, overridable in tests). Log messages start capitalized, no trailing period.
- **Testing.** Use `cobra.Command.SetOut`/`SetErr`/`SetArgs` to drive subcommands in tests; assert on captured buffers. Provider HTTP mocked via `httptest`. Keyring tests use an in-memory fake. `testify/require`, `t.TempDir()`, `t.Setenv()`.
- **Implementation status.** Append a `## Implementation status` section listing built subcommands, any deferred (`--json`), the keyring library actually adopted, and golden-file table snapshots committed under `testdata/`.

## Implementation status

First-pass implementation completed for Step 12:

- Added `internal/cmd` with Cobra root command, exported `Execute()`, and
  unexported command factories for `run`, `login`, `logout`, `models`,
  `models switch`, `sessions list/show/delete`, `stats`, `budget set`,
  `update-providers`, `config edit`, and `version`.
- Root no-args path builds `app.App`, resolves the default `coder` agent loop,
  and calls the TUI through a test seam.
- Added persistent `--config`, `--verbose`, `--yolo`, and `--project-dir`.
- Added small command seams for app construction, TUI launch, provider refresh,
  and credential storage so tests do not open a terminal, hit the network, or
  touch a production credential backend.
- Added focused tests for root no-args TUI dispatch, help text, version format,
  model table rendering, empty sessions, session not found, empty stats,
  monthly budget persistence, fake-keyring login/logout, keyring failure, and
  stdin prompt reading.

Deferred or intentionally narrow in this pass:

- No production keyring dependency was adopted. `login`/`logout` use a
  package-local `keyringBackend` interface with an unavailable default backend;
  tests use an in-memory implementation. This follows the task requirement to
  avoid adding a keyring dependency until the stack decision is made.
- `login` stores a supplied or stdin-entered token; full browser OAuth is
  deferred.
- `update-providers` performs a registry fetch and reports failures cleanly,
  but does not yet parse or persist model packs.
- `run --json` is not implemented.
- `stats` currently prints a compact aggregate table from the ledger summary
  helpers instead of the full provider/model and day/month breakdowns.
- `config edit` creates and opens the config file, but advisory file locking is
  deferred.
- Table tests assert the aligned table contract directly; no golden
  `testdata/models_table.txt` snapshot is committed yet.

### Behavior added after the first pass

- **`auth` command group (`auth.go`).** Holds OAuth-based sign-in flows that do
  not fit `login`'s token-paste model. Its only subcommand today is the
  experimental `auth chatgpt`, which runs a browser-based OAuth (PKCE) flow via
  `llm.LoginChatGPT`, then prints the signed-in identity and token expiry.
  `--status` calls `llm.ChatGPTStatus` and `--logout` calls `llm.LogoutChatGPT`.
  It is deliberately scoped to personal single-account use and depends on
  undocumented OpenAI endpoints, so it carries an EXPERIMENTAL banner.
- **`doctor` ChatGPT status.** When a `chatgpt` provider is enabled in the
  loaded config, `doctor` adds a **ChatGPT subscription** line reporting the
  sign-in state: `[OK] ... signed in as <email> on the <plan> plan`, or
  `[WARN] ... not signed in (run 'bharatcode auth chatgpt')`. An expired access
  token is noted as `(will refresh on next use)`. The check is skipped entirely
  when no chatgpt provider is enabled, since the credential file is irrelevant
  then.
- **`run` changed-file summary.** A non-interactive `bharatcode run` (both the
  human-formatted and `--json` paths) ends by printing a `Changed files:` block
  derived from the file tracker's `ChangesForSession`. Each path is labelled
  `created`, `modified`, or `deleted` — a path's change history is collapsed to
  its net effect (e.g. created-then-deleted nets out as deleted). Nothing is
  printed when the run touched no files.
- **Headless / quiet rendering.** The root TUI dispatch consults the TUI's
  renderer gate: `BHARATCODE_HEADLESS=1` (or `CI`, or a `dumb`/unset `TERM`)
  forces the non-rendering Bubble Tea program so scripts and PTY captures do not
  accumulate redraw noise; `BHARATCODE_QUIET_REDRAW` slows the spinner and
  dedupes the status bar when output is captured but still reports a TTY. (The
  gate itself lives in `internal/tui`; `cmd` only triggers the launch.)
