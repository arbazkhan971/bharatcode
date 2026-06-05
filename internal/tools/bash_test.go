package tools

import (
	"context"
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/permission"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/shell"
	"github.com/stretchr/testify/require"
)

func TestRegistryListsShellTools(t *testing.T) {
	registry := NewRegistry(shellDeps(t, nil))
	names := make([]string, 0, len(registry.List()))
	for _, tool := range registry.List() {
		names = append(names, tool.Name())
	}
	require.Equal(t, []string{
		"bash",
		"diagnostics",
		"edit",
		"glob",
		"grep",
		"job_kill",
		"job_output",
		"ls",
		"multiedit",
		"navigate",
		"symbols",
		"todo",
		"view",
		"web_fetch",
		"web_search",
		"write",
	}, names)
}

func TestBashRunsCommand(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	})).Get("bash")
	require.True(t, ok)

	result, err := tool.Run(context.Background(), json.RawMessage(`{"command":"echo -n hello"}`))
	require.NoError(t, err)
	require.False(t, result.IsError)
	// Output now includes the exit-code header on the first line.
	require.Contains(t, result.Content, "[exit 0 | Completed]")
	require.Contains(t, result.Content, "hello")
	require.NotEmpty(t, result.Metadata["job_id"])
}

// TestBashExitCodeHeaderAlwaysPresent asserts that every command result includes
// the "[exit N | Status]" header regardless of success or failure.
func TestBashExitCodeHeaderAlwaysPresent(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	})).Get("bash")
	require.True(t, ok)

	for _, tc := range []struct {
		name     string
		cmd      string
		wantExit string
	}{
		{"success", `echo ok`, "[exit 0 | Completed]"},
		{"failure", `exit 1`, "[exit 1 | Failed]"},
		{"nonzero", `exit 42`, "[exit 42 | Failed]"},
	} {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{"command": tc.cmd}))
			require.NoError(t, err)
			require.Contains(t, result.Content, tc.wantExit,
				"exit-code header must appear in Content for command %q", tc.cmd)
		})
	}
}

// TestBashNoisySuccessIsFiltered asserts that a successful command's output is
// run through the outputfilter engine: the filter notice is injected and the
// exit-code header is always present. We exercise the go-build filter which
// matches "go build" prefix and strips blank lines.
func TestBashNoisySuccessIsFiltered(t *testing.T) {
	// formatJob is the internal function wired into the bash tool. We test it
	// directly here (same package) to avoid spawning an actual "go build" process.
	job := shell.Job{
		ID:       "test-job-1",
		Command:  "go build ./...",
		Status:   shell.StatusCompleted,
		ExitCode: 0,
		Stdout:   "\n\n\n", // only blank lines — go-build filter strips them → on_empty fires
	}
	content := formatJob(job)
	// Exit-code header must be present.
	require.Contains(t, content, "[exit 0 | Completed]")
	// The filter was applied — notice line present.
	require.Contains(t, content, "[filtered by outputfilter/go-build]")
	// on_empty message for go-build is "go build: ok".
	require.Contains(t, content, "go build: ok")
}

// TestBashFailingCommandPreservesErrorLines asserts that when a command fails (non-zero
// exit), all error lines appear in the output without filtering.
func TestBashFailingCommandPreservesErrorLines(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	})).Get("bash")
	require.True(t, ok)

	// Emit a specific error string and then exit non-zero.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"command": `echo "ERROR: something went wrong"; exit 1`,
	}))
	require.NoError(t, err)
	require.True(t, result.IsError, "non-zero exit must set IsError")
	require.Contains(t, result.Content, "[exit 1 | Failed]", "exit-code header must appear")
	require.Contains(t, result.Content, "ERROR: something went wrong",
		"error message must be preserved verbatim on failure")
}

func TestBashMalformedArgs(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, nil)).Get("bash")
	require.True(t, ok)

	result, err := tool.Run(context.Background(), json.RawMessage(`{`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "invalid tool arguments")
}

func TestBashPermissionDeniedSkipsShell(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{Deny: []string{"bash:echo"}},
	})).Get("bash")
	require.True(t, ok)

	result, err := tool.Run(context.Background(), json.RawMessage(`{"command":"echo should-not-run"}`))
	require.NoError(t, err)
	require.True(t, result.IsError)
	require.Contains(t, result.Content, "permission denied")
}

