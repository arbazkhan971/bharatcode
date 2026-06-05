package tui

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	tea "github.com/charmbracelet/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

// TestSubmitWhileRunning_QueuesAsSteeringAndDelivers is the steering contract
// test. A first prompt starts a run that is held open in the provider. While it
// is in flight, a second plain message is submitted: it must be QUEUED as
// steering (surfaced in the chat as queued, not dropped, and not started as a
// second concurrent Run), and after the turn boundary it must reach the agent
// as a user message in the persisted session.
func TestSubmitWhileRunning_QueuesAsSteeringAndDelivers(t *testing.T) {
	provider := &gatedProvider{release: make(chan struct{})}
	// Turn 1 is gated open (no tool call). When steering arrives, the loop's
	// no-tool-calls branch sees it and continues to turn 2, which carries the
	// steering message in its request history.
	provider.scripts = [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "working on the first thing"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
		{
			llm.DeltaTextEvent{Text: "also handling the steered request"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 1, OutputTokens: 1}},
		},
	}

	h := newAgentHarness(t, provider)
	m := h.model

	const steerText = "also handle errors"

	// Start the first run synchronously. m.running is set inside launchTurn
	// during Update, so it is true the moment Update returns. We drive the run
	// command ourselves so the eventual runDoneMsg is not lost to the harness's
	// lossy poll loop.
	m.input.WriteString("first prompt")
	_, startCmd := m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.running, "first prompt must start a run")

	// Drive the start batch ourselves. Each sub-command (the blocking run driver
	// and the listen command) runs in its own goroutine and forwards its message
	// to msgCh, so the eventual runDoneMsg is captured rather than lost to the
	// harness's lossy poll loop.
	msgCh := runBatch(t, startCmd)

	// Wait until the gated provider has actually begun turn 1, so the Run holds
	// its run mutex before we submit the steering message.
	waitForCalls(t, provider, 1)

	// Submit the second message while the run is in flight. It must be queued as
	// steering, not started as a second concurrent Run.
	m.input.WriteString(steerText)
	_, steerCmd := m.Update(keySpecial("enter", tea.KeyEnter))
	require.Nil(t, steerCmd, "a queued steering message must not start a new run command")
	require.True(t, m.running, "the original run must still be in flight")

	// It surfaces in the chat as queued.
	rendered := plainText(m.chat.Render(200))
	require.Contains(t, rendered, "[queued]", "steering message must be shown as queued")
	require.Contains(t, rendered, steerText, "steering text must be visible in the chat")

	// No second Run was started: the provider has still only been called once.
	require.Equal(t, 1, provider.calls(), "queuing must not trigger a second concurrent Run")

	// Release the gate; the run proceeds through the steering boundary to turn 2.
	close(provider.release)

	// Pump messages (agent events + the run's runDoneMsg) until the run returns.
	done := drainUntilRunDone(t, h, msgCh)
	require.NoError(t, done.err)
	require.False(t, m.running, "run must finish")

	// The provider ran a second turn that carried the steering message, proving
	// the run continued without a restart.
	require.Equal(t, 2, provider.calls(), "steering must continue the same turn into a second provider call")

	// The steering message reached the agent and was persisted as a user message.
	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.True(t, sessionHasUserText(msgs, steerText), "steering text must be delivered to the agent as a user message")

	// The steering text appears exactly once in the chat: as the queued bubble,
	// never echoed into the assistant's reply or double-rendered on delivery.
	require.Equal(t, 1, strings.Count(plainText(m.chat.Render(200)), steerText), "steering text must render exactly once (queued bubble only)")
}

// TestSteeringDoesNotLeakIntoLaterTurnAfterInterrupt locks the fix for stale
// steering: a message queued onto an interrupted turn must not be carried into
// a later, unrelated run. The agent drains its steering queue unconditionally
// at the top of every turn, so the TUI must clear leftovers on a failed run.
func TestSteeringDoesNotLeakIntoLaterTurnAfterInterrupt(t *testing.T) {
	provider := &gatedProvider{release: make(chan struct{})}
	provider.scripts = [][]llm.Event{
		// Turn 1 is gated open and then interrupted; it never completes.
		{llm.DeltaTextEvent{Text: "starting"}, llm.EndEvent{}},
		// A later, unrelated run. Its request must NOT carry the stale steering.
		{llm.DeltaTextEvent{Text: "second prompt reply"}, llm.EndEvent{}},
	}

	h := newAgentHarness(t, provider)
	m := h.model

	const steerText = "stale steering that must not leak"

	// Start and gate the first run.
	m.input.WriteString("first prompt")
	_, startCmd := m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.running)
	msgCh := runBatch(t, startCmd)
	waitForCalls(t, provider, 1)

	// Queue steering onto the in-flight (gated) turn.
	m.input.WriteString(steerText)
	_, steerCmd := m.Update(keySpecial("enter", tea.KeyEnter))
	require.Nil(t, steerCmd)

	// Interrupt: the gated provider returns on ctx cancellation, the run errors
	// out, and the queued steering is left undrained on the shared Loop.
	m.deps.Agent.Interrupt()
	close(provider.release)
	done := drainUntilRunDone(t, h, msgCh)
	require.Error(t, done.err, "interrupted run must report an error")
	require.False(t, m.running)

	// The leftover steering must have been cleared, not carried forward.
	require.Empty(t, m.deps.Agent.PendingSteering(), "interrupt must clear leftover steering")

	// Run a second, unrelated prompt to completion and assert no leak.
	m.input.WriteString("second prompt")
	_, startCmd2 := m.Update(keySpecial("enter", tea.KeyEnter))
	require.True(t, m.running)
	msgCh2 := runBatch(t, startCmd2)
	done2 := drainUntilRunDone(t, h, msgCh2)
	require.NoError(t, done2.err)

	msgs, err := h.repo.Messages(context.Background(), m.sessionID)
	require.NoError(t, err)
	require.False(t, sessionHasUserText(msgs, steerText), "stale steering must not leak into a later run")
}

