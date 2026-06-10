package llm

import (
	"errors"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

// TestBuiltinProvidersRegistered asserts every provider type the type switch
// used to handle now resolves through the package-level factory registry, so the
// registry — not a hardcoded branch — is the single source of truth NewRegistry
// consults.
func TestBuiltinProvidersRegistered(t *testing.T) {
	builtins := []config.ProviderType{
		config.ProviderOpenAI,
		config.ProviderOpenAIResponses,
		config.ProviderOpenAICompatible,
		config.ProviderLMStudio,
		config.ProviderAzure,
		config.ProviderOllama,
		config.ProviderAnthropic,
		config.ProviderGemini,
		config.ProviderCodexOAuth,
		config.ProviderChatGPT,
	}
	for _, typ := range builtins {
		if _, ok := Lookup(string(typ)); !ok {
			t.Errorf("Lookup(%q): built-in provider type is not registered", typ)
		}
	}
}

// TestNewRegistryUsesFactoryRegistry proves NewRegistry constructs providers
// through the registry: a custom provider type registered at runtime is wired
// into the registry exactly like a built-in, with no special-casing in the
// construction path. This is the behavior that "unblocks custom providers".
func TestNewRegistryUsesFactoryRegistry(t *testing.T) {
	const customType = "test-custom-provider"
	var got ProviderSpec
	if err := Register(customType, func(spec ProviderSpec) (Provider, error) {
		got = spec
		return newOllamaProvider(spec.Config.Name, spec.Config.BaseURL, spec.Models, spec.Client), nil
	}); err != nil {
		t.Fatalf("Register: %v", err)
	}

	cfg := testConfig("custom", config.ProviderType(customType), "http://localhost:9999")
	reg, err := NewRegistry(cfg)
	if err != nil {
		t.Fatalf("NewRegistry: %v", err)
	}

	provider, err := reg.Get("custom")
	if err != nil {
		t.Fatalf("Get(custom): %v", err)
	}
	if provider.Name() != "custom" {
		t.Fatalf("provider name: got %q want %q", provider.Name(), "custom")
	}
	// The registry must have threaded the resolved models and a ready client into
	// the spec, not just the bare config entry.
	if len(got.Models) != 1 || got.Models[0].ID != "test-model" {
		t.Fatalf("factory spec models: got %+v want one model test-model", got.Models)
	}
	if got.Client == nil {
		t.Fatalf("factory spec client: got nil, want a ready HTTP client")
	}
}

// TestNewRegistryRejectsUnregisteredType confirms a provider whose type has no
// registered factory fails construction with ErrUnsupportedFeature rather than
// silently producing a nil provider.
func TestNewRegistryRejectsUnregisteredType(t *testing.T) {
	cfg := testConfig("nope", config.ProviderType("test-never-registered-type"), "")
	_, err := NewRegistry(cfg)
	if !errors.Is(err, ErrUnsupportedFeature) {
		t.Fatalf("NewRegistry: error = %v, want ErrUnsupportedFeature", err)
	}
}
