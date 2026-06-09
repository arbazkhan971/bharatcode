// Package pubsub_test implements unit tests for the pubsub module.
package pubsub_test

import (
	"context"
	"testing"
	"time"

	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestMain sets up goleak verification to detect goroutine leaks.
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m)
}

// TestPublishFanOut registers N=10 subscribers, publishes 100 events, and
// asserts every subscriber receives exactly 100 events in order.
func TestPublishFanOut(t *testing.T) {
	topic := pubsub.NewTopic[int]("fanout", 128)
	defer topic.Close()

	const numSubs = 10
	const numEvents = 100

	channels := make([]<-chan int, numSubs)
	cancels := make([]func(), numSubs)

	for i := 0; i < numSubs; i++ {
		channels[i], cancels[i] = topic.Subscribe()
	}

	ctx := context.Background()
	for i := 0; i < numEvents; i++ {
		topic.Publish(ctx, i)
	}

	for i := 0; i < numSubs; i++ {
		for j := 0; j < numEvents; j++ {
			select {
			case val := <-channels[i]:
				require.Equal(t, j, val)
			case <-time.After(1 * time.Second):
				t.Fatalf("Timeout waiting for event %d on subscriber %d", j, i)
			}
		}
		cancels[i]()
	}
}

// TestSubscribeAfterPublish registers a subscriber after one Publish has
// fired, publishes once more, and asserts the late subscriber sees only the
// second event (no replay).
func TestSubscribeAfterPublish(t *testing.T) {
	topic := pubsub.NewTopic[int]("late", 10)
	defer topic.Close()

	ctx := context.Background()
	topic.Publish(ctx, 1)

	ch, cancel := topic.Subscribe()
	defer cancel()

	topic.Publish(ctx, 2)

	select {
	case val := <-ch:
		require.Equal(t, 2, val)
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for event")
	}

	select {
	case val := <-ch:
		t.Fatalf("Unexpected event: %v", val)
	default:
	}
}

// TestCancelStopsDelivery cancels a subscriber, publishes 10 more events, and
// asserts no further reads on the cancelled channel block or panic; the
// channel is observed closed.
func TestCancelStopsDelivery(t *testing.T) {
	topic := pubsub.NewTopic[int]("cancel", 10)
	defer topic.Close()

	ch, cancel := topic.Subscribe()
	cancel()

	select {
	case _, ok := <-ch:
		require.False(t, ok, "Channel should be closed after cancel")
	case <-time.After(1 * time.Second):
		t.Fatal("Timeout waiting for channel close")
	}

	ctx := context.Background()
	for i := 0; i < 10; i++ {
		topic.Publish(ctx, i)
	}
}

// TestSlowSubscriberDrops registers one subscriber with bufferSize=4,
// publishes 100 events without reading, asserts DropCount() == 96, and asserts
// no producer goroutine is blocked.
func TestSlowSubscriberDrops(t *testing.T) {
	topic := pubsub.NewTopic[int]("slow", 4)
	defer topic.Close()

	_, cancel := topic.Subscribe()
	defer cancel()

	// Deadlock guard: if Publish blocks, this timer will fire and trigger a
	// panic.
	timer := time.AfterFunc(2*time.Second, func() {
		panic("TestSlowSubscriberDrops blocked - deadlock detected!")
	})
	defer timer.Stop()

	ctx := context.Background()
	for i := 0; i < 100; i++ {
		topic.Publish(ctx, i)
	}

	require.Equal(t, uint64(96), topic.DropCount())
}

