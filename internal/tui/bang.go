package tui

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strings"
	"time"
)

// bangPattern matches an inline shell-substitution token: a "!" immediately
// followed by a backtick-delimited command, e.g. !`git status -s`. It mirrors
// the Claude Code / pi custom-command convention where the command's standard
// output is spliced into the prompt before it is sent to the model, letting a
// slash-command template embed live repository state (branch, diff, test
// output) at invocation time rather than at authoring time. The command may
// not span a line so a stray backtick elsewhere in prose cannot swallow it.
var bangPattern = regexp.MustCompile("!`([^`\n]+)`")

const (
	// bangTimeout bounds how long a single embedded command may run before it
	// is cancelled, so a hung command cannot stall prompt submission.
	bangTimeout = 30 * time.Second
	// maxBangOutputBytes caps the output spliced in for one command so a
	// chatty command cannot dominate the context window.
	maxBangOutputBytes = 16 * 1024
	// maxBangCommands bounds how many substitutions are executed for a single
	// template, guarding against a template that fans out into many commands.
	maxBangCommands = 32
)

// bangRunner runs one shell command and returns its combined output together
// with any execution error. It is injected so expandBangCommands can be
// exercised without a real shell.
type bangRunner func(cmd string) (string, error)

// expandBangCommands replaces each !`cmd` token in text with the output of
// running cmd via run. The command's combined output (trimmed of a trailing
// newline and capped at maxBangOutputBytes) is substituted in place, so a
// custom slash command can inline live context such as `git status` or `go
// test` results. A failing command is replaced with its partial output plus an
// error marker so the model still sees what happened rather than a silent gap.
// Tokens past maxBangCommands are left verbatim. When text contains no token,
// or run is nil, the input is returned unchanged.
func expandBangCommands(text string, run bangRunner) string {
	if run == nil || !strings.Contains(text, "!`") {
		return text
	}
	n := 0
	return bangPattern.ReplaceAllStringFunc(text, func(match string) string {
		// match is exactly !`...`; strip the leading "!`" and trailing "`".
		cmd := strings.TrimSpace(match[2 : len(match)-1])
		if cmd == "" {
			return match
		}
		n++
		if n > maxBangCommands {
			return match
		}
		out, err := run(cmd)
		out = strings.TrimRight(out, "\n")
		if len(out) > maxBangOutputBytes {
			out = out[:maxBangOutputBytes] + "\n[output truncated]"
		}
		if err != nil {
			if out == "" {
				return fmt.Sprintf("[command failed: %s]", err.Error())
			}
			return out + fmt.Sprintf("\n[command exited with error: %s]", err.Error())
		}
		return out
	})
}

// runBangCommand executes cmd with bash in the workspace root, bounded by
// bangTimeout, returning the combined stdout+stderr. A non-zero exit yields the
// captured output together with a non-nil error so callers can surface both.
func (m *model) runBangCommand(cmd string) (string, error) {
	base := m.ctx
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithTimeout(base, bangTimeout)
	defer cancel()

	c := exec.CommandContext(ctx, "bash", "-c", cmd)
	c.Dir = m.workspaceRoot
	var buf bytes.Buffer
	c.Stdout = &buf
	c.Stderr = &buf
	err := c.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return buf.String(), fmt.Errorf("timed out after %s", bangTimeout)
	}
	if err != nil {
		return buf.String(), err
	}
	return buf.String(), nil
}
