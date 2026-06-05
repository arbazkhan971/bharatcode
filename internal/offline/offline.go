// Package offline implements BharatCode's sovereignty offline mode: a verifiable
// guarantee that no source code or prompt leaves the local machine. When the
// mode is active BharatCode rejects any LLM provider whose endpoint is not on
// localhost and disables the web_fetch and web_search tools, so the only network
// traffic is to a model running on the same machine.
package offline

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

// EnvVar forces offline mode on when set to a truthy value (1, true, yes, on;
// case-insensitive). A command-line flag may enable it independently.
const EnvVar = "BHARATCODE_OFFLINE"

// Banner is the proof message surfaced at startup when offline mode is active.
const Banner = "offline mode active: code will not leave this machine"

// EnabledFromEnv reports whether BHARATCODE_OFFLINE selects offline mode.
func EnabledFromEnv() bool {
	return truthy(os.Getenv(EnvVar))
}

func truthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// CheckProviders verifies every enabled provider in cfg resolves to a localhost
// endpoint, the precondition for offline mode. It returns a single error naming
// every offending provider so the user can fix them all at once, or nil when the
// configuration is safe to run offline. A disabled provider is ignored: it is
// never contacted, so it cannot leak code.
func CheckProviders(cfg *config.Config) error {
	if cfg == nil {
		return nil
	}
	var offenders []string
	for _, p := range cfg.Providers {
		if p.Disabled {
			continue
		}
		if !providerIsLocal(p) {
			offenders = append(offenders, fmt.Sprintf("%q (%s -> %s)", p.Name, p.Type, describeEndpoint(p)))
		}
	}
	if len(offenders) == 0 {
		return nil
	}
	return fmt.Errorf(
		"offline mode rejects non-localhost providers: %s; point base_url at a local model server (e.g. ollama at http://localhost:11434) or disable these providers",
		strings.Join(offenders, ", "),
	)
}

// providerIsLocal reports whether p's effective endpoint is a loopback address.
func providerIsLocal(p config.Provider) bool {
	// codex_oauth always talks to OpenAI's remote Codex backend; its BaseURL only
	// overrides a local auth-file path, never the network endpoint, so it can
	// never be offline-safe.
	if p.Type == config.ProviderCodexOAuth {
		return false
	}
	return isLoopbackURL(effectiveBaseURL(p))
}

// effectiveBaseURL reproduces the registry's per-type default so the offline
// check sees the same endpoint the LLM client will actually dial. An explicit
// base_url always wins; otherwise local-by-design providers default to localhost
// and remote providers default to their public API.
func effectiveBaseURL(p config.Provider) string {
	if strings.TrimSpace(p.BaseURL) != "" {
		return p.BaseURL
	}
	switch p.Type {
	case config.ProviderOllama:
		return "http://localhost:11434"
	case config.ProviderLMStudio:
		return "http://localhost:1234/v1"
	case config.ProviderAnthropic:
		return "https://api.anthropic.com/v1"
	case config.ProviderGemini:
		return "https://generativelanguage.googleapis.com"
	default:
		// openai, openai_compatible, openai_responses, and any unknown type fall
		// through to OpenAI's public API when no base_url is given.
		return "https://api.openai.com/v1"
	}
}

// describeEndpoint renders the endpoint shown in the rejection message.
func describeEndpoint(p config.Provider) string {
	if p.Type == config.ProviderCodexOAuth {
		return "remote Codex backend"
	}
	return effectiveBaseURL(p)
}

// isLoopbackURL reports whether rawURL names a loopback host (localhost, an IPv4
// 127.x address, or ::1). A URL that cannot be parsed, or that has no host, is
// treated as non-local: offline mode fails closed.
func isLoopbackURL(rawURL string) bool {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return false
	}
	host := u.Hostname()
	if host == "" {
		return false
	}
	if strings.EqualFold(host, "localhost") {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}
