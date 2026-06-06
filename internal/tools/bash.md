When to call this tool: use `bash` when BharatCode needs to run a shell command in the current workspace, inspect command output, or start a long-running background job.

Arguments:
- `command` string: the command passed to `bash -c`.
- `timeout` integer, optional: seconds before BharatCode stops the process.
- `cwd` string, optional: working directory for the command. When omitted, defaults to the directory left by the previous foreground bash call in this session (persistent CWD); if no previous call has changed the directory, falls back to the workspace root. An explicit `cwd` overrides the cached value but does not update it — only a `cd` executed inside `command` advances the session CWD.
- `background` boolean, optional: start the process and return a `job_id` immediately.
- `env` object (string→string), optional: extra environment variables merged over the inherited environment for this command only. Prefer this to inline `VAR=val command` prefixes, which break across pipes (`VAR=val a | b` sets `VAR` only for `a`) and subshells.
- `stdin` string, optional: text written to the command's standard input. Use this to feed content to a command that reads stdin (e.g. `patch -p1`, `git apply`, `python3 -`, `tee FILE`, `jq`) instead of embedding it as a heredoc or quoted string in `command`, which avoids shell-quoting bugs. When omitted, the command sees no input (immediate EOF).

Output format: every result begins with a one-line header `[exit N | Status]` followed by the captured output. The header is always present — use it to read the exit code without scanning the text.

Noise filtering: for successful commands (exit 0), BharatCode runs the output through a built-in filter engine that strips predictable noise lines (e.g. `make[N]: Entering/Leaving directory`, `Compiling ...`, blank lines in build output) and caps line count. When a filter matches, a `[filtered by outputfilter/<name>]` notice appears at the top of the body. The output still looks like real command output — only noise lines are removed, not reformatted. If you need the raw unfiltered output, narrow the command itself or use `head`/`tail`/`grep`.

Failure handling: on non-zero exit, output is never filtered — all error lines are preserved verbatim. A hard cap of 500 lines is applied if exceeded: the head and tail are kept and the middle is elided with a `[N lines truncated]` notice, so a build/test failure summary at the end of the output always survives.

Wide-line capping: independent of the line-count cap, any single output line longer than 2000 characters is truncated on a character boundary with a `… [N characters truncated]` marker. This keeps a pathological line — a minified bundle, a one-line JSON response, a build emitting one enormous line — from dominating the result on its own. Pipe such output through `jq`, `head -c`, or `fold` if you need the full content.

Truncation: each of stdout and stderr is captured up to roughly 10 MB; output beyond that is dropped and a `[truncated, N bytes]` marker is appended. To stay well under that limit and keep results focused, narrow your commands with `head`, `tail`, `grep`, or `sed -n "<start>,<end>p"` rather than dumping whole files.

Offline mode: when BharatCode runs in offline mode (the `--offline` flag or `BHARATCODE_OFFLINE`), commands that invoke a known network-egress client — `curl`, `wget`, `scp`, `sftp`, `rsync`, `ssh`, `nc`/`netcat`, `socat`, `git push`/`pull`/`fetch`/`clone`, and similar — are refused before they run, because offline mode guarantees code does not leave the machine. Accomplish the task without reaching the network, or tell the user to disable offline mode if network access is genuinely required.

Failure cases include malformed arguments, a missing command argument, permission denial, a blocked network command in offline mode, unavailable shell support, timeout, a non-zero exit status, or context cancellation.
