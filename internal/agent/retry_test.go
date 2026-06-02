package agent

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// recordingSleep is a no-op sleepFunc that records the durations it is handed,
// so a test can assert the loop fed the backoff schedule's exact delays without
// ever sleeping for real.
type recordingSleep struct {
	mu    sync.Mutex
	waits []time.Duration
}

func (r *recordingSleep) sleep(ctx context.Context, d time.Duration) error {
	r.mu.Lock()
	r.waits = append(r.waits, d)
	r.mu.Unlock()
	return ctx.Err()
}

func (r *recordingSleep) recorded() []time.Duration {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]time.Duration, len(r.waits))
	copy(out, r.waits)
	return out
}

// transientThenOK scripts N turns that each fail mid-stream with a wrapped
// transient sentinel, followed by one turn that succeeds with assistant text.
func transientThenOK(failures int, sentinel error) [][]llm.Event {
	scripts := make([][]llm.Event, 0, failures+1)
	for i := 0; i < failures; i++ {
		scripts = append(scripts, []llm.Event{
			llm.ErrorEvent{Err: fmt.Errorf("provider stream failed: %w", sentinel)},
		})
	}
	scripts = append(scripts, []llm.Event{
		llm.DeltaTextEvent{Text: "recovered"},
		llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
	})
	return scripts
}

func TestRunRetriesTransientProviderErrorThenSucceeds(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// Fail twice with a transient server error, then succeed on the third call.
	provider := &scriptProvider{scripts: transientThenOK(2, llm.ErrServer)}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("agent-test", 16),
	})
	// Deterministic, no-real-sleep backoff: exact delays, generous cap.
	sleeper := &recordingSleep{}
	loop.backoff = llm.Backoff{NoJitter: true, MaxAttempts: 5, Base: time.Second}
	loop.sleep = sleeper.sleep

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("hello")))

	// The provider was called three times: two failures plus the success.
	require.Len(t, provider.reqs, 3)

	// The loop slept exactly twice (once before each retry), with the backoff
	// schedule's deterministic delays. This proves backoff is actually wired,
	// not merely that the loop spun.
	require.Equal(
		t,
		[]time.Duration{loop.backoff.Delay(1), loop.backoff.Delay(2)},
		sleeper.recorded(),
	)

	// The recovered turn's assistant text was persisted as the final message.
	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	last := messages[len(messages)-1]
	require.Equal(t, message.RoleAssistant, last.Role)
	require.Contains(t, textOf(last), "recovered")
}

func TestRunRetriesTransientErrorFromStreamHandshake(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// The provider rejects the Stream handshake transiently once, then accepts.
	provider := &handshakeFailProvider{
		failures: 1,
		err:      fmt.Errorf("provider unavailable: %w", llm.ErrRateLimit),
		success: []llm.Event{
			llm.DeltaTextEvent{Text: "ok now"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})
	sleeper := &recordingSleep{}
	loop.backoff = llm.Backoff{NoJitter: true, MaxAttempts: 4, Base: time.Second}
	loop.sleep = sleeper.sleep

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("hello")))

	// One handshake rejection plus one accepted call.
	require.Equal(t, 2, provider.calls())
	require.Equal(t, []time.Duration{loop.backoff.Delay(1)}, sleeper.recorded())

	messages, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.Contains(t, textOf(messages[len(messages)-1]), "ok now")
}

func TestRunDoesNotRetryPermanentProviderError(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// A terminal auth failure must not be retried.
	permanent := fmt.Errorf("bad key: %w", llm.ErrAuth)
	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.ErrorEvent{Err: permanent}},
		// A second success script exists to prove it is never reached.
		{llm.DeltaTextEvent{Text: "should-not-run"}, llm.EndEvent{}},
	}}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})
	sleeper := &recordingSleep{}
	loop.backoff = llm.Backoff{NoJitter: true, MaxAttempts: 5, Base: time.Second}
	loop.sleep = sleeper.sleep

	err := loop.Run(ctx, sessionID, userMessage("hello"))
	require.Error(t, err)
	require.True(t, errors.Is(err, llm.ErrAuth), "permanent error must propagate: %v", err)

	// Exactly one provider call and zero backoff waits: no retry occurred.
	require.Len(t, provider.reqs, 1)
	require.Empty(t, sleeper.recorded())
}

