package llm

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

// ErrFactoryRegistered indicates Register was called with a key that already
// has a factory. It is a sentinel so callers can distinguish a duplicate from a
// malformed registration with errors.Is.
var ErrFactoryRegistered = errors.New("provider factory already registered")

// ProviderSpec carries everything a Factory needs to construct one Provider:
// the user's provider config entry plus the inputs the registry resolves at
// wiring time (the provider's models and a ready HTTP client with per-provider
// headers already folded in). It mirrors the arguments NewRegistry threads into
// each provider constructor today, so a Factory can be a thin adapter over an
// existing new*Provider call.
type ProviderSpec struct {
	// Config is the validated provider entry from the loaded config.
	Config config.Provider
	// Models are the models resolved for this provider, already copied so the
	// factory may retain the slice without aliasing registry state.
	Models []Model
	// Client is the HTTP client the provider should use; it carries the request
	// timeout and any per-provider custom headers.
	Client *http.Client
}

// Factory constructs a Provider from a resolved ProviderSpec. An error is
// returned only when the spec cannot yield a usable provider (for example a
// required field is missing); the zero value of Provider is never returned
// without an accompanying error.
type Factory func(spec ProviderSpec) (Provider, error)

// providerFactories maps a lower-cased provider type key to its Factory. It is
// kept as a sync.Map so registration (typically at init time) and concurrent
// lookups during wiring need no external locking. Keys are compared
// case-insensitively via the canonical form produced by factoryKey.
var providerFactories sync.Map // map[string]Factory

// Register associates a provider type key with a factory. The key is matched
// case-insensitively and trimmed of surrounding whitespace, so "OpenAI" and
// "openai" resolve to the same entry. Registering an already-registered key, or
// registering an empty key or nil factory, returns an error and leaves any
// existing registration untouched. This makes Register safe to call from
// multiple package init functions without silently shadowing a provider.
func Register(key string, factory Factory) error {
	k := factoryKey(key)
	if k == "" {
		return fmt.Errorf("registering provider factory: key is empty")
	}
	if factory == nil {
		return fmt.Errorf("registering provider factory %q: factory is nil", key)
	}
	if _, loaded := providerFactories.LoadOrStore(k, factory); loaded {
		return fmt.Errorf("registering provider factory %q: %w", key, ErrFactoryRegistered)
	}
	return nil
}

// MustRegister is like Register but panics on error. It is intended for use in
// package init functions, where a duplicate or malformed registration is a
// programming error that should fail loudly at startup rather than at request
// time.
func MustRegister(key string, factory Factory) {
	if err := Register(key, factory); err != nil {
		panic(err)
	}
}

// Lookup returns the factory registered for key. The lookup is
// case-insensitive. The boolean is false when no factory is registered, mirror-
// ing the comma-ok form callers use for map access.
func Lookup(key string) (Factory, bool) {
	v, ok := providerFactories.Load(factoryKey(key))
	if !ok {
		return nil, false
	}
	return v.(Factory), true
}

// Registered returns the sorted set of registered provider keys. The result is
// a fresh slice the caller owns; it is primarily useful for diagnostics and for
// validating config against the providers actually compiled in.
func Registered() []string {
	keys := make([]string, 0)
	providerFactories.Range(func(k, _ any) bool {
		keys = append(keys, k.(string))
		return true
	})
	sort.Strings(keys)
	return keys
}

// factoryKey canonicalizes a provider key so registration and lookup agree
// regardless of casing or incidental whitespace.
func factoryKey(key string) string {
	return strings.ToLower(strings.TrimSpace(key))
}
