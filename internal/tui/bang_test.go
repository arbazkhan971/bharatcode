package tui

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestExpandBangCommands_SubstitutesOutput(t *testing.T) {
	run := func(cmd string) (string, error) {
		require.Equal(t, "git status -s", cmd)
		return " M main.go\n", nil
	}
	got := expandBangCommands("status:\n!`git status -s`\ndone", run)
	require.Equal(t, "status:\n M main.go\ndone", got)
}

func TestExpandBangCommands_MultipleTokens(t *testing.T) {
	run := func(cmd string) (string, error) {
		switch cmd {
		case "branch":
			return "auto/x\n", nil
		case "count":
			return "3", nil
		default:
			return "", fmt.Errorf("unexpected %q", cmd)
		}
	}
	got := expandBangCommands("on !`branch` with !`count` files", run)
	require.Equal(t, "on auto/x with 3 files", got)
}

func TestExpandBangCommands_NoToken(t *testing.T) {
	called := false
	run := func(string) (string, error) { called = true; return "", nil }
	in := "plain prompt, no shell here"
	require.Equal(t, in, expandBangCommands(in, run))
	require.False(t, called, "runner must not fire when there is no token")
}

func TestExpandBangCommands_NilRunner(t *testing.T) {
	in := "embed !`echo hi`"
	require.Equal(t, in, expandBangCommands(in, nil))
}

func TestExpandBangCommands_EmptyCommandUntouched(t *testing.T) {
	called := false
	run := func(string) (string, error) { called = true; return "x", nil }
	in := "edge !`` case"
	require.Equal(t, in, expandBangCommands(in, run))
	require.False(t, called)
}

func TestExpandBangCommands_ErrorMarker(t *testing.T) {
	run := func(string) (string, error) {
		return "partial out\n", errors.New("exit status 1")
	}
	got := expandBangCommands("!`go test ./...`", run)
	require.Equal(t, "partial out\n[command exited with error: exit status 1]", got)
}

func TestExpandBangCommands_ErrorNoOutput(t *testing.T) {
	run := func(string) (string, error) {
		return "", errors.New("command not found")
	}
	got := expandBangCommands("!`nope`", run)
	require.Equal(t, "[command failed: command not found]", got)
}

func TestExpandBangCommands_OutputCapped(t *testing.T) {
	big := strings.Repeat("a", maxBangOutputBytes+500)
	run := func(string) (string, error) { return big, nil }
	got := expandBangCommands("!`spew`", run)
	require.True(t, strings.HasSuffix(got, "[output truncated]"))
	require.Less(t, len(got), maxBangOutputBytes+len("\n[output truncated]")+1)
}

func TestExpandBangCommands_CommandCountGuard(t *testing.T) {
	n := 0
	run := func(string) (string, error) { n++; return "x", nil }
	var b strings.Builder
	for i := 0; i < maxBangCommands+5; i++ {
		b.WriteString("!`c` ")
	}
	expandBangCommands(b.String(), run)
	require.Equal(t, maxBangCommands, n, "runner must stop after the guard limit")
}

func TestExpandBangCommands_DoesNotCrossNewline(t *testing.T) {
	called := false
	run := func(string) (string, error) { called = true; return "", nil }
	// A backtick opened on one line is not closed on the next, so the token
	// must not match and the runner must never fire.
	in := "!`first\nsecond`"
	require.Equal(t, in, expandBangCommands(in, run))
	require.False(t, called)
}

func TestRunBangCommand(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip("bash not available")
	}
	dir := t.TempDir()
	m := &model{ctx: context.Background(), workspaceRoot: dir}

	out, err := m.runBangCommand("echo hi")
	require.NoError(t, err)
	require.Equal(t, "hi\n", out)

	// Runs in the workspace root.
	out, err = m.runBangCommand("pwd")
	require.NoError(t, err)
	require.Equal(t, dir, strings.TrimSpace(out))

	// Non-zero exit surfaces both the captured output and an error.
	out, err = m.runBangCommand("echo boom; exit 3")
	require.Error(t, err)
	require.Equal(t, "boom", strings.TrimSpace(out))
}