func TestRunStopsRetryingAtAttemptCapForPersistentTransientError(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	// Every call fails transiently and forever. The loop must give up after the
	// attempt cap rather than retrying without bound.
	provider := &alwaysErrorProvider{err: fmt.Errorf("still down: %w", llm.ErrServer)}

	const maxAttempts = 3
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})
	sleeper := &recordingSleep{}
	loop.backoff = llm.Backoff{NoJitter: true, MaxAttempts: maxAttempts, Base: time.Second}
	loop.sleep = sleeper.sleep

	err := loop.Run(ctx, sessionID, userMessage("hello"))
	require.Error(t, err)
	require.True(t, errors.Is(err, llm.ErrServer), "last transient error must propagate: %v", err)

	// Exactly maxAttempts provider calls, with one fewer backoff wait between
	// them. This proves the cap holds and there is no infinite retry.
	require.Equal(t, maxAttempts, provider.calls())
	require.Len(t, sleeper.recorded(), maxAttempts-1)
	require.Equal(
		t,
		[]time.Duration{loop.backoff.Delay(1), loop.backoff.Delay(2)},
		sleeper.recorded(),
	)
}

func TestCallProviderWithRetryStopsOnContextCancelDuringBackoff(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	repo := testRepo(t)
	registry := newFakeRegistry()

	provider := &alwaysErrorProvider{err: fmt.Errorf("down: %w", llm.ErrServer)}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})
	loop.backoff = llm.Backoff{NoJitter: true, MaxAttempts: 5, Base: time.Second}
	// The sleeper cancels the context the moment a backoff wait begins, modelling
	// an Interrupt landing mid-backoff, then reports the cancellation.
	loop.sleep = func(c context.Context, _ time.Duration) error {
		cancel()
		return c.Err()
	}

	_, _, _, err := loop.callProviderWithRetry(ctx, nil)
	require.ErrorIs(t, err, context.Canceled)
	// Only the first attempt ran; the backoff cancellation halted further calls.
	require.Equal(t, 1, provider.calls())
}

func TestContextSleepHonoursContextAndDelay(t *testing.T) {
	t.Run("cancelled context returns promptly", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		// A one-hour delay would block forever if the context were ignored; a
		// prompt return proves cancellation short-circuits the wait.
		start := time.Now()
		err := contextSleep(ctx, time.Hour)
		require.ErrorIs(t, err, context.Canceled)
		require.Less(t, time.Since(start), time.Second)
	})

	t.Run("positive delay on live context returns nil after waiting", func(t *testing.T) {
		err := contextSleep(context.Background(), time.Millisecond)
		require.NoError(t, err)
	})

	t.Run("non-positive delay returns immediately with no error", func(t *testing.T) {
		// Full jitter can round a delay to zero; that path must retry at once.
		require.NoError(t, contextSleep(context.Background(), 0))
		require.NoError(t, contextSleep(context.Background(), -time.Second))
	})
}

func TestNewWiresDefaultSleep(t *testing.T) {
	// Guard against a refactor dropping the contextSleep default, which would
	// leave sleep nil and panic on the first production retry.
	loop := New(Config{
		Provider: &scriptProvider{},
		Tools:    newFakeRegistry(),
		Sessions: testRepo(t),
	})
	require.NotNil(t, loop.sleep)
}

// handshakeFailProvider rejects Provider.Stream synchronously the first
// `failures` times, then streams `success`. It exercises the retry path for the
// Stream handshake error (as opposed to a mid-stream ErrorEvent).
type handshakeFailProvider struct {
	mu       sync.Mutex
	failures int
	count    int
	err      error
	success  []llm.Event
}

func (p *handshakeFailProvider) Name() string { return "fake" }

func (p *handshakeFailProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	_ = req
	p.mu.Lock()
	p.count++
	fail := p.count <= p.failures
	p.mu.Unlock()
	if fail {
		return nil, p.err
	}
	ch := make(chan llm.Event, len(p.success))
	go func() {
		defer close(ch)
		for _, ev := range p.success {
			select {
			case <-ctx.Done():
				return
			case ch <- ev:
			}
		}
	}()
	return ch, nil
}

func (p *handshakeFailProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func (p *handshakeFailProvider) Models() []llm.Model {
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: 8192, SupportsTools: true}}
}

func (p *handshakeFailProvider) SupportsTools() bool  { return true }
func (p *handshakeFailProvider) SupportsImages() bool { return false }

// alwaysErrorProvider rejects every Stream handshake with the same error and
// counts the attempts, so a test can assert the retry cap is honoured.
type alwaysErrorProvider struct {
	mu    sync.Mutex
	count int
	err   error
}

func (p *alwaysErrorProvider) Name() string { return "fake" }

func (p *alwaysErrorProvider) Stream(ctx context.Context, req llm.Request) (<-chan llm.Event, error) {
	_ = ctx
	_ = req
	p.mu.Lock()
	p.count++
	p.mu.Unlock()
	return nil, p.err
}

func (p *alwaysErrorProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.count
}

func (p *alwaysErrorProvider) Models() []llm.Model {
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: 8192, SupportsTools: true}}
}

func (p *alwaysErrorProvider) SupportsTools() bool  { return true }
func (p *alwaysErrorProvider) SupportsImages() bool { return false }
