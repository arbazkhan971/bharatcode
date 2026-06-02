package tools

import (
	"context"
	"encoding/json"
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
	require.Equal(t, "hello", result.Content)
	require.NotEmpty(t, result.Metadata["job_id"])
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
		return err == nil && !result.IsError && result.Content == "ready"
	}, 2*time.Second, 20*time.Millisecond)

	killed, err := killTool.Run(context.Background(), mustJSON(t, map[string]string{"job_id": jobID}))
	require.NoError(t, err)
	require.False(t, killed.IsError)
	require.Contains(t, killed.Content, "job "+jobID)
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
