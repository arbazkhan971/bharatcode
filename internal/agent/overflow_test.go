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

// recordingCompactor returns a fixed condensed slice and records that it was
// invoked along with the history it received, so a test can prove automatic
// compaction fired on overflow and saw the real, over-window history.
type recordingCompactor struct {
	condensed []message.Message
	calls     int
	gotTokens int
}

func (c *recordingCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	_ = ctx
	c.calls++
	c.gotTokens = historyTokens(history)
	return append([]message.Message(nil), c.condensed...), nil
}

// bigText returns a deterministic filler string of n bytes containing the given
// marker, used to inflate a message's estimated token cost past the window.
func bigText(marker string, n int) string {
	var b strings.Builder
	b.WriteString(marker)
	b.WriteByte(' ')
	for b.Len() < n {
		b.WriteString("filler ")
	}
	return b.String()
}

// TestRunCompactsOnOverflowAndFitsWindow proves the core feature: when the
// on-disk history exceeds the model context window, Run invokes the Compactor
// to SUMMARIZE (not hard-drop), the provider request fits the usable window, and
// the invariants hold (system prompt carried separately, latest user message
// preserved). It also proves the dropped ancient content does not leak.
func TestRunCompactsOnOverflowAndFitsWindow(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Each big message is ~5000 bytes (~1250 estimated tokens). Three of them
	// (~3750 tokens) blow past the usable budget for a 2048-token window, so
	// compaction must fire.
	seed := []message.Message{
		userMessage(bigText("ANCIENT-PROMPT-7c1f", 5000)),
		assistantMessage(bigText("ancient-answer", 5000)),
		userMessage(bigText("MIDDLE-PROMPT-2b3c", 5000)),
		assistantMessage(bigText("middle-answer", 5000)),
		assistantMessage(bigText("trailing-answer", 5000)),
	}
	for _, msg := range seed {
		require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
	}

	before, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)

	// The stub returns ONLY a tiny summary marker and deliberately omits the
	// latest user message, forcing the loop to re-append it (the invariant).
	stub := &recordingCompactor{condensed: []message.Message{
		{
			SessionID: sessionID,
			Role:      message.RoleUser,
			Content:   []message.ContentBlock{message.TextBlock{Text: "CONDENSED-SUMMARY-MARKER-d4e5"}},
		},
	}}

	provider := &scriptProvider{
		contextWindow: 2048,
		scripts: [][]llm.Event{
			{
				llm.DeltaTextEvent{Text: "ok"},
				llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 2}},
			},
		},
	}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "SYSTEM-PROMPT-SENTINEL-b8c9",
		Compactor:    stub,
	})

	// A small follow-up keeps the new turn well under the window so the only
	// reason for overflow is the seeded history. This is the latest genuine user
	// message at fit time and must survive compaction.
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("LATEST-PROMPT-9a2b continue")))

	// Compaction was invoked automatically on overflow, and it saw the real,
	// over-window history (its tokens exceeded the usable budget).
	require.Equal(t, 1, stub.calls, "automatic compaction must fire exactly once on overflow")
	budget := fitBudget(2048, "SYSTEM-PROMPT-SENTINEL-b8c9")
	require.Greater(t, stub.gotTokens, budget,
		"compactor should have received an over-budget history")

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]

	// The request actually fits the usable window after compaction.
	require.LessOrEqual(t, historyTokens(req.Messages), budget,
		"compacted provider request must fit the usable window")

	// Invariant: the system prompt is carried separately, never inside history.
	require.Equal(t, "SYSTEM-PROMPT-SENTINEL-b8c9", req.SystemPrompt)
	require.False(t, reqContains(req, "SYSTEM-PROMPT-SENTINEL-b8c9"),
		"system prompt must not leak into the message history")

	// Invariant: the latest genuine user message (this turn's live prompt)
	// survives even though the stub dropped it (the loop re-appended it).
	require.True(t, reqContains(req, "LATEST-PROMPT-9a2b"),
		"latest user message must be preserved through compaction")

	// The condensed marker is present and the dropped ancient content is gone
	// from the request, though it remains on disk.
	require.True(t, reqContains(req, "CONDENSED-SUMMARY-MARKER-d4e5"))
	require.False(t, reqContains(req, "ANCIENT-PROMPT-7c1f"),
		"dropped ancient message must not leak into the provider request")

	// On-disk history is untouched by automatic compaction.
	after, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.True(t, diskContains(after, "ANCIENT-PROMPT-7c1f"),
		"ancient message must still be on disk")
	require.True(t, diskContains(after, "LATEST-PROMPT-9a2b"),
		"the new turn's message is persisted")
	// The seeded prefix (5 seed + 1 followup) is intact on disk.
	require.GreaterOrEqual(t, len(after), len(before)+1)
}

