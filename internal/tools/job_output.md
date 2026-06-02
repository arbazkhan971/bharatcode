When to call this tool: use `job_output` when BharatCode needs the latest captured stdout, stderr, and status for a background command started by `bash`.

Arguments:
- `job_id` string: the opaque identifier returned by `bash` when `background` was true.

Success looks like the job output rendered as text, with metadata containing the job id, status, and exit code. A running job may return partial output; call again later if the process is still active.

Failure cases include malformed arguments, a missing `job_id` argument, an unknown or expired job id, unavailable shell support, or a job that has been evicted from the current session's in-memory job table.
