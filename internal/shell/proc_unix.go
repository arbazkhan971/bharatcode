//go:build !windows

package shell

import "syscall"

// sysProcAttr returns the platform SysProcAttr used for every spawned command.
// On Unix we start each command in its own process group (Setpgid) so the whole
// tree can be signalled with a single negative-pid kill.
func sysProcAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{
		Setpgid: true, // Start in a new process group for clean signaling.
	}
}

// killProcessGroup sends SIGKILL to the entire process group led by pid. The
// negative pid targets the group, so children spawned by the command die too.
// This mirrors the long-standing Unix behaviour the shell relies on for timeout
// and explicit-kill paths.
func killProcessGroup(pid int) {
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}
