package tools

import (
	"context"
	_ "embed"
	"encoding/json"

	"github.com/arbazkhan971/bharatcode/internal/shell"
)

type jobKillTool struct {
	shell *shell.Shell
}

type jobKillArgs struct {
	JobID string `json:"job_id"`
}

var jobKillSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["job_id"],
  "properties": {
    "job_id": {"type": "string", "minLength": 1}
  }
}`)

//go:embed job_kill.md
var jobKillDescription string

func newJobKillTool(deps Dependencies) Tool {
	return &jobKillTool{shell: deps.Shell}
}

func (t *jobKillTool) Name() string {
	return "job_kill"
}

func (t *jobKillTool) Description() string {
	return jobKillDescription
}

func (t *jobKillTool) Schema() json.RawMessage {
	return copySchema(jobKillSchema)
}

func (t *jobKillTool) Run(_ context.Context, raw json.RawMessage) (Result, error) {
	args, bad := decodeArgs[jobKillArgs](raw)
	if bad != nil {
		return *bad, nil
	}
	if args.JobID == "" {
		return errorResult("job_id is required"), nil
	}
	if t.shell == nil {
		return errorResult("shell is not configured"), nil
	}
	if err := t.shell.Kill(args.JobID); err != nil {
		return errorResult(err.Error()), nil
	}
	job, err := t.shell.Output(args.JobID)
	if err != nil {
		return Result{Content: "job kill requested for " + args.JobID}, nil
	}
	return Result{
		Content: formatJobStatus(job),
		Metadata: map[string]any{
			"job_id":    job.ID,
			"status":    job.Status,
			"exit_code": job.ExitCode,
		},
	}, nil
}
