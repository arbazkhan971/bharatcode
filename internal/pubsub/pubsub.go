// Package pubsub provides a generic, in-process, per-topic event bus.
// Topics are typed: each Topic[T] carries one payload type, validated
// at compile time. Publish is non-blocking and lossy; subscribers
// with full buffers drop events with a warn log.
package pubsub

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"

	"github.com/arbazkhan971/bharatcode/internal/util"
)

// DefaultBufferSize is the per-subscriber channel capacity used when
// NewTopic is called with bufferSize == 0. Sized to absorb a long
// streaming assistant turn (~one token-delta event per token) under
// a slow TUI render stall.
const DefaultBufferSize = 1024

// Topic is a typed, buffered, fan-out event channel. Each Topic owns
// its own subscriber set; a subscriber receives every event published
// after it subscribes and before it unsubscribes. The zero value is
// not usable; construct topics with NewTopic.
type Topic[T any] struct {
	name       string
	bufferSize int
	mu         sync.RWMutex
	subs       map[*chan T]struct{}
	closed     bool
	dropCount  atomic.Uint64
}

// NewTopic constructs a Topic with the given name (used in logs) and
// per-subscriber buffer size. A bufferSize of zero is treated as
// DefaultBufferSize (1024). Panics if name is empty.
func NewTopic[T any](name string, bufferSize int) *Topic[T] {
	if name == "" {
		panic("pubsub: topic name cannot be empty")
	}
	if bufferSize == 0 {
		bufferSize = DefaultBufferSize
	}
	return &Topic[T]{
		name:       name,
		bufferSize: bufferSize,
		subs:       make(map[*chan T]struct{}),
	}
}

// Publish delivers event to every active subscriber. Delivery is
// non-blocking: if a subscriber's channel is full, the event is
// dropped for that subscriber, a slog.Warn is logged with the topic
// name, and the topic's drop counter is incremented. Publish is safe
// to call from multiple goroutines and never blocks longer than the
// time to acquire the topic's read lock. If the topic has been closed
// Publish is a no-op. The provided ctx is used only to skip delivery
// when ctx.Done has fired before any subscriber is reached.
func (t *Topic[T]) Publish(ctx context.Context, event T) {
	if ctx.Err() != nil {
		return
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed {
		return
	}

	for chPtr := range t.subs {
		select {
		case *chPtr <- event:
		default:
			t.dropCount.Add(1)
			// Truncate the event formatting to prevent hot logs blowing up.
			eventStr := util.Truncate(fmt.Sprintf("%+v", event), 200)
			slog.WarnContext(
				ctx, "Event dropped because subscriber buffer is full",
				"topic", t.name,
				"event", eventStr,
			)
		}
	}
}

// PublishBlocking delivers event to every active subscriber with
// back-pressure rather than loss: for each subscriber whose buffer is
// full it waits for room instead of dropping, so an event published
// this way is never silently lost. Delivery is bounded by ctx — if
// ctx.Done fires while waiting on a full subscriber that subscriber is
// skipped (its DropCount is incremented and a warn is logged), so a
// stalled or vanished reader can never block the producer forever.
// Subscribers are served sequentially while the topic read lock is
// held, so PublishBlocking should carry only the low-frequency,
// must-not-drop events (terminal turn transitions, permission
// requests); the lossy Publish remains the hot path for token deltas.
// If the topic has been closed PublishBlocking is a no-op.
func (t *Topic[T]) PublishBlocking(ctx context.Context, event T) {
	if ctx.Err() != nil {
		return
	}

	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.closed {
		return
	}

	for chPtr := range t.subs {
		// Fast path: deliver without blocking when the buffer has room. Only
		// fall back to the ctx-bounded wait when the buffer is momentarily full,
		// so a keeping-up subscriber never pays for the select-with-Done arm.
		select {
		case *chPtr <- event:
			continue
		default:
		}
		select {
		case *chPtr <- event:
		case <-ctx.Done():
			t.dropCount.Add(1)
			eventStr := util.Truncate(fmt.Sprintf("%+v", event), 200)
			slog.WarnContext(
				ctx, "Must-deliver event dropped because context ended while subscriber buffer was full",
				"topic", t.name,
				"event", eventStr,
			)
		}
	}
}

// Subscribe registers a new subscriber and returns a receive-only
// channel that delivers every subsequent Publish, plus a cancel
// function that unregisters the subscriber and closes the channel.
// The cancel function is idempotent and safe to call from any
// goroutine. The returned channel is buffered to the topic's
// configured size. After Close, Subscribe returns an already-closed
// channel and a no-op cancel.
func (t *Topic[T]) Subscribe() (events <-chan T, cancel func()) {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		ch := make(chan T)
		close(ch)
		return ch, func() {}
	}

	ch := make(chan T, t.bufferSize)
	// We key the map on a pointer to the channel (*chan T) rather than the
	// channel value itself. This avoids GC retention pitfalls and makes it
	// clear we are tracking unique channel instances.
	chPtr := &ch
	t.subs[chPtr] = struct{}{}

	var once sync.Once
	cancel = func() {
		once.Do(func() {
			t.mu.Lock()
			defer t.mu.Unlock()
			if t.closed || t.subs == nil {
				return
			}
			if _, exists := t.subs[chPtr]; exists {
				delete(t.subs, chPtr)
				close(ch)
			}
		})
	}

	return ch, cancel
}

// Close marks the topic as closed, unregisters every subscriber, and
// closes every subscriber's channel. Subsequent Publish calls are
// no-ops and subsequent Subscribe calls return an already-closed
// channel. Close is idempotent.
func (t *Topic[T]) Close() {
	t.mu.Lock()
	defer t.mu.Unlock()

	if t.closed {
		return
	}

	t.closed = true
	for chPtr := range t.subs {
		close(*chPtr)
	}
	t.subs = nil
}

// Name returns the topic's name as passed to NewTopic.
func (t *Topic[T]) Name() string {
	return t.name
}

// SubscriberCount returns the number of currently registered
// subscribers. Intended for tests and diagnostics.
func (t *Topic[T]) SubscriberCount() int {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return len(t.subs)
}

// DropCount returns the cumulative number of events dropped because
// a subscriber's channel was full. The counter is monotonically
// non-decreasing for the lifetime of the topic.
func (t *Topic[T]) DropCount() uint64 {
	return t.dropCount.Load()
}
