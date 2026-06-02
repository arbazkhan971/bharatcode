//go:build linux

package shell

import (
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
)

// bwrapBinary is the bubblewrap launcher. Unlike macOS's sandbox-exec it is
// not guaranteed to be installed, so we LookPath it and degrade to no sandbox
// (with a warning) when it is absent.
const bwrapBinary = "bwrap"

// init wires the default functional probe for Linux. On locked-down hosts bwrap
// is frequently present yet nonfunctional because user namespaces are disabled;
// the probe catches that case so we degrade to plain bash instead of failing
// every command.
func init() {
	sandboxProbe = linuxSandboxProbe
}

// linuxSandboxProbe reports whether bwrap is present and can actually construct
// a sandbox in this environment. It folds the PATH lookup and a trivial no-op
// run (exercising user-namespace, mount-namespace and net-unshare setup) into
// one check so a present-but-broken launcher degrades like a missing one. The
// probe argv mirrors the integration test's so production and test logic cannot
// drift.
func linuxSandboxProbe() bool {
	if _, err := exec.LookPath(bwrapBinary); err != nil {
		return false
	}
	return exec.Command(bwrapBinary, "--ro-bind", "/", "/", "--unshare-net", "true").Run() == nil
}

// wrapCommand builds the argv used to execute cmdStr under the requested
// sandbox mode on Linux, preferring bubblewrap (bwrap).
//
// bubblewrap constructs an unprivileged user namespace and a fresh mount
// namespace, then we populate it explicitly:
//
//   - --ro-bind / /            : the whole host filesystem is visible but
//     read-only by default, preserving "reads anywhere".
//   - --dev /dev, --proc /proc : minimal device and proc filesystems so bash
//     and tools function.
//   - --bind <ws> <ws>         : (workspace-write only) re-bind the workspace
//     read-write so writes there succeed.
//   - --bind <tmp> <tmp>       : (workspace-write only) likewise the temp dir.
//   - --unshare-net            : drop network access for every confining mode.
//   - --die-with-parent        : ensure the sandbox dies if the agent does.
//
// read-only mode omits the rw binds entirely, so the only writable surfaces
// are the tmpfs /dev and /proc bwrap provides.
//
// Paths are canonicalised with filepath.EvalSymlinks so the bind source and
// destination match the paths the command actually resolves. An empty
// workspace under workspace-write degrades to no sandbox with a warning,
// matching the macOS behaviour. If the cached functional probe reports bwrap is
// missing or unusable (e.g. user namespaces disabled on a locked-down host) we
// fall back to a plain bash invocation and log a warning rather than failing.
func wrapCommand(mode SandboxMode, workspace, cmdStr string) []string {
	if !mode.confines() {
		return plainBash(cmdStr)
	}

	if mode == SandboxWorkspaceWrite && workspace == "" {
		slog.Warn("sandbox: workspace-write requested with empty cwd; running without sandbox")
		return plainBash(cmdStr)
	}

	// Gate on a cached functional probe rather than a bare PATH lookup: on a
	// locked-down host bwrap may be present yet unable to create a user
	// namespace, which would otherwise make every command fail. The probe runs
	// at most once per process and degrades to plain bash when it fails.
	if !sandboxLauncherUsable() {
		return plainBash(cmdStr)
	}

	return bwrapArgv(mode, workspace, os.TempDir(), cmdStr)
}

// bwrapArgv assembles the bubblewrap argv around a bash invocation. It is pure
// apart from resolving symlinks on the bind paths, so it can be unit-tested
// directly.
func bwrapArgv(mode SandboxMode, workspace, tmpDir, cmdStr string) []string {
	args := []string{
		bwrapBinary,
		"--ro-bind", "/", "/",
		"--dev", "/dev",
		"--proc", "/proc",
		"--unshare-net",
		"--die-with-parent",
	}

	if mode == SandboxWorkspaceWrite {
		for _, p := range canonicalWritePaths(workspace, tmpDir) {
			args = append(args, "--bind", p, p)
		}
	}

	args = append(args, "bash", "-c", cmdStr)
	return args
}

// canonicalWritePaths returns the deduplicated, symlink-resolved paths that
// workspace-write mode re-binds read-write.
func canonicalWritePaths(workspace, tmpDir string) []string {
	var out []string
	seen := make(map[string]bool)
	for _, p := range []string{workspace, tmpDir} {
		if p == "" {
			continue
		}
		resolved := p
		if r, err := filepath.EvalSymlinks(p); err == nil {
			resolved = r
		}
		if seen[resolved] {
			continue
		}
		seen[resolved] = true
		out = append(out, resolved)
	}
	return out
}

// plainBash returns the unwrapped bash invocation.
func plainBash(cmdStr string) []string {
	return []string{"bash", "-c", cmdStr}
}
