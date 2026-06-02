package llm

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"

	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// blockingSSEServer starts an httptest server that opens an SSE stream, writes
// and flushes the caller-supplied opening event, then blocks until its request
// context is cancelled (which the client triggers by cancelling the request).
// Writing a real, flushed event lets the test synchronize on being genuinely
// mid-stream -- blocked inside scanner.Scan -- without any timing guesswork, and
// blocking on r.Context().Done lets the handler return cleanly once the client
// cancels so the server leaks no goroutine of its own.
func blockingSSEServer(t *testing.T, openingEvent string) *httptest.Server {
	t.Helper()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, ok := w.(http.Flusher)
		require.True(t, ok, "test server must support flushing")
		fmt.Fprint(w, openingEvent)
		flusher.Flush()
		// Hold the connection open mid-stream until the client cancels.
		<-r.Context().Done()
	}))
	return server
}

// drainUntil reads events until one of type T is observed, returning the full
// slice consumed so far. It is the synchronization point that proves the stream
// is mid-flight before the test cancels: the opening event has been parsed and
// delivered, so the reader goroutine is now blocked in scanner.Scan awaiting
// bytes that will never come.
func drainUntil[T Event](t *testing.T, events <-chan Event) {
	t.Helper()
	deadline := time.After(2 * time.Second)
	for {
		select {
		case ev, ok := <-events:
			require.True(t, ok, "stream closed before the awaited event arrived")
			if _, isT := ev.(T); isT {
				return
			}
		case <-deadline:
			t.Fatal("timed out waiting for the opening stream event")
		}
	}
}

// assertPromptCancellation drains the remaining events after a cancel and
// asserts the stream both terminates promptly and surfaces context.Canceled --
// either as a trailing ErrorEvent or, if the channel closes first, with no
// EndEvent masquerading as a clean finish. The 2s ceiling is an upper bound on
// promptness, never a fixed wait: a correct stream returns near-instantly.
func assertPromptCancellation(t *testing.T, events <-chan Event) {
	t.Helper()
	done := make(chan []Event, 1)
	go func() {
		var rest []Event
		for ev := range events {
			rest = append(rest, ev)
		}
		done <- rest
	}()

	select {
	case rest := <-done:
		// The cancellation must be observable: a terminal ErrorEvent wrapping
		// context.Canceled, and never a clean EndEvent that would let the agent
		// loop mistake a cancel for a finished turn.
		var sawCancel bool
		for _, ev := range rest {
			switch e := ev.(type) {
			case ErrorEvent:
				require.ErrorIs(t, e.Err, context.Canceled,
					"a stream error after cancel must wrap context.Canceled")
				sawCancel = true
			case EndEvent:
				t.Fatalf("a cancelled stream must not emit a clean EndEvent, got %#v", e)
			}
		}
		require.True(t, sawCancel, "cancelled stream must emit an ErrorEvent carrying context.Canceled")
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not return promptly after context cancellation")
	}
}

// TestOpenAIStreamCancelMidStreamReturnsContextCanceled proves cancelling the
// context while the OpenAI-compatible SSE reader is blocked mid-stream promptly
// stops the read, returns context.Canceled (not a partial or garbage event),
// and leaks no goroutine.
func TestOpenAIStreamCancelMidStreamReturnsContextCanceled(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	server := blockingSSEServer(t, "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n")
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Stream(ctx, streamRequest())
	require.NoError(t, err)

	// Wait until the first text delta lands: now the reader is blocked in
	// scanner.Scan, mid-stream, which is exactly the state cancellation must
	// interrupt.
	drainUntil[DeltaTextEvent](t, events)
	cancel()

	assertPromptCancellation(t, events)

	// Close the server (releasing its blocked handler) before goleak inspects
	// goroutines, so the server's own connection goroutines are not mistaken for
	// a leak from the package under test. VerifyNone, deferred first, runs last.
	server.Close()
}

