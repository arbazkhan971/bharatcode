package llm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"testing"

	"github.com/stretchr/testify/require"
)

// stubProvider is a test Provider that returns a fixed stream/error from Stream
// and records how many times Stream was called, so failover tests can assert
// both the surfaced result and which providers were actually tried.
type stubProvider struct {
	name   string
	events <-chan Event
	err    error
	calls  int
}

func (s *stubProvider) Name() string         { return s.name }
func (s *stubProvider) Models() []Model      { return nil }
func (s *stubProvider) SupportsTools() bool  { return false }
func (s *stubProvider) SupportsImages() bool { return false }

func (s *stubProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	s.calls++
	if s.err != nil {
		return nil, s.err
	}
	return s.events, nil
}

// streamOf returns a closed channel that yields the given events, modeling a
// provider whose Stream succeeded and produced a finished stream.
func streamOf(events ...Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch
}

func TestFailoverUsesFallbackOnRetryableError(t *testing.T) {
	primary := &stubProvider{name: "primary", err: fmt.Errorf("primary down: %w", ErrServer)}
	want := DeltaTextEvent{Text: "from-fallback"}
	fallback := &stubProvider{
		name:   "fallback",
		events: streamOf(StartEvent{Provider: "fallback", Model: "m"}, want, EndEvent{}),
	}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.NoError(t, err)

	got := collectEvents(events)
	// The stream that was returned is the fallback's: it carries the
	// fallback's distinctive event, proving failover surfaced the second
	// provider's stream rather than the primary's.
	require.Contains(t, got, want)
	require.Contains(t, got, StartEvent{Provider: "fallback", Model: "m"})

	// Both providers were tried exactly once: the primary failed retryably and
	// the fallback succeeded.
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 1, fallback.calls)
}

func TestFailoverDoesNotFailOverOnAuthError(t *testing.T) {
	primary := &stubProvider{name: "primary", err: fmt.Errorf("bad key: %w", ErrAuth)}
	fallback := &stubProvider{name: "fallback", events: streamOf(DeltaTextEvent{Text: "unreached"})}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	// A user/auth 4xx is terminal: the error surfaces immediately and no
	// stream is returned.
	require.Nil(t, events)
	require.ErrorIs(t, err, ErrAuth)

	// The primary was tried once; the fallback was never reached.
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 0, fallback.calls)
}

func TestFailoverFailsOverOnRateLimit(t *testing.T) {
	primary := &stubProvider{name: "primary", err: fmt.Errorf("slow down: %w", ErrRateLimit)}
	fallback := &stubProvider{name: "fallback", events: streamOf(DeltaTextEvent{Text: "ok"})}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.NoError(t, err)
	require.Contains(t, collectEvents(events), DeltaTextEvent{Text: "ok"})
	require.Equal(t, 1, fallback.calls)
}

func TestFailoverFailsOverOnConnectionError(t *testing.T) {
	// A transport-level dial failure arrives as a *net.OpError wrapped in the
	// "sending provider request" message; failover must treat it as retryable.
	connErr := fmt.Errorf("sending provider request: %w", &net.OpError{Op: "dial", Err: errors.New("connection refused")})
	primary := &stubProvider{name: "primary", err: connErr}
	fallback := &stubProvider{name: "fallback", events: streamOf(DeltaTextEvent{Text: "ok"})}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.NoError(t, err)
	require.Contains(t, collectEvents(events), DeltaTextEvent{Text: "ok"})
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 1, fallback.calls)
}

func TestFailoverReturnsLastErrorWhenAllFail(t *testing.T) {
	primary := &stubProvider{name: "primary", err: fmt.Errorf("p: %w", ErrServer)}
	last := fmt.Errorf("f: %w", ErrRateLimit)
	fallback := &stubProvider{name: "fallback", err: last}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.Nil(t, events)
	// Every provider failed retryably, so the last error is surfaced.
	require.ErrorIs(t, err, ErrRateLimit)
	require.Equal(t, 1, primary.calls)
	require.Equal(t, 1, fallback.calls)
}

func TestFailoverDoesNotFailOverOnContextLimit(t *testing.T) {
	primary := &stubProvider{name: "primary", err: fmt.Errorf("too big: %w", ErrContextLimit)}
	fallback := &stubProvider{name: "fallback", events: streamOf(DeltaTextEvent{Text: "unreached"})}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.Nil(t, events)
	require.ErrorIs(t, err, ErrContextLimit)
	require.Equal(t, 0, fallback.calls)
}

func TestFailoverDoesNotFailOverOnContextCanceled(t *testing.T) {
	// A caller-initiated cancellation often surfaces as a net.Error, but the
	// caller, not the provider, ended the request, so it must not fail over.
	primary := &stubProvider{name: "primary", err: fmt.Errorf("aborted: %w", context.Canceled)}
	fallback := &stubProvider{name: "fallback", events: streamOf(DeltaTextEvent{Text: "unreached"})}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	events, err := fp.Stream(context.Background(), Request{Model: "m"})
	require.Nil(t, events)
	require.ErrorIs(t, err, context.Canceled)
	require.Equal(t, 0, fallback.calls)
}

func TestNewFailoverProviderRejectsNilPrimary(t *testing.T) {
	fp, err := NewFailoverProvider(nil)
	require.Error(t, err)
	require.Nil(t, fp)
}

func TestFailoverDelegatesMetadataToPrimary(t *testing.T) {
	primary := &stubProvider{name: "primary"}
	fallback := &stubProvider{name: "fallback"}

	fp, err := NewFailoverProvider(primary, fallback)
	require.NoError(t, err)

	// Metadata reflects the primary, not the fallback.
	require.Equal(t, "primary", fp.Name())
}
