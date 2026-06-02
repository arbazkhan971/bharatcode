package agent

import (
	"context"
	"encoding/json"
	"sync"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/hooks"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/arbazkhan971/bharatcode/internal/tools"
	"github.com/stretchr/testify/require"
)

// firedHook captures one Fire call so a test can assert which lifecycle event
// fired with which payload.
type firedHook struct {
	event   hooks.Event
	payload any
}

// captureHooks is a hookFirer that records every Fire call in order. It is the
// fake injected via Config.Hooks so tests observe lifecycle hooks firing
// without running real shell commands.
type captureHooks struct {
	mu    sync.Mutex
	fired []firedHook
}

func (h *captureHooks) Fire(ctx context.Context, event hooks.Event, payload any) (hooks.Decision, error) {
	_ = ctx
	h.mu.Lock()
	defer h.mu.Unlock()
	h.fired = append(h.fired, firedHook{event: event, payload: payload})
	return hooks.Decision{Continue: true}, nil
}

// events returns the recorded events in order.
func (h *captureHooks) events() []hooks.Event {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := make([]hooks.Event, len(h.fired))
	for i, f := range h.fired {
		out[i] = f.event
	}
	return out
}

// count returns how many times event fired.
func (h *captureHooks) count(event hooks.Event) int {
	h.mu.Lock()
	defer h.mu.Unlock()
	n := 0
	for _, f := range h.fired {
		if f.event == event {
			n++
		}
	}
	return n
}

// payloadFor returns the payload of the first recorded fire of event.
func (h *captureHooks) payloadFor(event hooks.Event) (any, bool) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for _, f := range h.fired {
		if f.event == event {
			return f.payload, true
		}
	}
	return nil, false
}

// TestRunFiresLifecycleHooks asserts the agent loop fires SessionStart once at
// the start of a session's first turn, FileEdit after a successful write-class
// tool, and SessionEnd at completion, each with the correct payload.
func TestRunFiresLifecycleHooks(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "edit", result: "edited"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		// Turn 1: model edits a file.
		{
			llm.DeltaTextEvent{Text: "Editing the file."},
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: json.RawMessage(`{"path":"src/main.go"}`)},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 10, OutputTokens: 5}},
		},
		// Turn 2: text-only reply ends the turn.
		{
			llm.DeltaTextEvent{Text: "Done."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 8, OutputTokens: 4}},
		},
	}}

	capture := &captureHooks{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Hooks:    capture,
		Bus:      pubsub.NewTopic[Event]("agent-test", 16),
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("update the file")))

	// SessionStart fired exactly once with the session ID.
	require.Equal(t, 1, capture.count(hooks.SessionStart), "SessionStart must fire once at run start")
	startPayload, ok := capture.payloadFor(hooks.SessionStart)
	require.True(t, ok)
	require.Equal(t, hooks.SessionPayload{SessionID: sessionID}, startPayload)

	// FileEdit fired after the write-class tool succeeded, with the edited path.
	require.Equal(t, 1, capture.count(hooks.FileEdit), "FileEdit must fire after a write-class tool")
	editPayload, ok := capture.payloadFor(hooks.FileEdit)
	require.True(t, ok)
	require.Equal(t, hooks.FileEditPayload{Path: "src/main.go", SessionID: sessionID}, editPayload)

	// SessionEnd fired exactly once at completion with the session ID.
	require.Equal(t, 1, capture.count(hooks.SessionEnd), "SessionEnd must fire at completion")
	endPayload, ok := capture.payloadFor(hooks.SessionEnd)
	require.True(t, ok)
	require.Equal(t, hooks.SessionPayload{SessionID: sessionID}, endPayload)

	// Ordering: SessionStart precedes FileEdit precedes SessionEnd. The same
	// engine also receives the per-tool PreToolUse/PostToolUse events (fired by
	// hookedTool), so assert the relative order of the lifecycle events rather
	// than an exact sequence.
	require.Equal(
		t,
		[]hooks.Event{
			hooks.SessionStart,
			hooks.PreToolUse,
			hooks.PostToolUse,
			hooks.FileEdit,
			hooks.SessionEnd,
		},
		capture.events(),
		"lifecycle and tool hooks must fire in this order",
	)
}

// TestRunFiresSessionStartOnlyOnFirstTurn asserts SessionStart fires on the
// session's first turn but not on a subsequent turn in the same session, while
// SessionEnd fires on every run.
func TestRunFiresSessionStartOnlyOnFirstTurn(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "First."}, llm.EndEvent{}},
		{llm.DeltaTextEvent{Text: "Second."}, llm.EndEvent{}},
	}}

	capture := &captureHooks{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Hooks:    capture,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("first turn")))
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("second turn")))

	require.Equal(t, 1, capture.count(hooks.SessionStart), "SessionStart must not refire on the second turn")
	require.Equal(t, 2, capture.count(hooks.SessionEnd), "SessionEnd must fire on every run")
}

// TestRunFiresSessionEndOnCancel asserts SessionEnd fires even when the run is
// cancelled mid-flight via Interrupt.
func TestRunFiresSessionEndOnCancel(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	provider := &blockingProvider{started: make(chan struct{})}

	capture := &captureHooks{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Hooks:    capture,
	})

	errCh := make(chan error, 1)
	go func() { errCh <- loop.Run(ctx, sessionID, userMessage("wait")) }()
	<-provider.started
	loop.Interrupt()
	<-errCh

	require.Equal(t, 1, capture.count(hooks.SessionEnd), "SessionEnd must fire when a run is cancelled")
}

// TestRunSkipsFileEditForFailedWriteTool asserts FileEdit does not fire when a
// write-class tool returns an error result.
func TestRunSkipsFileEditForFailedWriteTool(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&erroringTool{name: "edit"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-1", Name: "edit", Input: json.RawMessage(`{"path":"src/main.go"}`)},
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	capture := &captureHooks{}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
		Hooks:    capture,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("edit it")))
	require.Equal(t, 0, capture.count(hooks.FileEdit), "FileEdit must not fire when the write tool errored")
}

// TestRunWithNilHooksDoesNotPanic asserts a Config without a hooks engine runs
// a full turn (including a write-class tool) without firing or panicking.
func TestRunWithNilHooksDoesNotPanic(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	registry := newFakeRegistry()
	registry.Register(&recordingTool{name: "write", result: "wrote"})

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.ToolUseEndEvent{ID: "call-1", Name: "write", Input: json.RawMessage(`{"path":"src/main.go"}`)},
			llm.EndEvent{},
		},
		{llm.DeltaTextEvent{Text: "Done."}, llm.EndEvent{}},
	}}

	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    registry,
		Sessions: repo,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("write it")))
}

// erroringTool is a tool whose Run returns an IsError result without a Go
// error, modelling a write that failed at the tool layer.
type erroringTool struct {
	name string
}

func (t *erroringTool) Name() string            { return t.name }
func (t *erroringTool) Description() string     { return "Erroring tool " + t.name }
func (t *erroringTool) Schema() json.RawMessage { return json.RawMessage(`{"type":"object"}`) }

func (t *erroringTool) Run(ctx context.Context, args json.RawMessage) (tools.Result, error) {
	_ = ctx
	_ = args
	return tools.Result{Content: "write failed", IsError: true}, nil
}
