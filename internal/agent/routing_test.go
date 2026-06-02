package agent

import (
	"context"
	"strings"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// twoModelProvider returns a scriptProvider exposing a cheap and a strong model
// distinguished by price, each backed by a single text-only reply so one Run
// produces exactly one provider request whose Model field records the routed
// choice. The cheap model is priced below the strong one so a cost-aware router
// can rank them by price alone.
func twoModelProvider(turns int) *scriptProvider {
	scripts := make([][]llm.Event, 0, turns)
	for range turns {
		scripts = append(scripts, []llm.Event{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		})
	}
	return &scriptProvider{
		scripts: scripts,
		models: []llm.Model{
			{
				ID:                    "cheap-model",
				Provider:              "fake",
				ContextWindow:         8192,
				InputPricePerMTokUSD:  0.25,
				OutputPricePerMTokUSD: 1.25,
				SupportsTools:         true,
			},
			{
				ID:                    "strong-model",
				Provider:              "fake",
				ContextWindow:         8192,
				InputPricePerMTokUSD:  3.0,
				OutputPricePerMTokUSD: 15.0,
				SupportsTools:         true,
			},
		},
	}
}

// TestCostAwareRouterRoutesByTurnComplexity proves the end-to-end routing path:
// a short, simple turn reaches the cheap model and a long, complex turn reaches
// the strong model, with the choice ranked by the models' price metadata.
func TestCostAwareRouterRoutesByTurnComplexity(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	provider := twoModelProvider(2)

	// Default to the strong model so the simple case proves routing actively
	// steers DOWN to cheap, not merely that it left the default in place.
	loop := New(Config{
		Name:         "coder",
		Model:        "strong-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("routing-test", 16),
		SystemPrompt: "test prompt",
		Router:       CostAwareRouter{PromptLenThreshold: 40},
	})

	// Simple turn: a short prompt well under the threshold routes to the cheap
	// model.
	simpleSession := testSession(t, repo)
	require.NoError(t, loop.Run(ctx, simpleSession, userMessage("fix typo")))

	// Complex turn: a long prompt at or above the threshold routes to the strong
	// model. A fresh session keeps the two turns independent.
	complexSession := testSession(t, repo)
	longPrompt := strings.Repeat("design a distributed rate limiter. ", 4)
	require.GreaterOrEqual(t, len(longPrompt), 40, "long prompt must clear the threshold")
	require.NoError(t, loop.Run(ctx, complexSession, userMessage(longPrompt)))

	require.Len(t, provider.reqs, 2, "each turn must issue exactly one provider request")
	require.Equal(t, "cheap-model", provider.reqs[0].Model, "simple turn must route to the cheap model")
	require.Equal(t, "strong-model", provider.reqs[1].Model, "complex turn must route to the strong model")
}

// fixedRouter always returns modelID, so a test can force a specific routing
// decision regardless of the turn's content.
type fixedRouter struct{ modelID string }

func (r fixedRouter) Route(Turn, []llm.Model) string { return r.modelID }

// TestRoutedModelDrivesContextWindow locks the fit ordering: the per-turn model
// must be resolved BEFORE history is fit, so the context-window budget reflects
// the model actually used. It routes to a model whose window differs from the
// configured default and asserts the Loop's effective window follows the routed
// model, not the default.
func TestRoutedModelDrivesContextWindow(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	provider := &scriptProvider{
		scripts: [][]llm.Event{{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		}},
		models: []llm.Model{
			{ID: "wide-model", Provider: "fake", ContextWindow: 200000, SupportsTools: true},
			{ID: "narrow-model", Provider: "fake", ContextWindow: 4096, SupportsTools: true},
		},
	}

	// Default is the wide model; force routing to the narrow one.
	loop := New(Config{
		Name:         "coder",
		Model:        "wide-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("routing-test", 16),
		SystemPrompt: "test prompt",
		Router:       fixedRouter{modelID: "narrow-model"},
	})

	// Before any turn the window reflects the configured default.
	require.Equal(t, 200000, loop.contextWindow(), "pre-run window must be the configured default")

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("short")))

	require.Equal(t, "narrow-model", provider.reqs[0].Model, "turn must route to the narrow model")
	require.Equal(t, "narrow-model", loop.activeModel, "active model must be the routed model")
	require.Equal(t, 4096, loop.contextWindow(),
		"context window must follow the routed model, proving fit used the routed window")
}

// TestRouterDefaultsToConfiguredModel proves routing is opt-in: with no Router
// the Loop always uses its configured model, even for a long prompt that a
// cost-aware router would escalate.
func TestRouterDefaultsToConfiguredModel(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	provider := twoModelProvider(1)
	sessionID := testSession(t, repo)

	loop := New(Config{
		Name:         "coder",
		Model:        "cheap-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("routing-test", 16),
		SystemPrompt: "test prompt",
		// No Router: behavior must be unchanged.
	})

	longPrompt := strings.Repeat("design a distributed rate limiter. ", 8)
	require.NoError(t, loop.Run(ctx, sessionID, userMessage(longPrompt)))

	require.Len(t, provider.reqs, 1)
	require.Equal(t, "cheap-model", provider.reqs[0].Model, "no Router must pin the configured model")
}

// TestRouteHintOverridesHeuristic proves an explicit complexity hint wins over
// the prompt-length heuristic: a short prompt hinted complex still escalates to
// the strong model.
func TestRouteHintOverridesHeuristic(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	provider := twoModelProvider(1)
	sessionID := testSession(t, repo)

	loop := New(Config{
		Name:         "coder",
		Model:        "cheap-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("routing-test", 16),
		SystemPrompt: "test prompt",
		Router:       CostAwareRouter{},
		RouteHint:    ComplexityComplex,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("hi")))

	require.Len(t, provider.reqs, 1)
	require.Equal(t, "strong-model", provider.reqs[0].Model, "explicit complex hint must escalate a short prompt")
}

// TestRouterUnitClassifiesAndRanks exercises CostAwareRouter.Route directly,
// covering the price-based ranking and the order-based fallback when no price
// metadata is present.
func TestRouterUnitClassifiesAndRanks(t *testing.T) {
	priced := []llm.Model{
		{ID: "a", InputPricePerMTokUSD: 3, OutputPricePerMTokUSD: 15},
		{ID: "b", InputPricePerMTokUSD: 0.25, OutputPricePerMTokUSD: 1.25},
	}
	r := CostAwareRouter{PromptLenThreshold: 40}

	simple := Turn{History: []message.Message{userMessage("short")}}
	require.Equal(t, "b", r.Route(simple, priced), "simple turn picks the cheapest priced model")

	complex := Turn{History: []message.Message{userMessage(strings.Repeat("x", 80))}}
	require.Equal(t, "a", r.Route(complex, priced), "complex turn picks the strongest priced model")

	// Unpriced models fall back to order: first is cheap, last is strong.
	unpriced := []llm.Model{{ID: "first"}, {ID: "last"}}
	require.Equal(t, "first", r.Route(simple, unpriced), "unpriced simple turn picks the first model")
	require.Equal(t, "last", r.Route(complex, unpriced), "unpriced complex turn picks the last model")

	// A single model gives the router no meaningful choice: it declines.
	require.Equal(t, "", r.Route(complex, priced[:1]), "fewer than two models declines routing")
}
