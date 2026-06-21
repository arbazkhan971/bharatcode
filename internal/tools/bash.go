package tools

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/offline"
	"github.com/arbazkhan971/bharatcode/internal/outputfilter"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/shell"
)

// filterEngine is a package-level singleton; all filter regexes are compiled
// once at startup. The Engine is goroutine-safe (read-only after init).
var filterEngine = outputfilter.NewEngine()

// maxBashLineLength caps how many characters of a single output line survive in
// the rendered result. The line-count cap (capOutput) bounds how many lines are
// kept, but a single pathological line — a minified bundle cat'd to stdout, a
// one-line JSON curl, a build emitting one enormous line — passes that cap
// untouched and can dominate the context budget on its own. Truncating per line
// mirrors the view tool's maxViewLineLength and Claude Code's Read.
const maxBashLineLength = 2000

// cwdSentinel is the unique prefix the CWD-capture wrapper injects into stderr
// after every foreground bash command. extractCWDFromStderr strips these lines
// from the displayed output and returns the final working directory.
const cwdSentinel = "__BHARATCODE_CWD_SENTINEL_v1__:"

type bashTool struct {
	permission *permission.Checker
	shell      *shell.Shell
	workDir    string
	// offline mirrors Dependencies.Offline. When set, the tool refuses commands
	// that invoke a known network-egress client (curl, scp, git push, …) so the
	// bash channel cannot defeat offline mode's "code will not leave this machine"
	// guarantee the way withholding web_fetch/web_search alone would not.
	offline bool
	// cwdMu protects lastCWD, the cached working directory from the previous
	// foreground bash call. It lets the agent cd once and have subsequent calls
	// default to that directory — matching common shell state propagation in agents.
	cwdMu   sync.Mutex
	lastCWD string
	// defaultTimeout, when positive, is the per-call timeout applied to
	// foreground bash commands that don't supply their own timeout= arg.
	// Populated from config.Options.BashDefaultTimeoutSec at construction;
	// zero means no default (unlimited). This prevents a hung command from
	// stalling the agent indefinitely — matching Claude Code's 120s default.
	defaultTimeout time.Duration
}

// cwdWrappedCommand wraps cmd so that the shell's final working directory is
// emitted to stderr as a cwdSentinel-prefixed line after the command finishes.
// A bash EXIT trap is used instead of inline code appended after cmd, for two
// reasons: (a) a command ending in a `# comment` would swallow any appended
// `;` code, and (b) an explicit `exit N` inside cmd would skip appended code —
// but the EXIT trap fires in both cases. The original exit code is preserved.
// Background commands are NOT wrapped — they run asynchronously and state
// updates there would race with subsequent foreground calls.
func cwdWrappedCommand(cmd string) string {
	// The trap function uses single-quoted '\n' so printf interprets the escape;
	// "$(pwd)" is double-quoted so the command substitution expands in the trap.
	// Two newlines separate the preamble from cmd so that cmd's first line is
	// clean even if the preamble has no trailing newline.
	return "__bhc_trap() { printf '\\n" + cwdSentinel + "%s\\n' \"$(pwd)\" >&2; }\n" +
		"trap __bhc_trap EXIT\n" +
		cmd
}

// extractCWDFromStderr scans stderr for cwdSentinel lines, returns the last
// directory found and the stderr with those lines removed. TrimSpace handles
// any trailing \r from the shell's printf output.
func extractCWDFromStderr(stderr string) (cwd, stripped string) {
	lines := strings.Split(stderr, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		if after, ok := strings.CutPrefix(line, cwdSentinel); ok {
			cwd = strings.TrimSpace(after)
		} else {
			kept = append(kept, line)
		}
	}
	// Trim trailing blank lines that appear where the sentinel was.
	for len(kept) > 0 && kept[len(kept)-1] == "" {
		kept = kept[:len(kept)-1]
	}
	if len(kept) == 0 {
		return cwd, ""
	}
	return cwd, strings.Join(kept, "\n")
}

