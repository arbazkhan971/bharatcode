package llm

import (
	"context"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/go-retryablehttp"

	"github.com/arbazkhan971/bharatcode/internal/config"
)

const defaultRequestTimeout = 2 * time.Minute

// providerOpenAIResponses opts a provider into OpenAI's Responses API
// (/v1/responses) instead of chat/completions. It is defined in-package as a
// typed config.ProviderType so the registry can route to the Responses client
// without the chat/completions path changing. Making it selectable from a
// config file additionally requires adding the constant and allowing it in the
// config package validator; that lives outside this package and is a followup.
const providerOpenAIResponses = config.ProviderOpenAIResponses

// providerCodexOAuth is the experimental provider that reuses the Codex CLI's
// stored ChatGPT subscription token. See codexOAuthProvider for the caveats.
const providerCodexOAuth = config.ProviderCodexOAuth

// providerChatGPT is the experimental "Sign in with ChatGPT" provider, which
// performs its own OAuth login and owns the token lifecycle. See
// chatgptOAuthProvider for the caveats.
const providerChatGPT = config.ProviderChatGPT

// Registry holds configured providers and is safe for concurrent callers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	models    []Model
}

// NewRegistry constructs providers from cfg.
func NewRegistry(cfg *config.Config) (*Registry, error) {
	if cfg == nil {
		return nil, fmt.Errorf("constructing llm registry: config is nil")
	}

	modelsByProvider := make(map[string][]Model)
	allModels := make([]Model, 0, len(cfg.Models))
	for _, m := range cfg.Models {
		// A model configured without an explicit context_window falls back to a
		// family heuristic keyed on the model id, so the agent's compaction and
		// overflow checks get a real budget instead of zero ("unknown").
		contextWindow := m.ContextWindow
		if contextWindow <= 0 {
			contextWindow = inferContextWindow(m.ID)
		}
		// When a Compat.ContextWindow override is present it takes precedence over
		// both the catalog value and the heuristic, so the agent's compaction and
		// overflow checks honor the user's explicit configuration.
		if m.Compat != nil && m.Compat.ContextWindow != nil && *m.Compat.ContextWindow > 0 {
			contextWindow = *m.Compat.ContextWindow
		}

		// Compat.SupportsImages, when set, overrides the catalog flag for the
		// capability checks that gate image inputs on this model.
		supportsImages := m.SupportsImages
		if m.Compat != nil && m.Compat.SupportsImages != nil {
			supportsImages = *m.Compat.SupportsImages
		}

		model := Model{
			ID:                    m.ID,
			Provider:              m.Provider,
			ContextWindow:         contextWindow,
			InputPricePerMTokUSD:  m.InputPricePerMTokUSD,
			OutputPricePerMTokUSD: m.OutputPricePerMTokUSD,
			SupportsImages:        supportsImages,
			SupportsTools:         m.SupportsTools,
			ReasoningEffort:       m.ReasoningEffort,
			ThinkingBudget:        m.ThinkingBudget,
			Compat:                m.Compat,
		}
		modelsByProvider[strings.ToLower(m.Provider)] = append(modelsByProvider[strings.ToLower(m.Provider)], model)
		allModels = append(allModels, model)
	}

	timeout := cfg.Options.RequestTimeout
	if timeout <= 0 {
		timeout = defaultRequestTimeout
	}

	reg := &Registry{
		providers: make(map[string]Provider),
		models:    allModels,
	}
	for _, p := range cfg.Providers {
		if p.Disabled {
			continue
		}
		name := strings.ToLower(p.Name)
		// Per-provider custom headers (OpenRouter attribution, Azure api-key, proxy
		// tokens) are injected at the transport layer so every provider dialect
		// honors them without re-plumbing its own header map. Empty headers leave
		// the client untouched. OpenRouter providers additionally get default
		// HTTP-Referer / X-Title attribution headers folded in, which the user's
		// own Headers override.
		headers := withOpenRouterAttribution(p.BaseURL, p.Headers)
		client := withExtraHeaders(newRetryingClient(timeout), headers)
		models := append([]Model(nil), modelsByProvider[name]...)

		// Construct the provider through the package-level factory registry. Every
		// built-in type registers a Factory in factory_builtins.go, so the common
		// providers resolve here exactly as the old type switch did, while a custom
		// provider registered via Register plugs into the same path with no special
		// casing. An unregistered type is an unsupported provider.
		factory, ok := Lookup(string(p.Type))
		if !ok {
			return nil, fmt.Errorf("constructing provider %q: %w", p.Name, ErrUnsupportedFeature)
		}
		provider, err := factory(ProviderSpec{Config: p, Models: models, Client: client})
		if err != nil {
			return nil, fmt.Errorf("constructing provider %q: %w", p.Name, err)
		}
		reg.providers[name] = provider
	}

	sort.Slice(reg.models, func(i, j int) bool {
		if reg.models[i].Provider == reg.models[j].Provider {
			return reg.models[i].ID < reg.models[j].ID
		}
		return reg.models[i].Provider < reg.models[j].Provider
	})

	return reg, nil
}

// Get returns a configured provider by name.
func (r *Registry) Get(providerName string) (Provider, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	p, ok := r.providers[strings.ToLower(providerName)]
	if !ok {
		return nil, fmt.Errorf("getting provider %q: %w", providerName, ErrProviderNotFound)
	}
	return p, nil
}

// ListModels returns a stable copy of all configured models.
func (r *Registry) ListModels() []Model {
	r.mu.RLock()
	defer r.mu.RUnlock()

	models := make([]Model, len(r.models))
	copy(models, r.models)
	return models
}

func newRetryingClient(timeout time.Duration) *http.Client {
	c := retryablehttp.NewClient()
	// Retries are owned by the backoff loop in http.go, so disable the
	// transport-level policy to keep attempt counts predictable.
	c.RetryMax = 0
	c.RetryWaitMin = 10 * time.Millisecond
	c.RetryWaitMax = 100 * time.Millisecond
	c.Logger = nil
	c.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		return false, nil
	}
	httpClient := c.StandardClient()
	httpClient.Timeout = timeout
	return httpClient
}

func supportsTools(models []Model) bool {
	for _, m := range models {
		if m.SupportsTools {
			return true
		}
	}
	return false
}

func supportsImages(models []Model) bool {
	for _, m := range models {
		if m.SupportsImages {
			return true
		}
	}
	return false
}
