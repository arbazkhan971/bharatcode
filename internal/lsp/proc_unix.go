//go:build !windows

package lsp

import "syscall"

// sysProcAttr returns the platform SysProcAttr used for every spawned language
// server. On Unix we start the server in its own process group (Setpgid) so the
// whole tree can be signalled with a single negative-pid kill.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true, // Start in a new process group for clean signaling.
	}
}

// killProcessGroup sends SIGKILL to the entire process group led by pid. The
// negative pid targets the group, so children spawned by the language server
// die too. The raw error is returned so the caller can filter an
// already-exited process from a genuine failure.
func killProcessGroup(pid int) error {
	return syscall.Kill(-pid, syscall.SIGKILL)
}
