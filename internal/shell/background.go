// Package shell manages bash command execution and background tracking.
package shell

import (
	"fmt"
	"log/slog"
	"sort"
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

// List returns a status snapshot of every tracked job, newest-started first
// (ties broken by ID for determinism). The returned Jobs carry status metadata
// only — Stdout/Stderr are left empty so listing stays cheap regardless of how
// much output a job has captured; call Output for a job's accumulated text.
// This lets a caller (e.g. the model after losing a job ID across compaction)
// recover the set of running and recently-finished background jobs.
func (s *Shell) List() []Job {
	var jobs []Job
	s.jobs.Range(func(_, value any) bool {
		state := value.(*jobState)

		state.mu.RLock()
		jobs = append(jobs, Job{
			ID:        state.id,
			Command:   state.command,
			StartedAt: state.startedAt,
			Status:    state.status,
			ExitCode:  state.exitCode,
		})
		state.mu.RUnlock()
		return true
	})

	sort.Slice(jobs, func(i, j int) bool {
		if !jobs[i].StartedAt.Equal(jobs[j].StartedAt) {
			return jobs[i].StartedAt.After(jobs[j].StartedAt)
		}
		return jobs[i].ID < jobs[j].ID
	})
	return jobs
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
	// Stamp the finish time so a killed job whose wait goroutine has not yet
	// reaped it still carries a TTL baseline. The wait goroutine may overwrite
	// this with the (marginally later) reap time, which is equally valid.
	if state.finishedAt.IsZero() {
		state.finishedAt = s.now()
	}

	if state.process != nil {
		// Kill the whole process group (negative pid on Unix).
		killProcessGroup(state.process.Pid)
	}

	return nil
}

// jobTTL is the grace window a finished job remains retrievable before the TTL
// watcher evicts it. It is measured from when the job FINISHED, not when it
// started, so a long-running job still gets the full window after completing.
const jobTTL = 10 * time.Minute

// startTTLWatcher monitors tracked jobs and evicts finished ones whose finish
// time is older than jobTTL. Eviction is keyed off finishedAt (not startedAt) so
// a job that ran longer than jobTTL is not dropped the instant it completes; it
// keeps the full grace window from completion.
func (s *Shell) startTTLWatcher() {
	ticker := time.NewTicker(1 * time.Minute)
	defer ticker.Stop()

	for {
		select {
		case <-s.cleanup:
			return
		case <-ticker.C:
			s.evictExpired(s.now())
		}
	}
}

// evictExpired removes every finished job whose grace window has elapsed
// relative to now. It is separated from the ticker loop so the eviction policy
// can be unit-tested directly with a controlled clock. Running jobs are never
// evicted; finished jobs with a zero finishedAt are skipped (their baseline is
// not yet known) and revisited on the next tick.
func (s *Shell) evictExpired(now time.Time) {
	s.jobs.Range(func(key, value any) bool {
		state := value.(*jobState)

		state.mu.RLock()
		status := state.status
		finishedAt := state.finishedAt
		state.mu.RUnlock()

		if status == StatusRunning || finishedAt.IsZero() {
			return true
		}

		if now.Sub(finishedAt) > jobTTL {
			s.jobs.Delete(key)
			slog.Debug("Evicted stale shell job from memory", "jobID", key)
		}
		return true
	})
}
