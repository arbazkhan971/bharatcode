package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

const reservedResponseTokens = 4096

// compactionSummaryMarker prefixes the synthetic message that the default
// Compactor leaves in place of the dropped conversation history.
const compactionSummaryMarker = "[compacted history]"

// Compactor condenses a conversation history into a smaller equivalent that is
// cheaper to send to a provider. Implementations must be pure: they receive a
// copy of the history and return a new slice; they must not mutate the input.
type Compactor interface {
	// Compact returns a condensed form of history. The returned slice replaces
	// the in-memory history sent to the provider; it does not affect on-disk
	// session storage.
	Compact(ctx context.Context, history []message.Message) ([]message.Message, error)
}

// dropAndMarkCompactor is the default Compactor. It drops the older portion of
// the conversation and leaves a single synthetic marker message in its place,
// retaining a tail of recent messages verbatim. The marker preserves a short
// textual census of what was condensed so the model knows context was elided.
type dropAndMarkCompactor struct {
	// keepRecent is the number of trailing messages preserved verbatim.
	keepRecent int
}

// newDropAndMarkCompactor returns the default Compactor, retaining keepRecent
// trailing messages verbatim. A non-positive keepRecent is clamped to 1.
func newDropAndMarkCompactor(keepRecent int) dropAndMarkCompactor {
	if keepRecent < 1 {
		keepRecent = 1
	}
	return dropAndMarkCompactor{keepRecent: keepRecent}
}

// Compact drops all but the most recent keepRecent messages, replacing the
// dropped prefix with a single marker message summarizing the count. When the
// history already fits within keepRecent, it is returned unchanged.
func (c dropAndMarkCompactor) Compact(ctx context.Context, history []message.Message) ([]message.Message, error) {
	_ = ctx
	if len(history) <= c.keepRecent {
		return append([]message.Message(nil), history...), nil
	}
	dropped := history[:len(history)-c.keepRecent]
	tail := history[len(history)-c.keepRecent:]

	out := make([]message.Message, 0, len(tail)+1)
	out = append(out, message.Message{
		SessionID: sessionIDOf(history),
		Role:      message.RoleUser,
		Content: []message.ContentBlock{message.TextBlock{
			Text: fmt.Sprintf("%s %s", compactionSummaryMarker, summarizeDropped(dropped)),
		}},
		CreatedAt: history[0].CreatedAt,
	})
	out = append(out, tail...)
	return out, nil
}

// summarizeDropped renders a terse, deterministic census of the dropped
// messages so the marker carries some signal about the elided context.
func summarizeDropped(dropped []message.Message) string {
	var users, assistants, tools int
	for _, msg := range dropped {
		switch msg.Role {
		case message.RoleUser:
			if hasToolResult(msg) {
				tools++
			} else {
				users++
			}
		case message.RoleAssistant:
			assistants++
		}
	}
	return fmt.Sprintf(
		"%d earlier messages condensed (%d user, %d assistant, %d tool result).",
		len(dropped), users, assistants, tools,
	)
}

func hasToolResult(msg message.Message) bool {
	for _, block := range msg.Content {
		if _, ok := block.(message.ToolResultBlock); ok {
			return true
		}
	}
	return false
}

func sessionIDOf(history []message.Message) string {
	for _, msg := range history {
		if msg.SessionID != "" {
			return msg.SessionID
		}
	}
	return ""
}

// latestUserIndex returns the index of the most recent message whose role is
// user and that does not carry a tool result, or -1 when none exists. Tool
// results are user-role on the wire but are not genuine user turns, so they are
// excluded to find the real prompt.
func latestUserIndex(history []message.Message) int {
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Role == message.RoleUser && !hasToolResult(history[i]) {
			return i
		}
	}
	return -1
}

// containsMessage reports whether want appears in history by value equality of
// its serialized content and role. It is used to enforce the preserve-latest
// invariant without relying on pointer identity.
func containsMessage(history []message.Message, want message.Message) bool {
	wantText := strings.TrimSpace(textContent(want))
	for _, msg := range history {
		if msg.Role != want.Role {
			continue
		}
		if strings.TrimSpace(textContent(msg)) == wantText && wantText != "" {
			return true
		}
	}
	return false
}

func textContent(msg message.Message) string {
	var b strings.Builder
	for _, block := range msg.Content {
		if t, ok := block.(message.TextBlock); ok {
			b.WriteString(t.Text)
		}
	}
	return b.String()
}

func truncateForContext(messages []message.Message, contextWindow int) []message.Message {
	if contextWindow <= 0 {
		return append([]message.Message(nil), messages...)
	}
	limit := messageBudget(contextWindow)
	if len(messages) <= 2 {
		return append([]message.Message(nil), messages...)
	}

	latestUser := -1
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == message.RoleUser {
			latestUser = i
			break
		}
	}

	outReverse := make([]message.Message, 0, len(messages))
	total := 0
	for i := len(messages) - 1; i >= 0; i-- {
		tokens := estimateMessageTokens(messages[i])
		keep := total+tokens <= limit || i == latestUser
		if keep {
			outReverse = append(outReverse, messages[i])
			total += tokens
		}
	}
	out := make([]message.Message, 0, len(outReverse))
	for i := len(outReverse) - 1; i >= 0; i-- {
		out = append(out, outReverse[i])
	}
	return out
}

// messageBudget returns the token budget available for conversation messages
// given a context window, reserving headroom for the model's response. It
// mirrors the historical truncateForContext math: subtract the reserved
// response tokens, but fall back to the full window when the reservation would
// leave an implausibly small budget (a sign of a tiny, likely test, window).
func messageBudget(contextWindow int) int {
	limit := contextWindow - reservedResponseTokens
	if limit < 1024 {
		limit = contextWindow
	}
	return limit
}

// fitBudget returns the token budget available for conversation messages once
// both the reserved response headroom and the system prompt (which the provider
// sends alongside the messages but outside the history) are accounted for. It
// is used by the automatic-compaction path to decide whether a history fits the
// window. The returned budget may be non-positive when the system prompt alone
// crowds out the window; callers treat a non-positive budget as "nothing fits".
func fitBudget(contextWindow int, systemPrompt string) int {
	return messageBudget(contextWindow) - estimateTextTokens(systemPrompt)
}

// historyTokens estimates the total tokens a history occupies on the wire.
func historyTokens(messages []message.Message) int {
	total := 0
	for _, msg := range messages {
		total += estimateMessageTokens(msg)
	}
	return total
}

// fitsBudget reports whether messages fit within budget tokens. A non-positive
// budget never fits a non-empty history.
func fitsBudget(messages []message.Message, budget int) bool {
	if len(messages) == 0 {
		return true
	}
	return historyTokens(messages) <= budget
}

func estimateMessageTokens(msg message.Message) int {
	data, err := json.Marshal(msg.Content)
	if err != nil {
		return 256
	}
	n := len(data) / 4
	if n < 1 {
		return 1
	}
	return n
}

// estimateTextTokens estimates the tokens occupied by a raw text string, using
// the same ~4-bytes-per-token heuristic as estimateMessageTokens. An empty
// string costs zero tokens.
func estimateTextTokens(s string) int {
	if s == "" {
		return 0
	}
	n := len(s) / 4
	if n < 1 {
		return 1
	}
	return n
}