// TestPublishBlockingNoDropUnderBackpressure registers one subscriber with a
// small buffer, has it read slowly, and asserts PublishBlocking delivers every
// event in order with zero drops — the must-deliver guarantee Publish lacks.
func TestPublishBlockingNoDropUnderBackpressure(t *testing.T) {
	topic := pubsub.NewTopic[int]("must_deliver", 2)
	defer topic.Close()

	ch, cancel := topic.Subscribe()
	defer cancel()

	const numEvents = 100
	done := make(chan struct{})
	go func() {
		defer close(done)
		ctx := context.Background()
		for i := 0; i < numEvents; i++ {
			topic.PublishBlocking(ctx, i)
		}
	}()

	// Read slowly: the buffer (2) is far smaller than the event count, so the
	// producer must block in PublishBlocking and would drop with plain Publish.
	for i := 0; i < numEvents; i++ {
		select {
		case val := <-ch:
			require.Equal(t, i, val)
		case <-time.After(2 * time.Second):
			t.Fatalf("Timeout waiting for must-deliver event %d", i)
		}
	}

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishBlocking producer did not finish")
	}
	require.Equal(t, uint64(0), topic.DropCount(), "PublishBlocking must not drop")
}

// TestPublishBlockingContextBoundsWait asserts PublishBlocking never blocks the
// producer forever on a stalled subscriber: while it is parked on a full buffer,
// cancelling ctx unblocks it and the stalled event is counted as dropped. ctx is
// live when the call starts (so the early ctx.Err short-circuit does not apply)
// and is cancelled concurrently while the producer is parked.
func TestPublishBlockingContextBoundsWait(t *testing.T) {
	topic := pubsub.NewTopic[int]("must_deliver_bounded", 1)
	defer topic.Close()

	// A subscriber that never reads, so its buffer fills immediately.
	_, cancel := topic.Subscribe()
	defer cancel()

	ctx, cancelCtx := context.WithCancel(context.Background())
	defer cancelCtx()

	topic.PublishBlocking(context.Background(), 1) // fills the 1-slot buffer

	// Deadlock guard: a blocked PublishBlocking would never reach the assertion.
	timer := time.AfterFunc(2*time.Second, func() {
		panic("TestPublishBlockingContextBoundsWait blocked - back-pressure not ctx-bounded!")
	})
	defer timer.Stop()

	// The buffer is full and stays full, so this call parks in the ctx-bounded
	// wait. Schedule the cancel slightly in the future so ctx is unambiguously
	// live when PublishBlocking is entered (defeating the early ctx.Err
	// short-circuit) and only fires once the producer is parked.
	done := make(chan struct{})
	go func() {
		defer close(done)
		topic.PublishBlocking(ctx, 2)
	}()
	time.AfterFunc(10*time.Millisecond, cancelCtx)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("PublishBlocking did not return after context cancellation")
	}
	require.Equal(t, uint64(1), topic.DropCount(), "the un-deliverable event must be counted as dropped")
}

// TestPublishBlockingAfterClose asserts PublishBlocking is a no-op on a closed
// topic, mirroring Publish.
func TestPublishBlockingAfterClose(t *testing.T) {
	topic := pubsub.NewTopic[int]("blocking_after_close", 10)
	_, cancel := topic.Subscribe()
	defer cancel()

	topic.Close()
	initialDropCount := topic.DropCount()
	topic.PublishBlocking(context.Background(), 42)
	require.Equal(t, initialDropCount, topic.DropCount())
}

// TestCloseIdempotent calls Close() twice and asserts the second call is a
// no-op.
func TestCloseIdempotent(t *testing.T) {
	topic := pubsub.NewTopic[int]("idempotent", 10)
	topic.Close()
	topic.Close()
}

// TestPublishAfterClose calls Close(), then Publish(...), and asserts no
// panic, no goroutine leak, and DropCount() unchanged.
func TestPublishAfterClose(t *testing.T) {
	topic := pubsub.NewTopic[int]("after_close", 10)
	_, cancel := topic.Subscribe()
	defer cancel()

	topic.Close()

	initialDropCount := topic.DropCount()

	ctx := context.Background()
	topic.Publish(ctx, 42)

	require.Equal(t, initialDropCount, topic.DropCount())
}

