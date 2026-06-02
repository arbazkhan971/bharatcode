package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"

	"github.com/arbazkhan971/bharatcode/internal/shell"
)

type jobOutputTool struct {
	shell *shell.Shell
}

type jobOutputArgs struct {
	JobID string `json:"job_id"`
}

var jobOutputSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["job_id"],
  "properties": {
    "job_id": {"type": "string", "minLength": 1}
  }
}`)

//go:embed job_output.md
var jobOutputDescription string

func newJobOutputTool(deps Dependencies) Tool {
	return &jobOutputTool{shell: deps.Shell}
}

func (t *jobOutputTool) Name() string {
	return "job_output"
}

func (t *jobOutputTool) Description() string {
	return jobOutputDescription
}

func (t *jobOutputTool) Schema() json.RawMessage {
	return copySchema(jobOutputSchema)
}

func (t *jobOutputTool) Run(_ context.Context, raw json.RawMessage) (Result, error) {
	args, bad := decodeArgs[jobOutputArgs](raw)
	if bad != nil {
		return *bad, nil
	}
	if args.JobID == "" {
		return errorResult("job_id is required"), nil
	}
	if t.shell == nil {
		return errorResult("shell is not configured"), nil
	}
	job, err := t.shell.Output(args.JobID)
	if err != nil {
		return errorResult(err.Error()), nil
	}
	return Result{
		Content: formatJob(job),
		IsError: job.Status == shell.StatusFailed ||
			job.Status == shell.StatusKilled ||
			job.Status == shell.StatusTimeout,
		Metadata: map[string]any{
			"job_id":    job.ID,
			"status":    job.Status,
			"exit_code": job.ExitCode,
		},
	}, nil
}

func formatJobStatus(job shell.Job) string {
	return fmt.Sprintf("job %s status: %s exit_code: %d", job.ID, job.Status, job.ExitCode)
}
