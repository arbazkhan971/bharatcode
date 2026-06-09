package app

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// recvUIEvent reads one UIEvent from ch or fails the test after a short timeout,
// so a fan-in regression surfaces as a clear failure instead of a hung test.
func recvUIEvent(t *testing.T, ch <-chan UIEvent) UIEvent {
	t.Helper()
	select {
	case ev, ok := <-ch:
		require.True(t, ok, "consolidated stream closed unexpectedly")
		return ev
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for consolidated UI event")
		return UIEvent{}
	}
}

// TestUIEventWrappers asserts each wrapping constructor sets the matching Kind
// and payload field and leaves the others zero.
func TestUIEventWrappers(t *testing.T) {
	agentEv := agent.Event{Kind: agent.EventToolCalled, ToolName: "bash"}
	ev := AgentUIEvent(agentEv)
	require.Equal(t, UIEventAgent, ev.Kind)
	require.Equal(t, agentEv, ev.Agent)
	require.Equal(t, pubsub.PermissionRequest{}, ev.Permission)
	require.Equal(t, Notice{}, ev.Notice)

	req := pubsub.PermissionRequest{Tool: "edit", Reason: "write file"}
	ev = PermissionUIEvent(req)
	require.Equal(t, UIEventPermission, ev.Kind)
	require.Equal(t, req, ev.Permission)
	require.Equal(t, agent.Event{}, ev.Agent)

	notice := Notice{Title: "lsp", Body: "ready"}
	ev = NoticeUIEvent(notice)
	require.Equal(t, UIEventNotice, ev.Kind)
	require.Equal(t, notice, ev.Notice)
}

// TestFanInAgentEvents publishes agent events on the bus's agent topic and
// asserts they arrive on the consolidated stream wrapped as UIEventAgent, in
// order, carrying the original payload.
func TestFanInAgentEvents(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	ctx := context.Background()
	stream := FanIn(ctx, bus)
	defer stream.Close()

	ch, cancel := stream.Subscribe()
	defer cancel()

	want := []agent.Event{
		{Kind: agent.EventTurnStarted, SessionID: "s1"},
		{Kind: agent.EventLLMResponse, SessionID: "s1"},
		{Kind: agent.EventTurnFinished, SessionID: "s1"},
	}
	for _, ev := range want {
		bus.Agent.Publish(ctx, ev)
	}

	for _, ev := range want {
		got := recvUIEvent(t, ch)
		require.Equal(t, UIEventAgent, got.Kind)
		require.Equal(t, ev, got.Agent)
	}
}

// TestFanInPermissionRoundTrip asserts a permission request published on the bus
// arrives on the consolidated stream and that answering on the embedded Reply
// channel still unblocks the producer — the request/response handshake must
// survive consolidation.
func TestFanInPermissionRoundTrip(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	ctx := context.Background()
	stream := FanIn(ctx, bus)
	defer stream.Close()

	ch, cancel := stream.Subscribe()
	defer cancel()

	reply := make(chan pubsub.PermissionDecision, 1)
	req := pubsub.PermissionRequest{Tool: "bash", Reason: "list", Reply: reply}
	go bus.Permission.Publish(ctx, req)

	got := recvUIEvent(t, ch)
	require.Equal(t, UIEventPermission, got.Kind)
	require.Equal(t, "bash", got.Permission.Tool)
	require.NotNil(t, got.Permission.Reply, "Reply channel must survive fan-in")

	got.Permission.Reply <- pubsub.PermissionDecision{Approved: true}

	select {
	case decision := <-reply:
		require.True(t, decision.Approved)
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for permission decision on the original Reply channel")
	}
}

// TestFanInBothSources asserts events from both source topics multiplex onto the
// one consolidated stream, each tagged with its originating kind.
func TestFanInBothSources(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	ctx := context.Background()
	stream := FanIn(ctx, bus)
	defer stream.Close()

	ch, cancel := stream.Subscribe()
	defer cancel()

	bus.Agent.Publish(ctx, agent.Event{Kind: agent.EventLLMResponse})
	bus.Permission.Publish(ctx, pubsub.PermissionRequest{Tool: "write"})

	var sawAgent, sawPermission bool
	for i := 0; i < 2; i++ {
		switch got := recvUIEvent(t, ch); got.Kind {
		case UIEventAgent:
			sawAgent = true
		case UIEventPermission:
			require.Equal(t, "write", got.Permission.Tool)
			sawPermission = true
		default:
			t.Fatalf("unexpected UI event kind %d", got.Kind)
		}
	}
	require.True(t, sawAgent, "expected an agent-sourced UI event")
	require.True(t, sawPermission, "expected a permission-sourced UI event")
}

// TestUIEventMustDeliverClassification pins the lossy/must-deliver split the
// fan-in relies on: the high-frequency streaming agent kinds are lossy, every
// other agent kind plus permission and notice events are must-deliver.
func TestUIEventMustDeliverClassification(t *testing.T) {
	lossy := []agent.EventKind{agent.EventLLMResponse, agent.EventToolResult}
	for _, k := range lossy {
		require.Falsef(t, AgentUIEvent(agent.Event{Kind: k}).mustDeliver(),
			"agent event kind %d should be lossy", k)
	}

	mustDeliver := []agent.EventKind{
		agent.EventTurnStarted,
		agent.EventToolCalled,
		agent.EventLoopDetected,
		agent.EventTurnFinished,
		agent.EventRunError,
		agent.EventAutoCompacted,
	}
	for _, k := range mustDeliver {
		require.Truef(t, AgentUIEvent(agent.Event{Kind: k}).mustDeliver(),
			"agent event kind %d should be must-deliver", k)
	}

	require.True(t, PermissionUIEvent(pubsub.PermissionRequest{Tool: "bash"}).mustDeliver(),
		"permission requests must always be delivered")
	require.True(t, NoticeUIEvent(Notice{Title: "x"}).mustDeliver(),
		"notices must always be delivered")
}