// runBatch executes the tea.Batch returned by startRun, launching every
// sub-command in its own goroutine and forwarding each produced message onto
// the returned channel. This captures the blocking run driver's eventual
// runDoneMsg instead of dropping it like the harness's poll loop.
func runBatch(t *testing.T, cmd tea.Cmd) <-chan tea.Msg {
	t.Helper()
	require.NotNil(t, cmd)
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	require.True(t, ok, "startRun must return a batch")
	out := make(chan tea.Msg, len(batch)+8)
	for _, sub := range batch {
		c := sub
		go func() {
			if m := c(); m != nil {
				out <- m
			}
		}()
	}
	return out
}

// waitForCalls blocks until the gated provider has been called n times.
func waitForCalls(t *testing.T, p *gatedProvider, n int) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for p.calls() < n {
		if time.Now().After(deadline) {
			t.Fatalf("provider was not called %d times (got %d)", n, p.calls())
		}
		time.Sleep(time.Millisecond)
	}
}

// drainUntilRunDone consumes messages produced by the run batch (agent events
// via the listen command, plus the run's terminal runDoneMsg), feeding each
// into Update so streaming and end-of-run handling execute. It returns once the
// runDoneMsg arrives. It also independently pumps the listen loop so bus events
// keep draining even if the batch's single listen command has already returned.
func drainUntilRunDone(t *testing.T, h *agentHarness, msgCh <-chan tea.Msg) runDoneMsg {
	t.Helper()
	deadline := time.Now().Add(20 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("run did not complete")
		}
		select {
		case msg := <-msgCh:
			if done, ok := msg.(runDoneMsg); ok {
				h.model.Update(done)
				return done
			}
			// Any non-terminal message (e.g. an agent event) is applied and the
			// follow-up listen command it yields is pumped.
			_, next := h.model.Update(msg)
			h.pump(t, next)
		case <-time.After(10 * time.Millisecond):
			h.pump(t, h.model.listenAgent())
		}
	}
}

// sessionHasUserText reports whether msgs contains a user message with text.
func sessionHasUserText(msgs []message.Message, text string) bool {
	for _, m := range msgs {
		if m.Role != message.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if tb, ok := b.(message.TextBlock); ok && strings.Contains(tb.Text, text) {
				return true
			}
		}
	}
	return false
}

// gatedProvider serves scripted turns but blocks the FIRST Stream call until
// release is closed, so a test can keep a run in flight while it submits a
// steering message.
type gatedProvider struct {
	mu      sync.Mutex
	scripts [][]llm.Event
	callN   int
	release chan struct{}
	gated   bool
}

func (p *gatedProvider) Name() string { return "fake" }

func (p *gatedProvider) Stream(ctx context.Context, _ llm.Request) (<-chan llm.Event, error) {
	p.mu.Lock()
	p.callN++
	first := !p.gated
	p.gated = true
	var events []llm.Event
	if len(p.scripts) > 0 {
		events = p.scripts[0]
		p.scripts = p.scripts[1:]
	}
	p.mu.Unlock()

	if first {
		select {
		case <-p.release:
		case <-ctx.Done():
		}
	}

	ch := make(chan llm.Event, len(events)+1)
	go func() {
		defer close(ch)
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

func (p *gatedProvider) Models() []llm.Model {
	// A large window keeps the steering transcript in budget so the assertions
	// stay focused on call ordering rather than tool-doc-driven compaction.
	return []llm.Model{{ID: "fake-model", Provider: "fake", ContextWindow: 1 << 20, SupportsTools: true}}
}

func (p *gatedProvider) SupportsTools() bool  { return true }
func (p *gatedProvider) SupportsImages() bool { return false }

func (p *gatedProvider) calls() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.callN
}
