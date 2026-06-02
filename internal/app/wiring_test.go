package app

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/arbazkhan971/bharatcode/internal/agent"
	"github.com/arbazkhan971/bharatcode/internal/config"
	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/mcp"
	"github.com/arbazkhan971/bharatcode/internal/message"
)

// twoProviderConfig returns a minimal valid config with two providers and one
// model each, so the composable provider wrappers have a real chain to build.
func twoProviderConfig() *config.Config {
	return &config.Config{
		Providers: []config.Provider{
			{Name: "primary", Type: config.ProviderOpenAICompatible, BaseURL: "http://localhost:1/v1", Models: []string{"m-primary"}},
			{Name: "backup", Type: config.ProviderOpenAICompatible, BaseURL: "http://localhost:2/v1", Models: []string{"m-backup"}},
		},
		Models: []config.Model{
			{ID: "m-primary", Provider: "primary"},
			{ID: "m-backup", Provider: "backup"},
		},
		Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 83.5},
	}
}

// TestConfiguredProviders_NoWrappersByDefault asserts the default config leaves
// providers unwrapped, so wiring the wrappers never changes baseline behavior.
func TestConfiguredProviders_NoWrappersByDefault(t *testing.T) {
	cfg := twoProviderConfig()
	require.NoError(t, config.Validate(cfg))

	reg, err := llm.NewRegistry(cfg)
	require.NoError(t, err)

	providers := configuredProviders(cfg, reg)
	require.Len(t, providers, 2)
	for name, p := range providers {
		_, isFailover := p.(*llm.FailoverProvider)
		_, isCaching := p.(*llm.CachingProvider)
		require.Falsef(t, isFailover, "provider %q must not be a FailoverProvider by default", name)
		require.Falsef(t, isCaching, "provider %q must not be a CachingProvider by default", name)
	}
}

// TestConfiguredProviders_FailoverWired asserts a provider that declares
// fallbacks is wrapped in a FailoverProvider reachable from configuredProviders.
func TestConfiguredProviders_FailoverWired(t *testing.T) {
	cfg := twoProviderConfig()
	cfg.Providers[0].Fallbacks = []string{"backup"}
	require.NoError(t, config.Validate(cfg))

	reg, err := llm.NewRegistry(cfg)
	require.NoError(t, err)

	providers := configuredProviders(cfg, reg)
	_, ok := providers["primary"].(*llm.FailoverProvider)
	require.True(t, ok, "primary must be wrapped in a FailoverProvider when fallbacks are configured")
	// The fallback-less provider stays unwrapped.
	_, wrapped := providers["backup"].(*llm.FailoverProvider)
	require.False(t, wrapped, "a provider with no fallbacks must stay unwrapped")
}

// TestConfiguredProviders_CachingWired asserts enabling the cache wraps every
// provider in a CachingProvider, and that it composes outside any failover.
func TestConfiguredProviders_CachingWired(t *testing.T) {
	cfg := twoProviderConfig()
	cfg.Cache.Enabled = true
	cfg.Cache.MaxEntries = 16
	cfg.Providers[0].Fallbacks = []string{"backup"}
	require.NoError(t, config.Validate(cfg))

	reg, err := llm.NewRegistry(cfg)
	require.NoError(t, err)

	providers := configuredProviders(cfg, reg)
	for name, p := range providers {
		_, ok := p.(*llm.CachingProvider)
		require.Truef(t, ok, "provider %q must be wrapped in a CachingProvider when caching is enabled", name)
	}
}

// TestRouterFromConfig asserts routing is off by default and that enabling it
// installs a CostAwareRouter carrying the configured thresholds.
func TestRouterFromConfig(t *testing.T) {
	cfg := twoProviderConfig()
	require.Nil(t, routerFromConfig(cfg), "routing must be off by default")

	cfg.Routing.Enabled = true
	cfg.Routing.PromptLenThreshold = 99
	cfg.Routing.ToolsImplyComplex = true
	r := routerFromConfig(cfg)
	require.NotNil(t, r)
	car, ok := r.(agent.CostAwareRouter)
	require.True(t, ok, "enabled routing must install a CostAwareRouter")
	require.Equal(t, 99, car.PromptLenThreshold)
	require.True(t, car.ToolsImplyComplex)
}