// TestOpenAIStreamCancelDiscardsPartialEvent proves the "not a partial/garbage
// event" half of the cancellation contract: when the context is cancelled while
// an unterminated SSE event sits half-read in the scanner buffer, that partial
// data is discarded rather than flushed as a bogus DeltaTextEvent. The server
// writes one complete event (the synchronization point) followed by a partial,
// newline-less data: line, then blocks. Because readSSE checks ctx.Err before
// the trailing flush, the half-event "GARBAGE" never reaches the consumer.
func TestOpenAIStreamCancelDiscardsPartialEvent(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	// A complete, terminated event, then a partial line with no trailing newline
	// so it stays buffered inside scanner.Scan when the cancel lands.
	opening := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\"GARBAGE"
	server := blockingSSEServer(t, opening)
	t.Setenv("TEST_API_KEY", "test-key")

	cfg := testConfig("deepseek", config.ProviderOpenAICompatible, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "TEST_API_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("deepseek")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Stream(ctx, streamRequest())
	require.NoError(t, err)

	// Sync on the first, fully terminated event; the partial line is now buffered
	// and the reader is blocked awaiting its terminator.
	drainUntil[DeltaTextEvent](t, events)
	cancel()

	// Drain the rest and assert the partial event was dropped, not emitted.
	done := make(chan []Event, 1)
	go func() {
		var rest []Event
		for ev := range events {
			rest = append(rest, ev)
		}
		done <- rest
	}()

	select {
	case rest := <-done:
		var sawCancel bool
		for _, ev := range rest {
			switch e := ev.(type) {
			case ErrorEvent:
				require.ErrorIs(t, e.Err, context.Canceled)
				sawCancel = true
			case DeltaTextEvent:
				require.NotContains(t, e.Text, "GARBAGE",
					"a partial unterminated event must not be flushed as text on cancel")
			case EndEvent:
				t.Fatalf("a cancelled stream must not emit a clean EndEvent, got %#v", e)
			}
		}
		require.True(t, sawCancel, "cancelled stream must emit an ErrorEvent carrying context.Canceled")
	case <-time.After(2 * time.Second):
		t.Fatal("stream did not return promptly after context cancellation")
	}

	server.Close()
}

// TestAnthropicStreamCancelMidStreamReturnsContextCanceled is the Anthropic
// readResponse counterpart: the same prompt-stop, context.Canceled, no-leak
// guarantees on the Messages SSE path, which has its own stream loop.
func TestAnthropicStreamCancelMidStreamReturnsContextCanceled(t *testing.T) {
	ignore := goleak.IgnoreCurrent()
	defer goleak.VerifyNone(t, ignore)

	opening := "event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"hi\"}}\n\n"
	server := blockingSSEServer(t, opening)
	t.Setenv("ANTHROPIC_TEST_KEY", "test-key")

	cfg := testConfig("anthropic", config.ProviderAnthropic, server.URL+"/v1")
	cfg.Providers[0].APIKeyEnv = "ANTHROPIC_TEST_KEY"
	reg, err := NewRegistry(cfg)
	require.NoError(t, err)
	provider, err := reg.Get("anthropic")
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())
	events, err := provider.Stream(ctx, Request{
		Model: "test-model",
		Messages: []message.Message{{
			Role:    message.RoleUser,
			Content: []message.ContentBlock{message.TextBlock{Text: "hi"}},
		}},
	})
	require.NoError(t, err)

	drainUntil[DeltaTextEvent](t, events)
	cancel()

	assertPromptCancellation(t, events)

	server.Close()
}

// TestReadSSEReturnsContextErrNotScanError pins the readSSE contract directly
// and proves the unconditional ctx.Err() check is load-bearing. It models the
// hostile real-transport case the SSE event-channel tests cannot: the context
// is cancelled, but the underlying reader unblocks with a generic, NON-context
// error (a connection reset, say) rather than a wrapped context.Canceled.
// Without the ctx.Err() guard this error would fall through to the
// "scanning provider stream" branch and the cancellation signal would be lost;
// with it, readSSE reports context.Canceled. An io.Pipe makes the read block
// deterministically with no network and no sleeps.
func TestReadSSEReturnsContextErrNotScanError(t *testing.T) {
	pr, pw := io.Pipe()
	t.Cleanup(func() { _ = pw.Close() })

	ctx, cancel := context.WithCancel(context.Background())

	// Feed one complete event so the handler runs once, then leave the reader
	// blocked awaiting the next line.
	go func() {
		_, _ = pw.Write([]byte("data: {\"k\":1}\n\n"))
	}()

	handled := make(chan struct{}, 1)
	errCh := make(chan error, 1)
	go func() {
		errCh <- readSSE(ctx, pr, func(sseEvent) error {
			select {
			case handled <- struct{}{}:
			default:
			}
			return nil
		})
	}()

	// Once the first event is handled the reader is blocked in Scan; cancel it.
	select {
	case <-handled:
	case <-time.After(2 * time.Second):
		t.Fatal("readSSE never handled the opening event")
	}
	cancel()
	// Unblock the underlying Read with a generic error that is NOT
	// context.Canceled, mimicking a transport that surfaces a connection-reset
	// instead of the context error. Only the unconditional ctx.Err() check can
	// recover the cancellation from here.
	_ = pw.CloseWithError(errors.New("connection reset by peer"))

	select {
	case err := <-errCh:
		require.ErrorIs(t, err, context.Canceled,
			"readSSE must return the context error even when the scanner surfaces a non-context error on cancel")
		require.NotContains(t, err.Error(), "connection reset",
			"the raw transport error must not leak past the context cancellation")
	case <-time.After(2 * time.Second):
		t.Fatal("readSSE did not return promptly after cancellation")
	}
}
