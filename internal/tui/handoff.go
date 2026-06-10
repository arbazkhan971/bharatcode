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
// "review before sending" guidance lives in the Handoff dialog, not in the
// draft body: everything in the draft is addressed to the successor session,
// so an unedited draft sends clean rather than carrying editorial footers the
// model would have to puzzle over.
//
// The structured format intentionally mirrors the fields a successor session
// needs to continue work without the full history:
//   - Goal / first prompt: what the session set out to do
//   - Changed files:       how many files were touched (a quick scope signal)
//   - Recent context:      the last few turns so the successor sees current
//     state; a turn that merely repeats the goal verbatim is skipped so a
//     short session does not say the same thing twice
//   - Next steps:          a placeholder the user fills in before sending
//
// The function never fails; a session with no first prompt or messages still
// returns a useful skeleton.
func buildHandoffDraft(firstPrompt string, changedFiles int, msgs []message.Message) string {
	var b strings.Builder

	b.WriteString("## Handoff\n\n")

	// Goal / first prompt. realGoal keeps the actual prompt (empty when none
	// was recorded) for the dedup check below; the placeholder is display-only.
	realGoal := strings.TrimSpace(firstPrompt)
	goal := realGoal
	if goal == "" {
		goal = "(not recorded — fill in the original goal)"
	}
	b.WriteString(fmt.Sprintf("**Goal:** %s\n\n", goal))

	// Scope signal
	if changedFiles > 0 {
		b.WriteString(fmt.Sprintf("**Files changed this session:** %d\n\n", changedFiles))
	}

	// Recent context — last N turns, text content only. A header is only
	// written once a turn survives the filters, so a tail that is entirely
	// goal-repeats leaves no dangling "Recent context:" heading.
	var context strings.Builder
	for _, m := range recentTurns(msgs, handoffRecentLimit) {
		role := "User"
		if m.Role == message.RoleAssistant {
			role = "Assistant"
		}
		text := firstTextBlock(m)
		if text == "" {
			continue
		}
		// A turn that merely repeats the goal verbatim (typically the session's
		// first prompt sampled back out of the transcript) adds bytes without
		// adding information — the Goal line above already carries it.
		if realGoal != "" && strings.TrimSpace(text) == realGoal {
			continue
		}
		// Indent continuation lines so the block stays visually contained.
		indented := strings.ReplaceAll(strings.TrimSpace(text), "\n", "\n  ")
		context.WriteString(fmt.Sprintf("- **%s:** %s\n", role, indented))
	}
	if context.Len() > 0 {
		b.WriteString("**Recent context:**\n")
		b.WriteString(context.String())
		b.WriteString("\n")
	}

	b.WriteString("**Next steps:** (fill in before sending)\n")

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
