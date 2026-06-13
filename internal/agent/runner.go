package agent

import (
	"context"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// RunFunc drives one user turn to completion against a session. *Loop.Run
// satisfies it; tests inject a deterministic fake. A RunFunc is expected to
// honour ctx cancellation so an in-flight turn can be interrupted.
type RunFunc func(ctx context.Context, sessionID string, msg message.Message) error

// Disposition reports how Submit handled a message: it either started running
// immediately because the session was idle, or was queued behind the session's
// active run.
type Disposition int

const (
	// Started means the submitted message began running immediately because no
	// other run was active for the session.
	Started Disposition = iota
	// Queued means the submitted message was placed on the session's FIFO queue
	// behind the run already in flight, and will run once that run (and any
	// messages queued ahead of it) completes.
	Queued
)

// SessionRunner serialises agent runs per session: at most one run is active
// for a given session at a time, additional messages submitted while a run is
// in flight are queued in FIFO order, and an in-flight-plus-queued session can
// be cancelled atomically. It is the run-discipline seam the interactive layer
// drives turns through, so a user who sends a second prompt mid-turn has it
// queued rather than racing the session's Loop or panicking it.
//
// The runner is agnostic to what a run does: it is constructed over a RunFunc
// (typically a per-session Loop's Run, resolved by the caller). Serialisation
// is PER SESSION: each session's queue has its own execution mutex, so distinct
// sessions now run concurrently while each session's own messages stay ordered.
// The caller is responsible for ensuring distinct sessions resolve to distinct
// Loop instances (a single Loop rejects concurrent Run calls); the runner only
// guarantees a session never invokes its own RunFunc twice at once.
type SessionRunner struct {
	run RunFunc

	mu       sync.Mutex
	sessions map[string]*sessionQueue
}

// sessionQueue holds one session's run state: whether a drain worker is active,
// the FIFO of pending jobs, the cancel func for the job currently running (or
// about to run), and the per-session execution mutex. All fields except execMu
// are guarded by SessionRunner.mu; execMu is taken by the session's single
// drain goroutine only while its RunFunc runs (never while mu is held).
type sessionQueue struct {
	running bool
	pending []*job
	cancel  context.CancelFunc

	// execMu serialises this session's RunFunc invocations. Only one drain
	// goroutine exists per session (guarded by running under mu), so execMu is
	// effectively single-acquirer; it is kept to preserve the explicit
	// "one exec per session" invariant. Because it lives on the session queue
	// rather than the runner, distinct sessions execute concurrently.
	execMu sync.Mutex
}

// job is one queued message together with the channel its completion error is
// delivered on. done is buffered (cap 1) so the worker never blocks delivering
// the result even when no caller is waiting.
type job struct {
	ctx  context.Context
	msg  message.Message
	done chan error
}

// RunHandle is returned by Submit so a caller can observe how the message was
// dispatched and block for its completion. Callers that fire-and-forget can
// ignore it; callers that need the blocking semantics of a direct Run (the
// Workspace seam) call Wait.
type RunHandle struct {
	job         *job
	disposition Disposition
}

// Disposition reports whether the submitted message started immediately or was
// queued behind an active run.
func (h *RunHandle) Disposition() Disposition { return h.disposition }

// Wait blocks until the submitted message's run completes and returns its
// error. A cancelled run (via Cancel) returns context.Canceled. Wait may be
// called at most once meaningfully; the result is delivered exactly once.
func (h *RunHandle) Wait() error { return <-h.job.done }

// NewSessionRunner returns a SessionRunner that drives runs through run. run
// must be non-nil.
func NewSessionRunner(run RunFunc) *SessionRunner {
	return &SessionRunner{
		run:      run,
		sessions: make(map[string]*sessionQueue),
	}
}

// Submit accepts msg for sessionID and returns a handle describing whether it
// started immediately or was queued. Submit never blocks on the run itself: it
// records the job and, when the session was idle, starts a drain worker that
// processes the session's queue in order. Use the returned handle's Wait to
// block for completion.
func (r *SessionRunner) Submit(ctx context.Context, sessionID string, msg message.Message) *RunHandle {
	j := &job{ctx: ctx, msg: msg, done: make(chan error, 1)}

	r.mu.Lock()
	sq := r.sessions[sessionID]
	if sq == nil {
		sq = &sessionQueue{}
		r.sessions[sessionID] = sq
	}
	sq.pending = append(sq.pending, j)
	disposition := Queued
	if !sq.running {
		sq.running = true
		disposition = Started
		go r.drain(sessionID, sq)
	}
	r.mu.Unlock()

	return &RunHandle{job: j, disposition: disposition}
}

// drain processes sq's queue for sessionID until it empties, running one job at
// a time. Each job runs under a fresh cancellable context derived from the
// job's own context, recorded as sq.cancel so Cancel can interrupt it (whether
// it is already executing or still waiting on execMu). The session's execMu is
// held only across the underlying run so a session's Loop is never entered
// concurrently; distinct sessions hold distinct execMu and so run in parallel.
func (r *SessionRunner) drain(sessionID string, sq *sessionQueue) {
	for {
		r.mu.Lock()
		if len(sq.pending) == 0 {
			sq.running = false
			sq.cancel = nil
			r.mu.Unlock()
			return
		}
		j := sq.pending[0]
		sq.pending = sq.pending[1:]
		runCtx, cancel := context.WithCancel(j.ctx)
		sq.cancel = cancel
		r.mu.Unlock()

		sq.execMu.Lock()
		err := r.run(runCtx, sessionID, j.msg)
		sq.execMu.Unlock()

		cancel()
		r.mu.Lock()
		sq.cancel = nil
		r.mu.Unlock()

		j.done <- err
	}
}

// Cancel atomically stops sessionID's active run and clears its pending queue,
// returning true when the session had any active or queued work. Cancelling the
// active run interrupts it through its context; each cleared pending job
// completes with context.Canceled so a caller blocked in Wait is released. A
// session with no work returns false. Cancel holds the runner lock for the whole
// operation, so the cancel-and-clear is observed as a single step.
func (r *SessionRunner) Cancel(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	sq := r.sessions[sessionID]
	if sq == nil {
		return false
	}
	had := sq.cancel != nil || len(sq.pending) > 0
	if sq.cancel != nil {
		sq.cancel()
	}
	for _, j := range sq.pending {
		j.done <- context.Canceled
	}
	sq.pending = nil
	return had
}

// CancelAll cancels every session's active run and clears every queue. It is
// the interrupt-everything affordance the Workspace seam uses, where the caller
// does not track which session is live. It returns the number of sessions that
// had work cancelled.
func (r *SessionRunner) CancelAll() int {
	r.mu.Lock()
	ids := make([]string, 0, len(r.sessions))
	for id := range r.sessions {
		ids = append(ids, id)
	}
	r.mu.Unlock()
	n := 0
	for _, id := range ids {
		if r.Cancel(id) {
			n++
		}
	}
	return n
}

// Remove drops sessionID's queue from the runner when it is idle (no active or
// draining run and no pending jobs), so a released session does not leak its
// bookkeeping and the sessions map does not grow unbounded across long-lived
// processes. Removing a session that is running, has queued work, or was never
// seen is a no-op; callers that need to stop live work first call Cancel.
func (r *SessionRunner) Remove(sessionID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	sq := r.sessions[sessionID]
	if sq == nil {
		return
	}
	if sq.running || len(sq.pending) > 0 {
		return
	}
	delete(r.sessions, sessionID)
}

// Running reports whether a run is currently active (or draining) for
// sessionID.
func (r *SessionRunner) Running(sessionID string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	sq := r.sessions[sessionID]
	return sq != nil && sq.running
}

// QueueLen returns the number of messages queued behind the active run for
// sessionID (not counting the one currently executing).
func (r *SessionRunner) QueueLen(sessionID string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	sq := r.sessions[sessionID]
	if sq == nil {
		return 0
	}
	return len(sq.pending)
}
