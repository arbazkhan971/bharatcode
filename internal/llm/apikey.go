package llm

import (
	"fmt"
	"os"
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

// noopKeyring is the zero-value keyring that always reports "not found"
// without an error, so resolveAPIKey falls through cleanly to its own error.
type noopKeyring struct{}

func (noopKeyring) Get(_, _ string) (string, error) { return "", nil }

// activeKeyring is the package-level keyring reader used by resolveAPIKey.
// It starts as a no-op; cmd layer replaces it with the OS keyring at startup.
var activeKeyring keyringReader = noopKeyring{}

// SetKeyringReader replaces the package-level keyring reader. It is called by
// the cmd layer after boot so that provider key resolution can consult the OS
// keyring without creating an import cycle between internal/llm and internal/cmd.
func SetKeyringReader(r keyringReader) {
	activeKeyring = r
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
	if activeKeyring != nil {
		if v, err := activeKeyring.Get(keyringServiceName, providerName); err == nil && v != "" {
			return v, nil
		}
	}
	return "", fmt.Errorf("no API key for %s: set %s or run 'bharatcode login %s': %w",
		providerName, envVar, providerName, ErrAuth)
}
