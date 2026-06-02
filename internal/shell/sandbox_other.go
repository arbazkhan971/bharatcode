//go:build !darwin && !linux

package shell

import "log/slog"

// wrapCommand is a no-op passthrough on platforms without a supported OS
// sandbox launcher (e.g. Windows, *BSD). It always returns a plain bash
// invocation and, for confining modes, logs a warning so the operator knows
// the requested boundary is not being enforced here.
func wrapCommand(mode SandboxMode, workspace, cmdStr string) []string {
	if mode.confines() {
		slog.Warn("sandbox: no OS sandbox available on this platform; running without sandbox", "mode", mode.String())
	}
	return []string{"bash", "-c", cmdStr}
}
