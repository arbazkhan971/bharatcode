//go:build !windows

package shell

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// newTestShell builds a Shell with a fixed injectable clock and no event bus.
// The clock lets the TTL eviction policy be exercised at precise offsets from a
// job's finish time without sleeping for the real ten-minute window.
func newTestShell(t *testing.T, clock func() time.Time) *Shell {
	t.Helper()
	s := New(nil)
	t.Cleanup(s.Shutdown)
	s.now = clock
	return s
}

// TestEviction_FinishedJobBaseline asserts the core fix: eviction is keyed off
// when a job FINISHED, not when it started. A real command is run to completion;
// its finish time is then used as the baseline. The job must remain retrievable
// just after finishing and through the grace window, and be evicted only once
// the clock advances past finishedAt + jobTTL.
func TestEviction_FinishedJobBaseline(t *testing.T) {
	// The clock is pinned so the wait goroutine stamps finishedAt = base and the
	// eviction sweeps below run at deterministic offsets from it.
	base := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newTestShell(t, func() time.Time { return base })

	job, err := s.Run(context.Background(), "echo -n done", RunOpts{})
	require.NoError(t, err)
	require.Equal(t, StatusCompleted, job.Status)

	// Confirm the finish time was stamped to the injected clock, not left zero
	// and not derived from start.
	stateRaw, ok := s.jobs.Load(job.ID)
	require.True(t, ok, "job must be tracked immediately after finishing")
	st := stateRaw.(*jobState)
	st.mu.RLock()
	finishedAt := st.finishedAt
	st.mu.RUnlock()
	require.Equal(t, base, finishedAt, "finishedAt must be stamped from the clock at completion")

	// Just after finishing: a sweep at the finish instant must NOT evict.
	s.evictExpired(base)
	_, err = s.Output(job.ID)
	require.NoError(t, err, "a just-finished job must still be retrievable")

	// Within the grace window (one second before the TTL elapses): still kept.
	s.evictExpired(base.Add(jobTTL - time.Second))
	_, err = s.Output(job.ID)
	require.NoError(t, err, "a finished job inside its grace window must be retained")

	// Past the grace window: now evicted.
	s.evictExpired(base.Add(jobTTL + time.Second))
	_, err = s.Output(job.ID)
	require.Error(t, err, "a finished job must be evicted only after finishedAt + jobTTL")
}

// TestEviction_LongRunningJobKeepsGraceWindow is the regression test for the
// audited bug: a job that RAN for longer than jobTTL must still get the full
// grace window after it finishes, rather than being evicted the instant it
// completes. With the old start-time baseline this job would be dropped on the
// very first sweep after completion; with the finish-time baseline it survives
// until finishedAt + jobTTL.
func TestEviction_LongRunningJobKeepsGraceWindow(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	// The job ran for 30 minutes (3x the TTL) before finishing.
	finish := start.Add(30 * time.Minute)

	s := newTestShell(t, func() time.Time { return start })

	// Construct a finished job by hand so we can control both start and finish
	// independently and assert the eviction policy in isolation.
	st := &jobState{
		id:         "longjob",
		command:    "sleep 1800",
		startedAt:  start,
		finishedAt: finish,
		status:     StatusCompleted,
		doneChan:   make(chan struct{}),
	}
	close(st.doneChan)
	s.jobs.Store(st.id, st)

	// A sweep the instant the long job finishes must NOT evict it: under the old
	// (start-based) logic now.Sub(startedAt) == 30m > 10m would have dropped it
	// here, losing the entire grace window.
	s.evictExpired(finish)
	_, err := s.Output(st.id)
	require.NoError(t, err, "a long-running job must keep its grace window after finishing, not be evicted immediately")

	// Still retained near the end of the window.
	s.evictExpired(finish.Add(jobTTL - time.Second))
	_, err = s.Output(st.id)
	require.NoError(t, err, "long job must remain through its full post-finish grace window")

	// Evicted only after finishedAt + jobTTL.
	s.evictExpired(finish.Add(jobTTL + time.Second))
	_, err = s.Output(st.id)
	require.Error(t, err, "long job must be evicted once its post-finish grace window elapses")
}

// TestEviction_RunningJobNeverEvicted asserts a still-running job (finishedAt
// zero) is never dropped, no matter how far the clock advances.
func TestEviction_RunningJobNeverEvicted(t *testing.T) {
	start := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	s := newTestShell(t, func() time.Time { return start })

	st := &jobState{
		id:        "running",
		command:   "sleep inf",
		startedAt: start,
		status:    StatusRunning,
		doneChan:  make(chan struct{}),
	}
	s.jobs.Store(st.id, st)

	// Far past any TTL: a running job has no finish baseline and must survive.
	s.evictExpired(start.Add(100 * jobTTL))
	_, ok := s.jobs.Load(st.id)
	require.True(t, ok, "a running job must never be evicted")
}
