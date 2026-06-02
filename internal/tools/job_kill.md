When to call this tool: use `job_kill` when BharatCode needs to stop a background shell job that is no longer useful, is producing unwanted output, or is blocking progress.

Arguments:
- `job_id` string: the opaque identifier returned by `bash` when `background` was true.

Success looks like a short status message confirming that BharatCode sent the stop request, with job metadata when the shell still has the job record. The job id remains the same for later status checks.

Failure cases include malformed arguments, a missing `job_id` argument, unavailable shell support, or an unknown job id. Unknown jobs are treated as already gone by the shell layer.
