package llm

import (
	"container/list"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// ResponseCache stores and retrieves completed response event streams keyed by a
// stable hash of a request. Implementations must be safe for concurrent use,
// since a single Provider is shared across concurrent callers. The stored value
// is the full ordered slice of events from one successful, completed stream so a
// later identical request can replay it without re-calling the provider.
type ResponseCache interface {
	// Get returns the cached events for key and whether the key was present.
	Get(key string) ([]Event, bool)
	// Set stores events under key, replacing any prior value.
	Set(key string, events []Event)
}

// CachingProvider wraps an inner Provider and serves repeated, deterministic
// requests from a ResponseCache instead of re-calling the inner provider. It is
// a thin composable Provider: metadata is delegated unchanged to the inner
// provider, and only Stream consults the cache.
//
// Caching is applied only to deterministic-eligible requests (temperature 0).
// Any other request bypasses the cache entirely and is forwarded straight to the
// inner provider. A request that fails before streaming, or whose stream carries
// an ErrorEvent, is never stored, so errors are never replayed as if they were
// successful responses.
type CachingProvider struct {
	inner Provider
	cache ResponseCache
}

// CachingProvider is a Provider so it composes transparently with any caller
// that holds a plain Provider.
var _ Provider = (*CachingProvider)(nil)

// NewCachingProvider wraps inner so identical deterministic requests are served
// from cache. When cache is nil an in-memory LRU with the default capacity is
// used. It returns an error if inner is nil so the wrapper always has a usable
// backend.
func NewCachingProvider(inner Provider, cache ResponseCache) (*CachingProvider, error) {
	if inner == nil {
		return nil, fmt.Errorf("constructing caching provider: inner provider is nil")
	}
	if cache == nil {
		cache = NewLRUCache(defaultCacheCapacity)
	}
	return &CachingProvider{inner: inner, cache: cache}, nil
}

// Name returns the inner provider's name.
func (p *CachingProvider) Name() string { return p.inner.Name() }

// Models returns the inner provider's models.
func (p *CachingProvider) Models() []Model { return p.inner.Models() }

// SupportsTools reports whether the inner provider supports tools.
func (p *CachingProvider) SupportsTools() bool { return p.inner.SupportsTools() }

// SupportsImages reports whether the inner provider supports images.
func (p *CachingProvider) SupportsImages() bool { return p.inner.SupportsImages() }

// Stream serves req from the cache when eligible and previously stored,
// otherwise forwards to the inner provider and stores the completed stream.
//
// Ineligible requests (temperature != 0) bypass the cache on both lookup and
// store and are forwarded unchanged. On a hit a fresh channel is built from the
// stored events for each call, because an Event channel is single-use. On a miss
// events are forwarded live to the caller while being collected; the collected
// slice is stored only after the stream completes successfully, and is never
// stored if the stream carries an ErrorEvent.
func (p *CachingProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	if !cacheable(req) {
		return p.inner.Stream(ctx, req)
	}

	key, err := cacheKey(req)
	if err != nil {
		// A key we cannot compute means we cannot cache safely; degrade to a
		// plain passthrough rather than failing the request.
		return p.inner.Stream(ctx, req)
	}

	if events, ok := p.cache.Get(key); ok {
		return replay(events), nil
	}

	upstream, err := p.inner.Stream(ctx, req)
	if err != nil {
		// A request that never started streaming is not cached: errors must
		// never be replayed as successful responses.
		return nil, err
	}

	out := make(chan Event)
	go func() {
		defer close(out)

		collected := make([]Event, 0)
		failed := false
		for event := range upstream {
			if _, isErr := event.(ErrorEvent); isErr {
				failed = true
			}
			collected = append(collected, event)
			select {
			case out <- event:
			case <-ctx.Done():
				// The caller went away; abandon collection without caching a
				// truncated stream.
				return
			}
		}

		// Store before the output channel closes so that a caller which has
		// finished draining the stream is guaranteed to observe the entry on a
		// subsequent identical request. A stream carrying an ErrorEvent is a
		// failed response and is not cached.
		if !failed {
			p.cache.Set(key, collected)
		}
	}()
	return out, nil
}

// cacheable reports whether req is deterministic-eligible and may be cached.
// Only temperature-0 requests are treated as deterministic.
func cacheable(req Request) bool {
	return req.Temperature == 0
}

// replay returns a fresh closed channel that yields events in order, modeling a
// completed stream. A channel is single-use, so each cache hit must build its
// own.
func replay(events []Event) <-chan Event {
	ch := make(chan Event, len(events))
	for _, ev := range events {
		ch <- ev
	}
	close(ch)
	return ch
}

// cacheKeyEnvelope is the stable projection of a Request used to derive a cache
// key. It deliberately excludes volatile per-instance message metadata (ID,
// SessionID, CreatedAt, Usage) so that semantically identical conversations hash
// to the same key, and includes every field that can change the model output.
type cacheKeyEnvelope struct {
	Model           string            `json:"model"`
	SystemPrompt    string            `json:"system_prompt"`
	MaxTokens       int               `json:"max_tokens"`
	Temperature     float64           `json:"temperature"`
	ReasoningEffort string            `json:"reasoning_effort"`
	Thinking        *ThinkingConfig   `json:"thinking,omitempty"`
	Tools           []Tool            `json:"tools"`
	Messages        []messageEnvelope `json:"messages"`
}

// messageEnvelope is the cache-key projection of a single message: only the
// fields that affect the response are retained.
type messageEnvelope struct {
	Role    message.Role           `json:"role"`
	Content []message.ContentBlock `json:"content"`
}

// cacheKey returns a hex SHA-256 over a stable JSON projection of req. The
// projection uses plain structs with fixed field order so marshaling is
// deterministic across calls for equal inputs.
func cacheKey(req Request) (string, error) {
	env := cacheKeyEnvelope{
		Model:           req.Model,
		SystemPrompt:    req.SystemPrompt,
		MaxTokens:       req.MaxTokens,
		Temperature:     req.Temperature,
		ReasoningEffort: req.ReasoningEffort,
		Thinking:        req.Thinking,
		Tools:           req.Tools,
		Messages:        make([]messageEnvelope, len(req.Messages)),
	}
	for i, m := range req.Messages {
		env.Messages[i] = messageEnvelope{Role: m.Role, Content: m.Content}
	}

	data, err := json.Marshal(env)
	if err != nil {
		return "", fmt.Errorf("hashing request for cache key: %w", err)
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:]), nil
}

