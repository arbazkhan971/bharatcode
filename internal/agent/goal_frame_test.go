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

// TestFirstUserGoal pins the goal-extraction helper: it returns the first user
// message's text, ignores leading non-user and empty messages, concatenates
// multiple text blocks, and truncates an over-long goal.
func TestFirstUserGoal(t *testing.T) {
	// Empty history yields no goal.
	require.Equal(t, "", firstUserGoal(nil))

	// The first user message wins; a leading assistant message is skipped.
	history := []message.Message{
		{Role: message.RoleAssistant, Content: []message.ContentBlock{message.TextBlock{Text: "hi, how can I help?"}}},
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "  build a CLI parser  "}}},
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "now add tests"}}},
	}
	require.Equal(t, "build a CLI parser", firstUserGoal(history),
		"goal must be the first user message, trimmed, not a later one")

	// A user message with no text block is skipped in favour of the next one
	// that carries text.
	withToolFirst := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.ToolResultBlock{ToolUseID: "x"}}},
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: "the real goal"}}},
	}
	require.Equal(t, "the real goal", firstUserGoal(withToolFirst))

	// Multiple text blocks in the first user message are concatenated in order.
	multiBlock := []message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{
			message.TextBlock{Text: "line one"},
			message.TextBlock{Text: "line two"},
		}},
	}
	require.Equal(t, "line one\nline two", firstUserGoal(multiBlock))

	// An over-long goal is truncated to maxGoalFrameRunes with an ellipsis.
	long := strings.Repeat("x", maxGoalFrameRunes+50)
	got := firstUserGoal([]message.Message{
		{Role: message.RoleUser, Content: []message.ContentBlock{message.TextBlock{Text: long}}},
	})
	require.Equal(t, maxGoalFrameRunes+1, len([]rune(got)),
		"over-long goal must be truncated to the cap plus the ellipsis rune")
	require.True(t, strings.HasSuffix(got, "…"), "truncated goal must end with an ellipsis")
}

// TestGoalFrameReInjectedAcrossTurns proves the persistent goal frame: the
// user's ORIGINAL request is re-injected into the system prompt on every turn,
// even after later turns add new user messages. The frame is carried in the
// system prompt, never inside the message history.
func TestGoalFrameReInjectedAcrossTurns(t *testing.T) {
	ctx := context.Background()
	repo := testRepo(t)
	sessionID := testSession(t, repo)

	provider := &scriptProvider{scripts: [][]llm.Event{
		{llm.DeltaTextEvent{Text: "turn 1 done"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}}},
		{llm.DeltaTextEvent{Text: "turn 2 done"}, llm.EndEvent{Usage: llm.Usage{InputTokens: 5, OutputTokens: 2}}},
	}}

	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     provider,
		Tools:        newFakeRegistry(),
		Sessions:     repo,
		Bus:          pubsub.NewTopic[Event]("agent-test", 16),
		SystemPrompt: "base prompt",
	})

	// Turn 1: the original goal.
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("build a CLI parser")))
	// Turn 2: a follow-up that must NOT replace the persisted goal.
	require.NoError(t, loop.Run(ctx, sessionID, userMessage("now refactor the lexer")))

	require.Len(t, provider.reqs, 2)
	for i, req := range provider.reqs {
		require.True(t, strings.HasPrefix(req.SystemPrompt, "base prompt"),
			"turn %d system prompt must lead with the base prompt", i+1)
		require.Contains(t, req.SystemPrompt, "Active goal for this session",
			"turn %d must re-inject the active-goal frame", i+1)
		require.Contains(t, req.SystemPrompt, "build a CLI parser",
			"turn %d must re-inject the ORIGINAL goal, not a later message", i+1)
		require.NotContains(t, req.SystemPrompt, "now refactor the lexer",
			"the goal frame must hold the original goal, not the latest follow-up")
		// The frame lives in the system prompt, never inside the conversation.
		require.False(t, reqContains(req, "Active goal for this session"),
			"the goal frame must not leak into the message history")
	}
}

// TestGoalFrameAbsentBeforeAnyRun proves the goal frame is opt-in: a freshly
// constructed Loop that has not run yet carries no frame, so the system prompt
// equals the unmodified base prompt.
func TestGoalFrameAbsentBeforeAnyRun(t *testing.T) {
	loop := New(Config{
		Name:         "coder",
		Model:        "fake-model",
		Provider:     &scriptProvider{},
		Tools:        newFakeRegistry(),
		Sessions:     testRepo(t),
		SystemPrompt: "base prompt",
	})

	require.Equal(t, "base prompt", loop.systemPrompt(),
		"with no captured goal the system prompt must be the unmodified base prompt")
	require.Equal(t, "", loop.goalFrame())
}
