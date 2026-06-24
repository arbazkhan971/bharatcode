package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/shell"
)

type jobListTool struct {
	shell *shell.Shell
}

// jobListArgs is intentionally empty: job_list takes no arguments. Decoding into
// it still rejects malformed JSON so the tool honours the shared garbage-args
// contract rather than silently ignoring a broken call.
type jobListArgs struct{}

const maxJobListCommandRunes = 240

var jobListSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "properties": {}
}`)

//go:embed job_list.md
var jobListDescription string

func newJobListTool(deps Dependencies) Tool {
	return &jobListTool{shell: deps.Shell}
}

func (t *jobListTool) Name() string {
	return "job_list"
}

func (t *jobListTool) IsReadOnly() bool { return true }

func (t *jobListTool) Description() string {
	return jobListDescription
}

func (t *jobListTool) Schema() json.RawMessage {
	return copySchema(jobListSchema)
}

func (t *jobListTool) Run(_ context.Context, raw json.RawMessage) (Result, error) {
	if _, bad := decodeArgs[jobListArgs](raw); bad != nil {
		return *bad, nil
	}
	if t.shell == nil {
		return errorResult("shell is not configured"), nil
	}

	jobs := t.shell.List()
	if len(jobs) == 0 {
		return Result{
			Content:  "No background jobs are currently tracked.",
			Metadata: map[string]any{"jobs": []any{}},
		}, nil
	}

	var b strings.Builder
	meta := make([]map[string]any, 0, len(jobs))
	for i, job := range jobs {
		if i > 0 {
			b.WriteByte('\n')
		}
		// One compact line per job; the command can be long, so it goes last.
		fmt.Fprintf(&b, "%s\t%s\texit %d\t%s", job.ID, job.Status, job.ExitCode, quoteJobCommand(job.Command))
		meta = append(meta, map[string]any{
			"job_id":    job.ID,
			"status":    job.Status,
			"exit_code": job.ExitCode,
			"command":   job.Command,
		})
	}

	return Result{
		Content:  b.String(),
		Metadata: map[string]any{"jobs": meta},
	}, nil
}

func quoteJobCommand(command string) string {
	runes := []rune(command)
	if len(runes) > maxJobListCommandRunes {
		command = string(runes[:maxJobListCommandRunes]) + "…"
	}
	return strconv.Quote(command)
}
