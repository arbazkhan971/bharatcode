//go:build windows

package shell

import (
	"os"
	"syscall"
)

// sysProcAttr returns an empty SysProcAttr on Windows. There is no Setpgid
// equivalent; process-group setup is a no-op here. This stub exists so the
// package compiles on Windows (GOOS=windows go build ./internal/shell/); the
// shell's runtime target remains Unix.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{}
}

// killProcessGroup best-effort kills the process by pid on Windows, which has
// no process-group signalling. Compile-only stub; not exercised on the
// supported Unix runtime.
func killProcessGroup(pid int) {
	if p, err := os.FindProcess(pid); err == nil {
		_ = p.Kill()
	}
}
