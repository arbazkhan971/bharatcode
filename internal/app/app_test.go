package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/llm"
)

func TestNew_DefaultConfig_NoAPIKeys_Succeeds(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	setAppEnv(t, tempDir)

	a, err := New(ctx, Options{ProjectDir: tempDir})
	require.NoError(t, err)
	require.NotNil(t, a)
	t.Cleanup(func() {
		require.NoError(t, a.Close(context.Background()))
	})

	require.NotNil(t, a.Cfg)
	require.NotNil(t, a.DB)
	require.NotNil(t, a.Bus)
	require.NotNil(t, a.LLM)
	require.NotNil(t, a.Sessions)
	require.NotNil(t, a.Ledger)
	require.NotNil(t, a.Permission)
	require.NotNil(t, a.Hooks)
	require.NotNil(t, a.Shell)
	require.NotNil(t, a.LSP)
	require.NotNil(t, a.MCP)
	require.NotNil(t, a.FileTracker)
	require.NotNil(t, a.Tools)
	require.NotNil(t, a.Agent)
	require.NotNil(t, a.Logger)

	provider, err := a.LLM.Get("deepseek")
	require.NoError(t, err)
	_, err = provider.Stream(ctx, llm.Request{Model: "deepseek-chat"})
	require.ErrorIs(t, err, llm.ErrAuth)
}

func TestClose_FastPath_UnderDeadline(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	setAppEnv(t, tempDir)

	a, err := New(ctx, Options{ProjectDir: tempDir})
	require.NoError(t, err)

	start := time.Now()
	require.NoError(t, a.Close(ctx))
	require.Less(t, time.Since(start), 100*time.Millisecond)
}

func TestClose_DoubleCall_Errors(t *testing.T) {
	ctx := context.Background()
	tempDir := t.TempDir()
	setAppEnv(t, tempDir)

	a, err := New(ctx, Options{ProjectDir: tempDir})
	require.NoError(t, err)

	require.NoError(t, a.Close(ctx))
	require.ErrorIs(t, a.Close(ctx), ErrAlreadyClosed)
}

func TestNoTUIOrCmdImport(t *testing.T) {
	cmd := exec.Command("go", "list", "-deps", "./internal/app")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	require.NoError(t, err, string(out))

	for _, dep := range strings.Fields(string(out)) {
		require.NotContains(t, dep, "/internal/tui")
		require.NotContains(t, dep, "/internal/cmd")
	}
}

func TestCloseSteps_ReverseConstructionOrder(t *testing.T) {
	var got []string
	steps := []closeStep{
		{name: "util", close: recordClose(&got, "util")},
		{name: "db", close: recordClose(&got, "db")},
		{name: "pubsub", close: recordClose(&got, "pubsub")},
		{name: "config", close: recordClose(&got, "config")},
		{name: "ledger", close: recordClose(&got, "ledger")},
		{name: "session", close: recordClose(&got, "session")},
		{name: "filetracker", close: recordClose(&got, "filetracker")},
		{name: "llm", close: recordClose(&got, "llm")},
		{name: "permission", close: recordClose(&got, "permission")},
		{name: "hooks", close: recordClose(&got, "hooks")},
		{name: "shell", close: recordClose(&got, "shell")},
		{name: "lsp", close: recordClose(&got, "lsp")},
		{name: "mcp", close: recordClose(&got, "mcp")},
		{name: "tools", close: recordClose(&got, "tools")},
		{name: "agent", close: recordClose(&got, "agent")},
	}

	err := closeSteps(context.Background(), steps, nil)
	require.NoError(t, err)
	require.Equal(t, []string{
		"agent",
		"tools",
		"mcp",
		"lsp",
		"shell",
		"hooks",
		"permission",
		"llm",
		"filetracker",
		"session",
		"ledger",
		"config",
		"pubsub",
		"db",
		"util",
	}, got)
}

func TestCloseSteps_ReportsSubsystemDeadline(t *testing.T) {
	steps := []closeStep{
		{name: "db", close: func(context.Context) error { return nil }},
		{name: "lsp", close: func(ctx context.Context) error {
			<-ctx.Done()
			return ctx.Err()
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()

	err := closeSteps(ctx, steps, nil)
	require.Error(t, err)
	require.Contains(t, err.Error(), "closing lsp")
	require.True(t, errors.Is(err, context.DeadlineExceeded))
}

func setAppEnv(t *testing.T, tempDir string) {
	t.Helper()
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(tempDir, "config"))
	t.Setenv("XDG_DATA_HOME", filepath.Join(tempDir, "data"))
	for _, key := range []string{
		"ANTHROPIC_API_KEY",
		"OPENAI_API_KEY",
		"DEEPSEEK_API_KEY",
		"MOONSHOT_API_KEY",
		"GROQ_API_KEY",
		"TOGETHER_API_KEY",
		"FIREWORKS_API_KEY",
		"OPENROUTER_API_KEY",
	} {
		t.Setenv(key, "")
	}
}

func recordClose(got *[]string, name string) func(context.Context) error {
	return func(context.Context) error {
		*got = append(*got, name)
		return nil
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	require.True(t, ok)
	root := filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
	_, err := os.Stat(filepath.Join(root, "go.mod"))
	require.NoError(t, err)
	return root
}
