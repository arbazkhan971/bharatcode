//go:build darwin || linux

package shell

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/require"
)

// withProbe installs a probe for the duration of a test and restores the
// previous probe and a fresh cache afterwards. Resetting the cache before the
// test means the injected probe is the one that runs; restoring it afterwards
// stops one test from poisoning the global cache for another (e.g. an
// injected-false probe would otherwise make later real-launcher tests silently
// degrade to bash and false-skip). Tests using it must not run in parallel.
func withProbe(t *testing.T, probe func() bool) {
	t.Helper()
	prev := sandboxProbe
	resetSandboxProbe()
	sandboxProbe = probe
	t.Cleanup(func() {
		sandboxProbe = prev
		resetSandboxProbe()
	})
}

// TestWrapCommand_BrokenProbeDegradesToBash asserts that when the functional
// probe reports the launcher is unusable (the present-but-broken case, e.g.
// bwrap with user namespaces disabled), wrapCommand degrades to the plain
// ["bash","-c",cmd] argv instead of emitting a launcher invocation that would
// fail every command.
func TestWrapCommand_BrokenProbeDegradesToBash(t *testing.T) {
	const cmd = "echo hi"
	withProbe(t, func() bool { return false })

	argv := wrapCommand(SandboxWorkspaceWrite, t.TempDir(), cmd)
	require.Equal(t, []string{"bash", "-c", cmd}, argv,
		"a broken launcher must degrade to plain bash")
}

// TestWrapCommand_WorkingProbeSandboxes asserts that when the probe reports the
// launcher is functional, wrapCommand returns the sandboxed argv: the launcher
// binary leads and the bash invocation is preserved verbatim at the tail. The
// assertion is platform-generic (not hardcoded to sandbox-exec/bwrap) because
// this test compiles on both darwin and linux. Crucially it injects a working
// probe so it passes even on a host with no real launcher installed.
func TestWrapCommand_WorkingProbeSandboxes(t *testing.T) {
	const cmd = "echo hi"
	withProbe(t, func() bool { return true })

	argv := wrapCommand(SandboxWorkspaceWrite, t.TempDir(), cmd)
	require.NotEqual(t, "bash", argv[0],
		"a working launcher must produce a sandboxed argv, not plain bash")
	n := len(argv)
	require.GreaterOrEqual(t, n, 3)
	require.Equal(t, []string{"bash", "-c", cmd}, argv[n-3:],
		"the bash invocation must be preserved verbatim at the tail")
}

// TestSandboxProbe_RunsAtMostOnce asserts the functional probe is cached: across
// many wrapCommand calls (here concurrent, to mirror real concurrent shell use)
// the probe runs exactly once. It uses a working probe so the sandboxed path is
// exercised on every call yet the underlying probe is consulted just once.
func TestSandboxProbe_RunsAtMostOnce(t *testing.T) {
	var calls atomic.Int64
	withProbe(t, func() bool {
		calls.Add(1)
		return true
	})

	const goroutines = 16
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			_ = wrapCommand(SandboxWorkspaceWrite, t.TempDir(), "echo hi")
		}()
	}
	wg.Wait()

	require.Equal(t, int64(1), calls.Load(),
		"the functional probe must run at most once per process")
}

// TestSandboxLauncherUsable_CachesFailure asserts a failing probe is cached too:
// it runs once and every subsequent call returns the cached false without
// re-probing, so the warning fires at most once.
func TestSandboxLauncherUsable_CachesFailure(t *testing.T) {
	var calls atomic.Int64
	withProbe(t, func() bool {
		calls.Add(1)
		return false
	})

	for i := 0; i < 5; i++ {
		require.False(t, sandboxLauncherUsable())
	}
	require.Equal(t, int64(1), calls.Load(),
		"a failing probe must be cached and run at most once")
}

// TestSandboxLauncherUsable_NilProbeIsUnusable asserts that a nil probe (the
// state on platforms without any launcher) reports unusable without panicking.
func TestSandboxLauncherUsable_NilProbeIsUnusable(t *testing.T) {
	withProbe(t, nil)
	require.False(t, sandboxLauncherUsable(),
		"a nil probe must report the launcher unusable")
}
