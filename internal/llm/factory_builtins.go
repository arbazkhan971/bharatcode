package llm

import (
	"github.com/arbazkhan971/bharatcode/internal/config"
)

// init registers a Factory for every provider type BharatCode ships with, so
// NewRegistry constructs providers by looking the type up in the package-level
// factory registry instead of branching on a hardcoded switch. Routing every
// built-in through the same registry a third party uses (via Register) means a
// custom provider plugs in on equal footing: the wiring path does not special-
// case the built-ins. The registrations run once at package load; a duplicate
// or malformed key here is a programming error, so MustRegister panics loudly at
// startup rather than failing the first request that needs the provider.
func init() {
	registerBuiltinProviders()
}

// registerBuiltinProviders installs the built-in provider factories. It is split
// out of init so a test can assert the built-in keys are present without
// re-running registration (which would panic on the duplicates).
func registerBuiltinProviders() {
	MustRegister(string(config.ProviderOpenAI), func(spec ProviderSpec) (Provider, error) {
		baseURL := spec.Config.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return newOpenAICompatibleProvider(spec.Config.Name, baseURL, spec.Config.APIKeyEnv, spec.Models, spec.Client), nil
	})

	MustRegister(string(config.ProviderOpenAIResponses), func(spec ProviderSpec) (Provider, error) {
		baseURL := spec.Config.BaseURL
		if baseURL == "" {
			baseURL = "https://api.openai.com/v1"
		}
		return newOpenAIResponsesProvider(spec.Config.Name, baseURL, spec.Config.APIKeyEnv, spec.Models, spec.Client), nil
	})

	MustRegister(string(config.ProviderCodexOAuth), func(spec ProviderSpec) (Provider, error) {
		// Experimental: reuses the Codex CLI's local subscription token. BaseURL
		// overrides the auth-file path for tests; empty = default.
		return newCodexOAuthProvider(spec.Config.Name, spec.Models, spec.Client, spec.Config.BaseURL), nil
	})

	MustRegister(string(config.ProviderChatGPT), func(spec ProviderSpec) (Provider, error) {
		// Experimental: "Sign in with ChatGPT". BharatCode performs the OAuth login
		// ('bharatcode auth chatgpt') and stores/refreshes the token itself. BaseURL
		// overrides the auth-file path for tests; empty = default config-dir location.
		return newChatGPTOAuthProvider(spec.Config.Name, spec.Models, spec.Client, spec.Config.BaseURL), nil
	})

	openAICompatible := func(spec ProviderSpec) (Provider, error) {
		return newOpenAICompatibleProvider(spec.Config.Name, spec.Config.BaseURL, spec.Config.APIKeyEnv, spec.Models, spec.Client), nil
	}
	MustRegister(string(config.ProviderOpenAICompatible), openAICompatible)
	MustRegister(string(config.ProviderLMStudio), openAICompatible)
	// Azure OpenAI speaks the OpenAI chat-completions wire format but authenticates
	// via the "api-key" header; isAzureOpenAI detects the host at request time and
	// selects the scheme, so it routes to the same compatible provider.
	MustRegister(string(config.ProviderAzure), openAICompatible)

	MustRegister(string(config.ProviderOllama), func(spec ProviderSpec) (Provider, error) {
		return newOllamaProvider(spec.Config.Name, spec.Config.BaseURL, spec.Models, spec.Client), nil
	})

	MustRegister(string(config.ProviderAnthropic), func(spec ProviderSpec) (Provider, error) {
		baseURL := spec.Config.BaseURL
		if baseURL == "" {
			baseURL = "https://api.anthropic.com/v1"
		}
		return newAnthropicProvider(spec.Config.Name, baseURL, spec.Config.APIKeyEnv, spec.Models, spec.Client), nil
	})

	MustRegister(string(config.ProviderGemini), func(spec ProviderSpec) (Provider, error) {
		// An empty BaseURL falls through to the Google API default inside the
		// provider constructor.
		return newGeminiProvider(spec.Config.Name, spec.Config.BaseURL, spec.Config.APIKeyEnv, spec.Models, spec.Client), nil
	})
}
