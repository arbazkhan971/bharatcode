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
// matching the macOS behaviour. If bwrap is not on PATH we fall back to a
// plain bash invocation and log a warning rather than failing.
func wrapCommand(mode SandboxMode, workspace, cmdStr string) []string {
	if !mode.confines() {
		return plainBash(cmdStr)
	}

	if mode == SandboxWorkspaceWrite && workspace == "" {
		slog.Warn("sandbox: workspace-write requested with empty cwd; running without sandbox")
		return plainBash(cmdStr)
	}

	if _, err := exec.LookPath(bwrapBinary); err != nil {
		slog.Warn("sandbox: bwrap not found on PATH; running without sandbox", "error", err)
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
