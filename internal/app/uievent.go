// Package app wires BharatCode services into one dependency graph.
package app

import (
	"context"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
)

// uiEventBufferSize is the per-subscriber buffer for the consolidated UI event
// topic. It must comfortably absorb a streaming turn's token-delta agent events
// (the dominant volume) interleaved with the occasional permission request, so
// it inherits the same sizing as the agent topic — pubsub.Publish is lossy and
// drops events for any subscriber whose buffer is full.
const uiEventBufferSize = agentEventBufferSize

// UIEventKind enumerates the categories of UIEvent that the consolidated stream
// carries. The UI switches on the kind to decide which payload field is set.
type UIEventKind int

const (
	// UIEventAgent carries an agent-loop transition in the Agent field. This is
	// the high-volume case (every token delta, tool call, and turn boundary).
	UIEventAgent UIEventKind = iota
	// UIEventPermission carries a tool-permission request in the Permission
	// field. The request embeds a Reply channel the UI answers on, so the
	// consolidated stream preserves the request/response handshake.
	UIEventPermission
	// UIEventNotice carries a UI-relevant notification in the Notice field that
	// originates outside the agent loop (for example a background subsystem
	// status change). It exists so future producers can reach the UI through the
	// single stream rather than adding another ad-hoc channel for the TUI to
	// subscribe to separately.
	UIEventNotice
)

// Notice is a free-form, UI-relevant notification that does not belong to the
// agent or permission flows. It is the extension point that lets the
// consolidated stream absorb new producers without growing the UIEvent surface
// or the number of sources the UI subscribes to.
type Notice struct {
	// Title is a short, human-readable label for the notification.
	Title string
	// Body is the optional detail shown beneath the title.
	Body string
}

// UIEvent is the single consolidated event type the UI subscribes to. It is a
// tagged struct (mirroring agent.Event's Kind-plus-optional-fields shape) rather
// than an interface sum-type so it stays a plain value flowing through the
// generic pubsub.Topic with no per-event boxing on the hot token-delta path. The
// Kind selects which payload field is populated; the others are zero.
type UIEvent struct {
	// Kind selects which payload field below is meaningful for this event.
	Kind UIEventKind
	// Agent is set when Kind == UIEventAgent.
	Agent agent.Event
	// Permission is set when Kind == UIEventPermission. It carries the request's
	// Reply channel, so the consumer answers on Permission.Reply exactly as it
	// would on the standalone permission topic.
	Permission pubsub.PermissionRequest
	// Notice is set when Kind == UIEventNotice.
	Notice Notice
}

// AgentUIEvent wraps an agent.Event as a UIEvent.
func AgentUIEvent(ev agent.Event) UIEvent {
	return UIEvent{Kind: UIEventAgent, Agent: ev}
}

// PermissionUIEvent wraps a permission request as a UIEvent.
func PermissionUIEvent(req pubsub.PermissionRequest) UIEvent {
	return UIEvent{Kind: UIEventPermission, Permission: req}
}

// NoticeUIEvent wraps a Notice as a UIEvent.
func NoticeUIEvent(n Notice) UIEvent {
	return UIEvent{Kind: UIEventNotice, Notice: n}
}

// mustDeliver reports whether ev carries semantics the UI must not miss, so the
// fan-in routes it through the back-pressuring PublishBlocking path instead of
// the lossy Publish. The distinction keeps the hot token-delta stream lossy
// (the per-subscriber buffer absorbs bursts and a dropped delta only costs a
// frame) while guaranteeing the low-frequency events that leave the UI wedged
// when lost — turn boundaries, errors, and the permission handshake — always
// land.
func (e UIEvent) mustDeliver() bool {
	switch e.Kind {
	case UIEventPermission:
		// The request embeds a Reply channel the agent blocks on; dropping it
		// would deadlock the turn, so a permission request is always delivered.
		return true
	case UIEventNotice:
		// Notices are rare, out-of-band, and exist precisely because something
		// wanted the user to see them — deliver rather than drop.
		return true
	case UIEventAgent:
		return agentEventMustDeliver(e.Agent.Kind)
	default:
		return false
	}
}

