When to call this tool: use `job_list` to see every background command started by `bash` (with `background` true) that is still tracked this session — running and recently-finished alike. Useful when you have lost a job id (e.g. after the conversation was compacted) and need to recover it before calling `job_output` or `job_kill`, or to check at a glance which long-running jobs are still active.

Arguments: none.

Success looks like one line per job — `job_id  status  exit_code  command` — newest-started first, with metadata containing a `jobs` array (each entry's id, status, exit_code, and command). An empty list reports that no background jobs are currently tracked.

Note: finished jobs are evicted from the in-memory table a few minutes after they complete, so a job that ran a while ago may no longer appear. Running jobs are never evicted.