// TestRunFallsBackToDropOldestWhenCompactionInsufficient proves the fallback:
// when the Compactor cannot free enough room (it returns a still-over-window
// slice), Run drops oldest messages so the turn still proceeds and the request
// fits, while always retaining the latest user message.
func TestRunFallsBackToDropOldestWhenCompactionInsufficient(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	seed := []message.Message{
		userMessage(bigText("ANCIENT-PROMPT-7c1f", 5000)),
		assistantMessage(bigText("ancient-answer", 5000)),
		userMessage(bigText("MIDDLE-PROMPT-2b3c", 5000)),
		assistantMessage(bigText("middle-answer", 5000)),
		userMessage(bigText("LATEST-PROMPT-9a2b", 200)),
	}
	for _, msg := range seed {
		require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
	}

	// This Compactor "fails to help": it returns the entire over-window history
	// unchanged, so compaction cannot make it fit and the loop must fall back to
	// drop-oldest.
	uselessCompactor := &passthroughCompactor{}

	provider := &scriptProvider{
		contextWindow: 2048,
		scripts: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "ok"}, llm.EndEvent{}},
		},
	}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "sys",
		Compactor:    uselessCompactor,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("FOLLOWUP-3f6a continue")))

	require.Equal(t, 1, uselessCompactor.calls, "compaction is attempted before the fallback")

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]

	budget := fitBudget(2048, "sys")
	require.LessOrEqual(t, historyTokens(req.Messages), budget,
		"drop-oldest fallback must bring the request within the window")

	// Drop-oldest always keeps the latest user message and the new turn's
	// message, and drops the oldest large prefix.
	require.True(t, reqContains(req, "FOLLOWUP-3f6a"),
		"the latest user message must survive drop-oldest")
	require.False(t, reqContains(req, "ANCIENT-PROMPT-7c1f"),
		"the oldest message must be dropped by the fallback")
}

// TestRunReturnsOverflowWhenLatestMessageExceedsWindow proves the hard-overflow
// case: when the latest user message alone is larger than the usable window, no
// compaction or drop-oldest can rescue the turn, so Run returns ErrContextOverflow
// rather than looping. It must not call the provider.
func TestRunReturnsOverflowWhenLatestMessageExceedsWindow(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	require.NoError(t, repo.AppendMessage(ctx, sessionID, userMessage("small earlier")))
	require.NoError(t, repo.AppendMessage(ctx, sessionID, assistantMessage("small reply")))

	// The Compactor must never be reached on the hard-overflow path.
	guard := &passthroughCompactor{}

	provider := &scriptProvider{
		contextWindow: 2048,
		scripts: [][]llm.Event{
			{llm.DeltaTextEvent{Text: "should never run"}, llm.EndEvent{}},
		},
	}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "sys",
		Compactor:    guard,
	})

	// The single latest user message alone (~40000 bytes ~ 10000 tokens) dwarfs
	// the ~2047-token usable window.
	err := loop.Run(ctx, sessionID, userMessage(bigText("HUGE-PROMPT-5e6f", 40000)))

	require.ErrorIs(t, err, ErrContextOverflow,
		"an oversized latest user message must return ErrContextOverflow")
	require.Equal(t, 0, guard.calls, "compaction must not run when the turn cannot possibly fit")
	require.Empty(t, provider.reqs, "the provider must not be called on hard overflow")
}

// passthroughCompactor returns its input unchanged and counts invocations. It
// models a Compactor that cannot free enough room.
type passthroughCompactor struct {
	calls int
}

func (c *passthroughCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	_ = ctx
	c.calls++
	return append([]message.Message(nil), history...), nil
}