// agentEventMustDeliver classifies an agent event kind as must-deliver (true)
// or lossy (false). The lossy kinds are the high-frequency streaming ones whose
// loss under render load is tolerable; every kind that moves the UI's run state
// or reports a failure is must-deliver so the view never stalls showing a turn
// as still running.
func agentEventMustDeliver(kind agent.EventKind) bool {
	switch kind {
	case agent.EventLLMResponse, agent.EventToolResult, agent.EventLLMDelta:
		// Streaming assistant output and tool-result chunks are the dominant
		// volume and are safe to drop occasionally — the buffer is sized for
		// the burst and the next event supersedes a missed one. Text deltas
		// are the highest-frequency kind of all and reconcile against the
		// canonical EventLLMResponse, so dropping one never corrupts the view.
		return false
	default:
		// Turn start/finish, the tool-call announcement, loop detection,
		// run errors, and auto-compaction notices all change what the UI shows
		// about the run; losing one wedges the view, so deliver them.
		return true
	}
}

// UIStream is the consolidated fan-in: it owns one output pubsub.Topic[UIEvent]
// and a set of goroutines that read each upstream source topic and republish
// every event onto the output, wrapped in a UIEvent. The UI subscribes to Out()
// once instead of subscribing to the agent and permission topics separately.
//
// The fan-in is additive: republishing onto a new topic leaves the source topics
// untouched, so existing direct subscribers keep working unchanged while callers
// migrate to the consolidated stream.
type UIStream struct {
	out *pubsub.Topic[UIEvent]

	cancels []func()
	wg      sync.WaitGroup

	closeOnce sync.Once
}

// FanIn constructs a UIStream that consolidates the agent and permission topics
// of bus into one UIEvent stream. It subscribes to each source immediately (so
// no event published after FanIn returns is missed) and starts one pump
// goroutine per source. ctx bounds the pump goroutines: cancelling ctx, or
// calling Close, drains and stops them. Passing a nil bus, or a bus with nil
// source topics, simply skips the corresponding pump.
func FanIn(ctx context.Context, bus *Bus) *UIStream {
	if ctx == nil {
		ctx = context.Background()
	}
	s := &UIStream{
		out: pubsub.NewTopic[UIEvent]("app_ui_events", uiEventBufferSize),
	}
	if bus == nil {
		return s
	}

	// One pump per source. pumpInto is a free generic function because Go methods
	// cannot carry their own type parameters; the stream's output topic and wait
	// group are passed in so every pump publishes to the same consolidated topic.
	if bus.Agent != nil {
		ch, cancel := bus.Agent.Subscribe()
		s.cancels = append(s.cancels, cancel)
		pumpInto(ctx, s.out, &s.wg, AgentUIEvent, ch)
	}
	if bus.Permission != nil {
		ch, cancel := bus.Permission.Subscribe()
		s.cancels = append(s.cancels, cancel)
		pumpInto(ctx, s.out, &s.wg, PermissionUIEvent, ch)
	}
	return s
}

// pump starts a goroutine that reads every value from src, wraps it with wrap,
// and republishes it onto the output topic until ctx is cancelled or src closes.
// Each wrapped event chooses its delivery guarantee: must-deliver events take
// the back-pressuring PublishBlocking path (bounded by ctx) so the UI never
// misses a turn boundary or permission request, while the high-frequency
// streaming events stay on the lossy Publish path.
func pumpInto[T any](ctx context.Context, out *pubsub.Topic[UIEvent], wg *sync.WaitGroup, wrap func(T) UIEvent, src <-chan T) {
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			select {
			case <-ctx.Done():
				return
			case v, ok := <-src:
				if !ok {
					return
				}
				ev := wrap(v)
				if ev.mustDeliver() {
					out.PublishBlocking(ctx, ev)
				} else {
					out.Publish(ctx, ev)
				}
			}
		}
	}()
}

// Out returns the consolidated topic the UI subscribes to. The returned topic is
// owned by the UIStream; callers Subscribe to it but must not Close it directly
// — Close on the UIStream tears the whole fan-in down.
func (s *UIStream) Out() *pubsub.Topic[UIEvent] {
	return s.out
}

// Subscribe is a convenience wrapper over Out().Subscribe(), returning a
// receive-only channel of consolidated UI events and a cancel function.
func (s *UIStream) Subscribe() (<-chan UIEvent, func()) {
	return s.out.Subscribe()
}

// Close stops every pump goroutine, unsubscribes from the source topics, and
// closes the consolidated output topic. It is idempotent and safe to call from
// any goroutine. Close does not close the source topics — those are owned by the
// Bus and closed on App shutdown.
func (s *UIStream) Close() {
	s.closeOnce.Do(func() {
		// Unsubscribe from every source first so no further values are delivered
		// to a pump that is about to exit, then wait for the pumps to drain and
		// return before closing the output topic they publish to.
		for _, cancel := range s.cancels {
			cancel()
		}
		s.wg.Wait()
		s.out.Close()
	})
}
