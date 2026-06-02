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

// stubCompactor returns a fixed condensed slice and records the history it was
// asked to compact, so the test can prove Compact forwarded the real history.
type stubCompactor struct {
	condensed []message.Message
	gotLen    int
	gotRoles  []message.Role
}

func (c *stubCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	_ = ctx
	c.gotLen = len(history)
	c.gotRoles = nil
	for _, msg := range history {
		c.gotRoles = append(c.gotRoles, msg.Role)
	}
	return append([]message.Message(nil), c.condensed...), nil
}

// TestCompactReplacesProviderHistoryPreservesInvariantsAndDisk drives the full
// seam: build N on-disk messages, compact with a stub Compactor returning a
// known condensed slice, then Run one turn and inspect what the provider got.
func TestCompactReplacesProviderHistoryPreservesInvariantsAndDisk(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	// Build N on-disk messages spanning several user/assistant turns. The
	// sentinel "ANCIENT-PROMPT-7c1f" lives in the OLDEST user message and must
	// be dropped by compaction. The latest genuine user message carries
	// "LATEST-PROMPT-9a2b" and must survive.
	seed := []message.Message{
		userMessage("ANCIENT-PROMPT-7c1f tell me about the repo"),
		assistantMessage("The repo is a Go agent."),
		userMessage("now refactor the budget code"),
		assistantMessage("Refactoring budget.go now."),
		userMessage("LATEST-PROMPT-9a2b explain the result"),
		assistantMessage("Here is the explanation."),
	}
	for _, msg := range seed {
		require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
	}

	before, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.Len(t, before, len(seed))

	// The stub returns ONLY a known summary marker. It deliberately omits the
	// latest user message, so the Loop must re-append it to honor the invariant.
	stub := &stubCompactor{condensed: []message.Message{
		{
			SessionID: sessionID,
			Role:      message.RoleUser,
			Content:   []message.ContentBlock{message.TextBlock{Text: "CONDENSED-SUMMARY-MARKER-d4e5"}},
		},
	}}

	provider := &scriptProvider{scripts: [][]llm.Event{
		{
			llm.DeltaTextEvent{Text: "Acknowledged."},
			llm.EndEvent{Usage: llm.Usage{InputTokens: 3, OutputTokens: 2}},
		},
	}}

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

	require.NoError(t, loop.Compact(ctx, sessionID))

	// The Compactor received the real, full on-disk history (not an empty slice).
	require.Equal(t, len(seed), stub.gotLen)
	require.Equal(
		t,
		[]message.Role{
			message.RoleUser, message.RoleAssistant,
			message.RoleUser, message.RoleAssistant,
			message.RoleUser, message.RoleAssistant,
		},
		stub.gotRoles,
	)

	// (c) Compaction must NOT mutate the on-disk session.
	after, err := repo.Messages(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, before, after, "on-disk messages changed after Compact")

	// Drive one turn so the provider receives the compacted history.
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("FOLLOWUP-PROMPT-3f6a continue please")))

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]

	// (b) The system prompt is preserved (carried separately, never compacted).
	require.Equal(t, "SYSTEM-PROMPT-SENTINEL-b8c9", req.SystemPrompt)

	// (a) The provider request uses the stub's condensed history.
	require.True(t, reqContains(req, "CONDENSED-SUMMARY-MARKER-d4e5"),
		"provider request missing the stub's condensed marker")

	// (b) The latest genuine user message at compaction time is preserved,
	// even though the stub dropped it (the Loop re-appended it).
	require.True(t, reqContains(req, "LATEST-PROMPT-9a2b"),
		"provider request missing the preserved latest user message")

	// The new user message that arrived this turn is present (grafted on).
	require.True(t, reqContains(req, "FOLLOWUP-PROMPT-3f6a"),
		"provider request missing the new turn's user message")

	// Positive proof of dropping: the ancient prompt is gone from the request,
	// even though it is still on disk.
	require.False(t, reqContains(req, "ANCIENT-PROMPT-7c1f"),
		"dropped ancient message leaked into provider request")
	require.True(t, diskContains(after, "ANCIENT-PROMPT-7c1f"),
		"ancient message should still be on disk")
}

// TestCompactDefaultCompactorDropsAndMarks exercises the built-in Compactor:
// with no injected Compactor it drops the older prefix and inserts a marker
// while keeping the recent tail.
func TestCompactDefaultCompactorDropsAndMarks(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	seed := []message.Message{
		userMessage("OLDEST-1a2b first question"),
		assistantMessage("first answer"),
		userMessage("middle question"),
		assistantMessage("middle answer"),
		userMessage("RECENT-9z8y latest question"),
	}
	for _, msg := range seed {
		require.NoError(t, repo.AppendMessage(ctx, sessionID, msg))
	}

	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "ok"}, llm.EndEvent{}},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "sys",
		// No Compactor: the default drop-and-mark Compactor is used.
	})

	require.NoError(t, loop.Compact(ctx, sessionID))
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("FOLLOWUP-c3d4")))

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]

	// The marker replaced the dropped prefix.
	require.True(t, reqContains(req, compactionSummaryMarker),
		"default Compactor did not insert its marker")
	// The oldest message is gone from the request but the recent tail survives.
	require.False(t, reqContains(req, "OLDEST-1a2b"), "oldest message should be dropped")
	require.True(t, reqContains(req, "RECENT-9z8y"), "recent message should be kept")
	require.True(t, reqContains(req, "FOLLOWUP-c3d4"), "new turn message should be present")
}

// TestRunWithoutCompactionIsUnaffected guards the no-op path: when Compact was
// never called, Run sends the full on-disk history untouched.
func TestRunWithoutCompactionIsUnaffected(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	require.NoError(t, repo.AppendMessage(ctx, sessionID, userMessage("HISTORIC-7m8n earlier")))
	require.NoError(t, repo.AppendMessage(ctx, sessionID, assistantMessage("earlier reply")))

	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "done"}, llm.EndEvent{}},
	}}
	loop := New(Config{
		Name:     "coder",
		Model:    "fake-model",
		Provider: provider,
		Tools:    newFakeRegistry(),
		Sessions: repo,
	})

	require.NoError(t, loop.Run(ctx, sessionID, userMessage("CURRENT-5k6l now")))

	require.Len(t, provider.reqs, 1)
	req := provider.reqs[0]
	require.True(t, reqContains(req, "HISTORIC-7m8n"), "no-compaction path must keep full history")
	require.True(t, reqContains(req, "CURRENT-5k6l"))
}

func assistantMessage(text string) message.Message {
	return message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
	}
}

func reqContains(req llm.Request, substr string) bool {
	for _, msg := range req.Messages {
		if textContainsBlock(msg, substr) {
			return true
		}
	}
	return false
}

func diskContains(history []message.Message, substr string) bool {
	for _, msg := range history {
		if textContainsBlock(msg, substr) {
			return true
		}
	}
	return false
}

func textContainsBlock(msg message.Message, substr string) bool {
	for _, block := range msg.Content {
		if b, ok := block.(message.TextBlock); ok {
			if strings.Contains(b.Text, substr) {
				return true
			}
		}
	}
	return false
}
