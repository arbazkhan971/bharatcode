package llm

import (
	"fmt"
	"os"
	"sync"
)

// keyringServiceName is the service identifier used when storing and retrieving
// tokens in the OS keyring. It must match internal/cmd/login.go:keyringService.
const keyringServiceName = "bharatcode"

// keyringReader is the interface used by resolveAPIKey to look up a stored
// token. The default implementation is a no-op that returns an empty string so
// the function degrades gracefully when no OS keyring is wired up. Replace it
// in tests or at startup (via SetKeyringReader) to connect a real keyring.
type keyringReader interface {
	Get(service, account string) (string, error)
}

// keyringWriter is the interface used by StoreAPIKey to persist a token. The
// default implementation is a no-op that succeeds without storing anything, so
// callers degrade gracefully when no OS keyring is wired up. Replace it at
// startup (via SetKeyringWriter) to connect a real keyring.
type keyringWriter interface {
	Set(service, account, secret string) error
}

// noopKeyring is the zero-value keyring that always reports "not found"
// without an error, so resolveAPIKey falls through cleanly to its own error.
// Its Set is a no-op so StoreAPIKey degrades quietly when no keyring is wired.
type noopKeyring struct{}

func (noopKeyring) Get(_, _ string) (string, error) { return "", nil }

func (noopKeyring) Set(_, _, _ string) error { return nil }

// keyringMu guards activeKeyring and activeKeyringWriter. SetKeyring* acquires
// a write lock; resolveAPIKey and StoreAPIKey acquire a read lock so concurrent
// provider goroutines do not race against startup wiring.
var keyringMu sync.RWMutex

// activeKeyring is the package-level keyring reader used by resolveAPIKey.
// It starts as a no-op; cmd layer replaces it with the OS keyring at startup.
var activeKeyring keyringReader = noopKeyring{}

// activeKeyringWriter is the package-level keyring writer used by StoreAPIKey.
// It starts as a no-op; the cmd layer replaces it with the OS keyring at
// startup so the TUI onboarding can persist a token through the same backend
// resolveAPIKey reads from.
var activeKeyringWriter keyringWriter = noopKeyring{}

// SetKeyringReader replaces the package-level keyring reader. It is called by
// the cmd layer after boot so that provider key resolution can consult the OS
// keyring without creating an import cycle between internal/llm and internal/cmd.
func SetKeyringReader(r keyringReader) {
	keyringMu.Lock()
	activeKeyring = r
	keyringMu.Unlock()
}

// SetKeyringWriter replaces the package-level keyring writer. It is called by
// the cmd layer after boot so the TUI onboarding can persist a provider token
// through the same OS keyring resolveAPIKey reads — without creating an import
// cycle between internal/llm and internal/cmd. A token stored this way is
// resolvable immediately on the next provider call, since key lookup is lazy.
func SetKeyringWriter(w keyringWriter) {
	keyringMu.Lock()
	activeKeyringWriter = w
	keyringMu.Unlock()
}

// StoreAPIKey persists a provider token in the OS keyring under the shared
// service name, keyed by providerName (the lowercase provider key, matching the
// account resolveAPIKey looks up and 'bharatcode login' writes). It lets the TUI
// onboarding save a key without importing the cmd layer. Because key resolution
// is lazy — resolveAPIKey is consulted on every provider call rather than at
// provider-build time — a token stored here takes effect on the very next turn,
// with no restart required.
func StoreAPIKey(providerName, token string) error {
	keyringMu.RLock()
	w := activeKeyringWriter
	keyringMu.RUnlock()
	if w == nil {
		return fmt.Errorf("storing API key for %s: no keyring configured", providerName)
	}
	if err := w.Set(keyringServiceName, providerName, token); err != nil {
		return fmt.Errorf("storing API key for %s: %w", providerName, err)
	}
	return nil
}

// HasAPIKey reports whether a usable API key resolves for a provider via the
// same two-step lookup resolveAPIKey uses (environment variable, then OS
// keyring). It lets the TUI decide at startup whether the active provider is
// already set up — and so whether first-run onboarding is needed — without
// triggering a provider call. An empty envVar means the provider needs no key
// (a local endpoint such as Ollama or LM Studio), so it always reports true.
func HasAPIKey(envVar, providerName string) bool {
	if envVar == "" {
		return true
	}
	_, err := resolveAPIKey(envVar, providerName)
	return err == nil
}

// resolveAPIKey returns the API key for a provider using a two-step lookup:
//
//  1. os.Getenv(envVar) — environment variable wins so CI and explicit
//     overrides always take precedence.
//  2. activeKeyring.Get(keyringServiceName, providerName) — falls back to a
//     token stored by 'bharatcode login'.
//
// If both sources are empty the function returns an error wrapping ErrAuth with
// a message that tells the user exactly how to fix it.
func resolveAPIKey(envVar, providerName string) (string, error) {
	if v := os.Getenv(envVar); v != "" {
		return v, nil
	}
	keyringMu.RLock()
	kr := activeKeyring
	keyringMu.RUnlock()
	if kr != nil {
		if v, err := kr.Get(keyringServiceName, providerName); err == nil && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key for %s: set %s or run 'bharatcode login %s': %w",
		providerName, envVar, providerName, ErrAuth)
}
