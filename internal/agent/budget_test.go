package agent

import (
	"encoding/json"
	"testing"

	"github.com/arbazkhan971/bharatcode/internal/message"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

// toolUseMsg builds an assistant message that contains a single ToolUseBlock,
// which exercises the "partner of a tool result" boundary in cutPointByTokenBudget.
func toolUseMsg(id, name string) message.Message {
	return message.Message{
		Role: message.RoleAssistant,
		Content: []message.ContentBlock{
			message.ToolUseBlock{ID: id, Name: name, Input: json.RawMessage(`{}`)},
		},
	}
}

// toolResultMsg builds a user-role message that carries a ToolResultBlock,
// simulating the wire format the agent uses for tool results.
func toolResultMsg(toolUseID, content string) message.Message {
	return message.Message{
		Role: message.RoleUser,
		Content: []message.ContentBlock{
			message.ToolResultBlock{ToolUseID: toolUseID, Content: content},
		},
	}
}

// assistantMsgWithUsage builds an assistant message whose Usage is set to the
// given input-token count.
func assistantMsgWithUsage(text string, inputTokens int) message.Message {
	return message.Message{
		Role:    message.RoleAssistant,
		Content: []message.ContentBlock{message.TextBlock{Text: text}},
		Usage:   &message.TokenUsage{InputTokens: inputTokens, OutputTokens: 10},
	}
}

// ---------------------------------------------------------------------------
// cutPointByTokenBudget
// ---------------------------------------------------------------------------

// TestCutPointByTokenBudget_BasicSplit verifies that when the budget is large
// enough to hold the last N messages but not all of them, the returned index
// lands at the first kept message.
func TestCutPointByTokenBudget_BasicSplit(t *testing.T) {
	// Build a history where each message is roughly 10 tokens (40 bytes JSON).
	// We use messages whose JSON-marshalled content is approximately 40 chars.
	msgs := []message.Message{
		userMessage("msg-0-xxxxxxxx"),   // ~idx 0
		assistantMessage("msg-1-yyyyyy"), // ~idx 1
		userMessage("msg-2-xxxxxxxx"),   // ~idx 2
		assistantMessage("msg-3-yyyyyy"), // ~idx 3
	}

	// Estimate how many tokens the last 2 messages occupy.
	tail2tokens := estimateMessageTokens(msgs[2]) + estimateMessageTokens(msgs[3])

	// Budget exactly covers the last 2 messages (give a small margin).
	budget := tail2tokens
	idx := cutPointByTokenBudget(msgs, budget)

	// The cut must be at index 2 (last two kept) or later (never earlier: the
	// budget is tight so we can't keep more).
	require.LessOrEqual(t, 2, idx, "cut index must be at or after msg-2")
	require.Less(t, idx, len(msgs), "cut index must be a valid message index")

	// The tail from idx onward must fit the budget.
	tailTokens := 0
	for _, m := range msgs[idx:] {
		tailTokens += estimateMessageTokens(m)
	}
	require.LessOrEqual(t, tailTokens, budget+1, // +1 for rounding
		"kept tail must fit within the token budget")
}

// TestCutPointByTokenBudget_NeverStrandsToolResult is the key safety invariant:
// the cut point must never land on a tool-result message, because that would
// strand the result without its preceding tool-use call. When the budget
// boundary falls on a tool result, the cut must advance past it to the next
// non-tool-result message.
func TestCutPointByTokenBudget_NeverStrandsToolResult(t *testing.T) {
	// History layout:
	//   [0] user "setup"
	//   [1] assistant "planning"
	//   [2] assistant (tool-use call, id="tc1")
	//   [3] user (tool-result for "tc1")    <- orphan if cut lands here
	//   [4] assistant "done"
	//
	// We set a budget that would normally cut at index 3 (the tool result),
	// and verify the cut is advanced to index 4.
	setup := userMessage("setup-text-pad")
	planning := assistantMessage("planning-text")
	tcall := toolUseMsg("tc1", "view")
	tresult := toolResultMsg("tc1", "result content here")
	final := assistantMessage("done")

	history := []message.Message{setup, planning, tcall, tresult, final}

	// Budget covers tresult + final but NOT tcall.
	budget := estimateMessageTokens(tresult) + estimateMessageTokens(final)
	idx := cutPointByTokenBudget(history, budget)

	// The cut must NOT land on a tool result.
	require.False(t, hasToolResult(history[idx]),
		"cut point must not be a tool-result message (index %d)", idx)

	// It must land at or after index 3 (the natural budget boundary).
	require.GreaterOrEqual(t, idx, 3,
		"cut must be at the boundary or later, never before")
}

// TestCutPointByTokenBudget_TinyBudgetKeepsAtLeastOne verifies that when the
// budget is so small that not even one message fits, the function still returns
// a valid index (len-1) so at least the last message is kept.
func TestCutPointByTokenBudget_TinyBudgetKeepsAtLeastOne(t *testing.T) {
	msgs := []message.Message{
		userMessage("ancient"),
		assistantMessage("also old"),
		userMessage("recent"),
	}
	idx := cutPointByTokenBudget(msgs, 1) // 1 token — nothing really fits
	require.Less(t, idx, len(msgs), "must return a valid index")
	require.GreaterOrEqual(t, idx, 0)
}

// TestCutPointByTokenBudget_EmptyHistory verifies no panic on an empty slice.
func TestCutPointByTokenBudget_EmptyHistory(t *testing.T) {
	idx := cutPointByTokenBudget(nil, 1000)
	require.Equal(t, 0, idx)

	idx = cutPointByTokenBudget([]message.Message{}, 1000)
	require.Equal(t, 0, idx)
}

// TestCutPointByTokenBudget_BudgetCoversAll verifies that when the budget is
// large enough for the entire history, the cut lands at 0 (keep everything).
func TestCutPointByTokenBudget_BudgetCoversAll(t *testing.T) {
	msgs := []message.Message{
		userMessage("a"),
		assistantMessage("b"),
		userMessage("c"),
	}
	bigBudget := historyTokens(msgs) * 2
	idx := cutPointByTokenBudget(msgs, bigBudget)
	require.Equal(t, 0, idx, "budget covering all messages must keep all (cut at 0)")
}

// ---------------------------------------------------------------------------
// usageAnchoredTokenEstimate
// ---------------------------------------------------------------------------

// TestUsageAnchoredTokenEstimate_UsesLastAssistantAnchor verifies that when an
// assistant message reports usage, the estimate uses that InputTokens value as
// the anchor and only adds heuristic estimates for messages after it.
func TestUsageAnchoredTokenEstimate_UsesLastAssistantAnchor(t *testing.T) {
	// history: [user, assistant(no usage), assistant(usage=500), user(trailing)]
	history := []message.Message{
		userMessage("ancient question"),
		assistantMessage("intermediate reply"),
		assistantMsgWithUsage("checkpoint reply", 500),
		userMessage("trailing follow-up"),
	}

	estimate := usageAnchoredTokenEstimate(history)

	// The anchor contributes 500 tokens; the trailing user message is estimated
	// by the heuristic. The estimate must be >= 500 (at minimum the anchor) and
	// strictly greater because of the trailing message.
	require.GreaterOrEqual(t, estimate, 500,
		"estimate must include at least the anchor tokens")
	trailingEst := estimateMessageTokens(history[3])
	require.Equal(t, 500+trailingEst, estimate,
		"estimate must be anchor + heuristic for trailing messages")
}

// TestUsageAnchoredTokenEstimate_FallbackWithNoUsage verifies that when no
// assistant message has a usage record, the function falls back to the full
// heuristic estimate (same as historyTokens).
func TestUsageAnchoredTokenEstimate_FallbackWithNoUsage(t *testing.T) {
	history := []message.Message{
		userMessage("no usage here"),
		assistantMessage("no usage either"),
		userMessage("still none"),
	}
	fallback := historyTokens(history)
	anchored := usageAnchoredTokenEstimate(history)
	require.Equal(t, fallback, anchored,
		"without any usage anchor, must equal historyTokens fallback")
}

// TestUsageAnchoredTokenEstimate_PicksLastAnchor verifies that when multiple
// assistant messages have usage, the LAST one is used as the anchor.
func TestUsageAnchoredTokenEstimate_PicksLastAnchor(t *testing.T) {
	history := []message.Message{
		assistantMsgWithUsage("first checkpoint", 100),
		userMessage("turn 2"),
		assistantMsgWithUsage("second checkpoint", 800), // <- this is the anchor
		userMessage("trailing-1"),
		userMessage("trailing-2"),
	}
	estimate := usageAnchoredTokenEstimate(history)

	trailing := estimateMessageTokens(history[3]) + estimateMessageTokens(history[4])
	require.Equal(t, 800+trailing, estimate,
		"must use the last assistant usage (800) as anchor, not 100")
}

// TestUsageAnchoredTokenEstimate_EmptyHistory verifies no panic on empty input.
func TestUsageAnchoredTokenEstimate_EmptyHistory(t *testing.T) {
	require.Equal(t, 0, usageAnchoredTokenEstimate(nil))
	require.Equal(t, 0, usageAnchoredTokenEstimate([]message.Message{}))
}
