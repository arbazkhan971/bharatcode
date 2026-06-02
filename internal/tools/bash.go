package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

type bashTool struct {
	permission *permission.Checker
	shell      *shell.Shell
	workDir    string
}

type bashArgs struct {
	Command    string `json:"command"`
	TimeoutSec int    `json:"timeout,omitempty"`
	Cwd        string `json:"cwd,omitempty"`
	Background bool   `json:"background,omitempty"`
}

var bashSchema = json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["command"],
  "properties": {
    "command": {"type": "string", "minLength": 1},
    "timeout": {"type": "integer", "minimum": 1},
    "cwd": {"type": "string"},
    "background": {"type": "boolean"}
  }
}`)

//go:embed bash.md
var bashDescription string

func newBashTool(deps Dependencies) Tool {
	return &bashTool{
		permission: deps.Permission,
		shell:      deps.Shell,
		workDir:    deps.WorkDir,
	}
}

func (t *bashTool) Name() string {
	return "bash"
}

func (t *bashTool) Description() string {
	return bashDescription
}

func (t *bashTool) Schema() json.RawMessage {
	return copySchema(bashSchema)
}

func (t *bashTool) Run(ctx context.Context, raw json.RawMessage) (Result, error) {
	args, bad := decodeArgs[bashArgs](raw)
	if bad != nil {
		return *bad, nil
	}
	if args.Command == "" {
		return errorResult("command is required"), nil
	}
	if t.shell == nil {
		return errorResult("shell is not configured"), nil
	}

	if t.permission != nil {
		decision, err := t.permission.Check(ctx, permission.Request{
			ToolName: "bash",
			Args: map[string]any{
				"command": args.Command,
				"cmd":     args.Command,
				"cwd":     args.Cwd,
			},
		})
		if err != nil {
			return errorResult("permission check failed: " + err.Error()), nil
		}
		if decision == permission.DecisionDeny {
			return errorResult("permission denied for bash"), nil
		}
	}

	opts := shell.RunOpts{Cwd: args.Cwd}
	if opts.Cwd == "" {
		opts.Cwd = t.workDir
	}
	if args.TimeoutSec > 0 {
		opts.Timeout = time.Duration(args.TimeoutSec) * time.Second
	}

	if args.Background {
		id, err := t.shell.Start(ctx, args.Command, opts)
		if err != nil {
			return Result{}, fmt.Errorf("starting background bash command: %w", err)
		}
		return Result{
			Content: "started background job " + id,
			Metadata: map[string]any{
				"job_id": id,
			},
		}, nil
	}

	job, err := t.shell.Run(ctx, args.Command, opts)
	if err != nil {
		return Result{}, fmt.Errorf("running bash command: %w", err)
	}
	content := formatJob(job)
	return Result{
		Content: content,
		IsError: job.Status != shell.StatusCompleted,
		Metadata: map[string]any{
			"job_id":    job.ID,
			"status":    job.Status,
			"exit_code": job.ExitCode,
		},
	}, nil
}

func formatJob(job shell.Job) string {
	out := ""
	if job.Stdout != "" {
		out += job.Stdout
	}
	if job.Stderr != "" {
		if out != "" {
			out += "\n"
		}
		out += "stderr:\n" + job.Stderr
	}
	if out == "" {
		out = fmt.Sprintf("status: %s, exit_code: %d", job.Status, job.ExitCode)
	}
	return out
}
