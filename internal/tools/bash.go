package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/outputfilter"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

// filterEngine is a package-level singleton; all filter regexes are compiled
// once at startup. The Engine is goroutine-safe (read-only after init).
var filterEngine = outputfilter.NewEngine()

type bashTool struct {
	permission *permission.Checker
	shell      *shell.Shell
	workDir    string
}

type bashArgs struct {
	Command    string            `json:"command"`
	TimeoutSec int               `json:"timeout,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Background bool              `json:"background,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
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
    "background": {"type": "boolean"},
    "env": {
      "type": "object",
      "additionalProperties": {"type": "string"},
      "description": "Extra environment variables for this command, merged over the inherited environment. Use this instead of inline VAR=val prefixes so values survive across pipelines and quoting."
    }
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

	opts := shell.RunOpts{Cwd: args.Cwd, Env: args.Env}
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
	metadata := map[string]any{
		"job_id":    job.ID,
		"status":    job.Status,
		"exit_code": job.ExitCode,
	}
	// Structured test-result parsing: when the command is a recognized test
	// runner that reported failures, surface the failed test names (and their
	// assertion/panic line) both as Metadata and as a compact summary appended
	// to the output, so the model homes in on what broke instead of re-scanning
	// the full log. Parse the raw combined output, not the (possibly truncated)
	// rendered content, so failures near a length cap are not lost.
	if failures := parseTestFailures(args.Command, job.Stdout+"\n"+job.Stderr); len(failures) > 0 {
		metadata[MetadataTestFailures] = failures
		metadata[MetadataTestFailedCount] = len(failures)
		content += "\n" + summarizeTestFailures(failures)
	}
	return Result{
		Content:  content,
		IsError:  job.Status != shell.StatusCompleted,
		Metadata: metadata,
	}, nil
}

// formatJob assembles the combined stdout+stderr, optionally noise-filters the
// output (success-only), prepends an exit-code/status header, and returns the
// final content string that goes into Result.Content.
//
// Filtering policy:
//   - On success (exit 0, status Completed): run through the outputfilter Engine;
//     filter noise lines and cap length.  A one-line "[filtered by <name>]" notice
//     is injected when the engine matched.
//   - On failure (non-zero exit or non-Completed status): never filter — all error
//     output is passed through verbatim.  Length is still capped at 500 lines to
//     prevent runaway logs, with a clear truncation notice.
func formatJob(job shell.Job) string {
	// Merge stdout and stderr exactly as before (stderr gets "stderr:" prefix).
	combined := ""
	if job.Stdout != "" {
		combined = job.Stdout
	}
	if job.Stderr != "" {
		if combined != "" {
			combined += "\n"
		}
		combined += "stderr:\n" + job.Stderr
	}

	// One-line header: always present so the model knows the exit status without
	// having to infer it from the output text.
	header := fmt.Sprintf("[exit %d | %s]", job.ExitCode, job.Status)

	if combined == "" {
		return header
	}

	succeeded := job.ExitCode == 0 && job.Status == shell.StatusCompleted

	var body string
	if succeeded {
		// Attempt noise filtering for successful commands.
		filtered, filterName, matched := filterEngine.Apply(job.Command, combined)
		if matched {
			// Prepend a compact filter notice so the model knows lines may be absent.
			notice := fmt.Sprintf("[filtered by outputfilter/%s]", filterName)
			if filtered == "" {
				body = notice
			} else {
				body = notice + "\n" + filtered
			}
		} else {
			body = capOutput(combined, 500)
		}
	} else {
		// Failure: preserve all output; only apply a hard length cap with notice.
		body = capOutput(combined, 500)
	}

	return header + "\n" + body
}

// capOutput caps output to maxLines lines, eliding the middle when truncation
// is needed. It keeps a head and a tail so both the start of the output and its
// end survive: for command output the most actionable lines — a build/test
// failure summary, the final error, a non-zero exit trace — land at the very
// end, and a head-only cap would silently drop exactly those. A clear notice
// reports how many middle lines were removed. (The loop-level byte cap also
// keeps head+tail; this mirrors that policy at the line granularity bash uses.)
func capOutput(output string, maxLines int) string {
	lines := splitLines(output)
	if len(lines) <= maxLines {
		return output
	}
	// Split the budget between head and tail. The tail gets the extra line on an
	// odd budget since the terminal summary is usually the most valuable part.
	tailLen := maxLines / 2
	headLen := maxLines - tailLen
	dropped := len(lines) - headLen - tailLen
	head := joinLines(lines[:headLen])
	tail := joinLines(lines[len(lines)-tailLen:])
	return head + fmt.Sprintf("\n[%d lines truncated]\n", dropped) + tail
}

// splitLines splits s on "\n" without including a spurious empty element for a
// trailing newline.
func splitLines(s string) []string {
	if s == "" {
		return nil
	}
	// strings.Split("a\n","\\n") returns ["a",""] — trim before splitting.
	if len(s) > 0 && s[len(s)-1] == '\n' {
		s = s[:len(s)-1]
	}
	var out []string
	start := 0
	for i := 0; i < len(s); i++ {
		if s[i] == '\n' {
			out = append(out, s[start:i])
			start = i + 1
		}
	}
	out = append(out, s[start:])
	return out
}

func joinLines(lines []string) string {
	total := 0
	for _, l := range lines {
		total += len(l) + 1
	}
	buf := make([]byte, 0, total)
	for i, l := range lines {
		if i > 0 {
			buf = append(buf, '\n')
		}
		buf = append(buf, l...)
	}
	return string(buf)
}