// defaultCacheCapacity bounds the default in-memory cache so an unbounded dev
// loop cannot grow it without limit.
const defaultCacheCapacity = 256

// LRUCache is a concurrency-safe, fixed-capacity in-memory ResponseCache that
// evicts the least recently used entry when full. It is the default cache used
// by CachingProvider when no cache is injected.
type LRUCache struct {
	mu       sync.Mutex
	capacity int
	ll       *list.List
	items    map[string]*list.Element
}

// lruEntry is the value stored in each list element.
type lruEntry struct {
	key    string
	events []Event
}

// LRUCache satisfies ResponseCache.
var _ ResponseCache = (*LRUCache)(nil)

// NewLRUCache returns an LRUCache holding at most capacity entries. A capacity
// of zero or less is clamped to the default so the cache always stores at least
// some entries.
func NewLRUCache(capacity int) *LRUCache {
	if capacity <= 0 {
		capacity = defaultCacheCapacity
	}
	return &LRUCache{
		capacity: capacity,
		ll:       list.New(),
		items:    make(map[string]*list.Element),
	}
}

// Get returns the events stored under key and marks the entry most recently
// used. The boolean reports whether the key was present.
func (c *LRUCache) Get(key string) ([]Event, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	el, ok := c.items[key]
	if !ok {
		return nil, false
	}
	c.ll.MoveToFront(el)
	return el.Value.(*lruEntry).events, true
}

// Set stores events under key as the most recently used entry, evicting the
// least recently used entry if the cache is over capacity.
func (c *LRUCache) Set(key string, events []Event) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if el, ok := c.items[key]; ok {
		c.ll.MoveToFront(el)
		el.Value.(*lruEntry).events = events
		return
	}

	el := c.ll.PushFront(&lruEntry{key: key, events: events})
	c.items[key] = el

	if c.ll.Len() > c.capacity {
		oldest := c.ll.Back()
		if oldest != nil {
			c.ll.Remove(oldest)
			delete(c.items, oldest.Value.(*lruEntry).key)
		}
	}
}
