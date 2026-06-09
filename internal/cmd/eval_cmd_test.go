package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/eval"
	"github.com/stretchr/testify/require"
)

func TestEvalListCommand(t *testing.T) {
	var buf bytes.Buffer
	suites := eval.BuiltinSuites()
	err := runEvalList(&buf, suites)
	require.NoError(t, err)
	out := buf.String()
	// Header row must be present.
	require.Contains(t, out, "NAME")
	require.Contains(t, out, "TASKS")
	require.Contains(t, out, "DESCRIPTION")
	// At least one suite is listed.
	require.Contains(t, out, "go-fix")
	require.Contains(t, out, "codex-parity")
}

func TestEvalRunSuitesText(t *testing.T) {
	var buf bytes.Buffer
	suites := eval.BuiltinSuites()
	err := runEvalSuites(context.Background(), &buf, suites, "go-fix", false, 10)
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "go-fix")
	require.Contains(t, out, "PASS")
	// Should show aggregate stats.
	require.Contains(t, out, "Passed:")
	require.Contains(t, out, "Avg steps:")
}

func TestEvalRunSuitesJSON(t *testing.T) {
	var buf bytes.Buffer
	suites := eval.BuiltinSuites()
	err := runEvalSuites(context.Background(), &buf, suites, "go-fix", true, 10)
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.NotEmpty(t, lines)
	// Each line must be valid JSON.
	for _, line := range lines {
		var obj map[string]any
		require.NoError(t, json.Unmarshal([]byte(line), &obj), "line must be valid JSON: %s", line)
		require.Equal(t, "go-fix", obj["suite"])
	}
}

func TestEvalRunSuitesUnknownSuiteErrors(t *testing.T) {
	var buf bytes.Buffer
	suites := eval.BuiltinSuites()
	err := runEvalSuites(context.Background(), &buf, suites, "no-such-suite", false, 10)
	require.Error(t, err)
	require.Contains(t, err.Error(), "no suite named")
}

func TestEvalCmdCobraRegistration(t *testing.T) {
	cmd := newEvalCmd()
	require.Equal(t, "eval", cmd.Use)
	require.NotEmpty(t, cmd.Short)
	// Flags must be wired.
	require.NotNil(t, cmd.Flags().Lookup("json"))
	require.NotNil(t, cmd.Flags().Lookup("suite"))
	require.NotNil(t, cmd.Flags().Lookup("list"))
	require.NotNil(t, cmd.Flags().Lookup("max-steps"))
}

func TestEvalCmdListFlag(t *testing.T) {
	var buf bytes.Buffer
	cmd := newEvalCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--list"})
	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "go-fix")
	require.Contains(t, out, "codex-parity")
}

func TestEvalCmdLiveFlagsRegistered(t *testing.T) {
	cmd := newEvalCmd()
	// The live-provider flags must be wired and parse.
	require.NotNil(t, cmd.Flags().Lookup("live-provider"))
	require.NotNil(t, cmd.Flags().Lookup("max-tasks"))
}

func TestEvalCmdHelpShowsLiveFlags(t *testing.T) {
	var buf bytes.Buffer
	cmd := newEvalCmd()
	cmd.SetOut(&buf)
	cmd.SetArgs([]string{"--help"})
	err := cmd.ExecuteContext(context.Background())
	require.NoError(t, err)
	out := buf.String()
	require.Contains(t, out, "--live-provider")
	require.Contains(t, out, "--max-tasks")
	// The help text must mention the gate so users know how to enable it.
	require.Contains(t, out, "BHARATCODE_LIVE_EVAL")
}

func TestEvalCmdLiveProviderGatedWithoutEnv(t *testing.T) {
	// Without BHARATCODE_LIVE_EVAL=1 the command must fail fast with a clear
	// error before touching any real provider.
	t.Setenv("BHARATCODE_LIVE_EVAL", "")
	require.NoError(t, os.Unsetenv("BHARATCODE_LIVE_EVAL"))

	var buf bytes.Buffer
	cmd := newEvalCmd()
	cmd.SetOut(&buf)
	cmd.SetErr(&buf)
	cmd.SetArgs([]string{"--live-provider"})
	err := cmd.ExecuteContext(context.Background())
	require.Error(t, err)
	require.Contains(t, err.Error(), "BHARATCODE_LIVE_EVAL")
}
