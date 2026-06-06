//go:build !windows

// Package shell implements execution of bash commands and background tracking.
package shell_test

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/shell"
	"github.com/stretchr/testify/require"
)

func TestShell_RunBasic(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_run", 64)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	tests := []struct {
		name       string
		cmd        string
		opts       shell.RunOpts
		wantStatus shell.JobStatus
		wantCode   int
		wantStdout string
	}{
		{
			name:       "simple echo",
			cmd:        "echo -n 'hello world'",
			wantStatus: shell.StatusCompleted,
			wantCode:   0,
			wantStdout: "hello world",
		},
		{
			name:       "failed command",
			cmd:        "exit 42",
			wantStatus: shell.StatusFailed,
			wantCode:   42,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			job, err := sh.Run(context.Background(), tc.cmd, tc.opts)
			require.NoError(t, err)
			require.Equal(t, tc.wantStatus, job.Status)
			require.Equal(t, tc.wantCode, job.ExitCode)
			if tc.wantStdout != "" {
				require.Equal(t, tc.wantStdout, job.Stdout)
			}
		})
	}
}

func TestShell_Stdin(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_stdin", 64)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	t.Run("stdin is piped to the command", func(t *testing.T) {
		job, err := sh.Run(context.Background(), "cat", shell.RunOpts{Stdin: "line one\nline two\n"})
		require.NoError(t, err)
		require.Equal(t, shell.StatusCompleted, job.Status)
		require.Equal(t, "line one\nline two\n", job.Stdout)
	})

	t.Run("multiline content survives without shell quoting", func(t *testing.T) {
		// Content full of shell metacharacters that would be a nightmare to
		// embed in the command string is delivered verbatim via stdin.
		content := "$(rm -rf /); `whoami`; 'quotes' \"and\" \\backslashes\\\n"
		job, err := sh.Run(context.Background(), "cat", shell.RunOpts{Stdin: content})
		require.NoError(t, err)
		require.Equal(t, content, job.Stdout)
	})

	t.Run("empty stdin yields immediate EOF", func(t *testing.T) {
		job, err := sh.Run(context.Background(), "wc -c", shell.RunOpts{})
		require.NoError(t, err)
		require.Equal(t, shell.StatusCompleted, job.Status)
		require.Equal(t, "0", strings.TrimSpace(job.Stdout))
	})
}

func TestShell_Timeout(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_timeout", 64)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	// Run a command that takes 5 seconds, but set a timeout of 100ms.
	opts := shell.RunOpts{
		Timeout: 100 * time.Millisecond,
	}

	job, err := sh.Run(context.Background(), "sleep 5", opts)
	require.NoError(t, err)
	require.Equal(t, shell.StatusTimeout, job.Status)
	require.Equal(t, -1, job.ExitCode)
}

func TestShell_DescendantKill(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_descendants", 64)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	// Spawn a shell script that starts a long sleep as a descendant in the background.
	// We want to make sure killing the parent job also kills the descendant sleep process.
	// We'll write sleep's PID to a temp file so we can check if it gets cleaned up.
	tmpFile := fmt.Sprintf("%s/sleep_pid", t.TempDir())

	cmd := fmt.Sprintf("sleep 100 & echo $! > %s; wait", tmpFile)

	id, err := sh.Start(context.Background(), cmd, shell.RunOpts{})
	require.NoError(t, err)

	// Wait a moment for bash to run and write the PID.
	time.Sleep(200 * time.Millisecond)

	// Read the descendant PID.
	data, err := os.ReadFile(tmpFile)
	require.NoError(t, err)

	var descendantPID int
	_, err = fmt.Sscanf(strings.TrimSpace(string(data)), "%d", &descendantPID)
	require.NoError(t, err)

	// Verify that the descendant sleep process is running.
	err = syscall.Kill(descendantPID, 0)
	require.NoError(t, err, "Descendant process should be running")

	// Kill the parent job.
	err = sh.Kill(id)
	require.NoError(t, err)

	// Wait for cleanup.
	time.Sleep(200 * time.Millisecond)

	// Verify that the descendant sleep process was terminated.
	err = syscall.Kill(descendantPID, 0)
	require.Error(t, err, "Descendant process should have been terminated")
}

func TestShell_Truncation(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_truncation", 64)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	// Temporarily override MaxCaptureSize to 10 bytes.
	origSize := shell.MaxCaptureSize
	shell.MaxCaptureSize = 10
	defer func() {
		shell.MaxCaptureSize = origSize
	}()

	job, err := sh.Run(context.Background(), "echo -n 'abcdefghijklmnop'", shell.RunOpts{})
	require.NoError(t, err)
	require.Contains(t, job.Stdout, "... [truncated, 16 bytes]")
	require.True(t, strings.HasPrefix(job.Stdout, "abcdefghij"))
}

func TestShell_ConcurrentJobsIsolation(t *testing.T) {
	bus := pubsub.NewTopic[pubsub.ShellJobPayload]("test_shell_concurrent", 256)
	defer bus.Close()

	sh := shell.New(bus)
	defer sh.Shutdown()

	var wg sync.WaitGroup
	numJobs := 10

	for i := 0; i < numJobs; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			job, err := sh.Run(context.Background(), fmt.Sprintf("echo -n 'job_%d'", idx), shell.RunOpts{})
			require.NoError(t, err)
			require.Equal(t, shell.StatusCompleted, job.Status)
			require.Equal(t, fmt.Sprintf("job_%d", idx), job.Stdout)
		}(i)
	}

	wg.Wait()
}