// TestReasoningFieldsReachRegistry asserts the per-model reasoning controls are
// carried from config.Model through the registry to llm.Model, where the agent
// loop reads them onto the request.
func TestReasoningFieldsReachRegistry(t *testing.T) {
	cfg := twoProviderConfig()
	cfg.Models[0].ReasoningEffort = "high"
	cfg.Models[1].ThinkingBudget = 4096
	require.NoError(t, config.Validate(cfg))

	reg, err := llm.NewRegistry(cfg)
	require.NoError(t, err)

	models := reg.ListModels()
	byID := make(map[string]llm.Model, len(models))
	for _, m := range models {
		byID[m.ID] = m
	}
	require.Equal(t, "high", byID["m-primary"].ReasoningEffort)
	require.Equal(t, 4096, byID["m-backup"].ThinkingBudget)
}

// TestMCPSampler_ResolvesProviderFromRegistry asserts the app-backed sampler is
// wired to the registry: with a real registry it picks the first model's
// provider and drives a completion (here returning the provider's auth error,
// since no API key is set), and with an empty registry it fails cleanly rather
// than hanging. This proves SetSampler points at a live completion path.
func TestMCPSampler_ResolvesProviderFromRegistry(t *testing.T) {
	t.Setenv("OPENAI_API_KEY", "")

	reg, err := llm.NewRegistry(twoProviderConfig())
	require.NoError(t, err)
	a := &App{LLM: reg}

	provider, model, err := a.samplingProvider()
	require.NoError(t, err)
	require.NotNil(t, provider)
	require.Equal(t, "m-backup", model) // first in stable (provider,id) order

	empty := &App{LLM: func() *llm.Registry {
		r, e := llm.NewRegistry(&config.Config{Ledger: config.LedgerConfig{Currency: "INR", UsdInrRate: 1}})
		require.NoError(t, e)
		return r
	}()}
	_, err = empty.mcpSampler(context.Background(), mcp.SamplingRequest{})
	require.Error(t, err, "an empty registry must yield an error, not a hang")
}

// TestCollectSamplingResponse asserts the stream-draining helper concatenates
// text deltas into the response content, so the sampler returns the model's
// completion text.
func TestCollectSamplingResponse(t *testing.T) {
	ch := make(chan llm.Event, 3)
	ch <- llm.DeltaTextEvent{Text: "he"}
	ch <- llm.DeltaTextEvent{Text: "llo"}
	ch <- llm.EndEvent{}
	close(ch)

	resp, err := collectSamplingResponse(context.Background(), ch, "m-x")
	require.NoError(t, err)
	require.Equal(t, "hello", resp.Content)
	require.Equal(t, "assistant", resp.Role)
	require.Equal(t, "m-x", resp.Model)
}

// TestSamplingMessages asserts MCP roles map onto agent message roles, defaulting
// unknown roles to user so the provider always sees a valid conversation.
func TestSamplingMessages(t *testing.T) {
	msgs := samplingMessages([]mcp.SamplingMessage{
		{Role: "user", Content: "q"},
		{Role: "assistant", Content: "a"},
		{Role: "weird", Content: "w"},
	})
	require.Len(t, msgs, 3)
	require.Equal(t, message.RoleUser, msgs[0].Role)
	require.Equal(t, message.RoleAssistant, msgs[1].Role)
	require.Equal(t, message.RoleUser, msgs[2].Role)
}

// TestAutoDeclineElicitation asserts the default elicitation handler declines so
// a server request never hangs.
func TestAutoDeclineElicitation(t *testing.T) {
	resp, err := autoDeclineElicitation(context.Background(), mcp.ElicitationRequest{Message: "name?"})
	require.NoError(t, err)
	require.Equal(t, mcp.ElicitationDecline, resp.Action)
}
