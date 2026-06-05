package agent

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// echoProvider is a content-addressed fake: it replies with the text of the
// latest user message in the request. Because its output is determined purely
// by its input (never by call order), concurrent subagent runs produce
// deterministic, attributable results even when several share one provider —
// the property the stock scriptProvider lacks under -race.
//
// When barrier is non-nil, each Stream arrives at the barrier and blocks until
// every expected stream has arrived before replying. That turns the provider
// into a rendezvous gate: a serial dispatch can never get all streams to the
// barrier at once and would deadlock, so a passing test genuinely proves the
// runs overlap.
type echoProvider struct {
	barrier *rendezvous
}

func (p *echoProvider) Name() string { return "echo" }

func (p *echoProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	if p.barrier != nil {
		p.barrier.arrive()
	}
	reply := latestUserText(req.Messages)
	ch := make(chan llm.Event, 2)
	go func() {
		defer close(ch)
		events := []llm.Event{
			llm.DeltaTextEvent{Text: reply},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		}
		for _, ev := range events {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (p *echoProvider) Models() []llm.Model {
	// A large window keeps these dispatch tests from being coupled to the byte
	// size of the built-in tool descriptions: a window too small to hold the
	// system prompt would make the loop return ErrContextOverflow before the
	// provider is ever called, deadlocking the hand-coordinated concurrency gate.
	return []llm.Model{{ID: "fake-model", Provider: "echo", ContextWindow: 1 << 20, SupportsTools: true}}
}

func (p *echoProvider) SupportsTools() bool  { return true }
func (p *echoProvider) SupportsImages() bool { return false }

// rendezvous is an N-way barrier: the first n callers of arrive block until
// the nth arrives, at which point all are released together. It lets a test
// assert that n goroutines were genuinely in flight at the same instant, since
// fewer than n arrivals never releases and a serial caller would deadlock.
type rendezvous struct {
	n       int
	mu      sync.Mutex
	count   int
	release chan struct{}
}

func newRendezvous(n int) *rendezvous {
	return &rendezvous{n: n, release: make(chan struct{})}
}

func (r *rendezvous) arrive() {
	r.mu.Lock()
	r.count++
	if r.count == r.n {
		close(r.release)
	}
	ch := r.release
	r.mu.Unlock()
	<-ch
}

// latestUserText returns the concatenated text of the last user message in
// history, mirroring how the loop seeds a turn from the user prompt.
func latestUserText(history []message.Message) string {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role != message.RoleUser {
			continue
		}
		if text := textOf(history[i]); text != "" {
			return text
		}
	}
	return ""
}

func TestDispatchParallelRunsSubtasksConcurrentlyInOrder(t *testing.T) {
	// The 3-way rendezvous gates the provider: all three subagents must be in
	// flight at once or the barrier never releases. A serial implementation
	// would deadlock here, so a pass proves genuine concurrency in addition to
	// in-order, attributable results.
	coord := newDispatchCoordinator(t, &echoProvider{barrier: newRendezvous(3)})

	tasks := []SubTask{
		{Agent: "coder", Prompt: "ALPHA-investigate-auth"},
		{Agent: "coder", Prompt: "BRAVO-investigate-db"},
		{Agent: "coder", Prompt: "CHARLIE-investigate-net"},
	}

	results, err := coord.DispatchParallel(context.Background(), tasks, 3)
	require.NoError(t, err)
	require.Len(t, results, 3)

	// Result[i] corresponds to tasks[i] regardless of completion order: the
	// echo provider returns each subagent's own prompt as its final output.
	for i, task := range tasks {
		require.Equal(t, i, results[i].Index)
		require.NoError(t, results[i].Err)
		require.NotEmpty(t, results[i].SessionID)
		require.Equal(t, task.Prompt, results[i].Output, "subtask %d output", i)
	}

	// Every subtask ran in its own fresh session (no sharing).
	seen := map[string]struct{}{}
	for _, r := range results {
		_, dup := seen[r.SessionID]
		require.False(t, dup, "sessions must be distinct per subtask")
		seen[r.SessionID] = struct{}{}
	}
}

func TestDispatchParallelBoundedConcurrencyStillCompletesAll(t *testing.T) {
	coord := newDispatchCoordinator(t, &echoProvider{})

	prompts := []string{"one", "two", "three", "four", "five"}
	tasks := make([]SubTask, len(prompts))
	for i, p := range prompts {
		tasks[i] = SubTask{Agent: "coder", Prompt: p}
	}

	// limit=1 forces fully serial execution; all results must still arrive in
	// input order with the right outputs.
	results, err := coord.DispatchParallel(context.Background(), tasks, 1)
	require.NoError(t, err)
	require.Len(t, results, len(tasks))
	for i, p := range prompts {
		require.NoError(t, results[i].Err)
		require.Equal(t, p, results[i].Output)
	}
}

// countingBlockProvider blocks every Stream until the context is cancelled,
// signalling on a WaitGroup the moment each stream actually starts. Tests wait
// on the WaitGroup to prove real concurrency, then cancel to drive the
// cancellation path — no timers, no sleeps.
type countingBlockProvider struct {
	started *sync.WaitGroup
}

func (p *countingBlockProvider) Name() string { return "block" }

func (p *countingBlockProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	_ = ctx
	_ = req
	p.started.Done()
	// Return a channel that is never closed and never sent on. The loop's
	// callProvider can then only exit via its ctx.Done() branch, so the run
	// ends with a cancellation error and nothing else — strictly deterministic,
	// with no goroutine to leak or race on close.
	return make(chan llm.Event), nil
}

func (p *countingBlockProvider) Models() []llm.Model {
	return []llm.Model{{ID: "fake-model", Provider: "block", ContextWindow: 1 << 20, SupportsTools: true}}
}

func (p *countingBlockProvider) SupportsTools() bool  { return true }
func (p *countingBlockProvider) SupportsImages() bool { return false }

func TestDispatchParallelCancellationStopsSubagents(t *testing.T) {
	var started sync.WaitGroup
	started.Add(3)
	coord := newDispatchCoordinator(t, &countingBlockProvider{started: &started})

	tasks := []SubTask{
		{Agent: "coder", Prompt: "a"},
		{Agent: "coder", Prompt: "b"},
		{Agent: "coder", Prompt: "c"},
	}

	ctx, cancel := context.WithCancel(context.Background())
	type dispatchOutcome struct {
		results []Result
		err     error
	}
	done := make(chan dispatchOutcome, 1)
	go func() {
		results, err := coord.DispatchParallel(ctx, tasks, 3)
		done <- dispatchOutcome{results: results, err: err}
	}()

	// All three subagents actually started streaming concurrently (proves
	// real concurrency) before we cancel — wait on the condition, not a timer.
	started.Wait()
	cancel()

	outcome := <-done
	require.Len(t, outcome.results, 3)
	require.Error(t, outcome.err)
	for i, r := range outcome.results {
		require.Error(t, r.Err, "subtask %d must report cancellation", i)
		require.True(t, errors.Is(r.Err, context.Canceled), "subtask %d err: %v", i, r.Err)
	}
}

func TestDispatchParallelEmptyReturnsNoResults(t *testing.T) {
	coord := newDispatchCoordinator(t, &echoProvider{})
	results, err := coord.DispatchParallel(context.Background(), nil, 4)
	require.NoError(t, err)
	require.Empty(t, results)
}

func TestDispatchParallelUnknownAgentReportsErrorPerTask(t *testing.T) {
	coord := newDispatchCoordinator(t, &echoProvider{})
	tasks := []SubTask{
		{Agent: "coder", Prompt: "good"},
		{Agent: "nope-not-an-agent", Prompt: "bad"},
	}

	results, err := coord.DispatchParallel(context.Background(), tasks, 2)
	require.Error(t, err)
	require.True(t, errors.Is(err, ErrUnknownAgent))

	// The healthy task still completed; only the unknown-agent task failed.
	require.NoError(t, results[0].Err)
	require.Equal(t, "good", results[0].Output)
	require.Error(t, results[1].Err)
	require.True(t, errors.Is(results[1].Err, ErrUnknownAgent))
}

// newDispatchCoordinator builds and Starts a Coordinator wired to provider,
// with a real session repo and a tools registry that satisfies the "task"
// agent's allow-list so Agent("task") never panics in New.
func newDispatchCoordinator(t *testing.T, provider llm.Provider) *Coordinator {
	t.Helper()
	coord, err := NewCoordinator(nil, Dependencies{
		Tools:     tools.NewRegistry(tools.Dependencies{}),
		Sessions:  testRepo(t),
		Providers: map[string]llm.Provider{"p": provider},
	})
	require.NoError(t, err)
	require.NoError(t, coord.Start(context.Background()))
	return coord
}
