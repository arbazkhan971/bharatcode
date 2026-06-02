package hooks_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/shell"
	"github.com/stretchr/testify/require"
)

func TestFire_BlockWinsAndUsesFirstConfiguredReason(t *testing.T) {
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Command: `sleep 0.1; echo '{"decision":{"block":true,"reason":"first block"}}'`,
		},
		{
			Event:   config.HookPreToolUse,
			Command: `echo '{"decision":{"approve":true}}'`,
		},
		{
			Event:   config.HookPreToolUse,
			Command: `echo '{"decision":{"block":true,"reason":"second block"}}'`,
		},
	})

	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool:      "bash",
		Args:      map[string]string{"command": "rm -rf /tmp/example"},
		SessionID: "session-1",
	})
	require.NoError(t, err)
	require.True(t, decision.Block)
	require.False(t, decision.Approve)
	require.Equal(t, "first block", decision.Reason)
}

func TestFire_ApproveWhenNoBlock(t *testing.T) {
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Command: `echo '{"decision":{"approve":true}}'`,
		},
	})

	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool: "bash",
	})
	require.NoError(t, err)
	require.True(t, decision.Approve)
	require.False(t, decision.Block)
}

func TestFire_RunsMatchingHooksInParallel(t *testing.T) {
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Command: `sleep 1; echo '{"decision":{"continue":true}}'`,
		},
		{
			Event:   config.HookPreToolUse,
			Command: `sleep 1; echo '{"decision":{"continue":true}}'`,
		},
	})

	start := time.Now()
	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool: "bash",
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.True(t, decision.Continue)
	require.Less(t, elapsed, 1700*time.Millisecond)
}

func TestFire_TimeoutIsPassThrough(t *testing.T) {
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Command: `sleep 2; echo '{"decision":{"block":true,"reason":"too late"}}'`,
			Timeout: 1,
		},
	})

	start := time.Now()
	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool: "bash",
	})
	elapsed := time.Since(start)

	require.NoError(t, err)
	require.True(t, decision.Continue)
	require.False(t, decision.Block)
	require.Less(t, elapsed, 1700*time.Millisecond)
}

func TestFire_PayloadShapeAndEnvironment(t *testing.T) {
	dir := t.TempDir()
	projectPath := filepath.Join(dir, ".bharatcode.json")
	require.NoError(t, os.WriteFile(projectPath, []byte("{}"), 0o644))
	oldWD, err := os.Getwd()
	require.NoError(t, err)
	require.NoError(t, os.Chdir(dir))
	t.Cleanup(func() {
		require.NoError(t, os.Chdir(oldWD))
	})

	payloadPath := filepath.Join(dir, "payload.json")
	envPath := filepath.Join(dir, "env.txt")
	pwdPath := filepath.Join(dir, "pwd.txt")
	engine := newEngine(t, []config.Hook{
		{
			Event: config.HookPreToolUse,
			Command: "cat > " + shellArg(payloadPath) +
				"; printf '%s' \"$BHARATCODE_EVENT:$BHARATCODE_SESSION_ID\" > " + shellArg(envPath) +
				"; pwd > " + shellArg(pwdPath),
		},
	})

	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool:      "bash",
		Args:      map[string]string{"command": "echo hi"},
		SessionID: "session-123",
	})
	require.NoError(t, err)
	require.True(t, decision.Continue)

	require.JSONEq(
		t,
		`{"event":"PreToolUse","tool":"bash","args":{"command":"echo hi"},"session_id":"session-123"}`,
		readFile(t, payloadPath),
	)
	require.Equal(t, "PreToolUse:session-123", readFile(t, envPath))
	// The hook's `pwd` reports the symlink-resolved working directory, which on
	// macOS differs from t.TempDir() (e.g. /tmp vs /private/tmp). Compare against
	// the resolved path so the assertion holds on every platform.
	resolvedDir, err := filepath.EvalSymlinks(dir)
	require.NoError(t, err)
	require.Equal(t, resolvedDir+"\n", readFile(t, pwdPath))
}

func TestFire_MatchesByGlobAndRegex(t *testing.T) {
	dir := t.TempDir()
	globPath := filepath.Join(dir, "glob")
	regexPath := filepath.Join(dir, "regex")
	missPath := filepath.Join(dir, "miss")
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Match:   "ba*",
			Command: "touch " + shellArg(globPath),
		},
		{
			Event:   config.HookPreToolUse,
			Match:   "/^ba(sh)?$/",
			Command: "touch " + shellArg(regexPath),
		},
		{
			Event:   config.HookPreToolUse,
			Match:   "edit",
			Command: "touch " + shellArg(missPath),
		},
	})

	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool: "bash",
	})
	require.NoError(t, err)
	require.True(t, decision.Continue)
	require.FileExists(t, globPath)
	require.FileExists(t, regexPath)
	require.NoFileExists(t, missPath)
}

func TestFire_MalformedDecisionIsPassThrough(t *testing.T) {
	engine := newEngine(t, []config.Hook{
		{
			Event:   config.HookPreToolUse,
			Command: `echo 'not json'`,
		},
	})

	decision, err := engine.Fire(context.Background(), hooks.PreToolUse, hooks.ToolPayload{
		Tool: "bash",
	})
	require.NoError(t, err)
	require.True(t, decision.Continue)
}

func newEngine(t *testing.T, hookDefs []config.Hook) *hooks.Engine {
	t.Helper()

	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("hooks_test", 128)
	t.Cleanup(bus.Close)

	sh := shell.New(bus)
	t.Cleanup(sh.Shutdown)

	return hooks.New(&config.Config{Hooks: hookDefs}, sh)
}

func readFile(t *testing.T, path string) string {
	t.Helper()

	data, err := os.ReadFile(path)
	require.NoError(t, err)
	return string(data)
}

func shellArg(value string) string {
	return "'" + value + "'"
}
