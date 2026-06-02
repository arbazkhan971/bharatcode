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
		model := Model{
			ID:                    m.ID,
			Provider:              m.Provider,
			ContextWindow:         m.ContextWindow,
			InputPricePerMTokUSD:  m.InputPricePerMTokUSD,
			OutputPricePerMTokUSD: m.OutputPricePerMTokUSD,
			SupportsImages:        m.SupportsImages,
			SupportsTools:         m.SupportsTools,
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
		client := newRetryingClient(timeout)
		models := append([]Model(nil), modelsByProvider[name]...)

		var provider Provider
		switch p.Type {
		case config.ProviderOpenAI:
			baseURL := p.BaseURL
			if baseURL == "" {
				baseURL = "https://api.openai.com/v1"
			}
			provider = newOpenAICompatibleProvider(p.Name, baseURL, p.APIKeyEnv, models, client)
		case config.ProviderOpenAICompatible, config.ProviderLMStudio:
			provider = newOpenAICompatibleProvider(p.Name, p.BaseURL, p.APIKeyEnv, models, client)
		case config.ProviderOllama:
			provider = newOllamaProvider(p.Name, p.BaseURL, models, client)
		case config.ProviderAnthropic:
			provider = unsupportedProvider{name: p.Name, models: models}
		default:
			return nil, fmt.Errorf("constructing provider %q: %w", p.Name, ErrUnsupportedFeature)
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
	c.RetryMax = 1
	c.RetryWaitMin = 10 * time.Millisecond
	c.RetryWaitMax = 100 * time.Millisecond
	c.Logger = nil
	c.CheckRetry = func(ctx context.Context, resp *http.Response, err error) (bool, error) {
		if err != nil {
			return retryablehttp.DefaultRetryPolicy(ctx, resp, err)
		}
		return false, nil
	}
	httpClient := c.StandardClient()
	httpClient.Timeout = timeout
	return httpClient
}

type unsupportedProvider struct {
	name   string
	models []Model
}

func (p unsupportedProvider) Name() string {
	return p.name
}

func (p unsupportedProvider) Stream(ctx context.Context, req Request) (<-chan Event, error) {
	return nil, fmt.Errorf("streaming with provider %q: %w", p.name, ErrNotYetSupported)
}

func (p unsupportedProvider) Models() []Model {
	models := make([]Model, len(p.models))
	copy(models, p.models)
	return models
}

func (p unsupportedProvider) SupportsTools() bool {
	return supportsTools(p.models)
}

func (p unsupportedProvider) SupportsImages() bool {
	return supportsImages(p.models)
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
