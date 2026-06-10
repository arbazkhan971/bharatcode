package tui

import (
	"fmt"
	"strings"

	"github.com/arbazkhan971/bharatcode/internal/message"
)

// handoffRecentLimit is the maximum number of recent messages sampled when
// building the handoff draft. Sampling the tail keeps the draft concise;
// the full history is in the session and can be consulted separately.
const handoffRecentLimit = 6

// buildHandoffDraft assembles a self-contained handoff prompt from the
// fields the TUI model already tracks without calling the agent loop. The
// draft is deliberately marked as a draft so the user knows to review and
// refine it before sending.
//
// The structured format intentionally mirrors the fields a successor session
// needs to continue work without the full history:
//   - Goal / first prompt: what the session set out to do
//   - Changed files:       how many files were touched (a quick scope signal)
//   - Recent context:      the last few turns so the successor sees current state
//   - Next steps:          a placeholder the user fills in before sending
//
// The function never fails; a session with no first prompt or messages still
// returns a useful skeleton.
func buildHandoffDraft(firstPrompt string, changedFiles int, msgs []message.Message) string {
	var b strings.Builder

	b.WriteString("## Handoff\n\n")

	// Goal / first prompt
	goal := strings.TrimSpace(firstPrompt)
	if goal == "" {
		goal = "(not recorded — fill in the original goal)"
	}
	b.WriteString(fmt.Sprintf("**Goal:** %s\n\n", goal))

	// Scope signal
	if changedFiles > 0 {
		b.WriteString(fmt.Sprintf("**Files changed this session:** %d\n\n", changedFiles))
	}

	// Recent context — last N turns, text content only
	tail := recentTurns(msgs, handoffRecentLimit)
	if len(tail) > 0 {
		b.WriteString("**Recent context:**\n")
		for _, m := range tail {
			role := "User"
			if m.Role == message.RoleAssistant {
				role = "Assistant"
			}
			text := firstTextBlock(m)
			if text == "" {
				continue
			}
			// Indent continuation lines so the block stays visually contained.
			indented := strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ")
			b.WriteString(fmt.Sprintf("- **%s:** %s\n", role, indented))
		}
		b.WriteString("\n")
	}

	b.WriteString("**Next steps:** (fill in before sending)\n\n")
	b.WriteString("---\n")
	b.WriteString("*Review and refine this draft, then send it to start the focused session.*\n")

	return b.String()
}

// recentTurns returns up to limit messages from the tail of msgs, keeping
// only user and assistant turns and skipping empty ones.
func recentTurns(msgs []message.Message, limit int) []message.Message {
	var turns []message.Message
	for _, m := range msgs {
		if m.Role != message.RoleUser && m.Role != message.RoleAssistant {
			continue
		}
		if firstTextBlock(m) == "" {
			continue
		}
		turns = append(turns, m)
	}
	if len(turns) <= limit {
		return turns
	}
	return turns[len(turns)-limit:]
}

// firstTextBlock returns the text of the first TextBlock content block in m,
// or an empty string if none is present. It is used to extract a concise
// snippet for the handoff draft without including tool-call JSON blobs.
func firstTextBlock(m message.Message) string {
	for _, b := range m.Content {
		if tb, ok := b.(message.TextBlock); ok && strings.TrimSpace(tb.Text) != "" {
			return tb.Text
		}
	}
	return ""
}
