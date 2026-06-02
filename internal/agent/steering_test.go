package agent

import (
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// TestSteerMidRunReachesProviderAsNextUserMessage drives a multi-turn scripted
// run and calls Steer at an exact boundary (from inside a tool, which runs
// synchronously between provider calls). It asserts the steering text is
// consumed as the next user message: it appears in the history sent to the
// provider on a subsequent turn, persists to the session as a user message, and
// the run continues to completion without a restart.
func TestSteerMidRunReachesProviderAsNextUserMessage(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()

	const steerText = "also handle errors"

	// steerOnce calls Steer exactly once, from inside the tool's Run, so the
	// steering message is queued at a deterministic mid-run boundary.
	var loop *Loop
	var once sync.Once
	steerer := &steeringTool{name: "probe", result: "ok", onRun: func() {
		once.Do(func() {
			queued := loop.Steer(steerText)
			require.True(t, queued, "Steer during an in-flight Run must report queued")
		})
	}}
	registry.Register(steerer)

	provider := &scriptProvider{scripts: [][]llm.Event{
		// Turn 1: model calls the probe tool. The tool steers mid-run.
		{
			llm.DeltaTextEvent{Text: "inspecting"},
			llm.ToolUseEndEvent{ID: "call-1", Name: "probe", Input: json.RawMessage(`{}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}},
		},
		// Turn 2: the steering message must already be in this request's history.
		{
			llm.DeltaTextEvent{Text: "acknowledged steering"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}},
		},
	}}

	loop = New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Bus:      pubsub.NewTopic[Event]("steer-test", 16),
	})

	err := loop.Run(ctx, sessionID, userMessage("start"))
	require.NoError(t, err)

	// The run continued without restart: exactly two provider turns ran.
	require.Len(t, provider.reqs, 2, "run must continue across the steering boundary without restarting")

	// The steering text was delivered to the provider as a user message on the
	// second turn (it was not present on the first).
	require.False(t, requestHasUserText(provider.reqs[0], steerText), "steering must not appear before it was queued")
	require.True(t, requestHasUserText(provider.reqs[1], steerText), "steering must reach the provider as the next user message")

	// It was also persisted to the session as a user message.
	msgs, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.True(t, sessionHasUserText(msgs, steerText), "steering must persist as a user message in the session")
}

// TestSteerWithNoRunInFlightReportsNotQueued asserts the sentinel behavior: when
// no Run is active, Steer returns false so the caller starts a fresh turn.
func TestSteerWithNoRunInFlightReportsNotQueued(t *testing.T) {
	repo := testRepo(t)
	registry := newFakeRegistry()
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: &scriptProvider{},
		Tools:    registry,
		Sessions: repo,
	})
	require.False(t, loop.Steer("queue me"), "Steer with no Run in flight must report not queued")
	require.Empty(t, loop.PendingSteering(), "nothing should be queued when no Run is in flight")
}

// TestSteerFromAnotherGoroutineDoesNotRace hammers Steer from a separate
// goroutine while Run is blocked in the provider, then interrupts. Run under
// -race, this asserts the steering queue is free of data races against the run
// loop's drains and the running-flag transitions.
func TestSteerFromAnotherGoroutineDoesNotRace(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)
	registry := newFakeRegistry()
	provider := &blockingProvider{started: make(chan struct{})}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})

	errCh := make(chan error, 1)
	go func() {
		errCh <- loop.Run(ctx, sessionID, userMessage("wait"))
	}()
	<-provider.started

	// Concurrently hammer Steer while the Run is blocked in the provider.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 100; i++ {
			loop.Steer("steer")
		}
	}()
	<-done

	loop.Interrupt()
	select {
	case err := <-errCh:
		require.True(t, errors.Is(err, context.Canceled), "expected cancellation: %v", err)
	case <-time.After(time.Second):
		t.Fatal("Run did not return after Interrupt")
	}

	// After the run releases, the running flag is clear and Steer no longer
	// queues (it reports not-in-flight), matching the sentinel contract.
	require.False(t, loop.Steer("after"), "Steer after Run must report not queued")

	// Leftover steering that was hammered onto the interrupted turn but never
	// drained remains retrievable via PendingSteering (the caller is responsible
	// for discarding or delivering it), and draining it leaves the queue empty.
	require.NotEmpty(t, loop.PendingSteering(), "undrained steering must be retrievable after an interrupted run")
	require.Empty(t, loop.PendingSteering(), "PendingSteering must clear the queue")
}

// requestHasUserText reports whether req carries a user message containing text.
func requestHasUserText(req llm.Request, text string) bool {
	for _, m := range req.Messages {
		if m.Role != message.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tb, ok := b.(message.TextBlock); ok && tb.Text == text {
				return true
			}
		}
	}
	return false
}

// sessionHasUserText reports whether msgs contains a user message with text.
func sessionHasUserText(msgs []message.Message, text string) bool {
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tb, ok := b.(message.TextBlock); ok && tb.Text == text {
				return true
			}
		}
	}
	return false
}

// steeringTool is a real tool whose Run invokes an onRun hook, letting a test
// queue steering at an exact mid-run boundary (tools run synchronously between
// provider calls).
type steeringTool struct {
	name   string
	result string
	onRun  func()
}

func (t *steeringTool) Name() string            { return t.name }
func (t *steeringTool) Description() string     { return "Test steering tool " + t.name }
func (t *steeringTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *steeringTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	_ = args
	if t.onRun != nil {
		t.onRun()
	}
	return tools.Result{Content: t.result}, nil
}
