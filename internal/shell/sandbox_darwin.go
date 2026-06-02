//go:build darwin

package shell

import (
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// sandboxExecBinary is the macOS sandbox launcher. It ships with the OS at a
// fixed path, so it is effectively always present; we still LookPath it and
// degrade gracefully if a stripped environment lacks it.
const sandboxExecBinary = "sandbox-exec"

// init wires the default functional probe for macOS. The probe is harmless
// here: sandbox-exec is reliable, so the no-op run succeeds and confinement
// stays enabled. It still guards against a stripped environment where
// sandbox-exec is missing from PATH.
func init() {
	sandboxProbe = darwinSandboxProbe
}

// darwinSandboxProbe reports whether sandbox-exec is present and can construct a
// minimal sandbox in this environment. It folds the PATH lookup and a trivial
// no-op run into one check so a present-but-unusable launcher degrades like a
// missing one. The probe argv mirrors the integration test's so production and
// test logic cannot drift.
func darwinSandboxProbe() bool {
	if _, err := exec.LookPath(sandboxExecBinary); err != nil {
		return false
	}
	return exec.Command(sandboxExecBinary, "-p", "(version 1)(allow default)", "true").Run() == nil
}

// wrapCommand builds the argv used to execute cmdStr under the requested
// sandbox mode on macOS.
//
// For confining modes it generates a seatbelt (.sb) profile and runs
// `sandbox-exec -p <profile> bash -c <cmd>`. seatbelt is the same mechanism
// the OS uses to confine App Store apps; the profile here is intentionally
// conservative:
//
//   - (allow default)        start permissive, then subtract.
//   - (deny network*)        block all outbound/inbound sockets.
//   - (deny file-write*)     block every filesystem write...
//   - (allow file-write* ...) ...except writes whose path is a subpath of the
//     workspace or the temp dir (workspace-write only), plus a tiny device
//     carve-out (/dev/null, /dev/tty, /dev/dtracehelper, /dev/random) that
//     bash and most tools need just to run.
//
// Paths are canonicalised with filepath.EvalSymlinks because the kernel
// evaluates seatbelt subpath rules against the real path: on macOS
// $TMPDIR and t.TempDir() live under /var/folders/... which is a symlink to
// /private/var/folders/..., so an unresolved subpath would deny writes that
// should succeed.
//
// If the workspace is empty (e.g. the hooks path that runs with no cwd) we
// cannot scope writes safely, so we degrade to no sandbox with a warning
// rather than emit a profile with an empty subpath. Likewise if the cached
// functional probe reports sandbox-exec is missing or unusable.
func wrapCommand(mode SandboxMode, workspace, cmdStr string) []string {
	if !mode.confines() {
		return plainBash(cmdStr)
	}

	if mode == SandboxWorkspaceWrite && workspace == "" {
		slog.Warn("sandbox: workspace-write requested with empty cwd; running without sandbox")
		return plainBash(cmdStr)
	}

	// Gate on a cached functional probe rather than a bare PATH lookup: a
	// present-but-broken launcher must degrade to plain bash, not fail every
	// command. The probe runs at most once per process.
	if !sandboxLauncherUsable() {
		return plainBash(cmdStr)
	}

	profile := buildSeatbeltProfile(mode, workspace, os.TempDir())
	return darwinArgv(profile, cmdStr)
}

// darwinArgv assembles the sandbox-exec argv around a bash invocation. It is
// pure (no PATH or filesystem access) so it can be unit-tested directly.
func darwinArgv(profile, cmdStr string) []string {
	return []string{sandboxExecBinary, "-p", profile, "bash", "-c", cmdStr}
}

// buildSeatbeltProfile renders the seatbelt profile string for the mode. It is
// pure apart from resolving symlinks on the supplied paths, and returns a
// profile that always denies network and, for workspace-write, permits writes
// only under the (canonicalised) workspace and temp dir.
func buildSeatbeltProfile(mode SandboxMode, workspace, tmpDir string) string {
	var b strings.Builder
	b.WriteString("(version 1)\n")
	b.WriteString("(allow default)\n")

	// Block all network access for every confining mode.
	b.WriteString("(deny network*)\n")

	// Block all writes, then add back the permitted subpaths below.
	b.WriteString("(deny file-write*)\n")

	// Minimal device carve-out so bash and tools can run even in read-only
	// mode (writing to /dev/null, the controlling tty, etc.).
	b.WriteString("(allow file-write*\n")
	b.WriteString("  (literal \"/dev/null\")\n")
	b.WriteString("  (literal \"/dev/tty\")\n")
	b.WriteString("  (literal \"/dev/dtracehelper\")\n")
	b.WriteString("  (literal \"/dev/random\")\n")
	b.WriteString("  (literal \"/dev/urandom\")\n")
	b.WriteString("  (regex #\"^/dev/fd/\")\n")
	b.WriteString(")\n")

	if mode == SandboxWorkspaceWrite {
		for _, p := range canonicalWritePaths(workspace, tmpDir) {
			fmt.Fprintf(&b, "(allow file-write* (subpath %s))\n", seatbeltQuote(p))
		}
	}

	return b.String()
}

// canonicalWritePaths returns the deduplicated, symlink-resolved subpaths that
// workspace-write mode permits writes under.
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

// seatbeltQuote renders a path as a double-quoted seatbelt string literal,
// escaping backslashes and embedded quotes so paths with unusual characters
// cannot break out of the profile expression.
func seatbeltQuote(p string) string {
	escaped := strings.ReplaceAll(p, `\`, `\\`)
	escaped = strings.ReplaceAll(escaped, `"`, `\"`)
	return `"` + escaped + `"`
}

// plainBash returns the unwrapped bash invocation.
func plainBash(cmdStr string) []string {
	return []string{"bash", "-c", cmdStr}
}