type bashArgs struct {
	Command    string            `json:"command"`
	TimeoutSec int               `json:"timeout,omitempty"`
	Cwd        string            `json:"cwd,omitempty"`
	Background bool              `json:"background,omitempty"`
	Env        map[string]string `json:"env,omitempty"`
	Stdin      string            `json:"stdin,omitempty"`
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
    },
    "stdin": {
      "type": "string",
      "description": "Text written to the command's standard input. Use this to feed content to a command that reads stdin (e.g. patch -p1, git apply, python3 -, tee FILE, jq) instead of embedding it as a heredoc or quoted string in the command, which avoids shell-quoting bugs."
    }
  }
}`)

//go:embed bash.md
var bashDescription string

func newBashTool(deps Dependencies) Tool {
	t := &bashTool{
		permission: deps.Permission,
		shell:      deps.Shell,
		workDir:    deps.WorkDir,
		offline:    deps.Offline,
	}
	if deps.Config != nil && deps.Config.Options.BashDefaultTimeoutSec > 0 {
		t.defaultTimeout = time.Duration(deps.Config.Options.BashDefaultTimeoutSec) * time.Second
	}
	return t
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

	// In offline mode the bash tool must not become an escape hatch around the
	// "code will not leave this machine" guarantee: refuse commands that invoke a
	// known network-egress client before the command can run.
	if t.offline {
		if name, blocked := offline.EgressCommand(args.Command); blocked {
			return errorResult(fmt.Sprintf(
				"offline mode blocks the network command %q: BharatCode's offline guarantee is that code does not leave this machine. Remove the network call, or run without offline mode (unset %s) if you intend to reach the network.",
				name, offline.EnvVar,
			)), nil
		}
	}

	if t.permission != nil {
		decision, err := t.permission.Check(ctx, permission.Request{
			ToolName: "bash",
			// The registry is built once at app startup with no session id, so the
			// active turn's id is carried on the context by the agent loop. Without
			// it, session-scoped --yolo never bypasses bash (headless run --yolo
			// would hang on the unanswered prompt) and a session bash grant would
			// key under "" and leak across sessions.
			SessionID: SessionIDFromContext(ctx),
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

	opts := shell.RunOpts{Cwd: args.Cwd, Env: args.Env, Stdin: args.Stdin}
	if opts.Cwd == "" {
		// Use the last known CWD from a previous foreground call in this session,
		// falling back to the workspace root. This mirrors common agent behavior: a
		// cd in one call propagates to the next without the caller re-specifying cwd.
		t.cwdMu.Lock()
		opts.Cwd = t.lastCWD
		t.cwdMu.Unlock()
		if opts.Cwd == "" {
			opts.Cwd = t.workDir
		}
	}
	switch {
	case args.TimeoutSec > 0:
		// Explicit per-call timeout overrides everything.
		opts.Timeout = time.Duration(args.TimeoutSec) * time.Second
	case t.defaultTimeout > 0:
		// Fall back to the configured default so a hung command cannot stall
		// the agent indefinitely. The model can always pass a larger timeout=
		// arg when it genuinely needs more time (e.g. a slow build).
		opts.Timeout = t.defaultTimeout
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

	// Wrap the command to capture the final CWD via a sentinel printed to stderr.
	wrappedCmd := cwdWrappedCommand(args.Command)
	job, err := t.shell.Run(ctx, wrappedCmd, opts)
	if err != nil {
		return Result{}, fmt.Errorf("running bash command: %w", err)
	}
	// Restore the original command so outputfilter matching ("go build", "npm
	// install", etc.) works against what the caller actually typed.
	job.Command = args.Command

	// Extract the captured CWD from stderr, update the session cache, and strip
	// the sentinel lines so they don't appear in the rendered output.
	if cwd, strippedStderr := extractCWDFromStderr(job.Stderr); cwd != "" {
		t.cwdMu.Lock()
		t.lastCWD = cwd
		t.cwdMu.Unlock()
		job.Stderr = strippedStderr
	}

	content := formatJob(job)
	metadata := map[string]any{
		"job_id":    job.ID,
		"status":    job.Status,
		"exit_code": job.ExitCode,
	}
	// Structured test-result parsing: when the command is a recognized test
	// runner, extract structured failure and count information from the raw
	// combined output (not the truncated rendered content, so data near a length
	// cap is not lost) and surface it as Metadata. Failures also get a compact
	// inline summary appended so the model homes in on what broke.
	combinedOutput := job.Stdout + "\n" + job.Stderr
	if failures := parseTestFailures(args.Command, combinedOutput); len(failures) > 0 {
		metadata[MetadataTestFailures] = failures
		metadata[MetadataTestFailedCount] = len(failures)
		content += "\n" + summarizeTestFailures(failures)
	}
	if counts := parseTestCounts(args.Command, combinedOutput); counts != nil && counts.total > 0 {
		metadata[MetadataTestPassCount] = counts.passed
		metadata[MetadataTestTotalCount] = counts.total
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

	// Cap the width of every surviving line last, after filtering and the
	// line-count cap, so a single very wide line can't blow the budget. Applied
	// uniformly across the filtered, capped-success, and capped-failure branches.
	body = truncateBashLines(body, maxBashLineLength)

	return header + "\n" + body
}

// truncateBashLines caps each line of body at max characters, reusing the view
// tool's per-line truncation. Short lines (headers, notices) pass through
// unchanged; an over-long line is cut on a rune boundary with a marker noting
// how many characters were elided.
func truncateBashLines(body string, max int) string {
	if max <= 0 {
		return body
	}
	lines := splitLines(body)
	truncated := false
	for i, line := range lines {
		if t := truncateLine(line, max); t != line {
			lines[i] = t
			truncated = true
		}
	}
	if !truncated {
		return body
	}
	return joinLines(lines)
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
