When to call this tool: use `bash` when BharatCode needs to run a shell command in the current workspace, inspect command output, or start a long-running background job.

Arguments:
- `command` string: the command passed to `bash -c`.
- `timeout` integer, optional: seconds before BharatCode stops the process.
- `cwd` string, optional: working directory for the command; defaults to the workspace.
- `background` boolean, optional: start the process and return a `job_id` immediately.

Output format: every result begins with a one-line header `[exit N | Status]` followed by the captured output. The header is always present — use it to read the exit code without scanning the text.

Noise filtering: for successful commands (exit 0), BharatCode runs the output through a built-in filter engine that strips predictable noise lines (e.g. `make[N]: Entering/Leaving directory`, `Compiling ...`, blank lines in build output) and caps line count. When a filter matches, a `[filtered by outputfilter/<name>]` notice appears at the top of the body. The output still looks like real command output — only noise lines are removed, not reformatted. If you need the raw unfiltered output, narrow the command itself or use `head`/`tail`/`grep`.

Failure handling: on non-zero exit, output is never filtered — all error lines are preserved verbatim. A hard cap of 500 lines is applied if exceeded: the head and tail are kept and the middle is elided with a `[N lines truncated]` notice, so a build/test failure summary at the end of the output always survives.

Truncation: each of stdout and stderr is captured up to roughly 10 MB; output beyond that is dropped and a `[truncated, N bytes]` marker is appended. To stay well under that limit and keep results focused, narrow your commands with `head`, `tail`, `grep`, or `sed -n "<start>,<end>p"` rather than dumping whole files.

Failure cases include malformed arguments, a missing command argument, permission denial, unavailable shell support, timeout, a non-zero exit status, or context cancellation.
