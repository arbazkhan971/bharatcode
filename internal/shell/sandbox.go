package shell

import (
	"log/slog"
	"sync"
	"sync/atomic"
)

// sandboxProbe reports whether the OS sandbox launcher for this platform is
// both present and actually functional in the current environment. It is a
// package var (not a const func) so tests can inject a broken or working probe
// without needing a real misconfigured launcher on the host. The default is set
// per platform: darwin and linux supply a real probe that runs a trivial no-op
// through the launcher; platforms without a launcher leave it nil.
//
// Folding the presence check (exec.LookPath) and the functional check (running
// a no-op) into a single probe is deliberate: a present-but-broken launcher
// (e.g. bwrap on a host with user namespaces disabled) must be treated as
// unavailable exactly like a missing one, and tests that inject a "working"
// probe must reach the sandboxed path even on a host that lacks the binary.
var sandboxProbe func() bool

// probe caching state. sandboxLauncherUsable runs sandboxProbe at most once per
// process and caches the boolean result. The pair is reassignable (rather than
// a bare sync.Once) so tests can reset the cache between cases via
// resetSandboxProbe; sync.Once cannot be reset.
var (
	probeOnce    sync.Once
	probeResult  atomic.Bool
	probeWarnMsg = "sandbox: launcher unavailable or non-functional in this environment; running without sandbox"
)

// sandboxLauncherUsable returns the cached functional-probe result, running the
// probe exactly once. If the probe is nil (no launcher on this platform) or
// reports failure, it returns false and logs a single warning the first time so
// the operator learns the requested boundary is not being enforced. Subsequent
// calls return the cached value without re-probing or re-warning.
func sandboxLauncherUsable() bool {
	probeOnce.Do(func() {
		ok := sandboxProbe != nil && sandboxProbe()
		probeResult.Store(ok)
		if !ok {
			slog.Warn(probeWarnMsg)
		}
	})
	return probeResult.Load()
}

// resetSandboxProbe clears the cached probe result so the next
// sandboxLauncherUsable call re-runs the probe. It exists for tests, which need
// a fresh cache between the broken-launcher and working-launcher cases and to
// avoid one test poisoning the global cache for another.
func resetSandboxProbe() {
	probeOnce = sync.Once{}
	probeResult.Store(false)
}

// SandboxMode selects the OS-level confinement applied around every bash
// command this Shell executes. Unlike the permission prompt (which is an
// in-process policy gate the model can be coaxed past), a SandboxMode maps
// to a real kernel-enforced boundary on filesystem writes and network
// access supplied by the host OS sandbox launcher (sandbox-exec on macOS,
// bubblewrap on Linux). The boundary holds even if the command spawns
// child processes or the model is compromised.
type SandboxMode int

const (
	// SandboxOff applies no confinement: the command runs exactly as a
	// plain `bash -c` would. This is the zero value so that callers which
	// do not opt in (and existing tests) are unaffected. The user-facing
	// default is SandboxWorkspaceWrite, applied at the config layer.
	SandboxOff SandboxMode = iota

	// SandboxWorkspaceWrite allows reads anywhere but restricts writes to
	// the workspace (the command's working directory) plus the system temp
	// directory, and blocks outbound network access. This is the
	// recommended default: it lets ordinary build/edit workflows proceed
	// while preventing a command from tampering with the wider filesystem
	// or exfiltrating data over the network.
	SandboxWorkspaceWrite

	// SandboxReadOnly forbids all filesystem writes (beyond a minimal
	// device carve-out required for bash itself, e.g. /dev/null) and blocks
	// network access. Use it for commands that should only inspect, never
	// mutate.
	SandboxReadOnly

	// SandboxFull is an alias for "no sandbox" expressed as an explicit,
	// user-selectable mode (the "full" trust level). It behaves like
	// SandboxOff but is distinct so config can round-trip the string.
	SandboxFull
)

// String returns the canonical config string for the mode.
func (m SandboxMode) String() string {
	switch m {
	case SandboxWorkspaceWrite:
		return "workspace-write"
	case SandboxReadOnly:
		return "read-only"
	case SandboxFull:
		return "full"
	case SandboxOff:
		return "off"
	default:
		return "off"
	}
}

// confines reports whether the mode imposes any real boundary. Off and Full
// are pass-through; the others wrap the command with the OS launcher.
func (m SandboxMode) confines() bool {
	return m == SandboxWorkspaceWrite || m == SandboxReadOnly
}

// ParseSandboxMode maps a config string to a SandboxMode. Unknown or empty
// values fall back to SandboxWorkspaceWrite, the safe default, rather than
// silently disabling confinement. Recognized values: "off", "workspace-write",
// "read-only", "full". Hyphen and underscore separators are both accepted.
func ParseSandboxMode(s string) SandboxMode {
	switch s {
	case "off", "none":
		return SandboxOff
	case "workspace-write", "workspace_write", "workspace":
		return SandboxWorkspaceWrite
	case "read-only", "read_only", "readonly":
		return SandboxReadOnly
	case "full", "danger-full-access":
		return SandboxFull
	default:
		// Empty or unrecognized: prefer the safe default.
		return SandboxWorkspaceWrite
	}
}

// Option configures a Shell at construction time.
type Option func(*Shell)

// WithSandboxMode sets the OS-level sandbox mode applied to every command
// the Shell runs. The default (no option) is SandboxOff.
func WithSandboxMode(mode SandboxMode) Option {
	return func(s *Shell) {
		s.sandboxMode = mode
	}
}
