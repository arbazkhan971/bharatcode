// Package shell manages bash command execution and background tracking.
package shell

import (
	"fmt"
	"log/slog"
	"time"
)

// Output retrieves the current state and accumulated stdout/stderr for a job.
func (s *Shell) Output(jobID string) (Job, error) {
	stateRaw, ok := s.jobs.Load(jobID)
	if !ok {
		return Job{}, fmt.Errorf("job not found: %s", jobID)
	}
	state := stateRaw.(*jobState)

	state.mu.RLock()
	defer state.mu.RUnlock()

	stdoutStr := state.stdout.String()
	if state.truncatedStdout {
		stdoutStr += fmt.Sprintf("\n... [truncated, %d bytes]", state.rawStdoutBytes)
	}

	stderrStr := state.stderr.String()
	if state.truncatedStderr {
		stderrStr += fmt.Sprintf("\n... [truncated, %d bytes]", state.rawStderrBytes)
	}

	return Job{
		ID:        state.id,
		Command:   state.command,
		StartedAt: state.startedAt,
		Status:    state.status,
		ExitCode:  state.exitCode,
		Stdout:    stdoutStr,
		Stderr:    stderrStr,
		// pgid is internal
	}, nil
}

// Kill halts a running background job by sending SIGKILL to its process group.
// It is idempotent; killing an already finished or non-existent job returns nil.
func (s *Shell) Kill(jobID string) error {
	stateRaw, ok := s.jobs.Load(jobID)
	if !ok {
		return nil
	}
	state := stateRaw.(*jobState)

	state.mu.Lock()
	defer state.mu.Unlock()

	if state.status != StatusRunning {
		return nil
	}

	state.status = StatusKilled
	state.exitCode = -1

	if state.process != nil {
		// Kill the whole process group (negative pid on Unix).
		killProcessGroup(state.process.Pid)
	}

	return nil
}

// startTTLWatcher monitors tracked jobs and evicts finished ones older than 10 minutes.
func (s *Shell) startTTLWatcher() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.cleanup:
			return
		case <-ticker.C:
			now := time.Now()
			s.jobs.Range(func(key, value any) bool {
				state := value.(*jobState)

				state.mu.RLock()
				status := state.status
				startedAt := state.startedAt
				state.mu.RUnlock()

				if status != StatusRunning {
					// Check if finished job is older than 10 minutes.
					// Note: Since we don't store finished time, startedAt + 10 mins is a safe proxy,
					// or we can use startedAt directly as a conservative bounds.
					if now.Sub(startedAt) > 10*time.Minute {
						s.jobs.Delete(key)
						slog.Debug("Evicted stale shell job from memory", "jobID", key)
					}
				}
				return true
			})
		}
	}
}
