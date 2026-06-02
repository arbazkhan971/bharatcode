When to call this tool: use `bash` when BharatCode needs to run a shell command in the current workspace, inspect command output, or start a long-running background job.

Arguments:
- `command` string: the command passed to `bash -c`.
- `timeout` integer, optional: seconds before BharatCode stops the process.
- `cwd` string, optional: working directory for the command; defaults to the workspace.
- `background` boolean, optional: start the process and return a `job_id` immediately.

Success looks like captured stdout and stderr as text, with metadata containing the job id, status, and exit code. For background commands, keep the returned `job_id` and use `job_output` to inspect progress or `job_kill` to stop it.

Failure cases include malformed arguments, a missing command argument, permission denial, unavailable shell support, timeout, a non-zero exit status, or context cancellation.
