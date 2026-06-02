package shell

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// sandboxBinaryAvailable reports the OS sandbox launcher for the current
// platform and whether it is both on PATH and actually functional. Returns
// ("", false) on unsupported platforms so the integration test skips cleanly.
//
// A bare exec.LookPath is insufficient on Linux CI: bwrap is frequently
// installed yet nonfunctional because the runner forbids user namespaces
// (e.g. unprivileged Docker/GitLab runners). In that case bwrap exits
// non-zero before running bash, which would turn the "inside write succeeds"
// assertion into a hard failure. To honour the "skip gracefully, never
// hard-fail in CI" bar we run a trivial probe through the launcher and only
// report availability when it actually executes. sandbox-exec on macOS passes
// this probe trivially.
func sandboxBinaryAvailable() (string, bool) {
	var bin string
	switch runtime.GOOS {
	case "darwin":
		bin = "sandbox-exec"
	case "linux":
		bin = "bwrap"
	default:
		return "", false
	}
	if _, err := exec.LookPath(bin); err != nil {
		return bin, false
	}
	if !sandboxLauncherWorks(bin) {
		return bin, false
	}
	return bin, true
}

// sandboxLauncherWorks runs a no-op command through the launcher to confirm it
// can actually construct a sandbox in this environment (not merely that the
// binary exists). The probe argv mirrors the real wrapper just enough to
// exercise namespace/profile setup.
func sandboxLauncherWorks(bin string) bool {
	switch bin {
	case "bwrap":
		// Exercises user-namespace + mount-namespace + net-unshare setup, which
		// is exactly what fails on locked-down CI runners.
		return exec.Command(bin, "--ro-bind", "/", "/", "--unshare-net", "true").Run() == nil
	case "sandbox-exec":
		return exec.Command(bin, "-p", "(version 1)(allow default)", "true").Run() == nil
	default:
		return false
	}
}

// TestSandbox_WorkspaceWriteEnforcement asserts REAL kernel enforcement: under
// SandboxWorkspaceWrite a write INSIDE the workspace succeeds while a write
// OUTSIDE it (and outside the temp dir) fails. It SKIPS gracefully when the OS
// sandbox binary is unavailable, so CI without sandbox-exec/bwrap never
// hard-fails.
func TestSandbox_WorkspaceWriteEnforcement(t *testing.T) {
	bin, ok := sandboxBinaryAvailable()
	if !ok {
		t.Skipf("OS sandbox binary %q unavailable; skipping enforcement test", bin)
	}

	sh := New(nil, WithSandboxMode(SandboxWorkspaceWrite))
	defer sh.Shutdown()

	workspace := t.TempDir()

	// The "outside" target must be outside BOTH the workspace and the temp
	// dir, because workspace-write also permits writes under os.TempDir().
	// t.TempDir() lives under the OS temp dir, so we instead pick a path under
	// the filesystem root that the sandbox can attempt but should be denied.
	// We resolve symlinks and assert the chosen path is genuinely outside the
	// permitted subpaths before relying on it as a negative case.
	outsideDir := outsideTempDir(t)
	outsideFile := filepath.Join(outsideDir, "should_not_write.txt")
	insideFile := filepath.Join(workspace, "ok.txt")

	requireOutside(t, outsideFile, workspace, os.TempDir())

	// Write INSIDE the workspace: must succeed.
	job, err := sh.Run(context.Background(), "echo inside > "+shellQuote(insideFile), RunOpts{Cwd: workspace})
	require.NoError(t, err)
	require.Equalf(t, StatusCompleted, job.Status,
		"in-workspace write should succeed under sandbox; stderr=%q", job.Stderr)
	_, statErr := os.Stat(insideFile)
	require.NoError(t, statErr, "in-workspace file should exist after write")

	// Write OUTSIDE the workspace and temp dir: must be blocked by the kernel
	// sandbox, surfacing as a non-zero exit (StatusFailed).
	job, err = sh.Run(context.Background(), "echo outside > "+shellQuote(outsideFile), RunOpts{Cwd: workspace})
	require.NoError(t, err)
	require.Equalf(t, StatusFailed, job.Status,
		"out-of-workspace write should be denied by the sandbox; stderr=%q", job.Stderr)
	_, statErr = os.Stat(outsideFile)
	require.Error(t, statErr, "out-of-workspace file must NOT exist; sandbox failed to block the write")
}

// TestSandbox_NetworkBlocked asserts that a confining mode blocks outbound
// network. It skips if the launcher is unavailable. The probe uses bash's
// /dev/tcp pseudo-device so no external binary (curl/nc) is required, and a
// short timeout bounds the case where the network is simply unreachable.
func TestSandbox_NetworkBlocked(t *testing.T) {
	bin, ok := sandboxBinaryAvailable()
	if !ok {
		t.Skipf("OS sandbox binary %q unavailable; skipping network test", bin)
	}

	sh := New(nil, WithSandboxMode(SandboxWorkspaceWrite))
	defer sh.Shutdown()

	workspace := t.TempDir()

	// Connecting to a routable address must fail fast under the sandbox. We do
	// not assert a specific status because a blocked socket can surface as a
	// connect error (non-zero exit) on both platforms; we assert it did NOT
	// complete successfully.
	job, err := sh.Run(context.Background(),
		"exec 3<>/dev/tcp/1.1.1.1/80",
		RunOpts{Cwd: workspace, Timeout: 5 * time.Second})
	require.NoError(t, err)
	require.NotEqualf(t, StatusCompleted, job.Status,
		"network connect should be blocked by the sandbox; stderr=%q", job.Stderr)
}

// outsideTempDir creates a directory that is guaranteed to be outside the OS
// temp dir (so workspace-write's TMPDIR allowance does not cover it). On macOS
// /tmp resolves to /private/tmp which is NOT under $TMPDIR (/var/folders/...),
// making it a valid negative target. On Linux it uses /var/tmp similarly. The
// dir is registered for cleanup.
func outsideTempDir(t *testing.T) string {
	t.Helper()
	roots := []string{"/tmp", "/var/tmp"}
	tmp := resolve(os.TempDir())
	for _, root := range roots {
		rroot := resolve(root)
		// Skip a root that is itself inside the OS temp dir.
		if isUnder(rroot, tmp) {
			continue
		}
		dir, err := os.MkdirTemp(root, "bharatcode-sbout-")
		if err != nil {
			continue
		}
		t.Cleanup(func() { _ = os.RemoveAll(dir) })
		return dir
	}
	t.Skip("no writable directory outside the OS temp dir available for the negative case")
	return ""
}

// requireOutside fails the test if target is under either permitted root,
// guarding the negative case from silently testing nothing.
func requireOutside(t *testing.T, target, workspace, tmpDir string) {
	t.Helper()
	rt := resolve(target)
	require.Falsef(t, isUnder(rt, resolve(workspace)),
		"negative target %s must be outside workspace %s", rt, workspace)
	require.Falsef(t, isUnder(rt, resolve(tmpDir)),
		"negative target %s must be outside temp dir %s", rt, tmpDir)
}

func resolve(p string) string {
	if r, err := filepath.EvalSymlinks(p); err == nil {
		return r
	}
	return p
}

// isUnder reports whether path is equal to or nested within base.
func isUnder(path, base string) bool {
	rel, err := filepath.Rel(base, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !filepathHasDotDotPrefix(rel))
}

func filepathHasDotDotPrefix(rel string) bool {
	return len(rel) >= 2 && rel[0] == '.' && rel[1] == '.'
}

// shellQuote single-quotes a path for safe interpolation into a bash command.
func shellQuote(s string) string {
	return "'" + s + "'"
}