// TestPermissionRequestReply exercises the PermissionRequests topic:
// subscriber receives a PermissionRequest, sends a PermissionDecision on
// Reply, producer's <-req.Reply unblocks with the decision.
func TestPermissionRequestReply(t *testing.T) {
	topic := pubsub.PermissionRequests

	ch, cancel := topic.Subscribe()
	defer cancel()

	replyChan := make(chan pubsub.PermissionDecision, 1)
	req := pubsub.PermissionRequest{
		Tool:   "bash",
		Args:   map[string]any{"cmd": "ls"},
		Reason: "list files",
		Reply:  replyChan,
	}

	ctx := context.Background()
	go func() {
		topic.Publish(ctx, req)
	}()

	select {
	case receivedReq := <-ch:
		require.Equal(t, "bash", receivedReq.Tool)
		require.Equal(t, "list files", receivedReq.Reason)
		receivedReq.Reply <- pubsub.PermissionDecision{
			Approved: true,
			Remember: true,
			Reason:   "user approved",
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for PermissionRequest")
	}

	select {
	case decision := <-replyChan:
		require.True(t, decision.Approved)
		require.True(t, decision.Remember)
		require.Equal(t, "user approved", decision.Reason)
	case <-time.After(2 * time.Second):
		t.Fatal("Timeout waiting for PermissionDecision")
	}
}

// TestNewTopicPanic asserts that NewTopic panics if the name is empty.
func TestNewTopicPanic(t *testing.T) {
	require.Panics(t, func() {
		pubsub.NewTopic[int]("", 0)
	})
}

// TestSubscribeAfterClose asserts that Subscribe after Close returns a closed
// channel and a no-op cancel function.
func TestSubscribeAfterClose(t *testing.T) {
	topic := pubsub.NewTopic[int]("sub_after_close", 10)
	topic.Close()

	ch, cancel := topic.Subscribe()
	cancel()

	select {
	case _, ok := <-ch:
		require.False(t, ok, "Channel should be closed")
	default:
		t.Fatal("Channel should be closed")
	}
}

// TestTopicName asserts that Name returns the correct topic name.
func TestTopicName(t *testing.T) {
	topic := pubsub.NewTopic[int]("my-topic-name", 10)
	defer topic.Close()
	require.Equal(t, "my-topic-name", topic.Name())
}

// TestSubscriberCount asserts that SubscriberCount returns the correct number
// of active subscribers.
func TestSubscriberCount(t *testing.T) {
	topic := pubsub.NewTopic[int]("count", 10)
	defer topic.Close()

	require.Equal(t, 0, topic.SubscriberCount())

	_, cancel1 := topic.Subscribe()
	require.Equal(t, 1, topic.SubscriberCount())

	_, cancel2 := topic.Subscribe()
	require.Equal(t, 2, topic.SubscriberCount())

	cancel1()
	require.Equal(t, 1, topic.SubscriberCount())

	cancel2()
	require.Equal(t, 0, topic.SubscriberCount())
}

// TestPublishContextCancelled asserts that if context is cancelled before
// publishing, the message delivery is skipped.
func TestPublishContextCancelled(t *testing.T) {
	topic := pubsub.NewTopic[int]("ctx_cancel", 10)
	defer topic.Close()

	ch, cancel := topic.Subscribe()
	defer cancel()

	ctx, cancelCtx := context.WithCancel(context.Background())
	cancelCtx()

	topic.Publish(ctx, 42)

	select {
	case val := <-ch:
		t.Fatalf("Unexpected event delivered: %v", val)
	default:
	}
}

// BenchmarkPublish_1Subscriber reports performance and allocations for
// publishing with 1 subscriber.
func BenchmarkPublish_1Subscriber(b *testing.B) {
	topic := pubsub.NewTopic[int]("bench", 100000)
	defer topic.Close()

	ch, cancel := topic.Subscribe()
	defer cancel()

	ctx := context.Background()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		topic.Publish(ctx, i)
		<-ch
	}
}