// TestFanInMustDeliverSurvivesSlowSubscriber is the core P1b guarantee: when the
// UI subscriber is too slow to keep up, the consolidated output topic overflows
// and lossy streaming deltas are dropped, yet the must-deliver events — a
// terminal turn transition and a permission request — still arrive. The loss is
// induced at the fan-in's output stage (a saturated slow reader), which is
// exactly what the lossy/must-deliver split governs; the source topics are not
// overflowed, so the pumps receive every event and the only question is how the
// output stage treats it.
func TestFanInMustDeliverSurvivesSlowSubscriber(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	ctx := context.Background()
	stream := FanIn(ctx, bus)
	defer stream.Close()

	ch, cancel := stream.Subscribe()
	defer cancel()

	// Saturate the output stage with a burst of lossy deltas, then stop. The
	// burst exceeds the output buffer (uiEventBufferSize) so the lossy Publish
	// path is forced to drop, while the slow reader below keeps the buffer full
	// long enough that the must-deliver events published next must back-pressure
	// through it. Pacing keeps the agent source topic (same buffer size) from
	// overflowing, and the burst is published before the must-deliver agent
	// event with no concurrent flooding, so that event is never lost at source.
	const burst = uiEventBufferSize * 3
	for i := 0; i < burst; i++ {
		bus.Agent.Publish(ctx, agent.Event{Kind: agent.EventLLMResponse, SessionID: "s1"})
		// Let the pump keep draining the source so its buffer never overflows;
		// the slow reader below is what keeps the output saturated.
		if i%32 == 0 {
			time.Sleep(time.Millisecond)
		}
	}

	// With the flood finished, publish the two must-deliver events. They route
	// through PublishBlocking and must land despite the still-congested output.
	reply := make(chan pubsub.PermissionDecision, 1)
	bus.Permission.Publish(ctx, pubsub.PermissionRequest{Tool: "edit", Reply: reply})
	bus.Agent.Publish(ctx, agent.Event{Kind: agent.EventTurnFinished, SessionID: "s1"})

	// Drain slowly (a sleep per read) so the output buffer stays saturated while
	// the must-deliver events work their way through, and assert both survive.
	deadline := time.After(10 * time.Second)
	var sawTurnFinished, sawPermission bool
	for !(sawTurnFinished && sawPermission) {
		select {
		case ev, ok := <-ch:
			require.True(t, ok, "consolidated stream closed before must-deliver events arrived")
			switch ev.Kind {
			case UIEventPermission:
				require.Equal(t, "edit", ev.Permission.Tool)
				sawPermission = true
			case UIEventAgent:
				if ev.Agent.Kind == agent.EventTurnFinished {
					sawTurnFinished = true
				}
			}
			time.Sleep(time.Millisecond) // keep the reader behind the burst
		case <-deadline:
			t.Fatalf("must-deliver events lost under load: turnFinished=%v permission=%v drops=%d",
				sawTurnFinished, sawPermission, stream.Out().DropCount())
		}
	}

	// Confirm the lossy path actually shed events at the output stage — otherwise
	// the test would not have exercised back-pressure at all.
	require.Positive(t, stream.Out().DropCount(),
		"expected the slow subscriber to force lossy drops at the output stage")
}

// TestFanInCloseStopsPumps asserts Close tears the fan-in down: the consolidated
// topic is closed (its subscriber channel drains to closed) and Close is
// idempotent. The package-level goleak TestMain additionally guarantees no pump
// goroutine survives.
func TestFanInCloseStopsPumps(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	stream := FanIn(context.Background(), bus)
	ch, cancel := stream.Subscribe()
	defer cancel()

	stream.Close()
	stream.Close() // idempotent

	select {
	case _, ok := <-ch:
		require.False(t, ok, "consolidated topic should be closed after stream.Close")
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for consolidated topic to close")
	}
}

// TestFanInContextCancelStopsPumps asserts cancelling the context drains the
// pump goroutines. After cancel the stream's output topic is closed via Close,
// and the package goleak check confirms the pumps exited.
func TestFanInContextCancelStopsPumps(t *testing.T) {
	bus := newBus()
	defer bus.Close()

	ctx, cancelCtx := context.WithCancel(context.Background())
	stream := FanIn(ctx, bus)
	defer stream.Close()

	cancelCtx()
	// Give the pumps a moment to observe cancellation, then confirm a Close that
	// follows still completes without hanging on wg.Wait.
	done := make(chan struct{})
	go func() {
		stream.Close()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Close did not return after context cancellation")
	}
}

// TestFanInNilBus asserts FanIn tolerates a nil bus: it returns a usable stream
// with an open output topic and no pumps, and Close is safe.
func TestFanInNilBus(t *testing.T) {
	stream := FanIn(context.Background(), nil)
	require.NotNil(t, stream.Out())
	require.Equal(t, 0, stream.Out().SubscriberCount())
	stream.Close()
}