func TestBackgroundJobOutputAndKill(t *testing.T) {
	registry := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	}))
	bashTool, ok := registry.Get("bash")
	require.True(t, ok)
	outputTool, ok := registry.Get("job_output")
	require.True(t, ok)
	killTool, ok := registry.Get("job_kill")
	require.True(t, ok)

	start, err := bashTool.Run(context.Background(), json.RawMessage(`{"command":"printf ready; sleep 10","background":true}`))
	require.NoError(t, err)
	require.False(t, start.IsError)
	jobID, ok := start.Metadata["job_id"].(string)
	require.True(t, ok)

	require.Eventually(t, func() bool {
		result, err := outputTool.Run(context.Background(), mustJSON(t, map[string]string{"job_id": jobID}))
		// Content now includes the exit-code header; check for "ready" as a substring.
		return err == nil && !result.IsError && strings.Contains(result.Content, "ready")
	}, 2*time.Second, 20*time.Millisecond)

	killed, err := killTool.Run(context.Background(), mustJSON(t, map[string]string{"job_id": jobID}))
	require.NoError(t, err)
	require.False(t, killed.IsError)
	require.Contains(t, killed.Content, "job "+jobID)
}

func TestCapOutputKeepsHeadAndTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 100; i++ {
		b.WriteString("line")
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	out := capOutput(b.String(), 10)

	// Head survives.
	require.Contains(t, out, "line1\n")
	require.Contains(t, out, "line5\n")
	// Tail survives — the part a head-only cap would drop.
	require.Contains(t, out, "line96")
	require.Contains(t, out, "line100")
	// The bulky middle is gone.
	require.NotContains(t, out, "line50")
	// A notice reports the elided count (100 - 5 head - 5 tail = 90).
	require.Contains(t, out, "[90 lines truncated]")

	// The rendered output stays within budget: maxLines kept + one notice line.
	require.Len(t, splitLines(out), 11)
}

func TestCapOutputShortInputUnchanged(t *testing.T) {
	in := "a\nb\nc"
	require.Equal(t, in, capOutput(in, 10))
	require.NotContains(t, capOutput(in, 10), "truncated")
}

func TestCapOutputOddBudgetFavorsTail(t *testing.T) {
	var b strings.Builder
	for i := 1; i <= 20; i++ {
		b.WriteString(strconv.Itoa(i))
		b.WriteByte('\n')
	}
	// Odd budget of 5: head=3, tail=2 (tail biased smaller, head larger here).
	out := capOutput(b.String(), 5)
	lines := splitLines(out)
	// 5 content lines + 1 notice line.
	require.Len(t, lines, 6)
	require.Contains(t, out, "[15 lines truncated]")
	// Last real line of input must be present.
	require.Contains(t, out, "20")
}

func TestBashTailOutputSurvivesLineCap(t *testing.T) {
	tool, ok := NewRegistry(shellDeps(t, &config.Config{
		Permissions: config.PermConfig{AllowAll: true},
	})).Get("bash")
	require.True(t, ok)

	// Emit far more than the 500-line cap, then a distinctive final summary line
	// that a head-only cap would discard.
	result, err := tool.Run(context.Background(), mustJSON(t, map[string]any{
		"command": `seq 1 2000; echo "FINAL-SUMMARY-MARKER"`,
	}))
	require.NoError(t, err)
	require.False(t, result.IsError)
	require.Contains(t, result.Content, "FINAL-SUMMARY-MARKER",
		"the terminal summary line must survive the bash line cap")
	require.Contains(t, result.Content, "lines truncated")
}

func shellDeps(t *testing.T, cfg *config.Config) Dependencies {
	t.Helper()
	if cfg == nil {
		cfg = &config.Config{}
	}
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("tools_shell_test", 64)
	t.Cleanup(bus.Close)
	sh := shell.New(bus)
	t.Cleanup(sh.Shutdown)
	return Dependencies{
		Config:     cfg,
		Permission: permission.New(cfg, nil),
		Shell:      sh,
		WorkDir:    t.TempDir(),
	}
}

func mustJSON(t *testing.T, v any) json.RawMessage {
	t.Helper()
	data, err := json.Marshal(v)
	require.NoError(t, err)
	return data
}
