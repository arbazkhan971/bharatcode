package llm

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// countingProvider is a test Provider that builds a FRESH event channel on every
// Stream call and records how many times Stream was invoked. Unlike the failover
// stubProvider it does not reuse a single channel, so a caching test can issue
// the same request more than once and still receive a drainable stream each time
// the inner provider is actually reached.
type countingProvider struct {
	name   string
	events []Event
	err    error
	calls  int
}

func (p *countingProvider) Name() string         { return p.name }
func (p *countingProvider) Models() []Model      { return nil }
func (p *countingProvider) SupportsTools() bool  { return false }
func (p *countingProvider) SupportsImages() bool { return false }

func (p *countingProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	ch := make(chan Event, len(p.events))
	for _, ev := range p.events {
		ch <- ev
	}
	close(ch)
	return ch, nil
}

// userMessage builds a minimal user message with volatile metadata set so tests
// can prove the cache key ignores ID/SessionID/CreatedAt: two messages with the
// same role and content but different metadata must still hit the same entry.
func userMessage(id, session, text string) message.Message {
	return message.Message{
		ID:        id,
		SessionID: session,
		Role:      message.RoleUser,
		Content:   []message.ContentBlock{message.TextBlock{Text: text}},
	}
}

func TestCachingProviderServesRepeatedRequestFromCache(t *testing.T) {
	want := []Event{
		StartEvent{Provider: "stub", Model: "m"},
		DeltaTextEvent{Text: "hello"},
		EndEvent{Usage: Usage{OutputTokens: 5}},
	}
	inner := &countingProvider{name: "stub", events: want}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	req := Request{
		Model:       "m",
		Temperature: 0,
		Messages:    []message.Message{userMessage("id-1", "sess-1", "ping")},
	}

	// First request misses: the inner provider is called and the stream is
	// forwarded live while being collected.
	first, err := cp.Stream(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, want, collectEvents(first))

	// Draining the first stream to completion guarantees the entry is stored
	// (Set runs before the output channel closes), so no sleep is needed before
	// the second request.
	second, err := cp.Stream(context.Background(), req)
	require.NoError(t, err)
	require.Equal(t, want, collectEvents(second))

	// The provider was called exactly once: the second identical request was
	// served from the cache.
	require.Equal(t, 1, inner.calls)
}

func TestCachingProviderIgnoresVolatileMessageMetadata(t *testing.T) {
	want := []Event{DeltaTextEvent{Text: "same"}, EndEvent{}}
	inner := &countingProvider{name: "stub", events: want}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	// Two requests whose messages differ only in volatile metadata (ID,
	// SessionID) must hash to the same key and so share one cache entry.
	first := Request{Model: "m", Messages: []message.Message{userMessage("id-1", "sess-1", "ping")}}
	second := Request{Model: "m", Messages: []message.Message{userMessage("id-2", "sess-2", "ping")}}

	require.Equal(t, want, collectEvents(mustStream(t, cp, first)))
	require.Equal(t, want, collectEvents(mustStream(t, cp, second)))

	require.Equal(t, 1, inner.calls)
}

func TestCachingProviderMissesOnDifferentRequest(t *testing.T) {
	inner := &countingProvider{name: "stub", events: []Event{DeltaTextEvent{Text: "x"}, EndEvent{}}}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	reqA := Request{Model: "m", Messages: []message.Message{userMessage("a", "s", "alpha")}}
	reqB := Request{Model: "m", Messages: []message.Message{userMessage("b", "s", "beta")}}

	require.NotNil(t, collectEvents(mustStream(t, cp, reqA)))
	require.NotNil(t, collectEvents(mustStream(t, cp, reqB)))

	// Different message content produces a different key, so the second request
	// misses and the provider is called again.
	require.Equal(t, 2, inner.calls)
}

func TestCachingProviderDoesNotCacheNonDeterministicRequests(t *testing.T) {
	inner := &countingProvider{name: "stub", events: []Event{DeltaTextEvent{Text: "x"}, EndEvent{}}}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	// Temperature != 0 is not deterministic-eligible: the cache is bypassed on
	// both lookup and store, so the same request twice calls the provider twice.
	req := Request{Model: "m", Temperature: 0.7, Messages: []message.Message{userMessage("id", "s", "ping")}}

	require.NotNil(t, collectEvents(mustStream(t, cp, req)))
	require.NotNil(t, collectEvents(mustStream(t, cp, req)))

	require.Equal(t, 2, inner.calls)
}

func TestCachingProviderDoesNotCacheStreamsWithErrorEvent(t *testing.T) {
	inner := &countingProvider{
		name:   "stub",
		events: []Event{StartEvent{Provider: "stub", Model: "m"}, ErrorEvent{Err: ErrServer}},
	}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	req := Request{Model: "m", Messages: []message.Message{userMessage("id", "s", "ping")}}

	// A stream that succeeded to open but carried an ErrorEvent is a failed
	// response and must not be stored.
	require.NotEmpty(t, collectEvents(mustStream(t, cp, req)))
	require.NotEmpty(t, collectEvents(mustStream(t, cp, req)))

	require.Equal(t, 2, inner.calls)
}

func TestCachingProviderDoesNotCacheStreamOpenError(t *testing.T) {
	inner := &countingProvider{name: "stub", err: ErrAuth}

	cp, err := NewCachingProvider(inner, NewLRUCache(8))
	require.NoError(t, err)

	req := Request{Model: "m", Messages: []message.Message{userMessage("id", "s", "ping")}}

	// Stream returning an error never starts a stream and is never cached; the
	// error is surfaced unchanged on every call.
	_, err1 := cp.Stream(context.Background(), req)
	require.ErrorIs(t, err1, ErrAuth)
	_, err2 := cp.Stream(context.Background(), req)
	require.ErrorIs(t, err2, ErrAuth)

	require.Equal(t, 2, inner.calls)
}

func TestCachingProviderDelegatesMetadataToInner(t *testing.T) {
	inner := &countingProvider{name: "inner"}
	cp, err := NewCachingProvider(inner, nil)
	require.NoError(t, err)

	// Metadata reflects the inner provider, and a nil cache falls back to the
	// default in-memory LRU.
	require.Equal(t, "inner", cp.Name())
	require.NotNil(t, cp.cache)
}

func TestNewCachingProviderRejectsNilInner(t *testing.T) {
	cp, err := NewCachingProvider(nil, NewLRUCache(8))
	require.Error(t, err)
	require.Nil(t, cp)
}

func TestLRUCacheEvictsLeastRecentlyUsed(t *testing.T) {
	c := NewLRUCache(2)
	c.Set("a", []Event{DeltaTextEvent{Text: "a"}})
	c.Set("b", []Event{DeltaTextEvent{Text: "b"}})

	// Touch "a" so "b" becomes the least recently used entry.
	_, ok := c.Get("a")
	require.True(t, ok)

	// Inserting a third entry evicts "b", the least recently used.
	c.Set("c", []Event{DeltaTextEvent{Text: "c"}})

	_, ok = c.Get("b")
	require.False(t, ok)
	_, ok = c.Get("a")
	require.True(t, ok)
	_, ok = c.Get("c")
	require.True(t, ok)
}

// mustStream issues req through cp and fails the test if Stream returns an
// error, returning the event channel for collection.
func mustStream(t *testing.T, cp *CachingProvider, req Request) <-chan Event {
	t.Helper()
	events, err := cp.Stream(context.Background(), req)
	require.NoError(t, err)
	return events
}
