package agent

import (
	"context"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/llm"
	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/arbazkhan971/bharatcode/internal/pubsub"
	"github.com/stretchr/testify/require"
)

// drainEvents reads all currently-buffered events from ch without blocking.
func drainEvents(ch <-chan Event) []Event {
	var out []Event
	for {
		select {
		case ev := <-ch:
			out = append(out, ev)
		default:
			return out
		}
	}
}

// TestAutoCompactTriggersAtThreshold verifies that when the provider reports an
// input-token count that meets or exceeds AutoCompactThreshold × contextWindow,
// the loop compacts in memory and publishes EventAutoCompacted.
func TestAutoCompactTriggersAtThreshold(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Seed some history with a distinctive ancient marker.
	seed := []message.Message{
		userMessage("ANCIENT-AC-1 setup question"),
		assistantMessage("ANCIENT-AC-1 setup answer"),
		userMessage("ANCIENT-AC-2 follow up"),
		assistantMessage("ANCIENT-AC-2 follow up answer"),
		userMessage("LATEST-AC-3 do the thing"),
	}
	for _, msg := range seed {
		require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
	}

	// Context window is 1000; threshold is 0.90 → fires at ≥ 900 tokens.
	// We report 950 input tokens so the threshold is met.
	provider := &scriptProvider{
		contextWindow: 1000,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "Done."},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 950, OutputTokens: 10}},
			},
		},
	}

	bus := pubsub.NewTopic[Event]("auto-compact-test", 64)
	events, cancel := bus.Subscribe()
	defer cancel()

	stub := &stubCompactor{condensed: []message.Message{
		{
			SessionID: sessionID,
			Role:      message.RoleUser,
			Content:   []message.ContentBlock{message.TextBlock{Text: "COMPACT-MARKER-ac7f summary"}},
		},
	}}

	loop := New(Config{
		Name:                 "coder",
		Model:                "fake-model",
		Provider:             provider,
		Tools:                newFakeRegistry(),
		Sessions:             repo,
		Bus:                  bus,
		Compactor:            stub,
		AutoCompactThreshold: 0.90,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("GO-AC-TRIGGER now")))

	evs := drainEvents(events)
	var gotAutoCompacted bool
	for _, ev := range evs {
		if ev.Kind == EventAutoCompacted {
			gotAutoCompacted = true
		}
	}
	require.True(t, gotAutoCompacted, "EventAutoCompacted must be published when threshold is met")

	// The compacted snapshot must be stored: a second run uses it.
	provider2 := &scriptProvider{
		contextWindow: 1000,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "Acknowledged."},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 50, OutputTokens: 5}},
			},
		},
	}
	loop.cfg.Provider = provider2
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("SECOND-TURN-AC-b2c3 continue")))

	require.Len(t, provider2.reqs, 1)
	req := provider2.reqs[0]
	// The compact marker must appear in what was sent to the provider.
	require.True(t, reqContains(req, "COMPACT-MARKER-ac7f"),
		"second turn must use the compacted history")
	// The ancient message must have been dropped.
	require.False(t, reqContains(req, "ANCIENT-AC-1"),
		"ancient message must not appear after auto-compaction")
}

// TestAutoCompactSkipsBelowThreshold verifies that when fill < threshold no
// compaction occurs and EventAutoCompacted is NOT published.
func TestAutoCompactSkipsBelowThreshold(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Context window 1000; threshold 0.90; we report 800 tokens (80% < 90%).
	provider := &scriptProvider{
		contextWindow: 1000,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "OK"},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 800, OutputTokens: 5}},
			},
		},
	}

	bus := pubsub.NewTopic[Event]("auto-compact-skip-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	stub := &stubCompactor{condensed: []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "SKIP-COMPACT-sentinel"}}},
	}}

	loop := New(Config{
		Name:                 "coder",
		Model:                "fake-model",
		Provider:             provider,
		Tools:                newFakeRegistry(),
		Sessions:             repo,
		Bus:                  bus,
		Compactor:            stub,
		AutoCompactThreshold: 0.90,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("below threshold")))

	for _, ev := range drainEvents(events) {
		require.NotEqual(t, EventAutoCompacted, ev.Kind,
			"EventAutoCompacted must NOT fire when fill is below threshold")
	}
}

// TestAutoCompactDisabledWhenThresholdZero verifies that a zero threshold
// disables auto-compaction entirely, even when the context is fully saturated.
func TestAutoCompactDisabledWhenThresholdZero(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Report 999 / 1000 tokens — well past any reasonable threshold.
	provider := &scriptProvider{
		contextWindow: 1000,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "full"},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 999, OutputTokens: 1}},
			},
		},
	}

	bus := pubsub.NewTopic[Event]("auto-compact-disabled-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	loop := New(Config{
		Name:                 "coder",
		Model:                "fake-model",
		Provider:             provider,
		Tools:                newFakeRegistry(),
		Sessions:             repo,
		Bus:                  bus,
		AutoCompactThreshold: 0, // disabled
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("disabled")))

	for _, ev := range drainEvents(events) {
		require.NotEqual(t, EventAutoCompacted, ev.Kind,
			"EventAutoCompacted must NOT fire when threshold is 0")
	}
}

// TestAutoCompactThresholdOneDisables verifies that a threshold ≥ 1.0 is
// treated as disabled — 100% fill is mathematically impossible to exceed.
func TestAutoCompactThresholdOneDisables(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	provider := &scriptProvider{
		contextWindow: 1000,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "at limit"},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 1000, OutputTokens: 1}},
			},
		},
	}

	bus := pubsub.NewTopic[Event]("auto-compact-one-test", 16)
	events, cancel := bus.Subscribe()
	defer cancel()

	loop := New(Config{
		Name:                 "coder",
		Model:                "fake-model",
		Provider:             provider,
		Tools:                newFakeRegistry(),
		Sessions:             repo,
		Bus:                  bus,
		AutoCompactThreshold: 1.0, // treated as disabled
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("at limit")))

	for _, ev := range drainEvents(events) {
		require.NotEqual(t, EventAutoCompacted, ev.Kind,
			"threshold >= 1.0 must behave as disabled")
	}
}
