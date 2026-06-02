package agent

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// oneTurnTextProvider returns a scriptProvider that replies once with plain
// text, exposing the given model catalog so a single Run issues exactly one
// recorded request whose reasoning fields can be inspected.
func oneTurnTextProvider(models []llm.Model) *scriptProvider {
	return &scriptProvider{
		scripts: [][]llm.Event{{
			llm.DeltaTextEvent{Text: "done"},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 4, OutputTokens: 2}},
		}},
		models: models,
	}
}

// TestApplyReasoning_PopulatesRequestFromModelConfig proves Feature 6 end-to-end
// within the loop: when the active model declares a reasoning effort and a
// thinking budget, the provider request carries both. The provider gates these
// by model id downstream, so the loop's job is only to surface the configured
// values onto the request.
func TestApplyReasoning_PopulatesRequestFromModelConfig(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	provider := oneTurnTextProvider([]llm.Model{{
		ID:              "reasoning-model",
		Provider:        "fake",
		ContextWindow:   8192,
		SupportsTools:   true,
		ReasoningEffort: "high",
		ThinkingBudget:  4096,
	}})

	loop := New(Config{
		Name:         "coder",
		Model:        "reasoning-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("reasoning-test", 16),
		SystemPrompt: "test prompt",
	})

	require.NoError(t, loop.Run(ctx, testSession(t, repo), userMessage("hi")))
	require.Len(t, provider.reqs, 1)
	require.Equal(t, "high", provider.reqs[0].ReasoningEffort)
	require.NotNil(t, provider.reqs[0].Thinking)
	require.Equal(t, 4096, provider.reqs[0].Thinking.BudgetTokens)
}

// TestApplyReasoning_UnconfiguredModelLeavesRequestUntouched proves the
// non-breaking default: a model with no reasoning config produces a request with
// empty reasoning fields, exactly as before the wiring.
func TestApplyReasoning_UnconfiguredModelLeavesRequestUntouched(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	provider := oneTurnTextProvider([]llm.Model{{
		ID:            "plain-model",
		Provider:      "fake",
		ContextWindow: 8192,
		SupportsTools: true,
	}})

	loop := New(Config{
		Name:         "coder",
		Model:        "plain-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("reasoning-test", 16),
		SystemPrompt: "test prompt",
	})

	require.NoError(t, loop.Run(ctx, testSession(t, repo), userMessage("hi")))
	require.Len(t, provider.reqs, 1)
	require.Empty(t, provider.reqs[0].ReasoningEffort)
	require.Nil(t, provider.reqs[0].Thinking)
}

// TestSetPlanMode_TogglesAtRuntime proves the runtime plan-mode primitive the
// TUI /plan and /approve commands drive: SetPlanMode flips the live flag both
// ways without recreating the loop.
func TestSetPlanMode_TogglesAtRuntime(t *testing.T) {
	repo := testRepo(t)
	loop := New(Config{
		Name:         "coder",
		Model:        "plain-model",
		Provider:     oneTurnTextProvider([]llm.Model{{ID: "plain-model", Provider: "fake", SupportsTools: true}}),
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("plan-test", 16),
		SystemPrompt: "test prompt",
	})

	require.False(t, loop.PlanMode(), "plan mode is off by default")
	loop.SetPlanMode(true)
	require.True(t, loop.PlanMode(), "SetPlanMode(true) enters plan mode")
	loop.Approve()
	require.False(t, loop.PlanMode(), "Approve exits plan mode")
}
