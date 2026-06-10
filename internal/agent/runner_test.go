package agent

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// msg builds a trivial user message carrying text, for runner tests.
func runnerMsg(text string) message.Message {
	return message.Message{
		Role:    message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}

// TestSessionRunnerStartsAndQueues verifies that the first submission for an
// idle session starts immediately, and a second submission while it runs is
// queued behind it rather than racing — then runs once the first releases.
func TestSessionRunnerStartsAndQueues(t *testing.T) {
	release := make(chan struct{})
	var order []string
	var mu sync.Mutex
	run := func(ctx context.Context, sessionID string, m message.Message) error {
		text := messageText(m)
		mu.Lock()
		order = append(order, text)
		mu.Unlock()
		if text == "first" {
			<-release // hold the active run open so the next submit must queue
		}
		return nil
	}

	r := NewSessionRunner(run)
	h1 := r.Submit(context.Background(), "s1", runnerMsg("first"))
	if got := h1.Disposition(); got != Started {
		t.Fatalf("first submit: disposition = %v, want Started", got)
	}

	// Wait until the first run is actually executing before submitting the second
	// so the queueing is deterministic.
	waitFor(t, func() bool { return r.Running("s1") && firstHasStarted(&mu, &order) })

	h2 := r.Submit(context.Background(), "s1", runnerMsg("second"))
	if got := h2.Disposition(); got != Queued {
		t.Fatalf("second submit: disposition = %v, want Queued", got)
	}
	if got := r.QueueLen("s1"); got != 1 {
		t.Fatalf("QueueLen = %d, want 1", got)
	}

	close(release)
	if err := h1.Wait(); err != nil {
		t.Fatalf("first wait: %v", err)
	}
	if err := h2.Wait(); err != nil {
		t.Fatalf("second wait: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(order) != 2 || order[0] != "first" || order[1] != "second" {
		t.Fatalf("run order = %v, want [first second]", order)
	}
}

func firstHasStarted(mu *sync.Mutex, order *[]string) bool {
	mu.Lock()
	defer mu.Unlock()
	return len(*order) >= 1
}

// TestSessionRunnerOneActivePerSession asserts the runner never invokes the
// underlying RunFunc concurrently: with many overlapping submissions the
// observed concurrency stays at one.
func TestSessionRunnerOneActivePerSession(t *testing.T) {
	var active int32
	var maxActive int32
	run := func(ctx context.Context, sessionID string, m message.Message) error {
		n := atomic.AddInt32(&active, 1)
		for {
			old := atomic.LoadInt32(&maxActive)
			if n <= old || atomic.CompareAndSwapInt32(&maxActive, old, n) {
				break
			}
		}
		time.Sleep(2 * time.Millisecond)
		atomic.AddInt32(&active, -1)
		return nil
	}

	r := NewSessionRunner(run)
	var handles []*RunHandle
	for i := 0; i < 8; i++ {
		handles = append(handles, r.Submit(context.Background(), "s1", runnerMsg("m")))
	}
	for _, h := range handles {
		if err := h.Wait(); err != nil {
			t.Fatalf("wait: %v", err)
		}
	}
	if maxActive != 1 {
		t.Fatalf("max concurrent runs = %d, want 1", maxActive)
	}
}

// TestSessionRunnerWaitReturnsRunError asserts the error from the underlying run
// is delivered to the waiting caller.
func TestSessionRunnerWaitReturnsRunError(t *testing.T) {
	want := errors.New("boom")
	r := NewSessionRunner(func(ctx context.Context, sessionID string, m message.Message) error {
		return want
	})
	h := r.Submit(context.Background(), "s1", runnerMsg("x"))
	if err := h.Wait(); !errors.Is(err, want) {
		t.Fatalf("Wait err = %v, want %v", err, want)
	}
}

// TestSessionRunnerCancelInterruptsActiveAndClearsQueue asserts Cancel both
// interrupts the running turn (via its context) and releases every queued job
// with context.Canceled, atomically.
func TestSessionRunnerCancelInterruptsActiveAndClearsQueue(t *testing.T) {
	started := make(chan struct{})
	run := func(ctx context.Context, sessionID string, m message.Message) error {
		if messageText(m) == "active" {
			close(started)
			<-ctx.Done() // block until cancelled
			return ctx.Err()
		}
		return nil
	}
	r := NewSessionRunner(run)

	active := r.Submit(context.Background(), "s1", runnerMsg("active"))
	<-started
	queued1 := r.Submit(context.Background(), "s1", runnerMsg("q1"))
	queued2 := r.Submit(context.Background(), "s1", runnerMsg("q2"))
	if got := r.QueueLen("s1"); got != 2 {
		t.Fatalf("QueueLen before cancel = %d, want 2", got)
	}

	if !r.Cancel("s1") {
		t.Fatal("Cancel returned false, want true (work was present)")
	}

	if err := active.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("active wait err = %v, want context.Canceled", err)
	}
	if err := queued1.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued1 wait err = %v, want context.Canceled", err)
	}
	if err := queued2.Wait(); !errors.Is(err, context.Canceled) {
		t.Fatalf("queued2 wait err = %v, want context.Canceled", err)
	}
	if got := r.QueueLen("s1"); got != 0 {
		t.Fatalf("QueueLen after cancel = %d, want 0", got)
	}
}

// TestSessionRunnerCancelNoWork asserts cancelling an unknown or idle session is
// a safe no-op reporting false.
func TestSessionRunnerCancelNoWork(t *testing.T) {
	r := NewSessionRunner(func(ctx context.Context, sessionID string, m message.Message) error { return nil })
	if r.Cancel("never-seen") {
		t.Fatal("Cancel of unknown session returned true, want false")
	}
	// A completed session reports no work to cancel.
	if err := r.Submit(context.Background(), "s1", runnerMsg("x")).Wait(); err != nil {
		t.Fatalf("wait: %v", err)
	}
	if r.Cancel("s1") {
		t.Fatal("Cancel of idle session returned true, want false")
	}
}

// TestSessionRunnerDistinctSessionsBothRun asserts independent sessions each get
// their message run (the per-session queues do not drop cross-session work).
func TestSessionRunnerDistinctSessionsBothRun(t *testing.T) {
	var mu sync.Mutex
	seen := map[string]bool{}
	r := NewSessionRunner(func(ctx context.Context, sessionID string, m message.Message) error {
		mu.Lock()
		seen[sessionID] = true
		mu.Unlock()
		return nil
	})
	h1 := r.Submit(context.Background(), "a", runnerMsg("x"))
	h2 := r.Submit(context.Background(), "b", runnerMsg("y"))
	_ = h1.Wait()
	_ = h2.Wait()
	mu.Lock()
	defer mu.Unlock()
	if !seen["a"] || !seen["b"] {
		t.Fatalf("not all sessions ran: %v", seen)
	}
}

// waitFor polls cond until it holds or a short deadline elapses, failing the
// test on timeout. It keeps the concurrency tests deterministic without fixed
// sleeps.
func waitFor(t *testing.T, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before deadline")
}
